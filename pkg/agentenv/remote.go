package agentenv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// RemoteController is the control-plane runner API used by RemoteEnvironment.
type RemoteController interface {
	OpenRun(ctx context.Context, runnerID string, params protocol.RunOpenParams) (runnerpayload.Manifest, error)
	CallRun(ctx context.Context, runID, method string, params any, result any) error
	ExecuteTool(ctx context.Context, params runnerpayload.ToolExecuteParams, updates func(runnerpayload.ToolUpdateParams)) (runnerpayload.ToolExecuteResult, error)
	CancelRun(ctx context.Context, runID, reason string) error
	CloseRun(ctx context.Context, runID string, status protocol.RunStatus, runErr error) error
}

// RemoteEnvironmentOption configures a remote runner environment.
type RemoteEnvironmentOption func(*RemoteEnvironment)

// WithRemoteClientCapabilities advertises the client UI attached to remote runs.
func WithRemoteClientCapabilities(capabilities protocol.ClientCapabilities) RemoteEnvironmentOption {
	return func(environment *RemoteEnvironment) {
		environment.clientCapabilities = capabilities
	}
}

// WithRemoteRunIDGenerator overrides opaque run ID generation, primarily for tests.
func WithRemoteRunIDGenerator(generate func() (string, error)) RemoteEnvironmentOption {
	return func(environment *RemoteEnvironment) {
		if generate != nil {
			environment.newRunID = generate
		}
	}
}

// RemoteEnvironment proxies a run-scoped agent environment to one registered runner.
type RemoteEnvironment struct {
	mu                 sync.RWMutex
	controller         RemoteController
	runnerID           string
	clientCapabilities protocol.ClientCapabilities
	newRunID           func() (string, error)
	runID              string
	manifest           Manifest
	wireManifest       runnerpayload.Manifest
	opened             bool
	opening            bool
	closing            bool
}

// NewRemoteEnvironment creates an unopened environment bound to one stable runner ID.
func NewRemoteEnvironment(controller RemoteController, runnerID string, options ...RemoteEnvironmentOption) *RemoteEnvironment {
	environment := &RemoteEnvironment{
		controller: controller,
		runnerID:   strings.TrimSpace(runnerID),
		newRunID:   newRemoteRunID,
	}
	for _, option := range options {
		if option != nil {
			option(environment)
		}
	}
	return environment
}

// Open reserves runner capacity and pins the returned environment manifest.
func (e *RemoteEnvironment) Open(ctx context.Context, spec RunSpec) (Manifest, error) {
	if e == nil || e.controller == nil {
		return Manifest{}, errors.New("remote environment controller is required")
	}
	if e.runnerID == "" {
		return Manifest{}, errors.New("remote runner id is required")
	}
	if strings.TrimSpace(spec.ConversationID) == "" {
		return Manifest{}, errors.New("remote conversation id is required")
	}

	e.mu.Lock()
	if e.opened || e.opening || e.closing {
		e.mu.Unlock()
		return Manifest{}, errors.New("remote environment is already active")
	}
	e.opening = true
	e.mu.Unlock()

	runID, err := e.newRunID()
	if err != nil {
		e.finishOpenFailure()
		return Manifest{}, errors.Wrap(err, "failed to generate remote run id")
	}
	params := protocol.RunOpenParams{
		RunID:          runID,
		ConversationID: spec.ConversationID,
		CWD:            strings.TrimSpace(spec.Config.WorkingDirectory),
		ExpectedCWD:    strings.TrimSpace(spec.ExpectedWorkingDirectory),
		Agent: protocol.AgentDescriptor{
			Provider:           spec.Config.Provider,
			Model:              spec.Config.Model,
			Profile:            spec.Config.Profile,
			EnvironmentProfile: spec.EnvironmentProfile,
			RecipeName:         spec.Config.RecipeName,
			InvokedBy:          firstNonEmpty(spec.InvokedBy, "main"),
		},
		ClientCapabilities: e.clientCapabilities,
		ReservedToolNames:  tools.ControlPlaneToolNames(),
	}
	wireManifest, err := e.controller.OpenRun(ctx, e.runnerID, params)
	if err != nil {
		e.finishOpenFailure()
		return Manifest{}, err
	}
	manifest, err := e.convertManifest(wireManifest, spec.Config)
	if err != nil {
		closeCtx := context.WithoutCancel(ctx)
		_ = e.controller.CloseRun(closeCtx, runID, protocol.RunStatusFailed, err)
		e.finishOpenFailure()
		return Manifest{}, err
	}

	e.mu.Lock()
	e.runID = runID
	e.manifest = manifest
	e.wireManifest = wireManifest
	e.opened = true
	e.opening = false
	e.mu.Unlock()
	return manifest.Clone(), nil
}

