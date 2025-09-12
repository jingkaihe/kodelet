package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConversationService implements a mock for testing
type mockConversationService struct {
	conversation *GetConversationResponse
	err          error
}

func (m *mockConversationService) GetConversation(ctx context.Context, id string) (*GetConversationResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.conversation, nil
}

func (m *mockConversationService) ListConversations(ctx context.Context, req *ListConversationsRequest) (*ListConversationsResponse, error) {
	return nil, nil
}

func (m *mockConversationService) GetToolResult(ctx context.Context, conversationID, toolCallID string) (*GetToolResultResponse, error) {
	return nil, nil
}

func (m *mockConversationService) DeleteConversation(ctx context.Context, id string) error {
	return nil
}

func (m *mockConversationService) Close() error {
	return nil
}

func TestNewConversationStreamer(t *testing.T) {
	service := &mockConversationService{}
	streamer := NewConversationStreamer(service)

	assert.NotNil(t, streamer)
	assert.NotNil(t, streamer.service)
	assert.NotNil(t, streamer.messageParsers)
	assert.Equal(t, 0, len(streamer.messageParsers))
}

func TestRegisterMessageParser(t *testing.T) {
	service := &mockConversationService{}
	streamer := NewConversationStreamer(service)

	// Test parser function
	parser := func(rawMessages json.RawMessage, toolResults map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
		return []StreamableMessage{}, nil
	}

	streamer.RegisterMessageParser("test-provider", parser)

	assert.Equal(t, 1, len(streamer.messageParsers))
	assert.NotNil(t, streamer.messageParsers["test-provider"])
}

