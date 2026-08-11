package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticRemoteChatProvider struct {
	client   RemoteChatClient
	runnerID string
	err      error
}

type blockingRemoteChatProvider struct{}

func (blockingRemoteChatProvider) WaitForRemoteChat(ctx context.Context) (RemoteChatClient, string, error) {
	<-ctx.Done()
	return nil, "", ctx.Err()
}

type switchableRemoteChatProvider struct {
	mu       sync.Mutex
	client   RemoteChatClient
	runnerID string
	blocked  bool
}

func (p *switchableRemoteChatProvider) WaitForRemoteChat(ctx context.Context) (RemoteChatClient, string, error) {
	p.mu.Lock()
	blocked := p.blocked
	client := p.client
	runnerID := p.runnerID
	p.mu.Unlock()
	if blocked {
		<-ctx.Done()
		return nil, "", ctx.Err()
	}
	return client, runnerID, nil
}

func (p *switchableRemoteChatProvider) setBlocked(blocked bool) {
	p.mu.Lock()
	p.blocked = blocked
	p.mu.Unlock()
}

type recordingRemoteCommandSource struct {
	commands []slashcommands.Command
	profile  string
}

func (s *recordingRemoteCommandSource) Commands(_ context.Context, profile string) ([]slashcommands.Command, error) {
	s.profile = profile
	return append([]slashcommands.Command(nil), s.commands...), nil
}

func (p staticRemoteChatProvider) WaitForRemoteChat(context.Context) (RemoteChatClient, string, error) {
	return p.client, p.runnerID, p.err
}

type fakeRemoteChatClient struct {
	mu sync.Mutex

	history  chat.ConversationHistory
	run      func(context.Context, chat.ChatRequest, chat.ChatEventSink) (string, error)
	stop     func(string)
	stopTurn func(string, string)
	stopErr  error
	requests []chat.ChatRequest
	stopped  []string
}

func (c *fakeRemoteChatClient) Run(ctx context.Context, request chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
	c.mu.Lock()
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	if c.run != nil {
		return c.run(ctx, request, sink)
	}
	return request.ConversationID, nil
}

func (c *fakeRemoteChatClient) ListConversations(context.Context, int) ([]convtypes.ConversationSummary, error) {
	return nil, nil
}

func (c *fakeRemoteChatClient) LoadConversation(context.Context, string) (chat.ConversationHistory, error) {
	return c.history, nil
}

func (c *fakeRemoteChatClient) StopConversation(_ context.Context, conversationID string) error {
	return c.StopConversationTurn(context.Background(), conversationID, "")
}

func (c *fakeRemoteChatClient) StopConversationTurn(_ context.Context, conversationID, turnID string) error {
	if c.stopErr != nil {
		return c.stopErr
	}
	if c.stopTurn != nil {
		c.stopTurn(conversationID, turnID)
		return nil
	}
	if c.stop != nil {
		c.stop(conversationID)
		return nil
	}
	c.mu.Lock()
	c.stopped = append(c.stopped, conversationID)
	c.mu.Unlock()
	return nil
}

func (c *fakeRemoteChatClient) recordedRequests() []chat.ChatRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]chat.ChatRequest(nil), c.requests...)
}

func newRemoteACPTestServer(t *testing.T, workspace string, client RemoteChatClient, output *bytes.Buffer) *Server {
	t.Helper()
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(output),
		WithContext(t.Context()),
		WithRemoteSessions(RemoteSessionConfig{
			Provider:           staticRemoteChatProvider{client: client, runnerID: "runner-1"},
			Workspace:          workspace,
			Profile:            "server-profile",
			ReasoningEffort:    "high",
			EnvironmentProfile: "workspace",
		}),
	)
	server.initialized.Store(true)
	t.Cleanup(server.Shutdown)
	return server
}

