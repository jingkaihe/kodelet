package client

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionTestMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

// This peer acknowledges delivery immediately, including before initialize
// returns. Only the inner RPC response completes an extension operation.
type sessionTestPeer struct {
	recordingPeer
	service      *Service
	registration extensions.InitializeResult
	closeOnInit  bool
	failInit     bool
	frames       chan protocol.ExtensionFrame
	sessionMu    sync.Mutex
	initializes  []protocol.ExtensionFrame
	events       map[string][]string
}

func (p *sessionTestPeer) Call(ctx context.Context, method string, params, result any) error {
	if method != protocol.MethodSessionExtensionFrame {
		return p.recordingPeer.Call(ctx, method, params, result)
	}
	frame := params.(protocol.ExtensionFrame)
	if frame.Close {
		return nil
	}
	var message sessionTestMessage
	if err := json.Unmarshal(frame.Message, &message); err != nil {
		return err
	}
	switch message.Method {
	case "extension.initialize":
		p.sessionMu.Lock()
		p.initializes = append(p.initializes, frame)
		p.sessionMu.Unlock()
		if p.closeOnInit {
			frame.Close, frame.Message = true, nil
			return p.deliver(frame)
		}
		if p.failInit {
			frame.Message = json.RawMessage(`{"jsonrpc":"2.0","id":` + string(message.ID) + `,"error":{"code":-32000,"message":"initialization failed"}}`)
			return p.deliver(frame)
		}
		return p.reply(frame, message.ID, p.registration)
	case "extension.event.handle":
		var event struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(message.Params, &event); err != nil {
			return err
		}
		p.sessionMu.Lock()
		p.events[frame.RunID] = append(p.events[frame.RunID], event.Event)
		p.sessionMu.Unlock()
		return p.reply(frame, message.ID, map[string]any{})
	default:
		if len(message.ID) == 0 && message.Method != "$/cancelRequest" {
			return nil
		}
		select {
		case p.frames <- frame:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (p *sessionTestPeer) reply(frame protocol.ExtensionFrame, id json.RawMessage, result any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		return err
	}
	frame.Message = payload
	return p.deliver(frame)
}

func (p *sessionTestPeer) deliver(frame protocol.ExtensionFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	_, rpcErr := p.service.HandleRequest(context.Background(), protocol.MethodSessionExtensionFrame, payload)
	if rpcErr != nil {
		return rpcErr
	}
	return nil
}

func (p *sessionTestPeer) next(t *testing.T) (protocol.ExtensionFrame, sessionTestMessage) {
	t.Helper()
	select {
	case frame := <-p.frames:
		var message sessionTestMessage
		require.NoError(t, json.Unmarshal(frame.Message, &message))
		return frame, message
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an extension frame")
		return protocol.ExtensionFrame{}, sessionTestMessage{}
	}
}

func newSessionTestService(t *testing.T, config llmtypes.Config) (*Service, *sessionTestPeer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	service := newRegisteredTestService(t, t.TempDir(), ServiceOptions{
		ConfigLoader: func(string) (llmtypes.Config, error) { return config, nil },
	})
	peer := &sessionTestPeer{
		service: service, frames: make(chan protocol.ExtensionFrame, 64), events: make(map[string][]string),
		registration: extensions.InitializeResult{
			Name: "inline", Tools: []extensions.ToolRegistration{{Name: "inline_tool", Description: "An inline callback", InputSchema: map[string]any{"type": "object"}}},
			Subscriptions: []extensions.Subscription{{Event: extensions.EventSessionStart}, {Event: extensions.EventResourcesDiscover}, {Event: extensions.EventSessionEnd}},
		},
	}
	service.Attach(peer)
	return service, peer
}

func sessionTestOpen(runID string) protocol.RunOpenParams {
	return protocol.RunOpenParams{
		RunID: runID, ConversationID: "conversation-" + runID,
		SessionExtensions:  &protocol.SessionExtensions{ID: "attachment", ExtensionIDs: []string{"inline-1"}},
		ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: true},
	}
}

func TestServiceSessionExtensionsInitializeExecuteReverseAndClose(t *testing.T) {
	service, peer := newSessionTestService(t, llmtypes.Config{AllowedTools: []string{"inline_tool"}})
	manifest, err := service.openRun(t.Context(), sessionTestOpen("run-1"))
	require.NoError(t, err)
	require.Len(t, manifest.Tools, 1)
	assert.Equal(t, "session:inline-1", manifest.Tools[0].ExtensionID)
	assert.Equal(t, []string{"inline-1"}, manifest.SessionExtensionIDs)
	digest, err := runnerpayload.ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.Equal(t, digest, manifest.Digest)
	peer.sessionMu.Lock()
	assert.Equal(t, []string{extensions.EventSessionStart, extensions.EventResourcesDiscover}, peer.events["run-1"])
	require.Len(t, peer.initializes, 1)
	assert.Contains(t, string(peer.initializes[0].Message), `"id":"session:inline-1"`)
	assert.Contains(t, string(peer.initializes[0].Message), `"backgroundTasks":false`)
	peer.sessionMu.Unlock()

	completed := make(chan runnerpayload.ToolExecuteResult, 1)
	go func() {
		result, err := service.executeTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "run-1", ToolCallID: "tool-1", Name: "inline_tool", Input: json.RawMessage(`{}`)})
		assert.NoError(t, err)
		completed <- result
	}()
	frame, execute := peer.next(t)
	assert.Equal(t, "extension.tool.execute", execute.Method)
	assert.Equal(t, "inline-1", frame.ExtensionID)
	request := frame
	request.Message = mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 100, "parentId": execute.ID, "method": "kodelet.ui.confirm", "params": map[string]any{"title": "Continue?"}})
	require.NoError(t, peer.deliver(request))
	_, response := peer.next(t)
	assert.Contains(t, string(response.Result), `"confirmed":true`)
	require.NoError(t, peer.reply(frame, execute.ID, extensions.ToolExecutionResult{Content: "callback result"}))
	select {
	case result := <-completed:
		assert.True(t, result.Result.Structured.Success)
		assert.Contains(t, result.Result.AssistantFacing, "callback result")
	case <-time.After(3 * time.Second):
		t.Fatal("tool did not finish")
	}
	require.NoError(t, service.closeRun(t.Context(), "run-1"))
	peer.sessionMu.Lock()
	assert.Equal(t, []string{extensions.EventSessionStart, extensions.EventResourcesDiscover, extensions.EventSessionEnd}, peer.events["run-1"], "closing replies must remain routable through session.end")
	peer.sessionMu.Unlock()
	assert.Error(t, peer.deliver(request), "closed runs must not accept stale callback messages")
}

