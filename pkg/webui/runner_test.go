package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerAPITestLink struct {
	done chan struct{}
	call func(context.Context, string, any, any) error
}

func newRunnerAPITestLink() *runnerAPITestLink {
	return &runnerAPITestLink{done: make(chan struct{})}
}

func (l *runnerAPITestLink) Call(ctx context.Context, method string, params any, result any) error {
	if l.call != nil {
		return l.call(ctx, method, params, result)
	}
	return nil
}

func (l *runnerAPITestLink) CallTracked(ctx context.Context, method string, params any, result any, onRequestID func(string)) error {
	if onRequestID != nil {
		onRequestID("web-test:request")
	}
	return l.Call(ctx, method, params, result)
}
func (*runnerAPITestLink) Notify(context.Context, string, any) error { return nil }
func (*runnerAPITestLink) Close() error                              { return nil }
func (l *runnerAPITestLink) Done() <-chan struct{}                   { return l.done }
func (*runnerAPITestLink) Err() error                                { return nil }

func TestRunnerWebsocketRegistersAndDetachesRunner(t *testing.T) {
	server := newRunnerTestServer(t, "")
	httpServer := httptest.NewServer(http.HandlerFunc(server.handleRunnerWebsocket))
	t.Cleanup(httpServer.Close)

	peer := dialRunnerPeer(t, "ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	var registration protocol.RegisterResult
	require.NoError(t, peer.Call(t.Context(), protocol.MethodRunnerRegister, protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host: protocol.Host{
			InstanceID: "host-one",
			Hostname:   "runner-host",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace:      protocol.Workspace{Path: "/work/project", Name: "project"},
		KodeletVersion: "test",
	}, &registration))
	assert.NotEmpty(t, registration.RunnerID)
	assert.Equal(t, protocol.Version, registration.ProtocolVersion)

	runner, ok := server.runnerRegistry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.Connected)
	assert.Equal(t, "runner-host", runner.Host.Hostname)
	assert.Equal(t, "/work/project", runner.Workspace.Path)

	require.NoError(t, peer.Notify(t.Context(), protocol.MethodRunnerHeartbeat, protocol.HeartbeatParams{
		RunnerID:       registration.RunnerID,
		Generation:     registration.Generation,
		State:          protocol.RunnerStateIdle,
		ManifestDigest: "sha256:test",
	}))
	require.Eventually(t, func() bool {
		runner, ok := server.runnerRegistry.Runner(registration.RunnerID)
		return ok && runner.ManifestDigest == "sha256:test"
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, peer.Close())
	require.Eventually(t, func() bool {
		runner, ok := server.runnerRegistry.Runner(registration.RunnerID)
		return ok && !runner.Connected && runner.Status == runnerregistry.RunnerStatusOffline
	}, time.Second, 10*time.Millisecond)
}

func TestRunnerWebsocketRequiresSubprotocol(t *testing.T) {
	server := newRunnerTestServer(t, "")
	httpServer := httptest.NewServer(http.HandlerFunc(server.handleRunnerWebsocket))
	t.Cleanup(httpServer.Close)

	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestRunnerWebsocketUsesServerAuthentication(t *testing.T) {
	server := newRunnerTestServer(t, "secret-token")
	server.router = mux.NewRouter()
	server.setupRoutes()
	httpServer := httptest.NewServer(server.router)
	t.Cleanup(httpServer.Close)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + protocol.Endpoint

	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	_, response, err := dialer.Dial(wsURL, nil)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())

	headers := http.Header{"Authorization": []string{"Bearer secret-token-runner"}}
	peer := dialRunnerPeer(t, wsURL, headers)
	require.NoError(t, peer.Close())

	webHeaders := http.Header{"Authorization": []string{"Bearer secret-token"}}
	_, response, err = dialer.Dial(wsURL, webHeaders)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestRunnerRESTEndpointsExposeRegisteredStatus(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		DisplayName:      "project-runner",
		Host: protocol.Host{
			InstanceID: "host-one",
			Hostname:   "worker-one",
			OS:         "linux",
			Arch:       "amd64",
			PID:        1234,
		},
		Workspace: protocol.Workspace{Path: "/work/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:       registration.RunnerID,
		Generation:     registration.Generation,
		State:          protocol.RunnerStateIdle,
		ManifestDigest: "sha256:manifest",
	}))

	listRecorder := httptest.NewRecorder()
	server.handleListRunners(listRecorder, httptest.NewRequest(http.MethodGet, "/api/runners", nil))
	require.Equal(t, http.StatusOK, listRecorder.Code)
	var list runnerListResponse
	require.NoError(t, json.Unmarshal(listRecorder.Body.Bytes(), &list))
	require.Len(t, list.Runners, 1)
	assert.Equal(t, registration.RunnerID, list.Runners[0].ID)
	assert.Equal(t, runnerregistry.RunnerStatusIdle, list.Runners[0].Status)
	assert.Equal(t, "worker-one", list.Runners[0].Host.Hostname)
	assert.Equal(t, 1234, list.Runners[0].Host.PID)

	getRecorder := httptest.NewRecorder()
	getRequest := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleGetRunner(getRecorder, getRequest)
	require.Equal(t, http.StatusOK, getRecorder.Code)
	var runner runnerregistry.Runner
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &runner))
	assert.Equal(t, "sha256:manifest", runner.ManifestDigest)
	assert.True(t, runner.Connected)

	missingRecorder := httptest.NewRecorder()
	missingRequest := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/runners/missing", nil), map[string]string{"id": "missing"})
	server.handleGetRunner(missingRecorder, missingRequest)
	assert.Equal(t, http.StatusNotFound, missingRecorder.Code)
}

