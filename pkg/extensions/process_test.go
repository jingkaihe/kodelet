package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	conversationmeta "github.com/jingkaihe/kodelet/pkg/conversations"
	kodelettools "github.com/jingkaihe/kodelet/pkg/tools"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachedProcessInitializeStreamingAndReverseRPC(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	local, sdk := net.Pipe()
	t.Cleanup(func() { _ = sdk.Close() })
	require.NoError(t, sdk.SetDeadline(time.Now().Add(5*time.Second)))
	process, err := AttachProcess(t.Context(), Extension{ID: "session:inline-1", Kind: SourceKindSession}, DefaultConfig(), t.TempDir(), local)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, process.Close()) })
	reader := bufio.NewReader(sdk)
	read := func() rpcIncomingMessage {
		message, err := readIncomingMessage(reader)
		require.NoError(t, err)
		return message
	}
	send := func(value any) {
		payload, err := json.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, writeFrame(sdk, payload))
	}
	ctx := ContextWithUIInputBroker(t.Context(), staticUIInputBroker{value: "runner-owned"})
	ctx = ContextWithRunnerID(ctx, "worker-one")
	ctx = ContextWithRuntimeCapabilities(ctx, RuntimeCapabilities{BackgroundTasks: true, RemoteProfiles: true})
	initialized := make(chan error, 1)
	go func() {
		_, err := process.Initialize(ctx, "/workspace")
		initialized <- err
	}()
	init := read()
	assert.Equal(t, "extension.initialize", init.Method)
	var params initializeParams
	require.NoError(t, json.Unmarshal(init.Params, &params))
	assert.Equal(t, "session:inline-1", params.Extension.ID)
	assert.Equal(t, "worker-one", params.Extension.RunnerID)
	assert.False(t, params.Capabilities["runtime"].(map[string]any)["backgroundTasks"].(bool))
	assert.True(t, params.Capabilities["profiles"].(map[string]any)["remote"].(bool))
	send(map[string]any{"jsonrpc": "2.0", "id": 100, "parentId": init.ID, "method": "kodelet.ui.input", "params": map[string]any{"title": "initialize"}})
	assert.Contains(t, string(read().Result), "runner-owned")
	send(map[string]any{"jsonrpc": "2.0", "id": init.ID, "result": InitializeResult{Name: "inline"}})
	require.NoError(t, <-initialized)

	updates := make(chan ToolExecutionResult, 1)
	completed := make(chan error, 1)
	store := &forkableMetadataStore{conversationID: "forked"}
	ctx = kodelettools.ContextWithToolContext(ctx, kodelettools.ToolContext{MetadataStore: store})
	go func() {
		result, err := process.ExecuteToolStreaming(ctx, "inline_tool", json.RawMessage(`{}`), ExtensionCallContext{}, func(update ToolExecutionResult) { updates <- update })
		if err == nil {
			assert.Equal(t, "finished", result.Content)
		}
		completed <- err
	}()
	tool := read()
	assert.Equal(t, "extension.tool.execute", tool.Method)
	for _, method := range []string{"kodelet.tool.update", ConversationForkMethod, BackgroundTaskAcquireMethod} {
		send(map[string]any{"jsonrpc": "2.0", "id": 101, "parentId": tool.ID, "method": method, "params": map[string]any{"content": "working"}})
		response := read()
		switch method {
		case "kodelet.tool.update":
			assert.Nil(t, response.Error)
			assert.Equal(t, "working", (<-updates).Content)
		case ConversationForkMethod:
			assert.Contains(t, string(response.Result), "forked")
		case BackgroundTaskAcquireMethod:
			require.NotNil(t, response.Error)
			assert.Contains(t, response.Error.Message, "not available")
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 102, "parentId": 999999, "method": "kodelet.tool.update", "params": map[string]any{}})
	require.NotNil(t, read().Error, "stale parents must not acquire another tool's update sink")
	send(map[string]any{"jsonrpc": "2.0", "id": tool.ID, "result": ToolExecutionResult{Content: "finished"}})
	require.NoError(t, <-completed)
	assert.Nil(t, process.cmd)
	assert.True(t, RuntimeCapabilitiesFromContext(ctx).BackgroundTasks, "attached capability narrowing must not mutate the host context")
}