func (e *RemoteEnvironment) finishOpenFailure() {
	e.mu.Lock()
	e.opening = false
	e.mu.Unlock()
}

// IsOpen reports whether a remote run lease is active.
func (e *RemoteEnvironment) IsOpen() bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.opened
}

// Manifest returns the manifest pinned by run.open.
func (e *RemoteEnvironment) Manifest() Manifest {
	if e == nil {
		return Manifest{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.manifest.Clone()
}

// RunID returns the active opaque control-plane run ID.
func (e *RemoteEnvironment) RunID() string {
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.runID
}

// ExecuteCommand proxies workspace and extension slash commands to the active runner.
func (e *RemoteEnvironment) ExecuteCommand(ctx context.Context, request CommandRequest) (CommandResult, error) {
	if !e.IsOpen() {
		if _, err := e.Open(ctx, request.RunSpec); err != nil {
			return CommandResult{}, err
		}
	}
	runID, err := e.activeRunID()
	if err != nil {
		return CommandResult{}, err
	}
	var result runnerpayload.CommandExecuteResult
	if err := e.controller.CallRun(ctx, runID, protocol.MethodCommandExecute, runnerpayload.CommandExecuteParams{
		RunID:   runID,
		Message: request.Message,
	}, &result); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{
		Matched:         result.Matched,
		Action:          CommandAction(result.Action),
		CommandName:     result.CommandName,
		Response:        result.Response,
		Prompt:          result.Prompt,
		Display:         result.Display,
		DisplayOverride: result.DisplayOverride,
		RecipeName:      result.RecipeName,
		AllowedTools:    slices.Clone(result.AllowedTools),
		AllowedCommands: slices.Clone(result.AllowedCommands),
	}, nil
}

// ProcessUserMessage applies runner-owned user.message handlers.
func (e *RemoteEnvironment) ProcessUserMessage(ctx context.Context, message string) (string, error) {
	result, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{Event: runnerpayload.LifecycleUserMessage, Message: message})
	if err != nil {
		return "", err
	}
	if result.Blocked {
		return "", errors.Errorf("message blocked by extension: %s", result.Reason)
	}
	return result.Message, nil
}

// DispatchAgentStart applies runner-owned agent.start handlers.
func (e *RemoteEnvironment) DispatchAgentStart(ctx context.Context) error {
	_, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{Event: runnerpayload.LifecycleAgentStart})
	return err
}

// DispatchTurnStart applies runner-owned turn.start handlers.
func (e *RemoteEnvironment) DispatchTurnStart(ctx context.Context, turnNumber int) error {
	_, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{Event: runnerpayload.LifecycleTurnStart, TurnNumber: turnNumber})
	return err
}

// ProcessAgentInit applies runner-owned system prompt and tool-list patches.
func (e *RemoteEnvironment) ProcessAgentInit(ctx context.Context, systemPrompt string, allowedTools []string) (AgentInitDecision, error) {
	result, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{
		Event:        runnerpayload.LifecycleAgentInit,
		SystemPrompt: systemPrompt,
		AllowedTools: slices.Clone(allowedTools),
	})
	if err != nil {
		return AgentInitDecision{}, err
	}
	return AgentInitDecision{
		SystemPrompt:  result.SystemPrompt,
		AllowedTools:  slices.Clone(result.AllowedTools),
		ToolsModified: result.ToolsModified,
	}, nil
}

// DispatchTurnEnd applies runner-owned turn.end handlers.
func (e *RemoteEnvironment) DispatchTurnEnd(ctx context.Context, finalOutput string, turnCount int) error {
	_, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{
		Event:       runnerpayload.LifecycleTurnEnd,
		FinalOutput: finalOutput,
		TurnCount:   turnCount,
	})
	return err
}

