package hooks

import (
	"context"
	"testing"

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

func TestBuiltinRegistry_Register(t *testing.T) {
	registry := &BuiltinRegistry{
		handlers: make(map[string]BuiltinHandler),
	}

	handler := &SwapContextHandler{}
	registry.Register(handler)

	got, ok := registry.Get("swap_context")
	assert.True(t, ok)
	assert.Equal(t, handler, got)
}

func TestBuiltinRegistry_GetNotFound(t *testing.T) {
	registry := &BuiltinRegistry{
		handlers: make(map[string]BuiltinHandler),
	}

	handler, ok := registry.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, handler)
}

func TestSwapContextHandler_Name(t *testing.T) {
	handler := &SwapContextHandler{}
	assert.Equal(t, "swap_context", handler.Name())
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

	err := handler.HandleTurnEnd(context.Background(), mock, "test summary")
	require.NoError(t, err)
	assert.True(t, mock.swapCalled)
	assert.Equal(t, "test summary", mock.summary)
}

func TestSwapContextHandler_HandleTurnEnd_NotContextSwapper(t *testing.T) {
	handler := &SwapContextHandler{}
	nonSwapper := &nonSwappingThread{}

	err := handler.HandleTurnEnd(context.Background(), nonSwapper, "test summary")
	require.Error(t, err)
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
