package client

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// Peer is the symmetric runner connection used for updates and reverse UI calls.
type Peer interface {
	Call(ctx context.Context, method string, params any, result any) error
	Notify(ctx context.Context, method string, params any) error
	NotifyUpdate(method string, params any) error
}

// RuntimeProvider supplies the persistent extension runtime for the bound workspace.
type RuntimeProvider interface {
	RuntimeWithCallContext(ctx context.Context, cwd string, callContext extensions.ExtensionCallContext) (*extensions.Runtime, error)
}

type runtimeDiscoveryProvider interface {
	RuntimeForCommandDiscovery(ctx context.Context, cwd string) (*extensions.Runtime, error)
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
}

// Service handles control-plane requests for one workspace-bound runner process.
type Service struct {
	ctx                context.Context
	mu                 sync.Mutex
	snapshotMu         sync.Mutex
	workspace          string
	runtimeProvider    RuntimeProvider
	ownedRuntime       *extensions.RuntimeManager
	configLoader       ConfigLoader
	environmentFactory EnvironmentFactory
	instanceProvider   ExecutionInstanceProvider
	peer               Peer
	runnerID           string
	generation         int64
	active             *activeRun
	lastManifestDigest string
	closed             bool
}

type activeRun struct {
	id             string
	conversationID string
	clientCaps     protocol.ClientCapabilities
	config         llmtypes.Config
	runtime        *extensions.Runtime
	instance       ExecutionInstance
	environment    agentenv.Environment
	manifest       protocol.Manifest
	ctx            context.Context
	cancel         context.CancelFunc
	updates        atomic.Uint64
	ops            sync.WaitGroup
	opening        bool
	closing        bool
	stopping       bool
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
		ctx:                parent,
		workspace:          workspace,
		runtimeProvider:    options.RuntimeProvider,
		configLoader:       options.ConfigLoader,
		environmentFactory: options.EnvironmentFactory,
		instanceProvider:   options.ExecutionInstanceProvider,
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
	return service, nil
}

