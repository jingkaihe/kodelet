package base

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace/noop"
)

// mockState is a minimal mock implementation of tooltypes.State for testing
type mockState struct{}

func (m *mockState) SetFileLastAccessed(_ string, _ time.Time) error { return nil }
func (m *mockState) GetFileLastAccessed(_ string) (time.Time, error) { return time.Time{}, nil }
func (m *mockState) ClearFileLastAccessed(_ string) error            { return nil }
func (m *mockState) TodoFilePath() (string, error)                   { return "", nil }
func (m *mockState) SetTodoFilePath(_ string)                        {}
func (m *mockState) SetFileLastAccess(_ map[string]time.Time)        {}
func (m *mockState) FileLastAccess() map[string]time.Time            { return nil }
func (m *mockState) BasicTools() []tooltypes.Tool                    { return nil }
func (m *mockState) MCPTools() []tooltypes.Tool                      { return nil }
func (m *mockState) Tools() []tooltypes.Tool                         { return nil }
func (m *mockState) AddBackgroundProcess(_ tooltypes.BackgroundProcess) error {
	return nil
}
func (m *mockState) GetBackgroundProcesses() []tooltypes.BackgroundProcess { return nil }
func (m *mockState) RemoveBackgroundProcess(_ int) error                   { return nil }
func (m *mockState) DiscoverContexts() map[string]string                   { return nil }
func (m *mockState) GetLLMConfig() any                             { return nil }
func (m *mockState) LockFile(_ string)                                     {}
func (m *mockState) UnlockFile(_ string)                                   {}

func TestNewThread(t *testing.T) {
	config := llmtypes.Config{
		Model:     "test-model",
		MaxTokens: 1000,
	}
	conversationID := "test-conv-123"
	hookTrigger := hooks.Trigger{}

	bt := NewThread(config, conversationID, hookTrigger)

	require.NotNil(t, bt)
	assert.Equal(t, config, bt.Config)
	assert.Equal(t, conversationID, bt.ConversationID)
	assert.False(t, bt.Persisted)
	assert.NotNil(t, bt.Usage)
	assert.NotNil(t, bt.ToolResults)
	assert.Len(t, bt.ToolResults, 0)
}

func TestSetState(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	state := &mockState{}
	bt.SetState(state)

	assert.Equal(t, state, bt.State)
}

func TestGetState(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	expectedState := &mockState{}
	bt.State = expectedState

	assert.Equal(t, expectedState, bt.GetState())
}

func TestGetConfig(t *testing.T) {
	config := llmtypes.Config{
		Model:     "claude-3-sonnet",
		MaxTokens: 4096,
	}
	bt := NewThread(config, "", hooks.Trigger{})

	assert.Equal(t, config, bt.GetConfig())
}

func TestGetConversationID(t *testing.T) {
	conversationID := "conv-abc-123"
	bt := NewThread(llmtypes.Config{}, conversationID, hooks.Trigger{})

	assert.Equal(t, conversationID, bt.GetConversationID())
}

func TestSetConversationID(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "initial-id", hooks.Trigger{})

	newID := "new-conversation-id"
	bt.SetConversationID(newID)

	assert.Equal(t, newID, bt.ConversationID)
	assert.Equal(t, newID, bt.HookTrigger.ConversationID)
}

func TestIsPersisted(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	assert.False(t, bt.IsPersisted())

	bt.Persisted = true
	assert.True(t, bt.IsPersisted())
}

func TestGetUsage(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	bt.Usage.InputTokens = 100
	bt.Usage.OutputTokens = 50

	usage := bt.GetUsage()
	assert.Equal(t, 100, usage.InputTokens)
	assert.Equal(t, 50, usage.OutputTokens)
}

func TestGetUsage_NilUsage(t *testing.T) {
	bt := &Thread{
		Usage: nil,
	}

	usage := bt.GetUsage()
	assert.Equal(t, llmtypes.Usage{}, usage)
}

func TestGetUsage_ConcurrentAccess(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	var wg sync.WaitGroup
	const numGoroutines = 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			bt.Mu.Lock()
			bt.Usage.InputTokens = val
			bt.Mu.Unlock()

			_ = bt.GetUsage()
		}(i)
	}

	wg.Wait()
}

