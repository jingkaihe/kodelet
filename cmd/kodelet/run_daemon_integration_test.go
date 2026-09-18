package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestManagedServerColdRunReuseAndRecovery(t *testing.T) {
	directory := localServerTestState(t)
	home := os.Getenv("HOME")
	var toolResults, helperCalls atomic.Int32
	provider := daemonTestProvider(t, "", "", &toolResults, &helperCalls, nil)
	defer provider.Close()
	// Dependency fixtures prevent network downloads. These tool-free runs never
	// use filesystem search; only binary version discovery is exercised.
	binDir := filepath.Join(home, ".kodelet", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o700))
	for name, output := range map[string]string{
		"rg": "ripgrep " + binaries.RipgrepVersion,
		"fd": "fd " + binaries.FdVersion,
	} {
		script := fmt.Sprintf(`#!/bin/sh
echo '%s'
`, output)
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700))
	}
	config := fmt.Sprintf(`provider: openai
model: gpt-4o
weak_model: gpt-4o
max_tokens: 256
openai:
  platform: openai
  base_url: %s
  api_mode: chat_completions
  api_key_env_var: KODELET_TEST_PROVIDER_KEY
extensions:
  enabled: false
skills:
  enabled: false
serve:
  host: 127.0.0.1
  port: 0
`, provider.URL)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte(config), 0o600))
	environment := []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"SHELL=/bin/sh",
		"KODELET_BASE_PATH=" + os.Getenv("KODELET_BASE_PATH"),
		"KODELET_TEST_CLI_PROCESS=1",
		"KODELET_TEST_PROVIDER_KEY=daemon-only-key",
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	cli := func(cwd string, args ...string) *exec.Cmd {
		return daemonCLIProcess(ctx, t, cwd, environment, args...)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		process := daemonCLIProcess(cleanupCtx, t, home, environment, "server", "stop", "--force")
		output, err := process.CombinedOutput()
		assert.NoError(t, err, "%s", output)
		if t.Failed() {
			data, _ := os.ReadFile(filepath.Join(directory, "server.log"))
			t.Logf("server log: %.12000s", data)
		}
	})

	// Two independently launched CLI processes race a completely cold startup.
	workspaces := []string{filepath.Join(home, "first"), filepath.Join(home, "second")}
	var processes []*exec.Cmd
	var outputs, diagnostics [2]bytes.Buffer
	for index, cwd := range workspaces {
		require.NoError(t, os.MkdirAll(cwd, 0o700))
		process := cli(cwd, "run", "--no-tools", "--result-only", "cold query")
		process.Stdout, process.Stderr = &outputs[index], &diagnostics[index]
		require.NoError(t, process.Start())
		processes = append(processes, process)
	}
	for index, process := range processes {
		require.NoError(t, process.Wait(), "%s", diagnostics[index].String())
		assert.Equal(t, "tool-free answer\n", outputs[index].String())
	}
	assert.Equal(t, 1, strings.Count(diagnostics[0].String()+diagnostics[1].String(), "Starting local Kodelet server"))
	connection, err := readLocalServerConnection(directory)
	require.NoError(t, err)
	token, err := os.ReadFile(filepath.Join(directory, "client-token"))
	require.NoError(t, err)
	status, err := probeLocalServer(ctx, connection, string(token))
	require.NoError(t, err)
	assert.True(t, status.EmbeddedRunner.Ready, "daemon survives the launching clients")
	session, err := unix.Getsid(connection.PID)
	require.NoError(t, err)
	assert.Equal(t, connection.PID, session, "daemon owns a detached Unix session")
	client, err := chat.NewClient(connection.URL, string(token), "")
	require.NoError(t, err)
	for _, cwd := range workspaces {
		history, err := client.ListConversationsInCWD(ctx, 10, cwd)
		require.NoError(t, err)
		require.Len(t, history, 1)
		assert.Equal(t, cwd, history[0].CWD)
	}
	for _, args := range [][]string{{"server", "status"}, {"conversation", "list"}, {"server", "logs"}} {
		output, err := cli(home, args...).CombinedOutput()
		require.NoError(t, err, "%s", output)
		assert.NotContains(t, string(output), string(token))
	}
	// A second foreground server cannot migrate/open the same daemon state,
	// even if asked to listen on a different free port.
	output, err := cli(home, "serve", "--port=0").CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(output), "already owns this state directory")

	// Crash recovery replaces stale metadata, and stable runner affinity allows
	// resuming conversations from either directory after the restart.
	process, err := os.FindProcess(connection.PID)
	require.NoError(t, err)
	require.NoError(t, process.Kill())
	require.Eventually(t, func() bool {
		lock, err := tryLocalServerLock(directory, "server.lock")
		if err != nil || lock == nil {
			return false
		}
		_ = lock.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)
	output, err = cli(workspaces[1], "run", "--no-tools", "--result-only", "after crash").CombinedOutput()
	require.NoError(t, err, "%s", output)
	replacement, err := readLocalServerConnection(directory)
	require.NoError(t, err)
	assert.NotEqual(t, connection.InstanceID, replacement.InstanceID)
	output, err = cli(workspaces[0], "run", "--no-tools", "--result-only", "--follow", "--cwd="+workspaces[0], "resume after crash").CombinedOutput()
	require.NoError(t, err, "%s", output)
	output, err = cli(home, "server", "restart").CombinedOutput()
	require.NoError(t, err, "%s", output)
	restarted, err := readLocalServerConnection(directory)
	require.NoError(t, err)
	assert.NotEqual(t, replacement.InstanceID, restarted.InstanceID)

	t.Run("cold chat", func(t *testing.T) {
		output, err := cli(home, "server", "stop").CombinedOutput()
		require.NoError(t, err, "%s", output)
		process := cli(workspaces[0], "chat", "--no-tools")
		process.Env = append(process.Env, "TERM=xterm-256color", "COLORTERM=truecolor")
		terminal, err := pty.StartWithSize(process, &pty.Winsize{Rows: 40, Cols: 120})
		require.NoError(t, err)
		var screen daemonChatPTYOutput
		readDone, processDone := make(chan struct{}), make(chan error, 1)
		go func() {
			_, _ = io.Copy(&screen, terminal)
			close(readDone)
		}()
		go func() { processDone <- process.Wait() }()
		exited := false
		defer func() {
			if !exited {
				_ = process.Process.Kill()
				<-processDone
			}
			_ = terminal.Close()
			<-readDone
			if t.Failed() {
				t.Logf("chat screen: %s", screen.String())
			}
		}()
		require.Eventually(t, func() bool {
			return strings.Contains(screen.String(), "extensions · ")
		}, 10*time.Second, 20*time.Millisecond)
		_, err = io.WriteString(terminal, "cold chat query\r")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return strings.Contains(screen.String(), "tool-free answer")
		}, 10*time.Second, 20*time.Millisecond)
		_, err = io.WriteString(terminal, "\x03")
		require.NoError(t, err)
		select {
		case err := <-processDone:
			exited = true
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "chat did not exit")
		}
		output, err = cli(home, "server", "status").CombinedOutput()
		require.NoError(t, err, "%s", output)
		assert.Contains(t, string(output), "Runner ready: true")
	})

	t.Run("cold ACP", func(t *testing.T) {
		output, err := cli(home, "server", "stop").CombinedOutput()
		require.NoError(t, err, "%s", output)
		process := cli(workspaces[1], "acp")
		input, err := process.StdinPipe()
		require.NoError(t, err)
		outputPipe, err := process.StdoutPipe()
		require.NoError(t, err)
		var diagnostics bytes.Buffer
		process.Stderr = &diagnostics
		require.NoError(t, process.Start())
		defer process.Process.Kill()
		require.NoError(t, json.NewEncoder(input).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params":  map[string]any{"protocolVersion": 1},
		}))
		var response map[string]any
		require.NoError(t, json.NewDecoder(outputPipe).Decode(&response), "ACP stdout must contain only JSON-RPC")
		assert.Equal(t, float64(1), response["id"])
		assert.NotNil(t, response["result"])
		require.NoError(t, input.Close())
		require.NoError(t, process.Wait(), "%s", diagnostics.String())
		assert.Contains(t, diagnostics.String(), "Starting local Kodelet server")
	})
}

