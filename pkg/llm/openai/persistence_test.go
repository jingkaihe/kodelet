package openai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestSaveConversationMessageCleanup(t *testing.T) {
	tests := []struct {
		name             string
		initialMessages  []openai.ChatCompletionMessage
		expectedMessages []openai.ChatCompletionMessage
		description      string
	}{
		{
			name:             "empty messages list",
			initialMessages:  []openai.ChatCompletionMessage{},
			expectedMessages: []openai.ChatCompletionMessage{},
			description:      "should handle empty message list without error",
		},
		{
			name: "remove single empty message at end",
			initialMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "", // empty content, no tool calls
				},
			},
			expectedMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
			},
			description: "should remove empty message at the end",
		},
		{
			name: "remove multiple empty messages at end",
			initialMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "", // empty content
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "", // empty content
				},
			},
			expectedMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
			},
			description: "should remove multiple empty messages at the end",
		},
		{
			name: "remove orphaned tool call message at end",
			initialMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role: openai.ChatMessageRoleAssistant,
					ToolCalls: []openai.ToolCall{
						{
							ID:   "call_123",
							Type: openai.ToolTypeFunction,
							Function: openai.FunctionCall{
								Name:      "test_tool",
								Arguments: `{"command": "test"}`,
							},
						},
					},
				},
			},
			expectedMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
			},
			description: "should remove orphaned tool call message at the end",
		},
		{
			name: "remove orphaned tool call message with content at end",
			initialMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "I'll help you with that.",
					ToolCalls: []openai.ToolCall{
						{
							ID:   "call_123",
							Type: openai.ToolTypeFunction,
							Function: openai.FunctionCall{
								Name:      "test_tool",
								Arguments: `{"command": "test"}`,
							},
						},
					},
				},
			},
			expectedMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
			},
			description: "should remove orphaned tool call message with content at the end",
		},
		{
			name: "preserve valid tool call followed by tool result",
			initialMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role: openai.ChatMessageRoleAssistant,
					ToolCalls: []openai.ToolCall{
						{
							ID:   "call_123",
							Type: openai.ToolTypeFunction,
							Function: openai.FunctionCall{
								Name:      "test_tool",
								Arguments: `{"command": "test"}`,
							},
						},
					},
				},
				{
					Role:       openai.ChatMessageRoleTool,
					Content:    "tool result",
					ToolCallID: "call_123",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "The result is: tool result",
				},
			},
			expectedMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role: openai.ChatMessageRoleAssistant,
					ToolCalls: []openai.ToolCall{
						{
							ID:   "call_123",
							Type: openai.ToolTypeFunction,
							Function: openai.FunctionCall{
								Name:      "test_tool",
								Arguments: `{"command": "test"}`,
							},
						},
					},
				},
				{
					Role:       openai.ChatMessageRoleTool,
					Content:    "tool result",
					ToolCallID: "call_123",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "The result is: tool result",
				},
			},
			description: "should preserve valid tool call when followed by tool result and final assistant message",
		},
		{
			name: "complex cleanup scenario",
			initialMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Valid response",
				},
				{
					Role: openai.ChatMessageRoleAssistant,
					ToolCalls: []openai.ToolCall{
						{
							ID:   "call_orphaned",
							Type: openai.ToolTypeFunction,
							Function: openai.FunctionCall{
								Name:      "test_tool",
								Arguments: `{"command": "orphaned"}`,
							},
						},
					},
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "", // empty
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "", // empty
				},
			},
			expectedMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Valid response",
				},
			},
			description: "should remove multiple types of invalid messages from the end",
		},
		{
			name: "preserve valid messages only",
			initialMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Hi there!",
				},
			},
			expectedMessages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "Hello",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Hi there!",
				},
			},
			description: "should preserve all valid messages without modification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a thread without persistence to avoid store issues
			thread := NewOpenAIThread(llmtypes.Config{
				Model: "gpt-4.1",
			})

			// Set up state
			state := tools.NewBasicState(context.TODO())
			thread.SetState(state)

			// Set the initial messages
			thread.messages = tt.initialMessages

			// Test the cleanup logic directly by calling the extracted function
			thread.cleanupOrphanedMessages()

			// Verify the messages were cleaned up correctly
			assert.Equal(t, len(tt.expectedMessages), len(thread.messages),
				"Message count mismatch for test: %s", tt.description)

			for i, expectedMsg := range tt.expectedMessages {
				if i >= len(thread.messages) {
					t.Errorf("Expected message %d missing in test: %s", i, tt.description)
					continue
				}

				actualMsg := thread.messages[i]
				assert.Equal(t, expectedMsg.Role, actualMsg.Role,
					"Role mismatch at message %d for test: %s", i, tt.description)
				assert.Equal(t, expectedMsg.Content, actualMsg.Content,
					"Content mismatch at message %d for test: %s", i, tt.description)

				// Compare tool calls if they exist
				assert.Equal(t, len(expectedMsg.ToolCalls), len(actualMsg.ToolCalls),
					"Tool calls count mismatch at message %d for test: %s", i, tt.description)

				for j, expectedToolCall := range expectedMsg.ToolCalls {
					if j >= len(actualMsg.ToolCalls) {
						t.Errorf("Expected tool call %d missing at message %d for test: %s",
							j, i, tt.description)
						continue
					}

					actualToolCall := actualMsg.ToolCalls[j]
					assert.Equal(t, expectedToolCall.ID, actualToolCall.ID,
						"Tool call ID mismatch at message %d, tool call %d for test: %s", i, j, tt.description)
					assert.Equal(t, expectedToolCall.Function.Name, actualToolCall.Function.Name,
						"Tool call name mismatch at message %d, tool call %d for test: %s", i, j, tt.description)
				}

				// Compare tool call ID if this is a tool result message
				if expectedMsg.Role == openai.ChatMessageRoleTool {
					assert.Equal(t, expectedMsg.ToolCallID, actualMsg.ToolCallID,
						"Tool call ID mismatch at message %d for test: %s", i, tt.description)
				}
			}
		})
	}
}

