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
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
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
// Each journey owns its state and processes so failures do not gate other journeys.
func TestDaemonChatPTYAcrossRunnerPlacements(t *testing.T) {
	for _, placement := range []string{"embedded", "standalone"} {
		t.Run(placement, func(t *testing.T) {
			t.Run("pty-prompts", func(t *testing.T) {
				f := newDaemonChatPTYFixture(t, placement)
				readyFile := os.Getenv("KODELET_BROWSER_READY_FILE")
				if readyFile != "" && os.Getenv("KODELET_BROWSER_REUSE_CONVERSATION") != "1" {
					f.waitForBrowser(t, readyFile, "")
					return
				}
				terminal := f.startChat(t, "--runner="+f.runnerID, "--cwd="+f.workspace)
				terminal.waitRendered(t, "extension · ")
				terminal.write(t, "exercise native PTY prompts\r")
				terminal.waitRendered(t, "PTY answer prompt")
				terminal.write(t, "terminal-answer\r")
				// A distinct body line survives incremental title repainting.
				terminal.waitRendered(t, "Dismiss this request to finish the PTY gate.")
				terminal.write(t, "\x1b")
				terminal.waitRendered(t, "pty-answer-and-cancel-complete")
				conversationID := f.conversation(t, "pty-answer-and-cancel-complete")
				terminal.exit(t)

				assert.EqualValues(t, 2, f.providerCalls.Load(), "only the daemon performs the model/tool round trip")
				data, err := os.ReadFile(filepath.Join(f.workspace, "pty-extension-evidence.json"))
				require.NoError(t, err)
				var evidence daemonChatPTYEvidence
				require.NoError(t, json.Unmarshal(data, &evidence))
				assert.Equal(t, f.workspace, evidence.CWD)
				assert.Equal(t, f.runnerPID, evidence.ParentPID)
				assert.NotEqual(t, terminal.process.Process.Pid, evidence.PID)
				assert.Equal(t, extensions.UIInputStatusSubmitted, evidence.Answer.Status)
				assert.Equal(t, "terminal-answer", evidence.Answer.Value)
				assert.Equal(t, extensions.UIInputStatusDismissed, evidence.Cancel.Status)
				initializations, err := os.ReadFile(filepath.Join(f.workspace, "pty-initializations.jsonl"))
				require.NoError(t, err)
				for _, line := range strings.Split(strings.TrimSpace(string(initializations)), "\n") {
					var initialization daemonChatPTYEvidence
					require.NoError(t, json.Unmarshal([]byte(line), &initialization))
					assert.Equal(t, f.runnerPID, initialization.ParentPID, "even discovery belongs to the runner")
				}
				if readyFile != "" {
					f.waitForBrowser(t, readyFile, conversationID)
				}
			})
			if os.Getenv("KODELET_BROWSER_READY_FILE") != "" {
				return // Manual browser mode uses only the prompt fixture.
			}

			t.Run("conversation-directory-panels", func(t *testing.T) {
				f := newDaemonChatPTYFixture(t, placement)
				for _, cwd := range []string{f.startup, f.workspace} {
					initDaemonChatPTYRepository(f.ctx, t, cwd)
					require.NoError(t, os.WriteFile(
						filepath.Join(cwd, "panel-marker.txt"), []byte(filepath.Base(cwd)+"-only-diff\n"), 0o600,
					))
				}
				conversationID := f.seedConversation(t)
				for _, target := range []struct {
					query url.Values
					cwd   string
				}{
					{url.Values{"runnerId": {f.runnerID}}, f.startup},
					{url.Values{"conversationId": {conversationID}}, f.workspace},
				} {
					request, err := http.NewRequestWithContext(
						f.ctx, http.MethodGet, f.endpoint+"/api/git/diff?"+target.query.Encode(), nil,
					)
					require.NoError(t, err)
					request.Header.Set("Authorization", "Bearer client-secret")
					response, err := http.DefaultClient.Do(request)
					require.NoError(t, err)
					t.Cleanup(func() { _ = response.Body.Close() })
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
				connection, _, err := websocket.DefaultDialer.DialContext(
					f.ctx, "ws"+strings.TrimPrefix(f.endpoint, "http")+"/api/terminal/ws?"+query.Encode(),
					http.Header{"Authorization": {"Bearer client-secret"}},
				)
				require.NoError(t, err)
				t.Cleanup(func() { _ = connection.Close() })
				require.NoError(t, connection.SetReadDeadline(time.Now().Add(5*time.Second)))
				var ready struct{ Type, CWD string }
				require.NoError(t, connection.ReadJSON(&ready))
				assert.Equal(t, "ready", ready.Type)
				assert.Equal(t, f.workspace, ready.CWD)
				require.NoError(t, connection.WriteJSON(map[string]any{"type": "resize", "rows": 37, "cols": 111}))
				const command = `printf 'actual-panel-cwd:%s\n' "$PWD"
printf 'actual-panel-parent:%s\n' "$PPID"
cat panel-marker.txt
stty size
exit 7
`
				require.NoError(t, connection.WriteJSON(map[string]any{"type": "input", "data": command}))
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
				physicalWorkspace, err := filepath.EvalSymlinks(f.workspace)
				require.NoError(t, err)
				assert.Contains(t, output.String(), "actual-panel-cwd:"+physicalWorkspace)
				assert.Contains(t, output.String(), "actual-panel-parent:"+strconv.Itoa(f.runnerPID))
				assert.Contains(t, output.String(), "runner-workspace-only-diff")
				assert.Contains(t, output.String(), "37 111")
				assert.NotContains(t, output.String(), "runner-startup-only-diff")
			})

			t.Run("discovered-native-shortcut", func(t *testing.T) {
				f := newDaemonChatPTYFixture(t, placement)
				terminal := f.startChat(t, "--runner="+f.runnerID, "--cwd="+f.workspace)
				terminal.waitRendered(t, "extension · ")
				terminal.write(t, "exercise native chat history\r")
				terminal.waitRendered(t, "pty-history-answer")
				conversationID := f.conversation(t, "pty-history-answer")
				terminal.write(t, "?")
				terminal.waitRendered(t, "PTY shortcut submission")
				terminal.write(t, "\r")   // Avoid merging an Escape prefix with Ctrl-R.
				terminal.write(t, "\x12") // Discovered binding, not a TUI test hook.
				terminal.waitRendered(t, "pty-shortcut-complete")
				data, err := os.ReadFile(filepath.Join(f.workspace, "pty-shortcut.json"))
				require.NoError(t, err)
				var invocation struct {
					ParentPID int                             `json:"parentPid"`
					Key       string                          `json:"key"`
					Context   extensions.ExtensionCallContext `json:"context"`
				}
				require.NoError(t, json.Unmarshal(data, &invocation))
				assert.Equal(t, f.runnerPID, invocation.ParentPID)
				assert.Equal(t, "ctrl+r", invocation.Key)
				assert.Equal(t, f.workspace, invocation.Context.CWD)
				assert.NotEmpty(t, invocation.Context.ConversationID)
				assert.NotEqual(t, conversationID, invocation.Context.ConversationID, "idle shortcut uses a temporary execution scope")
				assert.Equal(t, conversationID, f.conversation(t, "pty-shortcut-complete"), "shortcut uses the existing conversation")
				record, err := f.client.LoadConversationRecord(f.ctx, conversationID)
				require.NoError(t, err)
				assert.Contains(t, string(record.RawMessages), "exercise native shortcut")
				terminal.exit(t)
				assert.EqualValues(t, 2, f.providerCalls.Load())
			})

			t.Run("builtin-history-recall-across-processes", func(t *testing.T) {
				f := newDaemonChatPTYFixture(t, placement)
				for _, cwd := range []string{f.startup, f.workspace} {
					initDaemonChatPTYRepository(f.ctx, t, cwd)
				}
				// Seed legacy JSONL history before either chat CLI starts.
				historyStore := messagehistory.NewStoreWithBasePath(f.historyBasePath)
				workspaceScope, err := messagehistory.ResolveScopeCWD(f.workspace)
				require.NoError(t, err)
				startupScope, err := messagehistory.ResolveScopeCWD(f.startup)
				require.NoError(t, err)
				const legacyRawMessage = "/goal legacy-raw-history ship the old task"
				const otherProjectMessage = "isolated-project history must stay in the other repository"
				require.NoError(t, historyStore.Append(f.ctx, messagehistory.Entry{
					ScopeCWD: workspaceScope, Source: "tui", Text: legacyRawMessage,
				}))
				require.NoError(t, historyStore.Append(f.ctx, messagehistory.Entry{
					ScopeCWD: startupScope, Source: "tui", Text: otherProjectMessage,
				}))
				first := f.startChat(t, "--runner="+f.runnerID, "--cwd="+f.workspace)
				first.waitRendered(t, "extension · ")
				first.write(t, "exercise native chat history\r")
				first.waitRendered(t, "pty-history-answer")
				conversationID := f.conversation(t, "pty-history-answer")
				first.exit(t)

				entries, err := historyStore.List(f.ctx, workspaceScope, messagehistory.MaxEntriesPerScope)
				require.NoError(t, err)
				var messages []string
				for _, entry := range entries {
					messages = append(messages, entry.Text)
				}
				require.Contains(t, messages, legacyRawMessage, "existing raw composer history must remain readable")
				require.Contains(t, messages, "exercise native chat history", "the first CLI must persist new submissions on its runner")
				assert.NotContains(t, messages, otherProjectMessage)

				// A fresh conversation in a subdirectory must recall the same Git
				// project's messages, not load a saved conversation transcript.
				subdirectory := filepath.Join(f.workspace, "history-subdirectory")
				require.NoError(t, os.MkdirAll(subdirectory, 0o700))
				callsBefore := f.providerCalls.Load()
				fresh := f.startChat(t, "--runner="+f.runnerID, "--cwd="+subdirectory, "--no-extensions")
				fresh.waitRendered(t, "0 extensions · ")
				assert.NotContains(t, fresh.screen.String(), "pty-history-answer", "fresh chat must not resume the old transcript")
				assert.NotContains(t, fresh.screen.String(), "exercise native chat history")
				// --no-extensions prevents the fixture's Ctrl-R shortcut from
				// overriding the built-in search. Never submit a recalled message.
				// Paste each query atomically: per-key matching can select a shared
				// prefix first, leaving only a suffix in Bubble Tea's delta output.
				// ansi.Strip removes escapes but does not reconstruct the screen.
				fresh.write(t, "\x12\x1b[200~legacy-raw-history\x1b[201~")
				fresh.waitRendered(t, "reverse-i-search:")
				fresh.waitRendered(t, legacyRawMessage)
				fresh.write(t, "\x15\x1b[200~native chat history\x1b[201~")
				fresh.waitRendered(t, "exercise native chat history")
				fresh.write(t, "\x15\x1b[200~isolated-project\x1b[201~")
				fresh.waitRendered(t, "isolated-project  no matches")
				assert.NotContains(t, fresh.screen.String(), otherProjectMessage)
				fresh.exit(t)
				assert.Equal(t, callsBefore, f.providerCalls.Load(), "composer history search must not call a model")
				conversations, err := f.client.ListConversations(f.ctx, 10)
				require.NoError(t, err)
				require.Len(t, conversations, 1, "recalling history must not create a conversation")
				assert.Equal(t, conversationID, conversations[0].ID)
			})

			t.Run("same-history-cli-and-native-resume", func(t *testing.T) {
				f := newDaemonChatPTYFixture(t, placement)
				first := f.startChat(t, "--runner="+f.runnerID, "--cwd="+f.workspace)
				first.waitRendered(t, "extension · ")
				first.write(t, "exercise native PTY prompts\r")
				first.waitRendered(t, "PTY answer prompt")
				first.write(t, "terminal-answer\r")
				first.waitRendered(t, "Dismiss this request to finish the PTY gate.")
				first.write(t, "\x1b")
				first.waitRendered(t, "pty-answer-and-cancel-complete")
				conversationID := f.conversation(t, "pty-answer-and-cancel-complete")
				// A later turn must not hide the earlier tool exchange on resume.
				first.write(t, "exercise native chat history\r")
				first.waitRendered(t, "pty-history-answer")
				assert.Equal(t, conversationID, f.conversation(t, "pty-history-answer"))
				first.exit(t)
				assert.EqualValues(t, 3, f.providerCalls.Load(), "tool exchange plus a second plain turn")
				expected, err := f.client.LoadConversationRecord(f.ctx, conversationID)
				require.NoError(t, err)
				assert.Contains(t, string(expected.RawMessages), "pty_prompt")
				assert.Contains(t, string(expected.RawMessages), "terminal-answer")
				show := f.command(t, "conversation", "show", conversationID, "--format=raw")
				output, err := show.CombinedOutput()
				require.NoError(t, err, "conversation show: %.2000s", output)
				var exported convtypes.ConversationRecord
				require.NoError(t, json.Unmarshal(output, &exported))
				assert.Equal(t, conversationID, exported.ID)
				assert.Equal(t, f.workspace, exported.CWD)
				assert.Equal(t, expected.Metadata, exported.Metadata)
				assert.Equal(t, expected.Usage, exported.Usage)
				assert.JSONEq(t, string(expected.RawMessages), string(exported.RawMessages))
				// No CWD or runner argument: resume must use the saved affinity,
				// not the client CWD or the embedded runner's startup directory.
				resumed := f.startChat(t, "--resume="+conversationID)
				resumed.waitRendered(t, "exercise native PTY prompts")
				resumed.waitRendered(t, "pty-answer-and-cancel-complete")
				resumed.waitRendered(t, "exercise native chat history")
				resumed.waitRendered(t, "pty-history-answer")
				resumed.waitRendered(t, "runner-workspace")
				resumed.exit(t)
				after, err := f.client.LoadConversationRecord(f.ctx, conversationID)
				require.NoError(t, err)
				assert.Equal(t, expected, after, "show and native resume are read-only until a user submits")
				assert.EqualValues(t, 3, f.providerCalls.Load(), "show and resume must not call a model")
			})
		})
	}
}

type daemonChatPTYFixture struct {
	ctx             context.Context
	startup         string
	workspace       string
	clientCWD       string
	clientEnv       []string
	endpoint        string
	runnerID        string
	runnerPID       int
	historyBasePath string
	providerCalls   atomic.Int32
	client          *chat.Client
}

func newDaemonChatPTYFixture(t *testing.T, placement string) *daemonChatPTYFixture {
	t.Helper()
	timeout := 40 * time.Second
	if os.Getenv("KODELET_BROWSER_READY_FILE") != "" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("SHELL", "/bin/sh")
	f := &daemonChatPTYFixture{
		ctx:             ctx,
		startup:         filepath.Join(root, "runner-startup"),
		workspace:       filepath.Join(root, "runner-workspace"),
		clientCWD:       filepath.Join(root, "client-workspace"),
		runnerPID:       os.Getpid(),
		historyBasePath: filepath.Join(root, "daemon-store"),
	}
	for _, cwd := range []string{f.startup, f.workspace, f.clientCWD} {
		require.NoError(t, os.MkdirAll(filepath.Join(cwd, ".kodelet", "extensions"), 0o700))
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf(`#!/bin/sh
KODELET_TEST_CHAT_EXTENSION=1 exec %q -test.run '^TestDaemonChatExtensionProcess$'
`, executable)
	require.NoError(t, os.WriteFile(
		filepath.Join(f.workspace, ".kodelet", "extensions", "kodelet-extension-pty"), []byte(script), 0o700,
	))
	clientMarker := filepath.Join(f.clientCWD, "client-extension-started")
	poison := fmt.Sprintf(`#!/bin/sh
printf 'unexpected local execution' > %q
exit 1
`, clientMarker)
	require.NoError(t, os.WriteFile(
		filepath.Join(f.clientCWD, ".kodelet", "extensions", "kodelet-extension-client-only"), []byte(poison), 0o700,
	))
	provider := daemonChatPTYProvider(t, &f.providerCalls)
	t.Cleanup(provider.Close)
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range previous {
			viper.Set(key, value)
		}
	})
	viper.Set("profile", "work")
	viper.Set("profiles", map[string]any{
		"work": daemonTestModelProfile(provider.URL, "KODELET_TEST_CHAT_PROVIDER_KEY"),
	})
	viper.Set("extensions.enabled", true)
	viper.Set("skills.enabled", false)
	viper.Set("allowed_tools", []string{"pty_prompt", "pty_background"})
	t.Setenv("KODELET_TEST_CHAT_PROVIDER_KEY", "daemon-only-pty-key")
	t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-store"))
	require.NoError(t, db.RunMigrations(ctx, migrations.All()))
	settings := map[string]any{
		"allowed_tools": []string{"pty_prompt", "pty_background"},
		"extensions":    map[string]any{"enabled": true},
		"skills":        map[string]any{"enabled": false},
	}
	settingsData, err := json.Marshal(settings)
	require.NoError(t, err)
	for _, cwd := range []string{f.startup, f.workspace} {
		require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), settingsData, 0o600))
	}
	config := &controlplane.ServerConfig{
		Host:            "127.0.0.1",
		AuthToken:       "client-secret",
		RunnerAuthToken: "runner-secret",
		CompactRatio:    0.8,
	}
	if placement == "embedded" {
		store, err := localstate.NewStore()
		require.NoError(t, err)
		config.EmbeddedRunner = &controlplane.EmbeddedRunnerConfig{
			Workspace: f.startup,
			Settings:  settings,
			Store:     store,
		}
	}
	frontend, err := webui.NewHandler()
	require.NoError(t, err)
	daemon, err := controlplane.NewServer(ctx, config, frontend)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f.endpoint = "http://" + listener.Addr().String()
	serverCtx, stopServer := context.WithCancel(ctx)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- daemon.Serve(serverCtx, listener)
	}()
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
	childEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + root,
		"SHELL=/bin/sh",
		"KODELET_TEST_CLI_PROCESS=1",
	}
	if placement == "standalone" {
		f.historyBasePath = filepath.Join(root, "runner-store")
		runnerCtx, stopRunner := context.WithCancel(ctx)
		process := daemonCLIProcess(
			runnerCtx, t, f.startup, append(childEnv, "KODELET_BASE_PATH="+f.historyBasePath),
			"runner", "start", "--server="+f.endpoint, "--auth-token=runner-secret",
		)
		var output bytes.Buffer
		process.Stdout, process.Stderr = &output, &output
		require.NoError(t, process.Start())
		f.runnerPID = process.Process.Pid
		t.Cleanup(func() {
			stopRunner()
			_ = process.Wait()
			if t.Failed() {
				t.Logf("runner output: %.4000s", output.String())
			}
		})
	}
	require.Eventually(t, func() bool {
		runners, _, err := fetchRunners(ctx, f.endpoint, "client-secret")
		if err != nil {
			return false
		}
		for _, runner := range runners {
			if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle {
				f.runnerID = runner.ID
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	f.client, err = chat.NewClient(f.endpoint, "client-secret", f.runnerID)
	require.NoError(t, err)
	invalidStore := filepath.Join(root, "client-store-is-a-file")
	require.NoError(t, os.WriteFile(invalidStore, []byte("no client conversation database"), 0o600))
	f.clientEnv = append(childEnv, "KODELET_BASE_PATH="+invalidStore, "TERM=xterm-256color", "COLORTERM=truecolor")
	t.Cleanup(func() {
		assert.NoFileExists(t, clientMarker, "client-side extensions must not be initialized")
		info, err := os.Stat(invalidStore)
		if assert.NoError(t, err) {
			assert.False(t, info.IsDir(), "chat did not replace the invalid client store")
		}
	})
	return f
}

func (f *daemonChatPTYFixture) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	args = append(args, "--server="+f.endpoint, "--auth-token=client-secret")
	return daemonCLIProcess(f.ctx, t, f.clientCWD, f.clientEnv, args...)
}

func (f *daemonChatPTYFixture) seedConversation(t *testing.T) string {
	t.Helper()
	// Panels and resume need persisted affinity, not an interactive prompt journey.
	process := f.command(t, "run", "--runner="+f.runnerID, "--cwd="+f.workspace,
		"--result-only", "exercise native chat history")
	output, err := process.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "pty-history-answer\n", string(output))
	return f.conversation(t, "pty-history-answer")
}

func (f *daemonChatPTYFixture) conversation(t *testing.T, text string) string {
	t.Helper()
	var conversationID string
	require.Eventually(t, func() bool {
		conversations, err := f.client.ListConversationsInCWD(f.ctx, 10, f.workspace)
		if err != nil || len(conversations) != 1 || conversations[0].IsRunning {
			return false
		}
		conversationID = conversations[0].ID
		record, err := f.client.LoadConversationRecord(f.ctx, conversationID)
		return err == nil && strings.Contains(string(record.RawMessages), text)
	}, 5*time.Second, 10*time.Millisecond, "expected one completed conversation containing %q", text)
	history, err := f.client.LoadConversation(f.ctx, conversationID)
	require.NoError(t, err)
	assert.Equal(t, f.runnerID, history.RunnerID)
	assert.Equal(t, f.workspace, history.CWD)
	return conversationID
}

func (f *daemonChatPTYFixture) waitForBrowser(t *testing.T, readyFile, conversationID string) {
	t.Helper()
	// Opt-in manual acceptance; ordinary tests never require a browser.
	data, err := json.Marshal(map[string]string{
		"server":         f.endpoint,
		"cwd":            f.workspace,
		"runnerId":       f.runnerID,
		"token":          "client-secret",
		"conversationId": conversationID,
	})
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
		case <-f.ctx.Done():
			require.FailNow(t, "browser acceptance did not finish within five minutes")
		}
	}
}

