package openai

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestOpenAIThread_StructuredToolResults(t *testing.T) {
	skipIfNoOpenAIAPIKey(t)

	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread, err := NewOpenAIThread(config, nil)
	require.NoError(t, err)

	// Test initial state
	results := thread.GetStructuredToolResults()
	assert.Len(t, results, 0)

	// Test setting a structured tool result
	result1 := tooltypes.StructuredToolResult{
		ToolName:  "file_read",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tooltypes.FileReadMetadata{
			FilePath: "test.go",
			Lines:    []string{"package main"},
			Language: "go",
		},
	}

	thread.SetStructuredToolResult("call_1", result1)

	// Test getting results
	results = thread.GetStructuredToolResults()
	assert.Len(t, results, 1)

	retrieved, exists := results["call_1"]
	assert.True(t, exists)

	assert.Equal(t, "file_read", retrieved.ToolName)

	// Test setting multiple results
	result2 := tooltypes.StructuredToolResult{
		ToolName:  "file_write",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tooltypes.FileWriteMetadata{
			FilePath: "output.txt",
			Content:  "Hello world",
			Size:     11,
			Language: "text",
		},
	}

	thread.SetStructuredToolResult("call_2", result2)

	results = thread.GetStructuredToolResults()
	assert.Len(t, results, 2)
}

func TestOpenAIThread_SetStructuredToolResults(t *testing.T) {
	skipIfNoOpenAIAPIKey(t)

	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread, err := NewOpenAIThread(config, nil)
	require.NoError(t, err)

	// Create bulk results
	bulkResults := map[string]tooltypes.StructuredToolResult{
		"call_1": {
			ToolName:  "glob",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tooltypes.GlobMetadata{
				Pattern: "*.go",
				Path:    "/test",
				Files: []tooltypes.FileInfo{
					{Path: "main.go", Size: 100, Type: "file", Language: "go"},
					{Path: "util.go", Size: 200, Type: "file", Language: "go"},
				},
			},
		},
		"call_2": {
			ToolName:  "command",
			Success:   false,
			Error:     "Command failed",
			Timestamp: time.Now(),
		},
	}

	// Test setting bulk results
	thread.SetStructuredToolResults(bulkResults)

	results := thread.GetStructuredToolResults()
	assert.Len(t, results, 2)

	// Verify individual results
	globResult, exists := results["call_1"]
	assert.True(t, exists)
	assert.Equal(t, "glob", globResult.ToolName)

	cmdResult, exists := results["call_2"]
	assert.True(t, exists)
	assert.False(t, cmdResult.Success)
	assert.Equal(t, "Command failed", cmdResult.Error)

	// Test setting nil (should reset)
	thread.SetStructuredToolResults(nil)
	results = thread.GetStructuredToolResults()
	assert.Len(t, results, 0)
}

func TestOpenAIThread_StructuredResultsConcurrency(t *testing.T) {
	skipIfNoOpenAIAPIKey(t)

	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread, err := NewOpenAIThread(config, nil)
	require.NoError(t, err)

	var wg sync.WaitGroup
	numGoroutines := 10
	resultsPerGoroutine := 10

	// Test concurrent writes
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < resultsPerGoroutine; j++ {
				callID := fmt.Sprintf("call_%d_%d", goroutineID, j)
				result := tooltypes.StructuredToolResult{
					ToolName:  "test_tool",
					Success:   true,
					Timestamp: time.Now(),
				}
				thread.SetStructuredToolResult(callID, result)
			}
		}(i)
	}

	// Test concurrent reads
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < resultsPerGoroutine; j++ {
				_ = thread.GetStructuredToolResults()
			}
		}()
	}

	wg.Wait()

	// Verify final state
	results := thread.GetStructuredToolResults()
	expectedCount := numGoroutines * resultsPerGoroutine
	assert.Len(t, results, expectedCount)
}

// Mock conversation store for testing persistence
type mockConversationStore struct {
	saved   conversations.ConversationRecord
	saveErr error
	loaded  *conversations.ConversationRecord
	loadErr error
}

func (m *mockConversationStore) Save(ctx context.Context, record conversations.ConversationRecord) error {
	m.saved = record
	return m.saveErr
}

func (m *mockConversationStore) Load(ctx context.Context, id string) (conversations.ConversationRecord, error) {
	if m.loadErr != nil {
		return conversations.ConversationRecord{}, m.loadErr
	}
	return *m.loaded, nil
}

