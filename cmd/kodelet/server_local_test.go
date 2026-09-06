package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func localServerTestState(t *testing.T) string {
	t.Helper()
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range previous {
			viper.Set(key, value)
		}
	})
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	for _, name := range []string{controlPlaneServerEnv, controlPlaneAuthTokenEnv, configFileEnv, configFileModeEnv} {
		t.Setenv(name, "")
	}
	directory, err := localServerDirectory()
	require.NoError(t, err)
	return directory
}

func forbidLocalServerSpawn(t *testing.T) {
	t.Helper()
	previous := detachedServerCommand
	detachedServerCommand = func() (*exec.Cmd, error) {
		assert.Fail(t, "must not spawn a local daemon")
		return nil, errors.New("unexpected local startup")
	}
	t.Cleanup(func() { detachedServerCommand = previous })
}

func TestLocalServerStateAndAuthentication(t *testing.T) {
	directory := localServerTestState(t)
	lock, err := tryLocalServerLock(directory, "server.lock")
	require.NoError(t, err)
	require.NotNil(t, lock)
	defer lock.Close()
	require.NoError(t, publishLocalServer(directory, "http://127.0.0.1:43210", "private-token", "instance", true))
	connection, err := readLocalServerConnection(directory)
	require.NoError(t, err)
	assert.Equal(t, "instance", connection.InstanceID)
	assert.Equal(t, os.Getpid(), connection.PID)
	data, err := os.ReadFile(filepath.Join(directory, "connection.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "private-token")
	for _, name := range []string{"connection.json", "client-token", "server.lock"} {
		info, err := os.Stat(filepath.Join(directory, name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	info, err := os.Stat(directory)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	other, err := tryLocalServerLock(directory, "server.lock")
	require.NoError(t, err)
	assert.Nil(t, other)
	cmd := &cobra.Command{Use: "run"}
	addRemoteRunFlags(cmd)
	server, explicit := serverFlagOrConfig(cmd)
	assert.False(t, explicit)
	assert.Equal(t, connection.URL, server)
	token, source, err := resolveControlPlaneAuthToken(cmd, server)
	require.NoError(t, err)
	assert.Equal(t, "private-token", token)
	assert.Equal(t, "local-server", source)
	t.Setenv(controlPlaneAuthTokenEnv, "environment-token")
	token, _, err = resolveControlPlaneAuthToken(cmd, server)
	require.NoError(t, err)
	assert.Equal(t, "environment-token", token)
	require.NoError(t, cmd.Flags().Set("auth-token", "flag-token"))
	token, _, err = resolveControlPlaneAuthToken(cmd, server)
	require.NoError(t, err)
	assert.Equal(t, "flag-token", token)
}

func TestPrepareClientExplicitServerNeverStartsLocalDaemon(t *testing.T) {
	for _, source := range []string{"flag", "environment", "configuration"} {
		t.Run(source, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			cmd := &cobra.Command{Use: "run"}
			addRemoteRunFlags(cmd)
			const endpoint = "http://127.0.0.1:1"
			switch source {
			case "flag":
				require.NoError(t, cmd.Flags().Set("server", endpoint))
			case "environment":
				t.Setenv(controlPlaneServerEnv, endpoint)
			case "configuration":
				viper.Set("server", endpoint)
			}
			require.NoError(t, cmd.Flags().Set("auth-token", "explicit"))
			server, token, err := prepareClientServer(t.Context(), cmd)
			require.NoError(t, err)
			assert.Equal(t, endpoint, server)
			assert.Equal(t, "explicit", token)
			assert.NoDirExists(t, directory)
		})
	}
}

func TestEnsureLocalServerReusesAndWaitsForRunner(t *testing.T) {
	directory := localServerTestState(t)
	forbidLocalServerSpawn(t)
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer private", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/status", r.URL.Path)
		_ = json.NewEncoder(w).Encode(localServerStatus{APIReady: true, InstanceID: "same", EmbeddedRunner: controlplane.EmbeddedRunnerStatus{Enabled: true, Ready: probes.Add(1) >= 3}})
	}))
	defer server.Close()
	lock, err := tryLocalServerLock(directory, "server.lock")
	require.NoError(t, err)
	require.NotNil(t, lock)
	defer lock.Close()
	require.NoError(t, publishLocalServer(directory, server.URL, "private", "same", true))
	connection, err := ensureLocalServer(t.Context(), io.Discard)
	require.NoError(t, err)
	assert.Equal(t, server.URL, connection.URL)
	assert.GreaterOrEqual(t, probes.Load(), int32(3))
}