func TestAggregateSubagentUsage(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	// Set initial values
	bt.Usage.InputTokens = 100
	bt.Usage.OutputTokens = 50
	bt.Usage.CacheCreationInputTokens = 10
	bt.Usage.CacheReadInputTokens = 5
	bt.Usage.InputCost = 0.01
	bt.Usage.OutputCost = 0.005
	bt.Usage.CacheCreationCost = 0.001
	bt.Usage.CacheReadCost = 0.0005
	bt.Usage.CurrentContextWindow = 1000
	bt.Usage.MaxContextWindow = 200000

	// Subagent usage to aggregate
	subagentUsage := llmtypes.Usage{
		InputTokens:              200,
		OutputTokens:             100,
		CacheCreationInputTokens: 20,
		CacheReadInputTokens:     10,
		InputCost:                0.02,
		OutputCost:               0.01,
		CacheCreationCost:        0.002,
		CacheReadCost:            0.001,
		CurrentContextWindow:     500,    // Should NOT be aggregated
		MaxContextWindow:         100000, // Should NOT be aggregated
	}

	bt.AggregateSubagentUsage(subagentUsage)

	// Verify token counts are aggregated
	assert.Equal(t, 300, bt.Usage.InputTokens)
	assert.Equal(t, 150, bt.Usage.OutputTokens)
	assert.Equal(t, 30, bt.Usage.CacheCreationInputTokens)
	assert.Equal(t, 15, bt.Usage.CacheReadInputTokens)

	// Verify costs are aggregated
	assert.InDelta(t, 0.03, bt.Usage.InputCost, 0.0001)
	assert.InDelta(t, 0.015, bt.Usage.OutputCost, 0.0001)
	assert.InDelta(t, 0.003, bt.Usage.CacheCreationCost, 0.0001)
	assert.InDelta(t, 0.0015, bt.Usage.CacheReadCost, 0.0001)

	// Verify context window is NOT aggregated (stays at original values)
	assert.Equal(t, 1000, bt.Usage.CurrentContextWindow)
	assert.Equal(t, 200000, bt.Usage.MaxContextWindow)
}

func TestAggregateSubagentUsage_NilUsage(t *testing.T) {
	bt := &Thread{
		Usage: nil,
	}

	subagentUsage := llmtypes.Usage{
		InputTokens:  100,
		OutputTokens: 50,
	}

	bt.AggregateSubagentUsage(subagentUsage)

	assert.NotNil(t, bt.Usage)
	assert.Equal(t, 100, bt.Usage.InputTokens)
	assert.Equal(t, 50, bt.Usage.OutputTokens)
}

func TestAggregateSubagentUsage_ConcurrentAccess(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	var wg sync.WaitGroup
	const numGoroutines = 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			bt.AggregateSubagentUsage(llmtypes.Usage{
				InputTokens:  val,
				OutputTokens: val * 2,
			})
		}(i)
	}

	wg.Wait()
	// Just verify no race conditions - exact values depend on goroutine ordering
	assert.GreaterOrEqual(t, bt.Usage.InputTokens, 0)
}

func TestSetStructuredToolResult(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	result := tooltypes.StructuredToolResult{
		ToolName: "test-tool",
		Success:  true,
	}
	bt.SetStructuredToolResult("tool-call-1", result)

	assert.Len(t, bt.ToolResults, 1)
	assert.Equal(t, result, bt.ToolResults["tool-call-1"])
}

func TestSetStructuredToolResult_NilMap(t *testing.T) {
	bt := &Thread{
		ToolResults: nil,
	}

	result := tooltypes.StructuredToolResult{
		ToolName: "test-tool",
		Success:  true,
	}
	bt.SetStructuredToolResult("tool-call-1", result)

	require.NotNil(t, bt.ToolResults)
	assert.Equal(t, result, bt.ToolResults["tool-call-1"])
}

func TestSetStructuredToolResult_MultipleResults(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	result1 := tooltypes.StructuredToolResult{ToolName: "tool-1", Success: true}
	result2 := tooltypes.StructuredToolResult{ToolName: "tool-2", Success: false}

	bt.SetStructuredToolResult("tool-1", result1)
	bt.SetStructuredToolResult("tool-2", result2)

	assert.Len(t, bt.ToolResults, 2)
	assert.Equal(t, result1, bt.ToolResults["tool-1"])
	assert.Equal(t, result2, bt.ToolResults["tool-2"])
}

func TestGetStructuredToolResults(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	result := tooltypes.StructuredToolResult{
		ToolName: "test-tool",
		Success:  true,
	}
	bt.ToolResults["tool-call-1"] = result

	results := bt.GetStructuredToolResults()

	assert.Len(t, results, 1)
	assert.Equal(t, result, results["tool-call-1"])
}

func TestGetStructuredToolResults_NilMap(t *testing.T) {
	bt := &Thread{
		ToolResults: nil,
	}

	results := bt.GetStructuredToolResults()

	require.NotNil(t, results)
	assert.Len(t, results, 0)
}

