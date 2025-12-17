package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// Discovery handles hook discovery from configured directories
type Discovery struct {
	hookDirs []string
}

// DiscoveryOption is a function that configures a Discovery
type DiscoveryOption func(*Discovery) error

// WithDefaultDirs initializes with default hook directories
func WithDefaultDirs() DiscoveryOption {
	return func(d *Discovery) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return errors.Wrap(err, "failed to get user home directory")
		}
		d.hookDirs = []string{
			"./.kodelet/hooks",                          // Repo-local (higher precedence)
			filepath.Join(homeDir, ".kodelet", "hooks"), // User-global
		}
		return nil
	}
}

// WithHookDirs sets custom hook directories
func WithHookDirs(dirs ...string) DiscoveryOption {
	return func(d *Discovery) error {
		d.hookDirs = dirs
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

// DiscoverHooks finds all available hooks from configured directories
func (d *Discovery) DiscoverHooks() (map[HookType][]*Hook, error) {
	hooks := make(map[HookType][]*Hook)
	seen := make(map[string]bool) // Track seen hook names to maintain precedence

	for _, dir := range d.hookDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Skip non-existent directories
			}
			return nil, errors.Wrapf(err, "failed to read hook directory %s", dir)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue // Skip directories
			}

			hookPath := filepath.Join(dir, entry.Name())

			// Check if executable
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Mode()&0o111 == 0 {
				continue // Not executable
			}

			// Skip if already discovered (earlier directories have precedence)
			if seen[entry.Name()] {
				continue
			}
			seen[entry.Name()] = true

			// Query hook type
			hookType, err := queryHookType(hookPath)
			if err != nil {
				continue // Skip invalid hooks
			}

			hook := &Hook{
				Name:     entry.Name(),
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
	case HookTypeBeforeToolCall, HookTypeAfterToolCall, HookTypeUserMessageSend, HookTypeAgentStop:
		return hookType, nil
	default:
		return "", errors.Errorf("invalid hook type: %s", hookTypeStr)
	}
}
