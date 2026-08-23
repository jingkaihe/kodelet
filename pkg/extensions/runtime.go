package extensions

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// Runtime manages discovered extension processes and registrations.
type Runtime struct {
	config              Config
	workingDir          string
	runtimeCtx          context.Context
	cancelRuntime       context.CancelFunc
	mu                  sync.RWMutex
	processes           []*Process
	tools               map[string]*Tool
	commands            []Command
	shortcuts           map[string]registeredShortcut
	subs                []Subscription
	eventHandlersByName map[string][]eventHandler
	lifecycleStarted    bool
	lifecycleCtx        context.Context
	lifecycleCallCtx    ExtensionCallContext
}

// Command is an extension command registration bound to its process.
type Command struct {
	ExtensionID  string
	Process      *Process
	Registration CommandRegistration
}

// Shortcut describes an effective extension shortcut exposed to an interactive host.
type Shortcut struct {
	Key         string
	Description string
	ExtensionID string
}

type registeredShortcut struct {
	Shortcut
	process *Process
}

// EmptyRuntime creates an extension runtime with no processes or registrations.
// It is useful for callers that want to attach a non-nil runtime before discovery
// has found any extensions.
func EmptyRuntime() *Runtime {
	return emptyRuntimeWithContext(context.Background())
}

func emptyRuntimeWithContext(ctx context.Context) *Runtime {
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeCtx, cancelRuntime := context.WithCancel(context.WithoutCancel(ctx))
	return &Runtime{
		config:              DefaultConfig(),
		runtimeCtx:          runtimeCtx,
		cancelRuntime:       cancelRuntime,
		tools:               map[string]*Tool{},
		shortcuts:           map[string]registeredShortcut{},
		eventHandlersByName: map[string][]eventHandler{},
	}
}

// NewRuntime creates and initializes an extension runtime.
func NewRuntime(ctx context.Context, opts ...DiscoveryOption) (*Runtime, error) {
	return newRuntime(ctx, true, opts...)
}

func newRuntime(ctx context.Context, startLifecycle bool, opts ...DiscoveryOption) (*Runtime, error) {
	discovery, err := NewDiscovery(opts...)
	if err != nil {
		return nil, err
	}
	r := emptyRuntimeWithContext(ctx)
	r.config = discovery.config
	r.workingDir = discovery.workingDir
	if err := r.initialize(ctx, discovery); err != nil {
		_ = r.Close()
		return nil, err
	}
	if startLifecycle {
		r.startLifecycle(ctx, ExtensionCallContext{})
	}
	return r, nil
}

// NewRuntimeFromViper creates a runtime from viper config.
func NewRuntimeFromViper(ctx context.Context, workingDir string) (*Runtime, error) {
	return newRuntimeFromViper(ctx, workingDir, true)
}

func newRuntimeFromViper(ctx context.Context, workingDir string, startLifecycle bool) (*Runtime, error) {
	config := LoadConfigFromViper()
	return newRuntime(ctx, startLifecycle, WithConfig(config), WithWorkingDir(workingDir))
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
		if err := ctx.Err(); err != nil {
			return err
		}
		proc, err := StartProcess(r.runtimeCtx, ext, r.config, r.workingDir)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			logger.G(ctx).WithError(err).WithField("extension", ext.ID).Warn("failed to start extension; disabling for this process")
			continue
		}
		initCtx, cancel := context.WithTimeout(ctx, extensionInitializeTimeout)
		result, err := proc.Initialize(initCtx, r.workingDir)
		cancel()
		if err != nil {
			_ = proc.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			logger.G(ctx).WithError(err).WithField("extension", ext.ID).Warn("failed to initialize extension; disabling for this process")
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = proc.Close()
			return err
		}
		r.processes = append(r.processes, proc)
		if err := r.register(ctx, proc, result); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) startLifecycle(ctx context.Context, callContext ExtensionCallContext) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(callContext.CWD) == "" {
		callContext.CWD = r.workingDir
	}
	if strings.TrimSpace(callContext.InvokedBy) == "" {
		callContext.InvokedBy = "main"
	}
	r.mu.Lock()
	if r.lifecycleStarted {
		r.mu.Unlock()
		return
	}
	r.lifecycleStarted = true
	r.lifecycleCtx = context.WithoutCancel(ctx)
	r.lifecycleCallCtx = callContext
	r.mu.Unlock()
	r.DispatchSessionStart(ctx, callContext)
	r.DispatchResourcesDiscover(ctx, callContext)
}

