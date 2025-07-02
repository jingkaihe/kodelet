package conversations

import (
	"os"
	"testing"
)

func TestConversationRecord_AddToolExecution(t *testing.T) {
	// Create a new conversation record
	record := NewConversationRecord("test-id")

	// Add a tool execution
	record.AddToolExecution("test-tool", "test input", "test user facing result", 0)

	// Verify the tool execution was added
	executions := record.GetToolExecutionsForMessage(0)
	if len(executions) != 1 {
		t.Errorf("Expected 1 tool execution, got %d", len(executions))
	}

	execution := executions[0]
	if execution.ToolName != "test-tool" {
		t.Errorf("Expected ToolName 'test-tool', got '%s'", execution.ToolName)
	}
	if execution.Input != "test input" {
		t.Errorf("Expected Input 'test input', got '%s'", execution.Input)
	}
	if execution.UserFacing != "test user facing result" {
		t.Errorf("Expected UserFacing 'test user facing result', got '%s'", execution.UserFacing)
	}
}

func TestConversationRecord_GetToolExecutionsForMessage(t *testing.T) {
	// Create a new conversation record
	record := NewConversationRecord("test-id")

	// Add tool executions for different message indices
	record.AddToolExecution("tool-1", "input-1", "result-1", 0)
	record.AddToolExecution("tool-2", "input-2", "result-2", 1)
	record.AddToolExecution("tool-3", "input-3", "result-3", 0)

	// Get tool executions for message 0 - should be O(1) now!
	executions := record.GetToolExecutionsForMessage(0)
	if len(executions) != 2 {
		t.Errorf("Expected 2 tool executions for message 0, got %d", len(executions))
	}

	// Verify the executions are correct
	foundTool1 := false
	foundTool3 := false
	for _, exec := range executions {
		if exec.ToolName == "tool-1" && exec.UserFacing == "result-1" {
			foundTool1 = true
		}
		if exec.ToolName == "tool-3" && exec.UserFacing == "result-3" {
			foundTool3 = true
		}
	}
	if !foundTool1 {
		t.Error("Expected to find tool-1 execution for message 0")
	}
	if !foundTool3 {
		t.Error("Expected to find tool-3 execution for message 0")
	}

	// Get tool executions for message 1
	executions = record.GetToolExecutionsForMessage(1)
	if len(executions) != 1 {
		t.Errorf("Expected 1 tool execution for message 1, got %d", len(executions))
	}
	if executions[0].ToolName != "tool-2" {
		t.Errorf("Expected ToolName 'tool-2' for message 1, got '%s'", executions[0].ToolName)
	}

	// Get tool executions for message 2 (should be empty)
	executions = record.GetToolExecutionsForMessage(2)
	if len(executions) != 0 {
		t.Errorf("Expected 0 tool executions for message 2, got %d", len(executions))
	}

	// Test total count
	totalCount := record.GetToolExecutionCount()
	if totalCount != 3 {
		t.Errorf("Expected total count of 3 tool executions, got %d", totalCount)
	}
}

func TestJSONConversationStore_AddToolExecution(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "conversation_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create the store
	store, err := NewJSONConversationStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create and save a conversation record
	record := NewConversationRecord("test-conv-id")
	err = store.Save(record)
	if err != nil {
		t.Fatalf("Failed to save record: %v", err)
	}

	// Add a tool execution
	err = store.AddToolExecution("test-conv-id", "test-tool", "test input", "test result", 0)
	if err != nil {
		t.Fatalf("Failed to add tool execution: %v", err)
	}

	// Load the record and verify the tool execution was added
	loadedRecord, err := store.Load("test-conv-id")
	if err != nil {
		t.Fatalf("Failed to load record: %v", err)
	}

	// Verify the tool execution was added
	executions := loadedRecord.GetToolExecutionsForMessage(0)
	if len(executions) != 1 {
		t.Errorf("Expected 1 tool execution, got %d", len(executions))
	}

	execution := executions[0]
	if execution.ToolName != "test-tool" {
		t.Errorf("Expected ToolName 'test-tool', got '%s'", execution.ToolName)
	}
	if execution.Input != "test input" {
		t.Errorf("Expected Input 'test input', got '%s'", execution.Input)
	}
	if execution.UserFacing != "test result" {
		t.Errorf("Expected UserFacing 'test result', got '%s'", execution.UserFacing)
	}
}

func TestJSONConversationStore_AddToolExecution_NonexistentConversation(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "conversation_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create the store
	store, err := NewJSONConversationStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Try to add a tool execution to a non-existent conversation
	err = store.AddToolExecution("nonexistent-id", "test-tool", "test input", "test result", 0)
	if err == nil {
		t.Error("Expected error when adding tool execution to non-existent conversation")
	}
}

func TestJSONConversationStore_AddMultipleToolExecutions(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "conversation_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create the store
	store, err := NewJSONConversationStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create and save a conversation record
	record := NewConversationRecord("test-conv-id")
	err = store.Save(record)
	if err != nil {
		t.Fatalf("Failed to save record: %v", err)
	}

	// Add multiple tool executions
	err = store.AddToolExecution("test-conv-id", "tool-1", "input-1", "result-1", 0)
	if err != nil {
		t.Fatalf("Failed to add first tool execution: %v", err)
	}

	err = store.AddToolExecution("test-conv-id", "tool-2", "input-2", "result-2", 1)
	if err != nil {
		t.Fatalf("Failed to add second tool execution: %v", err)
	}

	err = store.AddToolExecution("test-conv-id", "tool-3", "input-3", "result-3", 0)
	if err != nil {
		t.Fatalf("Failed to add third tool execution: %v", err)
	}

	// Load the record and verify all tool executions were added
	loadedRecord, err := store.Load("test-conv-id")
	if err != nil {
		t.Fatalf("Failed to load record: %v", err)
	}

	// Verify all tool executions were added
	totalCount := loadedRecord.GetToolExecutionCount()
	if totalCount != 3 {
		t.Errorf("Expected 3 tool executions, got %d", totalCount)
	}

	// Verify we can get executions by message index efficiently
	executions0 := loadedRecord.GetToolExecutionsForMessage(0)
	if len(executions0) != 2 {
		t.Errorf("Expected 2 tool executions for message 0, got %d", len(executions0))
	}

	executions1 := loadedRecord.GetToolExecutionsForMessage(1)
	if len(executions1) != 1 {
		t.Errorf("Expected 1 tool execution for message 1, got %d", len(executions1))
	}
	if executions1[0].ToolName != "tool-2" {
		t.Errorf("Expected ToolName 'tool-2' for message 1, got '%s'", executions1[0].ToolName)
	}
}