func TestAttachedProcessCancellationAndDisconnectDoNotReplay(t *testing.T) {
	local, sdk := net.Pipe()
	t.Cleanup(func() { _ = sdk.Close() })
	require.NoError(t, sdk.SetDeadline(time.Now().Add(5*time.Second)))
	process, err := AttachProcess(t.Context(), Extension{ID: "session:inline-1"}, DefaultConfig(), t.TempDir(), local)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, process.Close()) })
	reader := bufio.NewReader(sdk)
	ctx, cancel := context.WithCancel(t.Context())
	completed := make(chan error, 1)
	go func() {
		_, err := process.ExecuteTool(ctx, "inline_tool", json.RawMessage(`{}`), ExtensionCallContext{})
		completed <- err
	}()
	request, err := readIncomingMessage(reader)
	require.NoError(t, err)
	cancel()
	notification, err := readIncomingMessage(reader)
	require.NoError(t, err)
	assert.Equal(t, "$/cancelRequest", notification.Method)
	assert.JSONEq(t, `{"id":`+string(request.ID)+`}`, string(notification.Params))
	require.ErrorIs(t, <-completed, context.Canceled)
	go func() {
		_, err := process.ExecuteTool(t.Context(), "inline_tool", json.RawMessage(`{}`), ExtensionCallContext{})
		completed <- err
	}()
	_, err = readIncomingMessage(reader)
	require.NoError(t, err)
	require.NoError(t, sdk.Close())
	select {
	case err := <-completed:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("disconnect did not fail pending execution")
	}
	_, err = process.ExecuteTool(t.Context(), "inline_tool", json.RawMessage(`{}`), ExtensionCallContext{})
	require.ErrorContains(t, err, "reattachment")
	assert.Nil(t, process.cmd)
}

func TestAttachedProcessDisconnectCancelsParentBoundReverseRequest(t *testing.T) {
	local, sdk := net.Pipe()
	t.Cleanup(func() { _ = sdk.Close() })
	require.NoError(t, sdk.SetDeadline(time.Now().Add(5*time.Second)))
	process, err := AttachProcess(t.Context(), Extension{ID: "session:inline-1"}, DefaultConfig(), t.TempDir(), local)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, process.Close()) })
	broker := &cancelAwareUIInputBroker{started: make(chan struct{}), canceled: make(chan struct{})}
	ctx := ContextWithUIInputBroker(t.Context(), broker)
	completed := make(chan error, 1)
	go func() {
		_, err := process.ExecuteTool(ctx, "inline_tool", json.RawMessage(`{}`), ExtensionCallContext{})
		completed <- err
	}()
	request, err := readIncomingMessage(bufio.NewReader(sdk))
	require.NoError(t, err)
	require.NoError(t, writeFrame(sdk, []byte(`{"jsonrpc":"2.0","id":100,"parentId":`+string(request.ID)+`,"method":"kodelet.ui.input","params":{"title":"waiting"}}`)))
	select {
	case <-broker.started:
	case <-time.After(time.Second):
		t.Fatal("reverse request did not start")
	}
	require.NoError(t, sdk.Close())
	select {
	case <-broker.canceled:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel the parent-bound reverse request")
	}
	select {
	case err := <-completed:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("generation cleanup deadlocked waiting for reverse request")
	}
}

func TestExtensionDataDirUsesKodeletBasePathAndSanitizedID(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	dataDir, err := extensionDataDir("org@repo/weather")

	require.NoError(t, err)
	assert.DirExists(t, dataDir)
	assert.Contains(t, dataDir, "org@repo_weather")
}

func TestRuntimeCapabilitiesDefaultAndOverride(t *testing.T) {
	assert.True(t, RuntimeCapabilitiesFromContext(context.Background()).BackgroundTasks)
	assert.False(t, RuntimeCapabilitiesFromContext(context.Background()).RemoteProfiles)

	ctx := ContextWithRuntimeCapabilities(context.Background(), RuntimeCapabilities{BackgroundTasks: false, RemoteProfiles: true})
	assert.False(t, RuntimeCapabilitiesFromContext(ctx).BackgroundTasks)
	assert.True(t, RuntimeCapabilitiesFromContext(ctx).RemoteProfiles)
}

type capabilityRecordingExtensionUIHost struct {
	*recordingExtensionUIHost
	capabilities ExtensionUIHostCapabilities
}

type recordingBackgroundTaskHost struct {
	acquired []BackgroundTaskAcquireRequest
	released []BackgroundTaskReleaseRequest
	owners   []UIExtensionOwner
	cleanups []UIExtensionOwner
}

