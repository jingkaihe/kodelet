package openai

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestNeedsResponsesAPI(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{
			name:     "gpt-5-codex requires Response API",
			model:    "gpt-5-codex",
			expected: true,
		},
		{
			name:     "gpt-4.1 does not require Response API",
			model:    "gpt-4.1",
			expected: false,
		},
		{
			name:     "gpt-4o does not require Response API",
			model:    "gpt-4o",
			expected: false,
		},
		{
			name:     "o3 does not require Response API",
			model:    "o3",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := needsResponsesAPI(tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConversationItemSerialization(t *testing.T) {
	t.Run("serialize and deserialize conversation item", func(t *testing.T) {
		item := ConversationItem{
			Type: "input",
			Item: json.RawMessage(`{"type":"message","content":[{"type":"input_text","text":"Hello"}],"role":"user"}`),
			Timestamp: time.Now(),
		}

		// Serialize to JSON
		data, err := json.Marshal(item)
		require.NoError(t, err)
		assert.NotEmpty(t, data)

		// Deserialize back
		var restored ConversationItem
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, item.Type, restored.Type)
		assert.JSONEq(t, string(item.Item), string(restored.Item))
	})

	t.Run("handle output items", func(t *testing.T) {
		item := ConversationItem{
			Type: "output",
			Item: json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"World"}]}`),
			Timestamp: time.Now(),
		}

		data, err := json.Marshal(item)
		require.NoError(t, err)

		var restored ConversationItem
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, "output", restored.Type)
	})
}

func TestConvertImageDetail(t *testing.T) {
	thread := &OpenAIThread{}

	tests := []struct {
		name     string
		detail   string
		expected string
	}{
		{
			name:     "low detail",
			detail:   "low",
			expected: "low",
		},
		{
			name:     "high detail",
			detail:   "high",
			expected: "high",
		},
		{
			name:     "auto detail (default)",
			detail:   "auto",
			expected: "auto",
		},
		{
			name:     "empty defaults to auto",
			detail:   "",
			expected: "auto",
		},
		{
			name:     "unknown defaults to auto",
			detail:   "unknown",
			expected: "auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := thread.convertImageDetail(tt.detail)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestConvertReasoningEffort(t *testing.T) {
	thread := &OpenAIThread{}

	tests := []struct {
		name     string
		effort   string
		expected string
	}{
		{
			name:     "low effort",
			effort:   "low",
			expected: "low",
		},
		{
			name:     "medium effort",
			effort:   "medium",
			expected: "medium",
		},
		{
			name:     "high effort",
			effort:   "high",
			expected: "high",
		},
		{
			name:     "default to medium",
			effort:   "",
			expected: "medium",
		},
		{
			name:     "unknown defaults to medium",
			effort:   "unknown",
			expected: "medium",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := thread.convertReasoningEffort(tt.effort)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestGetSystemPrompt(t *testing.T) {
	t.Run("generates system prompt without state", func(t *testing.T) {
		thread := &OpenAIThread{
			config: llmtypes.Config{
				Model: "gpt-4.1",
			},
		}

		ctx := context.Background()
		prompt := thread.getSystemPrompt(ctx)
		assert.NotEmpty(t, prompt)
	})

	t.Run("generates subagent prompt", func(t *testing.T) {
		thread := &OpenAIThread{
			config: llmtypes.Config{
				Model:      "gpt-4.1",
				IsSubAgent: true,
			},
		}

		ctx := context.Background()
		prompt := thread.getSystemPrompt(ctx)
		assert.NotEmpty(t, prompt)
	})
}

func TestStreamResponseAPIMessages(t *testing.T) {
	t.Run("stream empty conversation", func(t *testing.T) {
		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": []ConversationItem{},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := streamResponseAPIMessages(rawMessages, nil)
		require.NoError(t, err)
		assert.Empty(t, messages)
	})

	t.Run("stream user message", func(t *testing.T) {
		inputItem := ConversationItem{
			Type: "input",
			Item: json.RawMessage(`{"type":"message","content":[{"type":"input_text","text":"Hello"}],"role":"user"}`),
			Timestamp: time.Now(),
		}

		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": []ConversationItem{inputItem},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := streamResponseAPIMessages(rawMessages, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)

		assert.Equal(t, "text", messages[0].Kind)
		assert.Equal(t, "user", messages[0].Role)
		assert.Equal(t, "Hello", messages[0].Content)
	})

	t.Run("stream assistant message", func(t *testing.T) {
		outputItem := ConversationItem{
			Type: "output",
			Item: json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"World"}]}`),
			Timestamp: time.Now(),
		}

		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": []ConversationItem{outputItem},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := streamResponseAPIMessages(rawMessages, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)

		assert.Equal(t, "text", messages[0].Kind)
		assert.Equal(t, "assistant", messages[0].Role)
		assert.Equal(t, "World", messages[0].Content)
	})

	t.Run("stream function call", func(t *testing.T) {
		outputItem := ConversationItem{
			Type: "output",
			Item: json.RawMessage(`{"type":"function_call","name":"get_weather","arguments":"{\"location\":\"SF\"}","call_id":"call_123"}`),
			Timestamp: time.Now(),
		}

		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": []ConversationItem{outputItem},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := streamResponseAPIMessages(rawMessages, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)

		assert.Equal(t, "tool-use", messages[0].Kind)
		assert.Equal(t, "assistant", messages[0].Role)
		assert.Equal(t, "get_weather", messages[0].ToolName)
		assert.Equal(t, "call_123", messages[0].ToolCallID)
		assert.Equal(t, `{"location":"SF"}`, messages[0].Input)
	})

	t.Run("stream function result", func(t *testing.T) {
		inputItem := ConversationItem{
			Type: "input",
			Item: json.RawMessage(`{"type":"function_call_output","call_id":"call_123","output":"Sunny, 72°F"}`),
			Timestamp: time.Now(),
		}

		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": []ConversationItem{inputItem},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		toolResults := map[string]tooltypes.StructuredToolResult{
			"call_123": {
				ToolName: "get_weather",
			},
		}

		messages, err := streamResponseAPIMessages(rawMessages, toolResults)
		require.NoError(t, err)
		require.Len(t, messages, 1)

		assert.Equal(t, "tool-result", messages[0].Kind)
		assert.Equal(t, "assistant", messages[0].Role)
		assert.Equal(t, "get_weather", messages[0].ToolName)
		assert.Equal(t, "call_123", messages[0].ToolCallID)
	})

	t.Run("stream reasoning content", func(t *testing.T) {
		outputItem := ConversationItem{
			Type: "output",
			Item: json.RawMessage(`{"type":"reasoning","content":[{"type":"reasoning_text","text":"Let me think..."}]}`),
			Timestamp: time.Now(),
		}

		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": []ConversationItem{outputItem},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := streamResponseAPIMessages(rawMessages, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)

		assert.Equal(t, "thinking", messages[0].Kind)
		assert.Equal(t, "assistant", messages[0].Role)
		assert.Equal(t, "Let me think...", messages[0].Content)
	})

	t.Run("stream full conversation", func(t *testing.T) {
		items := []ConversationItem{
			{
				Type: "input",
				Item: json.RawMessage(`{"type":"message","content":[{"type":"input_text","text":"What's the weather?"}],"role":"user"}`),
				Timestamp: time.Now(),
			},
			{
				Type: "output",
				Item: json.RawMessage(`{"type":"function_call","name":"get_weather","arguments":"{}","call_id":"call_1"}`),
				Timestamp: time.Now(),
			},
			{
				Type: "input",
				Item: json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"Sunny"}`),
				Timestamp: time.Now(),
			},
			{
				Type: "output",
				Item: json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"It's sunny!"}]}`),
				Timestamp: time.Now(),
			},
		}

		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": items,
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := streamResponseAPIMessages(rawMessages, nil)
		require.NoError(t, err)
		assert.Len(t, messages, 4)

		// Verify order and types
		assert.Equal(t, "text", messages[0].Kind)
		assert.Equal(t, "user", messages[0].Role)

		assert.Equal(t, "tool-use", messages[1].Kind)
		assert.Equal(t, "assistant", messages[1].Role)

		assert.Equal(t, "tool-result", messages[2].Kind)
		assert.Equal(t, "assistant", messages[2].Role)

		assert.Equal(t, "text", messages[3].Kind)
		assert.Equal(t, "assistant", messages[3].Role)
	})
}