func TestServiceSessionExtensionPolicyAndCollisions(t *testing.T) {
	for _, tt := range []struct {
		name       string
		settings   map[string]any
		allowed    []string
		reserved   []string
		ids        []string
		wantErr    string
		wantNoTool bool
	}{
		{name: "disabled", settings: map[string]any{"enabled": false}, wantErr: "disabled"},
		{name: "path allow is not logical", settings: map[string]any{"allow": []string{"./inline-1"}}, wantErr: "not allowed"},
		{name: "deny wins", settings: map[string]any{"allow": []string{"session:inline-1"}, "deny": []string{"session:inline-1"}}, wantErr: "not allowed"},
		{name: "logical allow", settings: map[string]any{"allow": []string{"session:inline-1"}}, allowed: []string{"inline_tool"}},
		{name: "tool disabled", settings: map[string]any{"tools": map[string]any{"inline_tool": map[string]any{"enabled": false}}}, allowed: []string{"inline_tool"}, wantNoTool: true},
		{name: "tool ceiling", allowed: []string{"file_read"}, wantNoTool: true},
		{name: "reserved collision", reserved: []string{"inline_tool"}, wantErr: "reserved"},
		{name: "duplicate registration", ids: []string{"inline-1", "inline-2"}, wantErr: "duplicate extension tool"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, peer := newSessionTestService(t, llmtypes.Config{ExtensionSettings: tt.settings, AllowedTools: tt.allowed})
			params := sessionTestOpen("policy")
			params.ReservedToolNames = tt.reserved
			if tt.ids != nil {
				params.SessionExtensions.ExtensionIDs = tt.ids
			}
			manifest, err := service.openRun(t.Context(), params)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				service.mu.Lock()
				assert.Empty(t, service.runs)
				service.mu.Unlock()
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []string{"inline-1"}, manifest.SessionExtensionIDs, "durable callback requirement must survive tool filtering")
			if tt.wantNoTool {
				assert.NotContains(t, manifestToolNames(manifest), "inline_tool")
				result, err := service.executeTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "policy", ToolCallID: "blocked", Name: "inline_tool", Input: json.RawMessage(`{}`)})
				require.NoError(t, err)
				assert.False(t, result.Result.Structured.Success)
				assert.Empty(t, peer.frames, "policy must block before invoking the callback")
			}
		})
	}
}

