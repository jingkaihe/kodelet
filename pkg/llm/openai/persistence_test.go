package openai

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// MockConversationStore is a test implementation of the ConversationStore interface
type MockConversationStore struct {
	SavedRecords []conversations.ConversationRecord
	LoadedRecord *conversations.ConversationRecord
}

func (m *MockConversationStore) Save(record conversations.ConversationRecord) error {
	m.SavedRecords = append(m.SavedRecords, record)
	return nil
}

func (m *MockConversationStore) Load(id string) (conversations.ConversationRecord, error) {
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

func (m *MockConversationStore) List() ([]conversations.ConversationSummary, error) {
	return nil, nil
}

func (m *MockConversationStore) Delete(id string) error {
	return nil
}

func (m *MockConversationStore) Query(options conversations.QueryOptions) ([]conversations.ConversationSummary, error) {
	return nil, nil
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
