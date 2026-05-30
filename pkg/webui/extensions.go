package webui

import (
	"context"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
)

type webExtensionRuntimeManager struct {
	mu       sync.Mutex
	runtimes map[string]*extensions.Runtime
}

func newWebExtensionRuntimeManager() *webExtensionRuntimeManager {
	return &webExtensionRuntimeManager{runtimes: map[string]*extensions.Runtime{}}
}

func (m *webExtensionRuntimeManager) Runtime(ctx context.Context, cwd string) (*extensions.Runtime, error) {
	if m == nil {
		return extensions.NewRuntimeFromViper(ctx, cwd)
	}
	key := webExtensionRuntimeKey(cwd)
	m.mu.Lock()
	defer m.mu.Unlock()
	if runtime, ok := m.runtimes[key]; ok {
		return runtime, nil
	}
	runtime, err := extensions.NewRuntimeFromViper(ctx, cwd)
	if err != nil {
		return nil, err
	}
	m.runtimes[key] = runtime
	return runtime, nil
}

func (m *webExtensionRuntimeManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	runtimes := m.runtimes
	m.runtimes = map[string]*extensions.Runtime{}
	m.mu.Unlock()

	var firstErr error
	for key, runtime := range runtimes {
		if err := runtime.Close(); err != nil && firstErr == nil {
			firstErr = errors.Wrapf(err, "failed to close extension runtime for %s", key)
			logger.G(context.Background()).WithError(err).WithField("cwd", key).Warn("failed to close web extension runtime")
		}
	}
	return firstErr
}

func webExtensionRuntimeKey(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return osutil.CanonicalizePath(cwd)
}
