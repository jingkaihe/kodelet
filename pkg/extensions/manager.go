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

type (
	runtimeFactory         func(context.Context, string, Config) (*Runtime, error)
	runtimeFingerprintFunc func(string, Config) (string, error)
)

type managedRuntime struct {
	runtime     *Runtime
	fingerprint string
	leases      int
	retired     bool
	closeOnce   sync.Once
	closeErr    error
}

func (r *managedRuntime) close() error {
	if r == nil || r.runtime == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.closeErr = r.runtime.Close()
	})
	return r.closeErr
}

// RuntimeManager reuses extension runtimes for the lifetime of an interactive host.
// Runtimes are scoped by canonical working directory and closed together with the host.
type RuntimeManager struct {
	mu          sync.Mutex
	runtimes    map[string]*managedRuntime
	retired     map[*managedRuntime]struct{}
	newRuntime  runtimeFactory
	fingerprint runtimeFingerprintFunc
	closed      bool
}

// NewRuntimeManager creates a persistent extension runtime manager.
func NewRuntimeManager() *RuntimeManager {
	return newRuntimeManager(func(ctx context.Context, cwd string, config Config) (*Runtime, error) {
		return newRuntime(ctx, false, WithConfig(config), WithWorkingDir(cwd))
	})
}

func newRuntimeManager(factory runtimeFactory) *RuntimeManager {
	return &RuntimeManager{
		runtimes:    map[string]*managedRuntime{},
		retired:     map[*managedRuntime]struct{}{},
		newRuntime:  factory,
		fingerprint: runtimeFingerprint,
	}
}

// Runtime returns the runtime associated with cwd, creating it on first use.
func (m *RuntimeManager) Runtime(ctx context.Context, cwd string) (*Runtime, error) {
	return m.runtime(ctx, ctx, cwd, "", LoadConfigFromViper(), true, ExtensionCallContext{})
}

// RuntimeWithCallContext returns the runtime associated with cwd and starts its
// lifecycle with the supplied call context when it has not already started.
func (m *RuntimeManager) RuntimeWithCallContext(ctx context.Context, cwd string, callContext ExtensionCallContext) (*Runtime, error) {
	return m.runtime(ctx, ctx, cwd, "", LoadConfigFromViper(), true, callContext)
}

// RuntimeForCommandDiscovery returns a cached runtime without starting session lifecycle events.
func (m *RuntimeManager) RuntimeForCommandDiscovery(ctx context.Context, cwd string) (*Runtime, error) {
	return m.runtime(ctx, ctx, cwd, "", LoadConfigFromViper(), false, ExtensionCallContext{})
}

// RuntimeWithConfigAndCallContext returns a runtime isolated by a caller-owned
// variant such as a runner environment profile.
func (m *RuntimeManager) RuntimeWithConfigAndCallContext(ctx context.Context, cwd, variant string, config Config, callContext ExtensionCallContext) (*Runtime, error) {
	return m.runtime(ctx, ctx, cwd, variant, config, true, callContext)
}

// RuntimeWithConfigAndCallContextForLease separates runtime construction
// cancellation from the caller lifetime that pins the selected generation.
func (m *RuntimeManager) RuntimeWithConfigAndCallContextForLease(ctx, leaseCtx context.Context, cwd, variant string, config Config, callContext ExtensionCallContext) (*Runtime, error) {
	return m.runtime(ctx, leaseCtx, cwd, variant, config, true, callContext)
}

// RuntimeWithConfigAndCallContextForIsolatedLease creates a runtime owned only
// by one caller lease. It is closed when that lease ends and is never reused by
// another concurrent session. The returned release function synchronously
// closes the runtime and is safe to call more than once.
func (m *RuntimeManager) RuntimeWithConfigAndCallContextForIsolatedLease(ctx, leaseCtx context.Context, cwd, _ string, config Config, callContext ExtensionCallContext) (*Runtime, func() error, error) {
	if m == nil {
		return nil, nil, errors.New("extension runtime manager is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if leaseCtx == nil {
		leaseCtx = ctx
	}
	if err := leaseCtx.Err(); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, errors.New("extension runtime manager is closed")
	}
	factory := m.newRuntime
	m.mu.Unlock()

	runtime, err := factory(ctx, cwd, config)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = runtime.Close()
		return nil, nil, err
	}
	if err := leaseCtx.Err(); err != nil {
		_ = runtime.Close()
		return nil, nil, err
	}
	managed := &managedRuntime{runtime: runtime, leases: 1, retired: true}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = runtime.Close()
		return nil, nil, errors.New("extension runtime manager is closed")
	}
	m.retired[managed] = struct{}{}
	m.mu.Unlock()
	runtime.startLifecycle(ctx, callContext)
	release := func() error {
		return m.release(managed)
	}
	context.AfterFunc(leaseCtx, func() {
		_ = release()
	})
	return runtime, release, nil
}

