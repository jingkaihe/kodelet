package main

import (
	"bufio"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
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
	for name, output := range map[string]string{"rg": "ripgrep " + binaries.RipgrepVersion, "fd": "fd " + binaries.FdVersion} {
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\necho '"+output+"'\n"), 0o700))
	}
	config := fmt.Sprintf("provider: openai\nmodel: gpt-4o\nweak_model: gpt-4o\nmax_tokens: 256\nopenai:\n  platform: openai\n  base_url: %s\n  api_mode: chat_completions\n  api_key_env_var: KODELET_TEST_PROVIDER_KEY\nextensions:\n  enabled: false\nskills:\n  enabled: false\nserve:\n  host: 127.0.0.1\n  port: 0\n", provider.URL)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte(config), 0o600))
	environment := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "SHELL=/bin/sh", "KODELET_BASE_PATH=" + os.Getenv("KODELET_BASE_PATH"), "KODELET_TEST_CLI_PROCESS=1", "KODELET_TEST_PROVIDER_KEY=daemon-only-key"}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	cli := func(cwd string, args ...string) *exec.Cmd { return daemonCLIProcess(ctx, t, cwd, environment, args...) }
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
		go func() { _, _ = io.Copy(&screen, terminal); close(readDone) }()
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
		require.Eventually(t, func() bool { return strings.Contains(screen.String(), "extensions · ") }, 10*time.Second, 20*time.Millisecond)
		_, err = io.WriteString(terminal, "cold chat query\r")
		require.NoError(t, err)
		require.Eventually(t, func() bool { return strings.Contains(screen.String(), "tool-free answer") }, 10*time.Second, 20*time.Millisecond)
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
		require.NoError(t, json.NewEncoder(input).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}}))
		var response map[string]any
		require.NoError(t, json.NewDecoder(outputPipe).Decode(&response), "ACP stdout must contain only JSON-RPC")
		assert.Equal(t, float64(1), response["id"])
		assert.NotNil(t, response["result"])
		require.NoError(t, input.Close())
		require.NoError(t, process.Wait(), "%s", diagnostics.String())
		assert.Contains(t, diagnostics.String(), "Starting local Kodelet server")
	})
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
			response = chat.ControlPlaneChatSettings{CurrentProfile: "default", DefaultRunnerID: "runner", DefaultRunnerReady: true}
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
		"HOME=" + root, "PATH=" + os.Getenv("PATH"), "SHELL=/bin/sh",
		"KODELET_BASE_PATH=" + filepath.Join(root, "state"), "KODELET_TEST_CLI_PROCESS=1",
		"TERM=xterm-256color", "COLORTERM=truecolor",
	}, "chat", "--server="+daemon.URL, "--auth-token=client-secret", "--cwd="+root)
	terminal, err := pty.StartWithSize(process, &pty.Winsize{Rows: 40, Cols: 120})
	require.NoError(t, err)
	var screen daemonChatPTYOutput
	readDone, processDone := make(chan struct{}), make(chan error, 1)
	go func() { _, _ = io.Copy(&screen, terminal); close(readDone) }()
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
		require.Eventually(t, func() bool { return strings.Contains(screen.String(), text) }, 5*time.Second, 10*time.Millisecond, "terminal did not render %q", text)
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
	require.Eventually(t, func() bool { return readyLabel.MatchString(screen.String()) }, 5*time.Second, 10*time.Millisecond, "terminal did not render elapsed readiness")
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
	for _, placement := range []string{"standalone", "embedded"} {
		t.Run(placement, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
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
			script := fmt.Sprintf("#!/bin/sh\nKODELET_TEST_CHILD_EXTENSION=1 exec %q -test.run '^TestDaemonChildExtensionProcess$'\n", executable)
			if sdk := os.Getenv("KODELET_TEST_EXTENSION_SDK"); sdk != "" {
				script = daemonSDKSearchExtension(t, extensionDir, sdk)
			}
			require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "kodelet-extension-search"), []byte(script), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "search-prompt.txt"), []byte("RUNNER_OWNED_CODE_SEARCH_PROMPT"), 0o600))
			webPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, "<p>runner-page-evidence</p>")
			}))
			defer webPage.Close()
			var toolResults, helperCalls atomic.Int32
			background := os.Getenv("KODELET_TEST_CHILD_BACKGROUND") == "1"
			childStarted, releaseChild := make(chan struct{}), make(chan struct{})
			var started sync.Once
			provider := daemonTestProvider(t, filePath, webPage.URL, &toolResults, &helperCalls, func(ctx context.Context) {
				if !background {
					return
				}
				started.Do(func() { close(childStarted) })
				select {
				case <-releaseChild:
				case <-ctx.Done():
					assert.Fail(t, "retained child's provider request was cancelled before release")
				}
			})
			defer provider.Close()
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
			viper.Set("openai", map[string]any{"platform": "openai", "base_url": provider.URL, "api_key_env_var": "KODELET_TEST_PROVIDER_KEY", "api_mode": "chat_completions"})
			viper.Set("extensions.enabled", true)
			viper.Set("skills.enabled", false)
			viper.Set("allowed_tools", []string{"file_read", "web_fetch", "grep_tool", "glob_tool", "code_search", "bash"})
			t.Setenv("KODELET_TEST_PROVIDER_KEY", "daemon-only-key")
			t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-store"))
			require.NoError(t, db.RunMigrations(ctx, migrations.All()))
			config := &controlplane.ServerConfig{Host: "127.0.0.1", Port: 0, CompactRatio: 0.8, AuthToken: "client-secret", RunnerAuthToken: "runner-secret"}
			settings := map[string]any{"tool_mode": "full", "enable_fs_search_tools": true, "allowed_tools": []string{"file_read", "web_fetch", "grep_tool", "glob_tool", "code_search", "bash"}, "extensions": map[string]any{"enabled": true}, "skills": map[string]any{"enabled": false}}
			if placement == "embedded" {
				store, err := localstate.NewStore()
				require.NoError(t, err)
				config.EmbeddedRunner = &controlplane.EmbeddedRunnerConfig{Workspace: workspace, Settings: settings, Store: store}
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
			childEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1"}
			if placement == "standalone" {
				data, err := json.Marshal(settings)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(workspace, "kodelet-config.yaml"), data, 0o600))
				runnerCtx, stopRunner := context.WithCancel(ctx)
				process := daemonCLIProcess(runnerCtx, t, workspace, append(childEnv, "KODELET_BASE_PATH="+filepath.Join(root, "runner-state")), "runner", "start", "--server="+serverURL, "--auth-token=runner-secret", "--name=acceptance-runner")
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
					if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle {
						runnerID = runner.ID
						return true
					}
				}
				return false
			}, 15*time.Second, 50*time.Millisecond)
			invalidStore := filepath.Join(root, "client-store-is-a-file")
			require.NoError(t, os.WriteFile(invalidStore, []byte("no client database"), 0o600))
			clientEnv := append(childEnv, "KODELET_BASE_PATH="+invalidStore)
			for _, scenario := range []string{"file", "no-tools", "extraction", "delegation"} {
				args := []string{"run", "--server=" + serverURL, "--auth-token=client-secret", "--cwd=" + workspace, "--result-only"}
				if placement == "standalone" {
					args = append(args, "--runner="+runnerID)
				}
				if scenario == "no-tools" {
					args = append(args, "--no-tools")
				}
				message := "inspect the runner file"
				if scenario == "extraction" {
					message = "extract the webpage"
				}
				if scenario == "delegation" {
					message = "delegate code search"
				}
				args = append(args, message)
				process := daemonCLIProcess(ctx, t, root, clientEnv, args...)
				var stdout, stderr bytes.Buffer
				process.Stdout, process.Stderr = &stdout, &stderr
				err := process.Run()
				require.NoError(t, err, "client stderr: %s", stderr.String())
				switch scenario {
				case "no-tools":
					assert.Equal(t, "tool-free answer\n", stdout.String())
				case "extraction":
					assert.Equal(t, "runner-page-evidence\n", stdout.String())
				default:
					assert.Equal(t, "runner-file-evidence\n", stdout.String())
				}
				if background && scenario == "delegation" {
					select {
					case <-childStarted:
					case <-ctx.Done():
						t.Fatal("retained child did not start")
					}
					// The parent CLI has exited, while the child provider call is
					// still blocked. Only the explicit active lease keeps it alive.
					close(releaseChild)
					require.Eventually(t, func() bool { return toolResults.Load() == 4 }, 5*time.Second, 10*time.Millisecond)
				}
			}
			assert.EqualValues(t, 4, toolResults.Load())
			assert.EqualValues(t, 1, helperCalls.Load(), "the active runner tool delegates exactly one provider call to the daemon")
			client, err := chat.NewClient(serverURL, "client-secret", runnerID)
			require.NoError(t, err)
			if background {
				require.Eventually(t, func() bool {
					summaries, err := client.ListConversationsInCWD(ctx, 10, workspace)
					if err != nil {
						return false
					}
					for _, summary := range summaries {
						if summary.FirstMessage != "child code search" {
							continue
						}
						stored, err := client.LoadConversation(ctx, summary.ID)
						if err != nil {
							return false
						}
						for _, message := range stored.Messages {
							if message.Role == "assistant" && message.Kind == "text" && message.Content == "runner-file-evidence" {
								return true
							}
						}
					}
					return false
				}, 5*time.Second, 10*time.Millisecond, "wait for durable child completion, not just the provider receiving its last request")
			}
			history, err := client.ListConversationsInCWD(ctx, 10, workspace)
			require.NoError(t, err)
			require.Len(t, history, 5, "four parent runs and one durable child; no extraction helper conversation")
			extractionFound := false
			childFound := false
			var childID string
			for _, summary := range history {
				stored, err := client.LoadConversation(ctx, summary.ID)
				require.NoError(t, err)
				assert.Equal(t, workspace, stored.CWD)
				assert.Equal(t, runnerID, stored.RunnerID)
				assert.NotEmpty(t, stored.Messages)
				assert.Positive(t, stored.Usage.OutputTokens)
				if summary.FirstMessage == "extract the webpage" {
					extractionFound = true
					assert.Equal(t, 15, stored.Usage.OutputTokens, "two parent exchanges plus one helper exchange")
					for _, message := range stored.Messages {
						assert.NotContains(t, message.Content, "Extraction request:", "temporary helper input must not enter parent history")
					}
				}
				if summary.FirstMessage == "child code search" {
					childFound = true
					childID = summary.ID
					identity, ok := summary.Metadata["delegation"].(map[string]any)
					require.True(t, ok)
					assert.Equal(t, summary.ID, identity["conversationId"])
					assert.NotEmpty(t, identity["runId"])
					assert.NotEqual(t, identity["parentConversationId"], identity["conversationId"])
					assert.NotEqual(t, identity["parentRunId"], identity["runId"])
					assert.Equal(t, 10, stored.Usage.OutputTokens)
				}
				if summary.FirstMessage == "delegate code search" {
					if background {
						assert.Equal(t, 10, stored.Usage.OutputTokens, "retained child usage belongs to its own conversation")
					} else {
						assert.Equal(t, 20, stored.Usage.OutputTokens, "parent aggregates foreground child's two exchanges")
					}
					for _, message := range stored.Messages {
						assert.NotContains(t, message.Content, "RUNNER_OWNED_CODE_SEARCH_PROMPT")
					}
				}
			}
			assert.True(t, extractionFound, "extraction history and usage assertions must run")
			assert.True(t, childFound, "child has its own persisted conversation")
			// The child's persisted preset, not the currently installed extension,
			// controls a later independent user turn on this durable conversation.
			require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "search-prompt.txt"), []byte("CHANGED_PROMPT_MUST_NOT_REPLACE_SNAPSHOT"), 0o600))
			resume := daemonCLIProcess(ctx, t, root, clientEnv, "run", "--server="+serverURL, "--auth-token=client-secret", "--runner="+runnerID, "--resume="+childID, "--result-only", "continue child code search")
			output, err := resume.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Equal(t, "runner-file-evidence\n", string(output))
			denied := daemonCLIProcess(ctx, t, root, clientEnv, "run", "--server="+serverURL, "--auth-token=client-secret", "--runner="+runnerID, "--resume="+childID, "--no-extensions=false", "must not run")
			output, err = denied.CombinedOutput()
			require.Error(t, err)
			assert.Contains(t, string(output), "cannot relax parent noExtensions")
			if sdk := os.Getenv("KODELET_TEST_EXTENSION_SDK"); sdk != "" {
				daemonSDKClient(ctx, t, root, workspace, sdk, serverURL, runnerID, clientEnv)
			}
			// The commit client has no Git executable, provider credentials or local
			// database. Both preparation and confirmed mutation use this runner.
			git := func(args ...string) string {
				command := exec.CommandContext(ctx, "git", args...)
				command.Dir = workspace
				output, err := command.CombinedOutput()
				require.NoError(t, err, "%s", output)
				return strings.TrimSpace(string(output))
			}
			git("init")
			git("config", "user.name", "Runner Commit User")
			git("config", "user.email", "commit@example.com")
			git("config", "commit.gpgsign", "false")
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "commit-evidence.txt"), []byte("APPROVED_RUNNER_COMMIT_CONTENT\n"), 0o600))
			git("add", "commit-evidence.txt")
			commitArgs := []string{"commit", "--server=" + serverURL, "--auth-token=client-secret", "--cwd=" + workspace, "--no-confirm"}
			if placement == "standalone" {
				commitArgs = append(commitArgs, "--runner="+runnerID)
			}
			commit := daemonCLIProcess(ctx, t, root, append(clientEnv, "PATH="+root), commitArgs...)
			output, err = commit.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Contains(t, string(output), "Commit created:")
			assert.Contains(t, git("log", "-1", "--format=%B"), "feat: commit runner snapshot")
			assert.Contains(t, git("log", "-1", "--format=%B"), "Signed-off-by: Runner Commit User <commit@example.com>")
			assert.Equal(t, "APPROVED_RUNNER_COMMIT_CONTENT", git("show", "HEAD:commit-evidence.txt"))
			assert.Empty(t, git("diff", "--cached"))
			// Exercise the actual recipe, template expansion and runner shell. Only
			// the final GitHub service command is replaced, avoiding a public mutation.
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "custom-pr-template.md"), []byte("RUNNER_ONLY_PR_TEMPLATE"), 0o600))
			gh := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" >> gh-invocations\nprintf '%s\\n' 'https://github.example/fixture/pull/1'\n"
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "fixture-gh"), []byte(gh), 0o700))
			prArgs := []string{"pr", "--server=" + serverURL, "--auth-token=client-secret", "--cwd=" + workspace, "--provider=github", "--target=fixture-target", "--draft", "--template-file=custom-pr-template.md", "--result-only"}
			if placement == "standalone" {
				prArgs = append(prArgs, "--runner="+runnerID)
			}
			pr := daemonCLIProcess(ctx, t, root, append(clientEnv, "PATH="+root), prArgs...)
			output, err = pr.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Equal(t, "https://github.example/fixture/pull/1\n", string(output))
			invocations, err := os.ReadFile(filepath.Join(workspace, "gh-invocations"))
			require.NoError(t, err)
			physicalWorkspace, err := filepath.EvalSymlinks(workspace)
			require.NoError(t, err)
			assert.Equal(t, physicalWorkspace+"\npr\ncreate\n--base\nfixture-target\n--draft\n--title\nRunner PR\n--body\nRUNNER_ONLY_PR_TEMPLATE\n", string(invocations), "one runner-host mutation with the requested target and template")
			alternateCWD := filepath.Join(root, "alternate-workspace")
			require.NoError(t, os.MkdirAll(alternateCWD, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(alternateCWD, "marker.txt"), []byte("alternate-directory-evidence"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(alternateCWD, "AGENTS.md"), []byte("ALTERNATE_DIRECTORY_CONTEXT"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(alternateCWD, "kodelet-config.yaml"), []byte("tool_mode: full\nallowed_tools: [file_read]\nextensions:\n  enabled: false\nskills:\n  enabled: false\nmodel: forbidden-repository-model\n"), 0o600))
			alternate := daemonCLIProcess(ctx, t, root, clientEnv, "run", "--server="+serverURL, "--auth-token=client-secret", "--runner="+runnerID, "--cwd="+alternateCWD, "--result-only", "alternate directory configuration")
			output, err = alternate.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Equal(t, "alternate-directory-evidence\n", string(output))
			history, err = client.ListConversationsInCWD(ctx, 10, alternateCWD)
			require.NoError(t, err)
			require.Len(t, history, 1)
			assert.Equal(t, alternateCWD, history[0].CWD)
			if placement == "embedded" {
				// A later same-installation client starts in a different repository.
				// Omitted --cwd must use that directory, not serve's startup workspace.
				invokingCWD := filepath.Join(root, "client-invoking-workspace")
				require.NoError(t, os.MkdirAll(invokingCWD, 0o700))
				localEnv := []string{"PATH=" + root, "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH=" + filepath.Join(root, "daemon-store"), "KODELET_SERVER=" + serverURL, "KODELET_AUTH_TOKEN=client-secret"}
				process := daemonCLIProcess(ctx, t, invokingCWD, localEnv, "run", "--no-tools", "--result-only", "same installation current directory")
				output, err := process.CombinedOutput()
				require.NoError(t, err, "%s", output)
				assert.Equal(t, "tool-free answer\n", string(output))
				history, err := client.ListConversationsInCWD(ctx, 10, invokingCWD)
				require.NoError(t, err)
				require.Len(t, history, 1)
				assert.Equal(t, invokingCWD, history[0].CWD)
			}
		})
	}
}

