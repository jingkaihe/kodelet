package extensions

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

const maxConcurrentExtensionInitializations = 4

// Attachment supplies one run-scoped callback channel. ID is the wire identity;
// registration and policy use the logical identity "session:" + ID.
type Attachment struct {
	ID        string
	Transport io.ReadWriteCloser
}

// Runtime manages discovered extension processes and registrations.
type Runtime struct {
	config              Config
	workingDir          string
	runtimeCtx          context.Context
	cancelRuntime       context.CancelFunc
	mu                  sync.RWMutex
	processes           []*Process
	tools               map[string]*Tool
	profiles            map[string]Profile
	commands            []Command
	shortcuts           map[string]registeredShortcut
	subs                []Subscription
	eventHandlersByName map[string][]eventHandler
	lifecycleStarted    bool
	lifecycleCtx        context.Context
	lifecycleCallCtx    ExtensionCallContext
	sessionExtensions   bool
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
	Generation  uint64
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
		profiles:            map[string]Profile{},
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
	initialized := make([]struct {
		process *Process
		result  *InitializeResult
	}, len(extensions))
	var group errgroup.Group
	group.SetLimit(maxConcurrentExtensionInitializations)
	for i, ext := range extensions {
		group.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			proc, err := StartProcess(r.runtimeCtx, ext, r.config, r.workingDir)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				logger.G(ctx).WithError(err).WithField("extension", ext.ID).Warn("failed to start extension; disabling for this process")
				return nil
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
				return nil
			}
			initialized[i].process = proc
			initialized[i].result = result
			return ctx.Err()
		})
	}
	err = group.Wait()
	// Own every initialized process before registration, so Close also cleans up
	// later results if cancellation or a registration conflict aborts startup.
	for _, init := range initialized {
		if init.process != nil {
			r.processes = append(r.processes, init.process)
		}
	}
	if err != nil {
		return err
	}
	// Completion order must not change shortcut precedence or event ordering.
	for _, init := range initialized {
		if err := ctx.Err(); err != nil {
			return err
		}
		if init.process != nil {
			if err := r.register(ctx, init.process, init.result); err != nil {
				return err
			}
		}
	}
	return nil
}

// attach runs only during isolated runtime construction, before lifecycle events.
func (r *Runtime) attach(ctx context.Context, attachments []Attachment) error {
	r.sessionExtensions = len(attachments) > 0
	// Explicit session callbacks bypass the installed-extension allowlist.
	deny := newMatcher(r.config.Deny, r.workingDir)
	seen := make(map[string]bool, len(attachments))
	for _, attachment := range attachments {
		ext := Extension{ID: "session:" + attachment.ID, Name: attachment.ID, Kind: SourceKindSession}
		if attachment.ID == "" || strings.ContainsAny(attachment.ID, "/\\:\x00\r\n\t ") || seen[ext.ID] {
			return errors.Errorf("invalid or duplicate session extension id %q", attachment.ID)
		}
		seen[ext.ID] = true
		if !r.config.Enabled || deny.matches(ext) {
			return errors.Errorf("session extension %s is not allowed by extension policy", ext.ID)
		}
	}
	for _, attachment := range attachments {
		ext := Extension{ID: "session:" + attachment.ID, Name: attachment.ID, Kind: SourceKindSession}
		proc, err := AttachProcess(r.runtimeCtx, ext, r.config, r.workingDir, attachment.Transport)
		if err != nil {
			return err
		}
		r.processes = append(r.processes, proc)
		initCtx, cancel := context.WithTimeout(ctx, extensionInitializeTimeout)
		result, err := proc.Initialize(initCtx, r.workingDir)
		cancel()
		if err != nil {
			return errors.Wrapf(err, "failed to initialize session extension %s", ext.ID)
		}
		if err := r.register(ctx, proc, result); err != nil {
			return err
		}
	}
	return nil
}

// HasSessionExtensions reports whether this runtime is pinned to callback channels.
func (r *Runtime) HasSessionExtensions() bool {
	return r != nil && r.sessionExtensions
}

// CloseSessionExtensions ends run-scoped channels even if installed extensions
// retain background leases. Closed callback registrations cannot be reused.
func (r *Runtime) CloseSessionExtensions(ctx context.Context) error {
	if !r.HasSessionExtensions() {
		return nil
	}
	r.mu.RLock()
	started, callContext := r.lifecycleStarted, r.lifecycleCallCtx
	processes := append([]*Process(nil), r.processes...)
	r.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if started {
		for _, handler := range r.eventHandlers(EventSessionEnd) {
			if handler.process.transport != nil {
				_, _ = r.dispatchEventToHandler(ctx, handler, EventSessionEnd, sessionEndPayload{}, callContext)
			}
		}
	}
	var firstErr error
	for _, proc := range processes {
		if proc.transport != nil {
			if err := proc.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
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
	profiles := make(map[string]Profile, len(result.Profiles))
	for _, registration := range result.Profiles {
		profile := Profile{
			Name:        registration.Name,
			ExtensionID: proc.Extension.ID,
			Options:     registration.Options.Clone(),
			Hidden:      registration.Hidden,
		}
		if err := profile.Validate(); err != nil {
			return errors.Wrapf(err, "failed to register extension profile %q", profile.Name)
		}
		if _, exists := r.profiles[profile.Name]; exists {
			return errors.Errorf("duplicate extension profile registration: %s", profile.Name)
		}
		if _, exists := profiles[profile.Name]; exists {
			return errors.Errorf("duplicate extension profile registration: %s", profile.Name)
		}
		profiles[profile.Name] = profile
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
		var generation uint64
		if _, source := proc.rpcSession(); source != nil {
			generation = source.owner.Generation
		}
		r.shortcuts[key] = registeredShortcut{
			Shortcut: Shortcut{
				Key:         key,
				Description: strings.TrimSpace(registration.Description),
				ExtensionID: proc.Extension.ID,
				Generation:  generation,
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
	for name, profile := range profiles {
		r.profiles[name] = profile
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

// ExtensionCount returns the number of initialized extension processes.
func (r *Runtime) ExtensionCount() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.processes)
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

// Profiles returns sorted, independent declarations for daemon acceptance.
// They become remotely selectable only after the daemon accepts the manifest,
// not during extension.initialize, session.start, or resources.discover.
func (r *Runtime) Profiles() []Profile {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	profiles := make([]Profile, 0, len(r.profiles))
	for _, profile := range r.profiles {
		profiles = append(profiles, profile.Clone())
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles
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
	if r == nil {
		return nil
	}
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

// UpdateUICapabilities informs current processes of client availability changes.
// It never initializes or restarts a process, including after a failed generation.
func (r *Runtime) UpdateUICapabilities(ctx context.Context, capabilities ExtensionUIHostCapabilities) error {
	if r == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.RLock()
	processes := append([]*Process(nil), r.processes...)
	r.mu.RUnlock()
	done := make(chan error, 1)
	go func() {
		for _, process := range processes {
			_, source := process.rpcSession()
			if source == nil || !source.current() {
				continue
			}
			if err := source.NotifyExtensionUI(ctx, "kodelet.ui.capabilities", map[string]bool{"widgets": capabilities.Widgets, "surfaces": capabilities.Surfaces, "transcript": capabilities.Transcript}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
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
		// Shutdown is bounded even when an extension opts out of event timeouts.
		// Keep processes alive for session.end, then reap their process groups.
		lifecycleCtx, cancel := context.WithTimeout(lifecycleCtx, 5*time.Second)
		r.DispatchSessionEnd(lifecycleCtx, lifecycleCallCtx)
		cancel()
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