func (m *mockConversationStore) Close() error {
	return nil
}

func (m *mockConversationStore) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *mockConversationStore) List(ctx context.Context) ([]conversations.ConversationSummary, error) {
	return []conversations.ConversationSummary{}, nil
}

func (m *mockConversationStore) Query(ctx context.Context, options conversations.QueryOptions) (conversations.QueryResult, error) {
	return conversations.QueryResult{
		ConversationSummaries: []conversations.ConversationSummary{},
		Total:                 0,
		QueryOptions:          options,
	}, nil
}

// Mock state for testing
type mockState struct{}

func (m *mockState) FileLastAccess() map[string]time.Time {
	return map[string]time.Time{}
}

func (m *mockState) SetFileLastAccess(access map[string]time.Time) {
	// No-op for mock
}

// Implement other required State interface methods with minimal implementations
func (m *mockState) SetFileLastAccessed(path string, lastAccessed time.Time) error  { return nil }
func (m *mockState) GetFileLastAccessed(path string) (time.Time, error)             { return time.Time{}, nil }
func (m *mockState) ClearFileLastAccessed(path string) error                        { return nil }
func (m *mockState) TodoFilePath() (string, error)                                  { return "", nil }
func (m *mockState) SetTodoFilePath(path string)                                    {}
func (m *mockState) BasicTools() []tooltypes.Tool                                   { return nil }
func (m *mockState) MCPTools() []tooltypes.Tool                                     { return nil }
func (m *mockState) Tools() []tooltypes.Tool                                        { return nil }
func (m *mockState) AddBackgroundProcess(process tooltypes.BackgroundProcess) error { return nil }
func (m *mockState) GetBackgroundProcesses() []tooltypes.BackgroundProcess          { return nil }
func (m *mockState) RemoveBackgroundProcess(pid int) error                          { return nil }
func (m *mockState) GetRelevantContexts() map[string]string         { return map[string]string{} }
func (m *mockState) GetLLMConfig() interface{}                                      { return nil }

func TestOpenAIThread_PersistenceWithStructuredResults(t *testing.T) {
	skipIfNoOpenAIAPIKey(t)

	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread, err := NewOpenAIThread(config, nil)
	require.NoError(t, err)

	// Set up mock store
	mockStore := &mockConversationStore{}
	thread.store = mockStore
	thread.isPersisted = true

	// Initialize state to avoid nil pointer dereference
	state := &mockState{}
	thread.state = state

	// Add some structured results
	result1 := tooltypes.StructuredToolResult{
		ToolName:  "file_read",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tooltypes.FileReadMetadata{
			FilePath: "test.go",
			Lines:    []string{"package main"},
			Language: "go",
		},
	}

	result2 := tooltypes.StructuredToolResult{
		ToolName:  "grep",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tooltypes.GrepMetadata{
			Pattern: "func",
			Path:    "/src",
			Results: []tooltypes.SearchResult{
				{FilePath: "main.go", Language: "go", Matches: []tooltypes.SearchMatch{
					{LineNumber: 5, Content: "func main() {"},
					{LineNumber: 10, Content: "func helper() {"},
				}},
			},
		},
	}

	thread.SetStructuredToolResult("call_1", result1)
	thread.SetStructuredToolResult("call_2", result2)

	// Test saving
	ctx := context.Background()
	err = thread.SaveConversation(ctx, false)
	require.NoError(t, err)

	// Verify the saved record contains structured results
	assert.Len(t, mockStore.saved.ToolResults, 2)

	savedResult1, exists := mockStore.saved.ToolResults["call_1"]
	assert.True(t, exists)
	assert.Equal(t, "file_read", savedResult1.ToolName)

	// Test loading
	thread2, err := NewOpenAIThread(config, nil)
	require.NoError(t, err)
	thread2.store = &mockConversationStore{
		loaded: &mockStore.saved,
	}
	thread2.isPersisted = true
	thread2.conversationID = mockStore.saved.ID
	thread2.state = &mockState{}

	err = thread2.loadConversation(context.Background())
	require.NoError(t, err)

	// Verify loaded structured results
	loadedResults := thread2.GetStructuredToolResults()
	assert.Len(t, loadedResults, 2)

	loadedResult1, exists := loadedResults["call_1"]
	assert.True(t, exists)
	assert.Equal(t, "file_read", loadedResult1.ToolName)
}
