package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionOptionsPreserveExplicitValues(t *testing.T) {
	options := ExecutionOptions{
		Provider: "anthropic", Model: "claude", MaxTokens: Ptr(512), MaxTurns: Ptr(0),
		NoTools: Ptr(false), NoSkills: Ptr(true), AllowedTools: []string{"file_read", `say "hi"`},
		AllowedCommands: []string{}, EnableFSSearchTools: Ptr(true), CodeMode: "only", ReasoningEffort: "high",
	}
	require.NoError(t, options.validate())
	assert.Equal(t, []string{
		"--provider=anthropic", "--model=claude", "--max-tokens=512", "--reasoning-effort=high", "--max-turns=0",
		"--no-tools=false", "--no-skills=true", `--allowed-tools="file_read","say ""hi"""`, "--allowed-commands=",
		"--enable-fs-search-tools=true", "--code-mode=only",
	}, options.args())
	assert.Empty(t, ExecutionOptions{}.args())

	err := ExecutionOptions{Provider: "gemini", MaxTokens: Ptr(0), ThinkingBudgetTokens: Ptr(-1), AllowedTools: []string{""}, CodeMode: "auto"}.validate()
	require.Error(t, err)
	for _, problem := range []string{"provider", "maxTokens", "thinkingBudgetTokens", "allowedTools", "codeMode"} {
		assert.Contains(t, err.Error(), problem)
	}
}

func TestCreateSessionLaunchesACPWithEndpointProfileAndMetadata(t *testing.T) {
	fake := newFakeACP(t)
	client := fake.client(ClientOptions{Server: "https://kodelet.example", Runner: "workstation", Command: "/bin/kodelet", CWD: "/workspace", Env: map[string]string{"KODELET_TEST": "1"}})
	session, err := client.CreateSession(t.Context(), SessionOptions{
		Profile: "coding", EnvironmentProfile: "restricted", ParentConversationID: " parent-1 ",
		Options: ExecutionOptions{Model: "gpt-5", NoTools: Ptr(true)},
	})
	require.NoError(t, err)
	assert.Equal(t, "conv-1", session.ID())
	assert.Equal(t, "/workspace", session.CWD())
	assert.Equal(t, "/bin/kodelet", fake.command)
	assert.Equal(t, []string{"acp", "--server", "https://kodelet.example", "--runner", "workstation", "--model=gpt-5", "--no-tools=true", "--profile=coding", "--runner-profile=restricted"}, fake.args)
	assert.Contains(t, fake.env, "KODELET_TEST=1")

	initialize := fake.nextRequest("initialize")
	assert.JSONEq(t, `{"protocolVersion":1,"clientCapabilities":{"terminal":true,"fs":{"readTextFile":false,"writeTextFile":false},"_meta":{"sessionExtensions":{"version":1}}},"clientInfo":{"name":"kodelet-sdk-go","title":"Kodelet Go SDK"}}`, string(initialize.Params))
	created := fake.nextRequest("session/new")
	assert.JSONEq(t, `{"cwd":"/workspace","_meta":{"conversationHierarchy":{"version":1,"parentConversationId":"parent-1"}}}`, string(created.Params))

	require.NoError(t, session.Close())
	assert.Equal(t, int32(1), fake.proc.terminated.Load())
	require.NoError(t, session.Close())
}

func TestResumeLoadsTheConversationWithInlineExtensionMetadata(t *testing.T) {
	fake := newFakeACP(t)
	ext := extension.New(extension.Metadata{})
	session, err := fake.client(ClientOptions{CWD: "/w"}).CreateSession(t.Context(), SessionOptions{Resume: "conv-9", Extensions: []*extension.Extension{ext, ext}})
	require.NoError(t, err)
	assert.Equal(t, "conv-9", session.ID())
	load := fake.nextRequest("session/load")
	assert.JSONEq(t, `{"sessionId":"conv-9","cwd":"/w","_meta":{"sessionExtensions":{"version":1,"extensionIds":["inline-1","inline-2"]}}}`, string(load.Params))
}

