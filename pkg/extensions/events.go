package extensions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const (
	// EventSessionStart is dispatched when the extension runtime starts.
	EventSessionStart = "session.start"
	// EventResourcesDiscover is dispatched before extension resources are finalized.
	EventResourcesDiscover = "resources.discover"
	// EventUserMessage is dispatched after a user prompt is received and before it is added to the conversation.
	EventUserMessage = "user.message"
	// EventAgentInit is dispatched after a system prompt is built and before a model request.
	EventAgentInit = "agent.init"
	// EventAgentStart is dispatched when an agent loop starts.
	EventAgentStart = "agent.start"
	// EventTurnStart is dispatched before each model turn.
	EventTurnStart = "turn.start"
	// EventToolCall is dispatched before a tool executes.
	EventToolCall = "tool.call"
	// EventToolResult is dispatched after a tool executes and before it is rendered/stored.
	EventToolResult = "tool.result"
	// EventTurnEnd is dispatched after one assistant turn completes.
	EventTurnEnd = "turn.end"
	// EventAgentEnd is dispatched when an agent loop has completed.
	EventAgentEnd = "agent.end"
	// EventSessionEnd is dispatched when the extension runtime shuts down.
	EventSessionEnd = "session.end"
)

// ToolCallDecision is the result of dispatching a tool.call event.
type ToolCallDecision struct {
	Blocked bool
	Reason  string
	Input   string
}

type eventHandler struct {
	process *Process
	sub     Subscription
	order   int
}

// UserMessageDecision is the result of dispatching a user.message event.
type UserMessageDecision struct {
	Blocked bool
	Reason  string
	Message string
}

type userMessagePayload struct {
	Message string `json:"message"`
}

type sessionStartPayload struct{}

type resourcesDiscoverPayload struct{}

type agentInitPayload struct {
	SystemPrompt string `json:"systemPrompt"`
}

type agentStartPayload struct{}

type turnStartPayload struct {
	TurnNumber int `json:"turnNumber"`
}

type turnEndPayload struct {
	Response   string `json:"response"`
	TurnNumber int    `json:"turnNumber"`
}

type agentEndPayload struct {
	Messages []llmtypes.Message `json:"messages"`
}

type sessionEndPayload struct{}

type toolCallPayload struct {
	Tool toolCallPayloadTool `json:"tool"`
}

type toolCallPayloadTool struct {
	Name   string          `json:"name"`
	CallID string          `json:"callId"`
	Input  json.RawMessage `json:"input"`
}

type toolResultPayload struct {
	Tool toolResultPayloadTool `json:"tool"`
}

type toolResultPayloadTool struct {
	Name   string                         `json:"name"`
	CallID string                         `json:"callId"`
	Input  json.RawMessage                `json:"input"`
	Output tooltypes.StructuredToolResult `json:"output"`
}

// DispatchSessionStart runs session.start subscriptions.
func (r *Runtime) DispatchSessionStart(ctx context.Context, callContext ExtensionCallContext) {
	r.dispatchObservationalEvent(ctx, EventSessionStart, sessionStartPayload{}, callContext)
}

// DispatchResourcesDiscover runs resources.discover subscriptions.
func (r *Runtime) DispatchResourcesDiscover(ctx context.Context, callContext ExtensionCallContext) {
	r.dispatchObservationalEvent(ctx, EventResourcesDiscover, resourcesDiscoverPayload{}, callContext)
}

// DispatchUserMessage runs user.message subscriptions sequentially.
func (r *Runtime) DispatchUserMessage(ctx context.Context, callContext ExtensionCallContext, message string) UserMessageDecision {
	decision := UserMessageDecision{Message: message}
	if r == nil {
		return decision
	}

	currentMessage := message
	for _, handler := range r.eventHandlers(EventUserMessage) {
		result, err := r.dispatchEventToHandler(ctx, handler, EventUserMessage, userMessagePayload{Message: currentMessage}, callContext)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("extension", handler.process.Extension.ID).Warn("extension user.message handler failed")
			continue
		}
		if result == nil {
			continue
		}
		if result.Block != nil {
			decision.Blocked = true
			decision.Reason = result.Block.Reason
			decision.Message = currentMessage
			return decision
		}
		if result.Message != nil {
			currentMessage = *result.Message
		}
	}

	decision.Message = currentMessage
	return decision
}

// DispatchAgentInit runs agent.init subscriptions and applies system prompt patches.
func (r *Runtime) DispatchAgentInit(ctx context.Context, callContext ExtensionCallContext, systemPrompt string) string {
	result := r.DispatchAgentInitDecision(ctx, callContext, systemPrompt, nil)
	return result.SystemPrompt
}

