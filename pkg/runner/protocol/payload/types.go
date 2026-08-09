// Package payload defines runner application payloads that depend on Kodelet's
// model, tool, slash-command, and extension types. The parent protocol package
// intentionally remains a transport and lease-protocol leaf.
package payload

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// ContextFile is one pinned model-facing context input.
type ContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Digest  string `json:"digest"`
}

// ToolDefinition is one serializable model-facing runner tool.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Placement   string         `json:"placement"`
}

// SkillDefinition describes a skill owned and pinned by the runner.
type SkillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Digest      string `json:"digest"`
}

// EnvironmentConfig is the sanitized runner-owned configuration projection.
type EnvironmentConfig struct {
	AllowedCommands     []string          `json:"allowedCommands,omitempty"`
	ToolMode            llmtypes.ToolMode `json:"toolMode,omitempty"`
	EnableFSSearchTools bool              `json:"enableFSSearchTools,omitempty"`
	SystemPromptPath    string            `json:"systemPromptPath,omitempty"`
	SystemPromptContent string            `json:"systemPromptContent,omitempty"`
	SystemPromptArgs    map[string]string `json:"systemPromptArgs,omitempty"`
}

// EnvironmentCapabilities advertises optional runner behavior.
type EnvironmentCapabilities struct {
	ToolUpdates        bool `json:"toolUpdates"`
	InteractiveUI      bool `json:"interactiveUI"`
	PersistentSurfaces bool `json:"persistentSurfaces"`
	Commands           bool `json:"commands"`
}

// Manifest is the immutable wire snapshot returned by run.open.
type Manifest struct {
	ProtocolVersion     int                     `json:"protocolVersion"`
	RunnerID            string                  `json:"runnerId"`
	RunID               string                  `json:"runId"`
	Generation          int64                   `json:"generation"`
	Digest              string                  `json:"digest"`
	WorkingDirectory    string                  `json:"workingDirectory"`
	ContextFiles        []ContextFile           `json:"contextFiles"`
	Tools               []ToolDefinition        `json:"tools"`
	Skills              []SkillDefinition       `json:"skills"`
	Commands            []slashcommands.Command `json:"commands"`
	Config              EnvironmentConfig       `json:"config"`
	ExtensionGeneration int64                   `json:"extensionGeneration"`
	Capabilities        EnvironmentCapabilities `json:"capabilities"`
}

