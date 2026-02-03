package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/plugins"
	"github.com/pkg/errors"
)

// HookDirConfig represents a directory to scan for hooks with optional prefix
type HookDirConfig struct {
	Dir    string // Directory path
	Prefix string // Prefix for hook names (e.g., "org/repo/" for plugins)
}

// Discovery handles hook discovery from configured directories
type Discovery struct {
	hookDirs []HookDirConfig
}

// DiscoveryOption is a function that configures a Discovery
type DiscoveryOption func(*Discovery) error

// WithDefaultDirs initializes with default hook directories including plugin directories.
// This provides the full precedence order: repo-local standalone > repo-local plugins > global standalone > global plugins
func WithDefaultDirs() DiscoveryOption {
	return func(d *Discovery) error {
		discovery, err := plugins.NewDiscovery()
		if err != nil {
			return errors.Wrap(err, "failed to create plugins discovery")
		}

		pluginHookDirs := discovery.HookDirs()
		d.hookDirs = make([]HookDirConfig, len(pluginHookDirs))
		for i, dir := range pluginHookDirs {
			d.hookDirs[i] = HookDirConfig{
				Dir:    dir.Dir,
				Prefix: dir.Prefix,
			}
		}
		return nil
	}
}

// WithHookDirs sets custom hook directories (for testing).
// All directories are treated as standalone (no prefix).
func WithHookDirs(dirs ...string) DiscoveryOption {
	return func(d *Discovery) error {
		d.hookDirs = make([]HookDirConfig, len(dirs))
		for i, dir := range dirs {
			d.hookDirs[i] = HookDirConfig{Dir: dir, Prefix: ""}
		}
		return nil
	}
}

// WithHookDirConfigs sets custom hook directories with prefix support (for testing).
func WithHookDirConfigs(configs ...HookDirConfig) DiscoveryOption {
	return func(d *Discovery) error {
		d.hookDirs = configs
		return nil
	}
}

// NewDiscovery creates a new hook discovery instance
func NewDiscovery(opts ...DiscoveryOption) (*Discovery, error) {
	d := &Discovery{}

	if len(opts) == 0 {
		if err := WithDefaultDirs()(d); err != nil {
			return nil, err
		}
	} else {
		for _, opt := range opts {
			if err := opt(d); err != nil {
				return nil, err
			}
		}
	}

	return d, nil
}

// DiscoverHooks finds all available hooks from configured directories.
// Hooks from plugin directories are prefixed with "org/repo/" format.
// Hooks are discovered in precedence order, with higher precedence hooks shadowing lower ones.
func (d *Discovery) DiscoverHooks() (map[HookType][]*Hook, error) {
	hooks := make(map[HookType][]*Hook)
	seen := make(map[string]bool) // Track seen hook names to maintain precedence

	for _, dirConfig := range d.hookDirs {
		entries, err := os.ReadDir(dirConfig.Dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Skip non-existent directories
			}
			return nil, errors.Wrapf(err, "failed to read hook directory %s", dirConfig.Dir)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue // Skip directories
			}

			// Skip disabled hooks (names ending with .disable)
			if strings.HasSuffix(entry.Name(), ".disable") {
				continue
			}

			hookPath := filepath.Join(dirConfig.Dir, entry.Name())

			// Check if executable
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Mode()&0o111 == 0 {
				continue // Not executable
			}

			// Build hook name with prefix (for plugin hooks)
			hookName := entry.Name()
			if dirConfig.Prefix != "" {
				hookName = dirConfig.Prefix + entry.Name()
			}

			// Skip if already discovered (earlier directories have precedence)
			if seen[hookName] {
				continue
			}
			seen[hookName] = true

			// Query hook type
			hookType, err := queryHookType(hookPath)
			if err != nil {
				continue // Skip invalid hooks
			}

			hook := &Hook{
				Name:     hookName,
				Path:     hookPath,
				HookType: hookType,
			}

			hooks[hookType] = append(hooks[hookType], hook)
		}
	}

	return hooks, nil
}

// queryHookType executes the hook with "hook" argument to determine its type
func queryHookType(hookPath string) (HookType, error) {
	cmd := exec.Command(hookPath, "hook")
	output, err := cmd.Output()
	if err != nil {
		return "", errors.Wrap(err, "failed to query hook type")
	}

	hookTypeStr := strings.TrimSpace(string(output))
	hookType := HookType(hookTypeStr)

	// Validate hook type
	switch hookType {
	case HookTypeBeforeToolCall, HookTypeAfterToolCall, HookTypeUserMessageSend, HookTypeAgentStop, HookTypeTurnEnd:
		return hookType, nil
	default:
		return "", errors.Errorf("invalid hook type: %s", hookTypeStr)
	}
}
