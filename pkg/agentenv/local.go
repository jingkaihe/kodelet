package agentenv

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// LocalEnvironment executes against the current process and a local tool state.
type LocalEnvironment struct {
	mu               sync.RWMutex
	workingDirectory string
	extensions       *extensions.Runtime
	providedState    tooltypes.State
	useProvidedState bool
	state            tooltypes.State
	manifest         Manifest
	spec             RunSpec
	opened           bool
}

// NewLocalEnvironment creates an environment that builds a fresh BasicState whenever a run opens.
func NewLocalEnvironment(workingDirectory string, runtime *extensions.Runtime) *LocalEnvironment {
	return &LocalEnvironment{
		workingDirectory: strings.TrimSpace(workingDirectory),
		extensions:       runtime,
	}
}

// NewLocalEnvironmentFromState creates a compatibility environment around an existing state.
func NewLocalEnvironmentFromState(state tooltypes.State, runtime *extensions.Runtime) *LocalEnvironment {
	workingDirectory := ""
	if state != nil {
		workingDirectory = state.WorkingDirectory()
	}
	return &LocalEnvironment{
		workingDirectory: workingDirectory,
		extensions:       runtime,
		providedState:    state,
		useProvidedState: true,
	}
}

// Open snapshots context, tools, commands, and extension behavior for one top-level run.
func (e *LocalEnvironment) Open(ctx context.Context, spec RunSpec) (Manifest, error) {
	if e == nil {
		return Manifest{}, errors.New("local environment is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.opened {
		return Manifest{}, errors.New("local environment is already open")
	}

	spec = spec.Clone()
	runtime := e.extensions
	if runtime == nil {
		runtime, _ = spec.Config.Extensions.(*extensions.Runtime)
	}

	state := e.providedState
	if !e.useProvidedState {
		workingDirectory := firstNonEmpty(e.workingDirectory, spec.Config.WorkingDirectory)
		stateOpts := []tools.BasicStateOption{
			tools.WithWorkingDirectory(workingDirectory),
			tools.WithLLMConfig(spec.Config),
			tools.WithMainTools(),
			tools.WithSkillTool(),
		}
		if runtime != nil {
			stateOpts = append(stateOpts, tools.WithExtensionTools(runtime.Tools()))
		}
		state = tools.NewBasicState(ctx, stateOpts...)
	}

	manifest := snapshotManifest(ctx, state, runtime)
	spec.Config.WorkingDirectory = manifest.WorkingDirectory
	e.extensions = runtime
	e.state = newSnapshotState(state, manifest)
	e.manifest = manifest
	e.spec = spec
	e.opened = true
	return manifest.Clone(), nil
}

// Manifest returns the currently pinned manifest.
func (e *LocalEnvironment) Manifest() Manifest {
	if e == nil {
		return Manifest{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.manifest.Clone()
}

// IsOpen reports whether this environment currently has a pinned run snapshot.
func (e *LocalEnvironment) IsOpen() bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.opened
}

// State returns the pinned local state used for tool execution.
func (e *LocalEnvironment) State() tooltypes.State {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.state != nil {
		return e.state
	}
	return e.providedState
}

// SetExtensions replaces the local extension runtime used by the next opened run.
func (e *LocalEnvironment) SetExtensions(runtime any) {
	if e == nil {
		return
	}
	extensionRuntime, _ := runtime.(*extensions.Runtime)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.opened {
		e.extensions = extensionRuntime
	}
}

// ApplyCommandResult updates run-scoped recipe and restriction context without changing the pinned manifest.
func (e *LocalEnvironment) ApplyCommandResult(result CommandResult) {
	if e == nil || !result.Matched {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.opened {
		return
	}
	if strings.TrimSpace(result.RecipeName) != "" {
		e.spec.Config.RecipeName = result.RecipeName
	}
	if len(result.AllowedTools) > 0 {
		e.spec.Config.AllowedTools = slices.Clone(result.AllowedTools)
	}
	if len(result.AllowedCommands) > 0 {
		e.spec.Config.AllowedCommands = slices.Clone(result.AllowedCommands)
	}
}

// ExecuteCommand resolves extension commands first, then workspace recipes.
func (e *LocalEnvironment) ExecuteCommand(ctx context.Context, request CommandRequest) (CommandResult, error) {
	command, args, found := slashcommands.Parse(request.Message)
	if !found {
		return CommandResult{}, nil
	}
	pinned, opened := e.pinnedCommands(command)
	if opened && command != goals.SlashCommandName && command != slashcommands.RenameCommandName && len(pinned) == 0 {
		return CommandResult{}, errors.Errorf("slash command '/%s' is not available in the pinned run manifest", command)
	}

	runtime := e.extensionRuntime()
	if runtime != nil {
		result, err := runtime.TryCommand(ctx, request.Message, command, args, extensionCallContext(request.RunSpec))
		if err != nil {
			return CommandResult{}, err
		}
		if result != nil && result.Matched {
			switch result.Action {
			case extensions.CommandActionRespond:
				return CommandResult{
					Matched:     true,
					Action:      CommandActionRespond,
					CommandName: result.CommandName,
					Response:    result.Response,
					Display:     result.Display,
				}, nil
			case extensions.CommandActionRunAgent:
				return CommandResult{
					Matched:     true,
					Action:      CommandActionRunAgent,
					CommandName: result.CommandName,
					Prompt:      result.Prompt,
					Display:     result.Display,
					RecipeName:  result.RecipeName,
				}, nil
			default:
				return CommandResult{}, errors.Errorf("extension command %s returned unknown action %q", result.CommandName, result.Action)
			}
		}
	}

	if command == goals.SlashCommandName || command == slashcommands.RenameCommandName {
		return CommandResult{}, nil
	}

	expectedDigest := ""
	for _, pinnedCommand := range pinned {
		if strings.TrimSpace(pinnedCommand.Digest) != "" {
			expectedDigest = pinnedCommand.Digest
			break
		}
	}
	if opened && expectedDigest == "" {
		return CommandResult{}, errors.Errorf("pinned extension command '/%s' is no longer available", command)
	}

	workingDirectory := firstNonEmpty(request.RunSpec.Config.WorkingDirectory, e.workingDirectory)
	processor, err := fragments.NewFragmentProcessor(fragments.WithDefaultDirsForCWD(workingDirectory))
	if err != nil {
		return CommandResult{}, errors.Wrap(err, "failed to initialize slash commands")
	}
	var expansion *slashcommands.Expansion
	if opened {
		expansion, err = slashcommands.ExpandPinned(ctx, processor, command, args, expectedDigest)
	} else {
		expansion, err = slashcommands.Expand(ctx, processor, command, args)
	}
	if err != nil {
		return CommandResult{}, err
	}

	return CommandResult{
		Matched:         true,
		Action:          CommandActionRunAgent,
		CommandName:     expansion.Command,
		Prompt:          expansion.Prompt,
		Display:         expansion.Display,
		RecipeName:      expansion.Command,
		AllowedTools:    slices.Clone(expansion.Metadata.AllowedTools),
		AllowedCommands: slices.Clone(expansion.Metadata.AllowedCommands),
	}, nil
}

// ProcessUserMessage applies user.message extension handlers.
func (e *LocalEnvironment) ProcessUserMessage(ctx context.Context, message string) (string, error) {
	runtime, callContext := e.runContext()
	if runtime == nil {
		return message, nil
	}
	decision := runtime.DispatchUserMessage(ctx, callContext, message)
	if decision.Blocked {
		return "", errors.Errorf("message blocked by extension: %s", decision.Reason)
	}
	return decision.Message, nil
}

// DispatchAgentStart applies agent.start extension handlers.
func (e *LocalEnvironment) DispatchAgentStart(ctx context.Context) error {
	runtime, callContext := e.runContext()
	if runtime != nil {
		runtime.DispatchAgentStart(ctx, callContext)
	}
	return nil
}

// DispatchTurnStart applies turn.start extension handlers.
func (e *LocalEnvironment) DispatchTurnStart(ctx context.Context, turnNumber int) error {
	runtime, callContext := e.runContext()
	if runtime != nil {
		runtime.DispatchTurnStart(ctx, callContext, turnNumber)
	}
	return nil
}

// ProcessAgentInit applies agent.init extension handlers.
func (e *LocalEnvironment) ProcessAgentInit(ctx context.Context, systemPrompt string, allowedTools []string) (AgentInitDecision, error) {
	runtime, callContext := e.runContext()
	decision := AgentInitDecision{SystemPrompt: systemPrompt, AllowedTools: slices.Clone(allowedTools)}
	if runtime == nil {
		return decision, nil
	}
	extensionDecision := runtime.DispatchAgentInitDecision(ctx, callContext, systemPrompt, allowedTools)
	return AgentInitDecision{
		SystemPrompt:  extensionDecision.SystemPrompt,
		AllowedTools:  extensionDecision.AllowedTools,
		ToolsModified: extensionDecision.ToolsModified,
	}, nil
}

// DispatchTurnEnd applies turn.end extension handlers.
func (e *LocalEnvironment) DispatchTurnEnd(ctx context.Context, finalOutput string, turnCount int) error {
	runtime, callContext := e.runContext()
	if runtime != nil {
		runtime.DispatchTurnEnd(ctx, callContext, finalOutput, turnCount)
	}
	return nil
}

// DispatchAgentEnd applies agent.end extension handlers.
func (e *LocalEnvironment) DispatchAgentEnd(ctx context.Context, messages []llmtypes.Message) ([]string, error) {
	runtime, callContext := e.runContext()
	if runtime == nil {
		return nil, nil
	}
	return runtime.DispatchAgentEnd(ctx, callContext, messages), nil
}

// DispatchToolCall applies tool.call extension policy without executing the tool.
func (e *LocalEnvironment) DispatchToolCall(ctx context.Context, request ToolRequest) (ToolCallDecision, error) {
	runtime, callContext := e.runContext()
	if runtime == nil {
		return ToolCallDecision{Input: request.Input}, nil
	}
	decision := runtime.DispatchToolCall(ctx, callContext, request.Name, request.Input, request.ToolCallID)
	return ToolCallDecision{Blocked: decision.Blocked, Reason: decision.Reason, Input: decision.Input}, nil
}

// DispatchToolUpdate applies tool.update extension policy without executing the tool.
func (e *LocalEnvironment) DispatchToolUpdate(ctx context.Context, request ToolOutputRequest) (ToolOutputDecision, error) {
	runtime, callContext := e.runContext()
	if runtime == nil {
		return ToolOutputDecision{StructuredResult: request.StructuredResult, Accepted: true}, nil
	}
	structured, modified, accepted := runtime.DispatchToolUpdate(ctx, callContext, request.Name, request.Input, request.ToolCallID, request.StructuredResult)
	return ToolOutputDecision{StructuredResult: structured, Modified: modified, Accepted: accepted}, nil
}

// DispatchToolResult applies tool.result extension policy without executing the tool.
func (e *LocalEnvironment) DispatchToolResult(ctx context.Context, request ToolOutputRequest) (ToolOutputDecision, error) {
	runtime, callContext := e.runContext()
	if runtime == nil {
		return ToolOutputDecision{StructuredResult: request.StructuredResult, Accepted: true}, nil
	}
	structured, modified := runtime.DispatchToolResult(ctx, callContext, request.Name, request.Input, request.ToolCallID, request.StructuredResult)
	return ToolOutputDecision{StructuredResult: structured, Modified: modified, Accepted: true}, nil
}

// CanStreamToolUpdates reports whether transient updates can preserve extension policy.
func (e *LocalEnvironment) CanStreamToolUpdates() bool {
	runtime := e.extensionRuntime()
	return runtime == nil || runtime.CanStreamToolUpdates()
}

// ExecuteTool executes a tool against the pinned state and applies the complete extension lifecycle.
func (e *LocalEnvironment) ExecuteTool(ctx context.Context, request ToolRequest, updates ToolUpdateSink) (ToolExecution, error) {
	state, _, _ := e.executionContext()
	spec := e.runSpec()
	effectiveInput := request.Input
	if tools.IsControlPlaneTool(request.Name) {
		result := tooltypes.BaseToolResult{Error: "control-plane tool cannot execute in the workspace environment: " + request.Name}
		structured := result.StructuredData()
		structured.ToolName = request.Name
		return ToolExecution{Input: effectiveInput, Result: result, StructuredResult: structured}, nil
	}
	if state == nil {
		result := tooltypes.BaseToolResult{Error: "agent environment is not open"}
		structured := result.StructuredData()
		structured.ToolName = request.Name
		return ToolExecution{Input: effectiveInput, Result: result, StructuredResult: structured}, nil
	}
	decision, err := e.DispatchToolCall(ctx, request)
	if err != nil {
		return ToolExecution{}, err
	}
	if decision.Blocked {
		result := tooltypes.NewBlockedToolResult(request.Name, decision.Reason)
		outputDecision, err := e.DispatchToolResult(ctx, ToolOutputRequest{
			Name:             request.Name,
			Input:            decision.Input,
			ToolCallID:       request.ToolCallID,
			StructuredResult: result.StructuredData(),
		})
		if err != nil {
			return ToolExecution{}, err
		}
		return ToolExecution{Input: decision.Input, Result: result, StructuredResult: outputDecision.StructuredResult, Modified: outputDecision.Modified}, nil
	}
	effectiveInput = decision.Input
	if !environmentToolAllowed(spec.Config, request.Name) {
		result := tooltypes.NewBlockedToolResult(request.Name, "tool is not allowed by the active workspace command")
		outputDecision, err := e.DispatchToolResult(ctx, ToolOutputRequest{
			Name:             request.Name,
			Input:            effectiveInput,
			ToolCallID:       request.ToolCallID,
			StructuredResult: result.StructuredData(),
		})
		if err != nil {
			return ToolExecution{}, err
		}
		return ToolExecution{Input: effectiveInput, Result: result, StructuredResult: outputDecision.StructuredResult, Modified: outputDecision.Modified}, nil
	}
	if request.Name == "bash" && len(spec.Config.AllowedCommands) > 0 {
		validator := tools.NewBashToolWithTimeout(spec.Config.AllowedCommands, spec.Config.EnableFSSearchTools, spec.Config.BashTimeout())
		if err := validator.ValidateInput(state, effectiveInput); err != nil {
			result := tooltypes.BaseToolResult{Error: err.Error()}
			structured := result.StructuredData()
			structured.ToolName = request.Name
			outputDecision, dispatchErr := e.DispatchToolResult(ctx, ToolOutputRequest{
				Name:             request.Name,
				Input:            effectiveInput,
				ToolCallID:       request.ToolCallID,
				StructuredResult: structured,
			})
			if dispatchErr != nil {
				return ToolExecution{}, dispatchErr
			}
			return ToolExecution{Input: effectiveInput, Result: result, StructuredResult: outputDecision.StructuredResult, Modified: outputDecision.Modified}, nil
		}
	}

	var onUpdate tooltypes.ToolUpdateCallback
	if updates != nil && e.CanStreamToolUpdates() {
		onUpdate = func(result tooltypes.ToolResult) {
			if result == nil {
				return
			}
			structured := result.StructuredData()
			if structured.ToolName == "" || structured.ToolName == "unknown" {
				structured.ToolName = request.Name
			}
			outputDecision, err := e.DispatchToolUpdate(ctx, ToolOutputRequest{
				Name:             request.Name,
				Input:            effectiveInput,
				ToolCallID:       request.ToolCallID,
				StructuredResult: structured,
			})
			if err != nil {
				return
			}
			if outputDecision.Accepted {
				updates(ToolUpdate{Result: result, StructuredResult: outputDecision.StructuredResult, Modified: outputDecision.Modified})
			}
		}
	}

	result := tools.RunToolWithUpdates(ctx, state, request.Name, effectiveInput, onUpdate)
	structured := result.StructuredData()
	if structured.ToolName == "" || structured.ToolName == "unknown" {
		structured.ToolName = request.Name
	}
	outputDecision, err := e.DispatchToolResult(ctx, ToolOutputRequest{
		Name:             request.Name,
		Input:            effectiveInput,
		ToolCallID:       request.ToolCallID,
		StructuredResult: structured,
	})
	if err != nil {
		return ToolExecution{}, err
	}
	return ToolExecution{Input: effectiveInput, Result: result, StructuredResult: outputDecision.StructuredResult, Modified: outputDecision.Modified}, nil
}

// Close releases the pinned run snapshot. It does not own or close the persistent extension runtime.
func (e *LocalEnvironment) Close(_ context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = nil
	e.manifest = Manifest{}
	e.spec = RunSpec{}
	e.opened = false
	return nil
}

func (e *LocalEnvironment) extensionRuntime() *extensions.Runtime {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.extensions
}

func (e *LocalEnvironment) runContext() (*extensions.Runtime, extensions.ExtensionCallContext) {
	if e == nil {
		return nil, extensions.ExtensionCallContext{InvokedBy: "main"}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.extensions, extensionCallContext(e.spec)
}

func (e *LocalEnvironment) executionContext() (tooltypes.State, *extensions.Runtime, extensions.ExtensionCallContext) {
	if e == nil {
		return nil, nil, extensions.ExtensionCallContext{InvokedBy: "main"}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state, e.extensions, extensionCallContext(e.spec)
}

func (e *LocalEnvironment) runSpec() RunSpec {
	if e == nil {
		return RunSpec{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.spec.Clone()
}

func (e *LocalEnvironment) pinnedCommands(name string) ([]slashcommands.Command, bool) {
	if e == nil {
		return nil, false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.opened {
		return nil, false
	}
	commands := make([]slashcommands.Command, 0, 1)
	for _, command := range e.manifest.Commands {
		if command.Name == name {
			commands = append(commands, command)
		}
	}
	return commands, true
}

func environmentToolAllowed(config llmtypes.Config, name string) bool {
	if len(config.AllowedTools) == 0 {
		return true
	}
	for _, allowed := range config.AllowedTools {
		if strings.TrimSpace(allowed) == name {
			return true
		}
	}
	return config.EnableFSSearchTools && (name == "grep_tool" || name == "glob_tool")
}

func snapshotManifest(ctx context.Context, state tooltypes.State, runtime *extensions.Runtime) Manifest {
	if state == nil {
		return Manifest{}
	}
	availableTools := state.Tools()
	definitions := make([]ToolDefinition, 0, len(availableTools))
	for _, tool := range availableTools {
		if tool == nil {
			continue
		}
		definitions = append(definitions, ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tooltypes.JSONSchemaForTool(tool),
			Placement:   placementForTool(tool.Name()),
			Tool:        tool,
		})
	}

	commands := workspaceCommands(ctx, state.WorkingDirectory(), runtime)
	return Manifest{
		WorkingDirectory: state.WorkingDirectory(),
		Contexts:         maps.Clone(state.DiscoverContexts()),
		Tools:            definitions,
		Commands:         commands,
	}
}

func workspaceCommands(ctx context.Context, workingDirectory string, runtime *extensions.Runtime) []slashcommands.Command {
	processor, err := fragments.NewFragmentProcessor(fragments.WithDefaultDirsForCWD(workingDirectory))
	if err != nil {
		return extensions.SlashCommands(runtimeCommands(runtime))
	}
	commands := slashcommands.List(ctx, processor)
	filtered := commands[:0]
	for _, command := range commands {
		if command.Name != goals.SlashCommandName && command.Name != slashcommands.RenameCommandName {
			filtered = append(filtered, command)
		}
	}
	filtered = append(filtered, extensions.SlashCommands(runtimeCommands(runtime))...)
	return slices.Clone(filtered)
}

func runtimeCommands(runtime *extensions.Runtime) []extensions.Command {
	if runtime == nil {
		return nil
	}
	return runtime.Commands()
}

func placementForTool(name string) ToolPlacement {
	if tools.IsControlPlaneTool(name) {
		return ToolPlacementControlPlane
	}
	return ToolPlacementEnvironment
}

func extensionCallContext(spec RunSpec) extensions.ExtensionCallContext {
	invokedBy := strings.TrimSpace(spec.InvokedBy)
	if invokedBy == "" {
		invokedBy = "main"
	}
	recipeName := spec.Config.RecipeName
	if recipeName == "" && spec.Metadata != nil {
		recipeName, _ = spec.Metadata["recipe_name"].(string)
	}
	return extensions.ExtensionCallContext{
		ConversationID: spec.ConversationID,
		CWD:            spec.Config.WorkingDirectory,
		Provider:       spec.Config.Provider,
		Model:          spec.Config.Model,
		Profile:        spec.Config.Profile,
		RecipeName:     recipeName,
		InvokedBy:      invokedBy,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type snapshotState struct {
	base       tooltypes.State
	basicTools []tooltypes.Tool
	tools      []tooltypes.Tool
	contexts   map[string]string
	workingDir string
}

func newSnapshotState(base tooltypes.State, manifest Manifest) tooltypes.State {
	if base == nil {
		return nil
	}
	return &snapshotState{
		base:       base,
		basicTools: slices.Clone(base.BasicTools()),
		tools:      manifest.AvailableTools(),
		contexts:   maps.Clone(manifest.Contexts),
		workingDir: manifest.WorkingDirectory,
	}
}

func (s *snapshotState) BasicTools() []tooltypes.Tool        { return slices.Clone(s.basicTools) }
func (s *snapshotState) Tools() []tooltypes.Tool             { return slices.Clone(s.tools) }
func (s *snapshotState) DiscoverContexts() map[string]string { return maps.Clone(s.contexts) }
func (s *snapshotState) GetLLMConfig() any                   { return s.base.GetLLMConfig() }
func (s *snapshotState) WorkingDirectory() string            { return s.workingDir }
func (s *snapshotState) LockFile(path string)                { s.base.LockFile(path) }
func (s *snapshotState) UnlockFile(path string)              { s.base.UnlockFile(path) }
