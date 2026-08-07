// Package protocol defines the versioned JSON-RPC protocol shared by Kodelet
// control planes and workspace-bound runners.
package protocol

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

const (
	// Version is the initial runner application protocol version.
	Version = 1
	// JSONRPCVersion is the JSON-RPC wire version used by the runner protocol.
	JSONRPCVersion = "2.0"
	// Subprotocol is required during the WebSocket upgrade.
	Subprotocol = "kodelet.runner.v1.jsonrpc"
	// Endpoint is the control-plane WebSocket endpoint used by runners.
	Endpoint = "/api/runner/v1/connect"
)

const (
	MethodRunnerRegister        = "runner.register"
	MethodRunnerHeartbeat       = "runner.heartbeat"
	MethodRunnerManifestChanged = "runner.manifestChanged"
	MethodRunnerGoodbye         = "runner.goodbye"
	MethodRunOpen               = "run.open"
	MethodRunClose              = "run.close"
	MethodRunCancel             = "run.cancel"
	MethodRunEnvironmentError   = "run.environmentError"
	MethodCommandExecute        = "command.execute"
	MethodLifecycleDispatch     = "lifecycle.dispatch"
	MethodToolExecute           = "tool.execute"
	MethodToolUpdate            = "tool.update"
	MethodUIInput               = "ui.input"
	MethodUIConfirm             = "ui.confirm"
	MethodUISelect              = "ui.select"
	MethodUINotify              = "ui.notify"
	MethodUIWidgetSet           = "ui.widget.set"
	MethodUIWidgetFrame         = "ui.widget.frame"
	MethodUIWidgetRemove        = "ui.widget.remove"
	MethodUITranscriptAppend    = "ui.transcript.append"
	MethodUISurfaceOpen         = "ui.surface.open"
	MethodUISurfaceFrame        = "ui.surface.frame"
	MethodUISurfaceClose        = "ui.surface.close"
	MethodUISurfaceInput        = "ui.surface.input"
	MethodUISurfaceResize       = "ui.surface.resize"
	MethodOperationCancel       = "operation.cancel"
)

const (
	ErrorCodeParseError     = -32700
	ErrorCodeInvalidRequest = -32600
	ErrorCodeMethodNotFound = -32601
	ErrorCodeInvalidParams  = -32602
	ErrorCodeInternal       = -32603
	ErrorCodeUnavailable    = -32000
	ErrorCodeConflict       = -32001
	ErrorCodeStale          = -32002
	ErrorCodeBusy           = -32003
)

// Message is the small JSON-native envelope used for requests, responses, and notifications.
// Runner request identifiers are strings with an origin-specific prefix.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *string         `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("runner rpc error %d: %s", e.Code, e.Message)
}