func createRemoteACPSession(t *testing.T, server *Server, output *bytes.Buffer, workspace string) acptypes.SessionID {
	t.Helper()
	require.NoError(t, server.handleSessionNew(&acptypes.Request{
		ID:     json.RawMessage(`1`),
		Params: mustJSONRawMessage(t, acptypes.NewSessionRequest{CWD: workspace}),
	}))
	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 2)
	response := messages[0]
	result := response["result"].(map[string]any)
	update := messages[1]["params"].(map[string]any)["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateAvailableCommands, update["sessionUpdate"])
	return acptypes.SessionID(result["sessionId"].(string))
}

func TestRemoteACPNewSessionAndPrompt(t *testing.T) {
	workspace := t.TempDir()
	output := bytes.NewBuffer(nil)
	client := &fakeRemoteChatClient{}
	client.run = func(_ context.Context, request chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
		require.NoError(t, sink.Send(chat.ChatEvent{Kind: "text-delta", Delta: "remote answer"}))
		return request.ConversationID, nil
	}
	server := newRemoteACPTestServer(t, workspace, client, output)
	sessionID := createRemoteACPSession(t, server, output, workspace)

	prompt := acptypes.PromptRequest{
		SessionID: sessionID,
		Prompt: []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/review target=main"},
			{Type: acptypes.ContentTypeImage, Data: "aGVsbG8=", MimeType: "image/png"},
		},
	}
	require.NoError(t, server.handleSessionPrompt(&acptypes.Request{ID: json.RawMessage(`2`), Params: mustJSONRawMessage(t, prompt)}))

	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 2)
	update := messages[0]["params"].(map[string]any)["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, update["sessionUpdate"])
	assert.Equal(t, "remote answer", update["content"].(map[string]any)["text"])
	assert.Equal(t, string(acptypes.StopReasonEndTurn), messages[1]["result"].(map[string]any)["stopReason"])

	requests := client.recordedRequests()
	require.Len(t, requests, 1)
	request := requests[0]
	assert.Equal(t, string(sessionID), request.ConversationID)
	assert.Equal(t, "runner-1", request.RunnerID)
	assert.Equal(t, "/review target=main", request.Message)
	assert.NotEmpty(t, request.TurnID)
	assert.Equal(t, "server-profile", request.Profile)
	assert.Equal(t, "high", request.ReasoningEffort)
	assert.Equal(t, "workspace", request.EnvironmentProfile)
	require.Len(t, request.Content, 2)
	assert.Equal(t, "text", request.Content[0].Type)
	assert.Equal(t, "image", request.Content[1].Type)

	require.NoError(t, server.handleSessionPrompt(&acptypes.Request{ID: json.RawMessage(`3`), Params: mustJSONRawMessage(t, prompt)}))
	_ = readJSONRPCMessages(t, output)
	requests = client.recordedRequests()
	require.Len(t, requests, 2)
	assert.NotEqual(t, requests[0].TurnID, requests[1].TurnID)
	assert.Empty(t, requests[1].Profile)
	assert.Empty(t, requests[1].ReasoningEffort)
	assert.Empty(t, requests[1].EnvironmentProfile)
}

func TestOrderedRemoteCancelCannotRacePromptRegistration(t *testing.T) {
	workspace := t.TempDir()
	output := bytes.NewBuffer(nil)
	runCalled := false
	client := &fakeRemoteChatClient{run: func(_ context.Context, request chat.ChatRequest, _ chat.ChatEventSink) (string, error) {
		runCalled = true
		return request.ConversationID, nil
	}}
	server := newRemoteACPTestServer(t, workspace, client, output)
	sessionID := createRemoteACPSession(t, server, output, workspace)

	promptData, err := json.Marshal(acptypes.Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "session/prompt",
		Params: mustJSONRawMessage(t, acptypes.PromptRequest{
			SessionID: sessionID,
			Prompt:    []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "work"}},
		}),
	})
	require.NoError(t, err)
	promptHandler, handled := server.prepareOrderedRemoteMessage(promptData)
	require.True(t, handled)

	cancelData, err := json.Marshal(acptypes.Notification{
		JSONRPC: "2.0",
		Method:  "session/cancel",
		Params:  mustJSONRawMessage(t, acptypes.CancelRequest{SessionID: sessionID}),
	})
	require.NoError(t, err)
	cancelHandler, handled := server.prepareOrderedRemoteMessage(cancelData)
	require.True(t, handled)
	require.NoError(t, cancelHandler())
	require.NoError(t, promptHandler())
	assert.False(t, runCalled)

	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 1)
	assert.Equal(t, string(acptypes.StopReasonCancelled), messages[0]["result"].(map[string]any)["stopReason"])
}