func loadRunnerConfig(profile string) (llmtypes.Config, error) {
	if strings.TrimSpace(profile) != "" {
		return llm.GetConfigFromViperWithProfile(profile)
	}
	return llm.GetConfigFromViperWithoutProfile()
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
	if s.active != nil {
		return errors.New("cannot replace runner registration during an active run")
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
		value, rpcErr := decodeParams[protocol.CommandExecuteParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.executeCommand(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodLifecycleDispatch:
		value, rpcErr := decodeParams[protocol.LifecycleDispatchParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.dispatchLifecycle(ctx, value)
		return rpcResult(result, err)
	case protocol.MethodToolExecute:
		value, rpcErr := decodeParams[protocol.ToolExecuteParams](params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		result, err := s.executeTool(ctx, value)
		return rpcResult(result, err)
	default:
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeMethodNotFound, Message: "runner request method not found"}
	}
}

// HandleNotification implements protocol.NotificationHandler for client UI events.
func (s *Service) HandleNotification(ctx context.Context, method string, params json.RawMessage) {
	var err error
	switch method {
	case protocol.MethodUISurfaceInput:
		var value protocol.UISurfaceInputParams
		if json.Unmarshal(params, &value) == nil {
			err = s.notifySurfaceInput(ctx, value)
		}
	case protocol.MethodUISurfaceResize:
		var value protocol.UISurfaceResizeParams
		if json.Unmarshal(params, &value) == nil {
			err = s.notifySurfaceResize(ctx, value)
		}
	}
	if err != nil {
		s.reportEnvironmentError(err)
	}
}

func (s *Service) openRun(ctx context.Context, params protocol.RunOpenParams) (protocol.Manifest, error) {
	if err := params.Validate(); err != nil {
		return protocol.Manifest{}, err
	}
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return protocol.Manifest{}, errors.New("runner service is closed")
	}
	if s.runnerID == "" || s.generation <= 0 {
		s.mu.Unlock()
		return protocol.Manifest{}, errors.New("runner has not completed registration")
	}
	if s.active != nil {
		activeID := s.active.id
		s.mu.Unlock()
		return protocol.Manifest{}, errors.Errorf("runner is busy with run %s", activeID)
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	run := &activeRun{
		id:             params.RunID,
		conversationID: params.ConversationID,
		clientCaps:     params.ClientCapabilities,
		ctx:            runCtx,
		cancel:         cancel,
		opening:        true,
	}
	run.ops.Add(1)
	defer run.ops.Done()
	s.active = run
	runnerID := s.runnerID
	generation := s.generation
	s.mu.Unlock()

	operationCtx, finishOperation := runOperationContext(ctx, run)
	defer finishOperation()
	operationCtx = s.decorateRunContext(operationCtx, run.conversationID)
	instance, err := s.instanceProvider.Create(operationCtx, ExecutionInstanceSpec{
		RunID:          params.RunID,
		ConversationID: params.ConversationID,
	})
	if err != nil {
		s.failOpen(run)
		return protocol.Manifest{}, errors.Wrap(err, "failed to create runner execution instance")
	}
	if instance == nil {
		s.failOpen(run)
		return protocol.Manifest{}, errors.New("runner execution instance provider returned nil")
	}
	workingDirectory := strings.TrimSpace(instance.WorkingDirectory())
	if workingDirectory == "" {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return protocol.Manifest{}, errors.New("runner execution instance returned an empty working directory")
	}

	config, err := s.configLoader(params.Agent.Profile)
	if err != nil {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return protocol.Manifest{}, errors.Wrap(err, "failed to load runner configuration")
	}
	config.WorkingDirectory = workingDirectory
	config.Provider = params.Agent.Provider
	config.Model = params.Agent.Model
	config.Profile = params.Agent.Profile
	config.RecipeName = params.Agent.RecipeName

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
	runtime, err := s.runtimeProvider.RuntimeWithCallContext(operationCtx, workingDirectory, callContext)
	if err != nil {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return protocol.Manifest{}, errors.Wrap(err, "failed to initialize runner extensions")
	}
	config.Extensions = runtime
	environment := s.environmentFactory(workingDirectory, runtime)
	if environment == nil {
		s.closeOpeningResources(operationCtx, nil, instance)
		s.failOpen(run)
		return protocol.Manifest{}, errors.New("runner environment factory returned nil")
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
		return protocol.Manifest{}, errors.Wrap(err, "failed to open runner environment")
	}
	wireManifest, err := buildWireManifest(localManifest, config, runtime, runnerID, params.RunID, generation, params.ReservedToolNames)
	if err == nil {
		err = operationCtx.Err()
	}
	if err != nil {
		s.closeOpeningResources(operationCtx, environment, instance)
		s.failOpen(run)
		return protocol.Manifest{}, err
	}

	s.mu.Lock()
	if s.active != run || run.closing {
		s.mu.Unlock()
		s.closeOpeningResources(operationCtx, environment, instance)
		s.failOpen(run)
		return protocol.Manifest{}, errors.New("runner run was canceled while opening")
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
	if err := closeExecutionResources(context.WithoutCancel(ctx), environment, instance); err != nil {
		logger.G(ctx).WithError(err).Warn("failed to clean up runner execution instance after open failure")
	}
}

func (s *Service) failOpen(run *activeRun) {
	if run == nil {
		return
	}
	run.cancel()
	s.mu.Lock()
	if s.active == run {
		s.active = nil
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
	if run.closing {
		s.mu.Unlock()
		return nil
	}
	run.closing = true
	run.stopping = true
	run.cancel()
	s.mu.Unlock()

	run.ops.Wait()
	closeErr := closeExecutionResources(context.WithoutCancel(ctx), run.environment, run.instance)
	s.mu.Lock()
	if s.active == run {
		s.active = nil
	}
	s.mu.Unlock()
	return closeErr
}

// ProbeManifestDigest snapshots idle runner resources without reserving a control-plane run.
func (s *Service) ProbeManifestDigest(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("runner service is required")
	}
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", errors.New("runner service is closed")
	}
	if s.active != nil {
		digest := s.active.manifest.Digest
		s.mu.Unlock()
		return digest, nil
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
		return "", errors.Wrap(err, "failed to create runner manifest probe instance")
	}
	if instance == nil {
		return "", errors.New("runner manifest probe instance provider returned nil")
	}
	workingDirectory := strings.TrimSpace(instance.WorkingDirectory())
	if workingDirectory == "" {
		_ = instance.Close(context.WithoutCancel(ctx))
		return "", errors.New("runner manifest probe instance returned an empty working directory")
	}

	config, err := s.configLoader("")
	if err != nil {
		_ = instance.Close(context.WithoutCancel(ctx))
		return "", errors.Wrap(err, "failed to load runner configuration")
	}
	config.WorkingDirectory = workingDirectory
	probeCtx := s.decorateRunContext(ctx, "runner-manifest-probe")
	var runtime *extensions.Runtime
	if provider, ok := s.runtimeProvider.(runtimeDiscoveryProvider); ok {
		runtime, err = provider.RuntimeForCommandDiscovery(probeCtx, workingDirectory)
	} else {
		runtime, err = s.runtimeProvider.RuntimeWithCallContext(probeCtx, workingDirectory, extensions.ExtensionCallContext{
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
		_ = instance.Close(context.WithoutCancel(probeCtx))
		return "", errors.Wrap(err, "failed to initialize runner extensions")
	}
	config.Extensions = runtime
	environment := s.environmentFactory(workingDirectory, runtime)
	if environment == nil {
		_ = instance.Close(context.WithoutCancel(probeCtx))
		return "", errors.New("runner environment factory returned nil")
	}
	manifest, err := environment.Open(probeCtx, agentenv.RunSpec{
		ConversationID: "runner-manifest-probe",
		Config:         config,
		InvokedBy:      "runner.manifest",
	})
	if err != nil {
		_ = closeExecutionResources(context.WithoutCancel(probeCtx), environment, instance)
		return "", errors.Wrap(err, "failed to probe runner environment")
	}
	defer func() { _ = closeExecutionResources(context.WithoutCancel(probeCtx), environment, instance) }()
	wire, err := buildWireManifest(manifest, config, runtime, runnerID, "runner-manifest-probe", generation, tools.ControlPlaneToolNames())
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.lastManifestDigest = wire.Digest
	s.mu.Unlock()
	return wire.Digest, nil
}

// HeartbeatSnapshot returns current application health for runner.heartbeat.
func (s *Service) HeartbeatSnapshot() (protocol.RunnerState, string, string) {
	if s == nil {
		return protocol.RunnerStateError, "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return protocol.RunnerStateIdle, "", s.lastManifestDigest
	}
	state := protocol.RunnerStateRunning
	if s.active.stopping || s.active.closing {
		state = protocol.RunnerStateStopping
	}
	digest := s.active.manifest.Digest
	if digest == "" {
		digest = s.lastManifestDigest
	}
	return state, s.active.id, digest
}

func (s *Service) executeCommand(ctx context.Context, params protocol.CommandExecuteParams) (protocol.CommandExecuteResult, error) {
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return protocol.CommandExecuteResult{}, err
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
		return protocol.CommandExecuteResult{}, err
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
	return protocol.CommandExecuteResult{
		Matched:         result.Matched,
		Action:          string(result.Action),
		CommandName:     result.CommandName,
		Response:        result.Response,
		Prompt:          result.Prompt,
		Display:         result.Display,
		RecipeName:      result.RecipeName,
		AllowedTools:    append([]string(nil), result.AllowedTools...),
		AllowedCommands: append([]string(nil), result.AllowedCommands...),
	}, nil
}

func (s *Service) dispatchLifecycle(ctx context.Context, params protocol.LifecycleDispatchParams) (protocol.LifecycleDispatchResult, error) {
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return protocol.LifecycleDispatchResult{}, err
	}
	defer finish()

	switch params.Event {
	case protocol.LifecycleUserMessage:
		message, err := run.environment.ProcessUserMessage(operationCtx, params.Message)
		return protocol.LifecycleDispatchResult{Message: message}, err
	case protocol.LifecycleAgentStart:
		return protocol.LifecycleDispatchResult{}, run.environment.DispatchAgentStart(operationCtx)
	case protocol.LifecycleTurnStart:
		return protocol.LifecycleDispatchResult{}, run.environment.DispatchTurnStart(operationCtx, params.TurnNumber)
	case protocol.LifecycleAgentInit:
		decision, err := run.environment.ProcessAgentInit(operationCtx, params.SystemPrompt, params.AllowedTools)
		return protocol.LifecycleDispatchResult{
			SystemPrompt:  decision.SystemPrompt,
			AllowedTools:  append([]string(nil), decision.AllowedTools...),
			ToolsModified: decision.ToolsModified,
		}, err
	case protocol.LifecycleTurnEnd:
		return protocol.LifecycleDispatchResult{}, run.environment.DispatchTurnEnd(operationCtx, params.FinalOutput, params.TurnCount)
	case protocol.LifecycleAgentEnd:
		messages, err := run.environment.DispatchAgentEnd(operationCtx, params.Messages)
		return protocol.LifecycleDispatchResult{FollowUpMessages: messages}, err
	case protocol.LifecycleToolCall:
		decision, err := run.environment.DispatchToolCall(operationCtx, agentenv.ToolRequest{
			Name:       params.ToolName,
			Input:      string(params.ToolInput),
			ToolCallID: params.ToolCallID,
		})
		input, encodeErr := rawJSON(decision.Input)
		if err == nil {
			err = encodeErr
		}
		return protocol.LifecycleDispatchResult{
			Blocked:   decision.Blocked,
			Reason:    decision.Reason,
			ToolInput: input,
		}, err
	case protocol.LifecycleToolUpdate, protocol.LifecycleToolResult:
		if params.StructuredResult == nil {
			return protocol.LifecycleDispatchResult{}, errors.New("structuredResult is required for tool output lifecycle")
		}
		request := agentenv.ToolOutputRequest{
			Name:             params.ToolName,
			Input:            string(params.ToolInput),
			ToolCallID:       params.ToolCallID,
			StructuredResult: *params.StructuredResult,
		}
		var decision agentenv.ToolOutputDecision
		if params.Event == protocol.LifecycleToolUpdate {
			decision, err = run.environment.DispatchToolUpdate(operationCtx, request)
		} else {
			decision, err = run.environment.DispatchToolResult(operationCtx, request)
		}
		structured := decision.StructuredResult
		return protocol.LifecycleDispatchResult{
			StructuredResult: &structured,
			Modified:         decision.Modified,
			Accepted:         decision.Accepted,
		}, err
	default:
		return protocol.LifecycleDispatchResult{}, errors.Errorf("unsupported lifecycle event %q", params.Event)
	}
}

func (s *Service) executeTool(ctx context.Context, params protocol.ToolExecuteParams) (protocol.ToolExecuteResult, error) {
	if strings.TrimSpace(params.ToolCallID) == "" || strings.TrimSpace(params.Name) == "" {
		return protocol.ToolExecuteResult{}, errors.New("toolCallId and name are required")
	}
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return protocol.ToolExecuteResult{}, err
	}
	defer finish()

	peer := s.currentPeer()
	var updateSink agentenv.ToolUpdateSink
	if params.WantUpdates {
		requestID := protocol.RequestIDFromContext(operationCtx)
		if requestID == "" {
			requestID = params.ToolCallID
		}
		updateSink = func(update agentenv.ToolUpdate) {
			if peer == nil || update.Result == nil {
				return
			}
			_ = peer.NotifyUpdate(protocol.MethodToolUpdate, protocol.ToolUpdateParams{
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
		return protocol.ToolExecuteResult{}, err
	}
	input, err := rawJSON(execution.Input)
	if err != nil {
		return protocol.ToolExecuteResult{}, err
	}
	return protocol.ToolExecuteResult{
		Input:    input,
		Result:   serializeToolResult(execution.Result, execution.StructuredResult),
		Modified: execution.Modified,
	}, nil
}

func serializeToolResult(result tooltypes.ToolResult, structured tooltypes.StructuredToolResult) protocol.ToolResult {
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
	wire := protocol.ToolResult{
		AssistantFacing: result.AssistantFacing(),
		Error:           errorMessage,
		Structured:      structured,
	}
	if multimodal, ok := result.(tooltypes.MultiModalToolResult); ok {
		wire.ContentParts = append([]tooltypes.ToolResultContentPart(nil), multimodal.ContentParts()...)
	}
	return wire
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
	operationCtx = s.decorateRunContext(operationCtx, run.conversationID)
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

func (s *Service) decorateRunContext(ctx context.Context, conversationID string) context.Context {
	ctx = extensions.ContextWithUIInputBroker(ctx, s)
	ctx = extensions.ContextWithExtensionUIHost(ctx, s)
	return extensions.ContextWithExtensionUIScope(ctx, conversationID)
}

func (s *Service) activeRunLocked(runID string) (*activeRun, error) {
	if s.active == nil {
		return nil, errors.New("runner has no active run")
	}
	if strings.TrimSpace(runID) == "" || s.active.id != strings.TrimSpace(runID) {
		return nil, errors.New("request belongs to another runner run")
	}
	return s.active, nil
}

func (s *Service) currentPeer() Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peer
}

func (s *Service) reportEnvironmentError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	peer := s.peer
	runID := ""
	if s.active != nil {
		runID = s.active.id
	}
	s.mu.Unlock()
	if peer != nil && runID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = peer.Notify(ctx, protocol.MethodRunEnvironmentError, protocol.EnvironmentErrorParams{RunID: runID, Message: err.Error()})
	}
}

// AbortActiveRun releases local run resources after a connection loss.
func (s *Service) AbortActiveRun(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.active == nil {
		s.peer = nil
		s.mu.Unlock()
		return nil
	}
	run := s.active
	run.closing = true
	run.stopping = true
	run.cancel()
	s.peer = nil
	s.mu.Unlock()
	run.ops.Wait()
	err := closeExecutionResources(context.WithoutCancel(ctx), run.environment, run.instance)
	s.mu.Lock()
	if s.active == run {
		s.active = nil
	}
	s.mu.Unlock()
	return err
}

// Close releases an active environment and any runtime manager owned by the service.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	activeErr := s.AbortActiveRun(context.Background())
	if s.ownedRuntime != nil {
		if err := s.ownedRuntime.Close(); activeErr == nil {
			activeErr = err
		}
	}
	return activeErr
}

func closeExecutionResources(ctx context.Context, environment agentenv.Environment, instance ExecutionInstance) error {
	var firstErr error
	if environment != nil {
		firstErr = environment.Close(ctx)
	}
	if instance != nil {
		if err := instance.Close(ctx); firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