func TestManagedOIDCServerLifecycleAndImplicitClients(t *testing.T) {
	for _, embedded := range []bool{true, false} {
		t.Run(fmt.Sprintf("embedded_runner=%t", embedded), func(t *testing.T) {
			directory := localServerTestState(t)
			home := os.Getenv("HOME")
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			var toolResults, helperCalls, discoveryCalls atomic.Int32
			provider := daemonTestProvider(t, "", "", &toolResults, &helperCalls, nil)
			t.Cleanup(provider.Close)
			// Only discovery is needed: the local API credential must not require
			// an interactive OIDC login or an externally usable compatibility token.
			var issuer *httptest.Server
			issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/.well-known/openid-configuration" {
					assert.Fail(t, "unexpected OIDC request", "%s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				discoveryCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"issuer":                                issuer.URL,
					"authorization_endpoint":                issuer.URL + "/authorize",
					"token_endpoint":                        issuer.URL + "/token",
					"jwks_uri":                              issuer.URL + "/keys",
					"id_token_signing_alg_values_supported": []string{"RS256"},
				}))
			}))
			t.Cleanup(issuer.Close)
			binDir := filepath.Join(home, ".kodelet", "bin")
			require.NoError(t, os.MkdirAll(binDir, 0o700))
			for name, output := range map[string]string{
				"rg": "ripgrep " + binaries.RipgrepVersion,
				"fd": "fd " + binaries.FdVersion,
			} {
				script := fmt.Sprintf(`#!/bin/sh
echo '%s'
`, output)
				require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700))
			}
			secretFile := writeOIDCSecretFile(t, "fixture-client-secret")
			reservation, err := net.Listen("tcp4", "0.0.0.0:0")
			require.NoError(t, err)
			port := reservation.Addr().(*net.TCPAddr).Port
			t.Cleanup(func() { _ = reservation.Close() })
			const webURL = "https://kodelet.example.test"
			config := fmt.Sprintf(`provider: openai
model: gpt-4o
weak_model: gpt-4o
max_tokens: 256
openai:
  platform: openai
  base_url: %s
  api_mode: chat_completions
  api_key_env_var: KODELET_TEST_PROVIDER_KEY
extensions:
  enabled: false
skills:
  enabled: false
serve:
  host: 0.0.0.0
  port: %d
  embedded_runner: %t
  web_auth_mode: oidc
  runner_auth_mode: enrollment
  oidc:
    issuer: %s
    client_id: fixture-client
    client_secret_file: %q
    redirect_url: %s/auth/oidc/callback
    admin_emails: [admin@example.test]
`, provider.URL, port, embedded, issuer.URL, secretFile, webURL)
			require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte(config), 0o600))
			environment := []string{
				"HOME=" + home,
				"PATH=" + os.Getenv("PATH"),
				"SHELL=/bin/sh",
				"KODELET_BASE_PATH=" + os.Getenv("KODELET_BASE_PATH"),
				"KODELET_TEST_CLI_PROCESS=1",
				"KODELET_TEST_PROVIDER_KEY=daemon-only-key",
			}
			cli := func(args ...string) *exec.Cmd {
				return daemonCLIProcess(ctx, t, home, environment, args...)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				process := daemonCLIProcess(cleanupCtx, t, home, environment, "server", "stop", "--force")
				output, err := process.CombinedOutput()
				assert.NoError(t, err, "%s", output)
				if t.Failed() {
					data, _ := os.ReadFile(filepath.Join(directory, "server.log"))
					t.Logf("server log: %.12000s", data)
				}
			})
			runCLI := func(args ...string) string {
				t.Helper()
				output, err := cli(args...).CombinedOutput()
				require.NoError(t, err, "%s", output)
				return string(output)
			}
			require.NoError(t, reservation.Close())
			assert.Contains(t, runCLI("server", "start"), "Local server ready")
			connection, err := readLocalServerConnection(directory)
			require.NoError(t, err)
			assert.True(t, connection.Managed)
			assert.NotEqual(t, os.Getpid(), connection.PID)
			assert.Equal(t, fmt.Sprintf("http://127.0.0.1:%d", port), connection.URL)
			assert.Equal(t, webURL, connection.WebURL)
			output := runCLI("server", "status")
			assert.Contains(t, output, "Managed: true")
			assert.Contains(t, output, "API ready: true")
			assert.Contains(t, output, fmt.Sprintf("Runner ready: %t", embedded))
			client := &http.Client{
				Timeout: 2 * time.Second,
				Transport: &http.Transport{
					Proxy:             nil,
					DisableKeepAlives: true,
				},
			}
			if embedded {
				// Exercise shared lifecycle and credential behavior once; the
				// external-only case only needs to prove readiness without a runner.
				token, err := os.ReadFile(filepath.Join(directory, "client-token"))
				require.NoError(t, err)
				require.NotEmpty(t, token)
				for name, mode := range map[string]os.FileMode{"": 0o700, "connection.json": 0o600, "client-token": 0o600} {
					info, err := os.Stat(filepath.Join(directory, name))
					require.NoError(t, err)
					assert.Equal(t, mode, info.Mode().Perm())
				}
				data, err := os.ReadFile(filepath.Join(directory, "connection.json"))
				require.NoError(t, err)
				assert.NotContains(t, string(data), string(token))
				status, err := probeLocalServer(ctx, connection, string(token))
				require.NoError(t, err)
				assert.True(t, status.EmbeddedRunner.Enabled)
				assert.True(t, status.EmbeddedRunner.Ready)
				for _, path := range []string{"/api/status", "/api/status?token=" + string(token)} {
					request, err := http.NewRequestWithContext(ctx, http.MethodGet, connection.URL+path, nil)
					require.NoError(t, err)
					response, err := client.Do(request)
					require.NoError(t, err)
					assert.Equal(t, http.StatusUnauthorized, response.StatusCode, "local credential is header-only; OIDC remains required otherwise")
					require.NoError(t, response.Body.Close())
				}
				assert.NotContains(t, runCLI("server", "start"), string(token))
				reused, err := readLocalServerConnection(directory)
				require.NoError(t, err)
				assert.Equal(t, connection.InstanceID, reused.InstanceID)
				assert.NotContains(t, output, string(token))
				assert.Equal(t, webURL+"\n", runCLI("server", "url"))
				logs := runCLI("server", "logs")
				assert.NotContains(t, logs, string(token))
				assert.Contains(t, logs, "Open this URL: "+webURL)
				assert.Contains(t, logs, "Approve runner enrollments at: "+webURL+"/runner/enroll")
				runCLI("server", "restart")
				restarted, err := readLocalServerConnection(directory)
				require.NoError(t, err)
				assert.NotEqual(t, connection.InstanceID, restarted.InstanceID)
				assert.Equal(t, connection.URL, restarted.URL)
				replacementToken, err := os.ReadFile(filepath.Join(directory, "client-token"))
				require.NoError(t, err)
				assert.NotEqual(t, string(token), string(replacementToken))
				_, err = probeLocalServer(ctx, restarted, string(token))
				assert.ErrorContains(t, err, "HTTP 401")
			}
			runCLI("server", "stop")
			assert.NoFileExists(t, filepath.Join(directory, "connection.json"))
			assert.NoFileExists(t, filepath.Join(directory, "client-token"))
			assert.Positive(t, discoveryCalls.Load())
			if !embedded {
				// Explicit foreground flags override the inherited OIDC policy and
				// wildcard host while retaining the configured pinned port.
				discoveries := discoveryCalls.Load()
				process := cli("serve", "--skip-auth", "--host=127.0.0.1")
				var diagnostics bytes.Buffer
				process.Stdout, process.Stderr = &diagnostics, &diagnostics
				require.NoError(t, process.Start())
				processDone := make(chan error, 1)
				go func() { processDone <- process.Wait() }()
				exited := false
				defer func() {
					if !exited {
						_ = process.Process.Kill()
						<-processDone
					}
					if t.Failed() {
						t.Logf("foreground server output: %s", diagnostics.String())
					}
				}()
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, connection.URL+"/api/status", nil)
				require.NoError(t, err)
				require.Eventually(t, func() bool {
					response, err := client.Do(request)
					if err != nil {
						return false
					}
					defer response.Body.Close()
					var status localServerStatus
					if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&status) != nil {
						return false
					}
					return status.APIReady && !status.EmbeddedRunner.Enabled
				}, 10*time.Second, 20*time.Millisecond, "foreground server must accept unauthenticated requests on the inherited port")
				assert.Equal(t, discoveries, discoveryCalls.Load(), "--skip-auth must not initialize OIDC")
				assert.NoFileExists(t, filepath.Join(directory, "connection.json"))
				assert.NoFileExists(t, filepath.Join(directory, "client-token"))
				require.NoError(t, process.Process.Signal(os.Interrupt))
				select {
				case err := <-processDone:
					exited = true
					require.NoError(t, err, "%s", diagnostics.String())
				case <-time.After(5 * time.Second):
					require.FailNow(t, "foreground server did not stop")
				}
				return
			}

			// A stale login at the bootstrap default must not prevent starting
			// this user's explicitly configured OIDC server on its pinned port.
			store, err := userauth.NewStore()
			require.NoError(t, err)
			credential := controlPlaneAuthTestCredential(
				defaultRunnerServer,
				"expired-login",
				controlPlaneAuthTestBearer(0x93),
				controlPlaneAuthTestPrincipal("user", "user@example.test"),
				time.Now().Add(-time.Hour),
			)
			require.NoError(t, store.SaveCredential(credential))
			// Both clients start with no lifecycle state or credential overrides.
			process := cli("run", "--no-tools", "--result-only", "cold OIDC query")
			var stdout, diagnostics bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &diagnostics
			require.NoError(t, process.Run(), "%s", diagnostics.String())
			assert.Equal(t, "tool-free answer\n", stdout.String())
			assert.Contains(t, diagnostics.String(), "Starting local Kodelet server")
			runCLI("server", "stop")
			process = cli("chat", "--no-tools")
			process.Env = append(process.Env, "TERM=xterm-256color", "COLORTERM=truecolor")
			terminal, err := pty.StartWithSize(process, &pty.Winsize{Rows: 40, Cols: 120})
			require.NoError(t, err)
			var screen daemonChatPTYOutput
			readDone, processDone := make(chan struct{}), make(chan error, 1)
			go func() {
				_, _ = io.Copy(&screen, terminal)
				close(readDone)
			}()
			go func() { processDone <- process.Wait() }()
			exited := false
			defer func() {
				if !exited {
					_ = process.Process.Kill()
					<-processDone
				}
				_ = terminal.Close()
				<-readDone
				if t.Failed() {
					t.Logf("chat screen: %s", screen.String())
				}
			}()
			require.Eventually(t, func() bool {
				return strings.Contains(screen.String(), "extensions · ")
			}, 10*time.Second, 20*time.Millisecond)
			_, err = io.WriteString(terminal, "cold OIDC chat query\r")
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				return strings.Contains(screen.String(), "tool-free answer")
			}, 10*time.Second, 20*time.Millisecond)
			_, err = io.WriteString(terminal, "\x03")
			require.NoError(t, err)
			select {
			case err := <-processDone:
				exited = true
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "chat did not exit")
			}
			assert.Contains(t, runCLI("server", "status"), "Runner ready: true")
			assert.GreaterOrEqual(t, discoveryCalls.Load(), int32(4))
		})
	}
}