func initDaemonChatPTYRepository(ctx context.Context, t *testing.T, cwd string) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "git: %.2000s", output)
	}
	git("init")
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "panel-marker.txt"), []byte("original\n"), 0o600))
	git("add", "panel-marker.txt")
	git("-c", "user.name=PTY test", "-c", "user.email=pty@example.test",
		"-c", "commit.gpgSign=false", "commit", "-m", "panel baseline")
}

type daemonChatPTYSession struct {
	process  *exec.Cmd
	terminal *os.File
	screen   daemonChatPTYOutput
	done     chan error
	exited   bool
}

func (f *daemonChatPTYFixture) startChat(t *testing.T, args ...string) *daemonChatPTYSession {
	t.Helper()
	s := &daemonChatPTYSession{
		process: f.command(t, append([]string{"chat"}, args...)...),
		done:    make(chan error, 1),
	}
	var err error
	s.terminal, err = pty.StartWithSize(s.process, &pty.Winsize{Rows: 40, Cols: 120})
	require.NoError(t, err)
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&s.screen, s.terminal)
		close(readDone)
	}()
	go func() {
		s.done <- s.process.Wait()
	}()
	t.Cleanup(func() {
		if !s.exited {
			_ = s.process.Process.Kill()
			<-s.done
		}
		_ = s.terminal.Close()
		<-readDone
		if t.Failed() {
			t.Logf("terminal output: %.6000s", s.screen.String())
		}
	})
	return s
}

