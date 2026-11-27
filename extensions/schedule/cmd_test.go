package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCLI struct {
	extension *extensionSettings
	daemon    *daemonSettings
	out       bytes.Buffer
}

func newTestCLI(t *testing.T) *testCLI {
	t.Helper()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	for _, key := range []string{"CONFIG", "DB", "AUTO_APPROVE", "SERVER", "KODELET", "POLL_INTERVAL", "CONCURRENCY", "RUN_TIMEOUT", "MISFIRE_GRACE"} {
		t.Setenv("KODELET_SCHEDULE_"+key, "")
	}
	return &testCLI{}
}

// Build a fresh command each time, as if invoked from a new process.
func (tc *testCLI) execute(t *testing.T, args ...string) error {
	t.Helper()
	*tc = testCLI{}
	c := newCLI()
	c.runExtension = func(_ context.Context, settings extensionSettings) error {
		tc.extension = &settings
		return nil
	}
	c.runDaemon = func(_ context.Context, settings daemonSettings, _ *slog.Logger) error {
		tc.daemon = &settings
		return nil
	}
	root := c.rootCommand()
	root.SetArgs(append([]string{}, args...))
	root.SetOut(&tc.out)
	root.SetErr(io.Discard)
	return root.ExecuteContext(t.Context())
}

func TestExtensionDefaultsAndLocalConfig(t *testing.T) {
	tc := newTestCLI(t)
	dir := filepath.Join(os.Getenv("KODELET_BASE_PATH"), "schedule")
	require.NoError(t, tc.execute(t))
	require.NotNil(t, tc.extension)
	assert.Equal(t, filepath.Join(dir, "schedule.db"), tc.extension.DBPath)
	assert.False(t, tc.extension.AutoApprove)
	assert.Empty(t, tc.out.String(), "stdout is reserved for the protocol")

	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
db: ~/shared.db
auto_approve: true
`), 0o600))
	require.NoError(t, tc.execute(t))
	require.NotNil(t, tc.extension)
	assert.Equal(t, filepath.Join(home, "shared.db"), tc.extension.DBPath)
	assert.True(t, tc.extension.AutoApprove)

	t.Setenv("KODELET_SCHEDULE_AUTO_APPROVE", "sometimes")
	assert.ErrorContains(t, tc.execute(t), "invalid auto_approve")
	assert.Nil(t, tc.extension, "invalid approval settings must not start the extension")
}

func TestDaemonSettingsPrecedence(t *testing.T) {
	tc := newTestCLI(t)
	config := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(config, []byte(`
db: from-config.db
server: https://config.example
poll_interval: 10s
concurrency: 3
`), 0o600))
	t.Setenv("KODELET_SCHEDULE_POLL_INTERVAL", "20s")
	t.Setenv("KODELET_SCHEDULE_CONCURRENCY", "4")
	t.Chdir(t.TempDir())
	require.NoError(t, tc.execute(t, "daemon", "--config", config, "--concurrency", "5"))

	require.NotNil(t, tc.daemon)
	assert.Equal(t, filepath.Join(os.Getenv("KODELET_BASE_PATH"), "schedule", "from-config.db"), tc.daemon.DBPath)
	assert.Equal(t, "https://config.example", tc.daemon.Server)
	assert.Equal(t, 20*time.Second, tc.daemon.Config.PollInterval, "environment overrides config")
	assert.Equal(t, 5, tc.daemon.Config.Concurrency, "flag overrides environment")
}

func TestDaemonRejectsInvalidSettings(t *testing.T) {
	for _, tt := range []struct {
		args             []string
		env, value, want string
	}{
		{args: []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}, want: "failed to read config file"},
		{env: "POLL_INTERVAL", value: "soon", want: "invalid poll_interval"},
		{env: "CONCURRENCY", value: "many", want: "invalid concurrency"},
		{args: []string{"--concurrency", "0"}, want: "concurrency must be at least 1"},
		{args: []string{"--misfire-grace", "1s"}, want: "misfire grace"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			tc := newTestCLI(t)
			if tt.env != "" {
				t.Setenv("KODELET_SCHEDULE_"+tt.env, tt.value)
			}
			assert.ErrorContains(t, tc.execute(t, append([]string{"daemon"}, tt.args...)...), tt.want)
			assert.Nil(t, tc.daemon)
		})
	}
}

func TestListCommandUsesHostWideDatabase(t *testing.T) {
	store, path := newTestStore(t)
	next := time.Now().Add(time.Hour)
	createTestSchedule(t, store, "runner-a", "daily", next)
	createTestSchedule(t, store, "runner-b", "weekly", next)
	tc := newTestCLI(t)
	t.Setenv("KODELET_SCHEDULE_DB", path)

	require.NoError(t, tc.execute(t, "list"))
	assert.Contains(t, tc.out.String(), "runner-a")
	assert.Contains(t, tc.out.String(), "runner-b")
	assert.Contains(t, tc.out.String(), "Daemon: not started")

	require.NoError(t, tc.execute(t, "list", "--json"))
	var decoded struct {
		Schedules []Schedule   `json:"schedules"`
		Daemon    daemonHealth `json:"daemon"`
	}
	require.NoError(t, json.Unmarshal(tc.out.Bytes(), &decoded))
	require.Len(t, decoded.Schedules, 2)
	assert.ElementsMatch(t, []string{"runner-a", "runner-b"}, []string{decoded.Schedules[0].Workspace, decoded.Schedules[1].Workspace})
	assert.Equal(t, daemonNotStarted, decoded.Daemon.State)
}