func TestRemoteACPLoadReplaysControlPlaneHistory(t *testing.T) {
	workspace := t.TempDir()
	output := bytes.NewBuffer(nil)
	client := &fakeRemoteChatClient{history: chat.ConversationHistory{
		ID:       "conversation-1",
		RunnerID: "runner-1",
		Messages: []conversations.StreamableMessage{
			{Kind: "text", Role: "user", Content: "hello"},
			{Kind: "text", Role: "assistant", Content: "world"},
		},
	}}
	server := newRemoteACPTestServer(t, workspace, client, output)

	require.NoError(t, server.handleSessionLoad(&acptypes.Request{
		ID: json.RawMessage(`1`),
		Params: mustJSONRawMessage(t, acptypes.LoadSessionRequest{
			SessionID: "conversation-1",
			CWD:       workspace,
		}),
	}))

	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 4)
	first := messages[0]["params"].(map[string]any)["update"].(map[string]any)
	second := messages[1]["params"].(map[string]any)["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateUserMessageChunk, first["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, second["sessionUpdate"])
	assert.NotNil(t, messages[2]["result"])
	commands := messages[3]["params"].(map[string]any)["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateAvailableCommands, commands["sessionUpdate"])
}

func TestRemoteACPLoadRejectsDifferentRunnerAffinity(t *testing.T) {
	workspace := t.TempDir()
	output := bytes.NewBuffer(nil)
	client := &fakeRemoteChatClient{history: chat.ConversationHistory{ID: "conversation-1", RunnerID: "runner-other"}}
	server := newRemoteACPTestServer(t, workspace, client, output)

	require.NoError(t, server.handleSessionLoad(&acptypes.Request{
		ID: json.RawMessage(`1`),
		Params: mustJSONRawMessage(t, acptypes.LoadSessionRequest{
			SessionID: "conversation-1",
			CWD:       workspace,
		}),
	}))

	response := readJSONRPCMessage(t, output)
	assertRPCErrorCode(t, response, acptypes.ErrCodeInternalError)
	assert.Contains(t, response["error"].(map[string]any)["message"], "runner-other")
}

