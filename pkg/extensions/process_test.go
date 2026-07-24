package extensions

import (
	"context"
	"encoding/json"
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
