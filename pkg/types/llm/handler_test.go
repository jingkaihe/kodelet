package llm

import (
	"testing"
)

// MockToolExecutionStore implements ToolExecutionStore for testing
type MockToolExecutionStore struct {
	executions []ToolExecutionRecord
}

type ToolExecutionRecord struct {
	ConversationID string
	ToolName       string
	Input          string
	UserFacing     string
	MessageIndex   int
}

func (m *MockToolExecutionStore) AddToolExecution(conversationID, toolName, input, userFacing string, messageIndex int) error {
	m.executions = append(m.executions, ToolExecutionRecord{
		ConversationID: conversationID,
		ToolName:       toolName,
		Input:          input,
		UserFacing:     userFacing,
		MessageIndex:   messageIndex,
	})
	return nil
}

// MockMessageHandler implements MessageHandler for testing
type MockMessageHandler struct {
	texts       []string
	toolUses    []ToolUseRecord
	toolResults []ToolResultRecord
	thinking    []string
	doneCount   int
}

type ToolUseRecord struct {
	ToolName string
	Input    string
}

type ToolResultRecord struct {
	ToolName string
	Result   string
}

func (m *MockMessageHandler) HandleText(text string) {
	m.texts = append(m.texts, text)
}

func (m *MockMessageHandler) HandleToolUse(toolName string, input string) {
	m.toolUses = append(m.toolUses, ToolUseRecord{
		ToolName: toolName,
		Input:    input,
	})
}

func (m *MockMessageHandler) HandleToolResult(toolName string, result string) {
	m.toolResults = append(m.toolResults, ToolResultRecord{
		ToolName: toolName,
		Result:   result,
	})
}

func (m *MockMessageHandler) HandleThinking(thinking string) {
	m.thinking = append(m.thinking, thinking)
}

func (m *MockMessageHandler) HandleDone() {
	m.doneCount++
}

func TestConversationStoringHandler_HandleToolUseAndResult(t *testing.T) {
	// Create mock store and handler
	mockStore := &MockToolExecutionStore{}
	mockHandler := &MockMessageHandler{}

	// Create the conversation storing handler
	storingHandler := NewConversationStoringHandler(mockHandler, mockStore, "test-conversation", 0)

	// Test tool use
	storingHandler.HandleToolUse("test-tool", "test input")

	// Verify the wrapped handler received the call
	if len(mockHandler.toolUses) != 1 {
		t.Errorf("Expected 1 tool use, got %d", len(mockHandler.toolUses))
	}
	if mockHandler.toolUses[0].ToolName != "test-tool" {
		t.Errorf("Expected ToolName 'test-tool', got '%s'", mockHandler.toolUses[0].ToolName)
	}
	if mockHandler.toolUses[0].Input != "test input" {
		t.Errorf("Expected Input 'test input', got '%s'", mockHandler.toolUses[0].Input)
	}

	// Test tool result
	storingHandler.HandleToolResult("test-tool", "test result")

	// Verify the wrapped handler received the call
	if len(mockHandler.toolResults) != 1 {
		t.Errorf("Expected 1 tool result, got %d", len(mockHandler.toolResults))
	}
	if mockHandler.toolResults[0].ToolName != "test-tool" {
		t.Errorf("Expected ToolName 'test-tool', got '%s'", mockHandler.toolResults[0].ToolName)
	}
	if mockHandler.toolResults[0].Result != "test result" {
		t.Errorf("Expected Result 'test result', got '%s'", mockHandler.toolResults[0].Result)
	}

	// Verify the store received the execution
	if len(mockStore.executions) != 1 {
		t.Errorf("Expected 1 tool execution in store, got %d", len(mockStore.executions))
	}
	execution := mockStore.executions[0]
	if execution.ConversationID != "test-conversation" {
		t.Errorf("Expected ConversationID 'test-conversation', got '%s'", execution.ConversationID)
	}
	if execution.ToolName != "test-tool" {
		t.Errorf("Expected ToolName 'test-tool', got '%s'", execution.ToolName)
	}
	if execution.Input != "test input" {
		t.Errorf("Expected Input 'test input', got '%s'", execution.Input)
	}
	if execution.UserFacing != "test result" {
		t.Errorf("Expected UserFacing 'test result', got '%s'", execution.UserFacing)
	}
	if execution.MessageIndex != 0 {
		t.Errorf("Expected MessageIndex 0, got %d", execution.MessageIndex)
	}
}