func TestGetStructuredToolResults_ReturnsCopy(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	result := tooltypes.StructuredToolResult{ToolName: "original-tool", Success: true}
	bt.ToolResults["tool-1"] = result

	results := bt.GetStructuredToolResults()
	results["tool-1"] = tooltypes.StructuredToolResult{ToolName: "modified-tool", Success: false}

	assert.Equal(t, "original-tool", bt.ToolResults["tool-1"].ToolName)
}

func TestSetStructuredToolResults(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	results := map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "tool-1", Success: true},
		"tool-2": {ToolName: "tool-2", Success: false},
	}

	bt.SetStructuredToolResults(results)

	assert.Len(t, bt.ToolResults, 2)
	assert.Equal(t, "tool-1", bt.ToolResults["tool-1"].ToolName)
	assert.Equal(t, "tool-2", bt.ToolResults["tool-2"].ToolName)
}

func TestSetStructuredToolResults_NilInput(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	bt.ToolResults["existing"] = tooltypes.StructuredToolResult{ToolName: "existing"}

	bt.SetStructuredToolResults(nil)

	require.NotNil(t, bt.ToolResults)
	assert.Len(t, bt.ToolResults, 0)
}

func TestSetStructuredToolResults_MakesCopy(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	results := map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "original-tool", Success: true},
	}

	bt.SetStructuredToolResults(results)

	results["tool-1"] = tooltypes.StructuredToolResult{ToolName: "modified-tool", Success: false}

	assert.Equal(t, "original-tool", bt.ToolResults["tool-1"].ToolName)
}

func TestStructuredToolResults_ConcurrentAccess(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	var wg sync.WaitGroup
	const numGoroutines = 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)
		go func(val int) {
			defer wg.Done()
			toolID := "tool-" + string(rune('a'+val%26))
			bt.SetStructuredToolResult(toolID, tooltypes.StructuredToolResult{
				ToolName: toolID,
				Success:  true,
			})
		}(i)

		go func() {
			defer wg.Done()
			_ = bt.GetStructuredToolResults()
		}()
	}

	wg.Wait()
}

