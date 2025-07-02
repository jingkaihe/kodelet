package anthropic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestDeserializeMessages(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{
		Model: string(anthropic.ModelClaudeSonnet4_20250514),
	})
	require.NoError(t, err)
	messages, err := DeserializeMessages([]byte(`[]`))
	assert.NoError(t, err)
	assert.Equal(t, 0, len(messages))

	rawMessages := `[
    {
      "content": [
        {
          "text": "ls -la",
          "cache_control": {
            "type": "ephemeral"
          },
          "type": "text"
        }
      ],
      "role": "user"
    },
    {
      "content": [
        {
          "text": "I'll list all files in the current directory with detailed information.",
          "citations": [],
          "type": "text"
        },
        {
          "id": "toolu_01Nc9gURS9CrZjCcruQNCcna",
          "input": {
            "command": "ls -la",
            "description": "List all files with detailed information",
            "timeout": 10
          },
          "name": "bash",
          "type": "tool_use"
        }
      ],
      "role": "assistant"
    },
    {
      "content": [
        {
          "tool_use_id": "toolu_01Nc9gURS9CrZjCcruQNCcna",
          "is_error": false,
          "content": [
            {
              "text": "/root/foo/bar",
              "type": "text"
            }
          ],
          "type": "tool_result"
        }
      ],
      "role": "user"
    },
    {
      "content": [],
      "role": "assistant"
    }
  ]`

	messages, err = DeserializeMessages([]byte(rawMessages))
	assert.NoError(t, err)
	thread.messages = messages

	assert.Equal(t, 4, len(messages)) // remove the empty assistant message
	assert.Equal(t, anthropic.MessageParamRoleUser, messages[0].Role)
	assert.Equal(t, "ls -la", messages[0].Content[0].OfText.Text)
	assert.Equal(t, "text", *messages[0].Content[0].GetType())
	assert.Equal(t, anthropic.CacheControlEphemeralParam{Type: "ephemeral"}, messages[0].Content[0].OfText.CacheControl)

	assert.Equal(t, anthropic.MessageParamRoleAssistant, thread.messages[1].Role)
	assert.Equal(t, "I'll list all files in the current directory with detailed information.", thread.messages[1].Content[0].OfText.Text)
	assert.Equal(t, "text", *thread.messages[1].Content[0].GetType())
	assert.Equal(t, "toolu_01Nc9gURS9CrZjCcruQNCcna", thread.messages[1].Content[1].OfToolUse.ID)
	assert.Equal(t, "bash", thread.messages[1].Content[1].OfToolUse.Name)

	input := thread.messages[1].Content[1].OfToolUse.Input.(map[string]interface{})
	assert.Equal(t, "ls -la", input["command"])
	assert.Equal(t, "List all files with detailed information", input["description"])
	assert.Equal(t, float64(10), input["timeout"])

	assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[2].Role)
	assert.Equal(t, "toolu_01Nc9gURS9CrZjCcruQNCcna", thread.messages[2].Content[0].OfToolResult.ToolUseID)
	assert.Equal(t, false, thread.messages[2].Content[0].OfToolResult.IsError.Value)
	assert.Equal(t, "/root/foo/bar", thread.messages[2].Content[0].OfToolResult.Content[0].OfText.Text)
	assert.Equal(t, "text", *thread.messages[2].Content[0].OfToolResult.Content[0].GetType())

	assert.Equal(t, anthropic.MessageParamRoleAssistant, thread.messages[3].Role)
	assert.Equal(t, 0, len(thread.messages[3].Content))
}

func TestSaveAndLoadConversationWithFileLastAccess(t *testing.T) {
	// Create a thread with a unique conversation ID
	conversationID := "test-file-last-access"
	thread, err := NewAnthropicThread(llmtypes.Config{
		Model: string(anthropic.ModelClaudeSonnet4_20250514),
	})
	require.NoError(t, err)
	thread.SetConversationID(conversationID)

	// Setup state with file access data
	state := tools.NewBasicState(context.TODO())
	thread.SetState(state)

	// Enable persistence
	thread.EnablePersistence(true)

	// Set file access times
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	fileAccessMap := map[string]time.Time{
		"/path/to/file1.txt": now,
		"/path/to/file2.txt": yesterday,
	}
	state.SetFileLastAccess(fileAccessMap)

	// Save the conversation
	err = thread.SaveConversation(context.Background(), false)
	assert.NoError(t, err)

	// Create a new thread with the same conversation ID
	newThread, err := NewAnthropicThread(llmtypes.Config{
		Model: string(anthropic.ModelClaudeSonnet4_20250514),
	})
	require.NoError(t, err)
	newThread.SetConversationID(conversationID)
	newState := tools.NewBasicState(context.TODO())
	newThread.SetState(newState)

	// Enable persistence to load the conversation
	newThread.EnablePersistence(true)

	// Verify the file last access data was preserved
	loadedState := newThread.GetState()
	loadedFileAccess := loadedState.FileLastAccess()

	assert.Equal(t, 2, len(loadedFileAccess))
	assert.Equal(t, now.Unix(), loadedFileAccess["/path/to/file1.txt"].Unix())
	assert.Equal(t, yesterday.Unix(), loadedFileAccess["/path/to/file2.txt"].Unix())
}