func TestServiceSessionExtensionDisconnectAndRunIsolation(t *testing.T) {
	service, peer := newSessionTestService(t, llmtypes.Config{AllowedTools: []string{"inline_tool"}})
	for _, id := range []string{"first", "second"} {
		_, err := service.openRun(t.Context(), sessionTestOpen(id))
		require.NoError(t, err)
	}
	service.mu.Lock()
	first := service.runs["first"].attachments["inline-1"]
	second := service.runs["second"].attachments["inline-1"]
	service.mu.Unlock()
	assert.NotSame(t, first, second)
	completed := make(chan runnerpayload.ToolExecuteResult, 1)
	go func() {
		result, _ := service.executeTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "first", ToolCallID: "pending", Name: "inline_tool", Input: json.RawMessage(`{}`)})
		completed <- result
	}()
	frame, _ := peer.next(t)
	wrong := frame
	wrong.AttachmentID = "other"
	assert.Error(t, peer.deliver(wrong))
	wrong = frame
	wrong.ExtensionID = "unknown"
	assert.Error(t, peer.deliver(wrong))
	frame.Message, frame.Close = nil, true
	require.NoError(t, peer.deliver(frame))
	select {
	case result := <-completed:
		assert.False(t, result.Result.Structured.Success)
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not fail pending tool")
	}
	result, err := service.executeTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "first", ToolCallID: "retry", Name: "inline_tool", Input: json.RawMessage(`{}`)})
	require.NoError(t, err)
	assert.False(t, result.Result.Structured.Success)
	assert.Empty(t, peer.frames, "a disconnected attachment must never initialize again or replay a call")
	second.mu.Lock()
	assert.False(t, second.closed)
	second.mu.Unlock()
	peer.sessionMu.Lock()
	assert.Len(t, peer.initializes, 2)
	peer.sessionMu.Unlock()
}

func TestSessionExtensionTransportAcknowledgesWithoutReaderAndClosesBlockedIO(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	transport := newSessionExtensionTransport(ctx, &recordingPeer{}, protocol.ExtensionFrame{AttachmentID: "attachment", RunID: "run", ExtensionID: "inline-1"})
	require.NoError(t, transport.deliver(json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)), "delivery must not wait for the pipe reader")
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, transport)
		done <- err
	}()
	select {
	case err := <-done:
		assert.True(t, err == nil || errors.Is(err, io.ErrClosedPipe))
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock pipe I/O")
	}
	require.NoError(t, transport.Close())
}

