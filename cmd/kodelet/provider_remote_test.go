package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteProviderAndProfileProcessesNeverUseClientState(t *testing.T) {
	for _, test := range []struct {
		name        string
		args        []string
		input, want string
		wantError   bool
		calls       int
	}{
		{"accounts", []string{"anthropic", "accounts", "list"}, "", "* work", false, 1},
		{"default", []string{"anthropic", "accounts", "default"}, "", "Default account: work", false, 1},
		{"set-default", []string{"anthropic", "accounts", "default", "work"}, "", "Account updated", false, 1},
		{"rename", []string{"anthropic", "accounts", "rename", "work", "renamed"}, "", "Account updated", false, 1},
		{"remove", []string{"anthropic", "accounts", "remove", "work"}, "", "Account updated", false, 1},
		{"usage", []string{"anthropic", "accounts", "usage", "work", "--json"}, "", `"utilization": 0.25`, false, 1},
		{"logout", []string{"anthropic", "logout", "--no-confirm"}, "", "accounts removed", false, 1},
		{"decline-logout", []string{"anthropic", "logout"}, "n\n", "Logout canceled", false, 0},
		{"oauth", []string{"anthropic", "login", "--no-browser", "--alias=named"}, "copied-code\n", "connected", false, 2},
		{"oauth-derived", []string{"anthropic", "login", "--no-browser"}, "copied-code\n", "connected", false, 2},
		{"oauth-eof", []string{"anthropic", "login", "--no-browser"}, "", "EOF", true, 2},
		{"bad-alias", []string{"anthropic", "accounts", "remove", "../bad"}, "", "path separators", true, 0},
		{"profile-current", []string{"profile", "current"}, "", "central", false, 1},
		{"profile-list", []string{"profile", "list"}, "", "central", false, 1},
		{"profile-show", []string{"profile", "show", "central"}, "", `"profile": "central"`, false, 1},
		{"profile-missing", []string{"profile", "show", "local-only"}, "", "could not load model profiles", true, 1},
		{"profile-use", []string{"profile", "use", "local-only", "--global"}, "", "profile use <profile> --local", true, 0},
		{"profile-format", []string{"profile", "show", "central", "--format=toml"}, "", "json or yaml", true, 0},
		{"codex-status", []string{"codex", "status"}, "", "ChatGPT subscription connected: true", false, 1},
		{"codex-login", []string{"codex", "login", "--device-auth", "--no-browser"}, "", "codex subscription connected", false, 2},
		{"codex-callback-rejected", []string{"codex", "login", "--device-auth=false"}, "", "sign-in uses a browser verification code", true, 0},
		{"copilot-login", []string{"copilot-login", "--no-browser"}, "", "copilot subscription connected", false, 2},
		{"codex-logout-guidance", []string{"codex", "logout", "--no-confirm"}, "", "codex logout --local", true, 0},
		{"copilot-logout-guidance", []string{"copilot-logout", "--no-confirm"}, "", "copilot-logout --local", true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
				switch r.URL.Path {
				case "/api/providers/anthropic/accounts":
					if r.Method == http.MethodPost {
						var mutation chat.AnthropicAccountMutation
						require.NoError(t, json.NewDecoder(r.Body).Decode(&mutation))
						expected := chat.AnthropicAccountMutation{Action: strings.TrimPrefix(test.name, "set-"), Alias: "work"}
						if test.name == "rename" {
							expected.NewAlias = "renamed"
						}
						if test.name == "logout" {
							expected.Alias = ""
						}
						assert.Equal(t, expected, mutation)
						w.WriteHeader(http.StatusNoContent)
						return
					}
					require.NoError(t, json.NewEncoder(w).Encode(chat.AnthropicAccounts{Accounts: []chat.AnthropicAccountSummary{{Alias: "work", Email: "daemon@example.com", IsDefault: true}}}))
				case "/api/providers/anthropic/accounts/usage":
					require.NoError(t, json.NewEncoder(w).Encode(chat.AnthropicAccountUsage{Account: "work", Window5h: chat.AnthropicUsageWindow{Status: "allowed", Utilization: 0.25}}))
				case "/api/providers/anthropic/oauth-login":
					require.NoError(t, json.NewEncoder(w).Encode(chat.AnthropicLogin{ID: "login", Status: "pending", AuthorizationURL: "https://provider.test/authorize"}))
				case "/api/providers/anthropic/oauth-login/login/complete":
					var body map[string]any
					defer r.Body.Close()
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					alias := ""
					if test.name == "oauth" {
						alias = "named"
					}
					assert.Equal(t, map[string]any{"code": "copied-code", "alias": alias}, body)
					require.NoError(t, json.NewEncoder(w).Encode(chat.AnthropicLogin{ID: "login", Status: "connected"}))
				case "/api/providers/anthropic/oauth-login/login":
					assert.Equal(t, http.MethodDelete, r.Method)
					w.WriteHeader(http.StatusNoContent)
				case "/api/chat/settings":
					if r.URL.Query().Get("profile") == "local-only" {
						http.Error(w, "profile not advertised", http.StatusBadRequest)
						return
					}
					require.NoError(t, json.NewEncoder(w).Encode(chat.ControlPlaneChatSettings{CurrentProfile: "central", Profiles: []chat.ControlPlaneProfileOption{{Name: "central", Scope: "global", Active: true}}, ReasoningEffort: "high"}))
				case "/api/providers/codex/status":
					require.NoError(t, json.NewEncoder(w).Encode(chat.CodexStatus{Connected: true, AccountID: "server-account"}))
				case "/api/providers/codex/device-login", "/api/providers/copilot/device-login":
					assert.Equal(t, http.MethodPost, r.Method)
					require.NoError(t, json.NewEncoder(w).Encode(chat.ProviderDeviceLogin{ID: "device", Status: "pending", VerificationURL: "https://provider.test/device", UserCode: "CODE"}))
				case "/api/providers/codex/device-login/device", "/api/providers/copilot/device-login/device":
					assert.Equal(t, http.MethodGet, r.Method)
					require.NoError(t, json.NewEncoder(w).Encode(chat.ProviderDeviceLogin{ID: "device", Status: "connected"}))
				default:
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			home := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o700))
			configPath := filepath.Join(home, ".kodelet", "config.yaml")
			config := []byte("profiles:\n  local-only:\n    model: client-only-model\n")
			require.NoError(t, os.WriteFile(configPath, config, 0o600))
			for _, name := range []string{"anthropic-credentials.json", "codex-credentials.json", "copilot-subscription.json"} {
				require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", name), []byte("client-credential-marker"), 0o600))
			}
			invalidStore := filepath.Join(home, "not-a-directory")
			require.NoError(t, os.WriteFile(invalidStore, []byte("invalid local store"), 0o600))
			env := []string{"PATH=" + home, "HOME=" + home, "KODELET_BASE_PATH=" + invalidStore, "KODELET_SERVER=" + daemon.URL, "KODELET_AUTH_TOKEN=client", "KODELET_TEST_CLI_PROCESS=1"}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			process := daemonCLIProcess(ctx, t, home, env, test.args...)
			process.Stdin = strings.NewReader(test.input)
			output, err := process.CombinedOutput()
			if test.wantError {
				require.Error(t, err, "%s", output)
			} else {
				require.NoError(t, err, "%s", output)
			}
			assert.Contains(t, string(output), test.want)
			assert.NotContains(t, string(output), "client-credential-marker")
			assert.EqualValues(t, test.calls, calls.Load())
			current, err := os.ReadFile(configPath)
			require.NoError(t, err)
			assert.Equal(t, config, current)
			for _, name := range []string{"anthropic-credentials.json", "codex-credentials.json", "copilot-subscription.json"} {
				contents, err := os.ReadFile(filepath.Join(home, ".kodelet", name))
				require.NoError(t, err)
				assert.Equal(t, "client-credential-marker", string(contents))
			}
		})
	}
}