func TestEnsureLocalServerNeverReplacesUnhealthyServer(t *testing.T) {
	for _, test := range []struct {
		name, body, expected string
		code                 int
	}{
		{"unauthorized", "", "HTTP 401", 401},
		{"server failure", "", "HTTP 500", 500},
		{"redirect", "", "HTTP 302", 302},
		{"invalid response", "not json", "invalid local server status", 200},
		{"wrong instance", `{"instanceId":"other"}`, "does not match", 200},
		{"runner failure", `{"instanceId":"same","apiReady":true,"embeddedRunner":{"enabled":true,"error":"workspace locked"}}`, "workspace locked", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.code)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			lock, err := tryLocalServerLock(directory, "server.lock")
			require.NoError(t, err)
			require.NotNil(t, lock)
			defer lock.Close()
			require.NoError(t, publishLocalServer(directory, server.URL, "private", "same", true))
			_, err = ensureLocalServer(t.Context(), io.Discard)
			assert.ErrorContains(t, err, test.expected)
		})
	}
}

func TestEnsureLocalServerRejectsUnownedEndpointAndConfigurationMismatch(t *testing.T) {
	for _, reason := range []string{"unowned endpoint", "configuration file", "configuration mode", "version"} {
		t.Run(reason, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(localServerStatus{APIReady: true, InstanceID: "same", EmbeddedRunner: controlplane.EmbeddedRunnerStatus{Enabled: true, Ready: true}})
			}))
			defer server.Close()
			lock, err := tryLocalServerLock(directory, "server.lock")
			require.NoError(t, err)
			require.NotNil(t, lock)
			defer lock.Close()
			require.NoError(t, publishLocalServer(directory, server.URL, "private", "same", true))
			connection, err := readLocalServerConnection(directory)
			require.NoError(t, err)
			expected := "different configuration file"
			switch reason {
			case "unowned endpoint":
				require.NoError(t, lock.Close())
				expected = "refusing to start a competing server"
			case "configuration file":
				connection.ConfigFile = filepath.Join(t.TempDir(), "other.yaml")
			case "configuration mode":
				connection.ConfigMode = configFileModeIsolate
			case "version":
				connection.Version = "different-version"
				expected = "different Kodelet version"
			}
			data, err := json.Marshal(connection)
			require.NoError(t, err)
			require.NoError(t, writeLocalServerFile(directory, "connection.json", data))
			_, err = ensureLocalServer(t.Context(), io.Discard)
			assert.ErrorContains(t, err, expected)
		})
	}
}

func TestEnsureLocalServerReportsEarlyExitAndTimeout(t *testing.T) {
	t.Run("early exit", func(t *testing.T) {
		directory := localServerTestState(t)
		previous := detachedServerCommand
		detachedServerCommand = func() (*exec.Cmd, error) {
			return exec.Command("/bin/sh", "-c", "echo startup-failure >&2; exit 2"), nil
		}
		t.Cleanup(func() { detachedServerCommand = previous })
		_, err := ensureLocalServer(t.Context(), io.Discard)
		require.ErrorContains(t, err, "exited during startup")
		data, err := os.ReadFile(filepath.Join(directory, "server.log"))
		require.NoError(t, err)
		assert.Contains(t, string(data), "startup-failure")
	})
	t.Run("startup timeout", func(t *testing.T) {
		directory := localServerTestState(t)
		forbidLocalServerSpawn(t)
		lock, err := tryLocalServerLock(directory, "server.lock")
		require.NoError(t, err)
		require.NotNil(t, lock)
		defer lock.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		_, err = ensureLocalServer(ctx, io.Discard)
		assert.ErrorContains(t, err, "did not become ready")
	})
	t.Run("cancelled", func(t *testing.T) {
		directory := localServerTestState(t)
		forbidLocalServerSpawn(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := ensureLocalServer(ctx, io.Discard)
		assert.ErrorIs(t, err, context.Canceled)
		assert.NoDirExists(t, directory)
	})
}

func TestPrepareLocalServeConfig(t *testing.T) {
	localServerTestState(t)
	config := NewServeConfig()
	require.NoError(t, prepareLocalServeConfig(config))
	assert.Equal(t, os.Getenv("HOME"), config.RunnerWorkspace)
	for _, mutate := range []func(*ServeConfig){
		func(c *ServeConfig) { c.Host = "0.0.0.0" },
		func(c *ServeConfig) { c.SkipAuth = true },
		func(c *ServeConfig) { c.EmbeddedRunner = false },
		func(c *ServeConfig) { c.WebAuthMode = controlplane.WebAuthModeOIDC },
		func(c *ServeConfig) { c.RunnerAuthMode = controlplane.RunnerAuthModeNone },
	} {
		config := NewServeConfig()
		mutate(config)
		assert.Error(t, prepareLocalServeConfig(config))
	}
}
