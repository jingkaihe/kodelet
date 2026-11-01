package extension

import (
	"context"
	"encoding/json"
	"time"
)

// Event is a typed lifecycle event key whose payload decodes into P.
type Event[P any] struct {
	name string
}

// Name returns the wire event name.
func (e Event[P]) Name() string {
	return e.name
}

// CustomEvent returns a key for an event this SDK does not model.
func CustomEvent(name string) Event[map[string]any] {
	return Event[map[string]any]{name: name}
}

// Lifecycle events.
var (
	EventSessionStart      = Event[SessionStartEvent]{name: "session.start"}
	EventResourcesDiscover = Event[ResourcesDiscoverEvent]{name: "resources.discover"}
	EventUserMessage       = Event[UserMessageEvent]{name: "user.message"}
	EventAgentInit         = Event[AgentInitEvent]{name: "agent.init"}
	EventAgentStart        = Event[AgentStartEvent]{name: "agent.start"}
	EventTurnStart         = Event[TurnStartEvent]{name: "turn.start"}
	EventToolCall          = Event[ToolCallEvent]{name: "tool.call"}
	EventToolUpdate        = Event[ToolUpdateEvent]{name: "tool.update"}
	EventToolResult        = Event[ToolResultEvent]{name: "tool.result"}
	EventTurnEnd           = Event[TurnEndEvent]{name: "turn.end"}
	EventAgentEnd          = Event[AgentEndEvent]{name: "agent.end"}
	// EventAgentFinished is observational: results are ignored by the host.
	EventAgentFinished = Event[AgentFinishedEvent]{name: "agent.finished"}
	EventSessionEnd    = Event[SessionEndEvent]{name: "session.end"}
)

// EventHeader identifies an event delivery.
type EventHeader struct {
	ID    string `json:"id"`
	Event string `json:"event"`
}

// SessionStartEvent is delivered when a session starts.
type SessionStartEvent struct{ EventHeader }

// ResourcesDiscoverEvent is delivered when resources are discovered.
type ResourcesDiscoverEvent struct{ EventHeader }

// AgentStartEvent is delivered when an agent run starts.
type AgentStartEvent struct{ EventHeader }

// SessionEndEvent is delivered when a session ends or the host disconnects.
type SessionEndEvent struct{ EventHeader }

// UserMessageEvent carries a submitted user message.
type UserMessageEvent struct {
	EventHeader
	Message string `json:"message"`
}

// AgentInitEvent is delivered before a run; handlers may patch the system
// prompt or tool allowlist.
type AgentInitEvent struct {
	EventHeader
	SystemPrompt string `json:"systemPrompt,omitempty"`
	// AllowedTools is the current allowlist, including earlier patches.
	AllowedTools []string `json:"allowedTools,omitempty"`
}

// TurnStartEvent is delivered when a model turn starts.
type TurnStartEvent struct {
	EventHeader
	TurnNumber int `json:"turnNumber,omitempty"`
}

// TurnEndEvent is delivered when a model turn ends.
type TurnEndEvent struct {
	EventHeader
	Response   string `json:"response"`
	TurnNumber int    `json:"turnNumber,omitempty"`
}

// AgentEndEvent is delivered when a run ends.
type AgentEndEvent struct {
	EventHeader
	Messages []json.RawMessage `json:"messages,omitempty"`
}