func TestRemoteACPLoadRequiresExactConversationAffinity(t *testing.T) {
	workspace := t.TempDir()
	tests := []struct {
		name    string
		history chat.ConversationHistory
		wantErr string
	}{
		{
			name:    "returned conversation id differs",
			history: chat.ConversationHistory{ID: "conversation-other", RunnerID: "runner-1", EnvironmentProfile: "workspace"},
			wantErr: "while loading conversation-1",
		},
		{
			name:    "conversation has no runner binding",
			history: chat.ConversationHistory{ID: "conversation-1", EnvironmentProfile: "workspace"},
			wantErr: "not bound to a workspace runner",
		},
		{
			name:    "runner profile differs",
			history: chat.ConversationHistory{ID: "conversation-1", RunnerID: "runner-1", EnvironmentProfile: "other"},
			wantErr: "not requested profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newRemoteSessionManager(RemoteSessionConfig{
				Provider:                   staticRemoteChatProvider{client: &fakeRemoteChatClient{history: tt.history}, runnerID: "runner-1"},
				Workspace:                  workspace,
				EnvironmentProfile:         "workspace",
				EnvironmentProfileExplicit: true,
			})

			_, err := manager.loadSession(t.Context(), acptypes.LoadSessionRequest{SessionID: "conversation-1", CWD: workspace})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestRemoteACPReadinessWaitHasDeadline(t *testing.T) {
	manager := newRemoteSessionManager(RemoteSessionConfig{
		Provider:         blockingRemoteChatProvider{},
		Workspace:        t.TempDir(),
		ReadinessTimeout: 10 * time.Millisecond,
	})

	_, err := manager.newSession(t.Context(), acptypes.NewSessionRequest{})
	require.ErrorContains(t, err, "did not become ready within 10ms")
}

func TestRemoteACPCommandsIncludeBuiltInsAndWorkspaceCommands(t *testing.T) {
	source := &recordingRemoteCommandSource{commands: []slashcommands.Command{
		{Name: "review", Description: "Review the workspace"},
		{Name: "goal", Description: "duplicate"},
	}}
	manager := newRemoteSessionManager(RemoteSessionConfig{
		CommandSource: source,
	})
	manager.sessions["session-1"] = &remoteSession{id: "session-1", environmentProfile: "gpu"}

	commands, err := manager.commands(t.Context(), "session-1")
	require.NoError(t, err)
	assert.Equal(t, "gpu", source.profile)
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	assert.Equal(t, []string{"goal", "rename", "review"}, names)
}

func TestRemoteACPSessionsCanRunConcurrently(t *testing.T) {
	manager := newRemoteSessionManager(RemoteSessionConfig{})
	manager.sessions["session-1"] = &remoteSession{id: "session-1"}
	manager.sessions["session-2"] = &remoteSession{id: "session-2"}

	firstPrompt, _, err := manager.beginPrompt("session-1")
	require.NoError(t, err)
	assert.True(t, firstPrompt)
	secondPrompt, _, err := manager.beginPrompt("session-2")
	require.NoError(t, err)
	assert.True(t, secondPrompt)
	_, _, err = manager.beginPrompt("session-1")
	require.ErrorContains(t, err, "already has an active prompt")

	assert.True(t, manager.isActive("session-1"))
	assert.True(t, manager.isActive("session-2"))
	manager.finishPrompt("session-1", true)
	assert.False(t, manager.isActive("session-1"))
	assert.True(t, manager.isActive("session-2"))
}

func TestRemoteACPRunEOFCancelsPromptAndStopsControlPlane(t *testing.T) {
	workspace := t.TempDir()
	stopCalled := make(chan struct{})
	stopOnce := sync.Once{}
	client := &fakeRemoteChatClient{stop: func(string) { stopOnce.Do(func() { close(stopCalled) }) }}
	server := newRemoteACPTestServer(t, workspace, client, bytes.NewBuffer(nil))
	sessionID := acptypes.SessionID("conversation-1")
	server.remoteSessions.mu.Lock()
	server.remoteSessions.sessions[sessionID] = &remoteSession{id: sessionID, started: true, active: true}
	server.remoteSessions.mu.Unlock()

	server.activePromptsMu.Lock()
	cancelCalled := make(chan struct{})
	cancelOnce := sync.Once{}
	server.activePrompts[sessionID] = &activePrompt{
		remoteClient:  client,
		remoteStarted: true,
		cancel:        func() { cancelOnce.Do(func() { close(cancelCalled) }) },
	}
	server.activePromptsMu.Unlock()

	require.NoError(t, server.Run())
	select {
	case <-cancelCalled:
	default:
		t.Fatal("expected prompt context cancellation")
	}
	select {
	case <-stopCalled:
	default:
		t.Fatal("expected StopConversation to be called")
	}
}

func TestRemoteACPCancelBeforeRemoteRunSkipsControlPlaneStop(t *testing.T) {
	workspace := t.TempDir()
	cancelled := make(chan struct{})
	client := &fakeRemoteChatClient{stop: func(string) {
		t.Error("control-plane stop must not be called before the remote turn starts")
	}}
	server := newRemoteACPTestServer(t, workspace, client, bytes.NewBuffer(nil))
	sessionID := acptypes.SessionID("conversation-1")
	server.remoteSessions.mu.Lock()
	server.remoteSessions.sessions[sessionID] = &remoteSession{id: sessionID, active: true}
	server.remoteSessions.mu.Unlock()
	server.activePromptsMu.Lock()
	server.activePrompts[sessionID] = &activePrompt{cancel: func() { close(cancelled) }, turnID: "turn-1"}
	server.activePromptsMu.Unlock()

	server.cancelRemotePrompt(sessionID)

	select {
	case <-cancelled:
	default:
		t.Fatal("expected local prompt cancellation")
	}
	server.activePromptsMu.Lock()
	delete(server.activePrompts, sessionID)
	server.activePromptsMu.Unlock()
}

func TestRemoteACPCancelStopsControlPlaneAndCancelsStream(t *testing.T) {
	workspace := t.TempDir()
	output := bytes.NewBuffer(nil)
	started := make(chan struct{})
	stopCalled := make(chan struct{})
	client := &fakeRemoteChatClient{}
	server := newRemoteACPTestServer(t, workspace, client, output)
	sessionID := createRemoteACPSession(t, server, output, workspace)

	client.run = func(ctx context.Context, request chat.ChatRequest, _ chat.ChatEventSink) (string, error) {
		close(started)
		<-ctx.Done()
		return request.ConversationID, ctx.Err()
	}
	stopOnce := sync.Once{}
	client.stop = func(conversationID string) {
		client.mu.Lock()
		client.stopped = append(client.stopped, conversationID)
		client.mu.Unlock()
		stopOnce.Do(func() { close(stopCalled) })
	}

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- server.handleSessionPrompt(&acptypes.Request{
			ID: json.RawMessage(`2`),
			Params: mustJSONRawMessage(t, acptypes.PromptRequest{
				SessionID: sessionID,
				Prompt:    []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "work"}},
			}),
		})
	}()
	<-started

	notification, err := json.Marshal(acptypes.Notification{
		JSONRPC: "2.0",
		Method:  "session/cancel",
		Params:  mustJSONRawMessage(t, acptypes.CancelRequest{SessionID: sessionID}),
	})
	require.NoError(t, err)
	require.NoError(t, server.handleNotification("session/cancel", notification))
	require.NoError(t, <-promptDone)

	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 1)
	assert.Equal(t, string(acptypes.StopReasonCancelled), messages[0]["result"].(map[string]any)["stopReason"])
}

