package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func remoteRunCommandForTest() *cobra.Command {
	cmd := &cobra.Command{Use: "run"}
	addRemoteRunFlags(cmd)
	for _, flag := range []string{"resume", "cwd", "profile", "model", "provider", "weak-model", "reasoning-effort", "recipe", "sysprompt", "account", "anthropic-api-access", "allowed-domains-file", "tool-mode"} {
		cmd.Flags().String(flag, "", "")
	}
	for _, flag := range []string{"follow", "no-save", "no-tools", "no-extensions", "no-skills", "use-weak-model", "enable-fs-search-tools", "result-only", "enable-openai-search"} {
		cmd.Flags().Bool(flag, false, "")
	}
	for _, flag := range []string{"max-turns", "max-tokens", "weak-model-max-tokens", "thinking-budget-tokens"} {
		cmd.Flags().Int(flag, 0, "")
	}
	for _, flag := range []string{"allowed-tools", "allowed-commands", "image", "fragment-dirs", "context-patterns"} {
		cmd.Flags().StringSlice(flag, nil, "")
	}
	cmd.Flags().StringToString("arg", nil, "")
	cmd.Flags().StringToString("sysprompt-arg", nil, "")
	cmd.Flags().Float64("compact-ratio", 0.8, "")
	return cmd
}