func TestCreateSessionValidatesHierarchyAndCapabilities(t *testing.T) {
	_, err := NewClient(ClientOptions{}).CreateSession(t.Context(), SessionOptions{ParentConversationID: "p", Resume: "r"})
	require.ErrorContains(t, err, "cannot be combined with resume")
	_, err = NewClient(ClientOptions{}).CreateSession(t.Context(), SessionOptions{ParentConversationID: "  "})
	require.ErrorContains(t, err, "non-empty conversation ID")
	_, err = NewClient(ClientOptions{}).CreateSession(t.Context(), SessionOptions{Extensions: []*extension.Extension{nil}})
	require.ErrorContains(t, err, "extension 1 is nil")

	fake := newFakeACP(t)
	fake.meta = map[string]any{}
	_, err = fake.client(ClientOptions{}).CreateSession(t.Context(), SessionOptions{ParentConversationID: "parent"})
	require.ErrorContains(t, err, "conversationHierarchy version 1")
	assert.Equal(t, int32(1), fake.proc.terminated.Load(), "failed startup closes the process")

	fake = newFakeACP(t)
	fake.meta = map[string]any{}
	_, err = fake.client(ClientOptions{}).CreateSession(t.Context(), SessionOptions{Extensions: []*extension.Extension{extension.New(extension.Metadata{})}})
	require.ErrorContains(t, err, "does not support inline session extensions")
}

func TestRunAndWaitTranslatesACPUpdatesIntoEvents(t *testing.T) {
	fake := newFakeACP(t)
	fake.prompt = func(f *fakeACP, message jsonrpc.Message) {
		f.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "thinking"}})
		f.chunk("Hello ")
		compaction := map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": ""},
			"_meta": map[string]any{"kodelet/contextCompacted": map[string]any{"compaction": map[string]any{"id": "c1", "method": "summary", "summary": "s", "createdAt": "now"}, "beforeCurrentUser": true}},
		}
		f.update(compaction)
		f.update(compaction)
		f.toolCall("t1", "bash", map[string]any{"command": "ls"})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "in_progress"})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "in_progress", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "a"}}}})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "in_progress", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "ab"}}}})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed", "content": []any{map[string]any{"type": "diff", "path": "a.txt", "newText": "x"}}})
		f.update(map[string]any{"sessionUpdate": "plan", "entries": []any{}})
		params, _ := json.Marshal(map[string]any{"sessionId": "other", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": "ignored"}}})
		f.write(jsonrpc.NewNotification("session/update", params))
		f.writeLine("not json")
		f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"text": "world"}}})
		f.respond(message.ID, map[string]any{"stopReason": "end_turn"})
	}
	session := fake.session()
	var mu sync.Mutex
	var seen []string
	session.Subscribe(func(event Event) {
		mu.Lock()
		seen = append(seen, event.Type)
		mu.Unlock()
	})

	response, err := session.RunAndWait(t.Context(), RunOptions{Message: "hi", Images: []string{"data:image/png;base64,AAAA", "https://example.com/a.png"}})
	require.NoError(t, err)
	assert.Equal(t, "Hello world", response.Content)
	assert.Equal(t, "end_turn", response.StopReason)
	assert.Equal(t, "conv-1", response.ConversationID)
	prompt := fake.nextRequest("session/prompt")
	assert.JSONEq(t, `{"sessionId":"conv-1","prompt":[{"type":"text","text":"hi"},{"type":"image","mimeType":"image/png","data":"AAAA"},{"type":"image","uri":"https://example.com/a.png"}]}`, string(prompt.Params))

	types := make([]string, 0, len(response.Events))
	for _, event := range response.Events {
		types = append(types, event.Type)
	}
	expected := []string{
		EventAgentStart, EventUserMessage, EventThinkingStart, EventThinkingDelta, EventAssistantMessageDelta,
		EventContextCompacted, EventToolCall, EventToolUpdate, EventToolResult, EventOther,
		EventAssistantMessageDelta, EventThinkingEnd, EventContentEnd, EventAssistantMessage, EventAgentEnd,
	}
	assert.Equal(t, expected, types, "tool updates are coalesced by tool call ID")
	mu.Lock()
	assert.Len(t, seen, len(expected)+1, "subscribers also see the superseded tool update")
	mu.Unlock()

	byType := func(eventType string) Event {
		index := slices.IndexFunc(response.Events, func(event Event) bool { return event.Type == eventType })
		require.GreaterOrEqual(t, index, 0, eventType)
		return response.Events[index]
	}
	assert.Equal(t, ToolCall{ToolName: "bash", Input: map[string]any{"command": "ls"}, RawInput: `{"command":"ls"}`, ToolCallID: "t1"}, byType(EventToolCall).Data)
	assert.Equal(t, ToolResult{ToolName: "bash", Result: "ab", ToolCallID: "t1", Status: "in_progress"}, byType(EventToolUpdate).Data)
	assert.Equal(t, ToolResult{ToolName: "bash", Result: "a.txt\nx", ToolCallID: "t1", Status: "completed"}, byType(EventToolResult).Data)
	assert.Equal(t, ContextCompacted{Compaction: CompactionMarker{ID: "c1", Method: "summary", Summary: "s", CreatedAt: "now"}, BeforeCurrentUser: true}, byType(EventContextCompacted).Data)
	assert.Equal(t, "plan", byType(EventOther).Data.(map[string]any)["sessionUpdate"])
	assert.Equal(t, ThinkingDelta{DeltaContent: "thinking"}, byType(EventThinkingDelta).Data)
	end := byType(EventAgentEnd).Data.(Response)
	assert.Equal(t, "Hello world", end.Content)
	assert.Len(t, end.Events, len(expected)-1)
}

