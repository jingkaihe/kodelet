package client

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"golang.org/x/sync/semaphore"
)

const (
	defaultCleanupTimeout     = 10 * time.Second
	maxToolDisplayOutputBytes = 128 * 1024
)

const toolDisplayOutputTruncationMarker = "\n\n[output truncated for remote display]"

var errNoActiveRun = errors.New("runner has no active run")

// Peer is the symmetric runner connection used for updates and reverse UI calls.
type Peer interface {
	Call(ctx context.Context, method string, params any, result any) error
	Notify(ctx context.Context, method string, params any) error
	NotifyUpdate(method string, params any) error
}

// RuntimeProvider supplies the persistent extension runtime for the bound workspace.
type RuntimeProvider interface {
	RuntimeWithConfigAndCallContext(ctx context.Context, cwd, variant string, config extensions.Config, callContext extensions.ExtensionCallContext) (*extensions.Runtime, error)
}

type runtimeLeaseProvider interface {
	RuntimeWithConfigAndCallContextForLease(ctx, leaseCtx context.Context, cwd, variant string, config extensions.Config, callContext extensions.ExtensionCallContext) (*extensions.Runtime, error)
}

type isolatedRuntimeLeaseProvider interface {
	RuntimeWithConfigAndCallContextForIsolatedLease(ctx, leaseCtx context.Context, cwd, variant string, config extensions.Config, callContext extensions.ExtensionCallContext) (*extensions.Runtime, func() error, error)
}

type runtimeDiscoveryProvider interface {
	RuntimeForCommandDiscoveryWithConfig(ctx context.Context, cwd, variant string, config extensions.Config) (*extensions.Runtime, error)
}

// ConfigLoader loads runner-owned configuration for an optional conversation profile.
type ConfigLoader func(profile string) (llmtypes.Config, error)

// EnvironmentFactory creates one agent environment inside a provisioned execution instance.
type EnvironmentFactory func(workingDirectory string, runtime *extensions.Runtime) agentenv.Environment

// ServiceOptions configures the runner-side request service.
type ServiceOptions struct {
	RuntimeProvider           RuntimeProvider
	ConfigLoader              ConfigLoader
	EnvironmentFactory        EnvironmentFactory
	ExecutionInstanceProvider ExecutionInstanceProvider
	CleanupTimeout            time.Duration
	// SnapshotWaitTimeout optionally bounds admission to the serialized snapshot
	// pipeline. Zero lets the caller's context own the deadline.
	SnapshotWaitTimeout time.Duration
}

// Service handles control-plane requests for one workspace-bound runner process.
type Service struct {
	ctx                 context.Context
	mu                  sync.Mutex
	snapshotGate        *semaphore.Weighted
	workspace           string
	runtimeProvider     RuntimeProvider
	ownedRuntime        *extensions.RuntimeManager
	configLoader        ConfigLoader
	environmentFactory  EnvironmentFactory
	instanceProvider    ExecutionInstanceProvider
	workspaceTerminals  *workspaceTerminalManager
	cleanupTimeout      time.Duration
	snapshotWaitTimeout time.Duration
	peer                Peer
	runnerID            string
	generation          int64
	runs                map[string]*activeRun
	lastManifestDigest  string
	closed              bool
	closeMu             sync.Mutex
	closeOnce           sync.Once
	closeErr            error
}

type activeRun struct {
	id                 string
	conversationID     string
	clientCaps         protocol.ClientCapabilities
	config             llmtypes.Config
	runtime            *extensions.Runtime
	runtimeRelease     func() error
	runtimeReleaseOnce sync.Once
	runtimeReleaseErr  error
	instance           ExecutionInstance
	environment        agentenv.Environment
	manifest           runnerpayload.Manifest
	ctx                context.Context
	cancel             context.CancelFunc
	runtimeLeaseCancel context.CancelFunc
	updates            atomic.Uint64
	ops                sync.WaitGroup
	opening            bool
	closing            bool
	stopping           bool
	cleanupOnce        sync.Once
	cleanupErr         error
}

// NewService creates a runner-side request handler bound to one canonical workspace.
func NewService(parent context.Context, workspace string, options ServiceOptions) (*Service, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("runner workspace is required")
	}
	workspace = osutil.CanonicalizePath(workspace)
	if parent == nil {
		parent = context.Background()
	}
	service := &Service{
		ctx:                 parent,
		snapshotGate:        semaphore.NewWeighted(1),
		workspace:           workspace,
		runtimeProvider:     options.RuntimeProvider,
		configLoader:        options.ConfigLoader,
		environmentFactory:  options.EnvironmentFactory,
		instanceProvider:    options.ExecutionInstanceProvider,
		cleanupTimeout:      options.CleanupTimeout,
		snapshotWaitTimeout: options.SnapshotWaitTimeout,
		runs:                make(map[string]*activeRun),
	}
	if service.cleanupTimeout <= 0 {
		service.cleanupTimeout = defaultCleanupTimeout
	}
	if service.runtimeProvider == nil {
		service.ownedRuntime = extensions.NewRuntimeManager()
		service.runtimeProvider = service.ownedRuntime
	}
	if service.configLoader == nil {
		service.configLoader = loadRunnerConfig
	}
	if service.environmentFactory == nil {
		service.environmentFactory = func(workingDirectory string, runtime *extensions.Runtime) agentenv.Environment {
			return agentenv.NewLocalEnvironment(workingDirectory, runtime)
		}
	}
	if service.instanceProvider == nil {
		provider, err := NewDirectWorkspaceInstanceProvider(workspace)
		if err != nil {
			return nil, err
		}
		service.instanceProvider = provider
	}
	service.workspaceTerminals = newWorkspaceTerminalManager(service.ctx, service.workspace)
	return service, nil
}