func (r *Runtime) register(ctx context.Context, proc *Process, result *InitializeResult) error {
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
		tool, err := newTool(proc.Extension.ID, proc, registration, r.toolTimeout(registration), r.config.MaxOutputSize)
		if err != nil {
			return errors.Wrapf(err, "failed to register extension tool %s", registration.Name)
		}
		r.tools[registration.Name] = tool
	}
	for _, command := range result.Commands {
		if err := validateCommandRegistration(command); err != nil {
			return err
		}
		seenNames := map[string]struct{}{}
		primaryName := normalizeCommandName(command.Name)
		seenNames[primaryName] = struct{}{}
		if r.commandNameRegistered(command.Name) {
			return errors.Errorf("duplicate extension command registration: %s", normalizeCommandName(command.Name))
		}
		for _, alias := range command.Aliases {
			if normalizeCommandName(alias) == primaryName {
				continue
			}
			if err := addCommandAlias(seenNames, alias); err != nil {
				return err
			}
			if r.commandNameRegistered(alias) {
				return errors.Errorf("duplicate extension command registration: %s", normalizeCommandName(alias))
			}
		}
		r.commands = append(r.commands, Command{ExtensionID: proc.Extension.ID, Process: proc, Registration: command})
	}
	for _, registration := range result.Shortcuts {
		key, err := NormalizeShortcutKey(registration.Key)
		if err != nil {
			r.reportShortcutDiagnostic(ctx, proc.Extension.ID, errors.Wrap(err, "invalid extension shortcut").Error())
			continue
		}
		registration.Key = key
		if existing, ok := r.shortcuts[key]; ok {
			r.reportShortcutDiagnostic(ctx, proc.Extension.ID, fmt.Sprintf(
				"shortcut %q conflicts with extension %s; using %s",
				key,
				existing.ExtensionID,
				proc.Extension.ID,
			))
		}
		r.shortcuts[key] = registeredShortcut{
			Shortcut: Shortcut{
				Key:         key,
				Description: strings.TrimSpace(registration.Description),
				ExtensionID: proc.Extension.ID,
			},
			process: proc,
		}
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

func (r *Runtime) reportShortcutDiagnostic(ctx context.Context, extensionID, message string) {
	logger.G(ctx).WithField("extension", extensionID).Warn(message)
	if sink, ok := DiagnosticSinkFromContext(ctx); ok {
		sink.ReportDiagnostic(ctx, Diagnostic{
			Level:     DiagnosticLevelWarning,
			Extension: extensionID,
			Message:   message,
		})
	}
}

func addCommandAlias(names map[string]struct{}, name string) error {
	name = normalizeCommandName(name)
	if name == "" {
		return nil
	}
	if _, ok := names[name]; ok {
		return errors.Errorf("duplicate extension command registration: %s", name)
	}
	names[name] = struct{}{}
	return nil
}

func validateCommandRegistration(command CommandRegistration) error {
	if normalizeCommandName(command.Name) == "" {
		return errors.New("extension command name is required")
	}
	if strings.TrimSpace(command.Description) == "" {
		return errors.Errorf("extension command %s description is required", normalizeCommandName(command.Name))
	}
	return nil
}

func (r *Runtime) commandNameRegistered(name string) bool {
	name = normalizeCommandName(name)
	if name == "" {
		return false
	}
	for _, command := range r.commands {
		if commandNameMatches(command.Registration.Name, name) {
			return true
		}
		for _, alias := range command.Registration.Aliases {
			if commandNameMatches(alias, name) {
				return true
			}
		}
	}
	return false
}

func (r *Runtime) toolEnabled(name string) bool {
	toolConfig, ok := r.config.Tools[name]
	return !ok || toolConfig.Enabled == nil || *toolConfig.Enabled
}

func (r *Runtime) toolTimeout(registration ToolRegistration) time.Duration {
	if registration.TimeoutInSec != nil {
		return timeoutInSecDuration(registration.TimeoutInSec)
	}
	return 10 * time.Minute
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

// Shortcuts returns effective extension shortcuts sorted by key.
func (r *Runtime) Shortcuts() []Shortcut {
	r.mu.RLock()
	defer r.mu.RUnlock()
	shortcuts := make([]Shortcut, 0, len(r.shortcuts))
	for _, shortcut := range r.shortcuts {
		shortcuts = append(shortcuts, shortcut.Shortcut)
	}
	sort.SliceStable(shortcuts, func(i, j int) bool {
		return strings.Compare(shortcuts[i].Key, shortcuts[j].Key) < 0
	})
	return shortcuts
}

// Subscriptions returns registered extension event subscriptions.
func (r *Runtime) Subscriptions() []Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Subscription(nil), r.subs...)
}

// NotifySurfaceInput routes control-plane surface input to the owning extension generation.
func (r *Runtime) NotifySurfaceInput(ctx context.Context, owner UIExtensionOwner, lifecycle uint64, request UISurfaceInputNotification) error {
	return r.notifySurfaceEvent(ctx, owner, lifecycle, UISurfaceInputMethod, request)
}

// NotifySurfaceResize routes control-plane resize events to the owning extension generation.
func (r *Runtime) NotifySurfaceResize(ctx context.Context, owner UIExtensionOwner, lifecycle uint64, request UISurfaceResizeNotification) error {
	return r.notifySurfaceEvent(ctx, owner, lifecycle, UISurfaceResizeMethod, request)
}

func (r *Runtime) notifySurfaceEvent(ctx context.Context, owner UIExtensionOwner, lifecycle uint64, method string, request any) error {
	if r == nil {
		return errors.New("extension runtime is required")
	}
	r.mu.RLock()
	processes := append([]*Process(nil), r.processes...)
	r.mu.RUnlock()
	for _, process := range processes {
		_, source := process.rpcSession()
		if source == nil || source.ExtensionUIOwner() != owner {
			continue
		}
		return NotifyUISurfaceEvent(ctx, source, lifecycle, method, request)
	}
	return errors.New("extension UI owner is no longer active")
}

// Close terminates all extension processes.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	lifecycleStarted := r.lifecycleStarted
	lifecycleCtx := r.lifecycleCtx
	lifecycleCallCtx := r.lifecycleCallCtx
	r.lifecycleStarted = false
	r.lifecycleCtx = nil
	r.lifecycleCallCtx = ExtensionCallContext{}
	r.mu.Unlock()
	if lifecycleStarted {
		if lifecycleCtx == nil {
			lifecycleCtx = context.Background()
		}
		r.DispatchSessionEnd(lifecycleCtx, lifecycleCallCtx)
	}
	r.mu.Lock()
	var firstErr error
	for _, proc := range r.processes {
		if err := proc.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.processes = nil
	r.mu.Unlock()
	// Process.Close owns process-group teardown. Cancel only after every process
	// has been reaped so exec.CommandContext cannot kill the group leader first.
	if r.cancelRuntime != nil {
		r.cancelRuntime()
	}
	return firstErr
}