func TestSaveConversationPreservesToolExecutions(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "kodelet_openai_test_tool_executions")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a conversation store
	store, err := conversations.NewJSONConversationStore(filepath.Join(tempDir, "conversations"))
	require.NoError(t, err)

	// Create a thread with persistence enabled
	conversationID := "test-openai-tool-executions-persistence"
	thread := NewOpenAIThread(llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	})

	thread.SetConversationID(conversationID)
	thread.SetState(tools.NewBasicState(context.TODO()))
	thread.EnablePersistence(true)

	// Override the thread's store with our test store
	thread.store = store

	// Add some messages to the thread
	ctx := context.Background()
	thread.AddUserMessage(ctx, "Hello, test message 1")
	thread.AddUserMessage(ctx, "Hello, test message 2")

	// Create initial conversation record
	initialRecord := conversations.ConversationRecord{
		ID:          conversationID,
		ModelType:   "openai",
		Usage:       llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
		RawMessages: []byte(`[{"role":"user","content":"Hello"}]`),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Save initial record
	err = store.Save(initialRecord)
	require.NoError(t, err)

	// Add tool executions using the store's AddToolExecution method
	// AddToolExecution signature: (conversationID, toolName, input, userFacing string, messageIndex int)

	// Add tool executions for different message indices
	err = store.AddToolExecution(conversationID, "bash",
		`{"command": "ls -la", "description": "List files"}`,
		"Listed directory contents", 0)
	require.NoError(t, err)

	err = store.AddToolExecution(conversationID, "file_read",
		`{"file_path": "/tmp/test.txt"}`,
		"Read test file contents", 0)
	require.NoError(t, err)

	err = store.AddToolExecution(conversationID, "grep_tool",
		`{"pattern": "test", "path": "/tmp"}`,
		"Searched for pattern in files", 1)
	require.NoError(t, err)

	// Verify tool executions were stored
	recordBefore, err := store.Load(conversationID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(recordBefore.ToolExecutionsByMessage), "Should have tool executions for 2 messages")
	assert.Equal(t, 2, len(recordBefore.ToolExecutionsByMessage[0]), "Message 0 should have 2 tool executions")
	assert.Equal(t, 1, len(recordBefore.ToolExecutionsByMessage[1]), "Message 1 should have 1 tool execution")

	// Verify the specific tool executions
	assert.Equal(t, "bash", recordBefore.ToolExecutionsByMessage[0][0].ToolName)
	assert.Equal(t, "file_read", recordBefore.ToolExecutionsByMessage[0][1].ToolName)
	assert.Equal(t, "grep_tool", recordBefore.ToolExecutionsByMessage[1][0].ToolName)

	// Now call saveConversation - this is where the bug was happening
	// The old implementation would overwrite ToolExecutionsByMessage
	err = thread.SaveConversation(ctx, true) // Enable summarization to test complete flow
	require.NoError(t, err)

	// Verify that tool executions are still preserved after saveConversation
	recordAfter, err := store.Load(conversationID)
	require.NoError(t, err)

	// This is the regression test - ensure tool executions are preserved
	assert.Equal(t, 2, len(recordAfter.ToolExecutionsByMessage), "Tool executions should be preserved after saveConversation")
	assert.Equal(t, 2, len(recordAfter.ToolExecutionsByMessage[0]), "Message 0 should still have 2 tool executions")
	assert.Equal(t, 1, len(recordAfter.ToolExecutionsByMessage[1]), "Message 1 should still have 1 tool execution")

	// Verify the specific tool executions are intact
	assert.Equal(t, "bash", recordAfter.ToolExecutionsByMessage[0][0].ToolName)
	assert.Equal(t, "Listed directory contents", recordAfter.ToolExecutionsByMessage[0][0].UserFacing)
	assert.Equal(t, "file_read", recordAfter.ToolExecutionsByMessage[0][1].ToolName)
	assert.Equal(t, "Read test file contents", recordAfter.ToolExecutionsByMessage[0][1].UserFacing)
	assert.Equal(t, "grep_tool", recordAfter.ToolExecutionsByMessage[1][0].ToolName)
	assert.Equal(t, "Searched for pattern in files", recordAfter.ToolExecutionsByMessage[1][0].UserFacing)

	// Verify other fields were updated correctly
	assert.NotEmpty(t, recordAfter.Summary, "Summary should be generated")
	assert.Equal(t, "openai", recordAfter.ModelType)
	assert.Equal(t, recordBefore.CreatedAt.Unix(), recordAfter.CreatedAt.Unix(), "CreatedAt should be preserved")
	assert.True(t, recordAfter.UpdatedAt.After(recordBefore.UpdatedAt), "UpdatedAt should be updated")
}