func loadRunnerConfig(profile string) (llmtypes.Config, error) {
	return llm.GetConfigFromViperWithEnvironmentProfile(profile)
}

// Attach installs the current symmetric connection used by runner-originated calls.
func (s *Service) Attach(peer Peer) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.peer = peer
	s.mu.Unlock()
}

// SetRegistration applies the stable ID and generation returned by runner.register.
func (s *Service) SetRegistration(result protocol.RegisterResult) error {
	if s == nil {
		return errors.New("runner service is required")
	}
	if strings.TrimSpace(result.RunnerID) == "" || result.Generation <= 0 {
		return errors.New("runner registration identity is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) > 0 {
		return errors.New("cannot replace runner registration during active runs")
	}
	s.runnerID = strings.TrimSpace(result.RunnerID)
	s.generation = result.Generation
	return nil
}

// HandleRequest implements protocol.RequestHandler for control-plane requests.
func (s *Service) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, *protocol.RPCError) {
	if s == nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: "runner service is unavailable"}
	}
	switch method {
	case protocol.MethodRunOpen:
		value, rpcErr := decodeParams[protocol.RunOpenParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.openRun(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodRunClose:
		value, rpcErr := decodeParams[protocol.RunCloseParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return rpcResult(nil, s.closeRun(ctx, value.RunID))
	case protocol.MethodRunCancel:
		value, rpcErr := decodeParams[protocol.RunCancelParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return rpcResult(nil, s.cancelRun(value.RunID))
	case protocol.MethodCommandExecute:
		value, rpcErr := decodeParams[runnerpayload.CommandExecuteParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.executeCommand(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodLifecycleDispatch:
		value, rpcErr := decodeParams[runnerpayload.LifecycleDispatchParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.dispatchLifecycle(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodToolExecute:
		value, rpcErr := decodeParams[runnerpayload.ToolExecuteParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.executeTool(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodWorkspaceGitDiff:
		if _, rpcErr := decodeParams[protocol.WorkspaceGitDiffParams](params); rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.workspaceGitDiff(ctx)
		return rpcResult(result, err)
	case protocol.MethodWorkspaceTerminalOpen:
		value, rpcErr := decodeParams[protocol.WorkspaceTerminalOpenParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.openWorkspaceTerminal(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodWorkspaceTerminalRead:
		value, rpcErr := decodeParams[protocol.WorkspaceTerminalReadParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.readWorkspaceTerminal(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodWorkspaceTerminalInput:
		value, rpcErr := decodeParams[protocol.WorkspaceTerminalInputParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return rpcResult(nil, s.writeWorkspaceTerminal(ctx, value))
	case protocol.MethodWorkspaceTerminalResize:
		value, rpcErr := decodeParams[protocol.WorkspaceTerminalResizeParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return rpcResult(nil, s.resizeWorkspaceTerminal(value))
	default:
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeMethodNotFound, Message: "runner request method not found"}
	}
}

// HandleNotification implements protocol.NotificationHandler for client UI events.
func (s *Service) HandleNotification(ctx context.Context, method string, params json.RawMessage) {
	var err error
	var runID string
	switch method {
	case protocol.MethodUISurfaceInput:
		var value runnerpayload.UISurfaceInputParams
		if json.Unmarshal(params, &value) == nil {
			runID = value.RunID
			err = s.notifySurfaceInput(ctx, value)
		}
	case protocol.MethodUISurfaceResize:
		var value runnerpayload.UISurfaceResizeParams
		if json.Unmarshal(params, &value) == nil {
			runID = value.RunID
			err = s.notifySurfaceResize(ctx, value)
		}
	}
	if err != nil {
		s.reportEnvironmentError(runID, err)
	}
}

func (s *Service) openRun(ctx context.Context, params protocol.RunOpenParams) (runnerpayload.Manifest, error) {
	if err := params.Validate(); err != nil {
		return runnerpayload.Manifest{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("runner service is closed")
	}
	if s.runnerID == "" || s.generation <= 0 {
		s.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("runner has not completed registration")
	}
	if _, exists := s.runs[params.RunID]; exists {
		s.mu.Unlock()
		return runnerpayload.Manifest{}, errors.Errorf("runner already has run %s", params.RunID)
	}
	for _, active := range s.runs {
		if active.conversationID == params.ConversationID {
			s.mu.Unlock()
			return runnerpayload.Manifest{}, errors.Errorf("conversation already has active run %s", active.id)
		}
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	runtimeLeaseCtx, runtimeLeaseCancel := context.WithCancel(s.ctx)
	run := &activeRun{
		id:                 params.RunID,
		conversationID:     params.ConversationID,
		clientCaps:         params.ClientCapabilities,
		ctx:                runCtx,
		cancel:             cancel,
		runtimeLeaseCancel: runtimeLeaseCancel,
		opening:            true,
	}
	run.ops.Add(1)
	defer run.ops.Done()
	s.runs[run.id] = run
	runnerID := s.runnerID
	generation := s.generation
	s.mu.Unlock()

	operationCtx, finishOperation := runOperationContext(ctx, run)
	defer finishOperation()
	operationCtx = s.decorateRunContext(operationCtx, run.id, run.conversationID)
	if err := s.lockSnapshot(operationCtx); err != nil {
		s.failOpen(run)
		return runnerpayload.Manifest{}, err
	}
	defer s.unlockSnapshot()
	instance, err := s.instanceProvider.Create(operationCtx, ExecutionInstanceSpec{
		RunID:          params.RunID,
		ConversationID: params.ConversationID,
	})
	if err != nil {
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to create runner execution instance")
	}
	if instance == nil {
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.New("runner execution instance provider returned nil")
	}
	workingDirectory := strings.TrimSpace(instance.WorkingDirectory())
	if workingDirectory == "" {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.New("runner execution instance returned an empty working directory")
	}

	config, err := s.configLoader(params.Agent.EnvironmentProfile)
	if err != nil {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to load runner configuration")
	}
	config.WorkingDirectory = workingDirectory
	config.Provider = params.Agent.Provider
	config.Model = params.Agent.Model
	config.Profile = params.Agent.Profile
	config.RecipeName = params.Agent.RecipeName
	extensionConfig, err := extensions.LoadConfigFromSettings(config.ExtensionSettings)
	if err != nil {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to load runner extension configuration")
	}

	callContext := extensions.ExtensionCallContext{
		ConversationID: params.ConversationID,
		UIScopeID:      params.ConversationID,
		CWD:            workingDirectory,
		Provider:       config.Provider,
		Model:          config.Model,
		Profile:        config.Profile,
		RecipeName:     config.RecipeName,
		InvokedBy:      firstNonEmpty(params.Agent.InvokedBy, "main"),
	}
	variant := normalizeEnvironmentProfile(params.Agent.EnvironmentProfile)
	var runtime *extensions.Runtime
	var runtimeRelease func() error
	if provider, ok := s.runtimeProvider.(isolatedRuntimeLeaseProvider); ok {
		runtime, runtimeRelease, err = provider.RuntimeWithConfigAndCallContextForIsolatedLease(operationCtx, runtimeLeaseCtx, workingDirectory, variant, extensionConfig, callContext)
	} else if provider, ok := s.runtimeProvider.(runtimeLeaseProvider); ok {
		runtime, err = provider.RuntimeWithConfigAndCallContextForLease(operationCtx, runtimeLeaseCtx, workingDirectory, variant, extensionConfig, callContext)
	} else {
		runtime, err = s.runtimeProvider.RuntimeWithConfigAndCallContext(operationCtx, workingDirectory, variant, extensionConfig, callContext)
	}
	if err != nil {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to initialize runner extensions")
	}
	run.runtimeRelease = runtimeRelease
	config.Extensions = runtime
	environment := s.environmentFactory(workingDirectory, runtime)
	if environment == nil {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.New("runner environment factory returned nil")
	}
	spec := agentenv.RunSpec{
		ConversationID: params.ConversationID,
		Config:         config,
		InvokedBy:      callContext.InvokedBy,
	}
	localManifest, err := environment.Open(operationCtx, spec)
	if err != nil {
		s.closeOpeningResources(operationCtx, environment, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to open runner environment")
	}
	wireManifest, err := buildWireManifest(localManifest, config, runtime, runnerID, params.RunID, generation, params.ReservedToolNames)
	if err == nil {
		err = operationCtx.Err()
	}
	if err != nil {
		s.closeOpeningResources(operationCtx, environment, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, err
	}

	s.mu.Lock()
	if s.runs[run.id] != run || run.closing {
		s.mu.Unlock()
		s.closeOpeningResources(operationCtx, environment, instance)
		s.failOpen(run)
		return runnerpayload.Manifest{}, errors.New("runner run was canceled while opening")
	}
	run.config = config
	run.runtime = runtime
	run.instance = instance
	run.environment = environment
	run.manifest = wireManifest
	run.opening = false
	s.lastManifestDigest = wireManifest.Digest
	s.mu.Unlock()
	return wireManifest, nil
}

func (s *Service) closeOpeningResources(ctx context.Context, environment agentenv.Environment, instance ExecutionInstance) {
	if err := closeExecutionResources(ctx, s.cleanupTimeout, environment, instance); err != nil {
		logger.G(ctx).WithError(err).Warn("failed to clean up runner execution instance after open failure")
	}
}

func (s *Service) closeProbeResources(ctx context.Context, environment agentenv.Environment, instance ExecutionInstance, cause error) error {
	cleanupErr := closeExecutionResources(ctx, s.cleanupTimeout, environment, instance)
	switch {
	case cause == nil && cleanupErr == nil:
		return nil
	case cause == nil:
		return errors.Wrap(cleanupErr, "failed to clean up runner manifest probe resources")
	case cleanupErr == nil:
		return cause
	default:
		return errors.Wrapf(cause, "runner manifest probe cleanup also failed: %v", cleanupErr)
	}
}

func (s *Service) failOpen(run *activeRun) {
	if run == nil {
		return
	}
	run.cancel()
	if err := s.releaseRunRuntime(context.Background(), run); err != nil {
		logger.G(s.ctx).WithError(err).Warn("failed to close runner extension runtime after open failure")
	}
	s.mu.Lock()
	if s.runs[run.id] == run {
		delete(s.runs, run.id)
	}
	s.mu.Unlock()
}

func (s *Service) cancelRun(runID string) error {
	s.mu.Lock()
	run, err := s.activeRunLocked(runID)
	if err == nil {
		run.stopping = true
		run.cancel()
	}
	s.mu.Unlock()
	return err
}

func (s *Service) closeRun(ctx context.Context, runID string) error {
	s.mu.Lock()
	run, err := s.activeRunLocked(runID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return s.closeActiveRun(ctx, run)
}

func (s *Service) closeActiveRun(ctx context.Context, run *activeRun) error {
	if run == nil {
		return nil
	}
	run.cleanupOnce.Do(func() {
		s.mu.Lock()
		run.closing = true
		run.stopping = true
		run.cancel()
		s.mu.Unlock()

		waitErr := waitForRunOperations(ctx, s.cleanupTimeout, run)
		closeErr := closeExecutionResources(ctx, s.cleanupTimeout, run.environment, run.instance)
		run.cleanupErr = combineCleanupErrors(waitErr, closeErr)
		run.cleanupErr = combineCleanupErrors(run.cleanupErr, s.releaseRunRuntime(ctx, run))
		s.mu.Lock()
		if s.runs[run.id] == run {
			delete(s.runs, run.id)
		}
		s.mu.Unlock()
	})
	return run.cleanupErr
}

func (s *Service) releaseRunRuntime(ctx context.Context, run *activeRun) error {
	if run == nil {
		return nil
	}
	var err error
	if run.runtimeRelease != nil {
		err = runBoundedCleanup(ctx, s.cleanupTimeout, "runner extension runtime", func(context.Context) error {
			run.runtimeReleaseOnce.Do(func() {
				run.runtimeReleaseErr = run.runtimeRelease()
			})
			return run.runtimeReleaseErr
		})
	}
	run.runtimeLeaseCancel()
	return err
}

// ProbeManifestDigest snapshots idle runner resources without reserving a control-plane run.
func (s *Service) ProbeManifestDigest(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("runner service is required")
	}
	if s.snapshotGate.TryAcquire(1) {
		defer s.unlockSnapshot()
		manifest, err := s.probeManifestLocked(ctx, "")
		if err != nil {
			return "", err
		}
		return manifest.Digest, nil
	}
	s.mu.Lock()
	cachedDigest := s.lastManifestDigest
	s.mu.Unlock()
	if cachedDigest != "" {
		return cachedDigest, nil
	}
	if err := s.lockSnapshot(ctx); err != nil {
		return "", err
	}
	defer s.unlockSnapshot()
	manifest, err := s.probeManifestLocked(ctx, "")
	if err != nil {
		return "", err
	}
	return manifest.Digest, nil
}

// ProbeManifest snapshots runner resources for command and capability discovery without reserving a control-plane run.
func (s *Service) ProbeManifest(ctx context.Context, environmentProfile string) (runnerpayload.Manifest, error) {
	if s == nil {
		return runnerpayload.Manifest{}, errors.New("runner service is required")
	}
	if err := s.lockSnapshot(ctx); err != nil {
		return runnerpayload.Manifest{}, err
	}
	defer s.unlockSnapshot()
	return s.probeManifestLocked(ctx, environmentProfile)
}

func (s *Service) probeManifestLocked(ctx context.Context, environmentProfile string) (runnerpayload.Manifest, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("runner service is closed")
	}
	runnerID := s.runnerID
	generation := s.generation
	s.mu.Unlock()
	instance, err := s.instanceProvider.Create(ctx, ExecutionInstanceSpec{
		RunID:          "runner-manifest-probe",
		ConversationID: "runner-manifest-probe",
		Probe:          true,
	})
	if err != nil {
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to create runner manifest probe instance")
	}
	if instance == nil {
		return runnerpayload.Manifest{}, errors.New("runner manifest probe instance provider returned nil")
	}
	workingDirectory := strings.TrimSpace(instance.WorkingDirectory())
	if workingDirectory == "" {
		return runnerpayload.Manifest{}, s.closeProbeResources(ctx, nil, instance, errors.New("runner manifest probe instance returned an empty working directory"))
	}

	config, err := s.configLoader(environmentProfile)
	if err != nil {
		return runnerpayload.Manifest{}, s.closeProbeResources(ctx, nil, instance, errors.Wrap(err, "failed to load runner configuration"))
	}
	config.WorkingDirectory = workingDirectory
	extensionConfig, err := extensions.LoadConfigFromSettings(config.ExtensionSettings)
	if err != nil {
		return runnerpayload.Manifest{}, s.closeProbeResources(ctx, nil, instance, errors.Wrap(err, "failed to load runner extension configuration"))
	}
	probeCtx := s.decorateRunContext(ctx, "runner-manifest-probe", "runner-manifest-probe")
	variant := normalizeEnvironmentProfile(environmentProfile)
	var runtime *extensions.Runtime
	if provider, ok := s.runtimeProvider.(runtimeDiscoveryProvider); ok {
		runtime, err = provider.RuntimeForCommandDiscoveryWithConfig(probeCtx, workingDirectory, variant, extensionConfig)
	} else {
		runtime, err = s.runtimeProvider.RuntimeWithConfigAndCallContext(probeCtx, workingDirectory, variant, extensionConfig, extensions.ExtensionCallContext{
			ConversationID: "runner-manifest-probe",
			UIScopeID:      "runner-manifest-probe",
			CWD:            workingDirectory,
			Provider:       config.Provider,
			Model:          config.Model,
			Profile:        config.Profile,
			RecipeName:     config.RecipeName,
			InvokedBy:      "runner.manifest",
		})
	}
	if err != nil {
		return runnerpayload.Manifest{}, s.closeProbeResources(probeCtx, nil, instance, errors.Wrap(err, "failed to initialize runner extensions"))
	}
	config.Extensions = runtime
	environment := s.environmentFactory(workingDirectory, runtime)
	if environment == nil {
		return runnerpayload.Manifest{}, s.closeProbeResources(probeCtx, nil, instance, errors.New("runner environment factory returned nil"))
	}
	manifest, err := environment.Open(probeCtx, agentenv.RunSpec{
		ConversationID: "runner-manifest-probe",
		Config:         config,
		InvokedBy:      "runner.manifest",
	})
	if err != nil {
		return runnerpayload.Manifest{}, s.closeProbeResources(probeCtx, environment, instance, errors.Wrap(err, "failed to probe runner environment"))
	}
	wire, err := buildWireManifest(manifest, config, runtime, runnerID, "runner-manifest-probe", generation, tools.ControlPlaneToolNames())
	if err != nil {
		return runnerpayload.Manifest{}, s.closeProbeResources(probeCtx, environment, instance, err)
	}
	if err := s.closeProbeResources(probeCtx, environment, instance, nil); err != nil {
		return runnerpayload.Manifest{}, err
	}
	s.mu.Lock()
	s.lastManifestDigest = wire.Digest
	s.mu.Unlock()
	return wire, nil
}

// HeartbeatSnapshot returns the legacy singular-run heartbeat shape.
func (s *Service) HeartbeatSnapshot() (protocol.RunnerState, string, string) {
	state, runIDs, digest := s.HeartbeatSnapshotRuns()
	if len(runIDs) == 1 {
		return state, runIDs[0], digest
	}
	return state, "", digest
}

// HeartbeatSnapshotRuns returns current application health and all active run IDs.
func (s *Service) HeartbeatSnapshotRuns() (protocol.RunnerState, []string, string) {
	if s == nil {
		return protocol.RunnerStateError, nil, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) == 0 {
		return protocol.RunnerStateIdle, nil, s.lastManifestDigest
	}
	state := protocol.RunnerStateStopping
	for _, run := range s.runs {
		if !run.stopping && !run.closing {
			state = protocol.RunnerStateRunning
			break
		}
	}
	return state, activeRunIDs(s.runs), s.lastManifestDigest
}

func activeRunIDs(runs map[string]*activeRun) []string {
	result := make([]string, 0, len(runs))
	for runID := range runs {
		result = append(result, runID)
	}
	sort.Strings(result)
	return result
}

func (s *Service) executeCommand(ctx context.Context, params runnerpayload.CommandExecuteParams) (runnerpayload.CommandExecuteResult, error) {
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return runnerpayload.CommandExecuteResult{}, err
	}
	defer finish()
	s.mu.Lock()
	runConfig := run.config
	s.mu.Unlock()
	result, err := run.environment.ExecuteCommand(operationCtx, agentenv.CommandRequest{
		Message: params.Message,
		RunSpec: agentenv.RunSpec{
			ConversationID: run.conversationID,
			Config:         runConfig,
			InvokedBy:      "main",
		},
	})
	if err != nil {
		return runnerpayload.CommandExecuteResult{}, err
	}
	if applier, ok := run.environment.(interface{ ApplyCommandResult(agentenv.CommandResult) }); ok {
		applier.ApplyCommandResult(result)
	}
	s.mu.Lock()
	if strings.TrimSpace(result.RecipeName) != "" {
		run.config.RecipeName = result.RecipeName
	}
	if len(result.AllowedTools) > 0 {
		run.config.AllowedTools = append([]string(nil), result.AllowedTools...)
	}
	if len(result.AllowedCommands) > 0 {
		run.config.AllowedCommands = append([]string(nil), result.AllowedCommands...)
	}
	s.mu.Unlock()
	return runnerpayload.CommandExecuteResult{
		Matched:         result.Matched,
		Action:          string(result.Action),
		CommandName:     result.CommandName,
		Response:        result.Response,
		Prompt:          result.Prompt,
		Display:         result.Display,
		DisplayOverride: result.DisplayOverride,
		RecipeName:      result.RecipeName,
		AllowedTools:    append([]string(nil), result.AllowedTools...),
		AllowedCommands: append([]string(nil), result.AllowedCommands...),
	}, nil
}

func (s *Service) dispatchLifecycle(ctx context.Context, params runnerpayload.LifecycleDispatchParams) (runnerpayload.LifecycleDispatchResult, error) {
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return runnerpayload.LifecycleDispatchResult{}, err
	}
	defer finish()

	switch params.Event {
	case runnerpayload.LifecycleUserMessage:
		message, err := run.environment.ProcessUserMessage(operationCtx, params.Message)
		return runnerpayload.LifecycleDispatchResult{Message: message}, err
	case runnerpayload.LifecycleAgentStart:
		return runnerpayload.LifecycleDispatchResult{}, run.environment.DispatchAgentStart(operationCtx)
	case runnerpayload.LifecycleTurnStart:
		return runnerpayload.LifecycleDispatchResult{}, run.environment.DispatchTurnStart(operationCtx, params.TurnNumber)
	case runnerpayload.LifecycleAgentInit:
		decision, err := run.environment.ProcessAgentInit(operationCtx, params.SystemPrompt, params.AllowedTools)
		return runnerpayload.LifecycleDispatchResult{
			SystemPrompt:  decision.SystemPrompt,
			AllowedTools:  append([]string(nil), decision.AllowedTools...),
			ToolsModified: decision.ToolsModified,
		}, err
	case runnerpayload.LifecycleTurnEnd:
		return runnerpayload.LifecycleDispatchResult{}, run.environment.DispatchTurnEnd(operationCtx, params.FinalOutput, params.TurnCount)
	case runnerpayload.LifecycleAgentEnd:
		messages, err := run.environment.DispatchAgentEnd(operationCtx, params.Messages)
		return runnerpayload.LifecycleDispatchResult{FollowUpMessages: messages}, err
	case runnerpayload.LifecycleToolCall:
		decision, err := run.environment.DispatchToolCall(operationCtx, agentenv.ToolRequest{
			Name:       params.ToolName,
			Input:      string(params.ToolInput),
			ToolCallID: params.ToolCallID,
		})
		input, encodeErr := rawJSON(decision.Input)
		if err == nil {
			err = encodeErr
		}
		return runnerpayload.LifecycleDispatchResult{
			Blocked:   decision.Blocked,
			Reason:    decision.Reason,
			ToolInput: input,
		}, err
	case runnerpayload.LifecycleToolUpdate, runnerpayload.LifecycleToolResult:
		if params.StructuredResult == nil {
			return runnerpayload.LifecycleDispatchResult{}, errors.New("structuredResult is required for tool output lifecycle")
		}
		request := agentenv.ToolOutputRequest{
			Name:             params.ToolName,
			Input:            string(params.ToolInput),
			ToolCallID:       params.ToolCallID,
			StructuredResult: *params.StructuredResult,
		}
		var decision agentenv.ToolOutputDecision
		if params.Event == runnerpayload.LifecycleToolUpdate {
			decision, err = run.environment.DispatchToolUpdate(operationCtx, request)
		} else {
			decision, err = run.environment.DispatchToolResult(operationCtx, request)
		}
		structured := decision.StructuredResult
		return runnerpayload.LifecycleDispatchResult{
			StructuredResult: &structured,
			Modified:         decision.Modified,
			Accepted:         decision.Accepted,
		}, err
	default:
		return runnerpayload.LifecycleDispatchResult{}, errors.Errorf("unsupported lifecycle event %q", params.Event)
	}
}

func (s *Service) executeTool(ctx context.Context, params runnerpayload.ToolExecuteParams) (runnerpayload.ToolExecuteResult, error) {
	if strings.TrimSpace(params.ToolCallID) == "" || strings.TrimSpace(params.Name) == "" {
		return runnerpayload.ToolExecuteResult{}, errors.New("toolCallId and name are required")
	}
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return runnerpayload.ToolExecuteResult{}, err
	}
	defer finish()

	peer := s.currentPeer()
	toolContext := tools.ToolContextFromThreadState(run.config, run.conversationID, run.manifest.WorkingDirectory, nil)
	if peer != nil {
		toolContext.MetadataStore = &controlPlaneConversationForker{peer: peer, runID: run.id, toolCallID: params.ToolCallID}
	}
	operationCtx = tools.ContextWithToolContext(operationCtx, toolContext)
	var updateSink agentenv.ToolUpdateSink
	if params.WantUpdates {
		requestID := protocol.RequestIDFromContext(operationCtx)
		if requestID == "" {
			return runnerpayload.ToolExecuteResult{}, errors.New("tool.execute request id is unavailable")
		}
		updateSink = func(update agentenv.ToolUpdate) {
			if peer == nil || update.Result == nil {
				return
			}
			_ = peer.NotifyUpdate(protocol.MethodToolUpdate, runnerpayload.ToolUpdateParams{
				RunID:      run.id,
				RequestID:  requestID,
				ToolCallID: params.ToolCallID,
				Sequence:   run.updates.Add(1),
				Result:     serializeToolResult(update.Result, update.StructuredResult),
				Modified:   update.Modified,
			})
		}
	}
	execution, err := run.environment.ExecuteTool(operationCtx, agentenv.ToolRequest{
		Name:       params.Name,
		Input:      string(params.Input),
		ToolCallID: params.ToolCallID,
	}, updateSink)
	if err != nil {
		return runnerpayload.ToolExecuteResult{}, err
	}
	input, err := rawJSON(execution.Input)
	if err != nil {
		return runnerpayload.ToolExecuteResult{}, err
	}
	return runnerpayload.ToolExecuteResult{
		Input:    input,
		Result:   serializeToolResult(execution.Result, execution.StructuredResult),
		Modified: execution.Modified,
	}, nil
}

type controlPlaneConversationForker struct {
	peer       Peer
	runID      string
	toolCallID string
}

func (*controlPlaneConversationForker) GetMetadata() map[string]any { return nil }

func (*controlPlaneConversationForker) SetMetadataValue(string, any) {}

func (f *controlPlaneConversationForker) ForkConversation(ctx context.Context) (string, error) {
	if f == nil || f.peer == nil {
		return "", llmtypes.ErrConversationForkUnavailable
	}
	params := runnerpayload.ConversationForkParams{RunID: f.runID, ToolCallID: f.toolCallID}
	var result runnerpayload.ConversationForkResult
	if err := f.peer.Call(ctx, protocol.MethodConversationFork, params, &result); err != nil {
		var rpcErr *protocol.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == protocol.ErrorCodeUnavailable {
			return "", llmtypes.ErrConversationForkUnavailable
		}
		return "", err
	}
	result.ConversationID = strings.TrimSpace(result.ConversationID)
	if result.ConversationID == "" {
		return "", errors.New("control plane returned an empty conversation fork ID")
	}
	return result.ConversationID, nil
}

func serializeToolResult(result tooltypes.ToolResult, structured tooltypes.StructuredToolResult) runnerpayload.ToolResult {
	if result == nil {
		result = tooltypes.BaseToolResult{Error: "runner environment returned no tool result"}
	}
	if structured.ToolName == "" {
		structured = result.StructuredData()
	}
	errorMessage := result.GetError()
	if errorMessage == "" {
		errorMessage = structured.Error
	}
	wire := runnerpayload.ToolResult{
		AssistantFacing: result.AssistantFacing(),
		DisplayOutput:   serializeToolDisplayOutput(result, structured.ToolName),
		Error:           errorMessage,
		Structured:      structured,
	}
	if multimodal, ok := result.(tooltypes.MultiModalToolResult); ok {
		wire.ContentParts = append([]tooltypes.ToolResultContentPart(nil), multimodal.ContentParts()...)
	}
	return wire
}

func serializeToolDisplayOutput(result tooltypes.ToolResult, toolName string) string {
	// Bash can spill arbitrarily large output to disk. Its structured metadata
	// already carries the bounded display snapshot, so avoid reading the full file.
	if toolName == "bash" {
		return ""
	}
	output := result.GetResult()
	if len(output) <= maxToolDisplayOutputBytes {
		return output
	}
	limit := maxToolDisplayOutputBytes - len(toolDisplayOutputTruncationMarker)
	for limit > 0 && !utf8.RuneStart(output[limit]) {
		limit--
	}
	return output[:limit] + toolDisplayOutputTruncationMarker
}

func (s *Service) beginRunOperation(ctx context.Context, runID string) (*activeRun, context.Context, func(), error) {
	s.mu.Lock()
	run, err := s.activeRunLocked(runID)
	if err != nil {
		s.mu.Unlock()
		return nil, nil, nil, err
	}
	if run.opening || run.closing || run.environment == nil {
		s.mu.Unlock()
		return nil, nil, nil, errors.New("runner environment is not ready")
	}
	run.ops.Add(1)
	s.mu.Unlock()

	operationCtx, cancel := runOperationContext(ctx, run)
	operationCtx = s.decorateRunContext(operationCtx, run.id, run.conversationID)
	finish := func() {
		cancel()
		run.ops.Done()
	}
	return run, operationCtx, finish, nil
}

func runOperationContext(ctx context.Context, run *activeRun) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(run.ctx, cancel)
	return operationCtx, func() {
		stop()
		cancel()
	}
}

func (s *Service) decorateRunContext(ctx context.Context, runID, conversationID string) context.Context {
	ctx = extensions.ContextWithUIInputBroker(ctx, s)
	ctx = extensions.ContextWithExtensionUIHost(ctx, s)
	ctx = extensions.ContextWithExtensionUIScope(ctx, conversationID)
	return context.WithValue(ctx, runnerRunIDContextKey{}, strings.TrimSpace(runID))
}

func (s *Service) activeRunLocked(runID string) (*activeRun, error) {
	if len(s.runs) == 0 {
		return nil, errNoActiveRun
	}
	runID = strings.TrimSpace(runID)
	run := s.runs[runID]
	if runID == "" || run == nil {
		return nil, errors.Wrap(errNoActiveRun, "request belongs to another runner run")
	}
	return run, nil
}

func (s *Service) currentPeer() Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peer
}

func (s *Service) reportEnvironmentError(runID string, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	peer := s.peer
	run := s.runs[strings.TrimSpace(runID)]
	s.mu.Unlock()
	if peer != nil && run != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = peer.Notify(ctx, protocol.MethodRunEnvironmentError, protocol.EnvironmentErrorParams{RunID: run.id, Message: err.Error()})
	}
}

// AbortActiveRun releases all local run resources after a connection loss.
func (s *Service) AbortActiveRun(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if len(s.runs) == 0 {
		s.peer = nil
		s.mu.Unlock()
		return nil
	}
	runs := make([]*activeRun, 0, len(s.runs))
	for _, run := range s.runs {
		runs = append(runs, run)
	}
	s.peer = nil
	s.mu.Unlock()

	errorsByRun := make(chan error, len(runs))
	var cleanup sync.WaitGroup
	cleanup.Add(len(runs))
	for _, run := range runs {
		go func() {
			defer cleanup.Done()
			if err := s.closeActiveRun(ctx, run); err != nil {
				errorsByRun <- err
			}
		}()
	}
	cleanup.Wait()
	close(errorsByRun)
	var firstErr error
	for err := range errorsByRun {
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) lockSnapshot(ctx context.Context) error {
	if s == nil || s.snapshotGate == nil {
		return errors.New("runner snapshot gate is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx := ctx
	cancel := func() {}
	if s.snapshotWaitTimeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, s.snapshotWaitTimeout)
	}
	defer cancel()
	if err := s.snapshotGate.Acquire(waitCtx, 1); err != nil {
		return errors.Wrap(err, "failed to wait for runner environment snapshot")
	}
	return nil
}

func (s *Service) unlockSnapshot() {
	if s != nil && s.snapshotGate != nil {
		s.snapshotGate.Release(1)
	}
}

// Close releases an active environment and any runtime manager owned by the service.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		activeErr := s.AbortActiveRun(context.Background())
		if s.ownedRuntime != nil {
			if err := s.ownedRuntime.Close(); activeErr == nil {
				activeErr = err
			}
		}
		if s.workspaceTerminals != nil {
			activeErr = combineCleanupErrors(activeErr, s.workspaceTerminals.Close())
		}
		s.mu.Lock()
		s.closeErr = activeErr
		s.mu.Unlock()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

func waitForRunOperations(ctx context.Context, timeout time.Duration, run *activeRun) error {
	if run == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		run.ops.Wait()
		close(done)
	}()
	waitCtx, cancel := cleanupContext(ctx, timeout)
	defer cancel()
	select {
	case <-done:
		return nil
	case <-waitCtx.Done():
		return errors.Wrap(waitCtx.Err(), "timed out waiting for runner operations to stop")
	}
}

func closeExecutionResources(ctx context.Context, timeout time.Duration, environment agentenv.Environment, instance ExecutionInstance) error {
	var firstErr error
	if environment != nil {
		firstErr = runBoundedCleanup(ctx, timeout, "runner environment", environment.Close)
	}
	if instance != nil {
		if err := runBoundedCleanup(ctx, timeout, "runner execution instance", instance.Close); firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func runBoundedCleanup(ctx context.Context, timeout time.Duration, resource string, closeFunc func(context.Context) error) error {
	cleanupCtx, cancel := cleanupContext(ctx, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- closeFunc(cleanupCtx)
	}()
	select {
	case err := <-done:
		return err
	case <-cleanupCtx.Done():
		return errors.Wrapf(cleanupCtx.Err(), "timed out closing %s", resource)
	}
}

func cleanupContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultCleanupTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func combineCleanupErrors(waitErr, closeErr error) error {
	switch {
	case waitErr == nil:
		return closeErr
	case closeErr == nil:
		return waitErr
	default:
		return errors.Wrapf(waitErr, "resource cleanup also failed: %v", closeErr)
	}
}

func rawJSON(value string) (json.RawMessage, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "{}"
	}
	if !json.Valid([]byte(value)) {
		return nil, errors.New("runner tool input is not valid JSON")
	}
	return json.RawMessage(value), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeEnvironmentProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if strings.EqualFold(profile, "default") {
		return ""
	}
	return profile
}

func decodeParams[T any](params json.RawMessage) (T, *protocol.RPCError) {
	var value T
	if err := json.Unmarshal(params, &value); err != nil {
		return value, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	return value, nil
}

func rpcResult(result any, err error) (any, *protocol.RPCError) {
	if err == nil {
		return result, nil
	}
	message := err.Error()
	code := protocol.ErrorCodeInternal
	switch {
	case errors.Is(err, errNoActiveRun):
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: message, Data: protocol.RPCErrorData{Reason: protocol.ErrorReasonRunNotActive}}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = protocol.ErrorCodeUnavailable
	case strings.Contains(message, "busy"), strings.Contains(message, "active run"):
		code = protocol.ErrorCodeBusy
	case strings.Contains(message, "another"), strings.Contains(message, "already"):
		code = protocol.ErrorCodeConflict
	case strings.Contains(message, "required"), strings.Contains(message, "invalid"), strings.Contains(message, "unsupported"):
		code = protocol.ErrorCodeInvalidParams
	case strings.Contains(message, "not ready"), strings.Contains(message, "closed"), strings.Contains(message, "registration"):
		code = protocol.ErrorCodeUnavailable
	}
	return nil, &protocol.RPCError{Code: code, Message: message}
}
