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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is the public main -> chat command -> Bubble Tea -> daemon path, not an
// injected TUI model or chat runner. Only the external model service is faked.
func TestDaemonChatPTYAcrossRunnerPlacements(t *testing.T) {
	for _, placement := range []string{"embedded", "standalone"} {
		t.Run(placement, func(t *testing.T) {
			readyFile := os.Getenv("KODELET_BROWSER_READY_FILE")
			timeout := 40 * time.Second
			if readyFile != "" {
				timeout = 5 * time.Minute
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("SHELL", "/bin/sh")
			startup := filepath.Join(root, "runner-startup")
			workspace, clientCWD := filepath.Join(root, "runner-workspace"), filepath.Join(root, "client-workspace")
			for _, cwd := range []string{startup, workspace, clientCWD} {
				require.NoError(t, os.MkdirAll(filepath.Join(cwd, ".kodelet", "extensions"), 0o700))
			}
			for _, cwd := range []string{startup, workspace} {
				git := func(args ...string) {
					t.Helper()
					command := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
					output, err := command.CombinedOutput()
					require.NoError(t, err, "git: %.2000s", output)
				}
				git("init")
				require.NoError(t, os.WriteFile(filepath.Join(cwd, "panel-marker.txt"), []byte("original\n"), 0o600))
				git("add", "panel-marker.txt")
				git("-c", "user.name=PTY test", "-c", "user.email=pty@example.test", "-c", "commit.gpgSign=false", "commit", "-m", "panel baseline")
				require.NoError(t, os.WriteFile(filepath.Join(cwd, "panel-marker.txt"), []byte(filepath.Base(cwd)+"-only-diff\n"), 0o600))
			}
			executable, err := os.Executable()
			require.NoError(t, err)
			script := fmt.Sprintf("#!/bin/sh\nKODELET_TEST_CHAT_EXTENSION=1 exec %q -test.run '^TestDaemonChatExtensionProcess$'\n", executable)
			require.NoError(t, os.WriteFile(filepath.Join(workspace, ".kodelet", "extensions", "kodelet-extension-pty"), []byte(script), 0o700))
			clientMarker := filepath.Join(clientCWD, "client-extension-started")
			poison := fmt.Sprintf("#!/bin/sh\nprintf 'unexpected local execution' > %q\nexit 1\n", clientMarker)
			require.NoError(t, os.WriteFile(filepath.Join(clientCWD, ".kodelet", "extensions", "kodelet-extension-client-only"), []byte(poison), 0o700))
			var providerCalls atomic.Int32
			provider := daemonChatPTYProvider(t, &providerCalls)
			defer provider.Close()
			previous := viper.AllSettings()
			viper.Reset()
			t.Cleanup(func() {
				viper.Reset()
				for key, value := range previous {
					viper.Set(key, value)
				}
			})
			viper.Set("provider", "openai")
			viper.Set("model", "gpt-4o")
			viper.Set("weak_model", "gpt-4o")
			viper.Set("max_tokens", 256)
			viper.Set("openai", map[string]any{"platform": "openai", "base_url": provider.URL, "api_key_env_var": "KODELET_TEST_CHAT_PROVIDER_KEY", "api_mode": "chat_completions"})
			viper.Set("extensions.enabled", true)
			viper.Set("skills.enabled", false)
			viper.Set("allowed_tools", []string{"pty_prompt"})
			t.Setenv("KODELET_TEST_CHAT_PROVIDER_KEY", "daemon-only-pty-key")
			t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-store"))
			require.NoError(t, db.RunMigrations(ctx, migrations.All()))
			settings := map[string]any{"allowed_tools": []string{"pty_prompt"}, "extensions": map[string]any{"enabled": true}, "skills": map[string]any{"enabled": false}}
			settingsData, err := json.Marshal(settings)
			require.NoError(t, err)
			for _, cwd := range []string{startup, workspace} {
				require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), settingsData, 0o600))
			}
			config := &controlplane.ServerConfig{Host: "127.0.0.1", AuthToken: "client-secret", RunnerAuthToken: "runner-secret", CompactRatio: 0.8, DisableControlPlaneWorkspace: true}
			if placement == "embedded" {
				store, err := localstate.NewStore()
				require.NoError(t, err)
				config.EmbeddedRunner = &controlplane.EmbeddedRunnerConfig{Workspace: startup, Settings: settings, Store: store}
			}
			frontend, err := webui.NewHandler()
			require.NoError(t, err)
			daemon, err := controlplane.NewServer(ctx, config, frontend)
			require.NoError(t, err)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			endpoint := "http://" + listener.Addr().String()
			serverCtx, stopServer := context.WithCancel(ctx)
			serverDone := make(chan error, 1)
			go func() { serverDone <- daemon.Serve(serverCtx, listener) }()
			t.Cleanup(func() {
				stopServer()
				select {
				case err := <-serverDone:
					assert.NoError(t, err)
				case <-time.After(10 * time.Second):
					assert.Fail(t, "chat acceptance daemon did not stop")
				}
				assert.NoError(t, daemon.Close())
			})
			// Deliberate independent test environments, not runtime token scrubbing.
			childEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "SHELL=/bin/sh", "KODELET_TEST_CLI_PROCESS=1"}
			runnerPID := os.Getpid()
			if placement == "standalone" {
				runnerCtx, stopRunner := context.WithCancel(ctx)
				process := daemonCLIProcess(runnerCtx, t, startup, append(childEnv, "KODELET_BASE_PATH="+filepath.Join(root, "runner-store")), "runner", "start", "--server="+endpoint, "--auth-token=runner-secret")
				var output bytes.Buffer
				process.Stdout, process.Stderr = &output, &output
				require.NoError(t, process.Start())
				runnerPID = process.Process.Pid
				t.Cleanup(func() {
					stopRunner()
					_ = process.Wait()
					if t.Failed() {
						t.Logf("runner output: %.4000s", output.String())
					}
				})
			}
			var runnerID string
			require.Eventually(t, func() bool {
				runners, _, err := fetchRunners(ctx, endpoint, "client-secret")
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
			}, 10*time.Second, 20*time.Millisecond)
			waitForBrowser := func(conversationID string) {
				// Opt-in manual browser acceptance against exactly this provider and
				// runner fixture. No production flag or unbounded server is added.
				data, err := json.Marshal(map[string]string{"server": endpoint, "cwd": workspace, "runnerId": runnerID, "token": "client-secret", "conversationId": conversationID})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(readyFile, data, 0o600))
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(readyFile + ".done"); err == nil {
						return
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						require.FailNow(t, "browser acceptance did not finish within five minutes")
					}
				}
			}
			if readyFile != "" && os.Getenv("KODELET_BROWSER_REUSE_CONVERSATION") != "1" {
				waitForBrowser("")
				return
			}
			invalidStore := filepath.Join(root, "client-store-is-a-file")
			require.NoError(t, os.WriteFile(invalidStore, []byte("no client conversation database"), 0o600))
			clientEnv := append(childEnv, "KODELET_BASE_PATH="+invalidStore, "TERM=xterm-256color", "COLORTERM=truecolor")
			process := daemonCLIProcess(ctx, t, clientCWD, clientEnv, "chat", "--server="+endpoint, "--auth-token=client-secret", "--runner="+runnerID, "--cwd="+workspace)
			terminal, err := pty.StartWithSize(process, &pty.Winsize{Rows: 40, Cols: 120})
			require.NoError(t, err)
			var screen daemonChatPTYOutput
			readDone, processDone := make(chan struct{}), make(chan error, 1)
			go func() { _, _ = io.Copy(&screen, terminal); close(readDone) }()
			go func() { processDone <- process.Wait() }()
			processExited := false
			t.Cleanup(func() {
				if !processExited {
					_ = process.Process.Kill()
					<-processDone
				}
				_ = terminal.Close()
				<-readDone
				if t.Failed() {
					t.Logf("terminal output: %.6000s", screen.String())
				}
			})
			waitRendered := func(t *testing.T, text string) {
				t.Helper()
				require.Eventually(t, func() bool { return strings.Contains(screen.String(), text) }, 10*time.Second, 10*time.Millisecond, "terminal did not render %q", text)
			}
			write := func(t *testing.T, text string) {
				t.Helper()
				_, err := io.WriteString(terminal, text)
				require.NoError(t, err)
			}
			waitRendered(t, "Ask kodelet")
			write(t, "exercise native PTY prompts\r")
			waitRendered(t, "PTY answer prompt")
			write(t, "terminal-answer\r")
			// The renderer may emit only the changed word in a reused title.
			// This new body line proves the second prompt reached the screen.
			waitRendered(t, "Dismiss this request to finish the PTY gate.")
			write(t, "\x1b")
			waitRendered(t, "pty-answer-and-cancel-complete")
			client, err := chat.NewControlPlaneChatRunner(endpoint, "client-secret", runnerID)
			require.NoError(t, err)
			var conversationID string
			require.Eventually(t, func() bool {
				conversations, err := client.ListConversationsInCWD(ctx, 10, workspace)
				if err != nil || len(conversations) != 1 {
					return false
				}
				conversationID = conversations[0].ID
				record, err := client.LoadConversationRecord(ctx, conversationID)
				return err == nil && strings.Contains(string(record.RawMessages), "pty-answer-and-cancel-complete")
			}, 5*time.Second, 10*time.Millisecond)
			history, err := client.LoadConversation(ctx, conversationID)
			require.NoError(t, err)
			assert.Equal(t, runnerID, history.RunnerID)
			assert.Equal(t, workspace, history.CWD)
			t.Run("conversation-directory-panels", func(t *testing.T) {
				for _, target := range []struct {
					query url.Values
					cwd   string
				}{{url.Values{"runnerId": {runnerID}}, startup}, {url.Values{"conversationId": {conversationID}}, workspace}} {
					request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/git/diff?"+target.query.Encode(), nil)
					require.NoError(t, err)
					request.Header.Set("Authorization", "Bearer client-secret")
					response, err := http.DefaultClient.Do(request)
					require.NoError(t, err)
					defer response.Body.Close()
					require.Equal(t, http.StatusOK, response.StatusCode)
					var diff struct {
						CWD     string `json:"cwd"`
						GitRoot string `json:"git_root"`
						Diff    string `json:"diff"`
						HasDiff bool   `json:"has_diff"`
					}
					require.NoError(t, json.NewDecoder(response.Body).Decode(&diff))
					assert.Equal(t, target.cwd, diff.CWD)
					assert.Equal(t, target.cwd, diff.GitRoot)
					assert.True(t, diff.HasDiff)
					assert.Contains(t, diff.Diff, "+"+filepath.Base(target.cwd)+"-only-diff")
				}
				query := url.Values{"conversationId": {conversationID}, "rows": {"30"}, "cols": {"100"}}
				connection, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(endpoint, "http")+"/api/terminal/ws?"+query.Encode(), http.Header{"Authorization": {"Bearer client-secret"}})
				require.NoError(t, err)
				defer connection.Close()
				require.NoError(t, connection.SetReadDeadline(time.Now().Add(5*time.Second)))
				var ready struct{ Type, CWD string }
				require.NoError(t, connection.ReadJSON(&ready))
				assert.Equal(t, "ready", ready.Type)
				assert.Equal(t, workspace, ready.CWD)
				require.NoError(t, connection.WriteJSON(map[string]any{"type": "resize", "rows": 37, "cols": 111}))
				require.NoError(t, connection.WriteJSON(map[string]any{"type": "input", "data": "printf 'actual-panel-cwd:%s\\n' \"$PWD\"; printf 'actual-panel-parent:%s\\n' \"$PPID\"; cat panel-marker.txt; stty size; exit 7\n"}))
				var output strings.Builder
				for {
					kind, data, err := connection.ReadMessage()
					require.NoError(t, err)
					if kind == websocket.BinaryMessage {
						output.Write(data)
						continue
					}
					var event struct {
						Type string `json:"type"`
						Code int    `json:"code"`
					}
					require.NoError(t, json.Unmarshal(data, &event))
					if event.Type == "exit" {
						assert.Equal(t, 7, event.Code)
						break
					}
				}
				assert.Contains(t, output.String(), "actual-panel-cwd:"+workspace)
				assert.Contains(t, output.String(), "actual-panel-parent:"+strconv.Itoa(runnerPID))
				assert.Contains(t, output.String(), "runner-workspace-only-diff")
				assert.Contains(t, output.String(), "37 111")
				assert.NotContains(t, output.String(), "runner-startup-only-diff")
			})
			t.Run("discovered-native-shortcut", func(t *testing.T) {
				write(t, "?")
				waitRendered(t, "PTY shortcut submission")
				write(t, "\r")   // Close help without an Escape prefix merging with Ctrl-R.
				write(t, "\x12") // Ctrl-R comes from runner discovery, not a TUI test hook.
				waitRendered(t, "pty-shortcut-complete")
				data, err := os.ReadFile(filepath.Join(workspace, "pty-shortcut.json"))
				require.NoError(t, err)
				var invocation struct {
					ParentPID int                             `json:"parentPid"`
					Key       string                          `json:"key"`
					Context   extensions.ExtensionCallContext `json:"context"`
				}
				require.NoError(t, json.Unmarshal(data, &invocation))
				assert.Equal(t, runnerPID, invocation.ParentPID)
				assert.Equal(t, "ctrl+r", invocation.Key)
				assert.Equal(t, workspace, invocation.Context.CWD)
				assert.NotEmpty(t, invocation.Context.ConversationID)
				assert.NotEqual(t, conversationID, invocation.Context.ConversationID, "idle shortcut uses a temporary execution scope")
				require.Eventually(t, func() bool {
					record, err := client.LoadConversationRecord(ctx, conversationID)
					return err == nil && strings.Contains(string(record.RawMessages), "exercise native shortcut") && strings.Contains(string(record.RawMessages), "pty-shortcut-complete")
				}, 5*time.Second, 10*time.Millisecond)
				conversations, err := client.ListConversationsInCWD(ctx, 10, workspace)
				require.NoError(t, err)
				require.Len(t, conversations, 1, "shortcut execution does not create an extra conversation")
				assert.Equal(t, conversationID, conversations[0].ID)
			})
			write(t, "\x03")
			select {
			case err := <-processDone:
				processExited = true
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "public chat did not exit after Ctrl-C")
			}
			t.Run("same-history-cli-and-native-resume", func(t *testing.T) {
				expected, err := client.LoadConversationRecord(ctx, conversationID)
				require.NoError(t, err)
				show := daemonCLIProcess(ctx, t, clientCWD, clientEnv, "conversation", "show", conversationID, "--format=raw", "--server="+endpoint, "--auth-token=client-secret")
				output, err := show.CombinedOutput()
				require.NoError(t, err, "conversation show: %.2000s", output)
				var exported convtypes.ConversationRecord
				require.NoError(t, json.Unmarshal(output, &exported))
				assert.Equal(t, conversationID, exported.ID)
				assert.Equal(t, workspace, exported.CWD)
				assert.Equal(t, expected.Metadata, exported.Metadata)
				assert.Equal(t, expected.Usage, exported.Usage)
				assert.JSONEq(t, string(expected.RawMessages), string(exported.RawMessages))
				// No CWD or runner argument: resume must use the saved affinity,
				// not the client CWD or the embedded runner's startup directory.
				resumed := daemonCLIProcess(ctx, t, clientCWD, clientEnv, "chat", "--resume="+conversationID, "--server="+endpoint, "--auth-token=client-secret")
				terminal, err := pty.StartWithSize(resumed, &pty.Winsize{Rows: 60, Cols: 120})
				require.NoError(t, err)
				var screen daemonChatPTYOutput
				readDone, done := make(chan struct{}), make(chan error, 1)
				go func() { _, _ = io.Copy(&screen, terminal); close(readDone) }()
				go func() { done <- resumed.Wait() }()
				exited := false
				t.Cleanup(func() {
					if !exited {
						_ = resumed.Process.Kill()
						<-done
					}
					_ = terminal.Close()
					<-readDone
					if t.Failed() {
						t.Logf("resumed terminal: %.6000s", screen.String())
					}
				})
				require.Eventually(t, func() bool {
					view := screen.String()
					return strings.Contains(view, "exercise native PTY prompts") && strings.Contains(view, "pty-answer-and-cancel-complete") && strings.Contains(view, "pty-shortcut-complete") && strings.Contains(view, "runner-workspace")
				}, 5*time.Second, 10*time.Millisecond, "resumed native CLI must render the same persisted conversation")
				_, err = io.WriteString(terminal, "\x03")
				require.NoError(t, err)
				select {
				case err := <-done:
					exited = true
					require.NoError(t, err)
				case <-time.After(5 * time.Second):
					require.FailNow(t, "resumed native CLI did not exit")
				}
				after, err := client.LoadConversationRecord(ctx, conversationID)
				require.NoError(t, err)
				assert.Equal(t, expected, after, "show and native resume are read-only until a user submits")
			})
			assert.EqualValues(t, 3, providerCalls.Load(), "only the daemon performs the model/tool round trip and shortcut submission")
			assert.NoFileExists(t, clientMarker, "client-side extensions must not be initialized")
			info, err := os.Stat(invalidStore)
			require.NoError(t, err)
			assert.False(t, info.IsDir(), "chat did not replace the invalid client store")
			data, err := os.ReadFile(filepath.Join(workspace, "pty-extension-evidence.json"))
			require.NoError(t, err)
			var evidence daemonChatPTYEvidence
			require.NoError(t, json.Unmarshal(data, &evidence))
			assert.Equal(t, workspace, evidence.CWD)
			assert.Equal(t, runnerPID, evidence.ParentPID)
			assert.NotEqual(t, process.Process.Pid, evidence.PID)
			assert.Equal(t, extensions.UIInputStatusSubmitted, evidence.Answer.Status)
			assert.Equal(t, "terminal-answer", evidence.Answer.Value)
			assert.Equal(t, extensions.UIInputStatusDismissed, evidence.Cancel.Status)
			initializations, err := os.ReadFile(filepath.Join(workspace, "pty-initializations.jsonl"))
			require.NoError(t, err)
			for _, line := range strings.Split(strings.TrimSpace(string(initializations)), "\n") {
				var initialization daemonChatPTYEvidence
				require.NoError(t, json.Unmarshal([]byte(line), &initialization))
				assert.Equal(t, runnerPID, initialization.ParentPID, "even discovery processes belong to the runner, not the client")
			}
			if readyFile != "" {
				waitForBrowser(conversationID)
			}
		})
	}
}

type daemonChatPTYOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *daemonChatPTYOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = append(o.data, data...)
	if len(o.data) > 256*1024 {
		o.data = o.data[len(o.data)-256*1024:]
	}
	return len(data), nil
}

func (o *daemonChatPTYOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return ansi.Strip(string(o.data))
}

func daemonChatPTYProvider(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer daemon-only-pty-key", r.Header.Get("Authorization"))
		var request struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		assert.True(t, request.Stream)
		finish := "tool_calls"
		delta := map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "pty-call", "type": "function", "function": map[string]any{"name": "pty_prompt", "arguments": "{}"}}}}
		var shortcut bool
		for _, message := range request.Messages {
			if message.Role == "user" {
				shortcut = strings.Contains(string(message.Content), "exercise native shortcut")
			}
			if message.Role == "tool" {
				assert.Contains(t, string(message.Content), "terminal-answer")
				assert.Contains(t, string(message.Content), "dismissed")
				finish = "stop"
				delta = map[string]any{"role": "assistant", "content": "pty-answer-and-cancel-complete"}
			}
		}
		if shortcut {
			finish = "stop"
			delta = map[string]any{"role": "assistant", "content": "pty-shortcut-complete"}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{"id": "pty-completion", "object": "chat.completion.chunk", "model": "gpt-4o", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
}

type daemonChatPTYEvidence struct {
	PID       int                        `json:"pid"`
	ParentPID int                        `json:"parentPid"`
	CWD       string                     `json:"cwd"`
	Answer    extensions.UIInputResponse `json:"answer"`
	Cancel    extensions.UIInputResponse `json:"cancel"`
}

func TestDaemonChatExtensionProcess(_ *testing.T) {
	if os.Getenv("KODELET_TEST_CHAT_EXTENSION") != "1" {
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
		var value map[string]json.RawMessage
		err := json.Unmarshal(data, &value)
		return value, err
	}
	write := func(value any) {
		data, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
	}
	var cwd string
	next := 1000
	for {
		message, err := read()
		if err != nil {
			os.Exit(0)
		}
		if len(message["id"]) == 0 {
			continue
		}
		var method string
		_ = json.Unmarshal(message["method"], &method)
		var result any = map[string]any{}
		switch method {
		case "extension.initialize":
			var params struct {
				Extension struct{ CWD string } `json:"extension"`
			}
			_ = json.Unmarshal(message["params"], &params)
			cwd = params.Extension.CWD
			file, err := os.OpenFile(filepath.Join(cwd, "pty-initializations.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err == nil {
				_ = json.NewEncoder(file).Encode(daemonChatPTYEvidence{PID: os.Getpid(), ParentPID: os.Getppid(), CWD: cwd})
				_ = file.Close()
			}
			result = extensions.InitializeResult{
				Name: "pty", Version: "1",
				Tools:     []extensions.ToolRegistration{{Name: "pty_prompt", Description: "Exercise native terminal prompts", InputSchema: map[string]any{"type": "object"}}},
				Shortcuts: []extensions.ShortcutRegistration{{Key: "ctrl+r", Description: "PTY shortcut submission"}},
			}
			if os.Getenv("KODELET_TEST_INSPECTION_RECIPES") == "1" {
				initialized := result.(extensions.InitializeResult)
				initialized.Commands = []extensions.CommandRegistration{{Name: "dynamic-review", Description: "Dynamic runner recipe", Kind: "recipe", InputSchema: map[string]any{"type": "object"}}, {Name: "not-a-recipe", Description: "Regular command", Kind: "command", InputSchema: map[string]any{"type": "object"}}}
				result = initialized
			}
		case "extension.shortcut.execute":
			var params struct {
				Key     string                          `json:"key"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			_ = json.Unmarshal(message["params"], &params)
			data, _ := json.Marshal(map[string]any{"parentPid": os.Getppid(), "key": params.Key, "context": params.Context})
			_ = os.WriteFile(filepath.Join(params.Context.CWD, "pty-shortcut.json"), data, 0o600)
			result = &extensions.ShortcutResult{Action: extensions.ShortcutActionSubmit, Message: "exercise native shortcut"}
		case "extension.tool.execute":
			input := func(title, body string) (extensions.UIInputResponse, error) {
				next++
				write(map[string]any{"jsonrpc": "2.0", "id": next, "parentId": message["id"], "method": "kodelet.ui.input", "params": extensions.UIInputRequest{Title: title, Message: body}})
				for {
					response, err := read()
					if err != nil {
						return extensions.UIInputResponse{}, err
					}
					if string(response["id"]) != strconv.Itoa(next) {
						continue // Capability refresh is an independent notification.
					}
					if raw := response["error"]; len(raw) > 0 && string(raw) != "null" {
						return extensions.UIInputResponse{}, errors.Errorf("host UI: %s", raw)
					}
					var value extensions.UIInputResponse
					err = json.Unmarshal(response["result"], &value)
					return value, err
				}
			}
			evidence := daemonChatPTYEvidence{PID: os.Getpid(), ParentPID: os.Getppid(), CWD: cwd}
			evidence.Answer, err = input("PTY answer prompt", "")
			if err == nil {
				evidence.Cancel, err = input("PTY cancel prompt", "Dismiss this request to finish the PTY gate.")
			}
			if err != nil {
				result = extensions.ToolExecutionResult{Error: err.Error()}
			} else {
				data, _ := json.Marshal(evidence)
				_ = os.WriteFile(filepath.Join(cwd, "pty-extension-evidence.json"), data, 0o600)
				result = extensions.ToolExecutionResult{Content: string(data)}
			}
		}
		write(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": result})
	}
}
