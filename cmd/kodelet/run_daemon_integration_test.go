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
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
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
	sdk := os.Getenv("KODELET_TEST_EXTENSION_SDK")
	for _, placement := range []string{"standalone", "embedded"} {
		t.Run(placement, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
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
			script := fmt.Sprintf("#!/bin/sh\nKODELET_TEST_ACP_EXTENSION=1 exec %q -test.run '^TestDaemonACPSearchExtensionProcess$'\n", executable)
			if sdk != "" {
				script = daemonSDKSearchExtension(t, extensionDir, sdk)
			}
			require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "kodelet-extension-search"), []byte(script), 0o700))
			webPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, "<p>runner-page-evidence</p>")
			}))
			defer webPage.Close()
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
			defer provider.Close()
			defer release()
			defer finish()
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
				"provider": "openai", "model": "gpt-4o", "reasoning_effort": "xhigh",
				"allowed_reasoning_efforts": []string{"high", "xhigh"},
			}})
			viper.Set("openai", map[string]any{"platform": "openai", "base_url": provider.URL, "api_key_env_var": "KODELET_TEST_PROVIDER_KEY", "api_mode": "chat_completions"})
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
			childEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_AUTH_TOKEN=client-secret", "KODELET_BIN=" + wrapper}
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
					if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle && runner.SessionExtensions {
						runnerID = runner.ID
						return true
					}
				}
				return false
			}, 15*time.Second, 50*time.Millisecond)
			require.NoError(t, os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nKODELET_SERVER=%q KODELET_TEST_CLI_PROCESS=1 exec %q -test.run '^TestDaemonFirstCLIProcess$' -- \"$@\"\n", serverURL, executable)), 0o700))
			client, err := chat.NewClient(serverURL, "client-secret", runnerID)
			require.NoError(t, err)
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
					args = append(args, "--profile=deep")
					// Use patch-mode settings and hide read/search tools in the parent's
					// agent.init hook. Explicit ACP options must select them independently.
					require.NoError(t, os.WriteFile(filepath.Join(workspace, "kodelet-config.yaml"), []byte("tool_mode: patch\n"), 0o600))
				}
				args = append(args, message)
				process := daemonCLIProcess(ctx, t, root, clientEnv, args...)
				var stdout, stderr bytes.Buffer
				process.Stdout, process.Stderr = &stdout, &stderr
				var err error
				if scenario == "delegation" {
					require.NoError(t, process.Start())
					done := make(chan error, 1)
					go func() { done <- process.Wait() }()
					select {
					case <-childStarted:
					case err := <-done:
						require.FailNow(t, "ACP search did not start", "error: %v; stderr: %s; stdout: %s", err, stderr.String(), stdout.String())
					case <-ctx.Done():
						require.FailNow(t, "ACP search did not reach the provider")
					}
					assertDaemonACPSearchBroadcast(ctx, t, client, serverURL, workspace, release, finish)
					select {
					case err = <-done:
					case <-ctx.Done():
						require.FailNow(t, "parent did not finish after ACP search")
					}
					settingsJSON, marshalErr := json.Marshal(settings)
					require.NoError(t, marshalErr)
					require.NoError(t, os.WriteFile(filepath.Join(workspace, "kodelet-config.yaml"), settingsJSON, 0o600))
				} else {
					err = process.Run()
				}
				require.NoError(t, err, "client stderr: %s", stderr.String())
				switch scenario {
				case "no-tools":
					assert.Equal(t, "tool-free answer\n", stdout.String())
				case "extraction":
					assert.Equal(t, "runner-page-evidence\n", stdout.String())
				default:
					assert.Equal(t, "runner-file-evidence\n", stdout.String())
				}
			}
			assert.EqualValues(t, 4, toolResults.Load())
			assert.EqualValues(t, 1, helperCalls.Load(), "the active runner tool delegates exactly one provider call to the daemon")
			history, err := client.ListConversationsInCWD(ctx, 10, workspace)
			require.NoError(t, err)
			require.Len(t, history, 5, "four parent runs and one durable child; no extraction helper conversation")
			extractionFound := false
			childFound := false
			parentFound := false
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
				if summary.Summary == daemonACPSearchName {
					childFound = true
					record, err := client.LoadConversationRecord(ctx, summary.ID)
					require.NoError(t, err)
					assert.Equal(t, daemonACPSearchName, record.Summary, "normal persistence must retain the explicit worker name")
					assert.Contains(t, string(record.RawMessages), "delegate code search", "live fork retains parent history")
					assert.Contains(t, string(record.RawMessages), "child code search")
					assert.Contains(t, string(record.RawMessages), "runner-file-evidence")
				}
				if sdk != "" && summary.Metadata["profile"] == "code-search" {
					childFound = true
					record, err := client.LoadConversationRecord(ctx, summary.ID)
					require.NoError(t, err)
					snapshot, present, err := conversations.ConfigSnapshotFromMetadata(record.Metadata)
					require.NoError(t, err)
					require.True(t, present)
					assert.Equal(t, "code-search", snapshot.Profile)
					assert.True(t, snapshot.ExtensionProfile)
					assert.Equal(t, "gpt-5.6-luna", snapshot.Model)
					assert.Equal(t, "none", snapshot.ReasoningEffort)
					assert.Equal(t, llmtypes.OpenAIAPIModeResponses, snapshot.OpenAI.APIMode)
					assert.Equal(t, llmtypes.OpenAIServiceTierFast, snapshot.OpenAI.ServiceTier)
					assert.Contains(t, string(record.RawMessages), "child code search")
					assert.Contains(t, string(record.RawMessages), "runner-file-evidence")
					assert.NotContains(t, string(record.RawMessages), "delegate code search")
				}
				if summary.FirstMessage == "delegate code search" && summary.Summary != daemonACPSearchName {
					parentFound = true
					snapshot, present, err := conversations.ConfigSnapshotFromMetadata(summary.Metadata)
					require.NoError(t, err)
					require.True(t, present)
					assert.Equal(t, "deep", snapshot.Profile)
					assert.Equal(t, "gpt-4o", snapshot.Model)
					assert.Equal(t, "xhigh", snapshot.ReasoningEffort)
					assert.Equal(t, 10, stored.Usage.OutputTokens, "ordinary ACP session usage is not charged to the parent again")
					if sdk != "" {
						record, err := client.LoadConversationRecord(ctx, summary.ID)
						require.NoError(t, err)
						assert.Contains(t, string(record.RawMessages), "extension-runner:"+runnerID, "SDK context metadata must identify the selected runner")
					}
					for _, message := range stored.Messages {
						assert.NotContains(t, message.Content, daemonACPSearchPrompt)
					}
				}
			}
			assert.True(t, extractionFound, "extraction history and usage assertions must run")
			assert.True(t, childFound, "child has its own persisted conversation")
			assert.True(t, parentFound, "parent profile, runner metadata and usage assertions must run")
			if sdk != "" {
				profileSettings, err := client.ChatSettings(ctx, "code-search")
				require.NoError(t, err)
				assert.Equal(t, "code-search", profileSettings.CurrentProfile)
				assert.Equal(t, "none", profileSettings.ReasoningEffort)
				for _, profile := range profileSettings.Profiles {
					assert.NotEqual(t, "code-search", profile.Name, "hidden profiles must not appear in normal pickers")
				}
				parentSettings, err := client.ChatSettings(ctx, "deep")
				require.NoError(t, err)
				assert.Equal(t, "xhigh", parentSettings.ReasoningEffort)
				assert.Equal(t, []string{"high", "xhigh"}, parentSettings.ReasoningEffortOptions)
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
			historyBeforeCommit, err := client.ListConversationsInCWD(ctx, 100, workspace)
			require.NoError(t, err)
			commit := daemonCLIProcess(ctx, t, root, append(clientEnv, "PATH="+root), commitArgs...)
			output, err := commit.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Contains(t, string(output), "Commit created successfully!")
			historyAfterCommit, err := client.ListConversationsInCWD(ctx, 100, workspace)
			require.NoError(t, err)
			assert.ElementsMatch(t, historyBeforeCommit, historyAfterCommit, "successful commits should remove only their temporary conversation")
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
// suite always runs the ordinary ACP protocol fixture without an external SDK.
func daemonSDKSearchExtension(t *testing.T, dir, sdk string) string {
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
    reasoningEffort: "none",
    openai: {
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
        const session = await client.createSession({ profile, options: {
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
`, "file://"+filepath.Join(dist, "index.js"), "file://"+filepath.Join(dist, "runtime.js"), daemonACPSearchPrompt)
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
    reasoning_effort="none",
    openai={
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
`, daemonACPSearchPrompt)
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

func daemonTestProvider(t *testing.T, filePath, pageURL string, toolResults, helperCalls *atomic.Int32, childGate func(context.Context, bool)) *httptest.Server {
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
		if child && finish == "stop" {
			// Send live content, then hold the provider open until observers receive
			// it. A test relying only on durable history cannot pass this gate.
			chunk := map[string]any{"id": "completion", "object": "chat.completion.chunk", "model": request.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
			encoded, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
			w.(http.Flusher).Flush()
			childGate(r.Context(), true)
			delta = map[string]any{}
		}
		chunk := map[string]any{"id": "completion", "object": "chat.completion.chunk", "model": "gpt-4o", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
}

func daemonSearchResponses(t *testing.T, w http.ResponseWriter, r *http.Request, filePath string, toolResults *atomic.Int32, childGate func(context.Context, bool)) {
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
			"type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "runner-file-evidence"}},
		}})
	} else {
		arguments, err := json.Marshal(map[string]string{"file_path": filePath})
		assert.NoError(t, err)
		emit(map[string]any{"type": "response.output_item.done", "item": map[string]any{
			"type": "function_call", "call_id": "call-read", "name": "file_read", "arguments": string(arguments),
		}})
	}
	emit(map[string]any{"type": "response.completed", "response": map[string]any{
		"id": "search-response", "status": "completed", "model": request.Model,
		"usage": map[string]any{
			"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
			"input_tokens_details":  map[string]int{"cached_tokens": 0},
			"output_tokens_details": map[string]int{"reasoning_tokens": 0},
		},
	}})
}