func TestServiceSessionExtensionOpenFailureClosesChannels(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "initialize error", true: "disconnect during initialize"}[disconnect], func(t *testing.T) {
			service, peer := newSessionTestService(t, llmtypes.Config{})
			peer.closeOnInit, peer.failInit = disconnect, !disconnect
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			_, err := service.openRun(ctx, sessionTestOpen("failed-open"))
			require.Error(t, err)
			assert.NotErrorIs(t, err, context.DeadlineExceeded)
			service.mu.Lock()
			assert.Empty(t, service.runs)
			service.mu.Unlock()
			peer.sessionMu.Lock()
			assert.Len(t, peer.initializes, 1, "failed attachments must not restart")
			assert.Empty(t, peer.events)
			peer.sessionMu.Unlock()
		})
	}
}

func TestServiceSessionExtensionCancellationRelaysInnerCancel(t *testing.T) {
	service, peer := newSessionTestService(t, llmtypes.Config{AllowedTools: []string{"inline_tool"}})
	_, err := service.openRun(t.Context(), sessionTestOpen("cancel"))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	completed := make(chan struct{})
	go func() {
		_, _ = service.executeTool(ctx, runnerpayload.ToolExecuteParams{RunID: "cancel", ToolCallID: "pending", Name: "inline_tool", Input: json.RawMessage(`{}`)})
		close(completed)
	}()
	frame, request := peer.next(t)
	cancel()
	_, notification := peer.next(t)
	assert.Equal(t, "$/cancelRequest", notification.Method)
	assert.JSONEq(t, `{"id":`+string(request.ID)+`}`, string(notification.Params))
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not finish the operation")
	}
	service.mu.Lock()
	transport := service.runs[frame.RunID].attachments[frame.ExtensionID]
	service.mu.Unlock()
	transport.mu.Lock()
	assert.False(t, transport.closed, "operation cancellation is not an attachment disconnect")
	transport.mu.Unlock()
}

func TestSessionExtensionTransportFailsClosedOnQueueOverflow(t *testing.T) {
	transport := newSessionExtensionTransport(t.Context(), &recordingPeer{}, protocol.ExtensionFrame{AttachmentID: "attachment", RunID: "run", ExtensionID: "inline-1"})
	t.Cleanup(func() { require.NoError(t, transport.Close()) })
	var err error
	for range sessionExtensionQueueSize + 2 {
		err = transport.deliver(json.RawMessage(`{"id":1,"result":{}}`))
		if err != nil {
			break
		}
	}
	require.ErrorContains(t, err, "queue is full")
	require.ErrorContains(t, transport.deliver(json.RawMessage(`{"id":2,"result":{}}`)), "closed")
}

func TestServiceSessionExtensionsDoNotRetainWithInstalledBackgroundRuntime(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	peer := &sessionTestPeer{
		service: service, frames: make(chan protocol.ExtensionFrame, 8), events: make(map[string][]string),
		registration: extensions.InitializeResult{Name: "inline", Subscriptions: []extensions.Subscription{{Event: extensions.EventSessionEnd}}},
	}
	service.Attach(peer)
	params := sessionTestOpen("mixed")
	_, err := service.openRun(t.Context(), params)
	require.NoError(t, err)
	state := backgroundState(t, workspace, params.ConversationID)
	require.NotEmpty(t, state.LeaseID, "installed extension background capability must remain enabled")
	service.mu.Lock()
	transport := service.runs[params.RunID].attachments["inline-1"]
	service.mu.Unlock()
	require.NoError(t, service.closeRun(t.Context(), params.RunID))
	transport.mu.Lock()
	assert.True(t, transport.closed, "callbacks must end at run.close despite another extension's lease")
	transport.mu.Unlock()
	for _, descriptor := range []*protocol.SessionExtensions{nil, params.SessionExtensions} {
		reopen := params
		reopen.RunID = "reattach"
		reopen.SessionExtensions = descriptor
		_, err := service.openRun(t.Context(), reopen)
		require.ErrorContains(t, err, "retained background resources")
	}
	require.NoError(t, service.Close())
	assertBackgroundProcessStopped(t, state.PID)
}