// DispatchAgentEnd applies runner-owned agent.end handlers.
func (e *RemoteEnvironment) DispatchAgentEnd(ctx context.Context, messages []llmtypes.Message) ([]string, error) {
	result, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{
		Event:    runnerpayload.LifecycleAgentEnd,
		Messages: slices.Clone(messages),
	})
	if err != nil {
		return nil, err
	}
	return slices.Clone(result.FollowUpMessages), nil
}

// DispatchToolCall proxies extension policy for a control-plane tool call.
func (e *RemoteEnvironment) DispatchToolCall(ctx context.Context, request ToolRequest) (ToolCallDecision, error) {
	input, err := rawToolInput(request.Input)
	if err != nil {
		return ToolCallDecision{}, err
	}
	result, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{
		Event:      runnerpayload.LifecycleToolCall,
		ToolName:   request.Name,
		ToolInput:  input,
		ToolCallID: request.ToolCallID,
	})
	if err != nil {
		return ToolCallDecision{}, err
	}
	effectiveInput := request.Input
	if len(result.ToolInput) > 0 {
		effectiveInput = string(result.ToolInput)
	}
	return ToolCallDecision{Blocked: result.Blocked, Reason: result.Reason, Input: effectiveInput}, nil
}

// DispatchToolUpdate proxies extension policy for a transient control-plane tool result.
func (e *RemoteEnvironment) DispatchToolUpdate(ctx context.Context, request ToolOutputRequest) (ToolOutputDecision, error) {
	return e.dispatchToolOutput(ctx, runnerpayload.LifecycleToolUpdate, request)
}

// DispatchToolResult proxies extension policy for an authoritative control-plane tool result.
func (e *RemoteEnvironment) DispatchToolResult(ctx context.Context, request ToolOutputRequest) (ToolOutputDecision, error) {
	return e.dispatchToolOutput(ctx, runnerpayload.LifecycleToolResult, request)
}

func (e *RemoteEnvironment) dispatchToolOutput(ctx context.Context, event runnerpayload.LifecycleEvent, request ToolOutputRequest) (ToolOutputDecision, error) {
	input, err := rawToolInput(request.Input)
	if err != nil {
		return ToolOutputDecision{}, err
	}
	structured := request.StructuredResult
	result, err := e.dispatchLifecycle(ctx, runnerpayload.LifecycleDispatchParams{
		Event:            event,
		ToolName:         request.Name,
		ToolInput:        input,
		ToolCallID:       request.ToolCallID,
		StructuredResult: &structured,
	})
	if err != nil {
		return ToolOutputDecision{}, err
	}
	if result.StructuredResult == nil {
		return ToolOutputDecision{}, errors.New("runner lifecycle response omitted structured result")
	}
	return ToolOutputDecision{
		StructuredResult: *result.StructuredResult,
		Modified:         result.Modified,
		Accepted:         result.Accepted,
	}, nil
}

// CanStreamToolUpdates reports the capability pinned in the active manifest.
func (e *RemoteEnvironment) CanStreamToolUpdates() bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.opened && e.wireManifest.Capabilities.ToolUpdates
}

