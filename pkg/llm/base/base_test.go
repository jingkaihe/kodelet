package base

import (
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
func (m *mockState) GetLLMConfig() interface{}                             { return nil }
func (m *mockState) LockFile(_ string)                                     {}
func (m *mockState) UnlockFile(_ string)                                   {}

func TestNewThread(t *testing.T) {
	config := llmtypes.Config{
		Model:     "test-model",
		MaxTokens: 1000,
	}
	conversationID := "test-conv-123"
	hookTrigger := hooks.Trigger{}

	bt := NewThread(config, conversationID, nil, hookTrigger)

	require.NotNil(t, bt)
	assert.Equal(t, config, bt.Config)
	assert.Equal(t, conversationID, bt.ConversationID)
	assert.False(t, bt.Persisted)
	assert.NotNil(t, bt.Usage)
	assert.NotNil(t, bt.ToolResults)
	assert.Len(t, bt.ToolResults, 0)
}

func TestSetState(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

	state := &mockState{}
	bt.SetState(state)

	assert.Equal(t, state, bt.State)
}

func TestGetState(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

	expectedState := &mockState{}
	bt.State = expectedState

	assert.Equal(t, expectedState, bt.GetState())
}

func TestGetConfig(t *testing.T) {
	config := llmtypes.Config{
		Model:     "claude-3-sonnet",
		MaxTokens: 4096,
	}
	bt := NewThread(config, "", nil, hooks.Trigger{})

	assert.Equal(t, config, bt.GetConfig())
}

func TestGetConversationID(t *testing.T) {
	conversationID := "conv-abc-123"
	bt := NewThread(llmtypes.Config{}, conversationID, nil, hooks.Trigger{})

	assert.Equal(t, conversationID, bt.GetConversationID())
}

func TestSetConversationID(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "initial-id", nil, hooks.Trigger{})

	newID := "new-conversation-id"
	bt.SetConversationID(newID)

	assert.Equal(t, newID, bt.ConversationID)
	assert.Equal(t, newID, bt.HookTrigger.ConversationID)
}

func TestIsPersisted(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

	assert.False(t, bt.IsPersisted())

	bt.Persisted = true
	assert.True(t, bt.IsPersisted())
}

func TestGetUsage(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

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
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

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

func TestSetStructuredToolResult(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

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
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

	result1 := tooltypes.StructuredToolResult{ToolName: "tool-1", Success: true}
	result2 := tooltypes.StructuredToolResult{ToolName: "tool-2", Success: false}

	bt.SetStructuredToolResult("tool-1", result1)
	bt.SetStructuredToolResult("tool-2", result2)

	assert.Len(t, bt.ToolResults, 2)
	assert.Equal(t, result1, bt.ToolResults["tool-1"])
	assert.Equal(t, result2, bt.ToolResults["tool-2"])
}

func TestGetStructuredToolResults(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

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
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

	result := tooltypes.StructuredToolResult{ToolName: "original-tool", Success: true}
	bt.ToolResults["tool-1"] = result

	results := bt.GetStructuredToolResults()
	results["tool-1"] = tooltypes.StructuredToolResult{ToolName: "modified-tool", Success: false}

	assert.Equal(t, "original-tool", bt.ToolResults["tool-1"].ToolName)
}

func TestSetStructuredToolResults(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

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
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

	bt.ToolResults["existing"] = tooltypes.StructuredToolResult{ToolName: "existing"}

	bt.SetStructuredToolResults(nil)

	require.NotNil(t, bt.ToolResults)
	assert.Len(t, bt.ToolResults, 0)
}

func TestSetStructuredToolResults_MakesCopy(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

	results := map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "original-tool", Success: true},
	}

	bt.SetStructuredToolResults(results)

	results["tool-1"] = tooltypes.StructuredToolResult{ToolName: "modified-tool", Success: false}

	assert.Equal(t, "original-tool", bt.ToolResults["tool-1"].ToolName)
}

func TestStructuredToolResults_ConcurrentAccess(_ *testing.T) {
	bt := NewThread(llmtypes.Config{}, "", nil, hooks.Trigger{})

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