func TestSaveConversationMessageCleanup(t *testing.T) {
	tests := []struct {
		name             string
		initialMessages  []anthropic.MessageParam
		expectedMessages []anthropic.MessageParam
		description      string
	}{
		{
			name:             "empty messages list",
			initialMessages:  []anthropic.MessageParam{},
			expectedMessages: []anthropic.MessageParam{},
			description:      "should handle empty message list without error",
		},
		{
			name: "remove single empty message at end",
			initialMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{}, // empty content
				},
			},
			expectedMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
			},
			description: "should remove empty message at the end",
		},
		{
			name: "remove multiple empty messages at end",
			initialMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{}, // empty content
				},
				{
					Role:    anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{}, // empty content
				},
			},
			expectedMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
			},
			description: "should remove multiple empty messages at the end",
		},
		{
			name: "remove orphaned tool use message at end",
			initialMessages: func() []anthropic.MessageParam {
				// Create messages similar to the existing test pattern
				rawMessages := `[
					{
						"content": [
							{
								"text": "Hello",
								"type": "text"
							}
						],
						"role": "user"
					},
					{
						"content": [
							{
								"id": "tool_123",
								"input": {
									"command": "test"
								},
								"name": "test_tool",
								"type": "tool_use"
							}
						],
						"role": "assistant"
					}
				]`
				messages, _ := DeserializeMessages([]byte(rawMessages))
				return messages
			}(),
			expectedMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
			},
			description: "should remove orphaned tool use message at the end",
		},
		{
			name: "remove orphaned tool use message with text message at end",
			initialMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						anthropic.NewTextBlock("Hi there"),
						anthropic.NewToolUseBlock("tool_123", "test_tool", "test"),
					},
				},
			},
			expectedMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
			},
			description: "should remove orphaned tool use message with text message at the end",
		},
		{
			name: "preserve valid tool use followed by tool result",
			initialMessages: func() []anthropic.MessageParam {
				rawMessages := `[
					{
						"content": [
							{
								"text": "Hello",
								"type": "text"
							}
						],
						"role": "user"
					},
					{
						"content": [
							{
								"id": "tool_123",
								"input": {
									"command": "test"
								},
								"name": "test_tool",
								"type": "tool_use"
							}
						],
						"role": "assistant"
					},
					{
						"content": [
							{
								"tool_use_id": "tool_123",
								"is_error": false,
								"content": [
									{
										"text": "result",
										"type": "text"
									}
								],
								"type": "tool_result"
							}
						],
						"role": "user"
					}
				]`
				messages, _ := DeserializeMessages([]byte(rawMessages))
				return messages
			}(),
			expectedMessages: func() []anthropic.MessageParam {
				rawMessages := `[
					{
						"content": [
							{
								"text": "Hello",
								"type": "text"
							}
						],
						"role": "user"
					},
					{
						"content": [
							{
								"id": "tool_123",
								"input": {
									"command": "test"
								},
								"name": "test_tool",
								"type": "tool_use"
							}
						],
						"role": "assistant"
					},
					{
						"content": [
							{
								"tool_use_id": "tool_123",
								"is_error": false,
								"content": [
									{
										"text": "result",
										"type": "text"
									}
								],
								"type": "tool_result"
							}
						],
						"role": "user"
					}
				]`
				messages, _ := DeserializeMessages([]byte(rawMessages))
				return messages
			}(),
			description: "should preserve valid tool use when followed by tool result",
		},
		{
			name: "complex cleanup scenario",
			initialMessages: func() []anthropic.MessageParam {
				rawMessages := `[
					{
						"content": [
							{
								"text": "Hello",
								"type": "text"
							}
						],
						"role": "user"
					},
					{
						"content": [
							{
								"text": "Valid response",
								"type": "text"
							}
						],
						"role": "assistant"
					},
					{
						"content": [
							{
								"id": "tool_orphaned",
								"input": {
									"command": "orphaned"
								},
								"name": "test_tool",
								"type": "tool_use"
							}
						],
						"role": "assistant"
					},
					{
						"content": [],
						"role": "user"
					},
					{
						"content": [],
						"role": "assistant"
					}
				]`
				messages, _ := DeserializeMessages([]byte(rawMessages))
				return messages
			}(),
			expectedMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						anthropic.NewTextBlock("Valid response"),
					},
				},
			},
			description: "should remove multiple types of invalid messages from the end",
		},
		{
			name: "preserve valid messages only",
			initialMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						anthropic.NewTextBlock("Hi there!"),
					},
				},
			},
			expectedMessages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						anthropic.NewTextBlock("Hi there!"),
					},
				},
			},
			description: "should preserve all valid messages without modification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a thread without persistence to avoid store issues
			thread, err := NewAnthropicThread(llmtypes.Config{
				Model: string(anthropic.ModelClaudeSonnet4_20250514),
			})
			require.NoError(t, err)

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
				assert.Equal(t, len(expectedMsg.Content), len(actualMsg.Content),
					"Content length mismatch at message %d for test: %s", i, tt.description)

				// Compare content blocks - focus on key properties
				for j, expectedContent := range expectedMsg.Content {
					if j >= len(actualMsg.Content) {
						t.Errorf("Expected content block %d missing at message %d for test: %s",
							j, i, tt.description)
						continue
					}

					actualContent := actualMsg.Content[j]

					// Compare text content if it's a text block
					if expectedContent.OfText != nil && actualContent.OfText != nil {
						assert.Equal(t, expectedContent.OfText.Text, actualContent.OfText.Text,
							"Text content mismatch at message %d, content %d for test: %s", i, j, tt.description)
					}

					// Compare tool use if it's a tool use block
					if expectedContent.OfToolUse != nil && actualContent.OfToolUse != nil {
						assert.Equal(t, expectedContent.OfToolUse.ID, actualContent.OfToolUse.ID,
							"Tool use ID mismatch at message %d, content %d for test: %s", i, j, tt.description)
						assert.Equal(t, expectedContent.OfToolUse.Name, actualContent.OfToolUse.Name,
							"Tool use name mismatch at message %d, content %d for test: %s", i, j, tt.description)
					}

					// Compare tool result if it's a tool result block
					if expectedContent.OfToolResult != nil && actualContent.OfToolResult != nil {
						assert.Equal(t, expectedContent.OfToolResult.ToolUseID, actualContent.OfToolResult.ToolUseID,
							"Tool result ID mismatch at message %d, content %d for test: %s", i, j, tt.description)
					}
				}
			}
		})
	}
}