// ExecuteTool performs one complete runner-side tool lifecycle.
func (e *RemoteEnvironment) ExecuteTool(ctx context.Context, request ToolRequest, updates ToolUpdateSink) (ToolExecution, error) {
	runID, err := e.activeRunID()
	if err != nil {
		return ToolExecution{}, err
	}
	input, err := rawToolInput(request.Input)
	if err != nil {
		return ToolExecution{}, err
	}
	params := runnerpayload.ToolExecuteParams{
		RunID:      runID,
		ToolCallID: request.ToolCallID,
		Name:       request.Name,
		Input:      input,
	}
	var updateCallback func(runnerpayload.ToolUpdateParams)
	if updates != nil && e.CanStreamToolUpdates() {
		updateCallback = func(update runnerpayload.ToolUpdateParams) {
			updates(ToolUpdate{
				Result:           newRemoteToolResult(update.Result),
				StructuredResult: update.Result.Structured,
				Modified:         update.Modified,
			})
		}
	}
	result, err := e.controller.ExecuteTool(ctx, params, updateCallback)
	if err != nil {
		var rpcErr *protocol.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == protocol.ErrorCodeUnavailable && rpcErr.Reason() == protocol.ErrorReasonResultTooLarge {
			toolResult := tooltypes.BaseToolResult{Error: rpcErr.Message}
			structured := toolResult.StructuredData()
			structured.ToolName = request.Name
			return ToolExecution{
				Input:            request.Input,
				Result:           toolResult,
				StructuredResult: structured,
			}, nil
		}
		return ToolExecution{}, err
	}
	effectiveInput := request.Input
	if len(result.Input) > 0 {
		effectiveInput = string(result.Input)
	}
	return ToolExecution{
		Input:            effectiveInput,
		Result:           newRemoteToolResult(result.Result),
		StructuredResult: result.Result.Structured,
		Modified:         result.Modified,
	}, nil
}

// Close records a successful remote run and releases its runner lease.
func (e *RemoteEnvironment) Close(ctx context.Context) error {
	return e.CloseWithError(ctx, nil)
}

// CloseWithError records the terminal run outcome and releases its runner lease.
func (e *RemoteEnvironment) CloseWithError(ctx context.Context, runErr error) error {
	if e == nil || e.controller == nil {
		return nil
	}
	e.mu.Lock()
	if !e.opened || e.closing {
		e.mu.Unlock()
		return nil
	}
	e.closing = true
	runID := e.runID
	e.mu.Unlock()

	status := protocol.RunStatusSucceeded
	if runErr != nil {
		status = protocol.RunStatusFailed
	}
	var cancelErr error
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		status = protocol.RunStatusCanceled
		cancelErr = e.controller.CancelRun(ctx, runID, runErr.Error())
	}
	closeErr := e.controller.CloseRun(ctx, runID, status, runErr)

	e.mu.Lock()
	e.runID = ""
	e.manifest = Manifest{}
	e.wireManifest = runnerpayload.Manifest{}
	e.opened = false
	e.closing = false
	e.mu.Unlock()
	if closeErr != nil {
		return closeErr
	}
	return cancelErr
}

func (e *RemoteEnvironment) activeRunID() (string, error) {
	if e == nil {
		return "", errors.New("remote environment is required")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.opened || e.closing || e.runID == "" {
		return "", errors.New("remote environment is not open")
	}
	return e.runID, nil
}

func (e *RemoteEnvironment) dispatchLifecycle(ctx context.Context, params runnerpayload.LifecycleDispatchParams) (runnerpayload.LifecycleDispatchResult, error) {
	runID, err := e.activeRunID()
	if err != nil {
		return runnerpayload.LifecycleDispatchResult{}, err
	}
	params.RunID = runID
	var result runnerpayload.LifecycleDispatchResult
	if err := e.controller.CallRun(ctx, runID, protocol.MethodLifecycleDispatch, params, &result); err != nil {
		return runnerpayload.LifecycleDispatchResult{}, err
	}
	return result, nil
}

func (e *RemoteEnvironment) convertManifest(wire runnerpayload.Manifest, config llmtypes.Config) (Manifest, error) {
	contexts := make(map[string]string, len(wire.ContextFiles))
	for _, contextFile := range wire.ContextFiles {
		path := strings.TrimSpace(contextFile.Path)
		if path == "" {
			return Manifest{}, errors.New("runner manifest contains a context file without a path")
		}
		if _, exists := contexts[path]; exists {
			return Manifest{}, errors.Errorf("runner manifest contains duplicate context path %s", path)
		}
		if contextFile.Digest != remoteContentDigest(contextFile.Content) {
			return Manifest{}, errors.Errorf("runner context digest does not match content for %s", path)
		}
		contexts[path] = contextFile.Content
	}

	controlPlaneTools := allowedControlPlaneTools(config)
	definitions := make([]ToolDefinition, 0, len(wire.Tools)+len(controlPlaneTools))
	for _, tool := range controlPlaneTools {
		definitions = append(definitions, ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tooltypes.JSONSchemaForTool(tool),
			Placement:   ToolPlacementControlPlane,
			Tool:        tool,
		})
	}
	for _, definition := range wire.Tools {
		proxy := newRemoteToolProxy(definition)
		definitions = append(definitions, ToolDefinition{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: cloneJSONMap(definition.InputSchema),
			Placement:   ToolPlacementEnvironment,
			Tool:        proxy,
		})
	}

	return Manifest{
		WorkingDirectory: wire.WorkingDirectory,
		Contexts:         contexts,
		Tools:            definitions,
		Commands:         slices.Clone(wire.Commands),
		Config: (&EnvironmentConfig{
			AllowedCommands:     slices.Clone(wire.Config.AllowedCommands),
			ToolMode:            wire.Config.ToolMode,
			EnableFSSearchTools: wire.Config.EnableFSSearchTools,
			SystemPromptPath:    wire.Config.SystemPromptPath,
			SystemPromptContent: wire.Config.SystemPromptContent,
			SystemPromptArgs:    maps.Clone(wire.Config.SystemPromptArgs),
			SystemInformation:   wire.Config.SystemInformation.Clone(),
		}).Clone(),
	}, nil
}

