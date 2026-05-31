package extensions

import (
	"context"
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

func TestProcessRPCClientNilWhenClosedDisabledOrShutdown(t *testing.T) {
	assert.Nil(t, (&Process{closed: true}).rpcClient())
	assert.Nil(t, (&Process{disabled: true}).rpcClient())
	assert.Nil(t, (&Process{shutdown: true}).rpcClient())
	client := newRPCClient(stringsNewReader(""), ioDiscard{})
	assert.Same(t, client, (&Process{client: client}).rpcClient())
}

func TestProcessContextIgnoresCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	processCtx := processContext(ctx)

	assert.NoError(t, processCtx.Err())
}

type ioDiscard struct{}

func (ioDiscard) Write(payload []byte) (int, error) { return len(payload), nil }

func stringsNewReader(value string) *strings.Reader { return strings.NewReader(value) }