func (h *recordingBackgroundTaskHost) AcquireBackgroundTask(_ context.Context, source UIExtensionSource, request BackgroundTaskAcquireRequest) (BackgroundTaskAcquireResponse, error) {
	h.acquired = append(h.acquired, request)
	h.owners = append(h.owners, source.ExtensionUIOwner())
	return BackgroundTaskAcquireResponse{LeaseID: "lease-1"}, nil
}

func (h *recordingBackgroundTaskHost) ReleaseBackgroundTask(_ context.Context, source UIExtensionSource, request BackgroundTaskReleaseRequest) (BackgroundTaskReleaseResponse, error) {
	h.released = append(h.released, request)
	h.owners = append(h.owners, source.ExtensionUIOwner())
	return BackgroundTaskReleaseResponse{Released: true}, nil
}

func (h *recordingBackgroundTaskHost) CleanupBackgroundTasks(owner UIExtensionOwner) {
	h.cleanups = append(h.cleanups, owner)
}

func (h *capabilityRecordingExtensionUIHost) ExtensionUIHostCapabilities(context.Context) ExtensionUIHostCapabilities {
	return h.capabilities
}

func TestProcessInitializeAdvertisesRuntimeBackgroundTasks(t *testing.T) {
	for _, test := range []struct {
		name           string
		ctx            context.Context
		expected       bool
		remoteProfiles bool
		uiCapabilities *ExtensionUIHostCapabilities
	}{
		{name: "local default", ctx: context.Background(), expected: true},
		{name: "explicitly unavailable", ctx: ContextWithRuntimeCapabilities(context.Background(), RuntimeCapabilities{BackgroundTasks: false}), expected: false},
		{name: "widget-only UI host", ctx: context.Background(), expected: true, uiCapabilities: &ExtensionUIHostCapabilities{Widgets: true}},
		{name: "runner host", ctx: ContextWithBackgroundTaskHost(ContextWithRuntimeCapabilities(context.Background(), RuntimeCapabilities{BackgroundTasks: true}), &recordingBackgroundTaskHost{}), expected: true},
		{name: "profile-capable runner", ctx: ContextWithRuntimeCapabilities(context.Background(), RuntimeCapabilities{BackgroundTasks: true, RemoteProfiles: true}), expected: true, remoteProfiles: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			clientReader, serverWriter := io.Pipe()
			serverReader, clientWriter := io.Pipe()
			t.Cleanup(func() {
				_ = clientReader.Close()
				_ = serverWriter.Close()
				_ = serverReader.Close()
				_ = clientWriter.Close()
			})

			client := newRPCClient(clientReader, clientWriter)
			process := &Process{Extension: Extension{ID: "runtime-capabilities"}, client: client}
			source := &processExtensionUISource{process: process, client: client}
			process.uiSource = source
			client.setHostRequestHandler(source)

			type initializeCall struct {
				result *InitializeResult
				err    error
			}
			initializeDone := make(chan initializeCall, 1)
			initializeCtx := test.ctx
			if test.uiCapabilities != nil {
				initializeCtx = ContextWithExtensionUIHost(initializeCtx, &capabilityRecordingExtensionUIHost{
					recordingExtensionUIHost: &recordingExtensionUIHost{},
					capabilities:             *test.uiCapabilities,
				})
			}
			go func() {
				result, err := process.Initialize(initializeCtx, "/workspace")
				initializeDone <- initializeCall{result: result, err: err}
			}()

			payload, err := readFrame(bufio.NewReader(serverReader))
			require.NoError(t, err)
			var request struct {
				ID     int64            `json:"id"`
				Method string           `json:"method"`
				Params initializeParams `json:"params"`
			}
			require.NoError(t, json.Unmarshal(payload, &request))
			assert.Equal(t, "extension.initialize", request.Method)
			runtimeCapabilities, ok := request.Params.Capabilities["runtime"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, test.expected, runtimeCapabilities["backgroundTasks"])
			profileCapabilities, ok := request.Params.Capabilities["profiles"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, test.remoteProfiles, profileCapabilities["remote"])
			uiCapabilities, ok := request.Params.Capabilities["ui"].(map[string]any)
			require.True(t, ok)
			if test.uiCapabilities == nil {
				assert.False(t, uiCapabilities["widgets"].(bool))
				assert.False(t, uiCapabilities["surfaces"].(bool))
			} else {
				assert.Equal(t, test.uiCapabilities.Widgets, uiCapabilities["widgets"])
				assert.Equal(t, test.uiCapabilities.Surfaces, uiCapabilities["surfaces"])
				assert.Equal(t, test.uiCapabilities.Transcript, uiCapabilities["transcript"])
			}

			resultPayload, err := json.Marshal(InitializeResult{Name: "runtime-capabilities"})
			require.NoError(t, err)
			responsePayload, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: resultPayload})
			require.NoError(t, err)
			require.NoError(t, writeFrame(serverWriter, responsePayload))

			select {
			case call := <-initializeDone:
				require.NoError(t, call.err)
				require.NotNil(t, call.result)
			case <-time.After(time.Second):
				t.Fatal("extension initialization did not complete")
			}
		})
	}
}