func TestRunsAreSequentialAndFailuresEmitErrors(t *testing.T) {
	fake := newFakeACP(t)
	release := make(chan struct{})
	fake.prompt = func(f *fakeACP, message jsonrpc.Message) {
		<-release
		f.respondError(message.ID, -32603, "session requires its session extensions; reattach callbacks")
	}
	session := fake.session()
	errs := make(chan error, 1)
	go func() {
		_, err := session.RunAndWait(t.Context(), RunOptions{Message: "first"})
		errs <- err
	}()
	fake.nextRequest("session/prompt")
	_, err := session.RunAndWait(t.Context(), RunOptions{Message: "second"})
	require.ErrorContains(t, err, "another run is in progress")
	close(release)
	err = <-errs
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.Contains(t, rpcErr.Message, "reattach")
}

func TestEarlyCancellationIsWrittenAfterThePrompt(t *testing.T) {
	// Cancelling as soon as a run starts used to race the prompt write, and
	// Kodelet ignores a cancel for a prompt it has not seen yet.
	for range 100 {
		fake := newFakeACP(t)
		var mu sync.Mutex
		var order []string
		fake.handle = func(_ *fakeACP, message jsonrpc.Message) bool {
			if message.Method == "session/prompt" || message.Method == "session/cancel" {
				mu.Lock()
				order = append(order, message.Method)
				mu.Unlock()
			}
			return false
		}
		fake.prompt = respondAfterCancel
		session := fake.session()
		ctx, cancel := context.WithCancel(t.Context())
		var errorEvent Event
		session.Subscribe(func(event Event) {
			switch event.Type {
			case EventAgentStart:
				cancel()
			case EventAgentError:
				errorEvent = event
			}
		})
		_, err := session.RunAndWait(ctx, RunOptions{Message: "x"})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, AgentError{Message: "context canceled"}, errorEvent.Data)
		require.NoError(t, session.Close())
		mu.Lock()
		require.Equal(t, []string{"session/prompt", "session/cancel"}, order)
		mu.Unlock()
	}
}

func TestStreamYieldsEventsAndBreakingCancelsTheRun(t *testing.T) {
	fake := newFakeACP(t)
	fake.prompt = func(f *fakeACP, message jsonrpc.Message) {
		f.chunk("chunk")
		f.respond(message.ID, map[string]any{"stopReason": "end_turn"})
	}
	session := fake.session()
	var types []string
	for event, err := range session.Stream(t.Context(), RunOptions{Message: "go"}) {
		require.NoError(t, err)
		types = append(types, event.Type)
	}
	assert.Equal(t, []string{EventAgentStart, EventUserMessage, EventAssistantMessageDelta, EventContentEnd, EventAssistantMessage, EventAgentEnd}, types)

	fake.prompt = func(f *fakeACP, message jsonrpc.Message) {
		f.chunk("chunk")
		respondAfterCancel(f, message)
	}
	for event := range session.Stream(t.Context(), RunOptions{Message: "stop early"}) {
		if event.Type == EventAssistantMessageDelta {
			break
		}
	}
	fake.prompt = nil
	_, err := session.RunAndWait(t.Context(), RunOptions{Message: "next"})
	require.NoError(t, err, "breaking waits for the cancelled run to settle")
}

