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
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConversationService implements the methods we need for testing
type mockConversationService struct {
	listFunc    func(ctx context.Context, req *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error)
	getFunc     func(ctx context.Context, id string) (*conversations.GetConversationResponse, error)
	deleteFunc  func(ctx context.Context, id string) error
	resolveFunc func(ctx context.Context, id string) (string, error)
	getToolFunc func(ctx context.Context, conversationID, toolCallID string) (*conversations.GetToolResultResponse, error)
	searchFunc  func(ctx context.Context, query string, limit int) (*conversations.ListConversationsResponse, error)
	statsFunc   func(ctx context.Context) (*conversations.ConversationStatistics, error)
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

func (m *mockConversationService) ResolveConversationID(ctx context.Context, id string) (string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, id)
	}
	return id, nil
}

func (m *mockConversationService) GetToolResult(ctx context.Context, conversationID, toolCallID string) (*conversations.GetToolResultResponse, error) {
	if m.getToolFunc != nil {
		return m.getToolFunc(ctx, conversationID, toolCallID)
	}
	return &conversations.GetToolResultResponse{}, nil
}

func (m *mockConversationService) SearchConversations(ctx context.Context, query string, limit int) (*conversations.ListConversationsResponse, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, query, limit)
	}
	return &conversations.ListConversationsResponse{}, nil
}

func (m *mockConversationService) GetConversationStatistics(ctx context.Context) (*conversations.ConversationStatistics, error) {
	if m.statsFunc != nil {
		return m.statsFunc(ctx)
	}
	return &conversations.ConversationStatistics{}, nil
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
		listFunc: func(ctx context.Context, req *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
			return &conversations.ListConversationsResponse{
				Conversations: []conversations.ConversationSummary{
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
		resolveFunc: func(ctx context.Context, id string) (string, error) {
			if id == conversationID {
				return conversationID, nil
			}
			return "", fmt.Errorf("conversation not found")
		},
		getFunc: func(ctx context.Context, id string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{
				ID:          conversationID,
				Summary:     "Test conversation",
				ModelType:   "anthropic",
				RawMessages: json.RawMessage(`[{"role":"user","content":"hello"}]`),
			}, nil
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
		resolveFunc: func(ctx context.Context, id string) (string, error) {
			return conversationID, nil
		},
		deleteFunc: func(ctx context.Context, id string) error {
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

func TestServer_handleSearchConversations(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		expectedStatus int
		mockResponse   *conversations.ListConversationsResponse
		mockError      error
	}{
		{
			name:           "successful search",
			query:          "?q=test&limit=10",
			expectedStatus: http.StatusOK,
			mockResponse: &conversations.ListConversationsResponse{
				Conversations: []conversations.ConversationSummary{
					{ID: "1", Summary: "Test result"},
				},
				Total: 1,
			},
		},
		{
			name:           "missing search term",
			query:          "?limit=10",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "search error",
			query:          "?q=test",
			expectedStatus: http.StatusInternalServerError,
			mockError:      fmt.Errorf("search failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &mockConversationService{
				searchFunc: func(ctx context.Context, query string, limit int) (*conversations.ListConversationsResponse, error) {
					if tt.mockError != nil {
						return nil, tt.mockError
					}
					return tt.mockResponse, nil
				},
			}

			server := &Server{
				conversationService: mockService,
				router:              mux.NewRouter(),
			}

			req := httptest.NewRequest("GET", "/api/search"+tt.query, nil)
			w := httptest.NewRecorder()

			server.handleSearchConversations(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}



func TestServer_handleGetToolResult(t *testing.T) {
	conversationID := "conv-123"
	toolCallID := "tool-456"

	mockService := &mockConversationService{
		resolveFunc: func(ctx context.Context, id string) (string, error) {
			return conversationID, nil
		},
		getToolFunc: func(ctx context.Context, convID, toolID string) (*conversations.GetToolResultResponse, error) {
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
		modelType     string
		expectedMsgs  int
		checkToolCall bool
	}{
		{
			name:          "anthropic messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":[{"type":"text","text":"Let me help"},{"type":"tool_use","id":"tool-123","name":"TestTool","input":{"arg":"value"}}]}]`),
			modelType:     "anthropic",
			expectedMsgs:  1,
			checkToolCall: true,
		},
		{
			name:          "openai messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":"Let me help","tool_calls":[{"id":"tool-123","function":{"name":"TestTool","arguments":"{\"arg\":\"value\"}"}}]}]`),
			modelType:     "openai",
			expectedMsgs:  1,
			checkToolCall: true,
		},
		{
			name:         "simple text messages",
			rawMessages:  json.RawMessage(`[{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi there!"}]`),
			modelType:    "anthropic",
			expectedMsgs: 2,
		},
		{
			name:         "empty messages should be filtered out",
			rawMessages:  json.RawMessage(`[{"role":"user","content":""},{"role":"assistant","content":"Hi there!"},{"role":"user","content":""}]`),
			modelType:    "anthropic",
			expectedMsgs: 1,
		},
		{
			name:         "empty messages with tool calls should be preserved",
			rawMessages:  json.RawMessage(`[{"role":"user","content":""},{"role":"assistant","content":"","tool_calls":[{"id":"tool-123","function":{"name":"TestTool","arguments":"{\"arg\":\"value\"}"}}]}]`),
			modelType:    "openai",
			expectedMsgs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := server.convertToWebMessages(tt.rawMessages, tt.modelType)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedMsgs, len(messages))

			if tt.checkToolCall && len(messages) > 0 {
				assert.Greater(t, len(messages[0].ToolCalls), 0)
				assert.Equal(t, "TestTool", messages[0].ToolCalls[0].Function.Name)
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
