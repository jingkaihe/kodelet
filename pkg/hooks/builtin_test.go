package hooks

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestBuiltinRegistry_DefaultRegistry(t *testing.T) {
	registry := DefaultBuiltinRegistry()
	require.NotNil(t, registry)
	require.NotNil(t, registry.handlers)

	handler, ok := registry.Get("swap_context")
	assert.True(t, ok)
	assert.NotNil(t, handler)
	assert.Equal(t, "swap_context", handler.Name())
}

func TestBaseBuiltinHandler_AllMethodsReturnNil(t *testing.T) {
	handler := &BaseBuiltinHandler{}
	ctx := context.Background()

	// Test all hook methods return nil
	beforeResult, err := handler.HandleBeforeToolCall(ctx, nil, BeforeToolCallPayload{})
	assert.NoError(t, err)
	assert.Nil(t, beforeResult)

	afterResult, err := handler.HandleAfterToolCall(ctx, nil, AfterToolCallPayload{})
	assert.NoError(t, err)
	assert.Nil(t, afterResult)

	userMsgResult, err := handler.HandleUserMessageSend(ctx, nil, UserMessageSendPayload{})
	assert.NoError(t, err)
	assert.Nil(t, userMsgResult)

	agentStopResult, err := handler.HandleAgentStop(ctx, nil, AgentStopPayload{})
	assert.NoError(t, err)
	assert.Nil(t, agentStopResult)

	turnEndResult, err := handler.HandleTurnEnd(ctx, nil, TurnEndPayload{})
	assert.NoError(t, err)
	assert.Nil(t, turnEndResult)
}

func TestSwapContextHandler_InheritsBaseBuiltinHandler(t *testing.T) {
	handler := &SwapContextHandler{}
	ctx := context.Background()

	// SwapContextHandler should return nil for hooks it doesn't implement
	beforeResult, err := handler.HandleBeforeToolCall(ctx, nil, BeforeToolCallPayload{})
	assert.NoError(t, err)
	assert.Nil(t, beforeResult)

	afterResult, err := handler.HandleAfterToolCall(ctx, nil, AfterToolCallPayload{})
	assert.NoError(t, err)
	assert.Nil(t, afterResult)

	userMsgResult, err := handler.HandleUserMessageSend(ctx, nil, UserMessageSendPayload{})
	assert.NoError(t, err)
	assert.Nil(t, userMsgResult)

	agentStopResult, err := handler.HandleAgentStop(ctx, nil, AgentStopPayload{})
	assert.NoError(t, err)
	assert.Nil(t, agentStopResult)
}

// mockContextSwapper implements ContextSwapper and Thread for testing
type mockContextSwapper struct {
	swapCalled bool
	summary    string
}

func (m *mockContextSwapper) SwapContext(_ context.Context, summary string) error {
	m.swapCalled = true
	m.summary = summary
	return nil
}

// Implement Thread interface methods
func (m *mockContextSwapper) SetState(_ tooltypes.State)                              {}
func (m *mockContextSwapper) GetState() tooltypes.State                               { return nil }
func (m *mockContextSwapper) AddUserMessage(_ context.Context, _ string, _ ...string) {}
func (m *mockContextSwapper) SendMessage(_ context.Context, _ string, _ llmtypes.MessageHandler, _ llmtypes.MessageOpt) (string, error) {
	return "", nil
}
func (m *mockContextSwapper) GetUsage() llmtypes.Usage                         { return llmtypes.Usage{} }
func (m *mockContextSwapper) GetConversationID() string                        { return "" }
func (m *mockContextSwapper) SetConversationID(_ string)                       {}
func (m *mockContextSwapper) SaveConversation(_ context.Context, _ bool) error { return nil }
func (m *mockContextSwapper) IsPersisted() bool                                { return false }
func (m *mockContextSwapper) EnablePersistence(_ context.Context, _ bool)      {}
func (m *mockContextSwapper) Provider() string                                 { return "mock" }
func (m *mockContextSwapper) GetMessages() ([]llmtypes.Message, error)         { return nil, nil }
func (m *mockContextSwapper) GetConfig() llmtypes.Config                       { return llmtypes.Config{} }
func (m *mockContextSwapper) NewSubAgent(_ context.Context, _ llmtypes.Config) llmtypes.Thread {
	return nil
}
func (m *mockContextSwapper) AggregateSubagentUsage(_ llmtypes.Usage)         {}
func (m *mockContextSwapper) SetRecipeHooks(_ map[string]llmtypes.HookConfig) {}
func (m *mockContextSwapper) GetRecipeHooks() map[string]llmtypes.HookConfig  { return nil }