func TestConvertToStreamEntry(t *testing.T) {
	service := &mockConversationService{}
	streamer := NewConversationStreamer(service)

	tests := []struct {
		name     string
		input    StreamableMessage
		expected StreamEntry
	}{
		{
			name: "text message",
			input: StreamableMessage{
				Kind:    "text",
				Role:    "user",
				Content: "Hello world",
			},
			expected: StreamEntry{
				Kind:    "text",
				Role:    "user",
				Content: stringPtr("Hello world"),
			},
		},
		{
			name: "thinking message",
			input: StreamableMessage{
				Kind:    "thinking",
				Role:    "assistant",
				Content: "Let me think about this",
			},
			expected: StreamEntry{
				Kind:    "thinking",
				Role:    "assistant",
				Content: stringPtr("Let me think about this"),
			},
		},
		{
			name: "tool-use message",
			input: StreamableMessage{
				Kind:       "tool-use",
				Role:       "assistant",
				ToolName:   "bash",
				ToolCallID: "call-123",
				Input:      `{"command": "ls"}`,
			},
			expected: StreamEntry{
				Kind:       "tool-use",
				Role:       "assistant",
				ToolName:   stringPtr("bash"),
				Input:      stringPtr(`{"command": "ls"}`),
				ToolCallID: stringPtr("call-123"),
			},
		},
		{
			name: "tool-result message",
			input: StreamableMessage{
				Kind:       "tool-result",
				Role:       "user",
				ToolName:   "bash",
				ToolCallID: "call-123",
				Content:    "file1.txt\nfile2.txt",
			},
			expected: StreamEntry{
				Kind:       "tool-result",
				Role:       "user",
				ToolName:   stringPtr("bash"),
				Result:     stringPtr("file1.txt\nfile2.txt"),
				ToolCallID: stringPtr("call-123"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := streamer.convertToStreamEntry(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStreamHistoricalData_Success(t *testing.T) {
	// Mock conversation response
	rawMessages := json.RawMessage(`[{"role": "user", "content": "test"}]`)
	toolResults := make(map[string]tools.StructuredToolResult)

	service := &mockConversationService{
		conversation: &GetConversationResponse{
			ID:          "test-conv-123",
			Provider:    "test-provider",
			RawMessages: rawMessages,
			ToolResults: toolResults,
		},
	}

	streamer := NewConversationStreamer(service)

	// Register mock parser
	var capturedMessages []StreamableMessage
	streamer.RegisterMessageParser("test-provider", func(raw json.RawMessage, results map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
		capturedMessages = []StreamableMessage{
			{Kind: "text", Role: "user", Content: "Hello"},
			{Kind: "text", Role: "assistant", Content: "Hi there"},
		}
		return capturedMessages, nil
	})

	// Capture output by redirecting stdout (simplified for test)
	ctx := context.Background()
	err := streamer.StreamHistoricalData(ctx, "test-conv-123")

	assert.NoError(t, err)
	// Note: In a real test, we'd capture stdout to verify JSON output
}

func TestStreamHistoricalData_ConversationNotFound(t *testing.T) {
	service := &mockConversationService{
		err: fmt.Errorf("conversation not found"),
	}

	streamer := NewConversationStreamer(service)

	ctx := context.Background()
	err := streamer.StreamHistoricalData(ctx, "non-existent")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get conversation")
}

func TestStreamHistoricalData_NoParserRegistered(t *testing.T) {
	service := &mockConversationService{
		conversation: &GetConversationResponse{
			ID:       "test-conv",
			Provider: "unknown-provider",
		},
	}

	streamer := NewConversationStreamer(service)

	ctx := context.Background()
	err := streamer.StreamHistoricalData(ctx, "test-conv")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no message parser registered for provider: unknown-provider")
}

func TestStreamHistoricalData_ParserError(t *testing.T) {
	service := &mockConversationService{
		conversation: &GetConversationResponse{
			ID:       "test-conv",
			Provider: "error-provider",
		},
	}

	streamer := NewConversationStreamer(service)
	streamer.RegisterMessageParser("error-provider", func(raw json.RawMessage, results map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
		return nil, fmt.Errorf("parser error")
	})

	ctx := context.Background()
	err := streamer.StreamHistoricalData(ctx, "test-conv")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse messages")
}

func TestStreamNewMessagesSince(t *testing.T) {
	// Mock conversation response
	service := &mockConversationService{
		conversation: &GetConversationResponse{
			ID:       "test-conv",
			Provider: "test-provider",
		},
	}

	streamer := NewConversationStreamer(service)

	// Register parser that returns 5 messages
	allMessages := []StreamableMessage{
		{Kind: "text", Role: "user", Content: "Message 1"},
		{Kind: "text", Role: "assistant", Content: "Message 2"},
		{Kind: "text", Role: "user", Content: "Message 3"},
		{Kind: "text", Role: "assistant", Content: "Message 4"},
		{Kind: "text", Role: "user", Content: "Message 5"},
	}

	streamer.RegisterMessageParser("test-provider", func(raw json.RawMessage, results map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
		return allMessages, nil
	})

	ctx := context.Background()

	// Test streaming from message 0 (should get all 5)
	count, err := streamer.streamNewMessagesSince(ctx, service.conversation, 0)
	assert.NoError(t, err)
	assert.Equal(t, 5, count)

	// Test streaming from message 3 (should get 2 new messages)
	count, err = streamer.streamNewMessagesSince(ctx, service.conversation, 3)
	assert.NoError(t, err)
	assert.Equal(t, 2, count)

	// Test streaming from message 5 (should get 0 new messages)
	count, err = streamer.streamNewMessagesSince(ctx, service.conversation, 5)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)

	// Test streaming from message 10 (beyond available, should get 0)
	count, err = streamer.streamNewMessagesSince(ctx, service.conversation, 10)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestStreamLiveUpdates_ContextCancellation(t *testing.T) {
	service := &mockConversationService{
		conversation: &GetConversationResponse{
			ID:        "test-conv",
			Provider:  "test-provider",
			UpdatedAt: time.Now(),
		},
	}

	streamer := NewConversationStreamer(service)
	streamer.RegisterMessageParser("test-provider", func(raw json.RawMessage, results map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
		return []StreamableMessage{}, nil
	})

	// Create context that cancels after short time
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := streamer.StreamLiveUpdates(ctx, "test-conv")

	// Should return context.DeadlineExceeded
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}

// Helper function for string pointers
func stringPtr(s string) *string {
	return &s
}

// Integration test that captures actual output (simplified)
func TestStreamEntry_JSONOutput(t *testing.T) {
	service := &mockConversationService{}
	_ = NewConversationStreamer(service)

	entry := StreamEntry{
		Kind:    "text",
		Role:    "user",
		Content: stringPtr("Hello world"),
	}

	// Test JSON marshaling
	jsonBytes, err := json.Marshal(entry)
	require.NoError(t, err)

	// Verify JSON structure
	var decoded map[string]interface{}
	err = json.Unmarshal(jsonBytes, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "text", decoded["kind"])
	assert.Equal(t, "user", decoded["role"])
	assert.Equal(t, "Hello world", decoded["content"])

	// Verify no null fields for omitempty fields
	assert.NotContains(t, string(jsonBytes), "tool_name")
	assert.NotContains(t, string(jsonBytes), "input")
	assert.NotContains(t, string(jsonBytes), "result")
}

func TestStreamEntry_JSONOutput_ToolUse(t *testing.T) {
	entry := StreamEntry{
		Kind:       "tool-use",
		Role:       "assistant",
		ToolName:   stringPtr("bash"),
		Input:      stringPtr(`{"command": "ls"}`),
		ToolCallID: stringPtr("call-123"),
	}

	jsonBytes, err := json.Marshal(entry)
	require.NoError(t, err)

	// Should contain expected fields and not contain content/result
	jsonStr := string(jsonBytes)
	assert.Contains(t, jsonStr, `"kind":"tool-use"`)
	assert.Contains(t, jsonStr, `"tool_name":"bash"`)
	assert.Contains(t, jsonStr, `"input":"{\"command\": \"ls\"}"`)
	assert.Contains(t, jsonStr, `"tool_call_id":"call-123"`)
	assert.NotContains(t, jsonStr, "content")
	assert.NotContains(t, jsonStr, "result")
}

// Benchmark test for streaming performance
func BenchmarkStreamEntry_Convert(b *testing.B) {
	service := &mockConversationService{}
	streamer := NewConversationStreamer(service)

	msg := StreamableMessage{
		Kind:       "tool-result",
		Role:       "assistant",
		ToolName:   "bash",
		ToolCallID: "call-123",
		Content:    strings.Repeat("test output ", 100),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = streamer.convertToStreamEntry(msg)
	}
}