func TestToolExecutionHostHandlerForksLiveConversation(t *testing.T) {
	store := &forkableMetadataStore{conversationID: "forked-conversation"}
	ctx := kodelettools.ContextWithToolContext(context.Background(), kodelettools.ToolContext{MetadataStore: store})

	result, rpcErr := (toolExecutionHostHandler{
		extensionID: "subagent",
		toolName:    "subagent",
	}).HandleRPCRequest(ctx, ConversationForkMethod, json.RawMessage(`{"name":"  Investigate\n fork naming  "}`))

	require.Nil(t, rpcErr)
	assert.Equal(t, conversationForkResult{ConversationID: "forked-conversation"}, result)
	assert.Equal(t, 1, store.calls)
	require.True(t, store.hasInitiator)
	assert.Equal(t, conversationtypes.ConversationForkInitiator{
		Type:        conversationtypes.ConversationForkInitiatorTypeExtensionTool,
		ExtensionID: "subagent",
		ToolName:    "subagent",
	}, store.initiator)
	assert.Equal(t, "Investigate fork naming", store.name)
}

func TestToolExecutionHostHandlerRejectsUnavailableConversationFork(t *testing.T) {
	t.Run("invalid params", func(t *testing.T) {
		store := &forkableMetadataStore{conversationID: "forked-conversation"}
		ctx := kodelettools.ContextWithToolContext(context.Background(), kodelettools.ToolContext{MetadataStore: store})

		result, rpcErr := (toolExecutionHostHandler{}).HandleRPCRequest(ctx, ConversationForkMethod, json.RawMessage(`{"name":42}`))

		assert.Nil(t, result)
		require.NotNil(t, rpcErr)
		assert.Equal(t, -32602, rpcErr.Code)
		assert.Zero(t, store.calls)
	})

	t.Run("missing live thread", func(t *testing.T) {
		result, rpcErr := (toolExecutionHostHandler{}).HandleRPCRequest(context.Background(), ConversationForkMethod, nil)

		assert.Nil(t, result)
		require.NotNil(t, rpcErr)
		assert.Equal(t, conversationForkUnavailableCode, rpcErr.Code)
		assert.Contains(t, rpcErr.Message, "unavailable")
	})

	t.Run("persistence disabled", func(t *testing.T) {
		store := &forkableMetadataStore{err: llmtypes.ErrConversationForkUnavailable}
		ctx := kodelettools.ContextWithToolContext(context.Background(), kodelettools.ToolContext{MetadataStore: store})

		result, rpcErr := (toolExecutionHostHandler{}).HandleRPCRequest(ctx, ConversationForkMethod, nil)

		assert.Nil(t, result)
		require.NotNil(t, rpcErr)
		assert.Equal(t, conversationForkUnavailableCode, rpcErr.Code)
	})

	t.Run("save failure", func(t *testing.T) {
		store := &forkableMetadataStore{err: errors.New("disk full")}
		ctx := kodelettools.ContextWithToolContext(context.Background(), kodelettools.ToolContext{MetadataStore: store})

		result, rpcErr := (toolExecutionHostHandler{}).HandleRPCRequest(ctx, ConversationForkMethod, nil)

		assert.Nil(t, result)
		require.NotNil(t, rpcErr)
		assert.Equal(t, -32000, rpcErr.Code)
		assert.Equal(t, "disk full", rpcErr.Message)
	})
}