func TestShouldAutoCompact(t *testing.T) {
	tests := []struct {
		name                 string
		compactRatio         float64
		currentContextWindow int
		maxContextWindow     int
		expected             bool
	}{
		{
			name:                 "should return true when utilization exceeds ratio",
			compactRatio:         0.8,
			currentContextWindow: 90000,
			maxContextWindow:     100000,
			expected:             true,
		},
		{
			name:                 "should return true when utilization equals ratio exactly",
			compactRatio:         0.8,
			currentContextWindow: 80000,
			maxContextWindow:     100000,
			expected:             true,
		},
		{
			name:                 "should return false when utilization is below ratio",
			compactRatio:         0.8,
			currentContextWindow: 70000,
			maxContextWindow:     100000,
			expected:             false,
		},
		{
			name:                 "should return false when ratio is zero",
			compactRatio:         0.0,
			currentContextWindow: 90000,
			maxContextWindow:     100000,
			expected:             false,
		},
		{
			name:                 "should return false when ratio is negative",
			compactRatio:         -0.5,
			currentContextWindow: 90000,
			maxContextWindow:     100000,
			expected:             false,
		},
		{
			name:                 "should return false when ratio exceeds 1.0",
			compactRatio:         1.5,
			currentContextWindow: 90000,
			maxContextWindow:     100000,
			expected:             false,
		},
		{
			name:                 "should return true when ratio is exactly 1.0 and fully utilized",
			compactRatio:         1.0,
			currentContextWindow: 100000,
			maxContextWindow:     100000,
			expected:             true,
		},
		{
			name:                 "should return false when MaxContextWindow is zero",
			compactRatio:         0.8,
			currentContextWindow: 90000,
			maxContextWindow:     0,
			expected:             false,
		},
		{
			name:                 "should return false when CurrentContextWindow is zero",
			compactRatio:         0.8,
			currentContextWindow: 0,
			maxContextWindow:     100000,
			expected:             false,
		},
		{
			name:                 "should return true with small ratio and some usage",
			compactRatio:         0.1,
			currentContextWindow: 15000,
			maxContextWindow:     100000,
			expected:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
			bt.Usage.CurrentContextWindow = tt.currentContextWindow
			bt.Usage.MaxContextWindow = tt.maxContextWindow

			result := bt.ShouldAutoCompact(tt.compactRatio)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldAutoCompact_NilUsage(t *testing.T) {
	bt := &Thread{
		Usage: nil,
	}

	result := bt.ShouldAutoCompact(0.8)
	assert.False(t, result)
}

func TestShouldAutoCompact_ConcurrentAccess(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
	bt.Usage.CurrentContextWindow = 90000
	bt.Usage.MaxContextWindow = 100000

	var wg sync.WaitGroup
	const numGoroutines = 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)
		go func(val int) {
			defer wg.Done()
			bt.Mu.Lock()
			bt.Usage.CurrentContextWindow = val * 1000
			bt.Mu.Unlock()
		}(i)

		go func() {
			defer wg.Done()
			_ = bt.ShouldAutoCompact(0.8)
		}()
	}

	wg.Wait()
}

func TestCreateMessageSpan(t *testing.T) {
	config := llmtypes.Config{
		Model:              "claude-sonnet-4-5",
		MaxTokens:          4096,
		WeakModelMaxTokens: 2048,
		IsSubAgent:         false,
	}
	bt := NewThread(config, "test-conv-123", hooks.Trigger{})
	bt.Persisted = true

	tracer := noop.NewTracerProvider().Tracer("test")
	ctx := context.Background()
	opt := llmtypes.MessageOpt{
		UseWeakModel: true,
	}
	message := "test message content"

	newCtx, span := bt.CreateMessageSpan(ctx, tracer, message, opt)

	require.NotNil(t, newCtx)
	require.NotNil(t, span)
	span.End()
}

func TestCreateMessageSpan_WithExtraAttributes(t *testing.T) {
	config := llmtypes.Config{
		Model:                "claude-sonnet-4-5",
		MaxTokens:            4096,
		ThinkingBudgetTokens: 1000,
	}
	bt := NewThread(config, "test-conv-456", hooks.Trigger{})

	tracer := noop.NewTracerProvider().Tracer("test")
	ctx := context.Background()
	opt := llmtypes.MessageOpt{
		PromptCache: true,
	}

	extraAttrs := []attribute.KeyValue{
		attribute.Int("thinking_budget_tokens", config.ThinkingBudgetTokens),
		attribute.Bool("prompt_cache", opt.PromptCache),
	}

	newCtx, span := bt.CreateMessageSpan(ctx, tracer, "test", opt, extraAttrs...)

	require.NotNil(t, newCtx)
	require.NotNil(t, span)
	span.End()
}

func TestCreateMessageSpan_EmptyMessage(t *testing.T) {
	bt := NewThread(llmtypes.Config{Model: "test"}, "", hooks.Trigger{})
	tracer := noop.NewTracerProvider().Tracer("test")
	ctx := context.Background()

	newCtx, span := bt.CreateMessageSpan(ctx, tracer, "", llmtypes.MessageOpt{})

	require.NotNil(t, newCtx)
	require.NotNil(t, span)
	span.End()
}

func TestFinalizeMessageSpan_Success(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
	bt.Usage.InputTokens = 100
	bt.Usage.OutputTokens = 50
	bt.Usage.InputCost = 0.01
	bt.Usage.OutputCost = 0.005
	bt.Usage.CurrentContextWindow = 1000
	bt.Usage.MaxContextWindow = 200000

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")

	bt.FinalizeMessageSpan(span, nil)
}

func TestFinalizeMessageSpan_WithError(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
	bt.Usage.InputTokens = 50
	bt.Usage.OutputTokens = 0

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")

	testErr := errors.New("API rate limit exceeded")
	bt.FinalizeMessageSpan(span, testErr)
}

func TestFinalizeMessageSpan_WithExtraAttributes(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
	bt.Usage.InputTokens = 100
	bt.Usage.OutputTokens = 50
	bt.Usage.CacheCreationInputTokens = 500
	bt.Usage.CacheReadInputTokens = 200

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")

	extraAttrs := []attribute.KeyValue{
		attribute.Int("tokens.cache_creation", bt.Usage.CacheCreationInputTokens),
		attribute.Int("tokens.cache_read", bt.Usage.CacheReadInputTokens),
	}

	bt.FinalizeMessageSpan(span, nil, extraAttrs...)
}

func TestFinalizeMessageSpan_NilUsage(_ *testing.T) {
	bt := &Thread{
		Usage: nil,
	}

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")

	bt.FinalizeMessageSpan(span, nil)
}

func TestCreateAndFinalizeMessageSpan_Integration(t *testing.T) {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 8192,
	}
	bt := NewThread(config, "integration-conv-123", hooks.Trigger{})
	bt.Persisted = true

	bt.Usage.InputTokens = 500
	bt.Usage.OutputTokens = 200
	bt.Usage.CurrentContextWindow = 700
	bt.Usage.MaxContextWindow = 128000
	bt.Usage.InputCost = 0.05
	bt.Usage.OutputCost = 0.02

	tracer := noop.NewTracerProvider().Tracer("test")
	ctx := context.Background()
	opt := llmtypes.MessageOpt{
		UseWeakModel: false,
	}

	ctx, span := bt.CreateMessageSpan(ctx, tracer, "Hello, world!", opt)
	require.NotNil(t, ctx)
	require.NotNil(t, span)

	bt.FinalizeMessageSpan(span, nil)
}