func TestDaemonChatRendersAndAcceptsInputBeforeBootstrapAndExtensionsReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	settingsGate, discoveryGate := make(chan struct{}), make(chan struct{})
	var settingsCalls, discoveryCalls, historyCalls, modelCalls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client-secret", r.Header.Get("Authorization"))
		var gate <-chan struct{}
		var response any
		switch r.URL.Path {
		case "/api/chat/settings":
			settingsCalls.Add(1)
			gate = settingsGate
			response = chat.ControlPlaneChatSettings{
				CurrentProfile:     "default",
				DefaultRunnerID:    "runner",
				DefaultRunnerReady: true,
			}
		case "/api/chat/cwd-suggestions":
			response = protocol.WorkspaceCWDHintsResult{BaseDir: root}
		case "/api/chat/message-history":
			historyCalls.Add(1)
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "runner", r.URL.Query().Get("runnerId"))
			assert.Equal(t, root, r.URL.Query().Get("cwd"))
			assert.False(t, r.URL.Query().Has("options"))
			response = protocol.WorkspaceMessageHistoryResult{CWD: root, ScopeCWD: root}
		case "/api/chat/slash-commands":
			discoveryCalls.Add(1)
			gate = discoveryGate
			response = map[string]any{"cwd": root, "commands": []any{}, "extensionCount": 9}
		case "/api/chat", "/api/chat/model":
			modelCalls.Add(1)
			http.Error(w, "unexpected model run", http.StatusServiceUnavailable)
			return
		default:
			assert.Fail(t, "unexpected daemon request", "%s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			case <-ctx.Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	t.Cleanup(daemon.Close)
	process := daemonCLIProcess(ctx, t, root, []string{
		"HOME=" + root,
		"PATH=" + os.Getenv("PATH"),
		"SHELL=/bin/sh",
		"KODELET_BASE_PATH=" + filepath.Join(root, "state"),
		"KODELET_TEST_CLI_PROCESS=1",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	}, "chat", "--server="+daemon.URL, "--auth-token=client-secret", "--cwd="+root)
	terminal, err := pty.StartWithSize(process, &pty.Winsize{Rows: 40, Cols: 120})
	require.NoError(t, err)
	var screen daemonChatPTYOutput
	readDone, processDone := make(chan struct{}), make(chan error, 1)
	go func() {
		_, _ = io.Copy(&screen, terminal)
		close(readDone)
	}()
	go func() { processDone <- process.Wait() }()
	exited := false
	t.Cleanup(func() {
		if !exited {
			_ = process.Process.Kill()
			<-processDone
		}
		_ = terminal.Close()
		<-readDone
		if t.Failed() {
			t.Logf("chat screen: %s", screen.String())
		}
	})
	waitRendered := func(text string) {
		t.Helper()
		require.Eventually(t, func() bool {
			return strings.Contains(screen.String(), text)
		}, 5*time.Second, 10*time.Millisecond, "terminal did not render %q", text)
	}
	write := func(text string) {
		t.Helper()
		_, err := io.WriteString(terminal, text)
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool { return settingsCalls.Load() > 0 }, 5*time.Second, 10*time.Millisecond)
	waitRendered("Ask kodelet")
	waitRendered("Starting…")
	// Bracketed paste makes each edit one input event, so terminal diff frames
	// cannot split the asserted text between character-by-character repaints.
	write("\x1b[200~startup draft\x1b[201~")
	waitRendered("startup draft")
	write("#")
	waitRendered("#")
	write("\r\x1b[200~ still typing\x1b[201~")
	waitRendered("still typing")
	assert.Zero(t, modelCalls.Load(), "Enter must not submit before daemon bootstrap completes")
	assert.Zero(t, discoveryCalls.Load(), "settings are still blocked")
	assert.Zero(t, historyCalls.Load(), "history requires the bootstrapped runner target")

	close(settingsGate)
	require.Eventually(t, func() bool { return discoveryCalls.Load() > 0 }, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return historyCalls.Load() > 0 }, 5*time.Second, 10*time.Millisecond)
	waitRendered("Loading extensions…")
	write("\x1b[200~ during discovery\x1b[201~")
	waitRendered("during discovery")
	write("@")
	waitRendered("@")
	assert.Zero(t, modelCalls.Load(), "loading resources must not start a model run")
	assert.NotContains(t, screen.String(), "extensions · ")

	close(discoveryGate)
	readyLabel := regexp.MustCompile(`9 extensions · [0-9]+(?:\.[0-9]+)? (?:ms|s)`)
	require.Eventually(t, func() bool {
		return readyLabel.MatchString(screen.String())
	}, 5*time.Second, 10*time.Millisecond, "terminal did not render elapsed readiness")
	assert.Zero(t, modelCalls.Load())
	write("\x03")
	select {
	case err := <-processDone:
		exited = true
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "chat did not exit")
	}
	assert.NotContains(t, screen.String(), "Message history unavailable")
}