func TestProcessEnsureRunningDisabledAndShutdownBranches(t *testing.T) {
	t.Run("disabled after repeated failures", func(t *testing.T) {
		process := &Process{Extension: Extension{ID: "weather"}}
		process.recordFailureLocked()
		process.recordFailureLocked()
		process.recordFailureLocked()

		err := process.ensureRunning(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disabled after repeated failures")
	})

	t.Run("shutdown", func(t *testing.T) {
		process := &Process{Extension: Extension{ID: "weather"}, shutdown: true}

		err := process.ensureRunning(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "is shut down")
	})

	t.Run("already running", func(t *testing.T) {
		process := &Process{Extension: Extension{ID: "weather"}, closed: false}

		assert.NoError(t, process.ensureRunning(context.Background()))
	})

	t.Run("closed with canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		process := &Process{Extension: Extension{ID: "weather"}, closed: true}

		err := process.ensureRunning(ctx)

		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestProcessFailClientGenerationCountsOnce(t *testing.T) {
	client := newRPCClient(strings.NewReader(""), ioDiscard{})
	process := &Process{Extension: Extension{ID: "weather"}, client: client}

	process.failClientGeneration(client)
	process.failClientGeneration(client)
	process.failClientGeneration(client)

	assert.True(t, process.closed)
	assert.Equal(t, 1, process.failures)
	assert.False(t, process.disabled)
}

func TestProcessFailClientGenerationIgnoresStaleClient(t *testing.T) {
	staleClient := newRPCClient(strings.NewReader(""), ioDiscard{})
	currentClient := newRPCClient(strings.NewReader(""), ioDiscard{})
	process := &Process{Extension: Extension{ID: "weather"}, client: currentClient}

	process.failClientGeneration(staleClient)

	assert.False(t, process.closed)
	assert.Zero(t, process.failures)
	assert.Same(t, currentClient, process.client)
}

type completedCallContext struct {
	context.Context
	done <-chan struct{}
	err  error
}

func (c completedCallContext) Done() <-chan struct{} { return c.done }
func (c completedCallContext) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

func TestProcessContextCompletionDoesNotTerminateConcurrentCall(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "cancellation", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProcessContextCompletionDoesNotTerminateConcurrentCall(t, test.err)
		})
	}
}