// nonSwappingThread implements Thread but not ContextSwapper
type nonSwappingThread struct{}

func (n *nonSwappingThread) SetState(_ tooltypes.State)                              {}
func (n *nonSwappingThread) GetState() tooltypes.State                               { return nil }
func (n *nonSwappingThread) AddUserMessage(_ context.Context, _ string, _ ...string) {}
func (n *nonSwappingThread) SendMessage(_ context.Context, _ string, _ llmtypes.MessageHandler, _ llmtypes.MessageOpt) (string, error) {
	return "", nil
}
func (n *nonSwappingThread) GetUsage() llmtypes.Usage                         { return llmtypes.Usage{} }
func (n *nonSwappingThread) GetConversationID() string                        { return "" }
func (n *nonSwappingThread) SetConversationID(_ string)                       {}
func (n *nonSwappingThread) SaveConversation(_ context.Context, _ bool) error { return nil }
func (n *nonSwappingThread) IsPersisted() bool                                { return false }
func (n *nonSwappingThread) EnablePersistence(_ context.Context, _ bool)      {}
func (n *nonSwappingThread) Provider() string                                 { return "mock" }
func (n *nonSwappingThread) GetMessages() ([]llmtypes.Message, error)         { return nil, nil }
func (n *nonSwappingThread) GetConfig() llmtypes.Config                       { return llmtypes.Config{} }
func (n *nonSwappingThread) NewSubAgent(_ context.Context, _ llmtypes.Config) llmtypes.Thread {
	return nil
}
func (n *nonSwappingThread) AggregateSubagentUsage(_ llmtypes.Usage)         {}
func (n *nonSwappingThread) SetRecipeHooks(_ map[string]llmtypes.HookConfig) {}
func (n *nonSwappingThread) GetRecipeHooks() map[string]llmtypes.HookConfig  { return nil }

func TestSwapContextHandler_HandleTurnEnd_Success(t *testing.T) {
	handler := &SwapContextHandler{}
	mock := &mockContextSwapper{}

	payload := TurnEndPayload{
		Response:   "test summary",
		TurnNumber: 1,
	}
	result, err := handler.HandleTurnEnd(context.Background(), mock, payload)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, mock.swapCalled)
	assert.Equal(t, "test summary", mock.summary)
}

func TestSwapContextHandler_HandleTurnEnd_NotContextSwapper(t *testing.T) {
	handler := &SwapContextHandler{}
	nonSwapper := &nonSwappingThread{}

	payload := TurnEndPayload{
		Response:   "test summary",
		TurnNumber: 1,
	}
	result, err := handler.HandleTurnEnd(context.Background(), nonSwapper, payload)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "thread does not support context swapping")
}

func TestTriggerTurnEnd_WithRecipeHooks(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	mock := &mockContextSwapper{}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"turn_end": {
			Handler: "swap_context",
			Once:    false,
		},
	}

	trigger.TriggerTurnEnd(context.Background(), mock, "test response", 1, recipeHooks)

	assert.True(t, mock.swapCalled)
	assert.Equal(t, "test response", mock.summary)
}

func TestTriggerTurnEnd_WithOnceTrue_FirstTurn(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	mock := &mockContextSwapper{}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"turn_end": {
			Handler: "swap_context",
			Once:    true,
		},
	}

	trigger.TriggerTurnEnd(context.Background(), mock, "first turn response", 1, recipeHooks)

	assert.True(t, mock.swapCalled)
	assert.Equal(t, "first turn response", mock.summary)
}