// TestDaemonFirstCLIProcess executes the real CLI entry point in an isolated
// process, without invoking a nested go build or using a developer's credentials.
func TestDaemonFirstCLIProcess(t *testing.T) {
	if os.Getenv("KODELET_TEST_CLI_PROCESS") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			// Re-exec the real test CLI entry point when local bootstrap launches
			// a detached serve process, just as the installed binary re-execs itself.
			detachedServerCommand = func() (*exec.Cmd, error) {
				executable, err := os.Executable()
				if err != nil {
					return nil, err
				}
				return exec.Command(executable, "-test.run=^TestDaemonFirstCLIProcess$", "--", "serve", "--managed"), nil
			}
			os.Args = append([]string{"kodelet"}, os.Args[index+1:]...)
			main()
			os.Exit(0)
		}
	}
	require.FailNow(t, "missing CLI argument delimiter")
}

func TestDaemonFirstRunAcrossProcessBoundary(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T, *daemonRunFixture)
	}{
		{"file", testDaemonFileRun},
		{"no-tools", testDaemonToolFreeRun},
		{"extraction", testDaemonExtraction},
		{"delegation", testDaemonDelegation},
		{"commit", testDaemonCommit},
		{"pull-request", testDaemonPullRequest},
		{"workspace-selection", testDaemonWorkspaceSelection},
	}
	for _, placement := range []string{"standalone", "embedded"} {
		t.Run(placement, func(t *testing.T) {
			for _, scenario := range scenarios {
				t.Run(scenario.name, func(t *testing.T) {
					fixture := newDaemonRunFixture(t, placement)
					scenario.run(t, fixture)
				})
			}
		})
	}
}

// Each scenario gets its own daemon, runner and history. A failed delegation
// must not suppress unrelated commit, PR or workspace checks.
type daemonRunFixture struct {
	ctx          context.Context
	root         string
	workspace    string
	placement    string
	sdk          string
	serverURL    string
	runnerID     string
	clientEnv    []string
	client       *chat.Client
	toolResults  *atomic.Int32
	helperCalls  *atomic.Int32
	childStarted <-chan struct{}
	releaseChild func()
	finishChild  func()
}

func newDaemonRunFixture(t *testing.T, placement string, processEnv ...string) *daemonRunFixture {
	t.Helper()
	sdk := os.Getenv("KODELET_TEST_EXTENSION_SDK")
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	t.Setenv("HOME", root)
	workspace := filepath.Join(root, "runner-workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	filePath := filepath.Join(workspace, "marker.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("runner-file-evidence"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("WORKSPACE_CONTEXT_MUST_NOT_REACH_HELPER"), 0o600))
	extensionDir := filepath.Join(workspace, ".kodelet", "extensions")
	require.NoError(t, os.MkdirAll(extensionDir, 0o700))
	executable, err := os.Executable()
	require.NoError(t, err)
	webPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<p>runner-page-evidence</p>")
	}))
	t.Cleanup(webPage.Close)
	var toolResults, helperCalls atomic.Int32
	childStarted, releaseChild, finishChild := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChild) })
	finish := sync.OnceFunc(func() { close(finishChild) })
	var started sync.Once
	provider := daemonTestProvider(t, filePath, webPage.URL, &toolResults, &helperCalls, func(ctx context.Context, streaming bool) {
		gate := releaseChild
		if streaming {
			gate = finishChild
		} else {
			started.Do(func() { close(childStarted) })
		}
		select {
		case <-gate:
		case <-ctx.Done():
			assert.Fail(t, "ACP provider request was cancelled before release")
		}
	})
	t.Cleanup(provider.Close)
	t.Cleanup(release)
	t.Cleanup(finish)
	script := fmt.Sprintf(`#!/bin/sh
KODELET_TEST_ACP_EXTENSION=1 exec %q -test.run '^TestDaemonACPSearchExtensionProcess$'
`, executable)
	if sdk != "" {
		script = daemonSDKSearchExtension(t, extensionDir, sdk, provider.URL)
	}
	require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "kodelet-extension-search"), []byte(script), 0o700))
	oldSettings := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range oldSettings {
			viper.Set(key, value)
		}
	})
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4o")
	viper.Set("weak_model", "gpt-4o")
	viper.Set("max_tokens", 256)
	viper.Set("reasoning_effort", "medium")
	viper.Set("profiles", map[string]any{"deep": map[string]any{
		"provider":                  "openai",
		"model":                     "gpt-4o",
		"reasoning_effort":          "xhigh",
		"allowed_reasoning_efforts": []string{"high", "xhigh"},
	}})
	viper.Set("openai", map[string]any{
		"platform":        "openai",
		"base_url":        provider.URL,
		"api_key_env_var": "KODELET_TEST_PROVIDER_KEY",
		"api_mode":        "chat_completions",
	})
	viper.Set("extensions.enabled", true)
	viper.Set("skills.enabled", false)
	viper.Set("allowed_tools", []string{"file_read", "web_fetch", "grep_tool", "glob_tool", "code_search", "bash"})
	t.Setenv("KODELET_TEST_PROVIDER_KEY", "daemon-only-key")
	// This fixture explicitly grants normal client credentials to extensions.
	// Runner authentication remains separate and cannot authorize ACP calls.
	t.Setenv("KODELET_AUTH_TOKEN", "client-secret")
	wrapper := filepath.Join(root, "sdk-kodelet")
	t.Setenv("KODELET_BIN", wrapper)
	t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-store"))
	require.NoError(t, db.RunMigrations(ctx, migrations.All()))
	config := &controlplane.ServerConfig{
		Host:            "127.0.0.1",
		Port:            0,
		CompactRatio:    0.8,
		AuthToken:       "client-secret",
		RunnerAuthToken: "runner-secret",
	}
	settings := map[string]any{
		"tool_mode":              "full",
		"enable_fs_search_tools": true,
		"allowed_tools":          []string{"file_read", "web_fetch", "grep_tool", "glob_tool", "code_search", "bash"},
		"extensions":             map[string]any{"enabled": true},
		"skills":                 map[string]any{"enabled": false},
	}
	if placement == "embedded" {
		store, err := localstate.NewStore()
		require.NoError(t, err)
		config.EmbeddedRunner = &controlplane.EmbeddedRunnerConfig{
			Workspace: workspace,
			Settings:  settings,
			Store:     store,
		}
	}
	daemon, err := controlplane.NewServer(ctx, config, nil)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serverURL := "http://" + listener.Addr().String()
	serverCtx, stopServer := context.WithCancel(ctx)
	serverDone := make(chan error, 1)
	go func() { serverDone <- daemon.Serve(serverCtx, listener) }()
	t.Cleanup(func() {
		stopServer()
		select {
		case err := <-serverDone:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			assert.Fail(t, "daemon did not shut down")
		}
		assert.NoError(t, daemon.Close())
	})

	// The child environment is deliberately independent, not a runtime
	// scrubbing feature: no provider secret is passed to these test processes.
	childEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + root,
		"KODELET_TEST_CLI_PROCESS=1",
		"KODELET_AUTH_TOKEN=client-secret",
		"KODELET_BIN=" + wrapper,
	}
	childEnv = append(childEnv, processEnv...)
	if placement == "standalone" {
		data, err := json.Marshal(settings)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(workspace, "kodelet-config.yaml"), data, 0o600))
		runnerCtx, stopRunner := context.WithCancel(ctx)
		runnerEnv := append(childEnv, "KODELET_BASE_PATH="+filepath.Join(root, "runner-state"))
		process := daemonCLIProcess(runnerCtx, t, workspace, runnerEnv,
			"runner", "start",
			"--server="+serverURL,
			"--auth-token=runner-secret",
			"--name=acceptance-runner",
		)
		var logs bytes.Buffer
		process.Stdout, process.Stderr = &logs, &logs
		require.NoError(t, process.Start())
		t.Cleanup(func() {
			stopRunner()
			_ = process.Wait()
			if t.Failed() {
				t.Log(logs.String())
			}
		})
	}
	var runnerID string
	require.Eventually(t, func() bool {
		runners, _, err := fetchRunners(ctx, serverURL, "client-secret")
		if err != nil {
			return false
		}
		for _, runner := range runners {
			if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle && runner.SessionExtensions {
				runnerID = runner.ID
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond)
	wrapperScript := fmt.Sprintf(`#!/bin/sh
KODELET_SERVER=%q KODELET_TEST_CLI_PROCESS=1 exec %q -test.run '^TestDaemonFirstCLIProcess$' -- "$@"
`, serverURL, executable)
	require.NoError(t, os.WriteFile(wrapper, []byte(wrapperScript), 0o700))
	client, err := chat.NewClient(serverURL, "client-secret", runnerID)
	require.NoError(t, err)
	invalidStore := filepath.Join(root, "client-store-is-a-file")
	require.NoError(t, os.WriteFile(invalidStore, []byte("no client database"), 0o600))
	clientEnv := append(childEnv, "KODELET_BASE_PATH="+invalidStore)
	return &daemonRunFixture{
		ctx:          ctx,
		root:         root,
		workspace:    workspace,
		placement:    placement,
		sdk:          sdk,
		serverURL:    serverURL,
		runnerID:     runnerID,
		clientEnv:    clientEnv,
		client:       client,
		toolResults:  &toolResults,
		helperCalls:  &helperCalls,
		childStarted: childStarted,
		releaseChild: release,
		finishChild:  finish,
	}
}

func (f *daemonRunFixture) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	args = append(args,
		"--server="+f.serverURL,
		"--auth-token=client-secret",
		"--cwd="+f.workspace,
	)
	if f.placement == "standalone" {
		args = append(args, "--runner="+f.runnerID)
	}
	return daemonCLIProcess(f.ctx, t, f.root, f.clientEnv, args...)
}

func (f *daemonRunFixture) history(t *testing.T, count int) []convtypes.ConversationSummary {
	t.Helper()
	history, err := f.client.ListConversationsInCWD(f.ctx, 10, f.workspace)
	require.NoError(t, err)
	require.Len(t, history, count)
	for _, summary := range history {
		stored, err := f.client.LoadConversation(f.ctx, summary.ID)
		require.NoError(t, err)
		assert.Equal(t, f.workspace, stored.CWD)
		assert.Equal(t, f.runnerID, stored.RunnerID)
		assert.NotEmpty(t, stored.Messages)
		assert.Positive(t, stored.Usage.OutputTokens)
	}
	return history
}

func testDaemonFileRun(t *testing.T, f *daemonRunFixture) {
	output, err := f.command(t, "run", "--result-only", "inspect the runner file").CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "runner-file-evidence\n", string(output))
	assert.EqualValues(t, 1, f.toolResults.Load())
	assert.Zero(t, f.helperCalls.Load())
	f.history(t, 1)
}

