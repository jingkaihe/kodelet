package anthropic

import (
	"context"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
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

func TestExtractMessages(t *testing.T) {
	// Test basic message extraction
	rawMessages := `[
		{
			"content": [
				{
					"text": "Hello there!",
					"type": "text"
				}
			],
			"role": "user"
		},
		{
			"content": [
				{
					"text": "Hi! How can I help you today?",
					"type": "text"
				}
			],
			"role": "assistant"
		}
	]`

	messages, err := ExtractMessages([]byte(rawMessages), nil)
	assert.NoError(t, err)
	assert.Len(t, messages, 2)

	// Check first message
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Hello there!", messages[0].Content)

	// Check second message
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "Hi! How can I help you today?", messages[1].Content)
}

func TestExtractMessagesWithToolUse(t *testing.T) {
	// Test with tool use and tool result
	rawMessages := `[
		{
			"content": [
				{
					"text": "List the files in the current directory",
					"type": "text"
				}
			],
			"role": "user"
		},
		{
			"content": [
				{
					"text": "I'll list the files for you.",
					"type": "text"
				},
				{
					"id": "toolu_123",
					"input": {
						"command": "ls -la",
						"description": "List files"
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
					"tool_use_id": "toolu_123",
					"is_error": false,
					"content": [
						{
							"text": "file1.txt\nfile2.txt\nREADME.md",
							"type": "text"
						}
					],
					"type": "tool_result"
				}
			],
			"role": "user"
		},
		{
			"content": [
				{
					"text": "Here are the files in your directory: file1.txt, file2.txt, README.md",
					"type": "text"
				}
			],
			"role": "assistant"
		}
	]`

	messages, err := ExtractMessages([]byte(rawMessages), nil)
	assert.NoError(t, err)
	assert.Len(t, messages, 5) // user + assistant text + tool use + tool result + assistant final

	// Check user message
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "List the files in the current directory", messages[0].Content)

	// Check assistant text message
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "I'll list the files for you.", messages[1].Content)

	// Check tool use message
	assert.Equal(t, "assistant", messages[2].Role)
	assert.Contains(t, messages[2].Content, "🔧 Using tool: bash")
	assert.Contains(t, messages[2].Content, "ls -la")

	// Check tool result message
	assert.Equal(t, "assistant", messages[3].Role)
	assert.Contains(t, messages[3].Content, "🔄 Tool result:")
	assert.Contains(t, messages[3].Content, "file1.txt\nfile2.txt\nREADME.md")

	// Check final assistant message
	assert.Equal(t, "assistant", messages[4].Role)
	assert.Equal(t, "Here are the files in your directory: file1.txt, file2.txt, README.md", messages[4].Content)
}

func TestExtractMessagesWithMultipleToolResults(t *testing.T) {
	// Test with multiple tool calls and results
	rawMessages := `[
		{
			"content": [
				{
					"text": "Get me the weather and time",
					"type": "text"
				}
			],
			"role": "user"
		},
		{
			"content": [
				{
					"text": "I'll get both the weather and time for you.",
					"type": "text"
				},
				{
					"id": "toolu_weather",
					"input": {
						"location": "NYC"
					},
					"name": "get_weather",
					"type": "tool_use"
				},
				{
					"id": "toolu_time",
					"input": {},
					"name": "get_time",
					"type": "tool_use"
				}
			],
			"role": "assistant"
		},
		{
			"content": [
				{
					"tool_use_id": "toolu_weather",
					"is_error": false,
					"content": [
						{
							"text": "Weather: 75°F, cloudy",
							"type": "text"
						}
					],
					"type": "tool_result"
				}
			],
			"role": "user"
		},
		{
			"content": [
				{
					"tool_use_id": "toolu_time",
					"is_error": false,
					"content": [
						{
							"text": "Current time: 2:30 PM",
							"type": "text"
						}
					],
					"type": "tool_result"
				}
			],
			"role": "user"
		},
		{
			"content": [
				{
					"text": "Here's the information: It's 75°F and cloudy, and the time is 2:30 PM.",
					"type": "text"
				}
			],
			"role": "assistant"
		}
	]`

	toolResults := map[string]tooltypes.StructuredToolResult{
		"toolu_weather": {
			ToolName:  "get_weather",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		},
		"toolu_time": {
			ToolName:  "get_time",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		},
	}

	messages, err := ExtractMessages([]byte(rawMessages), toolResults)
	assert.NoError(t, err)
	assert.Len(t, messages, 7) // user + assistant text + weather tool use + time tool use + weather result + time result + assistant final

	// Check weather tool result uses CLI rendering
	weatherToolResult := messages[4]
	assert.Equal(t, "assistant", weatherToolResult.Role)
	assert.Contains(t, weatherToolResult.Content, "get_weather")

	// Check time tool result uses CLI rendering
	timeToolResult := messages[5]
	assert.Equal(t, "assistant", timeToolResult.Role)
	assert.Contains(t, timeToolResult.Content, "get_time")
}

func TestExtractMessagesWithThinking(t *testing.T) {
	// Test with thinking content
	rawMessages := `[
		{
			"content": [
				{
					"text": "What is 2+2?",
					"type": "text"
				}
			],
			"role": "user"
		},
		{
			"content": [
				{
					"thinking": "The user is asking for a simple arithmetic calculation. 2+2 equals 4.",
					"type": "thinking"
				},
				{
					"text": "2+2 equals 4.",
					"type": "text"
				}
			],
			"role": "assistant"
		}
	]`

	messages, err := ExtractMessages([]byte(rawMessages), nil)
	assert.NoError(t, err)
	assert.Len(t, messages, 3) // user + thinking + assistant text

	// Check user message
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "What is 2+2?", messages[0].Content)

	// Check thinking message
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Contains(t, messages[1].Content, "💭 Thinking:")
	assert.Contains(t, messages[1].Content, "The user is asking for a simple arithmetic calculation. 2+2 equals 4.")

	// Check final assistant message
	assert.Equal(t, "assistant", messages[2].Role)
	assert.Equal(t, "2+2 equals 4.", messages[2].Content)
}