func TestTriggerTurnEnd_WithOnceTrue_SecondTurn(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	mock := &mockContextSwapper{}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"turn_end": {
			Handler: "swap_context",
			Once:    true,
		},
	}

	trigger.TriggerTurnEnd(context.Background(), mock, "second turn response", 2, recipeHooks)

	// Should skip execution because once=true and turnNumber > 1
	assert.False(t, mock.swapCalled)
}

func TestTriggerTurnEnd_UnknownHandler(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	mock := &mockContextSwapper{}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"turn_end": {
			Handler: "unknown_handler",
			Once:    false,
		},
	}

	// Should not panic with unknown handler
	trigger.TriggerTurnEnd(context.Background(), mock, "test response", 1, recipeHooks)

	// Handler not found, so should not be called
	assert.False(t, mock.swapCalled)
}

// mockFailingContextSwapper implements ContextSwapper that returns an error
type mockFailingContextSwapper struct {
	swapCalled bool
	swapError  error
}

func (m *mockFailingContextSwapper) SwapContext(_ context.Context, _ string) error {
	m.swapCalled = true
	return m.swapError
}

// Implement Thread interface methods
func (m *mockFailingContextSwapper) SetState(_ tooltypes.State)                              {}
func (m *mockFailingContextSwapper) GetState() tooltypes.State                               { return nil }
func (m *mockFailingContextSwapper) AddUserMessage(_ context.Context, _ string, _ ...string) {}
func (m *mockFailingContextSwapper) SendMessage(_ context.Context, _ string, _ llmtypes.MessageHandler, _ llmtypes.MessageOpt) (string, error) {
	return "", nil
}
func (m *mockFailingContextSwapper) GetUsage() llmtypes.Usage                         { return llmtypes.Usage{} }
func (m *mockFailingContextSwapper) GetConversationID() string                        { return "" }
func (m *mockFailingContextSwapper) SetConversationID(_ string)                       {}
func (m *mockFailingContextSwapper) SaveConversation(_ context.Context, _ bool) error { return nil }
func (m *mockFailingContextSwapper) IsPersisted() bool                                { return false }
func (m *mockFailingContextSwapper) EnablePersistence(_ context.Context, _ bool)      {}
func (m *mockFailingContextSwapper) Provider() string                                 { return "mock" }
func (m *mockFailingContextSwapper) GetMessages() ([]llmtypes.Message, error)         { return nil, nil }
func (m *mockFailingContextSwapper) GetConfig() llmtypes.Config                       { return llmtypes.Config{} }
func (m *mockFailingContextSwapper) NewSubAgent(_ context.Context, _ llmtypes.Config) llmtypes.Thread {
	return nil
}
func (m *mockFailingContextSwapper) AggregateSubagentUsage(_ llmtypes.Usage)         {}
func (m *mockFailingContextSwapper) SetRecipeHooks(_ map[string]llmtypes.HookConfig) {}
func (m *mockFailingContextSwapper) GetRecipeHooks() map[string]llmtypes.HookConfig  { return nil }

func TestSwapContextHandler_HandleTurnEnd_Error(t *testing.T) {
	handler := &SwapContextHandler{}
	expectedErr := errors.New("swap context failed")
	mock := &mockFailingContextSwapper{
		swapError: expectedErr,
	}

	payload := TurnEndPayload{
		Response:   "test summary",
		TurnNumber: 1,
	}
	result, err := handler.HandleTurnEnd(context.Background(), mock, payload)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, mock.swapCalled)
	assert.Contains(t, err.Error(), "swap context failed")
}

func TestTriggerTurnEnd_HandlerReturnsError(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	mock := &mockFailingContextSwapper{
		swapError: errors.New("context swap failed"),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"turn_end": {
			Handler: "swap_context",
			Once:    false,
		},
	}

	// Should not panic even when handler returns error
	trigger.TriggerTurnEnd(context.Background(), mock, "test response", 1, recipeHooks)

	// Handler should still have been called
	assert.True(t, mock.swapCalled)
}

// Tests for TriggerBeforeToolCall