func TestRemoteACPCancelUsesPromptClientWithoutWaitingForRunnerReadiness(t *testing.T) {
	workspace := t.TempDir()
	output := bytes.NewBuffer(nil)
	started := make(chan struct{})
	stopped := make(chan string, 1)
	client := &fakeRemoteChatClient{}
	provider := &switchableRemoteChatProvider{client: client, runnerID: "runner-1"}
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(output),
		WithContext(t.Context()),
		WithRemoteSessions(RemoteSessionConfig{Provider: provider, Workspace: workspace}),
	)
	server.initialized.Store(true)
	t.Cleanup(server.Shutdown)
	sessionID := createRemoteACPSession(t, server, output, workspace)
	client.run = func(ctx context.Context, request chat.ChatRequest, _ chat.ChatEventSink) (string, error) {
		close(started)
		<-ctx.Done()
		return request.ConversationID, ctx.Err()
	}
	client.stopTurn = func(conversationID, turnID string) {
		assert.Equal(t, string(sessionID), conversationID)
		stopped <- turnID
	}

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- server.handleSessionPrompt(&acptypes.Request{
			ID: json.RawMessage(`2`),
			Params: mustJSONRawMessage(t, acptypes.PromptRequest{
				SessionID: sessionID,
				Prompt:    []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "work"}},
			}),
		})
	}()
	<-started
	provider.setBlocked(true)

	cancelDone := make(chan error, 1)
	go func() {
		notification, err := json.Marshal(acptypes.Notification{
			JSONRPC: "2.0",
			Method:  "session/cancel",
			Params:  mustJSONRawMessage(t, acptypes.CancelRequest{SessionID: sessionID}),
		})
		if err != nil {
			cancelDone <- err
			return
		}
		cancelDone <- server.handleNotification("session/cancel", notification)
	}()

	select {
	case err := <-cancelDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("remote ACP cancellation waited for runner readiness")
	}
	select {
	case turnID := <-stopped:
		assert.NotEmpty(t, turnID)
	case <-time.After(time.Second):
		t.Fatal("cached control-plane client did not receive the scoped stop")
	}
	require.NoError(t, <-promptDone)
}

