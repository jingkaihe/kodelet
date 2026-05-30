package extensions

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// Runtime manages discovered extension processes and registrations.
type Runtime struct {
	config              Config
	workingDir          string
	mu                  sync.RWMutex
	processes           []*Process
	tools               map[string]*Tool
	commands            []Command
	subs                []Subscription
	eventHandlersByName map[string][]eventHandler
}

// Command is an extension command registration bound to its process.
type Command struct {
	ExtensionID  string
	Process      *Process
	Registration CommandRegistration
}

// EmptyRuntime creates an extension runtime with no processes or registrations.
// It is useful for callers that want to attach a non-nil runtime before discovery
// has found any extensions.
func EmptyRuntime() *Runtime {
	return &Runtime{
		config:              DefaultConfig(),
		tools:               map[string]*Tool{},
		eventHandlersByName: map[string][]eventHandler{},
	}
}

// NewRuntime creates and initializes an extension runtime.
func NewRuntime(ctx context.Context, opts ...DiscoveryOption) (*Runtime, error) {
	discovery, err := NewDiscovery(opts...)
	if err != nil {
		return nil, err
	}
	r := EmptyRuntime()
	r.config = discovery.config
	r.workingDir = discovery.workingDir
	if err := r.initialize(ctx, discovery); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

// NewRuntimeFromViper creates a runtime from viper config.
func NewRuntimeFromViper(ctx context.Context, workingDir string) (*Runtime, error) {
	config := LoadConfigFromViper()
	return NewRuntime(ctx, WithConfig(config), WithWorkingDir(workingDir))
}

func (r *Runtime) initialize(ctx context.Context, discovery *Discovery) error {
	if !r.config.Enabled {
		return nil
	}
	extensions, err := discovery.Discover()
	if err != nil {
		return err
	}
	for _, ext := range extensions {
		proc, err := StartProcess(ctx, ext, r.config.Timeout)
		if err != nil {
			return errors.Wrapf(err, "failed to start extension %s", ext.ID)
		}
		initCtx, cancel := context.WithTimeout(ctx, timeoutOrDefault(r.config.Timeout, DefaultConfig().Timeout))
		result, err := proc.Initialize(initCtx, r.workingDir)
		cancel()
		if err != nil {
			_ = proc.Close()
			return errors.Wrapf(err, "failed to initialize extension %s", ext.ID)
		}
		r.processes = append(r.processes, proc)
		if err := r.register(ctx, proc, result); err != nil {
			return err
		}
	}
	callContext := ExtensionCallContext{CWD: r.workingDir, InvokedBy: "main"}
	r.DispatchSessionStart(ctx, callContext)
	r.DispatchResourcesDiscover(ctx, callContext)
	return nil
}

func (r *Runtime) register(_ context.Context, proc *Process, result *InitializeResult) error {
	if result == nil {
		return nil
	}
	for _, registration := range result.Tools {
		if !r.toolEnabled(registration.Name) {
			continue
		}
		if _, exists := r.tools[registration.Name]; exists {
			return errors.Errorf("duplicate extension tool registration: %s", registration.Name)
		}
		toolTimeout := r.toolTimeout(registration.Name)
		tool, err := newTool(proc.Extension.ID, proc, registration, toolTimeout, r.config.MaxOutputSize)
		if err != nil {
			return errors.Wrapf(err, "failed to register extension tool %s", registration.Name)
		}
		r.tools[registration.Name] = tool
	}
	for _, command := range result.Commands {
		r.commands = append(r.commands, Command{ExtensionID: proc.Extension.ID, Process: proc, Registration: command})
	}
	for _, subscription := range result.Subscriptions {
		r.subs = append(r.subs, subscription)
		r.eventHandlersByName[subscription.Event] = append(r.eventHandlersByName[subscription.Event], eventHandler{
			process: proc,
			sub:     subscription,
			order:   len(r.subs) - 1,
		})
	}
	return nil
}

func (r *Runtime) toolEnabled(name string) bool {
	toolConfig, ok := r.config.Tools[name]
	return !ok || toolConfig.Enabled == nil || *toolConfig.Enabled
}

func (r *Runtime) toolTimeout(name string) time.Duration {
	if toolConfig, ok := r.config.Tools[name]; ok && toolConfig.Timeout > 0 {
		return toolConfig.Timeout
	}
	return timeoutOrDefault(r.config.ToolTimeout, DefaultConfig().ToolTimeout)
}

func timeoutOrDefault(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

// Tools returns registered extension tools sorted by name.
func (r *Runtime) Tools() []tooltypes.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	tools := make([]tooltypes.Tool, 0, len(names))
	for _, name := range names {
		tools = append(tools, r.tools[name])
	}
	return tools
}

// Commands returns registered extension commands.
func (r *Runtime) Commands() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	commands := append([]Command(nil), r.commands...)
	sort.SliceStable(commands, func(i, j int) bool {
		return strings.Compare(commands[i].Registration.Name, commands[j].Registration.Name) < 0
	})
	return commands
}

// Subscriptions returns registered extension event subscriptions.
func (r *Runtime) Subscriptions() []Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Subscription(nil), r.subs...)
}

// Close terminates all extension processes.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.DispatchSessionEnd(context.Background(), ExtensionCallContext{CWD: r.workingDir, InvokedBy: "main"})
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, proc := range r.processes {
		if err := proc.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.processes = nil
	return firstErr
}