// AgentFinishedEvent reports a run's termination outcome.
type AgentFinishedEvent struct {
	EventHeader
	// Status is "succeeded", "failed", or "canceled".
	Status string `json:"status"`
	// Reason is "completed", "error", "canceled", "deadline_exceeded", or "turn_limit".
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ToolCallDetails describes a tool invocation.
type ToolCallDetails struct {
	Name   string          `json:"name"`
	CallID string          `json:"callId,omitempty"`
	Input  json.RawMessage `json:"input"`
}

// DecodeInput decodes the tool input into v.
func (d ToolCallDetails) DecodeInput(v any) error {
	return json.Unmarshal(d.Input, v)
}

// ToolResultDetails describes a tool invocation and its output.
type ToolResultDetails struct {
	ToolCallDetails
	Output json.RawMessage `json:"output"`
}

// DecodeOutput decodes the tool output into v.
func (d ToolResultDetails) DecodeOutput(v any) error {
	return json.Unmarshal(d.Output, v)
}

// ToolCallEvent is delivered before a tool runs; handlers may replace the
// input or block the call.
type ToolCallEvent struct {
	EventHeader
	Tool ToolCallDetails `json:"tool"`
}

// ToolUpdateEvent carries a transient accumulated tool snapshot.
type ToolUpdateEvent struct {
	EventHeader
	Tool ToolResultDetails `json:"tool"`
}

// ToolResultEvent is delivered after a tool runs; handlers may replace the output.
type ToolResultEvent struct {
	EventHeader
	Tool ToolResultDetails `json:"tool"`
}

// EventBlock blocks a blocking event.
type EventBlock struct {
	Reason string `json:"reason"`
}

// SystemPromptPatch changes the system prompt in agent.init.
type SystemPromptPatch struct {
	Append  string `json:"append,omitempty"`
	Prepend string `json:"prepend,omitempty"`
	Replace string `json:"replace,omitempty"`
}

// ToolListPatch changes the tool allowlist in agent.init.
type ToolListPatch struct {
	Disable []string `json:"disable,omitempty"`
	Enable  []string `json:"enable,omitempty"`
}

// EventResult is returned by event handlers; the zero value means no change.
// Input and Output replace the tool input or output seen by later handlers and
// the host.
type EventResult struct {
	Input            any                `json:"input,omitempty"`
	Output           any                `json:"output,omitempty"`
	Block            *EventBlock        `json:"block,omitempty"`
	Message          *string            `json:"message,omitempty"`
	FollowUpMessages []string           `json:"followUpMessages,omitempty"`
	SystemPrompt     *SystemPromptPatch `json:"systemPrompt,omitempty"`
	Tools            *ToolListPatch     `json:"tools,omitempty"`
	Resources        any                `json:"resources,omitempty"`
}

// EventHandler handles a lifecycle event.
type EventHandler[P any] func(ctx context.Context, event P, ec *EventContext) (EventResult, error)

type subscriptionOptions struct {
	priority int
	timeout  time.Duration
}

// SubscriptionOption configures an event subscription.
type SubscriptionOption func(*subscriptionOptions)

// WithPriority runs the handler before lower-priority handlers.
func WithPriority(priority int) SubscriptionOption {
	return func(options *subscriptionOptions) { options.priority = priority }
}

// WithTimeout overrides the host timeout for this event; NoTimeout disables it.
func WithTimeout(timeout time.Duration) SubscriptionOption {
	return func(options *subscriptionOptions) { options.timeout = timeout }
}

// On subscribes handler to an event. Handlers for the same event run in
// descending priority, then registration order; a block stops the chain.
func On[P any](e *Extension, event Event[P], handler EventHandler[P], opts ...SubscriptionOption) {
	if event.name == "" {
		panic("extension: event name is required")
	}
	if handler == nil {
		panic("extension: missing handler for event " + event.name)
	}
	var options subscriptionOptions
	for _, opt := range opts {
		opt(&options)
	}
	invoke := func(ctx context.Context, payload map[string]any, ec *EventContext) (EventResult, error) {
		data, err := json.Marshal(payload)
		if err != nil {
			return EventResult{}, err
		}
		var decoded P
		if err := json.Unmarshal(data, &decoded); err != nil {
			return EventResult{}, err
		}
		return handler(ctx, decoded, ec)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers = append(e.handlers, &registeredHandler{
		event:    event.name,
		priority: options.priority,
		timeout:  timeoutSeconds(options.timeout),
		order:    len(e.handlers),
		invoke:   invoke,
	})
}