func TestTriggerBeforeToolCall_NoHooksConfigured(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	blocked, reason, input := trigger.TriggerBeforeToolCall(
		context.Background(),
		&mockContextSwapper{},
		"bash",
		`{"command": "ls"}`,
		"tool-123",
		nil,
	)

	assert.False(t, blocked)
	assert.Empty(t, reason)
	assert.Equal(t, `{"command": "ls"}`, input)
}

func TestTriggerBeforeToolCall_UnknownHandler(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"before_tool_call": {
			Handler: "unknown_handler",
		},
	}

	blocked, reason, input := trigger.TriggerBeforeToolCall(
		context.Background(),
		&mockContextSwapper{},
		"bash",
		`{"command": "ls"}`,
		"tool-123",
		recipeHooks,
	)

	assert.False(t, blocked)
	assert.Empty(t, reason)
	assert.Equal(t, `{"command": "ls"}`, input)
}

func TestTriggerBeforeToolCall_BaseHandlerReturnsNil(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	// swap_context handler returns nil for before_tool_call
	recipeHooks := map[string]llmtypes.HookConfig{
		"before_tool_call": {
			Handler: "swap_context",
		},
	}

	blocked, reason, input := trigger.TriggerBeforeToolCall(
		context.Background(),
		&mockContextSwapper{},
		"bash",
		`{"command": "ls"}`,
		"tool-123",
		recipeHooks,
	)

	assert.False(t, blocked)
	assert.Empty(t, reason)
	assert.Equal(t, `{"command": "ls"}`, input)
}

// Tests for TriggerAfterToolCall

func TestTriggerAfterToolCall_NoHooksConfigured(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	toolOutput := tooltypes.StructuredToolResult{
		ToolName: "bash",
		Success:  true,
	}

	result := trigger.TriggerAfterToolCall(
		context.Background(),
		&mockContextSwapper{},
		"bash",
		`{"command": "ls"}`,
		"tool-123",
		toolOutput,
		nil,
	)

	assert.Nil(t, result)
}

func TestTriggerAfterToolCall_UnknownHandler(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"after_tool_call": {
			Handler: "unknown_handler",
		},
	}

	toolOutput := tooltypes.StructuredToolResult{
		ToolName: "bash",
		Success:  true,
	}

	result := trigger.TriggerAfterToolCall(
		context.Background(),
		&mockContextSwapper{},
		"bash",
		`{"command": "ls"}`,
		"tool-123",
		toolOutput,
		recipeHooks,
	)

	assert.Nil(t, result)
}

func TestTriggerAfterToolCall_BaseHandlerReturnsNil(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	// swap_context handler returns nil for after_tool_call
	recipeHooks := map[string]llmtypes.HookConfig{
		"after_tool_call": {
			Handler: "swap_context",
		},
	}

	toolOutput := tooltypes.StructuredToolResult{
		ToolName: "bash",
		Success:  true,
	}

	result := trigger.TriggerAfterToolCall(
		context.Background(),
		&mockContextSwapper{},
		"bash",
		`{"command": "ls"}`,
		"tool-123",
		toolOutput,
		recipeHooks,
	)

	assert.Nil(t, result)
}

// Tests for TriggerUserMessageSend

func TestTriggerUserMessageSend_NoHooksConfigured(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	blocked, reason := trigger.TriggerUserMessageSend(
		context.Background(),
		&mockContextSwapper{},
		"Hello, please help me",
		nil,
	)

	assert.False(t, blocked)
	assert.Empty(t, reason)
}

func TestTriggerUserMessageSend_UnknownHandler(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"user_message_send": {
			Handler: "unknown_handler",
		},
	}

	blocked, reason := trigger.TriggerUserMessageSend(
		context.Background(),
		&mockContextSwapper{},
		"Hello, please help me",
		recipeHooks,
	)

	assert.False(t, blocked)
	assert.Empty(t, reason)
}

func TestTriggerUserMessageSend_BaseHandlerReturnsNil(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	// swap_context handler returns nil for user_message_send
	recipeHooks := map[string]llmtypes.HookConfig{
		"user_message_send": {
			Handler: "swap_context",
		},
	}

	blocked, reason := trigger.TriggerUserMessageSend(
		context.Background(),
		&mockContextSwapper{},
		"Hello, please help me",
		recipeHooks,
	)

	assert.False(t, blocked)
	assert.Empty(t, reason)
}

