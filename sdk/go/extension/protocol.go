package extension

import (
	"encoding/json"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
)

// ProtocolVersion is the extension protocol version implemented by this SDK.
const ProtocolVersion = "2026-05-30"

// RPCError is a JSON-RPC error. Host RPC failures are returned as *RPCError,
// and a handler may return one to choose the error code sent to Kodelet.
type RPCError = jsonrpc.Error

// NoTimeout disables the host timeout for a tool, command, or event handler.
// A zero timeout keeps the host default.
const NoTimeout time.Duration = -1

// InitializeParams is sent by Kodelet in extension.initialize.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Kodelet         map[string]any `json:"kodelet,omitempty"`
	Extension       ExtensionInfo  `json:"extension"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

// ExtensionInfo identifies the extension process to itself.
type ExtensionInfo struct {
	ID       string         `json:"id"`
	RunnerID string         `json:"runnerId,omitempty"`
	Config   map[string]any `json:"config,omitempty"`
	CWD      string         `json:"cwd,omitempty"`
	DataDir  string         `json:"dataDir,omitempty"`
}

// InitializeResult declares an extension's registrations.
type InitializeResult struct {
	Name          string                    `json:"name"`
	Version       string                    `json:"version,omitempty"`
	Profiles      []ProfileDeclaration      `json:"profiles,omitempty"`
	Tools         []ToolDeclaration         `json:"tools"`
	Commands      []CommandDeclaration      `json:"commands"`
	Shortcuts     []ShortcutDeclaration     `json:"shortcuts"`
	Subscriptions []SubscriptionDeclaration `json:"subscriptions"`
}

// ProfileDeclaration is a registered model profile.
type ProfileDeclaration struct {
	Name    string         `json:"name"`
	Options map[string]any `json:"options"`
	Hidden  bool           `json:"hidden"`
}

// ToolDeclaration is a registered tool.
type ToolDeclaration struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Group        string         `json:"group,omitempty"`
	Short        string         `json:"short,omitempty"`
	TimeoutInSec *float64       `json:"timeoutInSec,omitempty"`
}

// CommandDeclaration is a registered slash command or dynamic recipe.
type CommandDeclaration struct {
	Name         string         `json:"name"`
	Aliases      []string       `json:"aliases,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	Kind         CommandKind    `json:"kind,omitempty"`
	TimeoutInSec *float64       `json:"timeoutInSec,omitempty"`
}

// ShortcutDeclaration is a registered native TUI shortcut.
type ShortcutDeclaration struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
}

// SubscriptionDeclaration is the merged subscription for one event.
type SubscriptionDeclaration struct {
	Event        string   `json:"event"`
	Priority     int      `json:"priority"`
	TimeoutInSec *float64 `json:"timeoutInSec,omitempty"`
}

// CallInfo describes the conversation and workspace a handler runs for.
type CallInfo struct {
	SessionID      string
	RunnerID       string
	ConversationID string
	// UIScopeID is an opaque host scope used internally to route persistent UI.
	UIScopeID  string
	CWD        string
	Provider   string
	Model      string
	Profile    string
	RecipeName string
	InvokedBy  string
}

type callContextWire struct {
	SessionID      string `json:"sessionId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	UIScopeID      string `json:"uiScopeId,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Profile        string `json:"profile,omitempty"`
	RecipeName     string `json:"recipeName,omitempty"`
	InvokedBy      string `json:"invokedBy,omitempty"`
}

// CommandInvocation describes the prompt that invoked a command. Flag values
// are strings, booleans, or string slices.
type CommandInvocation struct {
	Raw         string         `json:"raw"`
	CommandName string         `json:"commandName"`
	Args        []string       `json:"args"`
	Flags       map[string]any `json:"flags"`
}

type executeToolParams struct {
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input,omitempty"`
	Context callContextWire `json:"context"`
}

type executeCommandParams struct {
	Name       string            `json:"name"`
	Input      json.RawMessage   `json:"input,omitempty"`
	Context    callContextWire   `json:"context"`
	Invocation CommandInvocation `json:"invocation"`
}

type executeShortcutParams struct {
	Key     string          `json:"key"`
	Context callContextWire `json:"context"`
}

type handleEventParams struct {
	ID      string          `json:"id"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Context callContextWire `json:"context"`
}

func timeoutSeconds(timeout time.Duration) *float64 {
	switch {
	case timeout == 0:
		return nil
	case timeout < 0:
		zero := 0.0
		return &zero
	default:
		seconds := timeout.Seconds()
		return &seconds
	}
}

func mergeTimeout(current, next *float64) *float64 {
	switch {
	case current != nil && *current == 0, next != nil && *next == 0:
		zero := 0.0
		return &zero
	case current == nil:
		return next
	case next == nil:
		return current
	case *next > *current:
		return next
	default:
		return current
	}
}

func capabilityValue(capabilities map[string]any, path ...string) any {
	var value any = capabilities
	for _, key := range path {
		record, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = record[key]
	}
	return value
}

func capabilityTrue(capabilities map[string]any, path ...string) bool {
	value, ok := capabilityValue(capabilities, path...).(bool)
	return ok && value
}

func capabilityVersion(capabilities map[string]any, path ...string) float64 {
	value, _ := capabilityValue(capabilities, path...).(float64)
	return value
}

func cloneJSON[T any](value T) (T, error) {
	var clone T
	data, err := json.Marshal(value)
	if err != nil {
		return clone, err
	}
	err = json.Unmarshal(data, &clone)
	return clone, err
}

func mustCloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	clone, err := cloneJSON(value)
	if err != nil {
		panic(err)
	}
	return clone
}
