package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCLI captures what commands would run instead of running it.
type testCLI struct {
	*cli
	extensionDB string
	daemon      *daemonSettings
	out, err    bytes.Buffer
}

func newTestCLI(t *testing.T) *testCLI {
	t.Helper()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	for _, key := range []string{"CONFIG", "DB", "SERVER", "KODELET", "POLL_INTERVAL", "CONCURRENCY", "RUN_TIMEOUT", "MISFIRE_GRACE", "SHUTDOWN_GRACE"} {
		t.Setenv("KODELET_SCHEDULE_"+key, "")
		os.Unsetenv("KODELET_SCHEDULE_" + key)
	}
	tc := &testCLI{cli: newCLI()}
	tc.runExtension = func(_ context.Context, dbPath string) error {
		tc.extensionDB = dbPath
		return nil
	}
	tc.runDaemon = func(_ context.Context, settings daemonSettings, _ *slog.Logger) error {
		tc.daemon = &settings
		return nil
	}
	return tc
}

func (tc *testCLI) execute(t *testing.T, args ...string) error {
	t.Helper()
	root := tc.rootCommand()
	root.SetArgs(args)
	root.SetOut(&tc.out)
	root.SetErr(&tc.err)
	return root.ExecuteContext(t.Context())
}

func TestRootServesExtensionWithDefaultDatabase(t *testing.T) {
	tc := newTestCLI(t)

	require.NoError(t, tc.execute(t))

	base := os.Getenv("KODELET_BASE_PATH")
	assert.Equal(t, filepath.Join(base, "schedule", "schedule.db"), tc.extensionDB)
	assert.Empty(t, tc.out.String(), "stdout is reserved for the protocol")
}

func TestRootRejectsUnknownCommands(t *testing.T) {
	tc := newTestCLI(t)

	err := tc.execute(t, "bogus")

	assert.ErrorContains(t, err, "unknown command")
	assert.Empty(t, tc.extensionDB)
}

func TestDaemonSettingsPrecedence(t *testing.T) {
	tc := newTestCLI(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(config, []byte(`
db: from-config.db
server: https://config.example
poll_interval: 10s
concurrency: 3
run_timeout: 2h
`), 0o600))
	t.Setenv("KODELET_SCHEDULE_POLL_INTERVAL", "20s")
	t.Setenv("KODELET_SCHEDULE_CONCURRENCY", "4")

	require.NoError(t, tc.execute(t, "daemon", "--config", config, "--concurrency", "5"))

	require.NotNil(t, tc.daemon)
	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wd, "from-config.db"), tc.daemon.DBPath)
	assert.Equal(t, "https://config.example", tc.daemon.Server, "config file")
	assert.Equal(t, 20*time.Second, tc.daemon.Config.PollInterval, "environment overrides config")
	assert.Equal(t, 5, tc.daemon.Config.Concurrency, "flag overrides environment")
	assert.Equal(t, 2*time.Hour, tc.daemon.Config.RunTimeout)
	assert.Equal(t, time.Hour, tc.daemon.Config.MisfireGrace, "default")
	assert.Equal(t, "kodelet", tc.daemon.Kodelet, "default")
}

func TestDefaultConfigFileIsOptional(t *testing.T) {
	tc := newTestCLI(t)
	require.NoError(t, tc.execute(t, "daemon"))
	assert.Equal(t, defaultDaemonConfig(), tc.daemon.Config)

	base := os.Getenv("KODELET_BASE_PATH")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "schedule"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(base, "schedule", "config.yaml"), []byte("concurrency: 7\ndb: /tmp/x.db\n"), 0o600))
	tc = newTestCLI(t)
	t.Setenv("KODELET_BASE_PATH", base)
	require.NoError(t, tc.execute(t))
	assert.Equal(t, "/tmp/x.db", tc.extensionDB, "the extension reads the default config file too")
	require.NoError(t, tc.execute(t, "daemon"))
	assert.Equal(t, 7, tc.daemon.Config.Concurrency)
}

func TestDaemonRejectsInvalidSettings(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		args []string
		err  string
	}{
		"missing config":      {args: []string{"--config", "/does/not/exist.yaml"}, err: "failed to read config file"},
		"bad duration":        {env: map[string]string{"KODELET_SCHEDULE_POLL_INTERVAL": "soon"}, err: "invalid poll_interval"},
		"bad concurrency":     {env: map[string]string{"KODELET_SCHEDULE_CONCURRENCY": "many"}, err: "invalid concurrency"},
		"failed validation":   {args: []string{"--concurrency", "0"}, err: "concurrency must be at least 1"},
		"grace below poll":    {args: []string{"--misfire-grace", "1s"}, err: "misfire grace"},
		"unexpected argument": {args: []string{"extra"}, err: "unknown command"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tc := newTestCLI(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			err := tc.execute(t, append([]string{"daemon"}, tt.args...)...)
			assert.ErrorContains(t, err, tt.err)
			assert.Nil(t, tc.daemon)
		})
	}
}

func TestVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		tc := newTestCLI(t)
		require.NoError(t, tc.execute(t, args...))
		assert.Equal(t, "kodelet-extension-schedule "+version+"\n", tc.out.String())
	}
}

func TestListCommandUsesConfiguredDatabase(t *testing.T) {
	store, path := newTestStore(t)
	createTestSchedule(t, store, "runner-a", "daily", time.Now().Add(time.Hour))
	tc := newTestCLI(t)
	t.Setenv("KODELET_SCHEDULE_DB", path)

	require.NoError(t, tc.execute(t, "list", "--json"))

	var decoded struct {
		Schedules []Schedule `json:"schedules"`
	}
	require.NoError(t, json.Unmarshal(tc.out.Bytes(), &decoded))
	require.Len(t, decoded.Schedules, 1)
	assert.Equal(t, "runner-a", decoded.Schedules[0].Workspace)
}