func TestRemoteRunOptionsPreserveExplicitRestrictions(t *testing.T) {
	cmd := remoteRunCommandForTest()
	options, err := remoteRunExecutionOptions(cmd)
	require.NoError(t, err)
	assert.Equal(t, &llmtypes.ExecutionOptions{}, options)
	require.NoError(t, cmd.ParseFlags([]string{"--no-tools=false", "--allowed-tools=", "--max-turns=0", "--model=test-model"}))
	options, err = remoteRunExecutionOptions(cmd)
	require.NoError(t, err)
	require.NotNil(t, options.NoTools)
	assert.False(t, *options.NoTools)
	require.NotNil(t, options.AllowedTools)
	assert.Empty(t, *options.AllowedTools)
	require.NotNil(t, options.MaxTurns)
	assert.Zero(t, *options.MaxTurns)
	assert.Equal(t, "test-model", *options.Model)
	encoded, err := json.Marshal(options)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"allowedTools":[]`)
	var decoded llmtypes.ExecutionOptions
	require.NoError(t, json.Unmarshal(encoded, &decoded))
}

func TestDefaultRunnerCWDRequiresMatchingInstallation(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	store, err := localstate.NewStore()
	require.NoError(t, err)
	identity, err := store.LoadOrCreateHostIdentity()
	require.NoError(t, err)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, test := range []struct {
		name, host, selector, requestedCWD, conversation, wantCWD, wantRunner string
	}{
		{name: "same installation", host: identity.InstanceID, wantCWD: cwd, wantRunner: "default"},
		{name: "forwarded loopback", host: "different-host", wantRunner: "default"},
		{name: "older daemon", wantRunner: "default"},
		{name: "explicit directory", host: identity.InstanceID, requestedCWD: "/runner/explicit", wantCWD: "/runner/explicit", wantRunner: "default"},
		{name: "explicit runner", host: identity.InstanceID, selector: "chosen", wantRunner: "chosen"},
		{name: "resume stored affinity", host: identity.InstanceID, conversation: "existing", wantRunner: "stored"},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/chat/settings":
					require.NoError(t, json.NewEncoder(w).Encode(chat.ControlPlaneChatSettings{DefaultRunnerID: "default", DefaultRunnerHostID: test.host, DefaultRunnerReady: true}))
				case "/api/runners":
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"runners": []runnerregistry.Runner{{ID: "chosen", Connected: true, Status: runnerregistry.RunnerStatusIdle, Workspace: protocol.Workspace{Path: "/runner/startup"}}}}))
				case "/api/conversations/existing":
					_, _ = io.WriteString(w, `{"id":"existing","runnerId":"stored","cwd":"/stored/workspace"}`)
				case "/api/chat":
					var request chat.ChatRequest
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					assert.Equal(t, test.wantRunner, request.RunnerID)
					assert.Equal(t, test.wantCWD, request.CWD)
					require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "done", ConversationID: request.ConversationID}))
				default:
					assert.Fail(t, "unexpected request", "%s", r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer daemon.Close()
			cmd := remoteRunCommandForTest()
			require.NoError(t, cmd.Flags().Set("runner", test.selector))
			request := chat.ChatRequest{ConversationID: test.conversation, CWD: test.requestedCWD}
			runner, err := prepareOneShotRunner(t.Context(), cmd, daemon.URL, "", &request)
			require.NoError(t, err)
			assert.Equal(t, test.wantCWD, request.CWD)
			request.Message = "targeting assertion"
			_, err = runner.Run(t.Context(), request, &remoteRunSink{output: io.Discard, diagnostics: io.Discard})
			require.NoError(t, err)
			if test.conversation == "" && test.selector == "" {
				client, err := chat.NewControlPlaneChatRunner(daemon.URL, "", "")
				require.NoError(t, err)
				runner := &configuredChatRunner{ControlPlaneChatRunner: client}
				target, err := runner.discoveryTarget(t.Context(), chat.WorkspaceTarget{CWD: test.requestedCWD})
				require.NoError(t, err)
				assert.Equal(t, test.wantCWD, target.CWD)
			}
		})
	}
}

func TestDaemonDefaultCLIAndRemovedFlagsWithoutLocalState(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/chat/settings":
			_, _ = io.WriteString(w, `{"defaultRunnerId":"default","defaultRunnerReady":true}`)
		case "/api/chat":
			var request chat.ChatRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Equal(t, "default", request.RunnerID)
			assert.Empty(t, request.CWD, "loopback alone must not imply a shared filesystem")
			result := "daemon result"
			encoder := json.NewEncoder(w)
			require.NoError(t, encoder.Encode(chat.ChatEvent{Kind: "result", Result: &result}))
			require.NoError(t, encoder.Encode(chat.ChatEvent{Kind: "done", ConversationID: request.ConversationID}))
		default:
			assert.Fail(t, "unexpected endpoint", "%s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer daemon.Close()
	root := t.TempDir()
	invalidStore := filepath.Join(root, "not-a-directory")
	require.NoError(t, os.WriteFile(invalidStore, []byte("no local state"), 0o600))
	env := []string{"HOME=" + root, "PATH=" + root, "KODELET_BASE_PATH=" + invalidStore, "KODELET_TEST_CLI_PROCESS=1", "KODELET_SERVER=" + daemon.URL, "KODELET_AUTH_TOKEN=client"}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for _, args := range [][]string{{"run", "--result-only", "hello"}, {"--result-only", "hello"}} {
		output, err := daemonCLIProcess(ctx, t, root, env, args...).CombinedOutput()
		require.NoError(t, err, "%s", output)
		assert.Equal(t, "daemon result\n", string(output))
	}
	before := calls.Load()
	for _, args := range [][]string{{"run", "--no-save", "hello"}, {"run", "--no-save=false", "hello"}, {"pr", "--no-save"}, {"commit", "--save"}} {
		output, err := daemonCLIProcess(ctx, t, root, env, args...).CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(output), "unknown flag")
	}
	assert.Equal(t, before, calls.Load(), "removed persistence flags must fail before any HTTP side effect")
}

func TestRemoteRunRequestRejectsUnsupportedFlagsBeforeReadingInputs(t *testing.T) {
	for _, flag := range []string{"--no-save", "--no-save=false", "--sysprompt=missing.txt", "--fragment-dirs=missing", "--max-turns=-1", "--max-tokens=0", "--follow --resume=existing"} {
		t.Run(flag, func(t *testing.T) {
			cmd := remoteRunCommandForTest()
			require.NoError(t, cmd.ParseFlags(strings.Fields(flag)))
			_, _, err := remoteRunRequest(cmd, nil)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "no query provided")
		})
	}
}

func TestRemoteRunRequestReadsStdinAndClientAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-only.png")
	require.NoError(t, os.WriteFile(path, []byte("image bytes"), 0o600))
	cmd := remoteRunCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{"--image=" + path, "--cwd=/runner-only/repo", "--recipe=review", "--arg=target=main"}))
	request := withPipeStdin(t, "piped details", func() chat.ChatRequest {
		request, _, err := remoteRunRequest(cmd, []string{"check this"})
		require.NoError(t, err)
		return request
	})
	assert.Equal(t, "/runner-only/repo", request.CWD)
	assert.Equal(t, "/review target=main check this\npiped details", request.Message)
	require.Len(t, request.Content, 1)
	assert.Equal(t, "data:image/png;base64,aW1hZ2UgYnl0ZXM=", request.Content[0].ImageURL.URL)
	_, images, err := chat.NormalizeRequest(request)
	require.NoError(t, err)
	assert.Len(t, images, 1)
}

func TestRemoteRecipeArgumentsRoundTrip(t *testing.T) {
	arguments := map[string]string{
		"quoted": `say "hello"`, "slashes": `a\b\`, "empty": "",
		"controls": "line1\nline2\t\r\x00\a\b\f\v", "unicode": "你好 🌍",
		"literal": `\n\t`, "equals": "a=b",
	}
	cmd := remoteRunCommandForTest()
	require.NoError(t, cmd.Flags().Set("recipe", "review"))
	// Encode the complete flag map using pflag's CSV syntax.
	values := make([]string, 0, len(arguments))
	for key, value := range arguments {
		values = append(values, key+"="+value)
	}
	var encodedFlags strings.Builder
	writer := csv.NewWriter(&encodedFlags)
	require.NoError(t, writer.Write(values))
	writer.Flush()
	require.NoError(t, writer.Error())
	require.NoError(t, cmd.Flags().Set("arg", encodedFlags.String()))
	request := withDevNullStdin(t, func() chat.ChatRequest {
		request, _, err := remoteRunRequest(cmd, []string{"extra instructions"})
		require.NoError(t, err)
		return request
	})
	command, encoded, found := slashcommands.Parse(request.Message)
	require.True(t, found)
	assert.Equal(t, "review", command)
	decoded, instructions := slashcommands.ParseArgs(encoded)
	assert.Equal(t, arguments, decoded)
	assert.Equal(t, "extra instructions", instructions)
}

func TestOneShotFollowResolvesRunnerDirectory(t *testing.T) {
	for _, selector := range []string{"chosen", ""} {
		for _, cwd := range []string{".", "~/project", "/runner/link", "/runner/project/"} {
			t.Run(selector+"/"+cwd, func(t *testing.T) {
				discovered := false
				daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/runners":
						require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"runners": []runnerregistry.Runner{{ID: "chosen", Connected: true, Status: runnerregistry.RunnerStatusIdle, Workspace: protocol.Workspace{Path: "/runner/startup"}}}}))
					case "/api/chat/settings":
						require.NoError(t, json.NewEncoder(w).Encode(chat.ControlPlaneChatSettings{DefaultRunnerID: "chosen", DefaultRunnerReady: true}))
					case "/api/chat/slash-commands":
						assert.Equal(t, "chosen", r.URL.Query().Get("runnerId"))
						assert.Equal(t, cwd, r.URL.Query().Get("cwd"))
						discovered = true
						_, _ = io.WriteString(w, `{"cwd":"/runner/project"}`)
					case "/api/conversations":
						assert.True(t, discovered)
						assert.Equal(t, "/runner/project", r.URL.Query().Get("cwd"))
						_, _ = io.WriteString(w, `{"conversations":[{"id":"saved","metadata":{"runner_id":"chosen"}}]}`)
					case "/api/conversations/saved":
						_, _ = io.WriteString(w, `{"id":"saved","runnerId":"chosen","cwd":"/runner/project"}`)
					default:
						assert.Fail(t, "unexpected request", "%s", r.URL)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer daemon.Close()
				cmd := remoteRunCommandForTest()
				require.NoError(t, cmd.ParseFlags([]string{"--follow", "--runner=" + selector}))
				request := chat.ChatRequest{CWD: cwd}
				_, err := prepareOneShotRunner(t.Context(), cmd, daemon.URL, "", &request)
				require.NoError(t, err)
				assert.Equal(t, "saved", request.ConversationID)
				assert.Equal(t, "/runner/project", request.CWD)
			})
		}
	}
}

func TestRemoteRunImageRejectsLocalReadFailureAndInsecureURL(t *testing.T) {
	_, err := remoteRunImage("http://example.com/image.png")
	require.ErrorContains(t, err, "HTTPS")
	_, err = remoteRunImage(filepath.Join(t.TempDir(), "missing.png"))
	require.ErrorContains(t, err, "image attachment")
}

func TestRemoteRunStreamsThroughDaemonWithoutLocalDatabase(t *testing.T) {
	// A local store path that cannot be opened proves the command never loads
	// resume/configuration state or starts database migrations locally.
	base := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(base, []byte("no local database"), 0o600))
	t.Setenv("KODELET_BASE_PATH", base)
	t.Setenv("KODELET_AUTH_TOKEN", "")
	var received chat.ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client-token", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/runners":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"runners": []runnerregistry.Runner{{ID: "runner-1", Connected: true, Status: runnerregistry.RunnerStatusIdle}}}))
		case "/api/chat":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
			encoder := json.NewEncoder(w)
			final := "final answer"
			for _, event := range []chat.ChatEvent{
				{Kind: "text-delta", Delta: "intermediate response"},
				{Kind: "tool-use", ToolName: "bash"},
				{Kind: "result", Result: &final},
				{Kind: "done", ConversationID: received.ConversationID},
			} {
				require.NoError(t, encoder.Encode(event))
			}
		default:
			assert.Fail(t, "unexpected endpoint", "%s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cmd := remoteRunCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{"--server=" + server.URL, "--auth-token=client-token", "--runner=runner-1", "--cwd=/remote/repo", "--result-only", "--no-tools"}))
	var output, diagnostics bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&diagnostics)
	cmd.SetContext(t.Context())
	require.NoError(t, initializeCommandResources(cmd, nil))
	err := withPipeStdin(t, "input", func() error { return runControlPlaneCommand(cmd, []string{"query"}) })
	require.NoError(t, err)
	assert.Equal(t, "final answer\n", output.String())
	assert.Empty(t, diagnostics.String())
	assert.Equal(t, "runner-1", received.RunnerID)
	assert.Equal(t, "/remote/repo", received.CWD)
	assert.Equal(t, "query\ninput", received.Message)
	assert.NotEmpty(t, received.ConversationID)
	assert.NotEmpty(t, received.TurnID)
	require.NotNil(t, received.Options)
	assert.True(t, *received.Options.NoTools)
}

type remoteRunClientStub struct {
	run  func(context.Context, chat.ChatRequest, chat.ChatEventSink) (string, error)
	stop func(context.Context, string, string) error
}

type terminalPromptSignalWriter struct {
	ready chan struct{}
	once  sync.Once
}

func (w *terminalPromptSignalWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), "> ") {
		w.once.Do(func() { close(w.ready) })
	}
	return len(data), nil
}

func TestRemoteRunCancellationWhileWaitingForTerminalPrompt(t *testing.T) {
	input, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = input.Close(); _ = writer.Close() })
	prompt := &terminalPromptSignalWriter{ready: make(chan struct{})}
	broker := extensions.NewTerminalUIInputBroker(input, prompt)
	broker.Interactive = true
	stopped := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "ui-input-request", ConversationID: "conversation", UIInput: &chat.UIInputEvent{ID: "prompt", Title: "Waiting"}}))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/api/conversations/conversation/stop":
			stopped <- r.URL.Query().Get("turnId")
			_, _ = io.WriteString(w, `{"stopped":true}`)
		default:
			t.Errorf("unexpected request after prompt cancellation: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	runner, err := chat.NewControlPlaneChatRunner(server.URL, "", "")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = extensions.ContextWithUIInputBroker(ctx, broker)
	done := make(chan error, 1)
	go func() {
		done <- executeRemoteRun(ctx, runner, chat.ChatRequest{ConversationID: "conversation", TurnID: "exact-turn", Message: "hello"}, false, io.Discard, io.Discard)
	}()
	select {
	case <-prompt.ready:
	case <-time.After(time.Second):
		t.Fatal("terminal prompt was not displayed")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorContains(t, err, "query was stopped")
	case <-time.After(time.Second):
		t.Fatal("terminal read prevented the scoped stop request")
	}
	assert.Equal(t, "exact-turn", <-stopped)
}

func (c remoteRunClientStub) Run(ctx context.Context, req chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
	return c.run(ctx, req, sink)
}

func (c remoteRunClientStub) StopConversationTurn(ctx context.Context, id, turn string) error {
	return c.stop(ctx, id, turn)
}

func TestRemoteRunCancellationUsesScopedAcknowledgement(t *testing.T) {
	for _, acknowledged := range []bool{true, false} {
		t.Run(map[bool]string{true: "acknowledged", false: "unreachable"}[acknowledged], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stopped := false
			client := remoteRunClientStub{
				run: func(ctx context.Context, _ chat.ChatRequest, _ chat.ChatEventSink) (string, error) {
					cancel()
					return "conversation", ctx.Err()
				},
				stop: func(ctx context.Context, id, turn string) error {
					stopped = true
					assert.NoError(t, ctx.Err())
					_, bounded := ctx.Deadline()
					assert.True(t, bounded)
					assert.Equal(t, "conversation", id)
					assert.Equal(t, "turn", turn)
					if !acknowledged {
						return errors.New("daemon unavailable")
					}
					return nil
				},
			}
			err := executeRemoteRun(ctx, client, chat.ChatRequest{ConversationID: "conversation", TurnID: "turn"}, true, io.Discard, io.Discard)
			require.ErrorIs(t, err, context.Canceled)
			assert.True(t, stopped)
			if acknowledged {
				assert.Contains(t, err.Error(), "query was stopped")
			} else {
				assert.Contains(t, err.Error(), "may still be running")
			}
		})
	}
}

func TestRemoteRunDisconnectDoesNotRetryOrCancel(t *testing.T) {
	calls := 0
	client := remoteRunClientStub{
		run: func(context.Context, chat.ChatRequest, chat.ChatEventSink) (string, error) {
			calls++
			return "", io.ErrUnexpectedEOF
		},
		stop: func(context.Context, string, string) error {
			assert.Fail(t, "disconnect must not cancel daemon execution")
			return nil
		},
	}
	err := executeRemoteRun(t.Context(), client, chat.ChatRequest{ConversationID: "conversation", TurnID: "turn"}, true, io.Discard, io.Discard)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.Contains(t, err.Error(), "before sending it again")
	assert.Equal(t, 1, calls)
}

func TestRemoteRunSinkStreamingAndEmptyResult(t *testing.T) {
	var output, diagnostics bytes.Buffer
	sink := &remoteRunSink{output: &output, diagnostics: &diagnostics}
	for _, event := range []chat.ChatEvent{
		{Kind: "text-delta", Delta: "answer"},
		{Kind: "text", Content: "answer"},
		{Kind: "content-end"},
		{Kind: "tool-use", ToolName: "file_read", Input: `{}`},
		{Kind: "tool-result", ToolOutput: "contents"},
		{Kind: "text", Content: "non-streaming answer"},
	} {
		require.NoError(t, sink.Send(event))
	}
	assert.Equal(t, "answer\nnon-streaming answer\n", output.String())
	assert.Contains(t, diagnostics.String(), "file_read")
	assert.Contains(t, diagnostics.String(), "contents")
	output.Reset()
	client := remoteRunClientStub{run: func(_ context.Context, _ chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
		value := ""
		require.NoError(t, sink.Send(chat.ChatEvent{Kind: "result", Result: &value}))
		return "conversation", nil
	}}
	require.NoError(t, executeRemoteRun(t.Context(), client, chat.ChatRequest{ConversationID: "conversation"}, true, &output, io.Discard))
	assert.Equal(t, "\n", output.String())
}