func TestRemoteACPCancelFailureStillCancelsLocalPrompt(t *testing.T) {
	workspace := t.TempDir()
	output := bytes.NewBuffer(nil)
	started := make(chan struct{})
	finish := make(chan struct{})
	promptContext := make(chan context.Context, 1)
	client := &fakeRemoteChatClient{stopErr: errors.New("stop unavailable")}
	server := newRemoteACPTestServer(t, workspace, client, output)
	sessionID := createRemoteACPSession(t, server, output, workspace)

	client.run = func(ctx context.Context, request chat.ChatRequest, _ chat.ChatEventSink) (string, error) {
		promptContext <- ctx
		close(started)
		select {
		case <-ctx.Done():
			return request.ConversationID, ctx.Err()
		case <-finish:
			return request.ConversationID, nil
		}
	}

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- server.handleSessionPrompt(&acptypes.Request{
			ID: json.RawMessage(`2`),
			Params: mustJSONRawMessage(t, acptypes.PromptRequest{
				SessionID: sessionID,
				Prompt:    []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "work"}},
			}),
		})
	}()
	<-started

	notification, err := json.Marshal(acptypes.Notification{
		JSONRPC: "2.0",
		Method:  "session/cancel",
		Params:  mustJSONRawMessage(t, acptypes.CancelRequest{SessionID: sessionID}),
	})
	require.NoError(t, err)
	require.NoError(t, server.handleNotification("session/cancel", notification))
	assert.ErrorIs(t, (<-promptContext).Err(), context.Canceled)
	close(finish)
	require.NoError(t, <-promptDone)

	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 1)
	assert.Equal(t, string(acptypes.StopReasonCancelled), messages[0]["result"].(map[string]any)["stopReason"])
}

func TestRemoteACPCancelTreatsControlPlaneEOFAsCancelled(t *testing.T) {
	workspace := t.TempDir()
	stopRequested := make(chan struct{})
	chatStarted := make(chan struct{})
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/api/chat":
			var chatRequest chat.ChatRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&chatRequest))
			w.Header().Set("Content-Type", "application/x-ndjson")
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "conversation", ConversationID: chatRequest.ConversationID}))
			w.(http.Flusher).Flush()
			close(chatStarted)
			<-stopRequested
		case strings.HasPrefix(request.URL.Path, "/api/conversations/") && strings.HasSuffix(request.URL.Path, "/stop"):
			close(stopRequested)
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte(`{"stopped":true}`))
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(controlPlane.Close)
	client, err := chat.NewControlPlaneChatRunner(controlPlane.URL, "", "runner-1")
	require.NoError(t, err)
	output := bytes.NewBuffer(nil)
	server := newRemoteACPTestServer(t, workspace, client, output)
	sessionID := createRemoteACPSession(t, server, output, workspace)

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- server.handleSessionPrompt(&acptypes.Request{
			ID: json.RawMessage(`2`),
			Params: mustJSONRawMessage(t, acptypes.PromptRequest{
				SessionID: sessionID,
				Prompt:    []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "work"}},
			}),
		})
	}()
	<-chatStarted

	notification, err := json.Marshal(acptypes.Notification{
		JSONRPC: "2.0",
		Method:  "session/cancel",
		Params:  mustJSONRawMessage(t, acptypes.CancelRequest{SessionID: sessionID}),
	})
	require.NoError(t, err)
	require.NoError(t, server.handleNotification("session/cancel", notification))
	require.NoError(t, <-promptDone)

	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 1)
	assert.Equal(t, string(acptypes.StopReasonCancelled), messages[0]["result"].(map[string]any)["stopReason"])
}