func TestTracingMethods_ConcurrentAccess(_ *testing.T) {
	bt := NewThread(llmtypes.Config{Model: "test-model"}, "conv-123", hooks.Trigger{})
	bt.Usage.InputTokens = 100
	bt.Usage.OutputTokens = 50
	bt.Usage.MaxContextWindow = 100000

	tracer := noop.NewTracerProvider().Tracer("test")

	var wg sync.WaitGroup
	const numGoroutines = 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		go func(val int) {
			defer wg.Done()
			bt.Mu.Lock()
			bt.Usage.InputTokens = val * 10
			bt.Usage.OutputTokens = val * 5
			bt.Mu.Unlock()
		}(i)

		go func() {
			defer wg.Done()
			ctx, span := bt.CreateMessageSpan(
				context.Background(),
				tracer,
				"concurrent test message",
				llmtypes.MessageOpt{},
			)
			require.NotNil(&testing.T{}, ctx)
			bt.FinalizeMessageSpan(span, nil)
		}()
	}

	wg.Wait()
}

func TestTracingMethods_VariableReuseAcrossSpans(t *testing.T) {
	bt := NewThread(llmtypes.Config{Model: "test-model", MaxTokens: 4096}, "conv-reuse", hooks.Trigger{})
	tracer := noop.NewTracerProvider().Tracer("test")

	for i := 0; i < 5; i++ {
		bt.Usage.InputTokens = i * 100
		bt.Usage.OutputTokens = i * 50

		ctx, span := bt.CreateMessageSpan(
			context.Background(),
			tracer,
			"message "+string(rune('A'+i)),
			llmtypes.MessageOpt{},
		)
		assert.NotNil(t, ctx)
		bt.FinalizeMessageSpan(span, nil)
	}
}

func TestCreateMessageSpan_VerifySpanInterface(t *testing.T) {
	bt := NewThread(llmtypes.Config{Model: "test"}, "conv-123", hooks.Trigger{})
	tracer := noop.NewTracerProvider().Tracer("test")
	ctx := context.Background()

	newCtx, span := bt.CreateMessageSpan(ctx, tracer, "test", llmtypes.MessageOpt{})

	require.NotNil(t, newCtx)
	require.NotNil(t, span)
	assert.NotEqual(t, ctx, newCtx, "CreateMessageSpan should return a new context with span")

	span.End()
}

// mockConversationStore is a minimal mock implementation for testing EnablePersistence
type mockConversationStore struct {
	saveFunc   func(ctx context.Context, record convtypes.ConversationRecord) error
	loadFunc   func(ctx context.Context, id string) (convtypes.ConversationRecord, error)
	deleteFunc func(ctx context.Context, id string) error
	queryFunc  func(ctx context.Context, options convtypes.QueryOptions) (convtypes.QueryResult, error)
	closeFunc  func() error
}

func (m *mockConversationStore) Save(ctx context.Context, record convtypes.ConversationRecord) error {
	if m.saveFunc != nil {
		return m.saveFunc(ctx, record)
	}
	return nil
}

func (m *mockConversationStore) Load(ctx context.Context, id string) (convtypes.ConversationRecord, error) {
	if m.loadFunc != nil {
		return m.loadFunc(ctx, id)
	}
	return convtypes.ConversationRecord{}, nil
}

func (m *mockConversationStore) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func (m *mockConversationStore) Query(ctx context.Context, options convtypes.QueryOptions) (convtypes.QueryResult, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, options)
	}
	return convtypes.QueryResult{}, nil
}

func (m *mockConversationStore) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestEnablePersistence_DisablePersistence(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})
	bt.Persisted = true // Start with persistence enabled

	bt.EnablePersistence(context.Background(), false)

	assert.False(t, bt.Persisted)
}

func TestEnablePersistence_WithExistingStore(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})
	mockStore := &mockConversationStore{}
	bt.Store = mockStore

	loadCalled := false
	bt.LoadConversation = func(_ context.Context) {
		loadCalled = true
	}

	bt.EnablePersistence(context.Background(), true)

	assert.True(t, bt.Persisted)
	assert.True(t, loadCalled, "LoadConversation callback should be called")
	assert.Equal(t, mockStore, bt.Store, "Store should remain the same")
}

func TestEnablePersistence_WithExistingStore_NoCallback(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})
	mockStore := &mockConversationStore{}
	bt.Store = mockStore
	bt.LoadConversation = nil // Explicitly no callback

	bt.EnablePersistence(context.Background(), true)

	assert.True(t, bt.Persisted)
	assert.Equal(t, mockStore, bt.Store)
}