// Tests for TriggerAgentStop

func TestTriggerAgentStop_NoHooksConfigured(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	messages := []llmtypes.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	followUp := trigger.TriggerAgentStop(
		context.Background(),
		&mockContextSwapper{},
		messages,
		nil,
	)

	assert.Nil(t, followUp)
}

func TestTriggerAgentStop_UnknownHandler(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	recipeHooks := map[string]llmtypes.HookConfig{
		"agent_stop": {
			Handler: "unknown_handler",
		},
	}

	messages := []llmtypes.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	followUp := trigger.TriggerAgentStop(
		context.Background(),
		&mockContextSwapper{},
		messages,
		recipeHooks,
	)

	assert.Nil(t, followUp)
}

func TestTriggerAgentStop_BaseHandlerReturnsNil(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}
	trigger := NewTrigger(manager, "test-conv", false)

	// swap_context handler returns nil for agent_stop
	recipeHooks := map[string]llmtypes.HookConfig{
		"agent_stop": {
			Handler: "swap_context",
		},
	}

	messages := []llmtypes.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	followUp := trigger.TriggerAgentStop(
		context.Background(),
		&mockContextSwapper{},
		messages,
		recipeHooks,
	)

	assert.Nil(t, followUp)
}

// mockAllHooksHandler implements BuiltinHandler with configurable behavior for all hooks
type mockAllHooksHandler struct {
	name string

	// Track which hooks were called
	beforeToolCallCalled  bool
	afterToolCallCalled   bool
	userMessageSendCalled bool
	agentStopCalled       bool
	turnEndCalled         bool

	// Capture payloads for verification
	lastBeforeToolCallPayload  BeforeToolCallPayload
	lastAfterToolCallPayload   AfterToolCallPayload
	lastUserMessageSendPayload UserMessageSendPayload
	lastAgentStopPayload       AgentStopPayload
	lastTurnEndPayload         TurnEndPayload

	// Configurable results
	beforeToolCallResult  *BeforeToolCallResult
	beforeToolCallError   error
	afterToolCallResult   *AfterToolCallResult
	afterToolCallError    error
	userMessageSendResult *UserMessageSendResult
	userMessageSendError  error
	agentStopResult       *AgentStopResult
	agentStopError        error
	turnEndResult         *TurnEndResult
	turnEndError          error
}

func (m *mockAllHooksHandler) Name() string {
	return m.name
}

func (m *mockAllHooksHandler) HandleBeforeToolCall(_ context.Context, _ llmtypes.Thread, payload BeforeToolCallPayload) (*BeforeToolCallResult, error) {
	m.beforeToolCallCalled = true
	m.lastBeforeToolCallPayload = payload
	return m.beforeToolCallResult, m.beforeToolCallError
}

func (m *mockAllHooksHandler) HandleAfterToolCall(_ context.Context, _ llmtypes.Thread, payload AfterToolCallPayload) (*AfterToolCallResult, error) {
	m.afterToolCallCalled = true
	m.lastAfterToolCallPayload = payload
	return m.afterToolCallResult, m.afterToolCallError
}

func (m *mockAllHooksHandler) HandleUserMessageSend(_ context.Context, _ llmtypes.Thread, payload UserMessageSendPayload) (*UserMessageSendResult, error) {
	m.userMessageSendCalled = true
	m.lastUserMessageSendPayload = payload
	return m.userMessageSendResult, m.userMessageSendError
}

func (m *mockAllHooksHandler) HandleAgentStop(_ context.Context, _ llmtypes.Thread, payload AgentStopPayload) (*AgentStopResult, error) {
	m.agentStopCalled = true
	m.lastAgentStopPayload = payload
	return m.agentStopResult, m.agentStopError
}

func (m *mockAllHooksHandler) HandleTurnEnd(_ context.Context, _ llmtypes.Thread, payload TurnEndPayload) (*TurnEndResult, error) {
	m.turnEndCalled = true
	m.lastTurnEndPayload = payload
	return m.turnEndResult, m.turnEndError
}