func TestSaveConversationWithoutExistingToolExecutions(t *testing.T) {
	// Test case where there are no existing tool executions
	// This ensures the fix doesn't break new conversations

	tempDir, err := os.MkdirTemp("", "kodelet_openai_test_no_tool_executions")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	store, err := conversations.NewJSONConversationStore(filepath.Join(tempDir, "conversations"))
	require.NoError(t, err)

	conversationID := "test-openai-new-conversation"
	thread := NewOpenAIThread(llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 1000,
	})

	thread.SetConversationID(conversationID)
	thread.SetState(tools.NewBasicState(context.TODO()))
	thread.EnablePersistence(true)
	thread.store = store

	// Add messages and save conversation
	ctx := context.Background()
	thread.AddUserMessage(ctx, "Hello, this is a new conversation")

	err = thread.SaveConversation(ctx, false) // Don't generate summary for this test
	require.NoError(t, err)

	// Verify the conversation was saved correctly
	record, err := store.Load(conversationID)
	require.NoError(t, err)

	// Should have empty or nil ToolExecutionsByMessage
	assert.Empty(t, record.ToolExecutionsByMessage, "New conversation should have no tool executions")
	assert.Equal(t, "openai", record.ModelType)
	assert.NotZero(t, record.CreatedAt)
	assert.NotZero(t, record.UpdatedAt)
}