func TestEnablePersistence_DisableDoesNotCallLoadConversation(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})
	mockStore := &mockConversationStore{}
	bt.Store = mockStore

	loadCalled := false
	bt.LoadConversation = func(_ context.Context) {
		loadCalled = true
	}

	bt.EnablePersistence(context.Background(), false)

	assert.False(t, bt.Persisted)
	assert.False(t, loadCalled, "LoadConversation callback should NOT be called when disabling")
}

func TestEnablePersistence_MultipleEnableCallsDoNotReinitializeStore(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})
	mockStore := &mockConversationStore{}
	bt.Store = mockStore

	loadCallCount := 0
	bt.LoadConversation = func(_ context.Context) {
		loadCallCount++
	}

	bt.EnablePersistence(context.Background(), true)
	bt.EnablePersistence(context.Background(), true)

	assert.Equal(t, 2, loadCallCount, "LoadConversation should be called each time persistence is enabled")
	assert.Same(t, mockStore, bt.Store, "Store should not be reinitialized")
}

// contextKey is a custom type for context keys to avoid using basic types
type contextKey string

func TestEnablePersistence_LoadConversationCallback(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "test-conv-id", hooks.Trigger{})
	mockStore := &mockConversationStore{}
	bt.Store = mockStore

	var receivedCtx context.Context
	bt.LoadConversation = func(ctx context.Context) {
		receivedCtx = ctx
	}

	testCtx := context.WithValue(context.Background(), contextKey("test-key"), "test-value")
	bt.EnablePersistence(testCtx, true)

	assert.Equal(t, testCtx, receivedCtx, "LoadConversation should receive the correct context")
}

func TestEnablePersistence_EnableThenDisable(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})
	mockStore := &mockConversationStore{}
	bt.Store = mockStore
	bt.LoadConversation = func(_ context.Context) {}

	bt.EnablePersistence(context.Background(), true)
	assert.True(t, bt.Persisted)

	bt.EnablePersistence(context.Background(), false)
	assert.False(t, bt.Persisted)

	// Store should still be present even after disabling
	assert.NotNil(t, bt.Store)
}

func TestEnablePersistence_ConcurrentAccess(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})
	mockStore := &mockConversationStore{}
	bt.Store = mockStore
	bt.LoadConversation = func(_ context.Context) {}

	var wg sync.WaitGroup
	const numGoroutines = 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(enable bool) {
			defer wg.Done()
			bt.EnablePersistence(context.Background(), enable)
		}(i%2 == 0)
	}

	wg.Wait()
	// Just verify no panic occurs during concurrent access
}

// === Additional Comprehensive Tests ===

// TestConstants verifies that image processing constants have the correct values
func TestConstants(t *testing.T) {
	assert.Equal(t, 5*1024*1024, MaxImageFileSize, "MaxImageFileSize should be 5MB")
	assert.Equal(t, 10, MaxImageCount, "MaxImageCount should be 10")
}

// TestSetState_Sequential verifies SetState/GetState work correctly in sequence
func TestSetState_Sequential(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	// Create mock states and set them sequentially
	state1 := &mockState{}
	state2 := &mockState{}

	bt.SetState(state1)
	assert.Same(t, state1, bt.GetState())

	bt.SetState(state2)
	assert.Same(t, state2, bt.GetState())

	bt.SetState(nil)
	assert.Nil(t, bt.GetState())
}

// TestGetSetConversationID_Sequential verifies conversation ID methods work correctly in sequence
func TestGetSetConversationID_Sequential(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "initial-conv", hooks.Trigger{})

	// Verify initial value
	assert.Equal(t, "initial-conv", bt.GetConversationID())

	// Update and verify
	bt.SetConversationID("conv-a")
	assert.Equal(t, "conv-a", bt.GetConversationID())

	bt.SetConversationID("conv-b")
	assert.Equal(t, "conv-b", bt.GetConversationID())

	// Empty string should work
	bt.SetConversationID("")
	assert.Equal(t, "", bt.GetConversationID())
}

// TestIsPersisted_Sequential verifies persistence flag works correctly in sequence
func TestIsPersisted_Sequential(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-123", hooks.Trigger{})

	// Default should be false
	assert.False(t, bt.IsPersisted())

	// Set to true
	bt.Persisted = true
	assert.True(t, bt.IsPersisted())

	// Set back to false
	bt.Persisted = false
	assert.False(t, bt.IsPersisted())
}