func testDaemonToolFreeRun(t *testing.T, f *daemonRunFixture) {
	output, err := f.command(t, "run", "--no-tools", "--result-only", "inspect the runner file").CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "tool-free answer\n", string(output))
	assert.Zero(t, f.toolResults.Load())
	assert.Zero(t, f.helperCalls.Load())
	f.history(t, 1)
}

func testDaemonExtraction(t *testing.T, f *daemonRunFixture) {
	output, err := f.command(t, "run", "--result-only", "extract the webpage").CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "runner-page-evidence\n", string(output))
	assert.EqualValues(t, 1, f.toolResults.Load())
	assert.EqualValues(t, 1, f.helperCalls.Load(), "the runner delegates one provider call to the daemon")
	history := f.history(t, 1) // Extraction must not create a helper conversation.
	stored, err := f.client.LoadConversation(f.ctx, history[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 15, stored.Usage.OutputTokens, "two parent exchanges plus one helper exchange")
	for _, message := range stored.Messages {
		assert.NotContains(t, message.Content, "Extraction request:", "temporary helper input must not enter parent history")
	}
}

func testDaemonDelegation(t *testing.T, f *daemonRunFixture) {
	// The parent hides read/search tools. Explicit ACP options must select them
	// independently instead of inheriting the parent's presentation filters.
	configPath := filepath.Join(f.workspace, "kodelet-config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("tool_mode: patch\n"), 0o600))
	process := f.command(t, "run", "--result-only", "--profile=deep", "delegate code search")
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	require.NoError(t, process.Start())
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	exited := false
	defer func() {
		f.releaseChild()
		f.finishChild()
		if !exited {
			_ = process.Process.Kill()
			<-done
		}
	}()
	select {
	case <-f.childStarted:
	case err := <-done:
		exited = true
		require.FailNow(t, "ACP search did not start", "error: %v; stderr: %s; stdout: %s", err, stderr.String(), stdout.String())
	case <-f.ctx.Done():
		require.FailNow(t, "ACP search did not reach the provider")
	}
	assertDaemonACPSearchBroadcast(f.ctx, t, f.client, f.serverURL, f.workspace, f.releaseChild, f.finishChild)
	select {
	case err := <-done:
		exited = true
		require.NoError(t, err, "client stderr: %s", stderr.String())
	case <-f.ctx.Done():
		require.FailNow(t, "parent did not finish after ACP search")
	}
	assert.Equal(t, "runner-file-evidence\n", stdout.String())
	assert.EqualValues(t, 2, f.toolResults.Load(), "one child tool result and one parent delegation result")
	assert.Zero(t, f.helperCalls.Load())
	history := f.history(t, 2)
	var parentID, childID, childParentID string
	for _, summary := range history {
		stored, err := f.client.LoadConversation(f.ctx, summary.ID)
		require.NoError(t, err)
		record, err := f.client.LoadConversationRecord(f.ctx, summary.ID)
		require.NoError(t, err)
		switch {
		case summary.Summary == daemonACPSearchName:
			childID = summary.ID
			assert.Equal(t, daemonACPSearchName, record.Summary, "persistence retains the explicit worker name")
			assert.Contains(t, string(record.RawMessages), "delegate code search", "live fork retains parent history")
			assert.Contains(t, string(record.RawMessages), "child code search")
			assert.Contains(t, string(record.RawMessages), "runner-file-evidence")
		case f.sdk != "" && summary.Metadata["profile"] == "code-search":
			childID, childParentID = summary.ID, stored.ParentConversationID
			snapshot, present, err := conversations.ConfigSnapshotFromMetadata(record.Metadata)
			require.NoError(t, err)
			require.True(t, present)
			assert.Equal(t, "code-search", snapshot.Profile)
			assert.True(t, snapshot.ExtensionProfile)
			assert.Equal(t, "gpt-5.6-luna", snapshot.Model)
			assert.Empty(t, snapshot.WeakModel, "registered profiles must not inherit the daemon weak model")
			assert.Equal(t, 512, snapshot.MaxTokens)
			assert.Equal(t, "none", snapshot.ReasoningEffort)
			assert.Equal(t, llmtypes.OpenAIAPIModeResponses, snapshot.OpenAI.APIMode)
			assert.Equal(t, llmtypes.OpenAIServiceTierFast, snapshot.OpenAI.ServiceTier)
			assert.Contains(t, string(record.RawMessages), "child code search")
			assert.Contains(t, string(record.RawMessages), "runner-file-evidence")
			assert.NotContains(t, string(record.RawMessages), "delegate code search")
		case summary.FirstMessage == "delegate code search":
			parentID = summary.ID
			snapshot, present, err := conversations.ConfigSnapshotFromMetadata(summary.Metadata)
			require.NoError(t, err)
			require.True(t, present)
			assert.Equal(t, "deep", snapshot.Profile)
			assert.Equal(t, "gpt-4o", snapshot.Model)
			assert.Equal(t, "xhigh", snapshot.ReasoningEffort)
			assert.Equal(t, 10, stored.Usage.OutputTokens, "ACP session usage is not charged to the parent again")
			if f.sdk != "" {
				assert.Contains(t, string(record.RawMessages), "extension-runner:"+f.runnerID)
			}
			for _, message := range stored.Messages {
				assert.NotContains(t, message.Content, daemonACPSearchPrompt)
			}
		}
	}
	require.NotEmpty(t, parentID, "parent profile, runner metadata and usage assertions must run")
	require.NotEmpty(t, childID, "child has its own persisted conversation")
	if f.sdk == "typescript" {
		assert.Equal(t, parentID, childParentID, "registered-profile ACP children retain their requested parent")
	}
	if f.sdk != "" {
		profileSettings, err := f.client.ChatSettings(f.ctx, "code-search")
		require.NoError(t, err)
		assert.Equal(t, "code-search", profileSettings.CurrentProfile)
		assert.Equal(t, "none", profileSettings.ReasoningEffort)
		for _, profile := range profileSettings.Profiles {
			assert.NotEqual(t, "code-search", profile.Name, "hidden profiles must not appear in normal pickers")
		}
		parentSettings, err := f.client.ChatSettings(f.ctx, "deep")
		require.NoError(t, err)
		assert.Equal(t, "xhigh", parentSettings.ReasoningEffort)
		assert.Equal(t, []string{"high", "xhigh"}, parentSettings.ReasoningEffortOptions)
		daemonSDKClient(f.ctx, t, f.root, f.workspace, f.sdk, f.serverURL, f.runnerID, f.clientEnv)
	}
}

func (f *daemonRunFixture) git(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.CommandContext(f.ctx, "git", args...)
	command.Dir = f.workspace
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return strings.TrimSpace(string(output))
}

func (f *daemonRunFixture) stageCommit(t *testing.T) {
	t.Helper()
	f.git(t, "init")
	f.git(t, "config", "user.name", "Runner Commit User")
	f.git(t, "config", "user.email", "commit@example.com")
	f.git(t, "config", "commit.gpgsign", "false")
	path := filepath.Join(f.workspace, "commit-evidence.txt")
	require.NoError(t, os.WriteFile(path, []byte("APPROVED_RUNNER_COMMIT_CONTENT\n"), 0o600))
	f.git(t, "add", "commit-evidence.txt")
}

func testDaemonCommit(t *testing.T, f *daemonRunFixture) {
	f.stageCommit(t)
	// Keep an unrelated conversation to catch over-broad temporary-history cleanup.
	output, err := f.command(t, "run", "--no-tools", "--result-only", "before commit").CombinedOutput()
	require.NoError(t, err, "%s", output)
	before := f.history(t, 1)

	// The client has no Git executable, provider credentials or usable database.
	commit := f.command(t, "commit", "--no-confirm")
	commit.Env = append(commit.Env, "PATH="+f.root)
	output, err = commit.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.ElementsMatch(t, before, f.history(t, 1), "commits remove only their temporary conversation")
	message := f.git(t, "log", "-1", "--format=%B")
	assert.Contains(t, message, "feat: commit runner snapshot")
	assert.Contains(t, message, "Signed-off-by: Runner Commit User <commit@example.com>")
	assert.Equal(t, "APPROVED_RUNNER_COMMIT_CONTENT", f.git(t, "show", "HEAD:commit-evidence.txt"))
	assert.Empty(t, f.git(t, "diff", "--cached"))
}

func testDaemonPullRequest(t *testing.T, f *daemonRunFixture) {
	// Seed the repository directly: PR coverage must not depend on the commit CLI.
	f.stageCommit(t)
	f.git(t, "commit", "-m", "feat: commit runner snapshot")
	templatePath := filepath.Join(f.workspace, "custom-pr-template.md")
	require.NoError(t, os.WriteFile(templatePath, []byte("RUNNER_ONLY_PR_TEMPLATE"), 0o600))
	// Only the external GitHub mutation is replaced; recipe expansion and shell
	// execution still use the real runner and its relative template path.
	const gh = `#!/bin/sh
printf '%s\n' "$PWD" "$@" >> gh-invocations
printf '%s\n' 'https://github.example/fixture/pull/1'
`
	require.NoError(t, os.WriteFile(filepath.Join(f.workspace, "fixture-gh"), []byte(gh), 0o700))
	pr := f.command(t, "pr",
		"--provider=github",
		"--target=fixture-target",
		"--draft",
		"--template-file=custom-pr-template.md",
		"--result-only",
	)
	pr.Env = append(pr.Env, "PATH="+f.root)
	output, err := pr.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "https://github.example/fixture/pull/1\n", string(output))
	invocations, err := os.ReadFile(filepath.Join(f.workspace, "gh-invocations"))
	require.NoError(t, err)
	physicalWorkspace, err := filepath.EvalSymlinks(f.workspace)
	require.NoError(t, err)
	wantInvocation := physicalWorkspace + `
pr
create
--base
fixture-target
--draft
--title
Runner PR
--body
RUNNER_ONLY_PR_TEMPLATE
`
	assert.Equal(t, wantInvocation, string(invocations), "one runner-host mutation with the requested target and template")
}