func TestConversationResponseIncludesDurableRunnerEnvironmentProfile(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host: protocol.Host{
			InstanceID: "host-one",
			Hostname:   "worker-one",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace: protocol.Workspace{Path: "/work/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-profile", registration.RunnerID, "gpu"))
	server.conversationService = &mockConversationService{
		getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{
				ID:          "conversation-profile",
				Provider:    "openai",
				RawMessages: json.RawMessage(`[]`),
			}, nil
		},
	}

	request := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/conversations/conversation-profile", nil), map[string]string{"id": "conversation-profile"})
	recorder := httptest.NewRecorder()
	server.handleGetConversation(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response WebConversationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, registration.RunnerID, response.RunnerID)
	assert.Equal(t, "gpu", response.EnvironmentProfile)
}

func TestDeleteRunnerRequiresOfflineAndClearsAffinity(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-delete", Hostname: "worker-delete", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/delete", Name: "delete"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-delete", registration.RunnerID))

	connectedRecorder := httptest.NewRecorder()
	connectedRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleDeleteRunner(connectedRecorder, connectedRequest)
	assert.Equal(t, http.StatusConflict, connectedRecorder.Code)

	server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)
	removeRecorder := httptest.NewRecorder()
	removeRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleDeleteRunner(removeRecorder, removeRequest)
	require.Equal(t, http.StatusOK, removeRecorder.Code)
	var result runnerregistry.RemovalResult
	require.NoError(t, json.Unmarshal(removeRecorder.Body.Bytes(), &result))
	assert.Equal(t, registration.RunnerID, result.RunnerID)
	assert.Equal(t, 1, result.RemovedConversationAffinities)

	missingRecorder := httptest.NewRecorder()
	missingRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleDeleteRunner(missingRecorder, missingRequest)
	assert.Equal(t, http.StatusNotFound, missingRecorder.Code)

	invalidForceRecorder := httptest.NewRecorder()
	invalidForceRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/missing?force=perhaps", nil), map[string]string{"id": "missing"})
	server.handleDeleteRunner(invalidForceRecorder, invalidForceRequest)
	assert.Equal(t, http.StatusBadRequest, invalidForceRecorder.Code)
}

type runnerUIEventSink struct {
	events chan ChatEvent
}

func (s *runnerUIEventSink) Send(event ChatEvent) error {
	s.events <- event
	return nil
}