func (s *daemonChatPTYSession) waitRendered(t *testing.T, text string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return strings.Contains(s.screen.String(), text)
	}, 10*time.Second, 10*time.Millisecond, "terminal did not render %q", text)
}

func (s *daemonChatPTYSession) write(t *testing.T, text string) {
	t.Helper()
	_, err := io.WriteString(s.terminal, text)
	require.NoError(t, err)
}

func (s *daemonChatPTYSession) exit(t *testing.T) {
	t.Helper()
	s.write(t, "\x03")
	select {
	case err := <-s.done:
		s.exited = true
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "chat did not exit after Ctrl-C")
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
		delta := map[string]any{
			"role": "assistant",
			"tool_calls": []any{map[string]any{
				"index":    0,
				"id":       "pty-call",
				"type":     "function",
				"function": map[string]any{"name": "pty_prompt", "arguments": "{}"},
			}},
		}
		var shortcut, history bool
		for _, message := range request.Messages {
			if message.Role == "user" {
				shortcut = strings.Contains(string(message.Content), "exercise native shortcut")
				history = strings.Contains(string(message.Content), "exercise native chat history")
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
		} else if history {
			// Non-interactive seed for journeys that do not exercise extension prompts.
			finish = "stop"
			delta = map[string]any{"role": "assistant", "content": "pty-history-answer"}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{
			"id":      "pty-completion",
			"object":  "chat.completion.chunk",
			"model":   "gpt-4o",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, `data: %s

data: [DONE]

`, encoded)
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
				Extension    struct{ CWD string } `json:"extension"`
				Capabilities struct {
					Runtime extensions.RuntimeCapabilities `json:"runtime"`
				} `json:"capabilities"`
			}
			_ = json.Unmarshal(message["params"], &params)
			cwd = params.Extension.CWD
			file, err := os.OpenFile(
				filepath.Join(cwd, "pty-initializations.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600,
			)
			if err == nil {
				_ = json.NewEncoder(file).Encode(daemonChatPTYEvidence{
					PID:       os.Getpid(),
					ParentPID: os.Getppid(),
					CWD:       cwd,
				})
				_ = file.Close()
			}
			initialized := extensions.InitializeResult{
				Name:    "pty",
				Version: "1",
				Tools: []extensions.ToolRegistration{{
					Name:        "pty_prompt",
					Description: "Exercise native terminal prompts",
					InputSchema: map[string]any{"type": "object"},
				}},
				Shortcuts: []extensions.ShortcutRegistration{{Key: "ctrl+r", Description: "PTY shortcut submission"}},
			}
			// Like subagent extensions, advertise background tools only during execution.
			if params.Capabilities.Runtime.BackgroundTasks {
				initialized.Tools = append(initialized.Tools, extensions.ToolRegistration{
					Name:        "pty_background",
					Description: "Requires background execution",
					InputSchema: map[string]any{"type": "object"},
				})
			}
			if os.Getenv("KODELET_TEST_INSPECTION_RECIPES") == "1" {
				initialized.Commands = []extensions.CommandRegistration{
					{
						Name:        "dynamic-review",
						Description: "Dynamic runner recipe",
						Kind:        "recipe",
						InputSchema: map[string]any{"type": "object"},
					},
					{
						Name:        "not-a-recipe",
						Description: "Regular command",
						Kind:        "command",
						InputSchema: map[string]any{"type": "object"},
					},
				}
			}
			result = initialized
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
				write(map[string]any{
					"jsonrpc":  "2.0",
					"id":       next,
					"parentId": message["id"],
					"method":   "kodelet.ui.input",
					"params":   extensions.UIInputRequest{Title: title, Message: body},
				})
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
