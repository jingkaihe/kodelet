package conversations

import (
	"testing"
)

func TestConversationRecord_AddToolExecution(t *testing.T) {
	// Create a new conversation record
	record := NewConversationRecord("test-id")

	// Add a tool execution
	record.AddToolExecution("test-tool", "test input", "test user facing result", 0)

	// Verify the tool execution was added
	if len(record.ToolExecutions) != 1 {
		t.Errorf("Expected 1 tool execution, got %d", len(record.ToolExecutions))
	}

	execution := record.ToolExecutions[0]
	if execution.ToolName != "test-tool" {
		t.Errorf("Expected ToolName 'test-tool', got '%s'", execution.ToolName)
	}
	if execution.Input != "test input" {
		t.Errorf("Expected Input 'test input', got '%s'", execution.Input)
	}
	if execution.UserFacing != "test user facing result" {
		t.Errorf("Expected UserFacing 'test user facing result', got '%s'", execution.UserFacing)
	}
	if execution.MessageIndex != 0 {
		t.Errorf("Expected MessageIndex 0, got %d", execution.MessageIndex)
	}
}

func TestConversationRecord_GetToolExecutionsForMessage(t *testing.T) {
	// Create a new conversation record
	record := NewConversationRecord("test-id")

	// Add tool executions for different message indices
	record.AddToolExecution("tool-1", "input-1", "result-1", 0)
	record.AddToolExecution("tool-2", "input-2", "result-2", 1)
	record.AddToolExecution("tool-3", "input-3", "result-3", 0)

	// Get tool executions for message 0
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
}