func TestHandleRunnerUIRequestRoutesInteractivePrompts(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, sink, broker := openRunnerUIRun(t, server)

	tests := []struct {
		name      string
		method    string
		params    any
		eventKind string
		requestID string
		response  extensions.UIInputResponse
	}{
		{
			name:      "input",
			method:    protocol.MethodUIInput,
			params:    runnerpayload.UIInputParams{RunID: "run-ui", Request: extensions.UIInputRequest{ID: "input-1", Title: "Input"}},
			eventKind: "ui-input-request",
			requestID: "input-1",
			response:  extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "answer"},
		},
		{
			name:      "confirm",
			method:    protocol.MethodUIConfirm,
			params:    runnerpayload.UIConfirmParams{RunID: "run-ui", Request: extensions.UIConfirmRequest{ID: "confirm-1", Title: "Confirm"}},
			eventKind: "ui-confirm-request",
			requestID: "confirm-1",
			response:  extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true},
		},
		{
			name:      "select",
			method:    protocol.MethodUISelect,
			params:    runnerpayload.UISelectParams{RunID: "run-ui", Request: extensions.UISelectRequest{ID: "select-1", Title: "Select", Options: []string{"one"}}},
			eventKind: "ui-select-request",
			requestID: "select-1",
			response:  extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "one"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type result struct {
				value  any
				rpcErr *protocol.RPCError
			}
			resultCh := make(chan result, 1)
			go func() {
				value, rpcErr := server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, test.method, mustRunnerJSON(t, test.params))
				resultCh <- result{value: value, rpcErr: rpcErr}
			}()

			select {
			case event := <-sink.events:
				assert.Equal(t, test.eventKind, event.Kind)
			case <-time.After(time.Second):
				t.Fatal("runner UI event was not emitted")
			}
			require.True(t, broker.Respond(test.requestID, test.response))
			select {
			case result := <-resultCh:
				require.Nil(t, result.rpcErr)
				response, ok := result.value.(extensions.UIInputResponse)
				require.True(t, ok)
				assert.Equal(t, test.response, response)
			case <-time.After(time.Second):
				t.Fatal("runner UI request did not resolve")
			}
		})
	}

	notify, rpcErr := server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, protocol.MethodUINotify, mustRunnerJSON(t, runnerpayload.UINotifyParams{
		RunID: "run-ui", Request: extensions.UINotifyRequest{Title: "Notice", Message: "done"},
	}))
	require.Nil(t, rpcErr)
	assert.Equal(t, extensions.UIInputStatusSubmitted, notify.(extensions.UIInputResponse).Status)
	select {
	case event := <-sink.events:
		assert.Equal(t, "ui-notification", event.Kind)
		assert.Equal(t, "done", event.UINotify.Message)
	case <-time.After(time.Second):
		t.Fatal("runner UI notification was not emitted")
	}
}

