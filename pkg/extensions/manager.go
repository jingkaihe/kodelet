package extensions

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
)

type runtimeFactory func(context.Context, string, Config) (*Runtime, error)

type managedRuntime struct {
	runtime     *Runtime
	fingerprint string
}

// RuntimeManager reuses extension runtimes for the lifetime of an interactive host.
// Runtimes are scoped by canonical working directory and closed together with the host.
type RuntimeManager struct {
	mu         sync.Mutex
	runtimes   map[string]managedRuntime
	newRuntime runtimeFactory
	closed     bool
}

// NewRuntimeManager creates a persistent extension runtime manager.
func NewRuntimeManager() *RuntimeManager {
	return newRuntimeManager(func(ctx context.Context, cwd string, config Config) (*Runtime, error) {
		return newRuntime(ctx, false, WithConfig(config), WithWorkingDir(cwd))
	})
}

func newRuntimeManager(factory runtimeFactory) *RuntimeManager {
	return &RuntimeManager{
		runtimes:   map[string]managedRuntime{},
		newRuntime: factory,
	}
}

// Runtime returns the runtime associated with cwd, creating it on first use.
func (m *RuntimeManager) Runtime(ctx context.Context, cwd string) (*Runtime, error) {
	return m.runtime(ctx, cwd, "", LoadConfigFromViper(), true, ExtensionCallContext{})
}

// RuntimeWithCallContext returns the runtime associated with cwd and starts its
// lifecycle with the supplied call context when it has not already started.
func (m *RuntimeManager) RuntimeWithCallContext(ctx context.Context, cwd string, callContext ExtensionCallContext) (*Runtime, error) {
	return m.runtime(ctx, cwd, "", LoadConfigFromViper(), true, callContext)
}

// RuntimeForCommandDiscovery returns a cached runtime without starting session lifecycle events.
func (m *RuntimeManager) RuntimeForCommandDiscovery(ctx context.Context, cwd string) (*Runtime, error) {
	return m.runtime(ctx, cwd, "", LoadConfigFromViper(), false, ExtensionCallContext{})
}

// RuntimeWithConfigAndCallContext returns a runtime isolated by a caller-owned
// variant such as a runner environment profile.
func (m *RuntimeManager) RuntimeWithConfigAndCallContext(ctx context.Context, cwd, variant string, config Config, callContext ExtensionCallContext) (*Runtime, error) {
	return m.runtime(ctx, cwd, variant, config, true, callContext)
}

// RuntimeForCommandDiscoveryWithConfig returns a configured runtime without starting lifecycle events.
func (m *RuntimeManager) RuntimeForCommandDiscoveryWithConfig(ctx context.Context, cwd, variant string, config Config) (*Runtime, error) {
	return m.runtime(ctx, cwd, variant, config, false, ExtensionCallContext{})
}

func (m *RuntimeManager) runtime(ctx context.Context, cwd, variant string, config Config, startLifecycle bool, callContext ExtensionCallContext) (*Runtime, error) {
	if m == nil {
		return nil, errors.New("extension runtime manager is required")
	}

	key := runtimeManagerKey(cwd, variant)
	fingerprint, err := runtimeFingerprint(cwd, config)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("extension runtime manager is closed")
	}
	managed := m.runtimes[key]
	runtime := managed.runtime
	if runtime == nil || managed.fingerprint != fingerprint {
		replacement, err := m.newRuntime(ctx, cwd, config)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			_ = replacement.Close()
			return nil, err
		}
		m.runtimes[key] = managedRuntime{runtime: replacement, fingerprint: fingerprint}
		if runtime != nil {
			_ = runtime.Close()
		}
		runtime = replacement
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
	for key, managed := range runtimes {
		if err := managed.runtime.Close(); err != nil && firstErr == nil {
			firstErr = errors.Wrapf(err, "failed to close extension runtime for %s", key)
		}
	}
	return firstErr
}

func runtimeManagerKey(cwd, variant string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		cwd = osutil.CanonicalizePath(cwd)
	}
	return cwd + "\x00" + strings.TrimSpace(variant)
}

func runtimeFingerprint(cwd string, config Config) (string, error) {
	payload, err := json.Marshal(config)
	if err != nil {
		return "", errors.Wrap(err, "failed to encode extension runtime configuration")
	}
	discovery, err := NewDiscovery(WithConfig(config), WithWorkingDir(cwd))
	if err != nil {
		return "", err
	}
	discovered, err := discovery.Discover()
	if err != nil {
		return "", err
	}
	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].ID == discovered[j].ID {
			return discovered[i].ExecPath < discovered[j].ExecPath
		}
		return discovered[i].ID < discovered[j].ID
	})
	hash := sha256.New()
	_, _ = hash.Write(payload)
	for _, extension := range discovered {
		info, err := os.Stat(extension.ExecPath)
		if err != nil {
			return "", errors.Wrapf(err, "failed to inspect extension %s", extension.ID)
		}
		_, _ = fmt.Fprintf(hash, "\x00%s\x00%s\x00%d\x00%d\x00%d", extension.ID, extension.ExecPath, info.Size(), info.ModTime().UnixNano(), info.Mode())
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