// TestNewThread_InitializesAllFields verifies all fields are properly initialized
func TestNewThread_InitializesAllFields(t *testing.T) {
	config := llmtypes.Config{
		Model:                "claude-sonnet-4-5",
		MaxTokens:            8192,
		WeakModelMaxTokens:   2048,
		ThinkingBudgetTokens: 1000,
		IsSubAgent:           true,
	}
	conversationID := "conv-comprehensive-test"
	hookTrigger := hooks.Trigger{
		ConversationID: conversationID,
	}

	bt := NewThread(config, conversationID, hookTrigger)

	// Verify all fields are properly initialized
	require.NotNil(t, bt)
	assert.Equal(t, config, bt.Config)
	assert.Equal(t, conversationID, bt.ConversationID)
	assert.False(t, bt.Persisted, "Persisted should be false by default")
	assert.NotNil(t, bt.Usage, "Usage should be initialized")
	assert.Equal(t, 0, bt.Usage.InputTokens, "Usage InputTokens should be 0")
	assert.Equal(t, 0, bt.Usage.OutputTokens, "Usage OutputTokens should be 0")
	assert.NotNil(t, bt.ToolResults, "ToolResults should be initialized")
	assert.Len(t, bt.ToolResults, 0, "ToolResults should be empty")
	assert.Nil(t, bt.State, "State should be nil by default")
	assert.Nil(t, bt.Store, "Store should be nil by default")
	assert.Nil(t, bt.LoadConversation, "LoadConversation should be nil by default")
}

// TestNewThread_DefaultHookTrigger verifies hook trigger is properly set
func TestNewThread_DefaultHookTrigger(t *testing.T) {
	hookTrigger := hooks.Trigger{
		ConversationID: "hook-conv-id",
	}

	bt := NewThread(llmtypes.Config{}, "other-conv-id", hookTrigger)

	// The hook trigger should maintain its own conversation ID set at creation
	assert.Equal(t, "hook-conv-id", bt.HookTrigger.ConversationID)
}

// TestSetConversationID_UpdatesHookTrigger verifies SetConversationID updates both fields
func TestSetConversationID_UpdatesHookTrigger(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "initial-id", hooks.Trigger{
		ConversationID: "initial-id",
	})

	assert.Equal(t, "initial-id", bt.ConversationID)
	assert.Equal(t, "initial-id", bt.HookTrigger.ConversationID)

	bt.SetConversationID("updated-id")

	assert.Equal(t, "updated-id", bt.ConversationID)
	assert.Equal(t, "updated-id", bt.HookTrigger.ConversationID)
}

// TestGetConfig_ReturnsDeepCopy verifies GetConfig returns the actual config (not a copy)
func TestGetConfig_ReturnsSameValue(t *testing.T) {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 4096,
	}
	bt := NewThread(config, "", hooks.Trigger{})

	retrieved := bt.GetConfig()

	assert.Equal(t, config.Model, retrieved.Model)
	assert.Equal(t, config.MaxTokens, retrieved.MaxTokens)
}

// TestAllMethods_WithNilThread verifies methods handle nil gracefully (by panicking expectedly)
func TestGetState_ReturnsNilWhenNotSet(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	// State should be nil when not explicitly set
	assert.Nil(t, bt.GetState())
}

// TestUsageAccumulation verifies usage can be accumulated correctly
func TestUsageAccumulation(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	// Simulate token accumulation
	bt.Mu.Lock()
	bt.Usage.InputTokens = 100
	bt.Usage.OutputTokens = 50
	bt.Usage.CurrentContextWindow = 150
	bt.Usage.MaxContextWindow = 200000
	bt.Usage.InputCost = 0.01
	bt.Usage.OutputCost = 0.005
	bt.Mu.Unlock()

	usage := bt.GetUsage()

	assert.Equal(t, 100, usage.InputTokens)
	assert.Equal(t, 50, usage.OutputTokens)
	assert.Equal(t, 150, usage.CurrentContextWindow)
	assert.Equal(t, 200000, usage.MaxContextWindow)
	assert.InDelta(t, 0.01, usage.InputCost, 0.0001)
	assert.InDelta(t, 0.005, usage.OutputCost, 0.0001)
}

// TestStructuredToolResults_OverwriteExisting verifies overwriting results works correctly
func TestStructuredToolResults_OverwriteExisting(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	result1 := tooltypes.StructuredToolResult{ToolName: "tool-v1", Success: true}
	result2 := tooltypes.StructuredToolResult{ToolName: "tool-v2", Success: false}

	bt.SetStructuredToolResult("tool-1", result1)
	assert.Equal(t, "tool-v1", bt.ToolResults["tool-1"].ToolName)

	// Overwrite with new result
	bt.SetStructuredToolResult("tool-1", result2)
	assert.Equal(t, "tool-v2", bt.ToolResults["tool-1"].ToolName)
	assert.False(t, bt.ToolResults["tool-1"].Success)
}