func allowedControlPlaneTools(config llmtypes.Config) []tooltypes.Tool {
	available := tools.ControlPlaneTools()
	if len(config.AllowedTools) == 0 {
		return available
	}

	allowed := make(map[string]struct{}, len(config.AllowedTools))
	for _, name := range config.AllowedTools {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	filtered := make([]tooltypes.Tool, 0, len(available))
	for _, tool := range available {
		if tool == nil {
			continue
		}
		if _, ok := allowed[tool.Name()]; ok {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func rawToolInput(input string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		trimmed = "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, errors.New("tool input is not valid JSON")
	}
	return json.RawMessage(trimmed), nil
}

func newRemoteRunID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "run_" + base64.RawURLEncoding.EncodeToString(value), nil
}

func remoteContentDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("sha256:%x", digest[:])
}

type remoteToolProxy struct {
	name        string
	description string
	schema      map[string]any
}

func newRemoteToolProxy(definition runnerpayload.ToolDefinition) *remoteToolProxy {
	return &remoteToolProxy{
		name:        definition.Name,
		description: definition.Description,
		schema:      cloneJSONMap(definition.InputSchema),
	}
}

func (t *remoteToolProxy) GenerateSchema() *jsonschema.Schema {
	payload, err := json.Marshal(t.schema)
	if err != nil {
		return &jsonschema.Schema{Type: "object"}
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(payload, &schema); err != nil {
		return &jsonschema.Schema{Type: "object"}
	}
	return &schema
}

func (t *remoteToolProxy) RawInputSchema() map[string]any { return cloneJSONMap(t.schema) }
func (t *remoteToolProxy) Name() string                   { return t.name }
func (t *remoteToolProxy) Description() string            { return t.description }
func (t *remoteToolProxy) ValidateInput(_ tooltypes.State, parameters string) error {
	_, err := rawToolInput(parameters)
	return err
}

func (t *remoteToolProxy) Execute(context.Context, tooltypes.State, string) tooltypes.ToolResult {
	return tooltypes.BaseToolResult{Error: "remote tool proxies must execute through RemoteEnvironment"}
}
func (t *remoteToolProxy) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

type remoteToolResult struct {
	result runnerpayload.ToolResult
}

func newRemoteToolResult(result runnerpayload.ToolResult) remoteToolResult {
	result.ContentParts = slices.Clone(result.ContentParts)
	return remoteToolResult{result: result}
}

func (r remoteToolResult) AssistantFacing() string { return r.result.AssistantFacing }
func (r remoteToolResult) IsError() bool {
	return r.result.Error != "" || !r.result.Structured.Success
}
func (r remoteToolResult) GetError() string { return r.result.Error }
func (r remoteToolResult) GetResult() string {
	if r.result.DisplayOutput != "" {
		return r.result.DisplayOutput
	}
	return r.result.AssistantFacing
}

func (r remoteToolResult) StructuredData() tooltypes.StructuredToolResult { return r.result.Structured }

func (r remoteToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	return slices.Clone(r.result.ContentParts)
}