func TestStreamWaitsForCallbacksWhenCloseStopsEvents(t *testing.T) {
	fake := newFakeACP(t)
	release := make(chan struct{})
	fake.prompt = func(f *fakeACP, message jsonrpc.Message) {
		f.toolCall("t1", "bash", map[string]any{})
		<-release
		f.respondError(message.ID, -32603, "connection closed")
	}
	session := fake.session()
	inCallback, callbackDone := busyOnToolCall(session, 100*time.Millisecond)
	go func() {
		<-inCallback
		// Close stops the queue after the process; force it to win the race with
		// the run's final flush while the callback is still running.
		session.events.Stop()
		close(release)
	}()
	var types []string
	var runErr error
	for event, err := range session.Stream(t.Context(), RunOptions{Message: "x"}) {
		if err != nil {
			runErr = err
			continue
		}
		types = append(types, event.Type)
	}
	assert.True(t, callbackDone.Load(), "Stream must wait for the callback in progress")
	assert.Equal(t, []string{EventAgentStart, EventUserMessage, EventToolCall}, types)
	require.ErrorContains(t, runErr, "connection closed")
}

func TestClosingDuringAStreamDoesNotPanic(t *testing.T) {
	for range 200 {
		fake := newFakeACP(t)
		fake.prompt = func(f *fakeACP, _ jsonrpc.Message) {
			f.toolCall("t1", "bash", map[string]any{})
		}
		session := fake.session()
		inCallback, _ := busyOnToolCall(session, time.Millisecond)
		closed := make(chan error, 1)
		go func() {
			<-inCallback
			closed <- session.Close()
		}()
		for range session.Stream(t.Context(), RunOptions{Message: "x"}) {
		}
		require.NoError(t, <-closed)
	}
}

func TestSteerRequiresAnActiveRunAndSupport(t *testing.T) {
	fake := newFakeACP(t)
	release := make(chan struct{})
	fake.prompt = func(f *fakeACP, message jsonrpc.Message) {
		<-release
		f.respond(message.ID, map[string]any{})
	}
	session := fake.session()
	_, err := session.Steer(t.Context(), "focus")
	require.ErrorContains(t, err, "without an active run")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.RunAndWait(t.Context(), RunOptions{Message: "work"})
	}()
	fake.nextRequest("session/prompt")
	_, err = session.Steer(t.Context(), "  ")
	require.ErrorContains(t, err, "non-empty")
	result, err := session.Steer(t.Context(), " focus on tests ")
	require.NoError(t, err)
	assert.Equal(t, SteerResult{Outcome: SteeringInjected}, result)
	steering := fake.nextRequest("_session/steering")
	assert.JSONEq(t, `{"sessionId":"conv-1","prompt":[{"type":"text","text":"focus on tests"}],"_meta":{"steering":{"idleBehavior":"promptRequired"}}}`, string(steering.Params))
	close(release)
	waitFor(t, done)
}

func TestProcessExitRejectsPendingRequestsWithStderr(t *testing.T) {
	fake := newFakeACP(t)
	fake.prompt = func(f *fakeACP, _ jsonrpc.Message) {
		f.proc.finish(processExit{Code: 3, Stderr: "daemon unreachable\n"})
	}
	session := fake.session()
	_, err := session.RunAndWait(t.Context(), RunOptions{Message: "x"})
	var runErr *RunError
	require.ErrorAs(t, err, &runErr)
	assert.Equal(t, "daemon unreachable", runErr.Message)
	assert.Equal(t, 3, runErr.ExitCode)
	_, err = session.RunAndWait(t.Context(), RunOptions{Message: "again"})
	require.ErrorAs(t, err, &runErr)
}