func testDaemonWorkspaceSelection(t *testing.T, f *daemonRunFixture) {
	// Warm the original workspace before switching, so stale execution context
	// or extension caches cannot make a fresh-directory-only test pass.
	output, err := f.command(t, "run", "--result-only", "inspect the runner file").CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "runner-file-evidence\n", string(output))

	alternateCWD := filepath.Join(f.root, "alternate-workspace")
	require.NoError(t, os.MkdirAll(alternateCWD, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(alternateCWD, "marker.txt"), []byte("alternate-directory-evidence"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(alternateCWD, "AGENTS.md"), []byte("ALTERNATE_DIRECTORY_CONTEXT"), 0o600))
	const config = `tool_mode: full
allowed_tools: [file_read]
extensions:
  enabled: false
skills:
  enabled: false
model: forbidden-repository-model
`
	require.NoError(t, os.WriteFile(filepath.Join(alternateCWD, "kodelet-config.yaml"), []byte(config), 0o600))
	alternate := daemonCLIProcess(f.ctx, t, f.root, f.clientEnv,
		"run",
		"--server="+f.serverURL,
		"--auth-token=client-secret",
		"--runner="+f.runnerID,
		"--cwd="+alternateCWD,
		"--result-only",
		"alternate directory configuration",
	)
	output, err = alternate.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "alternate-directory-evidence\n", string(output))
	history, err := f.client.ListConversationsInCWD(f.ctx, 10, alternateCWD)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, alternateCWD, history[0].CWD)
	if f.placement == "embedded" {
		// Without --cwd, a same-installation client must use its own directory,
		// not the daemon's startup workspace.
		invokingCWD := filepath.Join(f.root, "client-invoking-workspace")
		require.NoError(t, os.MkdirAll(invokingCWD, 0o700))
		localEnv := []string{
			"PATH=" + f.root,
			"HOME=" + f.root,
			"KODELET_TEST_CLI_PROCESS=1",
			"KODELET_BASE_PATH=" + filepath.Join(f.root, "daemon-store"),
			"KODELET_SERVER=" + f.serverURL,
			"KODELET_AUTH_TOKEN=client-secret",
		}
		process := daemonCLIProcess(f.ctx, t, invokingCWD, localEnv,
			"run", "--no-tools", "--result-only", "same installation current directory",
		)
		output, err := process.CombinedOutput()
		require.NoError(t, err, "%s", output)
		assert.Equal(t, "tool-free answer\n", string(output))
		history, err := f.client.ListConversationsInCWD(f.ctx, 10, invokingCWD)
		require.NoError(t, err)
		require.Len(t, history, 1)
		assert.Equal(t, invokingCWD, history[0].CWD)
	}
}

