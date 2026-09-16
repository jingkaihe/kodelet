package main

import (
	"bytes"
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

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/tui"
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
	require.NoError(t, publishLocalServer(directory, "http://127.0.0.1:43210", "private-token", "instance", true, ""))
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

func TestReadLocalServerConnectionRequiresLoopbackEndpoint(t *testing.T) {
	directory := localServerTestState(t)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	for _, test := range []struct {
		endpoint string
		valid    bool
	}{
		{"http://localhost:8080", true},
		{"http://LOCALHOST.:8080", true},
		{"http://127.0.0.1:8080", true},
		{"http://127.0.0.2:8080", true},
		{"http://[::1]:8080", true},
		{"http://localhost.example:8080", false},
		{"http://0.0.0.0:8080", false},
		{"http://[::]:8080", false},
		{"http://192.0.2.1:8080", false},
		{"http://localhost", false},
		{"https://localhost:8080", false},
		{"http://user@localhost:8080", false},
		{"http://localhost:8080/path", false},
		{"http://localhost:8080?query=value", false},
		{"http://localhost:8080#fragment", false},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			require.NoError(t, publishLocalServer(directory, test.endpoint, "private-token", "instance", true, ""))
			connection, err := readLocalServerConnection(directory)
			if !test.valid {
				require.ErrorContains(t, err, "expected a loopback HTTP address")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.endpoint, connection.URL)
		})
	}
}

func TestPrepareClientExplicitServerNeverStartsLocalDaemon(t *testing.T) {
	for _, source := range []string{"flag", "environment", "configuration"} {
		t.Run(source, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			// Explicit endpoints must remain connect-only even with a local
			// OIDC serve policy that would otherwise enable automatic startup.
			viper.Set("serve", map[string]any{"host": "0.0.0.0", "web_auth_mode": "oidc", "runner_auth_mode": "enrollment"})
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

func TestPrepareClientSavedLoginWithoutServeConfigRemainsConnectOnly(t *testing.T) {
	for _, state := range []string{"valid", "expired"} {
		t.Run(state, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			bearer := controlPlaneAuthTestBearer(0x91)
			store, err := userauth.NewStore()
			require.NoError(t, err)
			expiry := time.Now().Add(time.Hour)
			if state == "expired" {
				expiry = time.Now().Add(-time.Hour)
			}
			require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(defaultRunnerServer, "saved-login", bearer, controlPlaneAuthTestPrincipal("user", "user@example.com"), expiry)))
			server, token, err := prepareClientServer(t.Context(), remoteRunCommandForTest())
			assert.Equal(t, defaultRunnerServer, server)
			if state == "expired" {
				require.ErrorContains(t, err, "kodelet auth login --server")
				assert.NotContains(t, err.Error(), bearer)
			} else {
				require.NoError(t, err)
				assert.Equal(t, bearer, token)
			}
			assert.NoDirExists(t, directory, "connect-only clients must not create local lifecycle state")
		})
	}
}

func TestOIDCClientsReuseSavedLoginWithoutLocalDiscovery(t *testing.T) {
	for _, client := range []string{"chat", "run", "acp"} {
		t.Run(client, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			// An existing operator-owned OIDC server, without local serve
			// configuration or discovery state, remains connect-only.
			lock, err := tryLocalServerLock(directory, "server.lock")
			require.NoError(t, err)
			require.NotNil(t, lock)
			defer lock.Close()
			bearer := controlPlaneAuthTestBearer(0x92)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "Bearer "+bearer, r.Header.Get("Authorization"))
				switch r.URL.Path {
				case "/api/chat/settings":
					_ = json.NewEncoder(w).Encode(chat.ControlPlaneChatSettings{CurrentProfile: "default", DefaultRunnerID: "runner", DefaultRunnerReady: true})
				case "/api/chat/cwd-suggestions":
					assert.Equal(t, http.MethodGet, r.Method)
					assert.Equal(t, "runner", r.URL.Query().Get("runnerId"))
					assert.False(t, r.URL.Query().Has("options"))
					_ = json.NewEncoder(w).Encode(protocol.WorkspaceCWDHintsResult{BaseDir: "/oidc-workspace"})
				case "/api/chat/slash-commands":
					_ = json.NewEncoder(w).Encode(protocol.WorkspaceDiscoverResult{CWD: "/oidc-workspace"})
				case "/api/chat":
					w.Header().Set("Content-Type", "application/x-ndjson")
					_ = json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "result", Result: new("OIDC result")})
					_ = json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "done"})
				default:
					assert.Fail(t, "unexpected client endpoint", "%s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			store, err := userauth.NewStore()
			require.NoError(t, err)
			require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(server.URL, "saved-login", bearer, controlPlaneAuthTestPrincipal("user", "user@example.com"), time.Now().Add(time.Hour))))
			cmd := remoteRunCommandForTest()
			cmd.Use = client
			cmd.Flags().String("theme", tui.AutoThemeName, "")
			// Replace only the default URL so the OS can assign a free test
			// port; --server, environment, and config remain unset.
			require.NoError(t, cmd.Flags().Lookup("server").Value.Set(server.URL))
			require.False(t, cmd.Flags().Changed("server"))
			var output, diagnostics bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&diagnostics)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			cmd.SetContext(ctx)
			switch client {
			case "chat":
				config, err := prepareDaemonChat(ctx, cmd)
				require.NoError(t, err)
				assert.Equal(t, "/oidc-workspace", config.CWD)
			case "run":
				require.NoError(t, cmd.Flags().Set("result-only", "true"))
				require.NoError(t, runControlPlaneCommand(cmd, []string{"hello"}))
				assert.Equal(t, "OIDC result\n", output.String())
			case "acp":
				config, err := remoteACPSessionConfig(ctx, cmd, server.URL)
				require.NoError(t, err)
				remote, _, err := config.Provider.WaitForRemoteChat(ctx)
				require.NoError(t, err)
				_, err = remote.DiscoverWorkspace(ctx, chat.WorkspaceTarget{RunnerID: "runner"})
				require.NoError(t, err)
			}
			assert.Positive(t, calls.Load())
			assert.Empty(t, diagnostics.String())
			assert.NoFileExists(t, filepath.Join(directory, "startup.lock"))
			assert.NoFileExists(t, filepath.Join(directory, "connection.json"))
			assert.NoFileExists(t, filepath.Join(directory, "client-token"))
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
	require.NoError(t, publishLocalServer(directory, server.URL, "private", "same", true, ""))
	connection, err := ensureLocalServer(t.Context(), io.Discard)
	require.NoError(t, err)
	assert.Equal(t, server.URL, connection.URL)
	assert.GreaterOrEqual(t, probes.Load(), int32(3))
}

