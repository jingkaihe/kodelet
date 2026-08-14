// Package agentenv defines the environment boundary used by Kodelet's central agent loop.
package agentenv

import (
	"context"
	"maps"
	"slices"

	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ToolPlacement identifies where a host-executed tool runs.
type ToolPlacement string

const (
	// ToolPlacementControlPlane identifies tools that execute beside central conversation state.
	ToolPlacementControlPlane ToolPlacement = "control_plane"
	// ToolPlacementEnvironment identifies tools that execute in the workspace environment.
	ToolPlacementEnvironment ToolPlacement = "environment"
)

// ToolDefinition is one model-facing tool and its execution placement.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Placement   ToolPlacement  `json:"placement"`
	Tool        tooltypes.Tool `json:"-"`
}

// Manifest is the immutable environment snapshot pinned to one top-level run.
type Manifest struct {
	WorkingDirectory string                  `json:"workingDirectory"`
	Contexts         map[string]string       `json:"contexts"`
	Tools            []ToolDefinition        `json:"tools"`
	Commands         []slashcommands.Command `json:"commands"`
	Config           *EnvironmentConfig      `json:"config,omitempty"`
}

// EnvironmentConfig is the runner-owned configuration projection pinned with a manifest.
type EnvironmentConfig struct {
	AllowedCommands     []string
	ToolMode            llmtypes.ToolMode
	EnableFSSearchTools bool
	SystemPromptPath    string
	SystemPromptContent string
	SystemPromptArgs    map[string]string
	SystemInformation   *llmtypes.SystemInformation
}

// Clone returns a defensive copy of the configuration projection.
func (c *EnvironmentConfig) Clone() *EnvironmentConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.AllowedCommands = slices.Clone(c.AllowedCommands)
	cloned.SystemPromptArgs = maps.Clone(c.SystemPromptArgs)
	cloned.SystemInformation = c.SystemInformation.Clone()
	return &cloned
}

// Clone returns a defensive copy of the manifest collections.
func (m Manifest) Clone() Manifest {
	toolDefinitions := make([]ToolDefinition, len(m.Tools))
	for i, definition := range m.Tools {
		toolDefinitions[i] = definition
		toolDefinitions[i].InputSchema = cloneJSONMap(definition.InputSchema)
	}
	return Manifest{
		WorkingDirectory: m.WorkingDirectory,
		Contexts:         maps.Clone(m.Contexts),
		Tools:            toolDefinitions,
		Commands:         slices.Clone(m.Commands),
		Config:           m.Config.Clone(),
	}
}

// ToolDefinition returns a pinned tool definition by name.
func (m Manifest) ToolDefinition(name string) (ToolDefinition, bool) {
	for _, definition := range m.Tools {
		if definition.Name == name {
			definition.InputSchema = cloneJSONMap(definition.InputSchema)
			return definition, true
		}
	}
	return ToolDefinition{}, false
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = cloneJSONValue(item)
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneJSONValue(item)
		}
		return cloned
	case []string:
		return slices.Clone(typed)
	case map[string]string:
		return maps.Clone(typed)
	default:
		return value
	}
}

// AvailableTools returns the pinned model-facing tool implementations.
func (m Manifest) AvailableTools() []tooltypes.Tool {
	result := make([]tooltypes.Tool, 0, len(m.Tools))
	for _, definition := range m.Tools {
		if definition.Tool != nil {
			result = append(result, definition.Tool)
		}
	}
	return result
}

// ToolNames returns the pinned model-facing tool names.
func (m Manifest) ToolNames() []string {
	result := make([]string, 0, len(m.Tools))
	for _, definition := range m.Tools {
		if definition.Name != "" {
			result = append(result, definition.Name)
		}
	}
	return result
}

// RunSpec describes the central run opening this environment.
type RunSpec struct {
	ConversationID     string
	EnvironmentProfile string
	Config             llmtypes.Config
	Metadata           map[string]any
	InvokedBy          string
}

// Clone returns a copy safe for retention by an environment implementation.
func (s RunSpec) Clone() RunSpec {
	s.Metadata = maps.Clone(s.Metadata)
	return s
}