func TestStreamMessagesAPITypeDetection(t *testing.T) {
	t.Run("detects Response API format", func(t *testing.T) {
		data := map[string]interface{}{
			"api_type":           "responses",
			"conversation_items": []ConversationItem{},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := StreamMessages(rawMessages, nil)
		require.NoError(t, err)
		assert.NotNil(t, messages)
		assert.Empty(t, messages) // Empty conversation items should return empty slice
	})

	t.Run("falls back to Chat Completion format", func(t *testing.T) {
		// Chat Completion format - just an array of messages
		data := []map[string]interface{}{
			{
				"role":    "user",
				"content": "Hello",
			},
		}
		rawMessages, err := json.Marshal(data)
		require.NoError(t, err)

		messages, err := StreamMessages(rawMessages, nil)
		require.NoError(t, err)
		assert.NotNil(t, messages)
	})
}

func TestResponseAPIConversationPersistence(t *testing.T) {
	t.Run("serialize conversation with Response API format", func(t *testing.T) {
		conversationData := map[string]interface{}{
			"api_type":             "responses",
			"previous_response_id": "resp_123",
			"conversation_items": []ConversationItem{
				{
					Type: "input",
					Item: json.RawMessage(`{"type":"message","content":[{"type":"input_text","text":"Hello"}],"role":"user"}`),
					Timestamp: time.Now(),
				},
			},
		}

		data, err := json.Marshal(conversationData)
		require.NoError(t, err)
		assert.NotEmpty(t, data)

		// Verify it can be deserialized
		var restored map[string]interface{}
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, "responses", restored["api_type"])
		assert.Equal(t, "resp_123", restored["previous_response_id"])
		assert.NotNil(t, restored["conversation_items"])
	})
}

