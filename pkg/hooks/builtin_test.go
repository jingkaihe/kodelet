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

// mockThread is a configurable mock for llmtypes.Thread and ContextSwapper
type mockThread struct {
	swapContextFunc func(ctx context.Context, summary string) error
	swapCalled      bool
	lastSummary     string
}

func (m *mockThread) SwapContext(ctx context.Context, summary string) error {
	m.swapCalled = true
	m.lastSummary = summary
	if m.swapContextFunc != nil {
		return m.swapContextFunc(ctx, summary)
	}
	return nil
}

func (m *mockThread) SetState(_ tooltypes.State)                              {}
func (m *mockThread) GetState() tooltypes.State                               { return nil }
func (m *mockThread) AddUserMessage(_ context.Context, _ string, _ ...string) {}
func (m *mockThread) SendMessage(_ context.Context, _ string, _ llmtypes.MessageHandler, _ llmtypes.MessageOpt) (string, error) {
	return "", nil
}
func (m *mockThread) GetUsage() llmtypes.Usage                         { return llmtypes.Usage{} }
func (m *mockThread) GetConversationID() string                        { return "" }
func (m *mockThread) SetConversationID(_ string)                       {}
func (m *mockThread) SaveConversation(_ context.Context, _ bool) error { return nil }
func (m *mockThread) IsPersisted() bool                                { return false }
func (m *mockThread) EnablePersistence(_ context.Context, _ bool)      {}
func (m *mockThread) Provider() string                                 { return "mock" }
func (m *mockThread) GetMessages() ([]llmtypes.Message, error)         { return nil, nil }
func (m *mockThread) GetConfig() llmtypes.Config                       { return llmtypes.Config{} }
func (m *mockThread) AggregateSubagentUsage(_ llmtypes.Usage)          {}
func (m *mockThread) SetRecipeHooks(_ map[string]llmtypes.HookConfig)  {}
func (m *mockThread) GetRecipeHooks() map[string]llmtypes.HookConfig   { return nil }

// nonSwappingThread implements Thread but NOT ContextSwapper
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
func (n *nonSwappingThread) AggregateSubagentUsage(_ llmtypes.Usage)          {}
func (n *nonSwappingThread) SetRecipeHooks(_ map[string]llmtypes.HookConfig)  {}
func (n *nonSwappingThread) GetRecipeHooks() map[string]llmtypes.HookConfig   { return nil }

// Verify interface compliance
var (
	_ llmtypes.Thread = (*mockThread)(nil)
	_ ContextSwapper  = (*mockThread)(nil)
	_ llmtypes.Thread = (*nonSwappingThread)(nil)
)

func TestDefaultBuiltinRegistry(t *testing.T) {
	registry := DefaultBuiltinRegistry()
	require.NotNil(t, registry)

	handler, ok := registry.Get("swap_context")
	assert.True(t, ok)
	assert.Equal(t, "swap_context", handler.Name())
}

func TestBuiltinRegistry_RegisterAndGet(t *testing.T) {
	registry := &BuiltinRegistry{handlers: make(map[string]BuiltinHandler)}

	handler := &SwapContextHandler{}
	registry.Register(handler)

	retrieved, ok := registry.Get("swap_context")
	assert.True(t, ok)
	assert.Equal(t, "swap_context", retrieved.Name())

	_, ok = registry.Get("nonexistent")
	assert.False(t, ok)
}

func TestBuiltinRegistry_OverwriteHandler(t *testing.T) {
	registry := &BuiltinRegistry{handlers: make(map[string]BuiltinHandler)}

	registry.Register(&SwapContextHandler{})
	registry.Register(&SwapContextHandler{})

	_, ok := registry.Get("swap_context")
	assert.True(t, ok, "handler should be retrievable after overwrite")
}