func TestRemoteProviderLoginCancellationClosesOnlyItsLogin(t *testing.T) {
	for _, provider := range []string{"anthropic", "codex", "copilot"} {
		t.Run(provider, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var deletes atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					assert.True(t, strings.HasSuffix(r.URL.Path, "/owned"))
					deletes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if provider == "anthropic" {
					require.NoError(t, json.NewEncoder(w).Encode(chat.AnthropicLogin{ID: "owned", Status: "pending", AuthorizationURL: "https://provider.test"}))
				} else {
					require.NoError(t, json.NewEncoder(w).Encode(chat.ProviderDeviceLogin{ID: "owned", Status: "pending", VerificationURL: "https://provider.test", UserCode: "CODE"}))
				}
			}))
			defer daemon.Close()
			cmd := &cobra.Command{Use: "login"}
			cmd.SetContext(ctx)
			addRemoteAdministrationFlags(cmd)
			cmd.Flags().String("alias", "", "")
			cmd.Flags().Bool("no-browser", true, "")
			require.NoError(t, cmd.ParseFlags([]string{"--server=" + daemon.URL, "--auth-token=client"}))
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			defer reader.Close()
			defer writer.Close()
			cmd.SetIn(reader)
			cmd.SetOut(cancelProviderPrompt{cancel: cancel})
			cmd.SetErr(io.Discard)
			if provider == "anthropic" {
				err = runRemoteAnthropicLogin(cmd, nil)
			} else {
				err = runRemoteProviderDeviceLogin(cmd, provider)
			}
			require.Error(t, err)
			assert.EqualValues(t, 1, deletes.Load())
		})
	}
}