func TestHandleRunnerUIRequestValidatesRunAndPersistentCapabilities(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, _, _ := openRunnerUIRun(t, server)

	server.activeChatsMu.Lock()
	delete(server.activeChats, "conversation-ui")
	server.activeChatsMu.Unlock()
	value, rpcErr := server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{
		RunID: "run-ui", Request: extensions.UIInputRequest{ID: "input-1", Title: "Input"},
	}))
	require.Nil(t, rpcErr)
	response := value.(extensions.UIInputResponse)
	assert.Equal(t, extensions.UIInputStatusUnavailable, response.Status)

	value, rpcErr = server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, protocol.MethodUITranscriptAppend, mustRunnerJSON(t, runnerpayload.UITranscriptAppendParams{RunID: "run-ui"}))
	require.Nil(t, rpcErr)
	assert.Contains(t, value.(extensions.UITranscriptAppendResponse).Reason, "not available")
	for _, method := range []string{
		protocol.MethodUIWidgetSet,
		protocol.MethodUIWidgetFrame,
		protocol.MethodUIWidgetRemove,
		protocol.MethodUISurfaceOpen,
		protocol.MethodUISurfaceFrame,
		protocol.MethodUISurfaceClose,
	} {
		value, rpcErr = server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, method, json.RawMessage(`{"runId":"run-ui"}`))
		require.Nil(t, rpcErr)
		assert.Contains(t, value.(extensions.UIFrameResponse).Reason, "not available")
	}

	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, protocol.MethodUIInput, json.RawMessage(`not-json`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), "another-runner", protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{RunID: "run-ui"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, "ui.unknown", json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeMethodNotFound, rpcErr.Code)

	require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), "run-ui", runnerregistry.RunStatusSucceeded, nil))
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), registration.RunnerID, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{RunID: "run-ui"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)

	nilServer := &Server{}
	_, rpcErr = nilServer.HandleRunnerUIRequest(t.Context(), registration.RunnerID, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{RunID: "run-ui"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
	assert.Nil(t, nilServer.runnerUIBroker("run-ui"))
}

func TestRunnerRESTEndpointsRequireRegistry(t *testing.T) {
	server := &Server{}
	listRecorder := httptest.NewRecorder()
	server.handleListRunners(listRecorder, httptest.NewRequest(http.MethodGet, "/api/runners", nil))
	assert.Equal(t, http.StatusServiceUnavailable, listRecorder.Code)

	getRecorder := httptest.NewRecorder()
	getRequest := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/runners/runner-1", nil), map[string]string{"id": "runner-1"})
	server.handleGetRunner(getRecorder, getRequest)
	assert.Equal(t, http.StatusServiceUnavailable, getRecorder.Code)

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/runner-1", nil), map[string]string{"id": "runner-1"})
	server.handleDeleteRunner(deleteRecorder, deleteRequest)
	assert.Equal(t, http.StatusServiceUnavailable, deleteRecorder.Code)
}

func TestCommitRunnerAffinityOnlyReleasesPendingBindingWhenConversationIsMissing(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-affinity", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/affinity", Name: "affinity"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)

	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-transient", registration.RunnerID))
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return nil, errors.New("database temporarily unavailable")
	}}
	err = server.commitRunnerAffinity(t.Context(), "conversation-transient")
	require.ErrorContains(t, err, "temporarily unavailable")
	runnerID, found := server.runnerRegistry.RunnerForConversation("conversation-transient")
	assert.True(t, found)
	assert.Equal(t, registration.RunnerID, runnerID)

	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-missing", registration.RunnerID))
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return nil, convtypes.ErrConversationNotFound
	}}
	err = server.commitRunnerAffinity(t.Context(), "conversation-missing")
	require.ErrorIs(t, err, convtypes.ErrConversationNotFound)
	_, found = server.runnerRegistry.RunnerForConversation("conversation-missing")
	assert.False(t, found)
}

func TestWebUIChatRunnerResolvesAffinityBeforeChatValidation(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-chat", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/chat", Name: "chat"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-chat", registration.RunnerID, "gpu"))

	defaultRunner := NewDefaultChatRunner("")
	runner := &webUIChatRunner{runner: defaultRunner, server: server}
	defaultRunner.SetEnvironmentResolver(runner)
	active := newActiveChatRun(func() {})
	active.uiInput = newWebUIInputBroker("conversation-chat", &recordingChatSink{})
	server.activeChats["conversation-chat"] = active

	conversationID, err := runner.Run(t.Context(), ChatRequest{
		ConversationID: "conversation-chat",
		Message:        " ",
		ClientCapabilities: &chat.ChatClientCapabilities{
			InteractiveUI: true,
		},
	}, &recordingChatSink{})
	require.ErrorContains(t, err, "message cannot be empty")
	assert.Equal(t, "conversation-chat", conversationID)
	affinity, found, err := server.runnerRegistry.ResolveConversationAffinity(t.Context(), conversationID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, registration.RunnerID, affinity.RunnerID)
	assert.Equal(t, "gpu", affinity.EnvironmentProfile)

	var nilRunner *webUIChatRunner
	conversationID, err = nilRunner.Run(t.Context(), ChatRequest{ConversationID: "local-conversation", Message: " "}, &recordingChatSink{})
	require.ErrorContains(t, err, "message cannot be empty")
	assert.Equal(t, "local-conversation", conversationID)

	assert.True(t, chatSupportsInteractiveUI(ChatRequest{ClientCapabilities: &chat.ChatClientCapabilities{InteractiveUI: true}}))
	assert.False(t, chatSupportsInteractiveUI(ChatRequest{}))
	require.NoError(t, runner.CloseConversation("conversation-chat"))
	require.NoError(t, runner.Close())
	require.NoError(t, runner.Close())
	require.NoError(t, (*webUIChatRunner)(nil).CloseConversation("conversation-chat"))
}

