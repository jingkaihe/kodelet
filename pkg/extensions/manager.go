package extensions

import (
	"context"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
)

type runtimeFactory func(context.Context, string) (*Runtime, error)

// RuntimeManager reuses extension runtimes for the lifetime of an interactive host.
// Runtimes are scoped by canonical working directory and closed together with the host.
type RuntimeManager struct {
	mu         sync.Mutex
	runtimes   map[string]*Runtime
	newRuntime runtimeFactory
	closed     bool
}

// NewRuntimeManager creates a persistent extension runtime manager.
func NewRuntimeManager() *RuntimeManager {
	return newRuntimeManager(func(ctx context.Context, cwd string) (*Runtime, error) {
		return newRuntimeFromViper(ctx, cwd, false)
	})
}

func newRuntimeManager(factory runtimeFactory) *RuntimeManager {
	return &RuntimeManager{
		runtimes:   map[string]*Runtime{},
		newRuntime: factory,
	}
}

// Runtime returns the runtime associated with cwd, creating it on first use.
func (m *RuntimeManager) Runtime(ctx context.Context, cwd string) (*Runtime, error) {
	return m.runtime(ctx, cwd, true, ExtensionCallContext{})
}

// RuntimeWithCallContext returns the runtime associated with cwd and starts its
// lifecycle with the supplied call context when it has not already started.
func (m *RuntimeManager) RuntimeWithCallContext(ctx context.Context, cwd string, callContext ExtensionCallContext) (*Runtime, error) {
	return m.runtime(ctx, cwd, true, callContext)
}

// RuntimeForCommandDiscovery returns a cached runtime without starting session lifecycle events.
func (m *RuntimeManager) RuntimeForCommandDiscovery(ctx context.Context, cwd string) (*Runtime, error) {
	return m.runtime(ctx, cwd, false, ExtensionCallContext{})
}

func (m *RuntimeManager) runtime(ctx context.Context, cwd string, startLifecycle bool, callContext ExtensionCallContext) (*Runtime, error) {
	if m == nil {
		return nil, errors.New("extension runtime manager is required")
	}

	key := runtimeManagerKey(cwd)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("extension runtime manager is closed")
	}
	runtime := m.runtimes[key]
	if runtime == nil {
		var err error
		runtime, err = m.newRuntime(ctx, cwd)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			_ = runtime.Close()
			return nil, err
		}
		m.runtimes[key] = runtime
	}
	if startLifecycle {
		runtime.startLifecycle(ctx, callContext)
	}
	return runtime, nil
}

// Close terminates every managed extension runtime. It is safe to call more than once.
func (m *RuntimeManager) Close() error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	runtimes := m.runtimes
	m.runtimes = nil
	m.mu.Unlock()

	var firstErr error
	for key, runtime := range runtimes {
		if err := runtime.Close(); err != nil && firstErr == nil {
			firstErr = errors.Wrapf(err, "failed to close extension runtime for %s", key)
		}
	}
	return firstErr
}

func runtimeManagerKey(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return osutil.CanonicalizePath(cwd)
}