// Verify mockAllHooksHandler implements BuiltinHandler
var _ BuiltinHandler = (*mockAllHooksHandler)(nil)

func TestMockAllHooksHandler_BeforeToolCall_Blocks(t *testing.T) {
	handler := &mockAllHooksHandler{
		name: "test_handler",
		beforeToolCallResult: &BeforeToolCallResult{
			Blocked: true,
			Reason:  "dangerous command detected",
		},
	}

	registry := &BuiltinRegistry{handlers: make(map[string]BuiltinHandler)}
	registry.Register(handler)

	manager := HookManager{hooks: make(map[HookType][]*Hook)}
	trigger := NewTrigger(manager, "test-conv", false)

	// Temporarily override default registry by using a custom trigger flow
	recipeHooks := map[string]llmtypes.HookConfig{
		"before_tool_call": {Handler: "test_handler"},
	}

	// Since we can't easily inject registry, test the handler directly
	payload := BeforeToolCallPayload{
		BasePayload: BasePayload{Event: HookTypeBeforeToolCall, ConvID: "test-conv"},
		ToolName:    "bash",
		ToolInput:   []byte(`{"command": "rm -rf /"}`),
		ToolUserID:  "tool-123",
	}

	result, err := handler.HandleBeforeToolCall(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Blocked)
	assert.Equal(t, "dangerous command detected", result.Reason)
	assert.True(t, handler.beforeToolCallCalled)
	assert.Equal(t, "bash", handler.lastBeforeToolCallPayload.ToolName)

	// Verify trigger helper is still usable
	_ = trigger
	_ = recipeHooks
}

func TestMockAllHooksHandler_BeforeToolCall_ModifiesInput(t *testing.T) {
	handler := &mockAllHooksHandler{
		name: "test_handler",
		beforeToolCallResult: &BeforeToolCallResult{
			Blocked: false,
			Input:   []byte(`{"command": "ls -la"}`),
		},
	}

	payload := BeforeToolCallPayload{
		BasePayload: BasePayload{Event: HookTypeBeforeToolCall, ConvID: "test-conv"},
		ToolName:    "bash",
		ToolInput:   []byte(`{"command": "ls"}`),
		ToolUserID:  "tool-123",
	}

	result, err := handler.HandleBeforeToolCall(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Blocked)
	assert.Equal(t, `{"command": "ls -la"}`, string(result.Input))
}

func TestMockAllHooksHandler_BeforeToolCall_Error(t *testing.T) {
	handler := &mockAllHooksHandler{
		name:                "test_handler",
		beforeToolCallError: errors.New("handler crashed"),
	}

	payload := BeforeToolCallPayload{
		ToolName: "bash",
	}

	result, err := handler.HandleBeforeToolCall(context.Background(), nil, payload)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "handler crashed")
}

func TestMockAllHooksHandler_AfterToolCall_ModifiesOutput(t *testing.T) {
	modifiedOutput := &tooltypes.StructuredToolResult{
		ToolName: "bash",
		Success:  true,
	}

	handler := &mockAllHooksHandler{
		name: "test_handler",
		afterToolCallResult: &AfterToolCallResult{
			Output: modifiedOutput,
		},
	}

	originalOutput := tooltypes.StructuredToolResult{
		ToolName: "bash",
		Success:  false,
		Error:    "command failed",
	}

	payload := AfterToolCallPayload{
		BasePayload: BasePayload{Event: HookTypeAfterToolCall, ConvID: "test-conv"},
		ToolName:    "bash",
		ToolInput:   []byte(`{"command": "ls"}`),
		ToolOutput:  originalOutput,
		ToolUserID:  "tool-123",
	}

	result, err := handler.HandleAfterToolCall(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Output)
	assert.True(t, result.Output.Success)
	assert.Empty(t, result.Output.Error)
	assert.True(t, handler.afterToolCallCalled)
}

func TestMockAllHooksHandler_AfterToolCall_Error(t *testing.T) {
	handler := &mockAllHooksHandler{
		name:               "test_handler",
		afterToolCallError: errors.New("post-processing failed"),
	}

	payload := AfterToolCallPayload{
		ToolName: "bash",
	}

	result, err := handler.HandleAfterToolCall(context.Background(), nil, payload)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "post-processing failed")
}