func TestCloseEscalatesToKillAndClientCloseClosesSessions(t *testing.T) {
	fake := newFakeACP(t)
	fake.proc.ignoreTerm = true
	client := fake.client(ClientOptions{})
	session, err := client.CreateSession(t.Context(), SessionOptions{})
	require.NoError(t, err)
	started := time.Now()
	require.NoError(t, client.Close())
	assert.GreaterOrEqual(t, time.Since(started), processExitTimeout)
	assert.Equal(t, int32(1), fake.proc.terminated.Load())
	assert.Equal(t, int32(1), fake.proc.killed.Load())
	_, err = session.RunAndWait(t.Context(), RunOptions{Message: "closed"})
	require.ErrorContains(t, err, "closed Kodelet session")
}

func TestUnsupportedServerRequestsAreRejected(t *testing.T) {
	fake := newFakeACP(t)
	_, err := fake.client(ClientOptions{}).CreateSession(t.Context(), SessionOptions{})
	require.NoError(t, err)
	response := fake.call("fs/read_text_file", map[string]any{"path": "/etc/passwd"})
	require.NotNil(t, response.Error)
	assert.Equal(t, jsonrpc.CodeMethodNotFound, response.Error.Code)
	response = fake.call(extensionFrameMethod, map[string]any{})
	require.NotNil(t, response.Error)
	assert.Equal(t, jsonrpc.CodeInvalidParams, response.Error.Code)
}