// AgentInitDecision is the effective result of environment agent.init handlers.
type AgentInitDecision struct {
	SystemPrompt  string
	AllowedTools  []string
	ToolsModified bool
}

// ToolRequest describes one model-requested host tool execution.
type ToolRequest struct {
	Name       string
	Input      string
	ToolCallID string
}

// ToolUpdate is one transient post-policy tool result snapshot.
type ToolUpdate struct {
	Result           tooltypes.ToolResult
	StructuredResult tooltypes.StructuredToolResult
	Modified         bool
}

// ToolCallDecision is the effective pre-execution extension policy result.
type ToolCallDecision struct {
	Blocked bool
	Reason  string
	Input   string
}

// ToolOutputRequest describes a transient or final structured tool output.
type ToolOutputRequest struct {
	Name             string
	Input            string
	ToolCallID       string
	StructuredResult tooltypes.StructuredToolResult
}

// ToolOutputDecision is the effective post-policy tool output.
type ToolOutputDecision struct {
	StructuredResult tooltypes.StructuredToolResult
	Modified         bool
	Accepted         bool
}

// ToolUpdateSink receives transient tool result snapshots.
type ToolUpdateSink func(ToolUpdate)

// ToolExecution is the environment's authoritative result for one host tool call.
type ToolExecution struct {
	Input            string
	Result           tooltypes.ToolResult
	StructuredResult tooltypes.StructuredToolResult
	Modified         bool
}

// CommandAction describes how an environment handled a slash command.
type CommandAction string

const (
	// CommandActionRespond returns a direct response without invoking the model.
	CommandActionRespond CommandAction = "respond"
	// CommandActionRunAgent replaces the submitted command with an agent prompt.
	CommandActionRunAgent CommandAction = "run_agent"
)

// CommandRequest asks the environment to resolve a workspace or extension command.
type CommandRequest struct {
	Message string
	RunSpec RunSpec
}

// CommandResult is the normalized result of workspace command resolution.
type CommandResult struct {
	Matched         bool
	Action          CommandAction
	CommandName     string
	Response        string
	Prompt          string
	Display         string
	DisplayOverride bool
	RecipeName      string
	AllowedTools    []string
	AllowedCommands []string
}

// Environment is the run-scoped boundary between the central agent loop and its workspace resources.
type Environment interface {
	Open(ctx context.Context, spec RunSpec) (Manifest, error)
	IsOpen() bool
	Manifest() Manifest
	ExecuteCommand(ctx context.Context, request CommandRequest) (CommandResult, error)
	ProcessUserMessage(ctx context.Context, message string) (string, error)
	DispatchAgentStart(ctx context.Context) error
	DispatchTurnStart(ctx context.Context, turnNumber int) error
	ProcessAgentInit(ctx context.Context, systemPrompt string, allowedTools []string) (AgentInitDecision, error)
	DispatchTurnEnd(ctx context.Context, finalOutput string, turnCount int) error
	DispatchAgentEnd(ctx context.Context, messages []llmtypes.Message) ([]string, error)
	DispatchToolCall(ctx context.Context, request ToolRequest) (ToolCallDecision, error)
	DispatchToolUpdate(ctx context.Context, request ToolOutputRequest) (ToolOutputDecision, error)
	DispatchToolResult(ctx context.Context, request ToolOutputRequest) (ToolOutputDecision, error)
	CanStreamToolUpdates() bool
	ExecuteTool(ctx context.Context, request ToolRequest, updates ToolUpdateSink) (ToolExecution, error)
	Close(ctx context.Context) error
}

// StateProvider is implemented by local environments that expose their pinned tool state for compatibility.
// Provider loops must use Environment methods rather than depending on this optional interface.
type StateProvider interface {
	State() tooltypes.State
}

// ExtensionSetter is implemented by environments whose local extension runtime can be replaced between runs.
type ExtensionSetter interface {
	SetExtensions(runtime any)
}

// OutcomeCloser lets a remote environment record the terminal outcome of a run.
type OutcomeCloser interface {
	CloseWithError(ctx context.Context, runErr error) error
}