// RuntimeForCommandDiscoveryWithConfig returns a configured runtime without starting lifecycle events.
func (m *RuntimeManager) RuntimeForCommandDiscoveryWithConfig(ctx context.Context, cwd, variant string, config Config) (*Runtime, error) {
	return m.runtime(ctx, ctx, cwd, variant, config, false, ExtensionCallContext{})
}

func (m *RuntimeManager) runtime(ctx, leaseCtx context.Context, cwd, variant string, config Config, startLifecycle bool, callContext ExtensionCallContext) (*Runtime, error) {
	if m == nil {
		return nil, errors.New("extension runtime manager is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if leaseCtx == nil {
		leaseCtx = ctx
	}

	key := runtimeManagerKey(cwd, variant)
	fingerprint, err := m.fingerprint(cwd, config)
	if err != nil {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("extension runtime manager is closed")
		}
		cached := m.runtimes[key]
		if cached == nil {
			m.mu.Unlock()
			return nil, err
		}
		m.acquireLocked(leaseCtx, cached)
		m.mu.Unlock()
		if startLifecycle {
			cached.runtime.startLifecycle(ctx, callContext)
		}
		return cached.runtime, nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("extension runtime manager is closed")
	}
	managed := m.runtimes[key]
	var closeRetired *managedRuntime
	if managed == nil || managed.fingerprint != fingerprint {
		replacement, err := m.newRuntime(ctx, cwd, config)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			m.mu.Unlock()
			_ = replacement.Close()
			return nil, err
		}
		if managed != nil {
			managed.retired = true
			if managed.leases == 0 {
				closeRetired = managed
			} else {
				m.retired[managed] = struct{}{}
			}
		}
		managed = &managedRuntime{runtime: replacement, fingerprint: fingerprint}
		m.runtimes[key] = managed
	}
	m.acquireLocked(leaseCtx, managed)
	runtime := managed.runtime
	m.mu.Unlock()
	if closeRetired != nil {
		_ = closeRetired.close()
	}
	if startLifecycle {
		runtime.startLifecycle(ctx, callContext)
	}
	return runtime, nil
}

func (m *RuntimeManager) acquireLocked(ctx context.Context, managed *managedRuntime) {
	managed.leases++
	context.AfterFunc(ctx, func() {
		_ = m.release(managed)
	})
}

func (m *RuntimeManager) release(managed *managedRuntime) error {
	if m == nil || managed == nil {
		return nil
	}
	closeRuntime := false
	m.mu.Lock()
	if managed.leases > 0 {
		managed.leases--
	}
	if managed.retired && managed.leases == 0 {
		if !m.closed {
			delete(m.retired, managed)
		}
		closeRuntime = true
	}
	m.mu.Unlock()
	if closeRuntime {
		return managed.close()
	}
	return nil
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
	retired := m.retired
	m.runtimes = nil
	m.retired = nil
	m.mu.Unlock()

	var firstErr error
	seen := make(map[*Runtime]struct{}, len(runtimes)+len(retired))
	for key, managed := range runtimes {
		seen[managed.runtime] = struct{}{}
		if err := managed.close(); err != nil && firstErr == nil {
			firstErr = errors.Wrapf(err, "failed to close extension runtime for %s", key)
		}
	}
	for managed := range retired {
		if _, ok := seen[managed.runtime]; ok {
			continue
		}
		if err := managed.close(); err != nil && firstErr == nil {
			firstErr = errors.Wrap(err, "failed to close retired extension runtime")
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