func TestInlineExtensionsRelayCallbacksLocalUIAndHostRPC(t *testing.T) {
	fake := newFakeACP(t)
	var mu sync.Mutex
	calls := 0
	var capabilities map[string]any
	ext := extension.New(extension.Metadata{Name: "inline"})
	extension.RegisterTool(ext, extension.Tool[struct {
		Text string `json:"text"`
	}]{Name: "sdk_echo", Description: "Echo", Execute: func(ctx context.Context, in struct {
		Text string `json:"text"`
	}, tc *extension.ToolContext,
	) (extension.ToolResult, error) {
		mu.Lock()
		calls++
		call := calls
		capabilities = tc.Capabilities()
		mu.Unlock()
		confirmed, err := tc.UI.Confirm(ctx, extension.ConfirmRequest{Message: "Allow callback?"})
		if err != nil || !confirmed {
			return extension.ToolResult{}, errors.Join(err, errors.New("not confirmed"))
		}
		if _, ok, _ := tc.UI.Input(ctx, extension.InputRequest{Title: "x"}); ok {
			return extension.ToolResult{}, errors.New("input should be unavailable")
		}
		if err := tc.Update(ctx, "callback progress", nil); err != nil {
			return extension.ToolResult{}, err
		}
		return extension.Text("closure:" + in.Text + ":" + string(rune('0'+call))), nil
	}})
	var confirms []extension.ConfirmRequest
	session, err := fake.client(ClientOptions{}).CreateSession(t.Context(), SessionOptions{
		Extensions: []*extension.Extension{ext},
		UI: &UIHandlers{Confirm: func(_ context.Context, request extension.ConfirmRequest) (bool, error) {
			mu.Lock()
			confirms = append(confirms, request)
			mu.Unlock()
			return true, nil
		}},
	})
	require.NoError(t, err)
	created := fake.nextRequest("session/new")
	assert.JSONEq(t, `{"version":1,"extensionIds":["inline-1"]}`, string(mustField(t, created.Params, "_meta", "sessionExtensions")))

	ack := fake.frame("run-1", `{"jsonrpc":"2.0","id":1,"method":"extension.tool.execute","params":{"name":"sdk_echo","input":{"text":"hi"}}}`)
	require.NotNil(t, ack.Error, "a runtime must start with initialize")
	ack = fake.frame("run-1", `{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"protocolVersion":"2026-05-30","extension":{"id":"session:inline-1"},"capabilities":{"toolUpdates":true,"ui":{"widgets":true}}}}`)
	require.Nil(t, ack.Error)
	initialized := fake.nextFrame()
	assert.JSONEq(t, "1", string(initialized.ID))
	assert.Contains(t, string(initialized.Result), `"name":"sdk_echo"`)
	ack = fake.frame("run-1", `{"jsonrpc":"2.0","id":2,"method":"extension.initialize","params":{"extension":{"id":"x"}}}`)
	require.NotNil(t, ack.Error, "a runtime cannot be initialized twice")

	ack = fake.frame("run-1", `{"jsonrpc":"2.0","id":3,"method":"extension.tool.execute","params":{"name":"sdk_echo","input":{"text":"hi"},"context":{"conversationId":"conv-1"}}}`)
	require.Nil(t, ack.Error)
	update := fake.nextFrame()
	assert.Equal(t, "kodelet.tool.update", update.Method)
	assert.JSONEq(t, "3", string(update.ParentID))
	assert.JSONEq(t, `{"content":"callback progress"}`, string(update.Params))
	fake.frame("run-1", `{"jsonrpc":"2.0","id":`+string(update.ID)+`,"result":{}}`)
	result := fake.nextFrame()
	assert.JSONEq(t, "3", string(result.ID))
	assert.JSONEq(t, `{"content":"closure:hi:1"}`, string(result.Result))
	mu.Lock()
	assert.Equal(t, []extension.ConfirmRequest{{Message: "Allow callback?"}}, confirms)
	assert.Equal(t, map[string]any{"widgets": true, "input": false, "confirm": true, "select": false, "notify": false}, capabilities["ui"])
	mu.Unlock()

	ack = fake.call(extensionFrameMethod, map[string]any{"sessionId": "conv-1", "runId": "run-1", "extensionId": "inline-1", "close": true})
	require.Nil(t, ack.Error)
	ack = fake.frame("run-1", `{"jsonrpc":"2.0","id":4,"method":"extension.tool.execute","params":{"name":"sdk_echo","input":{"text":"again"}}}`)
	require.NotNil(t, ack.Error)
	assert.Contains(t, ack.Error.Message, "cannot be replayed")

	for _, invalid := range []map[string]any{
		{"sessionId": "conv-1", "runId": "run-2", "extensionId": "inline-9", "message": map[string]any{"jsonrpc": "2.0", "id": 1, "method": "extension.initialize"}},
		{"sessionId": "other", "runId": "run-2", "extensionId": "inline-1", "message": map[string]any{"jsonrpc": "2.0", "id": 1, "method": "extension.initialize", "params": map[string]any{"extension": map[string]any{"id": "x"}}}},
		{"sessionId": "conv-1", "runId": "run-2", "extensionId": "inline-1", "message": map[string]any{"jsonrpc": "1.0", "id": 1, "method": "x"}},
		{"sessionId": "conv-1", "runId": "run-2", "extensionId": "inline-1", "message": map[string]any{"jsonrpc": "2.0", "id": 1, "method": "extension.initialize", "params": map[string]any{}}},
		{"sessionId": "conv-1", "runId": "run-2", "extensionId": "inline-1", "close": true, "message": map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil}},
	} {
		ack = fake.call(extensionFrameMethod, invalid)
		require.NotNil(t, ack.Error, "%v", invalid)
		assert.Equal(t, jsonrpc.CodeInvalidParams, ack.Error.Code)
	}

	// A new run gets a fresh runtime that reuses the same captured state.
	fake.frame("run-2", `{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"extension":{"id":"session:inline-1"},"capabilities":{}}}`)
	fake.nextFrame()
	fake.frame("run-2", `{"jsonrpc":"2.0","id":2,"method":"extension.tool.execute","params":{"name":"sdk_echo","input":{"text":"two"}}}`)
	result = fake.nextFrame()
	assert.JSONEq(t, `{"content":"closure:two:2"}`, string(result.Result), "updates are skipped without host support")
	require.NoError(t, session.Close())
}

func TestClosingASessionCancelsInlineCallbacks(t *testing.T) {
	fake := newFakeACP(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "wait", Execute: func(ctx context.Context, _ struct{}, _ *extension.ToolContext) (extension.ToolResult, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return extension.ToolResult{}, ctx.Err()
	}})
	session, err := fake.client(ClientOptions{}).CreateSession(t.Context(), SessionOptions{Extensions: []*extension.Extension{ext}})
	require.NoError(t, err)
	fake.frame("run-1", `{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"extension":{"id":"session:inline-1"}}}`)
	fake.nextFrame()
	fake.frame("run-1", `{"jsonrpc":"2.0","id":2,"method":"extension.tool.execute","params":{"name":"wait","input":{}}}`)
	waitFor(t, started)
	require.NoError(t, session.Close())
	waitFor(t, stopped)
}

func mustField(t *testing.T, raw json.RawMessage, path ...string) json.RawMessage {
	t.Helper()
	for _, key := range path {
		var object map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &object))
		raw = object[key]
	}
	return raw
}