// Opt-in cross-repository SDK gate: build sdk/dist first, or set
// KODELET_PYTHON_SDK_PATH to a uv-synced Python SDK checkout. The ordinary Go
// suite always runs the credential-free protocol fixture above.
func daemonSDKSearchExtension(t *testing.T, dir, sdk string) string {
	t.Helper()
	background := os.Getenv("KODELET_TEST_CHILD_BACKGROUND") == "1"
	var executable, source, name string
	switch sdk {
	case "typescript":
		var err error
		executable, err = exec.LookPath("node")
		require.NoError(t, err)
		dist, err := filepath.Abs("../../sdk/dist")
		require.NoError(t, err)
		name = "search.mjs"
		source = fmt.Sprintf(`import { defineExtension, z } from %q;
import { runExtension } from %q;
await runExtension(defineExtension(ext => {
  ext.registerProfile({ name: "code_search", systemPromptPath: "search-prompt.txt", options: {
    model: "gpt-4o-mini", allowedTools: ["file_read", "grep_tool", "glob_tool"],
    noExtensions: true, noSkills: true, enableFSSearchTools: true, maxTurns: 3,
  }});
  ext.registerTool({ name: "code_search", description: "Run restricted code search", inputSchema: z.object({}),
    async execute(_input, ctx) {
      const lease = %t ? await ctx.acquireBackgroundTask("retained search") : undefined;
      const child = await ctx.children.start({ profile: "code_search", message: "child code search", requestId: "search-once", lease });
      if (lease) {
        void child.wait().finally(() => lease.close());
        return "runner-file-evidence";
      }
      const result = await child.wait({ signal: ctx.signal, onEvent: event => ctx.update(event.text ?? event.kind) });
      return result.output;
    }
  });
}));
`, "file://"+filepath.Join(dist, "index.js"), "file://"+filepath.Join(dist, "runtime.js"), background)
	case "python":
		root := os.Getenv("KODELET_PYTHON_SDK_PATH")
		require.NotEmpty(t, root, "set KODELET_PYTHON_SDK_PATH to a uv-synced SDK checkout")
		executable = filepath.Join(root, ".venv", "bin", "python")
		name = "search.py"
		source = fmt.Sprintf(`import asyncio
from kodelet_sdk import BaseModel, Extension
from kodelet_sdk.runtime import run_extension
ext = Extension(name="code_search")
tasks = set()
ext.register_profile({"name": "code_search", "systemPromptPath": "search-prompt.txt", "options": {
    "model": "gpt-4o-mini", "allowedTools": ["file_read", "grep_tool", "glob_tool"],
    "noExtensions": True, "noSkills": True, "enableFSSearchTools": True, "maxTurns": 3,
}})
class Input(BaseModel):
    pass
@ext.tool("code_search", description="Run restricted code search", input_schema=Input)
async def search(_input, ctx):
    lease = await ctx.acquire_background_task("retained search") if %s else None
    child = await ctx.children.start(profile="code_search", message="child code search", request_id="search-once", lease=lease)
    if lease:
        async def finish():
            try:
                await child.wait()
            finally:
                await lease.close()
        task = asyncio.create_task(finish())
        tasks.add(task)
        task.add_done_callback(tasks.discard)
        return "runner-file-evidence"
    result = await child.wait(on_event=lambda event: ctx.update(event.get("text") or event["kind"]))
    return result["output"]
asyncio.run(run_extension(ext))
`, map[bool]string{true: "True", false: "False"}[background])
	default:
		t.Fatalf("unknown extension SDK %q", sdk)
	}
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return fmt.Sprintf("#!/bin/sh\nexec %q %q\n", executable, path)
}