// AgentInitDecision is the result of dispatching agent.init handlers.
type AgentInitDecision struct {
	SystemPrompt  string
	AllowedTools  []string
	ToolsModified bool
}

// DispatchAgentInitDecision runs agent.init subscriptions and applies system prompt and tool-list patches.
func (r *Runtime) DispatchAgentInitDecision(ctx context.Context, callContext ExtensionCallContext, systemPrompt string, allowedTools []string) AgentInitDecision {
	decision := AgentInitDecision{SystemPrompt: systemPrompt, AllowedTools: append([]string(nil), allowedTools...)}
	if r == nil {
		return decision
	}

	for _, handler := range r.eventHandlers(EventAgentInit) {
		result, err := r.dispatchEventToHandler(ctx, handler, EventAgentInit, agentInitPayload{SystemPrompt: decision.SystemPrompt}, callContext)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("extension", handler.process.Extension.ID).Warn("extension agent.init handler failed")
			continue
		}
		if result == nil {
			continue
		}
		if result.SystemPrompt != nil {
			decision.SystemPrompt = applySystemPromptPatch(decision.SystemPrompt, result.SystemPrompt)
		}
		if result.Tools != nil {
			decision.AllowedTools = applyToolListPatch(decision.AllowedTools, result.Tools)
			decision.ToolsModified = true
		}
	}

	return decision
}

// DispatchAgentStart runs agent.start subscriptions.
func (r *Runtime) DispatchAgentStart(ctx context.Context, callContext ExtensionCallContext) {
	r.dispatchObservationalEvent(ctx, EventAgentStart, agentStartPayload{}, callContext)
}

// DispatchTurnStart runs turn.start subscriptions.
func (r *Runtime) DispatchTurnStart(ctx context.Context, callContext ExtensionCallContext, turnNumber int) {
	r.dispatchObservationalEvent(ctx, EventTurnStart, turnStartPayload{TurnNumber: turnNumber}, callContext)
}

// DispatchToolCall runs tool.call subscriptions sequentially.
func (r *Runtime) DispatchToolCall(ctx context.Context, callContext ExtensionCallContext, toolName, toolInput, toolCallID string) ToolCallDecision {
	decision := ToolCallDecision{Input: toolInput}
	if r == nil {
		return decision
	}

	currentInput := json.RawMessage(toolInput)
	for _, handler := range r.eventHandlers(EventToolCall) {
		payload := toolCallPayload{Tool: toolCallPayloadTool{Name: toolName, CallID: toolCallID, Input: currentInput}}
		result, err := r.dispatchEventToHandler(ctx, handler, EventToolCall, payload, callContext)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("extension", handler.process.Extension.ID).Warn("extension tool.call handler failed")
			continue
		}
		if result == nil {
			continue
		}
		if result.Block != nil {
			decision.Blocked = true
			decision.Reason = result.Block.Reason
			decision.Input = string(currentInput)
			return decision
		}
		if len(result.Input) > 0 {
			currentInput = result.Input
		}
	}

	decision.Input = string(currentInput)
	return decision
}

// DispatchToolResult runs tool.result subscriptions sequentially.
func (r *Runtime) DispatchToolResult(ctx context.Context, callContext ExtensionCallContext, toolName, toolInput, toolCallID string, output tooltypes.StructuredToolResult) (tooltypes.StructuredToolResult, bool) {
	if r == nil {
		return output, false
	}

	currentOutput := output
	modifiedOutput := false
	input := json.RawMessage(toolInput)
	for _, handler := range r.eventHandlers(EventToolResult) {
		payload := toolResultPayload{Tool: toolResultPayloadTool{Name: toolName, CallID: toolCallID, Input: input, Output: currentOutput}}
		result, err := r.dispatchEventToHandler(ctx, handler, EventToolResult, payload, callContext)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("extension", handler.process.Extension.ID).Warn("extension tool.result handler failed")
			continue
		}
		if result == nil || len(result.Output) == 0 {
			continue
		}
		var modified tooltypes.StructuredToolResult
		if err := json.Unmarshal(result.Output, &modified); err != nil {
			logger.G(ctx).WithError(err).WithField("extension", handler.process.Extension.ID).Warn("extension tool.result handler returned invalid output")
			continue
		}
		currentOutput = modified
		modifiedOutput = true
	}

	return currentOutput, modifiedOutput
}