func TestConversationStoringHandler_UpdateMessageIndex(t *testing.T) {
	// Create mock store and handler
	mockStore := &MockToolExecutionStore{}
	mockHandler := &MockMessageHandler{}

	// Create the conversation storing handler
	storingHandler := NewConversationStoringHandler(mockHandler, mockStore, "test-conversation", 0)

	// Update message index
	storingHandler.UpdateMessageIndex(5)

	// Test tool use and result with new message index
	storingHandler.HandleToolUse("test-tool", "test input")
	storingHandler.HandleToolResult("test-tool", "test result")

	// Verify the store received the execution with correct message index
	if len(mockStore.executions) != 1 {
		t.Errorf("Expected 1 tool execution in store, got %d", len(mockStore.executions))
	}
	execution := mockStore.executions[0]
	if execution.MessageIndex != 5 {
		t.Errorf("Expected MessageIndex 5, got %d", execution.MessageIndex)
	}
}

func TestConversationStoringHandler_ForwardsAllMethods(t *testing.T) {
	// Create mock store and handler
	mockStore := &MockToolExecutionStore{}
	mockHandler := &MockMessageHandler{}

	// Create the conversation storing handler
	storingHandler := NewConversationStoringHandler(mockHandler, mockStore, "test-conversation", 0)

	// Test all handler methods
	storingHandler.HandleText("test text")
	storingHandler.HandleThinking("test thinking")
	storingHandler.HandleDone()

	// Verify all calls were forwarded
	if len(mockHandler.texts) != 1 || mockHandler.texts[0] != "test text" {
		t.Errorf("HandleText not forwarded correctly")
	}
	if len(mockHandler.thinking) != 1 || mockHandler.thinking[0] != "test thinking" {
		t.Errorf("HandleThinking not forwarded correctly")
	}
	if mockHandler.doneCount != 1 {
		t.Errorf("HandleDone not forwarded correctly")
	}
}

func TestConversationStoringHandler_MultipleToolExecutions(t *testing.T) {
	// Create mock store and handler
	mockStore := &MockToolExecutionStore{}
	mockHandler := &MockMessageHandler{}

	// Create the conversation storing handler
	storingHandler := NewConversationStoringHandler(mockHandler, mockStore, "test-conversation", 0)

	// Test multiple tool executions
	storingHandler.HandleToolUse("tool-1", "input-1")
	storingHandler.HandleToolResult("tool-1", "result-1")

	storingHandler.HandleToolUse("tool-2", "input-2")
	storingHandler.HandleToolResult("tool-2", "result-2")

	// Verify both executions were stored
	if len(mockStore.executions) != 2 {
		t.Errorf("Expected 2 tool executions in store, got %d", len(mockStore.executions))
	}

	// Verify first execution
	exec1 := mockStore.executions[0]
	if exec1.ToolName != "tool-1" || exec1.Input != "input-1" || exec1.UserFacing != "result-1" {
		t.Errorf("First execution not stored correctly: %+v", exec1)
	}

	// Verify second execution
	exec2 := mockStore.executions[1]
	if exec2.ToolName != "tool-2" || exec2.Input != "input-2" || exec2.UserFacing != "result-2" {
		t.Errorf("Second execution not stored correctly: %+v", exec2)
	}
}

func TestConversationStoringHandler_MultipleSameToolExecutions(t *testing.T) {
	// Create mock store and handler
	mockStore := &MockToolExecutionStore{}
	mockHandler := &MockMessageHandler{}

	// Create the conversation storing handler
	storingHandler := NewConversationStoringHandler(mockHandler, mockStore, "test-conversation", 0)

	// Test multiple executions of the same tool (the problematic case)
	storingHandler.HandleToolUse("bash", "ls -la")
	storingHandler.HandleToolUse("bash", "pwd")
	storingHandler.HandleToolUse("bash", "whoami")

	// Results come back in same order
	storingHandler.HandleToolResult("bash", "result of ls -la")
	storingHandler.HandleToolResult("bash", "result of pwd")
	storingHandler.HandleToolResult("bash", "result of whoami")

	// Verify all executions were stored correctly
	if len(mockStore.executions) != 3 {
		t.Errorf("Expected 3 tool executions in store, got %d", len(mockStore.executions))
	}

	// Verify correct correlation (FIFO order)
	expectedExecutions := []struct {
		input, result string
	}{
		{"ls -la", "result of ls -la"},
		{"pwd", "result of pwd"},
		{"whoami", "result of whoami"},
	}

	for i, expected := range expectedExecutions {
		exec := mockStore.executions[i]
		if exec.ToolName != "bash" {
			t.Errorf("Execution %d: expected ToolName 'bash', got '%s'", i, exec.ToolName)
		}
		if exec.Input != expected.input {
			t.Errorf("Execution %d: expected Input '%s', got '%s'", i, expected.input, exec.Input)
		}
		if exec.UserFacing != expected.result {
			t.Errorf("Execution %d: expected UserFacing '%s', got '%s'", i, expected.result, exec.UserFacing)
		}
	}
}