// Opt-in cross-repository SDK gate: build sdk/dist first, or set
// KODELET_PYTHON_SDK_PATH to a uv-synced Python SDK checkout. The ordinary Go
// suite always runs the ordinary ACP protocol fixture without an external SDK.
// The Python SDK must emit native snake_case profile configuration on the wire.
func daemonSDKSearchExtension(t *testing.T, dir, sdk, providerURL string) string {
	t.Helper()
	var executable, source, name string
	switch sdk {
	case "typescript":
		var err error
		executable, err = exec.LookPath("node")
		require.NoError(t, err)
		dist, err := filepath.Abs("../../sdk/dist")
		require.NoError(t, err)
		name = "search.mjs"
		source = fmt.Sprintf(`import { Client, defineExtension, z } from %q;
import { runExtension } from %q;
await runExtension(defineExtension(ext => {
  const profile = ext.registerProfile({
    name: "code-search",
    provider: "openai",
    model: "gpt-5.6-luna",
    max_tokens: 512,
    reasoning_effort: "none",
    openai: {
      platform: "openai",
      base_url: %q,
      api_key_env_var: "KODELET_TEST_PROVIDER_KEY",
      api_mode: "responses",
      service_tier: "fast",
      websocket_mode: false,
    },
    hidden: true,
  });
  let parentSearch = false;
  ext.on("user.message", event => { parentSearch = event.message === "delegate code search"; });
  ext.on("agent.init", () => parentSearch ? { tools: { disable: ["file_read", "grep_tool", "glob_tool"] } } : undefined);
  ext.registerTool({ name: "code_search", description: "Run restricted code search", inputSchema: z.object({}),
    async execute(_input, ctx) {
      if (!ctx.runnerId) throw new Error("Missing extension runner metadata");
      const client = new Client({ command: process.env.KODELET_BIN, cwd: ctx.cwd, runner: ctx.runnerId });
      try {
        const session = await client.createSession({ profile, parentConversationId: ctx.conversationId, options: {
          allowedTools: ["file_read", "grep_tool", "glob_tool"],
          noSkills: true, enableFSSearchTools: true, maxTurns: 3,
        }, extensions: [api => api.on("agent.init", () => ({ systemPrompt: { replace: %q } }))] });
        const result = await session.runAndWait({ message: "child code search", signal: ctx.signal });
        return "extension-runner:" + ctx.runnerId + "\n" + result.content;
      } finally {
        await client.close();
      }
    }
  });
}));
`, "file://"+filepath.Join(dist, "index.js"), "file://"+filepath.Join(dist, "runtime.js"), providerURL, daemonACPSearchPrompt)
	case "python":
		root := os.Getenv("KODELET_PYTHON_SDK_PATH")
		require.NotEmpty(t, root, "set KODELET_PYTHON_SDK_PATH to a uv-synced SDK checkout")
		executable = filepath.Join(root, ".venv", "bin", "python")
		name = "search.py"
		source = fmt.Sprintf(`import asyncio, os
from kodelet_sdk import BaseModel, Client, ExecutionOptions, Extension
from kodelet_sdk.runtime import run_extension
ext = Extension()
profile = ext.register_profile(
    "code-search",
    provider="openai",
    model="gpt-5.6-luna",
    max_tokens=512,
    reasoning_effort="none",
    openai={
        "platform": "openai",
        "base_url": %q,
        "api_key_env_var": "KODELET_TEST_PROVIDER_KEY",
        "api_mode": "responses",
        "service_tier": "fast",
        "websocket_mode": False,
    },
    hidden=True,
)
parent_search = False
@ext.on("user.message")
def user_message(event, _ctx):
    global parent_search
    parent_search = event.message == "delegate code search"
@ext.on("agent.init")
def filter_parent(_event, _ctx):
    if parent_search:
        return {"tools": {"disable": ["file_read", "grep_tool", "glob_tool"]}}
prompt = Extension(name="search-prompt")
@prompt.on("agent.init")
def search_prompt(_event, _ctx):
    return {"systemPrompt": {"replace": %q}}
class Input(BaseModel):
    pass
@ext.tool("code_search", description="Run restricted code search", input_schema=Input)
async def search(_input, ctx):
    if not ctx.runner_id:
        raise RuntimeError("Missing extension runner metadata")
    client = Client(command=os.environ["KODELET_BIN"], cwd=ctx.cwd, runner=ctx.runner_id)
    try:
        session = await client.create_session(profile=profile, options=ExecutionOptions(
            allowed_tools=["file_read", "grep_tool", "glob_tool"],
            no_skills=True, enable_fs_search_tools=True, max_turns=3,
        ), extensions=[prompt])
        result = await session.run_and_wait(message="child code search")
        return "extension-runner:" + ctx.runner_id + "\n" + result.content
    finally:
        await client.close()
asyncio.run(run_extension(ext))
`, providerURL, daemonACPSearchPrompt)
	default:
		t.Fatalf("unknown extension SDK %q", sdk)
	}
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return fmt.Sprintf(`#!/bin/sh
exec %q %q
`, executable, path)
}

func daemonSDKClient(ctx context.Context, t *testing.T, root, workspace, sdk, server, runner string, environment []string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(root, "sdk-kodelet")
	script := fmt.Sprintf(`#!/bin/sh
KODELET_TEST_CLI_PROCESS=1 exec %q -test.run '^TestDaemonFirstCLIProcess$' -- "$@"
`, executable)
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o700))
	options, err := json.Marshal(map[string]string{"command": wrapper, "server": server, "runner": runner})
	require.NoError(t, err)
	var interpreter, path, source string
	if sdk == "typescript" {
		interpreter, err = exec.LookPath("node")
		require.NoError(t, err)
		dist, err := filepath.Abs("../../sdk/dist/index.js")
		require.NoError(t, err)
		path = filepath.Join(root, "client.mjs")
		source = fmt.Sprintf(`import { Client } from %q;
const client = new Client(%s);
try {
  const session = await client.createSession({ cwd: %q, options: { model: "gpt-4o-mini", noTools: true, maxTurns: 1 } });
  console.log((await session.runAndWait({ message: "sdk typed options" })).content);
} finally { await client.close(); }
`, "file://"+dist, options, workspace)
	} else {
		interpreter = filepath.Join(os.Getenv("KODELET_PYTHON_SDK_PATH"), ".venv", "bin", "python")
		path = filepath.Join(root, "client.py")
		source = fmt.Sprintf(`import asyncio
from kodelet_sdk import Client
async def main():
    client = Client(%s)
    try:
        session = await client.create_session(cwd=%q, options={"model": "gpt-4o-mini", "noTools": True, "maxTurns": 1})
        print((await session.run_and_wait(message="sdk typed options")).content)
    finally:
        await client.close()
asyncio.run(main())
`, options, workspace)
	}
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	process := exec.CommandContext(ctx, interpreter, path)
	process.Env = append(environment, "KODELET_AUTH_TOKEN=client-secret")
	process.Dir = root
	output, err := process.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "tool-free answer\n", string(output))
}

func daemonCLIProcess(ctx context.Context, t *testing.T, cwd string, environment []string, args ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestDaemonFirstCLIProcess$", "--"}, args...)...)
	command.Dir, command.Env = cwd, environment
	if testing.CoverMode() != "" {
		// This subprocess calls main/os.Exit rather than testing.M.Run. Give the
		// instrumented executable a real output directory for its coverage data.
		command.Env = append(append([]string{}, environment...), "GOCOVERDIR="+t.TempDir())
	}
	command.WaitDelay = 5 * time.Second
	return command
}