type cancelProviderPrompt struct{ cancel context.CancelFunc }

func (w cancelProviderPrompt) Write(data []byte) (int, error) {
	w.cancel()
	return len(data), nil
}

func TestRemoteProviderAndProfileUnavailableNeverFallBack(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
	}))
	defer daemon.Close()
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "no-store"), []byte("blocked"), 0o600))
	env := []string{"HOME=" + home, "PATH=" + home, "KODELET_BASE_PATH=" + filepath.Join(home, "no-store"), "KODELET_TEST_CLI_PROCESS=1"}
	for _, args := range [][]string{{"anthropic", "accounts", "list"}, {"anthropic", "login", "--no-browser"}, {"anthropic", "logout", "--no-confirm"}, {"profile", "current"}, {"codex", "status"}, {"codex", "login", "--no-browser"}, {"copilot-login", "--no-browser"}} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		process := daemonCLIProcess(ctx, t, home, env, append(args, "--server="+daemon.URL, "--auth-token=client")...)
		output, err := process.CombinedOutput()
		cancel()
		require.Error(t, err)
		assert.Contains(t, string(output), "503")
	}
	assert.EqualValues(t, 7, calls.Load(), "failed operations must not be retried")
	assert.NoDirExists(t, filepath.Join(home, ".kodelet"))
}

func TestLocalProviderAndProfileOperationsAreExplicit(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o700))
	for _, name := range []string{"codex-credentials.json", "copilot-subscription.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", name), []byte("{}"), 0o600))
	}
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer daemon.Close()
	env := []string{"HOME=" + home, "PATH=" + home, "KODELET_SERVER=" + daemon.URL, "KODELET_TEST_CLI_PROCESS=1"}
	for _, args := range [][]string{{"codex", "logout", "--local", "--no-confirm"}, {"copilot-logout", "--local", "--no-confirm"}, {"profile", "use", "default", "--local", "--global"}} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		output, err := daemonCLIProcess(ctx, t, home, env, args...).CombinedOutput()
		cancel()
		require.NoError(t, err, "%s", output)
	}
	assert.Zero(t, calls.Load())
	assert.NoFileExists(t, filepath.Join(home, ".kodelet", "codex-credentials.json"))
	assert.NoFileExists(t, filepath.Join(home, ".kodelet", "copilot-subscription.json"))
	assert.FileExists(t, filepath.Join(home, ".kodelet", "config.yaml"))
}

func TestProviderInputCancellationAndBounds(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err = readProviderInput(ctx, reader)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = readProviderInput(t.Context(), strings.NewReader(strings.Repeat("x", 9000)))
	require.ErrorContains(t, err, "8192")
}

func TestLocalAdministrationRejectsRemoteFlags(t *testing.T) {
	for _, args := range [][]string{
		{"profile", "list", "--local", "--server=http://remote.invalid"},
		{"profile", "show", "default", "--local", "--auth-token=remote"},
		{"profile", "use", "default", "--local", "--global", "--server=http://remote.invalid"},
		{"codex", "logout", "--local", "--no-confirm", "--server=http://remote.invalid"},
		{"copilot-logout", "--local", "--no-confirm", "--auth-token=remote"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			env := []string{"HOME=" + home, "PATH=" + home, "KODELET_TEST_CLI_PROCESS=1"}
			output, err := daemonCLIProcess(t.Context(), t, home, env, args...).CombinedOutput()
			require.Error(t, err, string(output))
			assert.Contains(t, string(output), "--local cannot be combined with")
			assert.NoDirExists(t, filepath.Join(home, ".kodelet"))
		})
	}
}