func TestEnsureLocalServerReusesWithoutEmbeddedRunner(t *testing.T) {
	directory := localServerTestState(t)
	forbidLocalServerSpawn(t)
	endpoint := publishLocalServerWithRunner(t, directory, controlplane.EmbeddedRunnerStatus{})
	connection, err := ensureLocalServer(t.Context(), io.Discard)
	require.NoError(t, err)
	assert.Equal(t, endpoint, connection.URL)
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
			require.NoError(t, publishLocalServer(directory, server.URL, "private", "same", true, ""))
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
			require.NoError(t, publishLocalServer(directory, server.URL, "private", "same", true, ""))
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
	config.PublicBaseURL = "https://images.example.com"
	require.NoError(t, prepareLocalServeConfig(config))
	assert.Equal(t, os.Getenv("HOME"), config.RunnerWorkspace)
	assert.Equal(t, "https://images.example.com", config.PublicBaseURL)
	for _, host := range []string{"localhost", "0.0.0.0"} {
		t.Run(host, func(t *testing.T) {
			config := NewServeConfig()
			configureValidOIDC(config, "unused-secret-file")
			config.RunnerAuthMode = controlplane.RunnerAuthModeEnrollment
			config.Host = host
			config.Port = 8765
			require.NoError(t, prepareLocalServeConfig(config))
			assert.Equal(t, host, config.Host, "background startup must preserve the configured bind address")
			assert.Equal(t, 8765, config.Port)
			assert.Equal(t, controlplane.WebAuthModeOIDC, config.WebAuthMode)
			assert.Equal(t, controlplane.RunnerAuthModeEnrollment, config.RunnerAuthMode)
			assert.Empty(t, config.AuthToken, "OIDC must not acquire a public compatibility credential")
		})
	}
	for _, mode := range []controlplane.RunnerAuthMode{controlplane.RunnerAuthModeEnrollment, controlplane.RunnerAuthModeToken} {
		t.Run("external runners with "+string(mode), func(t *testing.T) {
			config := NewServeConfig()
			config.RunnerAuthMode = mode
			config.EmbeddedRunner = false
			if mode == controlplane.RunnerAuthModeToken {
				require.ErrorContains(t, prepareLocalServeConfig(config), "requires serve.runner_auth_token")
				config.RunnerAuthToken = "configured-runner-token"
			}
			require.NoError(t, prepareLocalServeConfig(config))
			assert.False(t, config.EmbeddedRunner)
			assert.Empty(t, config.RunnerWorkspace)
		})
	}
	for _, test := range []struct {
		name, expected string
		mutate         func(*ServeConfig)
	}{
		{"non-loopback interface", "loopback or wildcard host", func(c *ServeConfig) { c.Host = "192.0.2.1" }},
		{"skip auth", "requires authentication", func(c *ServeConfig) { c.SkipAuth = true }},
		{"incomplete OIDC", "OIDC client secret file is required", func(c *ServeConfig) { c.WebAuthMode = controlplane.WebAuthModeOIDC }},
		{"no web auth", "requires authentication", func(c *ServeConfig) { c.WebAuthMode = controlplane.WebAuthModeNone }},
		{"no runner auth", "requires authentication", func(c *ServeConfig) { c.RunnerAuthMode = controlplane.RunnerAuthModeNone }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := NewServeConfig()
			test.mutate(config)
			assert.ErrorContains(t, prepareLocalServeConfig(config), test.expected)
		})
	}
}

func TestLocalServerHost(t *testing.T) {
	for host, expected := range map[string]string{
		"localhost": "localhost", "LOCALHOST.": "LOCALHOST.", "127.0.0.2": "127.0.0.2", "::1": "::1",
		"0.0.0.0": "127.0.0.1", "::": "::1", "192.0.2.1": "", "example.com": "",
	} {
		assert.Equal(t, expected, localServerHost(host), host)
	}
}