func TestSaveConversationPreservesToolExecutions(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "kodelet_test_tool_executions")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a conversation store
	store, err := conversations.NewJSONConversationStore(filepath.Join(tempDir, "conversations"))
	require.NoError(t, err)

	// Create a thread with persistence enabled
	conversationID := "test-tool-executions-persistence"
	thread, err := NewAnthropicThread(llmtypes.Config{
		Model:     string(anthropic.ModelClaudeSonnet4_20250514),
		MaxTokens: 1000,
	})
	require.NoError(t, err)

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
		ModelType:   "anthropic",
		Usage:       llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
		RawMessages: []byte(`[{"role":"user","content":[{"type":"text","text":"Hello"}]}]`),
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
	assert.Equal(t, "anthropic", recordAfter.ModelType)
	assert.Equal(t, recordBefore.CreatedAt.Unix(), recordAfter.CreatedAt.Unix(), "CreatedAt should be preserved")
	assert.True(t, recordAfter.UpdatedAt.After(recordBefore.UpdatedAt), "UpdatedAt should be updated")
}

func TestSaveConversationWithoutExistingToolExecutions(t *testing.T) {
	// Test case where there are no existing tool executions
	// This ensures the fix doesn't break new conversations

	tempDir, err := os.MkdirTemp("", "kodelet_test_no_tool_executions")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	store, err := conversations.NewJSONConversationStore(filepath.Join(tempDir, "conversations"))
	require.NoError(t, err)

	conversationID := "test-new-conversation"
	thread, err := NewAnthropicThread(llmtypes.Config{
		Model:     string(anthropic.ModelClaudeSonnet4_20250514),
		MaxTokens: 1000,
	})
	require.NoError(t, err)

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
	assert.Equal(t, "anthropic", record.ModelType)
	assert.NotZero(t, record.CreatedAt)
	assert.NotZero(t, record.UpdatedAt)
}