// DecodeMessage decodes and validates one complete WebSocket text frame.
func DecodeMessage(payload []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(payload, &message); err != nil {
		return Message{}, errors.Wrap(err, "failed to decode runner rpc message")
	}
	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// Validate checks the JSON-RPC envelope shape used by this first-party protocol.
func (m Message) Validate() error {
	if m.JSONRPC != JSONRPCVersion {
		return errors.Errorf("unsupported jsonrpc version %q", m.JSONRPC)
	}
	if m.ID != nil && strings.TrimSpace(*m.ID) == "" {
		return errors.New("rpc id must not be empty")
	}
	if strings.TrimSpace(m.Method) != "" {
		if len(m.Result) > 0 || m.Error != nil {
			return errors.New("rpc request cannot contain result or error")
		}
		return nil
	}
	if m.ID == nil {
		return errors.New("rpc response id is required")
	}
	if len(m.Result) > 0 && m.Error != nil {
		return errors.New("rpc response cannot contain both result and error")
	}
	if len(m.Result) == 0 && m.Error == nil {
		return errors.New("rpc response must contain result or error")
	}
	return nil
}

// Host describes one stable runner installation and its mutable display metadata.
type Host struct {
	InstanceID string `json:"instanceId"`
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	PID        int    `json:"pid,omitempty"`
}

// Workspace describes the one canonical workspace bound to a runner process.
type Workspace struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// RegisterParams is the first request sent by a runner connection.
type RegisterParams struct {
	ProtocolVersions []int     `json:"protocolVersions"`
	RunnerID         string    `json:"runnerId,omitempty"`
	DisplayName      string    `json:"displayName,omitempty"`
	Host             Host      `json:"host"`
	Workspace        Workspace `json:"workspace"`
	KodeletVersion   string    `json:"kodeletVersion"`
	ManifestDigest   string    `json:"manifestDigest,omitempty"`
}

// Validate checks registration identity and version negotiation fields.
func (p RegisterParams) Validate() error {
	if !SupportsVersion(p.ProtocolVersions, Version) {
		return errors.Errorf("runner does not support protocol version %d", Version)
	}
	if strings.TrimSpace(p.Host.InstanceID) == "" {
		return errors.New("host.instanceId is required")
	}
	if strings.TrimSpace(p.Workspace.Path) == "" {
		return errors.New("workspace.path is required")
	}
	if strings.TrimSpace(p.Workspace.Name) == "" {
		return errors.New("workspace.name is required")
	}
	return nil
}

// SupportsVersion reports whether a peer advertised a protocol version.
func SupportsVersion(versions []int, version int) bool {
	for _, candidate := range versions {
		if candidate == version {
			return true
		}
	}
	return false
}

// RegisterResult establishes the stable runner ID and live connection generation.
type RegisterResult struct {
	RunnerID            string `json:"runnerId"`
	ProtocolVersion     int    `json:"protocolVersion"`
	ConnectionID        string `json:"connectionId"`
	Generation          int64  `json:"generation"`
	HeartbeatIntervalMS int64  `json:"heartbeatIntervalMs"`
}

// RunnerState is the application-level availability reported by heartbeats.
type RunnerState string

const (
	RunnerStateIdle     RunnerState = "idle"
	RunnerStateRunning  RunnerState = "running"
	RunnerStateStopping RunnerState = "stopping"
	RunnerStateError    RunnerState = "error"
)

// HeartbeatParams reports application health separately from WebSocket liveness.
type HeartbeatParams struct {
	RunnerID       string      `json:"runnerId"`
	Generation     int64       `json:"generation"`
	State          RunnerState `json:"state"`
	ActiveRunID    string      `json:"activeRunId,omitempty"`
	ManifestDigest string      `json:"manifestDigest,omitempty"`
}

// ManifestChangedParams reports an idle-manifest digest transition.
type ManifestChangedParams struct {
	RunnerID       string `json:"runnerId"`
	Generation     int64  `json:"generation"`
	ManifestDigest string `json:"manifestDigest"`
}

// GoodbyeParams reports an intentional runner disconnect.
type GoodbyeParams struct {
	RunnerID   string `json:"runnerId"`
	Generation int64  `json:"generation"`
	Reason     string `json:"reason,omitempty"`
}

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
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", errors.Wrap(err, "failed to encode runner manifest")
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

// AgentDescriptor carries only provider-sensitive identifiers needed by runner resources.
type AgentDescriptor struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Profile    string `json:"profile,omitempty"`
	RecipeName string `json:"recipeName,omitempty"`
	InvokedBy  string `json:"invokedBy,omitempty"`
}

// ClientCapabilities describes the interactive client attached to a run.
type ClientCapabilities struct {
	InteractiveUI      bool `json:"interactiveUI"`
	PersistentSurfaces bool `json:"persistentSurfaces"`
}

// RunOpenParams asks a runner to pin one environment snapshot.
type RunOpenParams struct {
	RunID              string             `json:"runId"`
	ConversationID     string             `json:"conversationId"`
	Agent              AgentDescriptor    `json:"agent"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ReservedToolNames  []string           `json:"reservedToolNames"`
}

func (p RunOpenParams) Validate() error {
	if strings.TrimSpace(p.RunID) == "" {
		return errors.New("runId is required")
	}
	if strings.TrimSpace(p.ConversationID) == "" {
		return errors.New("conversationId is required")
	}
	return nil
}

// RunCloseParams releases a pinned run environment.
type RunCloseParams struct {
	RunID string `json:"runId"`
}

// RunCancelParams cancels active runner operations for one run.
type RunCancelParams struct {
	RunID  string `json:"runId"`
	Reason string `json:"reason,omitempty"`
}

// EnvironmentErrorParams reports a runner-side asynchronous run failure.
type EnvironmentErrorParams struct {
	RunID   string `json:"runId"`
	Message string `json:"message"`
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

// OperationCancelParams cancels one in-flight JSON-RPC request by its wire ID.
type OperationCancelParams struct {
	RequestID string `json:"requestId"`
}

// ExtensionOwner preserves extension process identity across the runner boundary.
type ExtensionOwner struct {
	ExtensionID string `json:"extensionId"`
	Generation  uint64 `json:"generation"`
}

type UIInputParams struct {
	RunID   string                    `json:"runId"`
	Request extensions.UIInputRequest `json:"request"`
}

type UIConfirmParams struct {
	RunID   string                      `json:"runId"`
	Request extensions.UIConfirmRequest `json:"request"`
}

type UISelectParams struct {
	RunID   string                     `json:"runId"`
	Request extensions.UISelectRequest `json:"request"`
}

type UINotifyParams struct {
	RunID   string                     `json:"runId"`
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