// DispatchTurnEnd runs turn.end subscriptions.
func (r *Runtime) DispatchTurnEnd(ctx context.Context, callContext ExtensionCallContext, response string, turnNumber int) {
	if strings.TrimSpace(response) == "" {
		return
	}
	r.dispatchObservationalEvent(ctx, EventTurnEnd, turnEndPayload{Response: response, TurnNumber: turnNumber}, callContext)
}

// DispatchAgentEnd runs agent.end subscriptions and returns accumulated follow-up messages.
func (r *Runtime) DispatchAgentEnd(ctx context.Context, callContext ExtensionCallContext, messages []llmtypes.Message) []string {
	if r == nil {
		return nil
	}

	followUps := []string{}
	for _, handler := range r.eventHandlers(EventAgentEnd) {
		result, err := r.dispatchEventToHandler(ctx, handler, EventAgentEnd, agentEndPayload{Messages: messages}, callContext)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("extension", handler.process.Extension.ID).Warn("extension agent.end handler failed")
			continue
		}
		if result == nil {
			continue
		}
		followUps = append(followUps, result.FollowUpMessages...)
	}
	return followUps
}

// DispatchSessionEnd runs session.end subscriptions.
func (r *Runtime) DispatchSessionEnd(ctx context.Context, callContext ExtensionCallContext) {
	r.dispatchObservationalEvent(ctx, EventSessionEnd, sessionEndPayload{}, callContext)
}

func (r *Runtime) dispatchObservationalEvent(ctx context.Context, eventName string, payload any, callContext ExtensionCallContext) {
	if r == nil {
		return
	}
	for _, handler := range r.eventHandlers(eventName) {
		if _, err := r.dispatchEventToHandler(ctx, handler, eventName, payload, callContext); err != nil {
			logger.G(ctx).WithError(err).WithField("extension", handler.process.Extension.ID).WithField("event", eventName).Warn("extension event handler failed")
		}
	}
}

func applySystemPromptPatch(systemPrompt string, patch *SystemPromptPatch) string {
	if patch == nil {
		return systemPrompt
	}
	if patch.Replace != nil {
		systemPrompt = *patch.Replace
	}
	if patch.Prepend != nil {
		systemPrompt = joinPromptParts(*patch.Prepend, systemPrompt)
	}
	if patch.Append != nil {
		systemPrompt = joinPromptParts(systemPrompt, *patch.Append)
	}
	return systemPrompt
}

func joinPromptParts(first, second string) string {
	first = strings.TrimRight(first, "\n")
	second = strings.TrimLeft(second, "\n")
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + "\n" + second
}

func applyToolListPatch(allowedTools []string, patch *ToolListPatch) []string {
	if patch == nil {
		return allowedTools
	}
	patched := append([]string(nil), allowedTools...)
	for _, disabled := range patch.Disable {
		disabled = strings.TrimSpace(disabled)
		if disabled == "" {
			continue
		}
		patched = slices.DeleteFunc(patched, func(name string) bool { return name == disabled })
	}
	for _, enabled := range patch.Enable {
		enabled = strings.TrimSpace(enabled)
		if enabled == "" || slices.Contains(patched, enabled) {
			continue
		}
		patched = append(patched, enabled)
	}
	return patched
}

func (r *Runtime) dispatchEventToHandler(ctx context.Context, handler eventHandler, eventName string, payload any, callContext ExtensionCallContext) (*EventResult, error) {
	timeout := r.eventTimeout(eventName)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if handler.process != nil {
		return handler.process.HandleEvent(ctx, nextEventID(), eventName, payload, callContext)
	}
	return &EventResult{}, nil
}

func (r *Runtime) eventTimeout(eventName string) time.Duration {
	if eventConfig, ok := r.config.Events[eventName]; ok && eventConfig.Timeout > 0 {
		return eventConfig.Timeout
	}
	return timeoutOrDefault(r.config.Timeout, DefaultConfig().Timeout)
}

func (r *Runtime) eventHandlers(eventName string) []eventHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handlers := append([]eventHandler(nil), r.eventHandlersByName[eventName]...)
	sort.SliceStable(handlers, func(i, j int) bool {
		if handlers[i].sub.Priority != handlers[j].sub.Priority {
			return handlers[i].sub.Priority > handlers[j].sub.Priority
		}
		return handlers[i].order < handlers[j].order
	})
	return handlers
}

func nextEventID() string {
	var random [4]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "evt_" + time.Now().UTC().Format("20060102T150405.000000000") + "_" + hex.EncodeToString(random[:])
	}
	return "evt_" + time.Now().UTC().Format("20060102T150405.000000000")
}