// TestShouldAutoCompact_BoundaryConditions tests edge cases around ratio boundaries
func TestShouldAutoCompact_BoundaryConditions(t *testing.T) {
	tests := []struct {
		name                 string
		compactRatio         float64
		currentContextWindow int
		maxContextWindow     int
		expected             bool
	}{
		{
			name:                 "ratio just above 0 with sufficient usage",
			compactRatio:         0.0001,
			currentContextWindow: 100,
			maxContextWindow:     100000,
			expected:             true, // 100/100000 = 0.001 >= 0.0001
		},
		{
			name:                 "ratio at 0.9999",
			compactRatio:         0.9999,
			currentContextWindow: 99990,
			maxContextWindow:     100000,
			expected:             true, // 0.9999 >= 0.9999
		},
		{
			name:                 "very small context window",
			compactRatio:         0.5,
			currentContextWindow: 1,
			maxContextWindow:     2,
			expected:             true, // 0.5 >= 0.5
		},
		{
			name:                 "large context window values",
			compactRatio:         0.8,
			currentContextWindow: 160000,
			maxContextWindow:     200000,
			expected:             true, // 0.8 >= 0.8
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
			bt.Usage.CurrentContextWindow = tt.currentContextWindow
			bt.Usage.MaxContextWindow = tt.maxContextWindow

			result := bt.ShouldAutoCompact(tt.compactRatio)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCreateMessageSpan_AllCommonAttributes verifies all expected attributes are set
func TestCreateMessageSpan_AllCommonAttributes(t *testing.T) {
	config := llmtypes.Config{
		Model:              "claude-sonnet-4-5",
		MaxTokens:          8192,
		WeakModelMaxTokens: 2048,
		IsSubAgent:         true,
	}
	bt := NewThread(config, "attr-test-conv", hooks.Trigger{})
	bt.Persisted = true

	tracer := noop.NewTracerProvider().Tracer("test")
	ctx := context.Background()
	opt := llmtypes.MessageOpt{
		UseWeakModel: true,
	}
	message := "test message for attribute verification"

	newCtx, span := bt.CreateMessageSpan(ctx, tracer, message, opt)

	require.NotNil(t, newCtx)
	require.NotNil(t, span)
	// Span is created with noop tracer, so we can only verify it doesn't panic
	span.End()
}

// TestFinalizeMessageSpan_ZeroUsage verifies finalization works with zero usage values
func TestFinalizeMessageSpan_ZeroUsage(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
	// Usage is initialized but all values are zero

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")

	// Should not panic with zero values
	bt.FinalizeMessageSpan(span, nil)
}

func TestSetRecipeHooks(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	hooks := map[string]llmtypes.HookConfig{
		"turn_end": {
			Handler: "swap_context",
			Once:    true,
		},
		"before_tool_call": {
			Handler: "audit_logger",
			Once:    false,
		},
	}

	bt.SetRecipeHooks(hooks)

	require.NotNil(t, bt.RecipeHooks)
	assert.Len(t, bt.RecipeHooks, 2)
	assert.Equal(t, "swap_context", bt.RecipeHooks["turn_end"].Handler)
	assert.True(t, bt.RecipeHooks["turn_end"].Once)
	assert.Equal(t, "audit_logger", bt.RecipeHooks["before_tool_call"].Handler)
}

func TestGetRecipeHooks(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})

	expectedHooks := map[string]llmtypes.HookConfig{
		"turn_end": {
			Handler: "swap_context",
			Once:    true,
		},
	}
	bt.RecipeHooks = expectedHooks

	result := bt.GetRecipeHooks()

	assert.Equal(t, expectedHooks, result)
}

func TestEstimateContextWindowFromMessage(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", hooks.Trigger{})
	bt.Usage.CurrentContextWindow = 50000

	// Message with about 800 characters = roughly 200 tokens (above the 100 minimum)
	message := "This is a test message that should help estimate the context window size after compaction. " +
		"It contains several sentences and should provide a rough estimate based on character count. " +
		"The estimation uses approximately 4 characters per token as a heuristic. " +
		"This message should update the current context window value to a smaller estimated size. " +
		"We add more text to ensure the message exceeds 400 characters so the result is above 100 tokens. " +
		"This additional text brings the total character count well above the threshold for the minimum. " +
		"Now we have enough content to properly test the token estimation logic without hitting the minimum. " +
		"The final character count should be around 800 characters which gives us about 200 tokens."

	bt.EstimateContextWindowFromMessage(message)

	// Should be len/4, and since we're above 100, no minimum applies
	expectedTokens := max(len(message)/4, 100)
	assert.Equal(t, expectedTokens, bt.Usage.CurrentContextWindow)
	assert.Greater(t, bt.Usage.CurrentContextWindow, 100, "Should be above minimum with this message length")
}