// ComputeManifestDigest returns a stable digest when callers provide deterministically ordered slices.
func ComputeManifestDigest(manifest Manifest) (string, error) {
	manifest.RunnerID = ""
	manifest.RunID = ""
	manifest.Generation = 0
	manifest.Digest = ""
	manifest.ExtensionGeneration = 0
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", errors.Wrap(err, "failed to encode runner manifest")
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

// CommandExecuteParams routes a workspace or extension slash command.
type CommandExecuteParams struct {
	RunID   string `json:"runId"`
	Message string `json:"message"`
}

// CommandExecuteResult is the normalized command routing result.
type CommandExecuteResult struct {
	Matched         bool     `json:"matched"`
	Action          string   `json:"action,omitempty"`
	CommandName     string   `json:"commandName,omitempty"`
	Response        string   `json:"response,omitempty"`
	Prompt          string   `json:"prompt,omitempty"`
	Display         string   `json:"display,omitempty"`
	RecipeName      string   `json:"recipeName,omitempty"`
	AllowedTools    []string `json:"allowedTools,omitempty"`
	AllowedCommands []string `json:"allowedCommands,omitempty"`
}

// LifecycleEvent identifies one proxied extension lifecycle operation.
type LifecycleEvent string

const (
	LifecycleUserMessage LifecycleEvent = "user.message"
	LifecycleAgentStart  LifecycleEvent = "agent.start"
	LifecycleTurnStart   LifecycleEvent = "turn.start"
	LifecycleAgentInit   LifecycleEvent = "agent.init"
	LifecycleTurnEnd     LifecycleEvent = "turn.end"
	LifecycleAgentEnd    LifecycleEvent = "agent.end"
	LifecycleToolCall    LifecycleEvent = "tool.call"
	LifecycleToolUpdate  LifecycleEvent = "tool.update"
	LifecycleToolResult  LifecycleEvent = "tool.result"
)

// LifecycleDispatchParams carries the typed union required by proxied lifecycle events.
type LifecycleDispatchParams struct {
	RunID            string                          `json:"runId"`
	Event            LifecycleEvent                  `json:"event"`
	Message          string                          `json:"message,omitempty"`
	SystemPrompt     string                          `json:"systemPrompt,omitempty"`
	AllowedTools     []string                        `json:"allowedTools,omitempty"`
	TurnNumber       int                             `json:"turnNumber,omitempty"`
	FinalOutput      string                          `json:"finalOutput,omitempty"`
	TurnCount        int                             `json:"turnCount,omitempty"`
	Messages         []llmtypes.Message              `json:"messages,omitempty"`
	ToolName         string                          `json:"toolName,omitempty"`
	ToolInput        json.RawMessage                 `json:"toolInput,omitempty"`
	ToolCallID       string                          `json:"toolCallId,omitempty"`
	StructuredResult *tooltypes.StructuredToolResult `json:"structuredResult,omitempty"`
}

// LifecycleDispatchResult is the aggregate effective extension decision.
type LifecycleDispatchResult struct {
	Message          string                          `json:"message,omitempty"`
	Blocked          bool                            `json:"blocked,omitempty"`
	Reason           string                          `json:"reason,omitempty"`
	SystemPrompt     string                          `json:"systemPrompt,omitempty"`
	AllowedTools     []string                        `json:"allowedTools,omitempty"`
	ToolsModified    bool                            `json:"toolsModified,omitempty"`
	FollowUpMessages []string                        `json:"followUpMessages,omitempty"`
	ToolInput        json.RawMessage                 `json:"toolInput,omitempty"`
	StructuredResult *tooltypes.StructuredToolResult `json:"structuredResult,omitempty"`
	Modified         bool                            `json:"modified,omitempty"`
	Accepted         bool                            `json:"accepted,omitempty"`
}

// ToolExecuteParams asks the runner to perform the complete local tool lifecycle.
type ToolExecuteParams struct {
	RunID       string          `json:"runId"`
	ToolCallID  string          `json:"toolCallId"`
	Name        string          `json:"name"`
	Input       json.RawMessage `json:"input"`
	WantUpdates bool            `json:"wantUpdates,omitempty"`
}

// ToolResult is the serializable authoritative or transient result sent over the runner link.
type ToolResult struct {
	AssistantFacing string                            `json:"assistantFacing"`
	Error           string                            `json:"error,omitempty"`
	Structured      tooltypes.StructuredToolResult    `json:"structured"`
	ContentParts    []tooltypes.ToolResultContentPart `json:"contentParts,omitempty"`
}

// ToolExecuteResult is the authoritative final runner tool result.
type ToolExecuteResult struct {
	Input    json.RawMessage `json:"input"`
	Result   ToolResult      `json:"result"`
	Modified bool            `json:"modified,omitempty"`
}

// ToolUpdateParams is a replaceable transient tool snapshot linked to one request ID.
type ToolUpdateParams struct {
	RunID      string     `json:"runId"`
	RequestID  string     `json:"requestId"`
	ToolCallID string     `json:"toolCallId"`
	Sequence   uint64     `json:"sequence"`
	Result     ToolResult `json:"result"`
	Modified   bool       `json:"modified,omitempty"`
}

// ExtensionOwner preserves extension process identity across the runner boundary.
type ExtensionOwner struct {
	ExtensionID string `json:"extensionId"`
	Generation  uint64 `json:"generation"`
}

type UIInputParams struct {
	RunID   string                    `json:"runId"`
	Owner   ExtensionOwner            `json:"owner"`
	Request extensions.UIInputRequest `json:"request"`
}

type UIConfirmParams struct {
	RunID   string                      `json:"runId"`
	Owner   ExtensionOwner              `json:"owner"`
	Request extensions.UIConfirmRequest `json:"request"`
}

type UISelectParams struct {
	RunID   string                     `json:"runId"`
	Owner   ExtensionOwner             `json:"owner"`
	Request extensions.UISelectRequest `json:"request"`
}

type UINotifyParams struct {
	RunID   string                     `json:"runId"`
	Owner   ExtensionOwner             `json:"owner"`
	Request extensions.UINotifyRequest `json:"request"`
}

type UIWidgetSetParams struct {
	RunID   string                        `json:"runId"`
	Owner   ExtensionOwner                `json:"owner"`
	Request extensions.UIWidgetSetRequest `json:"request"`
}

type UIWidgetFrameParams struct {
	RunID   string                          `json:"runId"`
	Owner   ExtensionOwner                  `json:"owner"`
	Request extensions.UIWidgetFrameRequest `json:"request"`
}

type UIWidgetRemoveParams struct {
	RunID   string                           `json:"runId"`
	Owner   ExtensionOwner                   `json:"owner"`
	Request extensions.UIWidgetRemoveRequest `json:"request"`
}

type UITranscriptAppendParams struct {
	RunID   string                               `json:"runId"`
	Owner   ExtensionOwner                       `json:"owner"`
	Request extensions.UITranscriptAppendRequest `json:"request"`
}

type UISurfaceOpenParams struct {
	RunID   string                          `json:"runId"`
	Owner   ExtensionOwner                  `json:"owner"`
	Request extensions.UISurfaceOpenRequest `json:"request"`
}

type UISurfaceFrameParams struct {
	RunID   string                           `json:"runId"`
	Owner   ExtensionOwner                   `json:"owner"`
	Request extensions.UISurfaceFrameRequest `json:"request"`
}

type UISurfaceCloseParams struct {
	RunID   string                           `json:"runId"`
	Owner   ExtensionOwner                   `json:"owner"`
	Request extensions.UISurfaceCloseRequest `json:"request"`
}

type UISurfaceInputParams struct {
	RunID     string                                `json:"runId"`
	Owner     ExtensionOwner                        `json:"owner"`
	Lifecycle uint64                                `json:"lifecycle"`
	Request   extensions.UISurfaceInputNotification `json:"request"`
}

type UISurfaceResizeParams struct {
	RunID     string                                 `json:"runId"`
	Owner     ExtensionOwner                         `json:"owner"`
	Lifecycle uint64                                 `json:"lifecycle"`
	Request   extensions.UISurfaceResizeNotification `json:"request"`
}