func testProcessContextCompletionDoesNotTerminateConcurrentCall(t *testing.T, completionErr error) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientReader.Close()
		_ = serverWriter.Close()
		_ = serverReader.Close()
		_ = clientWriter.Close()
	})

	client := newRPCClient(clientReader, clientWriter)
	host := &recordingExtensionUIHost{}
	process := &Process{Extension: Extension{ID: "shared"}, client: client, uiHost: host}
	source := &processExtensionUISource{
		process: process,
		client:  client,
		owner:   UIExtensionOwner{ExtensionID: "shared", Generation: 1},
	}
	process.uiSource = source

	type commandCallResult struct {
		name   string
		result *CommandResult
		err    error
	}
	results := make(chan commandCallResult, 2)
	firstDone := make(chan struct{})
	firstCtx := completedCallContext{Context: context.Background(), done: firstDone, err: completionErr}
	for _, call := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "first", ctx: firstCtx},
		{name: "second", ctx: context.Background()},
	} {
		go func() {
			result, err := process.ExecuteCommand(call.ctx, call.name, nil, CommandInvocation{}, ExtensionCallContext{ConversationID: call.name})
			results <- commandCallResult{name: call.name, result: result, err: err}
		}()
	}

	outbound := bufio.NewReader(serverReader)
	requestIDs := make(map[string]int64, 2)
	for range 2 {
		payload, err := readFrame(outbound)
		require.NoError(t, err)
		var request struct {
			ID     int64 `json:"id"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		require.NoError(t, json.Unmarshal(payload, &request))
		requestIDs[request.Params.Name] = request.ID
	}
	require.NotZero(t, requestIDs["first"])
	require.NotZero(t, requestIDs["second"])

	close(firstDone)
	cancelPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var cancelNotification struct {
		Method string              `json:"method"`
		Params cancelRequestParams `json:"params"`
	}
	require.NoError(t, json.Unmarshal(cancelPayload, &cancelNotification))
	assert.Equal(t, "$/cancelRequest", cancelNotification.Method)
	assert.Equal(t, requestIDs["first"], cancelNotification.Params.ID)

	firstResult := <-results
	assert.Equal(t, "first", firstResult.name)
	require.ErrorIs(t, firstResult.err, completionErr)
	assert.Nil(t, firstResult.result)
	assert.False(t, process.closed)
	assert.Same(t, client, process.client)
	assert.Zero(t, process.failures)
	assert.Empty(t, host.cleanups)

	responsePayload, err := json.Marshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      requestIDs["second"],
		Result:  json.RawMessage(`{"action":"respond","response":"still running"}`),
	})
	require.NoError(t, err)
	require.NoError(t, writeFrame(serverWriter, responsePayload))

	secondResult := <-results
	assert.Equal(t, "second", secondResult.name)
	require.NoError(t, secondResult.err)
	require.NotNil(t, secondResult.result)
	assert.Equal(t, "still running", secondResult.result.Response)
	assert.False(t, process.closed)
	assert.Same(t, client, process.client)
	assert.Empty(t, host.cleanups)
}

func TestProcessHandleRPCRequestSupportsUIConfirmSelectAndNotify(t *testing.T) {
	ctx := ContextWithUIInputBroker(context.Background(), staticUIInputBroker{value: "answer"})
	process := &Process{}

	confirmParams, err := json.Marshal(UIConfirmRequest{Title: "Allow?"})
	require.NoError(t, err)
	result, rpcErr := process.HandleRPCRequest(ctx, "kodelet.ui.confirm", confirmParams)
	require.Nil(t, rpcErr)
	confirm, ok := result.(UIInputResponse)
	require.True(t, ok)
	assert.True(t, confirm.Confirmed)

	selectParams, err := json.Marshal(UISelectRequest{Title: "Pick", Options: []string{"Pasta", "Pizza"}})
	require.NoError(t, err)
	result, rpcErr = process.HandleRPCRequest(ctx, "kodelet.ui.select", selectParams)
	require.Nil(t, rpcErr)
	selection, ok := result.(UIInputResponse)
	require.True(t, ok)
	assert.Equal(t, "Pasta", selection.Value)

	notifyParams, err := json.Marshal(UINotifyRequest{Message: "Done"})
	require.NoError(t, err)
	result, rpcErr = process.HandleRPCRequest(ctx, "kodelet.ui.notify", notifyParams)
	require.Nil(t, rpcErr)
	notification, ok := result.(UIInputResponse)
	require.True(t, ok)
	assert.Equal(t, UIInputStatusSubmitted, notification.Status)
}

func TestProcessHandleRPCRequestDelegatesBackgroundTasksToHost(t *testing.T) {
	host := &recordingBackgroundTaskHost{}
	process := &Process{}
	source := &processExtensionUISource{
		process: process,
		owner:   UIExtensionOwner{ExtensionID: "subagent", Generation: 4},
	}
	process.uiSource = source
	ctx := ContextWithBackgroundTaskHost(context.Background(), host)

	result, rpcErr := process.handleRPCRequest(ctx, source, BackgroundTaskAcquireMethod, json.RawMessage(`{"description":"child agent"}`))
	require.Nil(t, rpcErr)
	acquired, ok := result.(BackgroundTaskAcquireResponse)
	require.True(t, ok)
	assert.Equal(t, "lease-1", acquired.LeaseID)
	require.Equal(t, []BackgroundTaskAcquireRequest{{Description: "child agent"}}, host.acquired)

	result, rpcErr = process.handleRPCRequest(ctx, source, BackgroundTaskReleaseMethod, json.RawMessage(`{"leaseId":"lease-1"}`))
	require.Nil(t, rpcErr)
	released, ok := result.(BackgroundTaskReleaseResponse)
	require.True(t, ok)
	assert.True(t, released.Released)
	require.Equal(t, []BackgroundTaskReleaseRequest{{LeaseID: "lease-1"}}, host.released)
	assert.Equal(t, []UIExtensionOwner{source.owner, source.owner}, host.owners)
}

func TestProcessHandleRPCRequestReturnsNoopLeaseForPersistentRuntime(t *testing.T) {
	process := &Process{}
	ctx := ContextWithRuntimeCapabilities(context.Background(), RuntimeCapabilities{BackgroundTasks: true})

	result, rpcErr := process.handleRPCRequest(ctx, nil, BackgroundTaskAcquireMethod, nil)
	require.Nil(t, rpcErr)
	acquired, ok := result.(BackgroundTaskAcquireResponse)
	require.True(t, ok)
	assert.Empty(t, acquired.LeaseID)

	result, rpcErr = process.handleRPCRequest(ctx, nil, BackgroundTaskReleaseMethod, json.RawMessage(`{"leaseId":"ignored"}`))
	require.Nil(t, rpcErr)
	released, ok := result.(BackgroundTaskReleaseResponse)
	require.True(t, ok)
	assert.True(t, released.Released)

	_, rpcErr = process.handleRPCRequest(
		ContextWithRuntimeCapabilities(context.Background(), RuntimeCapabilities{BackgroundTasks: false}),
		nil,
		BackgroundTaskAcquireMethod,
		nil,
	)
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "not available")
}

func TestProcessCloseCleansBackgroundTasks(t *testing.T) {
	host := &recordingBackgroundTaskHost{}
	client := newRPCClient(strings.NewReader(""), io.Discard)
	process := &Process{client: client}
	source := &processExtensionUISource{
		process: process,
		client:  client,
		owner:   UIExtensionOwner{ExtensionID: "subagent", Generation: 8},
	}
	source.setHostContext(ContextWithBackgroundTaskHost(context.Background(), host))
	process.uiSource = source

	require.NoError(t, process.Close())
	assert.Equal(t, []UIExtensionOwner{source.owner}, host.cleanups)
}

func TestProcessRetainedHostContextSurvivesReinitializationButNotGenerationClose(t *testing.T) {
	source := &processExtensionUISource{}
	first, cancel := context.WithCancel(context.WithValue(t.Context(), rpcCallContextKey{}, "first-run"))
	source.setHostContext(first)
	retained := source.backgroundHostContext()
	initialUI := source.hostContext()
	cancel()
	require.NoError(t, retained.Err())
	source.setHostContext(context.WithValue(t.Context(), rpcCallContextKey{}, "new-run"))
	require.NoError(t, retained.Err(), "reattachment must not cancel in-flight background lease releases")
	assert.ErrorIs(t, initialUI.Err(), context.Canceled)
	assert.Equal(t, "first-run", retained.Value(rpcCallContextKey{}))
	assert.Equal(t, "new-run", source.hostContext().Value(rpcCallContextKey{}))
	source.cancelHostContext()
	assert.ErrorIs(t, retained.Err(), context.Canceled)
}

func TestProcessCloseCancelsAndWaitsForParentlessHostRequests(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientReader.Close()
		_ = serverWriter.Close()
	})
	broker := &cancelAwareUIInputBroker{started: make(chan struct{}), canceled: make(chan struct{})}
	client := newRPCClient(clientReader, io.Discard)
	process := &Process{closed: false, client: client}
	source := &processExtensionUISource{
		process: process,
		client:  client,
		owner:   UIExtensionOwner{ExtensionID: "test", Generation: 1},
	}
	source.setHostContext(ContextWithUIInputBroker(context.Background(), broker))
	process.uiSource = source
	client.setHostRequestHandler(source)
	go client.readLoop()

	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","id":1,"method":"kodelet.ui.input","params":{"title":"Choose"}}`)))
	select {
	case <-broker.started:
	case <-time.After(time.Second):
		t.Fatal("parentless host request did not start")
	}
	done := make(chan error, 1)
	go func() { done <- process.Close() }()
	select {
	case <-broker.canceled:
	case <-time.After(time.Second):
		t.Fatal("process close did not cancel the parentless host request")
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("process close did not wait for the parentless host request")
	}
}

type cancelAwareUIInputBroker struct {
	started  chan struct{}
	canceled chan struct{}
}

type forkableMetadataStore struct {
	conversationID string
	calls          int
	err            error
	initiator      conversationtypes.ConversationForkInitiator
	hasInitiator   bool
	name           string
}

func (*forkableMetadataStore) GetMetadata() map[string]any { return nil }

func (*forkableMetadataStore) SetMetadataValue(string, any) {}

func (s *forkableMetadataStore) ForkConversation(ctx context.Context) (string, error) {
	s.calls++
	s.initiator, s.hasInitiator = conversationtypes.ConversationForkInitiatorFromContext(ctx)
	s.name = conversationmeta.ConversationForkNameFromContext(ctx)
	return s.conversationID, s.err
}

func (b *cancelAwareUIInputBroker) Input(ctx context.Context, _ UIInputRequest) (UIInputResponse, error) {
	close(b.started)
	<-ctx.Done()
	close(b.canceled)
	return UIInputResponse{}, ctx.Err()
}

type ioDiscard struct{}

func (ioDiscard) Write(payload []byte) (int, error) { return len(payload), nil }