func daemonTestProvider(
	t *testing.T,
	filePath, pageURL string,
	toolResults, helperCalls *atomic.Int32,
	childGate func(context.Context, bool),
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer daemon-only-key", r.Header.Get("Authorization"))
		if strings.HasSuffix(r.URL.Path, "/responses") {
			daemonSearchResponses(t, w, r, filePath, toolResults, childGate)
			return
		}
		var request struct {
			Model    string           `json:"model"`
			Stream   bool             `json:"stream"`
			Tools    []map[string]any `json:"tools"`
			Messages []struct {
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				ToolCallID string          `json:"tool_call_id"`
				ToolCalls  []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		text := "tool-free answer"
		var delta map[string]any
		finish := "stop"
		var helper, extraction, delegated, child, pullRequest, alternate bool
		lastUser := 0
		pending := map[string]bool{}
		for index, message := range request.Messages {
			for _, call := range message.ToolCalls {
				pending[call.ID] = true
			}
			if message.Role == "tool" {
				assert.True(t, pending[message.ToolCallID], "tool results must retain their calls in live forks")
				delete(pending, message.ToolCallID)
			}
			if message.Role == "user" {
				lastUser = index
			}
			helper = helper || strings.Contains(string(message.Content), "Extraction request:")
			extraction = extraction || (message.Role == "user" && strings.Contains(string(message.Content), "extract the webpage"))
			delegated = delegated || (message.Role == "user" && strings.Contains(string(message.Content), "delegate code search"))
			child = child || (message.Role == "user" && strings.Contains(string(message.Content), "child code search"))
			alternate = alternate || (message.Role == "user" && strings.Contains(string(message.Content), "alternate directory configuration"))
			if message.Role == "user" && strings.Contains(string(message.Content), "Create a **DRAFT** pull request") {
				pullRequest = true
				assert.Contains(t, string(message.Content), "RUNNER_ONLY_PR_TEMPLATE", "recipe template must be read from the runner CWD")
				assert.Contains(t, string(message.Content), "fixture-target")
			}
			if message.Role == "user" && strings.Contains(string(message.Content), "sdk typed options") {
				assert.Equal(t, "gpt-4o-mini", request.Model)
				assert.Empty(t, request.Tools)
			}
			if message.Role == "user" && strings.Contains(string(message.Content), "Return only the message, without Markdown fences.") {
				assert.Empty(t, request.Tools, "commit generation must not perform its own Git mutation")
				assert.Contains(t, string(message.Content), "APPROVED_RUNNER_COMMIT_CONTENT")
				text = "feat: commit runner snapshot"
			}
		}
		assert.Empty(t, pending, "live forks must not submit incomplete tool-call history")
		delegated = delegated && !child
		if delegated {
			for _, tool := range request.Tools {
				name := tool["function"].(map[string]any)["name"]
				assert.NotContains(t, []string{"file_read", "grep_tool", "glob_tool"}, name, "parent patch-mode and agent.init presentation filters must be active")
			}
		}
		if alternate {
			assert.Equal(t, "gpt-4o", request.Model, "repository model settings must not reach the daemon")
			require.Len(t, request.Tools, 1, "the new CWD restricts tools and disables startup-directory extensions")
			assert.Equal(t, "file_read", request.Tools[0]["function"].(map[string]any)["name"])
			assert.Contains(t, string(request.Messages[0].Content), "ALTERNATE_DIRECTORY_CONTEXT")
			assert.NotContains(t, string(request.Messages[0].Content), "WORKSPACE_CONTEXT_MUST_NOT_REACH_HELPER")
		}
		if child {
			childGate(r.Context(), false)
			assert.Equal(t, "gpt-4o", request.Model, "ordinary forks retain the parent's model snapshot")
			var names []string
			for _, tool := range request.Tools {
				if fn, ok := tool["function"].(map[string]any); ok {
					name, _ := fn["name"].(string)
					names = append(names, name)
				}
			}
			assert.ElementsMatch(t, []string{"file_read", "grep_tool", "glob_tool"}, names)
			assert.Contains(t, string(request.Messages[0].Content), daemonACPSearchPrompt)
		}
		if helper {
			helperCalls.Add(1)
			assert.Empty(t, request.Tools, "central extraction cannot expose model-callable tools")
			for _, message := range request.Messages {
				assert.NotContains(t, string(message.Content), "WORKSPACE_CONTEXT_MUST_NOT_REACH_HELPER")
				assert.NotContains(t, string(message.Content), filepath.Dir(filePath))
			}
			text = "runner-page-evidence"
		}
		if len(request.Tools) > 0 {
			found := false
			for _, message := range request.Messages[lastUser:] {
				if message.Role == "tool" {
					expected := "runner-file-evidence"
					if extraction {
						expected = "runner-page-evidence"
					}
					if pullRequest {
						expected = "https://github.example/fixture/pull/1"
						assert.Contains(t, string(message.Content), "feat: commit runner snapshot", "Git inspection must use the selected runner repository")
					}
					if alternate {
						expected = "alternate-directory-evidence"
					}
					assert.Contains(t, string(message.Content), expected)
					toolResults.Add(1)
					found = true
				}
			}
			if found {
				text = "runner-file-evidence"
				if extraction {
					text = "runner-page-evidence"
				}
				if pullRequest {
					text = "https://github.example/fixture/pull/1"
				}
				if alternate {
					text = "alternate-directory-evidence"
				}
			} else {
				name := "file_read"
				input := map[string]any{"file_path": filePath}
				if alternate {
					input["file_path"] = filepath.Join(filepath.Dir(filepath.Dir(filePath)), "alternate-workspace", "marker.txt")
				}
				if extraction {
					name = "web_fetch"
					input = map[string]any{"url": pageURL, "prompt": "Extract the evidence marker"}
				}
				if delegated {
					name = "code_search"
					input = map[string]any{}
				}
				if pullRequest {
					name = "bash"
					command := fmt.Sprintf(`git status --porcelain &&
git log -1 --format=%%s &&
%q pr create --base fixture-target --draft --title 'Runner PR' --body 'RUNNER_ONLY_PR_TEMPLATE'`,
						filepath.Join(filepath.Dir(filePath), "fixture-gh"))
					input = map[string]any{
						"command":     command,
						"description": "Inspect runner repository and create draft request",
						"timeout":     10,
					}
				}
				arguments, _ := json.Marshal(input)
				delta = map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"index": 0,
						"id":    "call-read",
						"type":  "function",
						"function": map[string]any{
							"name":      name,
							"arguments": string(arguments),
						},
					}},
				}
				finish = "tool_calls"
			}
		}
		if !request.Stream {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "utility",
				"object": "chat.completion",
				"model":  "gpt-4o",
				"choices": []any{map[string]any{
					"index":         0,
					"message":       map[string]string{"role": "assistant", "content": text},
					"finish_reason": "stop",
				}},
			})
			return
		}
		if delta == nil {
			delta = map[string]any{"role": "assistant", "content": text}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if child && finish == "stop" {
			// Send live content, then hold the provider open until observers receive
			// it. A test relying only on durable history cannot pass this gate.
			chunk := map[string]any{
				"id":     "completion",
				"object": "chat.completion.chunk",
				"model":  request.Model,
				"choices": []any{map[string]any{
					"index":         0,
					"delta":         delta,
					"finish_reason": nil,
				}},
			}
			encoded, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
			w.(http.Flusher).Flush()
			childGate(r.Context(), true)
			delta = map[string]any{}
		}
		chunk := map[string]any{
			"id":     "completion",
			"object": "chat.completion.chunk",
			"model":  "gpt-4o",
			"choices": []any{map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finish,
			}},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
}

func daemonSearchResponses(
	t *testing.T,
	w http.ResponseWriter,
	r *http.Request,
	filePath string,
	toolResults *atomic.Int32,
	childGate func(context.Context, bool),
) {
	t.Helper()
	var request struct {
		Model        string `json:"model"`
		Stream       bool   `json:"stream"`
		Instructions string `json:"instructions"`
		Reasoning    struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Input []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
			CallID  string          `json:"call_id"`
			Output  json.RawMessage `json:"output"`
		} `json:"input"`
	}
	if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	assert.Equal(t, "gpt-5.6-luna", request.Model)
	assert.Equal(t, "none", request.Reasoning.Effort)
	assert.True(t, request.Stream)
	assert.Contains(t, request.Instructions, daemonACPSearchPrompt)
	var names []string
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{"file_read", "grep_tool", "glob_tool"}, names)
	var found, child bool
	for _, item := range request.Input {
		child = child || strings.Contains(string(item.Content), "child code search")
		if item.Type == "function_call_output" {
			assert.Equal(t, "call-read", item.CallID)
			assert.Contains(t, string(item.Output), "runner-file-evidence")
			toolResults.Add(1)
			found = true
		}
	}
	assert.True(t, child)
	childGate(r.Context(), false)
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(event map[string]any) {
		encoded, err := json.Marshal(event)
		assert.NoError(t, err)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
	}
	if found {
		emit(map[string]any{"type": "response.output_text.delta", "delta": "runner-file-evidence"})
		w.(http.Flusher).Flush()
		childGate(r.Context(), true)
		emit(map[string]any{"type": "response.output_item.done", "item": map[string]any{
			"type":    "message",
			"role":    "assistant",
			"status":  "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "runner-file-evidence"}},
		}})
	} else {
		arguments, err := json.Marshal(map[string]string{"file_path": filePath})
		assert.NoError(t, err)
		emit(map[string]any{"type": "response.output_item.done", "item": map[string]any{
			"type":      "function_call",
			"call_id":   "call-read",
			"name":      "file_read",
			"arguments": string(arguments),
		}})
	}
	emit(map[string]any{"type": "response.completed", "response": map[string]any{
		"id":     "search-response",
		"status": "completed",
		"model":  request.Model,
		"usage": map[string]any{
			"input_tokens":          10,
			"output_tokens":         5,
			"total_tokens":          15,
			"input_tokens_details":  map[string]int{"cached_tokens": 0},
			"output_tokens_details": map[string]int{"reasoning_tokens": 0},
		},
	}})
}