func TestWebUIChatRunnerResolveEnvironmentValidatesRunnerState(t *testing.T) {
	runner := &webUIChatRunner{}
	_, err := runner.ResolveEnvironment(t.Context(), ChatRequest{}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner id is required")
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: "runner"}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner registry is unavailable")

	server := newRunnerTestServer(t, "")
	runner.server = server
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: "missing"}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner not found")

	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-environment", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/environment", Name: "environment"},
	}, link)
	require.NoError(t, err)
	environment, err := runner.ResolveEnvironment(t.Context(), ChatRequest{
		RunnerID: registration.RunnerID,
		ClientCapabilities: &chat.ChatClientCapabilities{
			InteractiveUI:      true,
			PersistentSurfaces: true,
		},
	}, "conversation", llmtypes.Config{}, "")
	require.NoError(t, err)
	require.NotNil(t, environment)

	server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: registration.RunnerID}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner is offline")

	_, err = server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version + 1},
		Host:             protocol.Host{InstanceID: "host-incompatible", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/incompatible", Name: "incompatible"},
	}, newRunnerAPITestLink())
	require.ErrorContains(t, err, "does not support protocol version")
	var incompatibleID string
	for _, registered := range server.runnerRegistry.Runners() {
		if registered.Status == runnerregistry.RunnerStatusIncompatible {
			incompatibleID = registered.ID
			break
		}
	}
	require.NotEmpty(t, incompatibleID)
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: incompatibleID}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "does not support protocol version")
}

func openRunnerUIRun(t *testing.T, server *Server) (protocol.RegisterResult, *runnerUIEventSink, *webUIInputBroker) {
	t.Helper()
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-ui", Hostname: "ui-host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/ui", Name: "ui"},
	}, link)
	require.NoError(t, err)
	link.call = func(_ context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodRunOpen:
			request := params.(protocol.RunOpenParams)
			manifest := runnerpayload.Manifest{
				ProtocolVersion:  protocol.Version,
				RunnerID:         registration.RunnerID,
				RunID:            request.RunID,
				Generation:       registration.Generation,
				WorkingDirectory: "/work/ui",
			}
			digest, digestErr := runnerpayload.ComputeManifestDigest(manifest)
			if digestErr != nil {
				return digestErr
			}
			manifest.Digest = digest
			*result.(*runnerpayload.Manifest) = manifest
		}
		return nil
	}
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
	}))
	_, err = server.runnerRegistry.OpenRun(t.Context(), registration.RunnerID, protocol.RunOpenParams{
		RunID: "run-ui", ConversationID: "conversation-ui",
	})
	require.NoError(t, err)
	sink := &runnerUIEventSink{events: make(chan ChatEvent, 8)}
	broker := newWebUIInputBroker("conversation-ui", sink)
	active := newActiveChatRun(func() {})
	active.uiInput = broker
	server.activeChats["conversation-ui"] = active
	return registration, sink, broker
}

func mustRunnerJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

func newRunnerTestServer(t *testing.T, authToken string) *Server {
	t.Helper()
	runCtx, runCancel := context.WithCancel(t.Context())
	registry, err := runnerregistry.New(runCtx, runnerregistry.Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		runCancel()
		_ = registry.Close()
	})
	config := &ServerConfig{}
	if authToken != "" {
		config.AuthToken = authToken
		config.RunnerAuthToken = authToken + "-runner"
	}
	server := &Server{
		config:          config,
		runCtx:          runCtx,
		runCancel:       runCancel,
		runnerRegistry:  registry,
		activeChats:     make(map[string]*activeChatRun),
		chatSubscribers: make(map[string]map[*subscriberEventSink]struct{}),
	}
	registry.SetEnvironmentErrorHandler(func(conversationID string) {
		server.cancelActiveChat(conversationID)
	})
	return server
}

func dialRunnerPeer(t *testing.T, url string, headers http.Header) *protocol.Peer {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	conn, response, err := dialer.DialContext(t.Context(), url, headers)
	if response != nil && response.Body != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	require.NoError(t, err)
	require.Equal(t, protocol.Subprotocol, conn.Subprotocol())
	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{RequestPrefix: "runner"})
	require.NoError(t, err)
	require.NoError(t, peer.Start(t.Context()))
	t.Cleanup(func() { _ = peer.Close() })
	return peer
}