func TestGetLastAssistantMessageText(t *testing.T) {
	t.Run("Chat Completion format - extracts assistant message", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: false,
			messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleUser, Content: "Hello"},
				{Role: openai.ChatMessageRoleAssistant, Content: "Hi there!"},
			},
		}

		text, err := thread.getLastAssistantMessageText()
		require.NoError(t, err)
		assert.Equal(t, "Hi there!", text)
	})

	t.Run("Chat Completion format - finds last assistant message", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: false,
			messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleUser, Content: "Hello"},
				{Role: openai.ChatMessageRoleAssistant, Content: "First response"},
				{Role: openai.ChatMessageRoleUser, Content: "Follow up"},
				{Role: openai.ChatMessageRoleAssistant, Content: "Second response"},
			},
		}

		text, err := thread.getLastAssistantMessageText()
		require.NoError(t, err)
		assert.Equal(t, "Second response", text)
	})

	t.Run("Chat Completion format - returns error when no messages", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: false,
			messages:        []openai.ChatCompletionMessage{},
		}

		_, err := thread.getLastAssistantMessageText()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no messages found")
	})

	t.Run("Chat Completion format - returns error when no assistant messages", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: false,
			messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleUser, Content: "Hello"},
				{Role: openai.ChatMessageRoleSystem, Content: "System message"},
			},
		}

		_, err := thread.getLastAssistantMessageText()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no text content found in assistant message")
	})
}

func TestResponseAPISimplifiedSummarization(t *testing.T) {
	ctx := context.Background()

	t.Run("ShortSummary returns first 10 words of initial user input", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: true,
			conversationItems: []ConversationItem{
				{
					Type: "input",
					Item: json.RawMessage(`{"role":"user","content":[{"type":"input_text","text":"This is a very long message with more than ten words to test the summary"}]}`),
					Timestamp: time.Now(),
				},
				{
					Type: "output",
					Item: json.RawMessage(`{"content":[{"type":"text","text":"Response"}]}`),
					Timestamp: time.Now(),
				},
			},
		}

		summary := thread.ShortSummary(ctx)
		assert.Equal(t, "This is a very long message with more than ten", summary)
	})

	t.Run("ShortSummary handles message with fewer than 10 words", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: true,
			conversationItems: []ConversationItem{
				{
					Type: "input",
					Item: json.RawMessage(`{"role":"user","content":[{"type":"input_text","text":"Short message here"}]}`),
					Timestamp: time.Now(),
				},
			},
		}

		summary := thread.ShortSummary(ctx)
		assert.Equal(t, "Short message here", summary)
	})

	t.Run("ShortSummary handles empty conversation", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI:   true,
			conversationItems: []ConversationItem{},
		}

		summary := thread.ShortSummary(ctx)
		assert.Equal(t, "No conversation yet", summary)
	})

	t.Run("ShortSummary falls back to item count if no user input found", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: true,
			conversationItems: []ConversationItem{
				{
					Type: "output",
					Item: json.RawMessage(`{"content":[{"type":"text","text":"Response"}]}`),
					Timestamp: time.Now(),
				},
			},
		}

		summary := thread.ShortSummary(ctx)
		assert.Equal(t, "Conversation with 1 items", summary)
	})

	t.Run("CompactContext is a no-op for Response API", func(t *testing.T) {
		thread := &OpenAIThread{
			useResponsesAPI: true,
			conversationItems: []ConversationItem{
				{Type: "input", Item: json.RawMessage(`{}`), Timestamp: time.Now()},
				{Type: "output", Item: json.RawMessage(`{}`), Timestamp: time.Now()},
			},
		}

		// Capture original state
		originalCount := len(thread.conversationItems)

		// Call CompactContext - should be a no-op
		err := thread.CompactContext(ctx)
		require.NoError(t, err)

		// Verify state unchanged
		assert.Equal(t, originalCount, len(thread.conversationItems))
	})
}
