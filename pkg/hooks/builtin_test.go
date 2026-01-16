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
