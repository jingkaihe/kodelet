package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionDataDirUsesKodeletBasePathAndSanitizedID(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	dataDir, err := extensionDataDir("org@repo/weather")

	require.NoError(t, err)
	assert.DirExists(t, dataDir)
	assert.Contains(t, dataDir, "org@repo_weather")
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

func TestProcessContextIgnoresCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	processCtx := processContext(ctx)

	assert.NoError(t, processCtx.Err())
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

type ioDiscard struct{}

func (ioDiscard) Write(payload []byte) (int, error) { return len(payload), nil }