func TestSwapContextHandler_HandleTurnEnd(t *testing.T) {
	tests := []struct {
		name        string
		thread      llmtypes.Thread
		swapErr     error
		wantErr     string
		wantSwapped bool
	}{
		{
			name:        "success",
			thread:      &mockThread{},
			wantSwapped: true,
		},
		{
			name:    "thread does not implement ContextSwapper",
			thread:  &nonSwappingThread{},
			wantErr: "thread does not support context swapping",
		},
		{
			name:        "swap context returns error",
			thread:      &mockThread{swapContextFunc: func(_ context.Context, _ string) error { return errors.New("swap failed") }},
			wantErr:     "swap failed",
			wantSwapped: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &SwapContextHandler{}
			payload := TurnEndPayload{Response: "test summary", TurnNumber: 1}

			result, err := handler.HandleTurnEnd(context.Background(), tt.thread, payload)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}

			if mock, ok := tt.thread.(*mockThread); ok {
				assert.Equal(t, tt.wantSwapped, mock.swapCalled)
				if tt.wantSwapped {
					assert.Equal(t, "test summary", mock.lastSummary)
				}
			}
		})
	}
}

func TestTriggerTurnEnd_OnceFlag(t *testing.T) {
	tests := []struct {
		name        string
		once        bool
		turnNumber  int
		wantSwapped bool
	}{
		{name: "once=false turn 1", once: false, turnNumber: 1, wantSwapped: true},
		{name: "once=false turn 2", once: false, turnNumber: 2, wantSwapped: true},
		{name: "once=true turn 1", once: true, turnNumber: 1, wantSwapped: true},
		{name: "once=true turn 2", once: true, turnNumber: 2, wantSwapped: false},
		{name: "once=true turn 5", once: true, turnNumber: 5, wantSwapped: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockThread{}
			trigger := NewTrigger(HookManager{hooks: make(map[HookType][]*Hook)}, "test-conv", false)
			recipeHooks := map[string]llmtypes.HookConfig{
				"turn_end": {Handler: "swap_context", Once: tt.once},
			}

			trigger.TriggerTurnEnd(context.Background(), mock, "response", tt.turnNumber, recipeHooks)

			assert.Equal(t, tt.wantSwapped, mock.swapCalled)
		})
	}
}

func TestTriggerTurnEnd_UnknownHandler(t *testing.T) {
	mock := &mockThread{}
	trigger := NewTrigger(HookManager{hooks: make(map[HookType][]*Hook)}, "test-conv", false)
	recipeHooks := map[string]llmtypes.HookConfig{
		"turn_end": {Handler: "unknown_handler"},
	}

	trigger.TriggerTurnEnd(context.Background(), mock, "response", 1, recipeHooks)

	assert.False(t, mock.swapCalled, "unknown handler should not trigger swap")
}

func TestTriggerMethods_NoHooksConfigured(t *testing.T) {
	trigger := NewTrigger(HookManager{hooks: make(map[HookType][]*Hook)}, "test-conv", false)
	mock := &mockThread{}
	ctx := context.Background()

	t.Run("BeforeToolCall", func(t *testing.T) {
		blocked, reason, input := trigger.TriggerBeforeToolCall(ctx, mock, "bash", `{"cmd":"ls"}`, "id", nil)
		assert.False(t, blocked)
		assert.Empty(t, reason)
		assert.Equal(t, `{"cmd":"ls"}`, input)
	})

	t.Run("AfterToolCall", func(t *testing.T) {
		result := trigger.TriggerAfterToolCall(ctx, mock, "bash", `{}`, "id", tooltypes.StructuredToolResult{}, nil)
		assert.Nil(t, result)
	})

	t.Run("UserMessageSend", func(t *testing.T) {
		blocked, reason := trigger.TriggerUserMessageSend(ctx, mock, "hello", nil)
		assert.False(t, blocked)
		assert.Empty(t, reason)
	})

	t.Run("AgentStop", func(t *testing.T) {
		followUp := trigger.TriggerAgentStop(ctx, mock, []llmtypes.Message{}, nil)
		assert.Nil(t, followUp)
	})
}