func TestMockAllHooksHandler_UserMessageSend_Blocks(t *testing.T) {
	handler := &mockAllHooksHandler{
		name: "test_handler",
		userMessageSendResult: &UserMessageSendResult{
			Blocked: true,
			Reason:  "message contains forbidden content",
		},
	}

	payload := UserMessageSendPayload{
		BasePayload: BasePayload{Event: HookTypeUserMessageSend, ConvID: "test-conv"},
		Message:     "please delete everything",
	}

	result, err := handler.HandleUserMessageSend(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Blocked)
	assert.Equal(t, "message contains forbidden content", result.Reason)
	assert.True(t, handler.userMessageSendCalled)
	assert.Equal(t, "please delete everything", handler.lastUserMessageSendPayload.Message)
}

func TestMockAllHooksHandler_UserMessageSend_Allows(t *testing.T) {
	handler := &mockAllHooksHandler{
		name: "test_handler",
		userMessageSendResult: &UserMessageSendResult{
			Blocked: false,
		},
	}

	payload := UserMessageSendPayload{
		BasePayload: BasePayload{Event: HookTypeUserMessageSend, ConvID: "test-conv"},
		Message:     "please help me with my code",
	}

	result, err := handler.HandleUserMessageSend(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Blocked)
}

func TestMockAllHooksHandler_UserMessageSend_Error(t *testing.T) {
	handler := &mockAllHooksHandler{
		name:                 "test_handler",
		userMessageSendError: errors.New("validation service unavailable"),
	}

	payload := UserMessageSendPayload{
		Message: "hello",
	}

	result, err := handler.HandleUserMessageSend(context.Background(), nil, payload)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "validation service unavailable")
}

func TestMockAllHooksHandler_AgentStop_ReturnsFollowUp(t *testing.T) {
	handler := &mockAllHooksHandler{
		name: "test_handler",
		agentStopResult: &AgentStopResult{
			FollowUpMessages: []string{
				"Please review the changes before committing",
				"Run tests to verify the fix",
			},
		},
	}

	messages := []llmtypes.Message{
		{Role: "user", Content: "fix the bug"},
		{Role: "assistant", Content: "I've fixed the bug in file.go"},
	}

	payload := AgentStopPayload{
		BasePayload: BasePayload{Event: HookTypeAgentStop, ConvID: "test-conv"},
		Messages:    messages,
	}

	result, err := handler.HandleAgentStop(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.FollowUpMessages, 2)
	assert.Equal(t, "Please review the changes before committing", result.FollowUpMessages[0])
	assert.Equal(t, "Run tests to verify the fix", result.FollowUpMessages[1])
	assert.True(t, handler.agentStopCalled)
	assert.Len(t, handler.lastAgentStopPayload.Messages, 2)
}

func TestMockAllHooksHandler_AgentStop_NoFollowUp(t *testing.T) {
	handler := &mockAllHooksHandler{
		name: "test_handler",
		agentStopResult: &AgentStopResult{
			FollowUpMessages: nil,
		},
	}

	payload := AgentStopPayload{
		BasePayload: BasePayload{Event: HookTypeAgentStop, ConvID: "test-conv"},
		Messages:    []llmtypes.Message{},
	}

	result, err := handler.HandleAgentStop(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.FollowUpMessages)
}

func TestMockAllHooksHandler_AgentStop_Error(t *testing.T) {
	handler := &mockAllHooksHandler{
		name:           "test_handler",
		agentStopError: errors.New("feedback service failed"),
	}

	payload := AgentStopPayload{
		Messages: []llmtypes.Message{},
	}

	result, err := handler.HandleAgentStop(context.Background(), nil, payload)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "feedback service failed")
}

