package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errNotImplemented = errors.New("not implemented in mock")

// mockConversationService implements a mock for testing
type mockConversationService struct {
	conversation *GetConversationResponse
	err          error
}

func (m *mockConversationService) GetConversation(_ context.Context, _ string) (*GetConversationResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.conversation, nil
}

func (m *mockConversationService) ListConversations(_ context.Context, _ *ListConversationsRequest) (*ListConversationsResponse, error) {
	return nil, errNotImplemented
}

func (m *mockConversationService) GetToolResult(_ context.Context, _, _ string) (*GetToolResultResponse, error) {
	return nil, errNotImplemented
}

func (m *mockConversationService) DeleteConversation(_ context.Context, _ string) error {
	return nil
}

func (m *mockConversationService) Close() error {
	return nil
}

func TestConvertToStreamEntry(t *testing.T) {
	service := &mockConversationService{}
	streamer := NewConversationStreamer(service)

	tests := []struct {
		name           string
		input          StreamableMessage
		conversationID string
		expected       StreamEntry
	}{
		{
			name: "text message",
			input: StreamableMessage{
				Kind:    "text",
				Role:    "user",
				Content: "Hello world",
			},
			conversationID: "test-conv-123",
			expected: StreamEntry{
				Kind:           "text",
				Role:           "user",
				Content:        "Hello world",
				ConversationID: "test-conv-123",
			},
		},
		{
			name: "thinking message",
			input: StreamableMessage{
				Kind:    "thinking",
				Role:    "assistant",
				Content: "Let me think about this",
			},
			conversationID: "test-conv-456",
			expected: StreamEntry{
				Kind:           "thinking",
				Role:           "assistant",
				Content:        "Let me think about this",
				ConversationID: "test-conv-456",
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
			conversationID: "test-conv-789",
			expected: StreamEntry{
				Kind:           "tool-use",
				Role:           "assistant",
				ToolName:       "bash",
				Input:          `{"command": "ls"}`,
				ToolCallID:     "call-123",
				ConversationID: "test-conv-789",
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
			conversationID: "test-conv-000",
			expected: StreamEntry{
				Kind:           "tool-result",
				Role:           "user",
				ToolName:       "bash",
				Result:         "file1.txt\nfile2.txt",
				ToolCallID:     "call-123",
				ConversationID: "test-conv-000",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := streamer.convertToStreamEntry(tt.input, tt.conversationID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStreamLiveUpdates_WithHistory(t *testing.T) {
	// Mock conversation response
	rawMessages := json.RawMessage(`[{"role": "user", "content": "test"}]`)
	toolResults := make(map[string]tools.StructuredToolResult)

	service := &mockConversationService{
		conversation: &GetConversationResponse{
			ID:          "test-conv-123",
			Provider:    "test-provider",
			RawMessages: rawMessages,
			ToolResults: toolResults,
			UpdatedAt:   time.Now(),
		},
	}

	streamer := NewConversationStreamer(service)
	streamer.RegisterMessageParser("test-provider", func(_ json.RawMessage, _ map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
		return []StreamableMessage{
			{Kind: "text", Role: "user", Content: "Hello"},
			{Kind: "text", Role: "assistant", Content: "Hi there"},
		}, nil
	})

	// Test with history included - should timeout quickly for testing
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	streamOpts := StreamOpts{
		Interval:       50 * time.Millisecond,
		IncludeHistory: true,
	}
	err := streamer.StreamLiveUpdates(ctx, "test-conv-123", streamOpts)

	// Should timeout after processing initial messages
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestStreamLiveUpdates_ErrorHandling(t *testing.T) {
	tests := []struct {
		name          string
		service       *mockConversationService
		setupStreamer func(*ConversationStreamer)
		expectedError string
	}{
		{
			name: "conversation not found",
			service: &mockConversationService{
				err: fmt.Errorf("conversation not found"),
			},
			setupStreamer: func(_ *ConversationStreamer) {},
			expectedError: "failed to get conversation",
		},
		{
			name: "no parser registered",
			service: &mockConversationService{
				conversation: &GetConversationResponse{
					ID:        "test-conv",
					Provider:  "unknown-provider",
					UpdatedAt: time.Now(),
				},
			},
			setupStreamer: func(_ *ConversationStreamer) {},
			expectedError: "no message parser registered for provider: unknown-provider",
		},
		{
			name: "parser error",
			service: &mockConversationService{
				conversation: &GetConversationResponse{
					ID:        "test-conv",
					Provider:  "error-provider",
					UpdatedAt: time.Now(),
				},
			},
			setupStreamer: func(s *ConversationStreamer) {
				s.RegisterMessageParser("error-provider", func(_ json.RawMessage, _ map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
					return nil, fmt.Errorf("parser error")
				})
			},
			expectedError: "failed to parse messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streamer := NewConversationStreamer(tt.service)
			tt.setupStreamer(streamer)

			ctx := context.Background()
			streamOpts := StreamOpts{
				Interval:       50 * time.Millisecond,
				IncludeHistory: false,
			}
			err := streamer.StreamLiveUpdates(ctx, "test-conv", streamOpts)

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestStreamNewMessagesSince(t *testing.T) {
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

	streamer.RegisterMessageParser("test-provider", func(_ json.RawMessage, _ map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
		return allMessages, nil
	})

	ctx := context.Background()

	// Test streaming from message 0 (should get all 5)
	count, err := streamer.streamNewMessagesSince(ctx, service.conversation, 0, "test-conv")
	assert.NoError(t, err)
	assert.Equal(t, 5, count)

	// Test streaming from message 3 (should get 2 new messages)
	count, err = streamer.streamNewMessagesSince(ctx, service.conversation, 3, "test-conv")
	assert.NoError(t, err)
	assert.Equal(t, 2, count)

	// Test streaming from message 5 (should get 0 new messages)
	count, err = streamer.streamNewMessagesSince(ctx, service.conversation, 5, "test-conv")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)

	// Test streaming from message 10 (beyond available, should get 0)
	count, err = streamer.streamNewMessagesSince(ctx, service.conversation, 10, "test-conv")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestStreamEntry_JSONOutput(t *testing.T) {
	tests := []struct {
		name     string
		entry    StreamEntry
		contains []string
		omits    []string
	}{
		{
			name: "text entry",
			entry: StreamEntry{
				Kind:           "text",
				Role:           "user",
				Content:        "Hello world",
				ConversationID: "test-conv-json",
			},
			contains: []string{`"kind":"text"`, `"role":"user"`, `"content":"Hello world"`, `"conversation_id":"test-conv-json"`},
			omits:    []string{"tool_name", "input", "result", "tool_call_id"},
		},
		{
			name: "tool-use entry",
			entry: StreamEntry{
				Kind:           "tool-use",
				Role:           "assistant",
				ToolName:       "bash",
				Input:          `{"command": "ls"}`,
				ToolCallID:     "call-123",
				ConversationID: "test-conv-tool",
			},
			contains: []string{`"kind":"tool-use"`, `"tool_name":"bash"`, `"input":"{\"command\": \"ls\"}"`, `"tool_call_id":"call-123"`},
			omits:    []string{"content", "result"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBytes, err := json.Marshal(tt.entry)
			require.NoError(t, err)

			jsonStr := string(jsonBytes)
			for _, contain := range tt.contains {
				assert.Contains(t, jsonStr, contain)
			}
			for _, omit := range tt.omits {
				assert.NotContains(t, jsonStr, omit)
			}
		})
	}
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
		_ = streamer.convertToStreamEntry(msg, "test-conv-benchmark")
	}
}

func TestStreamLiveUpdates_NewConversation(t *testing.T) {
	service := &mockConversationService{
		err: fmt.Errorf("should not be called for new conversation"),
	}

	streamer := NewConversationStreamer(service)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	streamOpts := StreamOpts{
		Interval:       25 * time.Millisecond,
		IncludeHistory: false,
		New:            true,
	}

	err := streamer.StreamLiveUpdates(ctx, "new-conversation-id", streamOpts)

	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}
