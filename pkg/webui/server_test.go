package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConversationService implements the methods we need for testing
type mockConversationService struct {
	listFunc    func(ctx context.Context, req *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error)
	getFunc     func(ctx context.Context, id string) (*conversations.GetConversationResponse, error)
	deleteFunc  func(ctx context.Context, id string) error
	getToolFunc func(ctx context.Context, conversationID, toolCallID string) (*conversations.GetToolResultResponse, error)
	closeFunc   func() error
}

func (m *mockConversationService) ListConversations(ctx context.Context, req *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, req)
	}
	return &conversations.ListConversationsResponse{}, nil
}

func (m *mockConversationService) GetConversation(ctx context.Context, id string) (*conversations.GetConversationResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, id)
	}
	return &conversations.GetConversationResponse{}, nil
}

func (m *mockConversationService) DeleteConversation(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func (m *mockConversationService) GetToolResult(ctx context.Context, conversationID, toolCallID string) (*conversations.GetToolResultResponse, error) {
	if m.getToolFunc != nil {
		return m.getToolFunc(ctx, conversationID, toolCallID)
	}
	return &conversations.GetToolResultResponse{}, nil
}

func (m *mockConversationService) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestServerConfig_Validate(t *testing.T) {
	tests := []struct {
		name          string
		config        *ServerConfig
		expectedError string
	}{
		{
			name: "valid config",
			config: &ServerConfig{
				Host: "localhost",
				Port: 8080,
			},
		},
		{
			name: "empty host",
			config: &ServerConfig{
				Host: "",
				Port: 8080,
			},
			expectedError: "host cannot be empty",
		},
		{
			name: "invalid port - too low",
			config: &ServerConfig{
				Host: "localhost",
				Port: 0,
			},
			expectedError: "port must be between 1 and 65535",
		},
		{
			name: "invalid port - too high",
			config: &ServerConfig{
				Host: "localhost",
				Port: 65536,
			},
			expectedError: "port must be between 1 and 65535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestServer_handleListConversations(t *testing.T) {
	mockService := &mockConversationService{
		listFunc: func(_ context.Context, _ *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
			return &conversations.ListConversationsResponse{
				Conversations: []convtypes.ConversationSummary{
					{ID: "1", Summary: "Test 1"},
					{ID: "2", Summary: "Test 2"},
				},
				Total: 2,
			}, nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("GET", "/api/conversations?limit=10", nil)
	w := httptest.NewRecorder()

	server.handleListConversations(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response conversations.ListConversationsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, 2, len(response.Conversations))
	assert.Equal(t, 2, response.Total)
}

func TestServer_handleGetConversation(t *testing.T) {
	conversationID := "test-id-123"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:          conversationID,
					Summary:     "Test conversation",
					Provider:    "anthropic",
					RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"hello"}]}]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, conversationID, response.ID)
	assert.Equal(t, "Test conversation", response.Summary)
	assert.Equal(t, 1, response.MessageCount)
}

func TestServer_handleDeleteConversation(t *testing.T) {
	conversationID := "test-id-123"
	deleteCalled := false

	mockService := &mockConversationService{
		deleteFunc: func(_ context.Context, id string) error {
			deleteCalled = true
			assert.Equal(t, conversationID, id)
			return nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("DELETE", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleDeleteConversation(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, deleteCalled)
}

func TestServer_handleGetToolResult(t *testing.T) {
	conversationID := "conv-123"
	toolCallID := "tool-456"

	mockService := &mockConversationService{
		getToolFunc: func(_ context.Context, convID, toolID string) (*conversations.GetToolResultResponse, error) {
			assert.Equal(t, conversationID, convID)
			assert.Equal(t, toolCallID, toolID)
			return &conversations.GetToolResultResponse{
				ToolCallID: toolCallID,
				Result: tools.StructuredToolResult{
					ToolName: "TestTool",
				},
			}, nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/conversations/%s/tools/%s", conversationID, toolCallID), nil)
	req = mux.SetURLVars(req, map[string]string{
		"id":         conversationID,
		"toolCallId": toolCallID,
	})
	w := httptest.NewRecorder()

	server.handleGetToolResult(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response conversations.GetToolResultResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, toolCallID, response.ToolCallID)
	assert.Equal(t, "TestTool", response.Result.ToolName)
}

func TestServer_convertToWebMessages(t *testing.T) {
	server := &Server{}

	tests := []struct {
		name          string
		rawMessages   json.RawMessage
		provider      string
		toolResults   map[string]tools.StructuredToolResult
		expectedMsgs  int
		checkToolCall bool
	}{
		{
			name:          "anthropic messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":[{"type":"text","text":"Let me help"},{"type":"tool_use","id":"tool-123","name":"TestTool","input":{"arg":"value"}}]}]`),
			provider:      "anthropic",
			expectedMsgs:  1,
			checkToolCall: true,
		},
		{
			name:          "openai messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":"Let me help","tool_calls":[{"id":"tool-123","function":{"name":"TestTool","arguments":"{\"arg\":\"value\"}"}}]}]`),
			provider:      "openai",
			expectedMsgs:  1,
			checkToolCall: true,
		},
		{
			name:         "simple text messages",
			rawMessages:  json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Hello"}]},{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]}]`),
			provider:     "anthropic",
			expectedMsgs: 2,
		},
		{
			name:         "empty messages should be filtered out",
			rawMessages:  json.RawMessage(`[{"role":"user","content":[]},{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]},{"role":"user","content":[]}]`),
			provider:     "anthropic",
			expectedMsgs: 1,
		},
		{
			name:         "empty messages with tool calls should be preserved",
			rawMessages:  json.RawMessage(`[{"role":"user","content":""},{"role":"assistant","content":"","tool_calls":[{"id":"tool-123","function":{"name":"TestTool","arguments":"{\"arg\":\"value\"}"}}]}]`),
			provider:     "openai",
			expectedMsgs: 1,
		},
		{
			name:          "openai responses messages with reasoning and tool calls",
			rawMessages:   json.RawMessage(`[{"type":"message","role":"user","content":"Hello"},{"type":"reasoning","role":"assistant","content":"Analyzing request"},{"type":"function_call","call_id":"tool-123","name":"TestTool","arguments":"{\"arg\":\"value\"}"},{"type":"function_call_output","call_id":"tool-123","output":"{\"ok\":true}"},{"type":"message","role":"assistant","content":"Done"}]`),
			provider:      "openai-responses",
			toolResults:   map[string]tools.StructuredToolResult{"tool-123": {ToolName: "TestTool"}},
			expectedMsgs:  4,
			checkToolCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := server.convertToWebMessages(tt.rawMessages, tt.provider, tt.toolResults)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedMsgs, len(messages))

			if tt.checkToolCall && len(messages) > 0 {
				foundToolCall := false
				for _, msg := range messages {
					if len(msg.ToolCalls) > 0 {
						assert.Equal(t, "TestTool", msg.ToolCalls[0].Function.Name)
						foundToolCall = true
						break
					}
				}
				assert.True(t, foundToolCall, "expected at least one message with tool calls")
			}
		})
	}
}

func TestServer_Close(t *testing.T) {
	closeCalled := false
	mockService := &mockConversationService{
		closeFunc: func() error {
			closeCalled = true
			return nil
		},
	}

	server := &Server{
		conversationService: mockService,
	}

	err := server.Close()
	assert.NoError(t, err)
	assert.True(t, closeCalled)
}