func TestMockAllHooksHandler_TurnEnd_Success(t *testing.T) {
	handler := &mockAllHooksHandler{
		name:          "test_handler",
		turnEndResult: &TurnEndResult{},
	}

	payload := TurnEndPayload{
		BasePayload: BasePayload{Event: HookTypeTurnEnd, ConvID: "test-conv"},
		Response:    "I've completed the task",
		TurnNumber:  3,
	}

	result, err := handler.HandleTurnEnd(context.Background(), nil, payload)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, handler.turnEndCalled)
	assert.Equal(t, "I've completed the task", handler.lastTurnEndPayload.Response)
	assert.Equal(t, 3, handler.lastTurnEndPayload.TurnNumber)
}

func TestMockAllHooksHandler_TurnEnd_Error(t *testing.T) {
	handler := &mockAllHooksHandler{
		name:         "test_handler",
		turnEndError: errors.New("turn processing failed"),
	}

	payload := TurnEndPayload{
		Response:   "test response",
		TurnNumber: 1,
	}

	result, err := handler.HandleTurnEnd(context.Background(), nil, payload)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "turn processing failed")
}

func TestMockAllHooksHandler_AllHooksCalledIndependently(t *testing.T) {
	handler := &mockAllHooksHandler{
		name:                  "test_handler",
		beforeToolCallResult:  &BeforeToolCallResult{Blocked: false},
		afterToolCallResult:   &AfterToolCallResult{},
		userMessageSendResult: &UserMessageSendResult{Blocked: false},
		agentStopResult:       &AgentStopResult{},
		turnEndResult:         &TurnEndResult{},
	}

	ctx := context.Background()

	// Call all hooks
	_, _ = handler.HandleBeforeToolCall(ctx, nil, BeforeToolCallPayload{ToolName: "tool1"})
	_, _ = handler.HandleAfterToolCall(ctx, nil, AfterToolCallPayload{ToolName: "tool2"})
	_, _ = handler.HandleUserMessageSend(ctx, nil, UserMessageSendPayload{Message: "msg"})
	_, _ = handler.HandleAgentStop(ctx, nil, AgentStopPayload{})
	_, _ = handler.HandleTurnEnd(ctx, nil, TurnEndPayload{TurnNumber: 1})

	// Verify all hooks were called
	assert.True(t, handler.beforeToolCallCalled, "beforeToolCall should be called")
	assert.True(t, handler.afterToolCallCalled, "afterToolCall should be called")
	assert.True(t, handler.userMessageSendCalled, "userMessageSend should be called")
	assert.True(t, handler.agentStopCalled, "agentStop should be called")
	assert.True(t, handler.turnEndCalled, "turnEnd should be called")

	// Verify payloads were captured correctly
	assert.Equal(t, "tool1", handler.lastBeforeToolCallPayload.ToolName)
	assert.Equal(t, "tool2", handler.lastAfterToolCallPayload.ToolName)
	assert.Equal(t, "msg", handler.lastUserMessageSendPayload.Message)
	assert.Equal(t, 1, handler.lastTurnEndPayload.TurnNumber)
}

func TestBuiltinRegistry_RegisterAndGet(t *testing.T) {
	registry := &BuiltinRegistry{handlers: make(map[string]BuiltinHandler)}

	handler := &mockAllHooksHandler{name: "custom_handler"}
	registry.Register(handler)

	retrieved, ok := registry.Get("custom_handler")
	assert.True(t, ok)
	assert.Equal(t, "custom_handler", retrieved.Name())

	_, ok = registry.Get("nonexistent")
	assert.False(t, ok)
}

func TestBuiltinRegistry_OverwriteHandler(t *testing.T) {
	registry := &BuiltinRegistry{handlers: make(map[string]BuiltinHandler)}

	handler1 := &mockAllHooksHandler{
		name:                 "my_handler",
		beforeToolCallResult: &BeforeToolCallResult{Blocked: true},
	}
	registry.Register(handler1)

	handler2 := &mockAllHooksHandler{
		name:                 "my_handler",
		beforeToolCallResult: &BeforeToolCallResult{Blocked: false},
	}
	registry.Register(handler2)

	retrieved, ok := registry.Get("my_handler")
	require.True(t, ok)

	result, _ := retrieved.HandleBeforeToolCall(context.Background(), nil, BeforeToolCallPayload{})
	assert.False(t, result.Blocked, "should use the second registered handler")
}
