package openai

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/conversations"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// skipIfNoOpenAIAPIKeyPersistence skips the test if OPENAI_API_KEY is not set
func skipIfNoOpenAIAPIKeyPersistence(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY environment variable not set")
	}
}

// MockConversationStore is a test implementation of the ConversationStore interface
type MockConversationStore struct {
	SavedRecords []conversations.ConversationRecord
	LoadedRecord *conversations.ConversationRecord
}

func (m *MockConversationStore) Save(ctx context.Context, record conversations.ConversationRecord) error {
	m.SavedRecords = append(m.SavedRecords, record)
	return nil
}

func (m *MockConversationStore) Load(ctx context.Context, id string) (conversations.ConversationRecord, error) {
	if m.LoadedRecord != nil {
		return *m.LoadedRecord, nil
	}

	// Find the record with the matching ID
	for _, record := range m.SavedRecords {
		if record.ID == id {
			return record, nil
		}
	}

	return conversations.ConversationRecord{}, nil
}

func (m *MockConversationStore) List(ctx context.Context) ([]conversations.ConversationSummary, error) {
	return nil, nil
}

func (m *MockConversationStore) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *MockConversationStore) Query(ctx context.Context, options conversations.QueryOptions) (conversations.QueryResult, error) {
	return conversations.QueryResult{}, nil
}

func (m *MockConversationStore) Close() error {
	return nil
}

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
			skipIfNoOpenAIAPIKeyPersistence(t)

			// Create a thread without persistence to avoid store issues
			thread, err := NewOpenAIThread(llmtypes.Config{
				Model: "gpt-4.1",
			}, nil)
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
					assert.Fail(t, "Expected message %d missing in test: %s", i, tt.description)
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
						assert.Fail(t, "Expected tool call %d missing at message %d for test: %s",
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

func TestStreamMessages_SimpleTextMessage(t *testing.T) {
	// Test data: simple text message
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hello, how are you?",
		},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "I'm doing well, thank you!",
		},
	}

	rawMessages, err := json.Marshal(messages)
	require.NoError(t, err)

	toolResults := make(map[string]tooltypes.StructuredToolResult)

	// Call StreamMessages
	streamableMessages, err := StreamMessages(rawMessages, toolResults)

	require.NoError(t, err)
	assert.Equal(t, 2, len(streamableMessages))

	// Check user message
	assert.Equal(t, "text", streamableMessages[0].Kind)
	assert.Equal(t, "user", streamableMessages[0].Role)
	assert.Equal(t, "Hello, how are you?", streamableMessages[0].Content)

	// Check assistant message
	assert.Equal(t, "text", streamableMessages[1].Kind)
	assert.Equal(t, "assistant", streamableMessages[1].Role)
	assert.Equal(t, "I'm doing well, thank you!", streamableMessages[1].Content)
}

func TestStreamMessages_ToolUseMessage(t *testing.T) {
	// Test data: message with tool call
	toolCall := openai.ToolCall{
		ID:   "call-123",
		Type: openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      "bash",
			Arguments: `{"command": "ls -la", "timeout": 10}`,
		},
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "List the files in the current directory",
		},
		{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   "",
			ToolCalls: []openai.ToolCall{toolCall},
		},
	}

	rawMessages, err := json.Marshal(messages)
	require.NoError(t, err)

	toolResults := make(map[string]tooltypes.StructuredToolResult)

	// Call StreamMessages
	streamableMessages, err := StreamMessages(rawMessages, toolResults)

	require.NoError(t, err)
	assert.Equal(t, 2, len(streamableMessages))

	// Check user message
	assert.Equal(t, "text", streamableMessages[0].Kind)
	assert.Equal(t, "user", streamableMessages[0].Role)
	assert.Equal(t, "List the files in the current directory", streamableMessages[0].Content)

	// Check tool use message
	assert.Equal(t, "tool-use", streamableMessages[1].Kind)
	assert.Equal(t, "assistant", streamableMessages[1].Role)
	assert.Equal(t, "bash", streamableMessages[1].ToolName)
	assert.Equal(t, "call-123", streamableMessages[1].ToolCallID)
	assert.Contains(t, streamableMessages[1].Input, "command")
}

func TestStreamMessages_ToolResultMessage(t *testing.T) {
	// Test data: tool result message
	messages := []openai.ChatCompletionMessage{
		{
			Role:       openai.ChatMessageRoleTool,
			Content:    "file1.txt\nfile2.txt\n",
			ToolCallID: "call-123",
		},
	}

	rawMessages, err := json.Marshal(messages)
	require.NoError(t, err)

	// Mock structured tool result
	toolResults := map[string]tooltypes.StructuredToolResult{
		"call-123": {
			ToolName: "bash",
			Success:  true,
			Metadata: tooltypes.BashMetadata{
				Command:       "ls -la",
				ExitCode:      0,
				Output:        "file1.txt\nfile2.txt\n",
				ExecutionTime: 100 * time.Millisecond,
			},
		},
	}

	// Call StreamMessages
	streamableMessages, err := StreamMessages(rawMessages, toolResults)

	require.NoError(t, err)
	assert.Equal(t, 1, len(streamableMessages))

	// Check tool result message
	assert.Equal(t, "tool-result", streamableMessages[0].Kind)
	assert.Equal(t, "assistant", streamableMessages[0].Role) // Tool results show as assistant
	assert.Equal(t, "bash", streamableMessages[0].ToolName)
	assert.Equal(t, "call-123", streamableMessages[0].ToolCallID)
	assert.Contains(t, streamableMessages[0].Content, "file1.txt")
}

func TestStreamMessages_SystemMessageSkipped(t *testing.T) {
	// Test data: includes system message which should be skipped
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: "You are a helpful assistant",
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hello",
		},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "Hi there!",
		},
	}

	rawMessages, err := json.Marshal(messages)
	require.NoError(t, err)

	toolResults := make(map[string]tooltypes.StructuredToolResult)

	// Call StreamMessages
	streamableMessages, err := StreamMessages(rawMessages, toolResults)

	require.NoError(t, err)
	assert.Equal(t, 2, len(streamableMessages)) // System message should be skipped

	// Check user message (first after skipping system)
	assert.Equal(t, "text", streamableMessages[0].Kind)
	assert.Equal(t, "user", streamableMessages[0].Role)
	assert.Equal(t, "Hello", streamableMessages[0].Content)

	// Check assistant message
	assert.Equal(t, "text", streamableMessages[1].Kind)
	assert.Equal(t, "assistant", streamableMessages[1].Role)
	assert.Equal(t, "Hi there!", streamableMessages[1].Content)
}

func TestStreamMessages_InvalidJSON(t *testing.T) {
	// Test data: invalid JSON
	rawMessages := json.RawMessage(`{"invalid": json}`)

	toolResults := make(map[string]tooltypes.StructuredToolResult)

	// Call StreamMessages
	streamableMessages, err := StreamMessages(rawMessages, toolResults)

	require.Error(t, err)
	assert.Nil(t, streamableMessages)
	assert.Contains(t, err.Error(), "error unmarshaling messages")
}
