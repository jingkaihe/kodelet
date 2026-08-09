package extensions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeManagerReusesRuntimeForCanonicalWorkingDirectory(t *testing.T) {
	var calls int
	manager := newRuntimeManager(func(_ context.Context, _ string, _ Config) (*Runtime, error) {
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
	manager := newRuntimeManager(func(_ context.Context, _ string, _ Config) (*Runtime, error) {
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

func TestRuntimeManagerDiscoveryCachesCommandsBeforeLifecycle(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	statePath := filepath.Join(rootDir, "events.log")
	extDir := filepath.Join(rootDir, "events")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-events"), helperExtensionScript(t))
	manager := newRuntimeManager(func(ctx context.Context, _ string, _ Config) (*Runtime, error) {
		return newRuntime(
			ctx,
			false,
			WithConfig(DefaultConfig()),
			WithWorkingDir(rootDir),
			WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
		)
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })

	discovered, err := manager.RuntimeForCommandDiscovery(context.Background(), rootDir)
	require.NoError(t, err)
	assert.NotEmpty(t, discovered.SlashCommands())
	_, err = os.ReadFile(statePath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	runCtx := ContextWithUIInputBroker(context.Background(), staticUIInputBroker{value: "ready"})
	active, err := manager.RuntimeWithCallContext(runCtx, rootDir, ExtensionCallContext{
		ConversationID: "conversation-123",
		Provider:       "anthropic",
		Model:          "claude-test",
		Profile:        "work",
	})
	require.NoError(t, err)
	assert.Same(t, discovered, active)
	_, err = manager.Runtime(runCtx, rootDir)
	require.NoError(t, err)

	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), EventSessionStart+"\n"))
	assert.Equal(t, 1, strings.Count(string(data), EventResourcesDiscover+"\n"))

	contextData, err := os.ReadFile(filepath.Join(rootDir, "session-start-context.json"))
	require.NoError(t, err)
	var callContext ExtensionCallContext
	require.NoError(t, json.Unmarshal(contextData, &callContext))
	assert.Equal(t, "conversation-123", callContext.ConversationID)
	assert.Equal(t, "anthropic", callContext.Provider)
	assert.Equal(t, "claude-test", callContext.Model)
	assert.Equal(t, "work", callContext.Profile)
}

func TestRuntimeManagerDoesNotCacheCreationFailures(t *testing.T) {
	var calls int
	manager := newRuntimeManager(func(_ context.Context, _ string, _ Config) (*Runtime, error) {
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

func TestRuntimeManagerScopesConfiguredRuntimesByVariantAndFingerprint(t *testing.T) {
	rootDir := t.TempDir()
	var configs []Config
	manager := newRuntimeManager(func(_ context.Context, _ string, config Config) (*Runtime, error) {
		configs = append(configs, config)
		return EmptyRuntime(), nil
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })

	baseConfig := Config{Enabled: false, MaxOutputSize: 1024}
	first, err := manager.RuntimeWithConfigAndCallContext(context.Background(), rootDir, "runner-work", baseConfig, ExtensionCallContext{})
	require.NoError(t, err)
	reused, err := manager.RuntimeForCommandDiscoveryWithConfig(context.Background(), filepath.Join(rootDir, "."), "runner-work", baseConfig)
	require.NoError(t, err)
	assert.Same(t, first, reused)

	otherVariant, err := manager.RuntimeWithConfigAndCallContext(context.Background(), rootDir, "runner-ci", baseConfig, ExtensionCallContext{})
	require.NoError(t, err)
	assert.NotSame(t, first, otherVariant)

	changedConfig := baseConfig
	changedConfig.MaxOutputSize = 2048
	replacement, err := manager.RuntimeWithConfigAndCallContext(context.Background(), rootDir, "runner-work", changedConfig, ExtensionCallContext{})
	require.NoError(t, err)
	assert.NotSame(t, first, replacement)
	select {
	case <-first.runtimeCtx.Done():
		t.Fatal("replacing a cached runtime closed a generation that may still be in use")
	default:
	}
	require.Len(t, configs, 3)
	assert.Equal(t, 2048, configs[2].MaxOutputSize)
}

func TestRuntimeManagerKeepsCachedRuntimeWhenFingerprintRefreshFails(t *testing.T) {
	manager := newRuntimeManager(func(_ context.Context, _ string, _ Config) (*Runtime, error) {
		return EmptyRuntime(), nil
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })

	first, err := manager.Runtime(context.Background(), "/workspace")
	require.NoError(t, err)
	manager.fingerprint = func(string, Config) (string, error) {
		return "", errors.New("extension metadata is temporarily unreadable")
	}

	cached, err := manager.Runtime(context.Background(), "/workspace")
	require.NoError(t, err)
	assert.Same(t, first, cached)

	_, err = manager.Runtime(context.Background(), "/other-workspace")
	require.ErrorContains(t, err, "temporarily unreadable")
}

func TestRuntimeManagerClosesRetiredRuntimeAfterItsCallerLeaseEnds(t *testing.T) {
	manager := newRuntimeManager(func(_ context.Context, _ string, _ Config) (*Runtime, error) {
		return EmptyRuntime(), nil
	})
	t.Cleanup(func() { assert.NoError(t, manager.Close()) })
	operationCtx, cancelOperation := context.WithCancel(context.Background())
	leaseCtx, cancelLease := context.WithCancel(context.Background())
	baseConfig := Config{Enabled: false, MaxOutputSize: 1024}
	first, err := manager.RuntimeWithConfigAndCallContextForLease(operationCtx, leaseCtx, "/workspace", "runner-work", baseConfig, ExtensionCallContext{})
	require.NoError(t, err)
	cancelOperation()

	changedConfig := baseConfig
	changedConfig.MaxOutputSize = 2048
	replacement, err := manager.RuntimeWithConfigAndCallContext(context.Background(), "/workspace", "runner-work", changedConfig, ExtensionCallContext{})
	require.NoError(t, err)
	assert.NotSame(t, first, replacement)
	select {
	case <-first.runtimeCtx.Done():
		t.Fatal("retired runtime closed before its active caller lease ended")
	default:
	}

	cancelLease()
	require.Eventually(t, func() bool {
		select {
		case <-first.runtimeCtx.Done():
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestRuntimeManagerDoesNotCacheRuntimeCreatedByCanceledCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	manager := newRuntimeManager(func(_ context.Context, _ string, _ Config) (*Runtime, error) {
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
	manager := newRuntimeManager(func(_ context.Context, _ string, _ Config) (*Runtime, error) {
		return EmptyRuntime(), nil
	})

	require.NoError(t, manager.Close())
	require.NoError(t, manager.Close())
	_, err := manager.Runtime(context.Background(), "/workspace")
	require.EqualError(t, err, "extension runtime manager is closed")
}
