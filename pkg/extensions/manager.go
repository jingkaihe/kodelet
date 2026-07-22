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
	return newRuntimeManager(NewRuntimeFromViper)
}

func newRuntimeManager(factory runtimeFactory) *RuntimeManager {
	return &RuntimeManager{
		runtimes:   map[string]*Runtime{},
		newRuntime: factory,
	}
}

// Runtime returns the runtime associated with cwd, creating it on first use.
func (m *RuntimeManager) Runtime(ctx context.Context, cwd string) (*Runtime, error) {
	if m == nil {
		return NewRuntimeFromViper(ctx, cwd)
	}

	key := runtimeManagerKey(cwd)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("extension runtime manager is closed")
	}
	if runtime, ok := m.runtimes[key]; ok {
		return runtime, nil
	}

	runtime, err := m.newRuntime(ctx, cwd)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	m.runtimes[key] = runtime
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