// mockBuiltinHandler is a configurable mock for testing trigger methods with custom handlers
type mockBuiltinHandler struct {
	name                  string
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

func (m *mockBuiltinHandler) Name() string { return m.name }

func (m *mockBuiltinHandler) HandleBeforeToolCall(_ context.Context, _ llmtypes.Thread, _ BeforeToolCallPayload) (*BeforeToolCallResult, error) {
	return m.beforeToolCallResult, m.beforeToolCallError
}

func (m *mockBuiltinHandler) HandleAfterToolCall(_ context.Context, _ llmtypes.Thread, _ AfterToolCallPayload) (*AfterToolCallResult, error) {
	return m.afterToolCallResult, m.afterToolCallError
}

func (m *mockBuiltinHandler) HandleUserMessageSend(_ context.Context, _ llmtypes.Thread, _ UserMessageSendPayload) (*UserMessageSendResult, error) {
	return m.userMessageSendResult, m.userMessageSendError
}

func (m *mockBuiltinHandler) HandleAgentStop(_ context.Context, _ llmtypes.Thread, _ AgentStopPayload) (*AgentStopResult, error) {
	return m.agentStopResult, m.agentStopError
}

func (m *mockBuiltinHandler) HandleTurnEnd(_ context.Context, _ llmtypes.Thread, _ TurnEndPayload) (*TurnEndResult, error) {
	return m.turnEndResult, m.turnEndError
}

var _ BuiltinHandler = (*mockBuiltinHandler)(nil)

func TestMockBuiltinHandler_BeforeToolCall(t *testing.T) {
	tests := []struct {
		name        string
		result      *BeforeToolCallResult
		err         error
		wantBlocked bool
		wantReason  string
		wantInput   string
	}{
		{
			name:        "blocks with reason",
			result:      &BeforeToolCallResult{Blocked: true, Reason: "dangerous"},
			wantBlocked: true,
			wantReason:  "dangerous",
		},
		{
			name:      "modifies input",
			result:    &BeforeToolCallResult{Blocked: false, Input: []byte(`{"modified":true}`)},
			wantInput: `{"modified":true}`,
		},
		{
			name: "error propagates",
			err:  errors.New("handler crashed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &mockBuiltinHandler{
				name:                 "test",
				beforeToolCallResult: tt.result,
				beforeToolCallError:  tt.err,
			}

			result, err := handler.HandleBeforeToolCall(context.Background(), nil, BeforeToolCallPayload{})

			if tt.err != nil {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tt.wantBlocked {
				assert.True(t, result.Blocked)
				assert.Equal(t, tt.wantReason, result.Reason)
			}
			if tt.wantInput != "" {
				assert.Equal(t, tt.wantInput, string(result.Input))
			}
		})
	}
}

func TestMockBuiltinHandler_AfterToolCall(t *testing.T) {
	modifiedOutput := &tooltypes.StructuredToolResult{ToolName: "bash", Success: true}

	handler := &mockBuiltinHandler{
		name:                "test",
		afterToolCallResult: &AfterToolCallResult{Output: modifiedOutput},
	}

	result, err := handler.HandleAfterToolCall(context.Background(), nil, AfterToolCallPayload{})

	require.NoError(t, err)
	require.NotNil(t, result.Output)
	assert.True(t, result.Output.Success)
}

func TestMockBuiltinHandler_UserMessageSend(t *testing.T) {
	tests := []struct {
		name        string
		result      *UserMessageSendResult
		wantBlocked bool
	}{
		{
			name:        "blocks message",
			result:      &UserMessageSendResult{Blocked: true, Reason: "forbidden"},
			wantBlocked: true,
		},
		{
			name:        "allows message",
			result:      &UserMessageSendResult{Blocked: false},
			wantBlocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &mockBuiltinHandler{
				name:                  "test",
				userMessageSendResult: tt.result,
			}

			result, err := handler.HandleUserMessageSend(context.Background(), nil, UserMessageSendPayload{})

			require.NoError(t, err)
			assert.Equal(t, tt.wantBlocked, result.Blocked)
		})
	}
}

func TestMockBuiltinHandler_AgentStop(t *testing.T) {
	handler := &mockBuiltinHandler{
		name: "test",
		agentStopResult: &AgentStopResult{
			FollowUpMessages: []string{"review changes", "run tests"},
		},
	}

	result, err := handler.HandleAgentStop(context.Background(), nil, AgentStopPayload{})

	require.NoError(t, err)
	assert.Len(t, result.FollowUpMessages, 2)
	assert.Equal(t, "review changes", result.FollowUpMessages[0])
}
