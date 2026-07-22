package extensions

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeManagerReusesRuntimeForCanonicalWorkingDirectory(t *testing.T) {
	var calls int
	manager := newRuntimeManager(func(_ context.Context, _ string) (*Runtime, error) {
		calls++
		return EmptyRuntime(), nil
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })

	cwd := t.TempDir()
	first, err := manager.Runtime(context.Background(), cwd)
	require.NoError(t, err)
	second, err := manager.Runtime(context.Background(), filepath.Join(cwd, "."))
	require.NoError(t, err)

	assert.Same(t, first, second)
	assert.Equal(t, 1, calls)
}

func TestRuntimeManagerCreatesOneRuntimeForConcurrentCallers(t *testing.T) {
	var calls int
	manager := newRuntimeManager(func(_ context.Context, _ string) (*Runtime, error) {
		calls++
		return EmptyRuntime(), nil
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })

	const callers = 8
	runtimes := make([]*Runtime, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			runtimes[index], errs[index] = manager.Runtime(context.Background(), "/workspace")
		}(i)
	}
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Same(t, runtimes[0], runtimes[i])
	}
	assert.Equal(t, 1, calls)
}

func TestRuntimeManagerDoesNotCacheCreationFailures(t *testing.T) {
	var calls int
	manager := newRuntimeManager(func(_ context.Context, _ string) (*Runtime, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("initialization failed")
		}
		return EmptyRuntime(), nil
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })

	_, err := manager.Runtime(context.Background(), "/workspace")
	require.Error(t, err)
	runtime, err := manager.Runtime(context.Background(), "/workspace")
	require.NoError(t, err)
	require.NotNil(t, runtime)
	assert.Equal(t, 2, calls)
}

func TestRuntimeManagerDoesNotCacheRuntimeCreatedByCanceledCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	manager := newRuntimeManager(func(_ context.Context, _ string) (*Runtime, error) {
		calls++
		if calls == 1 {
			cancel()
		}
		return EmptyRuntime(), nil
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })

	_, err := manager.Runtime(ctx, "/workspace")
	require.ErrorIs(t, err, context.Canceled)
	runtime, err := manager.Runtime(context.Background(), "/workspace")
	require.NoError(t, err)
	require.NotNil(t, runtime)
	assert.Equal(t, 2, calls)
}

func TestRuntimeManagerRejectsUseAfterClose(t *testing.T) {
	manager := newRuntimeManager(func(_ context.Context, _ string) (*Runtime, error) {
		return EmptyRuntime(), nil
	})

	require.NoError(t, manager.Close())
	require.NoError(t, manager.Close())
	_, err := manager.Runtime(context.Background(), "/workspace")
	require.EqualError(t, err, "extension runtime manager is closed")
}
