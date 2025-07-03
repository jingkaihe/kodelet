package openai

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestOpenAIThread_StructuredToolResults(t *testing.T) {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread := NewOpenAIThread(config)

	// Test initial state
	results := thread.GetStructuredToolResults()
	if len(results) != 0 {
		t.Errorf("Expected empty initial results, got %d items", len(results))
	}

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
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	retrieved, exists := results["call_1"]
	if !exists {
		t.Errorf("Expected result for call_1 to exist")
	}

	if retrieved.ToolName != "file_read" {
		t.Errorf("Expected tool name 'file_read', got %s", retrieved.ToolName)
	}

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
	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
}

func TestOpenAIThread_SetStructuredToolResults(t *testing.T) {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread := NewOpenAIThread(config)

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
	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	// Verify individual results
	globResult, exists := results["call_1"]
	if !exists {
		t.Errorf("Expected glob result to exist")
	}
	if globResult.ToolName != "glob" {
		t.Errorf("Expected tool name 'glob', got %s", globResult.ToolName)
	}

	cmdResult, exists := results["call_2"]
	if !exists {
		t.Errorf("Expected command result to exist")
	}
	if cmdResult.Success {
		t.Errorf("Expected command result to be failed")
	}
	if cmdResult.Error != "Command failed" {
		t.Errorf("Expected error 'Command failed', got %s", cmdResult.Error)
	}

	// Test setting nil (should reset)
	thread.SetStructuredToolResults(nil)
	results = thread.GetStructuredToolResults()
	if len(results) != 0 {
		t.Errorf("Expected empty results after setting nil, got %d", len(results))
	}
}

func TestOpenAIThread_StructuredResultsConcurrency(t *testing.T) {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread := NewOpenAIThread(config)

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
	if len(results) != expectedCount {
		t.Errorf("Expected %d results, got %d", expectedCount, len(results))
	}
}

// Mock conversation store for testing persistence
type mockConversationStore struct {
	saved   conversations.ConversationRecord
	saveErr error
	loaded  *conversations.ConversationRecord
	loadErr error
}

func (m *mockConversationStore) Save(record conversations.ConversationRecord) error {
	m.saved = record
	return m.saveErr
}

func (m *mockConversationStore) Load(id string) (conversations.ConversationRecord, error) {
	if m.loadErr != nil {
		return conversations.ConversationRecord{}, m.loadErr
	}
	return *m.loaded, nil
}

func (m *mockConversationStore) Close() error {
	return nil
}

func (m *mockConversationStore) Delete(id string) error {
	return nil
}

func (m *mockConversationStore) List() ([]conversations.ConversationSummary, error) {
	return []conversations.ConversationSummary{}, nil
}

func (m *mockConversationStore) Query(options conversations.QueryOptions) ([]conversations.ConversationSummary, error) {
	return []conversations.ConversationSummary{}, nil
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
func (m *mockState) GetBrowserManager() tooltypes.BrowserManager                    { return nil }
func (m *mockState) SetBrowserManager(manager tooltypes.BrowserManager)             {}
func (m *mockState) GetLLMConfig() interface{}                                      { return nil }

func TestOpenAIThread_PersistenceWithStructuredResults(t *testing.T) {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread := NewOpenAIThread(config)

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
	err := thread.SaveConversation(ctx, false)
	if err != nil {
		t.Fatalf("Failed to save conversation: %v", err)
	}

	// Verify the saved record contains structured results
	if len(mockStore.saved.ToolResults) != 2 {
		t.Errorf("Expected 2 structured results in saved record, got %d", len(mockStore.saved.ToolResults))
	}

	savedResult1, exists := mockStore.saved.ToolResults["call_1"]
	if !exists {
		t.Errorf("Expected call_1 result in saved record")
	}
	if savedResult1.ToolName != "file_read" {
		t.Errorf("Expected saved result1 tool name 'file_read', got %s", savedResult1.ToolName)
	}

	// Test loading
	thread2 := NewOpenAIThread(config)
	thread2.store = &mockConversationStore{
		loaded: &mockStore.saved,
	}
	thread2.isPersisted = true
	thread2.conversationID = mockStore.saved.ID
	thread2.state = &mockState{}

	err = thread2.loadConversation()
	if err != nil {
		t.Fatalf("Failed to load conversation: %v", err)
	}

	// Verify loaded structured results
	loadedResults := thread2.GetStructuredToolResults()
	if len(loadedResults) != 2 {
		t.Errorf("Expected 2 loaded structured results, got %d", len(loadedResults))
	}

	loadedResult1, exists := loadedResults["call_1"]
	if !exists {
		t.Errorf("Expected call_1 result in loaded thread")
	}
	if loadedResult1.ToolName != "file_read" {
		t.Errorf("Expected loaded result1 tool name 'file_read', got %s", loadedResult1.ToolName)
	}
}

func TestOpenAIThread_BackwardCompatibility(t *testing.T) {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	}
	thread := NewOpenAIThread(config)

	// Test that both old and new methods work together
	thread.SetUserFacingToolResult("call_1", "User facing result")

	structuredResult := tooltypes.StructuredToolResult{
		ToolName:  "test_tool",
		Success:   true,
		Timestamp: time.Now(),
	}
	thread.SetStructuredToolResult("call_1", structuredResult)

	// Both should be retrievable
	userFacing := thread.GetUserFacingToolResults()
	structured := thread.GetStructuredToolResults()

	if len(userFacing) != 1 {
		t.Errorf("Expected 1 user facing result, got %d", len(userFacing))
	}

	if len(structured) != 1 {
		t.Errorf("Expected 1 structured result, got %d", len(structured))
	}

	if userFacing["call_1"] != "User facing result" {
		t.Errorf("Expected user facing result, got %s", userFacing["call_1"])
	}

	if structured["call_1"].ToolName != "test_tool" {
		t.Errorf("Expected structured result tool name 'test_tool', got %s", structured["call_1"].ToolName)
	}
}