func daemonSDKClient(ctx context.Context, t *testing.T, root, workspace, sdk, server, runner string, environment []string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(root, "sdk-kodelet")
	require.NoError(t, os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nKODELET_TEST_CLI_PROCESS=1 exec %q -test.run '^TestDaemonFirstCLIProcess$' -- \"$@\"\n", executable)), 0o700))
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

func daemonTestProvider(t *testing.T, filePath, pageURL string, toolResults, helperCalls *atomic.Int32, childGate func(context.Context)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer daemon-only-key", r.Header.Get("Authorization"))
		var request struct {
			Model    string           `json:"model"`
			Stream   bool             `json:"stream"`
			Tools    []map[string]any `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
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
		for _, message := range request.Messages {
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
		if alternate {
			assert.Equal(t, "gpt-4o", request.Model, "repository model settings must not reach the daemon")
			require.Len(t, request.Tools, 1, "the new CWD restricts tools and disables startup-directory extensions")
			assert.Equal(t, "file_read", request.Tools[0]["function"].(map[string]any)["name"])
			assert.Contains(t, string(request.Messages[0].Content), "ALTERNATE_DIRECTORY_CONTEXT")
			assert.NotContains(t, string(request.Messages[0].Content), "WORKSPACE_CONTEXT_MUST_NOT_REACH_HELPER")
		}
		if child {
			childGate(r.Context())
			assert.Equal(t, "gpt-4o-mini", request.Model)
			var names []string
			for _, tool := range request.Tools {
				if fn, ok := tool["function"].(map[string]any); ok {
					name, _ := fn["name"].(string)
					names = append(names, name)
				}
			}
			assert.ElementsMatch(t, []string{"file_read", "grep_tool", "glob_tool"}, names)
			assert.Contains(t, string(request.Messages[0].Content), "RUNNER_OWNED_CODE_SEARCH_PROMPT")
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
			for _, message := range request.Messages {
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
					input = map[string]any{"command": fmt.Sprintf("git status --porcelain && git log -1 --format=%%s && %q pr create --base fixture-target --draft --title 'Runner PR' --body 'RUNNER_ONLY_PR_TEMPLATE'", filepath.Join(filepath.Dir(filePath), "fixture-gh")), "description": "Inspect runner repository and create draft request", "timeout": 10}
				}
				arguments, _ := json.Marshal(input)
				delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call-read", "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}}}}
				finish = "tool_calls"
			}
		}
		if !request.Stream {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "utility", "object": "chat.completion", "model": "gpt-4o", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
			return
		}
		if delta == nil {
			delta = map[string]any{"role": "assistant", "content": text}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{"id": "completion", "object": "chat.completion.chunk", "model": "gpt-4o", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
}

// The fixture is a runner-installed stdio extension. It intentionally has no
// access to the client's token and never launches kodelet or a local provider.
func TestDaemonChildExtensionProcess(_ *testing.T) {
	if os.Getenv("KODELET_TEST_CHILD_EXTENSION") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	read := func() (map[string]json.RawMessage, error) {
		length := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(line) == "" {
				break
			}
			if value, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				length, err = strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					return nil, err
				}
			}
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		var message map[string]json.RawMessage
		err := json.Unmarshal(data, &message)
		return message, err
	}
	write := func(value any) {
		data, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
	}
	next := 1000
	for {
		message, err := read()
		if err != nil {
			break
		}
		var method string
		_ = json.Unmarshal(message["method"], &method)
		var result any = map[string]any{}
		switch method {
		case "extension.initialize":
			result = extensions.InitializeResult{
				Name: "code_search", Tools: []extensions.ToolRegistration{{Name: "code_search", Description: "Run restricted code search", InputSchema: map[string]any{"type": "object"}}},
				Profiles: []delegation.Profile{{Name: "code_search", SystemPromptPath: "search-prompt.txt", Options: &llmtypes.ExecutionOptions{Model: new("gpt-4o-mini"), AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool"}), NoExtensions: new(true), NoSkills: new(true), EnableFSSearchTools: new(true), MaxTurns: new(3)}}},
			}
		case "extension.tool.execute":
			call := func(method string, params any) (delegation.Result, error) {
				next++
				write(map[string]any{"jsonrpc": "2.0", "id": next, "parentId": message["id"], "method": "kodelet." + method, "params": params})
				response, err := read()
				if err != nil {
					return delegation.Result{}, err
				}
				if raw := response["error"]; len(raw) > 0 && string(raw) != "null" {
					return delegation.Result{}, fmt.Errorf("host: %s", raw)
				}
				var child delegation.Result
				err = json.Unmarshal(response["result"], &child)
				return child, err
			}
			child, err := call(delegation.StartMethod, delegation.Request{RequestID: "search-once", Profile: "code_search", Message: "child code search"})
			deadline := time.Now().Add(15 * time.Second)
			for err == nil && !child.Done && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				child, err = call(delegation.ReadMethod, map[string]any{"childId": child.ConversationID})
			}
			if err != nil {
				result = map[string]any{"error": err.Error()}
			} else if child.Error != "" {
				result = map[string]any{"error": child.Error}
			} else if !child.Done {
				result = map[string]any{"error": "child timed out"}
			} else {
				result = map[string]any{"content": child.Output}
			}
		}
		write(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": result})
	}
	os.Exit(0)
}
