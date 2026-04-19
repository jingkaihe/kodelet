package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/steer"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConversationService implements the methods we need for testing
type mockConversationService struct {
	listFunc    func(ctx context.Context, req *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error)
	getFunc     func(ctx context.Context, id string) (*conversations.GetConversationResponse, error)
	forkFunc    func(ctx context.Context, id string) (*conversations.GetConversationResponse, error)
	deleteFunc  func(ctx context.Context, id string) error
	getToolFunc func(ctx context.Context, conversationID, toolCallID string) (*conversations.GetToolResultResponse, error)
	closeFunc   func() error
}

type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	serverConn, clientConn := net.Pipe()
	reader := bufio.NewReader(serverConn)
	writer := bufio.NewWriter(serverConn)
	return clientConn, bufio.NewReadWriter(reader, writer), nil
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

func (m *mockConversationService) ForkConversation(ctx context.Context, id string) (*conversations.GetConversationResponse, error) {
	if m.forkFunc != nil {
		return m.forkFunc(ctx, id)
	}
	return &conversations.GetConversationResponse{}, nil
}

type mockChatRunner struct {
	runFunc func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error)
}

func (m *mockChatRunner) Run(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, req, sink)
	}
	return "", nil
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
		{
			name: "invalid compact ratio",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: -0.1,
			},
			expectedError: "compact-ratio must be between 0.0 and 1.0",
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

func TestServerConfig_Validate_RejectsInvalidCWD(t *testing.T) {
	config := &ServerConfig{
		Host: "localhost",
		Port: 8080,
		CWD:  filepath.Join(t.TempDir(), "missing"),
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cwd")
}

func TestServer_handleListConversations(t *testing.T) {
	mockService := &mockConversationService{
		listFunc: func(_ context.Context, _ *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
			return &conversations.ListConversationsResponse{
				Conversations: []convtypes.ConversationSummary{
					{
						ID:       "1",
						Summary:  "Test 1",
						Provider: "openai",
						Metadata: map[string]any{"platform": "fireworks", "api_mode": "chat_completions"},
					},
					{ID: "2", Summary: "Test 2", Provider: "anthropic"},
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
	assert.Equal(t, "OpenAI", response.Conversations[0].Provider)
	assert.Equal(t, "fireworks", response.Conversations[0].Metadata["platform"])
	assert.Equal(t, "chat_completions", response.Conversations[0].Metadata["api_mode"])
	assert.Equal(t, "Anthropic", response.Conversations[1].Provider)
}

func TestServer_handleGetConversation(t *testing.T) {
	conversationID := "test-id-123"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:          conversationID,
					CWD:         "/workspace/project",
					Summary:     "Test conversation",
					Provider:    "openai",
					Metadata:    map[string]any{"platform": "fireworks", "api_mode": "responses", "profile": "anthropic"},
					RawMessages: json.RawMessage(`[{"type":"message","role":"user","content":"hello"}]`),
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
	assert.Equal(t, "OpenAI", response.Provider)
	assert.Equal(t, "/workspace/project", response.CWD)
	assert.True(t, response.CWDLocked)
	assert.Equal(t, "anthropic", response.Profile)
	assert.True(t, response.ProfileLocked)
	assert.Equal(t, 1, response.MessageCount)
}

func TestServer_handleGetChatSettings_IncludesDefaultCWD(t *testing.T) {
	tmpDir := t.TempDir()
	server := &Server{
		config: &ServerConfig{CWD: tmpDir},
	}

	req := httptest.NewRequest("GET", "/api/chat/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetChatSettings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ChatSettingsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, tmpDir, response.DefaultCWD)
}

func TestServer_defaultCWD_ReturnsErrorForInvalidConfiguredCWD(t *testing.T) {
	server := &Server{
		config: &ServerConfig{CWD: filepath.Join(t.TempDir(), "missing")},
	}

	defaultCWD, err := server.defaultCWD()
	require.Error(t, err)
	assert.Empty(t, defaultCWD)
	assert.Contains(t, err.Error(), "cwd directory does not exist")
}

func TestServer_handleGetCWDHints(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(tmpDir, "kodelet"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(tmpDir, "koala"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("x"), 0o644))

	server := &Server{
		config: &ServerConfig{CWD: tmpDir},
	}

	req := httptest.NewRequest("GET", "/api/chat/cwd-suggestions?q=ko", nil)
	w := httptest.NewRecorder()

	server.handleGetCWDHints(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response CWDHintsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Hints, 2)
	assert.Equal(t, filepath.Join(tmpDir, "koala"), response.Hints[0].Path)
	assert.Equal(t, filepath.Join(tmpDir, "kodelet"), response.Hints[1].Path)
}

func TestServer_handleGetCWDHints_NaturalSiblingQuery(t *testing.T) {
	parentDir := t.TempDir()
	defaultDir := filepath.Join(parentDir, "workspace")
	require.NoError(t, os.Mkdir(defaultDir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(parentDir, "kodelet"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(parentDir, "kodelet-website"), 0o755))

	server := &Server{
		config: &ServerConfig{CWD: defaultDir},
	}

	req := httptest.NewRequest("GET", "/api/chat/cwd-suggestions?q=kodelet", nil)
	w := httptest.NewRecorder()

	server.handleGetCWDHints(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response CWDHintsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Hints, 2)
	assert.Equal(t, filepath.Join(parentDir, "kodelet"), response.Hints[0].Path)
	assert.Equal(t, filepath.Join(parentDir, "kodelet-website"), response.Hints[1].Path)
}

func TestServer_resolveRequestedCWD(t *testing.T) {
	tmpDir := t.TempDir()
	server := &Server{config: &ServerConfig{CWD: tmpDir}}

	resolved, err := server.resolveRequestedCWD("")
	require.NoError(t, err)
	assert.Equal(t, tmpDir, resolved)

	childDir := filepath.Join(tmpDir, "child")
	require.NoError(t, os.Mkdir(childDir, 0o755))

	resolved, err = server.resolveRequestedCWD("child")
	require.NoError(t, err)
	assert.Equal(t, childDir, resolved)
}

func TestServer_handleGetGitDiff(t *testing.T) {
	repoDir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
	}

	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("old\n"), 0o644))
	runGit("add", "file.txt")
	runGit("commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("new\n"), 0o644))

	server := &Server{config: &ServerConfig{CWD: repoDir}}
	req := httptest.NewRequest("GET", "/api/git/diff", nil)
	w := httptest.NewRecorder()

	server.handleGetGitDiff(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response gitDiffResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.HasDiff)
	assert.Equal(t, repoDir, response.CWD)
	assert.Equal(t, repoDir, response.GitRoot)
	assert.Contains(t, response.Diff, "diff --git a/file.txt b/file.txt")
	assert.Contains(t, response.Diff, "-old")
	assert.Contains(t, response.Diff, "+new")
	assert.Equal(t, 0, response.ExitCode)
}

func TestServer_handleGetConversationOpenAIChatCompletionsSkipsSystemAndPreservesThinking(t *testing.T) {
	conversationID := "test-openai-chat-completions"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:       conversationID,
					Provider: "openai",
					Metadata: map[string]any{"api_mode": "chat_completions"},
					RawMessages: json.RawMessage(`[
						{"role":"system","content":"SECRET SYSTEM PROMPT"},
						{"role":"user","content":"hello"},
						{"role":"assistant","content":"Hi there!","reasoning_content":"\ninternal reasoning"}
					]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{conversationService: mockService, router: mux.NewRouter()}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Messages, 2)

	assert.Equal(t, "user", response.Messages[0].Role)
	assert.Equal(t, "hello", response.Messages[0].Content)
	assert.Equal(t, "assistant", response.Messages[1].Role)
	assert.Equal(t, "Hi there!", response.Messages[1].Content)
	assert.Equal(t, "internal reasoning", response.Messages[1].ThinkingText)
}

func TestServer_handleGetChatSettings(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("profile", "work")

	server := &Server{router: mux.NewRouter()}
	req := httptest.NewRequest("GET", "/api/chat/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetChatSettings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ChatSettingsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "work", response.CurrentProfile)
	require.NotEmpty(t, response.Profiles)
	assert.Equal(t, "default", response.Profiles[0].Name)
}

func TestServer_handleGetConversationPreservesImageContent(t *testing.T) {
	conversationID := "test-image-conv"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:       conversationID,
					Provider: "openai",
					RawMessages: json.RawMessage(`[
						{
							"role":"user",
							"content":[
								{"type":"text","text":"what is in the image?"},
								{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}
							]
						}
					]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{conversationService: mockService, router: mux.NewRouter()}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)

	contentBytes, err := json.Marshal(response.Messages[0].Content)
	require.NoError(t, err)
	assert.Contains(t, string(contentBytes), `"type":"image"`)
	assert.Contains(t, string(contentBytes), `"media_type":"image/png"`)
	assert.Contains(t, string(contentBytes), `"data":"aGVsbG8="`)
}

func TestServer_handleGetConversationLegacyOpenAIResponses(t *testing.T) {
	conversationID := "test-id-legacy"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:          conversationID,
					Summary:     "Legacy Test conversation",
					Provider:    "openai-responses",
					Metadata:    map[string]any{},
					RawMessages: json.RawMessage(`[{"type":"message","role":"user","content":"hello"}]`),
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
	assert.Equal(t, "OpenAI", response.Provider)
}

func TestServer_handleGetConversationOpenAIResponsesPreservesImageContent(t *testing.T) {
	conversationID := "test-openai-responses-image"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:       conversationID,
					Provider: "openai",
					Metadata: map[string]any{"api_mode": "responses"},
					RawMessages: json.RawMessage(`[
						{
							"type":"message",
							"role":"user",
							"content":"what is in the image?",
							"raw_item":{
								"role":"user",
								"content":[
									{"type":"input_image","image_url":"data:image/png;base64,aGVsbG8="},
									{"type":"input_text","text":"what is in the image?"}
								]
							}
						}
					]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{conversationService: mockService, router: mux.NewRouter()}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)

	contentBytes, err := json.Marshal(response.Messages[0].Content)
	require.NoError(t, err)
	assert.Contains(t, string(contentBytes), `"type":"image"`)
	assert.Contains(t, string(contentBytes), `"media_type":"image/png"`)
	assert.Contains(t, string(contentBytes), `"data":"aGVsbG8="`)
	assert.Contains(t, string(contentBytes), `"text":"what is in the image?"`)
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

func TestServer_handleForkConversation(t *testing.T) {
	conversationID := "test-id-123"
	mockService := &mockConversationService{
		forkFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			assert.Equal(t, conversationID, id)
			return &conversations.GetConversationResponse{ID: "forked-456"}, nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/conversations/"+conversationID+"/fork", nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleForkConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response forkConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.Equal(t, "forked-456", response.ConversationID)
}

func TestServer_handleChat(t *testing.T) {
	var capturedRequest ChatRequest
	requestCtx, requestCancel := context.WithCancel(context.Background())
	defer requestCancel()
	runnerStarted := make(chan struct{})
	allowFinish := make(chan struct{})

	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatRunner: &mockChatRunner{
			runFunc: func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
				capturedRequest = req
				err := sink.Send(ChatEvent{
					Kind:           "conversation",
					ConversationID: "conv-123",
					Role:           "assistant",
				})
				require.NoError(t, err)
				close(runnerStarted)
				<-allowFinish
				require.NoError(t, ctx.Err())
				return "conv-123", nil
			},
		},
		router: mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello"}`))
	req = req.WithContext(requestCtx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleChat(w, req)
		close(done)
	}()

	<-runnerStarted
	requestCancel()
	close(allowFinish)
	<-done

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-ndjson", w.Header().Get("Content-Type"))
	assert.Equal(t, "hello", capturedRequest.Message)
	assert.NotEmpty(t, capturedRequest.ConversationID)

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var firstEvent ChatEvent
	err := json.Unmarshal([]byte(lines[0]), &firstEvent)
	require.NoError(t, err)
	assert.Equal(t, "conversation", firstEvent.Kind)
	assert.Equal(t, "conv-123", firstEvent.ConversationID)

	var doneEvent ChatEvent
	err = json.Unmarshal([]byte(lines[1]), &doneEvent)
	require.NoError(t, err)
	assert.Equal(t, "done", doneEvent.Kind)
	assert.Equal(t, "conv-123", doneEvent.ConversationID)
}

func TestServer_handleChatWithImageContent(t *testing.T) {
	var capturedRequest ChatRequest

	server := &Server{
		conversationService: &mockConversationService{},
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
				capturedRequest = req
				err := sink.Send(ChatEvent{Kind: "conversation", ConversationID: "conv-img", Role: "assistant"})
				require.NoError(t, err)
				return "conv-img", nil
			},
		},
		router: mux.NewRouter(),
	}

	reqBody := `{"message":"describe this image","content":[{"type":"text","text":"describe this image"},{"type":"image","source":{"data":"aGVsbG8=","media_type":"image/png"}}]}`
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	server.handleChat(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, capturedRequest.Content, 2)
	assert.Equal(t, "image", capturedRequest.Content[1].Type)
	assert.Equal(t, "image/png", capturedRequest.Content[1].Source.MediaType)
}

func TestServer_handleChatWithProfile(t *testing.T) {
	var capturedRequest ChatRequest

	server := &Server{
		conversationService: &mockConversationService{},
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
				capturedRequest = req
				err := sink.Send(ChatEvent{Kind: "conversation", ConversationID: "conv-profile", Role: "assistant"})
				require.NoError(t, err)
				return "conv-profile", nil
			},
		},
		router: mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello","profile":"anthropic"}`))
	w := httptest.NewRecorder()

	server.handleChat(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "anthropic", capturedRequest.Profile)
}

func TestServer_handleChatRunnerError(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, _ ChatRequest, sink ChatEventSink) (string, error) {
				err := sink.Send(ChatEvent{
					Kind:           "conversation",
					ConversationID: "conv-err",
					Role:           "assistant",
				})
				require.NoError(t, err)
				return "conv-err", errors.New("boom")
			},
		},
		router: mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello"}`))
	w := httptest.NewRecorder()

	server.handleChat(w, req)

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var errorEvent ChatEvent
	err := json.Unmarshal([]byte(lines[1]), &errorEvent)
	require.NoError(t, err)
	assert.Equal(t, "error", errorEvent.Kind)
	assert.Equal(t, "conv-err", errorEvent.ConversationID)
	assert.Equal(t, "boom", errorEvent.Error)
}

func TestServer_handleChatRejectsConcurrentRunForConversation(t *testing.T) {
	runnerStarted := make(chan struct{})
	allowFinish := make(chan struct{})
	done := make(chan struct{})

	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
				close(runnerStarted)
				<-allowFinish
				return req.ConversationID, nil
			},
		},
		router: mux.NewRouter(),
	}

	firstReq := httptest.NewRequest(
		"POST",
		"/api/chat",
		strings.NewReader(`{"message":"hello","conversationId":"conv-123"}`),
	)
	firstW := httptest.NewRecorder()
	go func() {
		server.handleChat(firstW, firstReq)
		close(done)
	}()

	<-runnerStarted

	secondReq := httptest.NewRequest(
		"POST",
		"/api/chat",
		strings.NewReader(`{"message":"again","conversationId":"conv-123"}`),
	)
	secondW := httptest.NewRecorder()
	server.handleChat(secondW, secondReq)

	assert.Equal(t, http.StatusConflict, secondW.Code)
	assert.Contains(t, secondW.Body.String(), "conversation already has an active run")

	close(allowFinish)
	<-done
}

func TestServer_handleStopConversation(t *testing.T) {
	var cancelled atomic.Bool

	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
		activeChats:         make(map[string]*activeChatRun),
	}
	server.activeChats["conv-123"] = newActiveChatRun(func() {
		cancelled.Store(true)
	})

	req := httptest.NewRequest("POST", "/api/conversations/conv-123/stop", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleStopConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, cancelled.Load())

	var response stopConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.Equal(t, "conv-123", response.ConversationID)
	assert.True(t, response.Stopped)
	assert.False(t, server.isActiveChat("conv-123"))
	assert.True(t, server.activeChats["conv-123"].stopRequested)
}

func TestServer_handleStreamConversation(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
		activeChats:         map[string]*activeChatRun{"conv-123": newActiveChatRun(func() {})},
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
	}

	req := httptest.NewRequest("GET", "/api/conversations/conv-123/stream", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.handleStreamConversation(w, req)
		close(done)
	}()

	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 1
	}, time.Second, 10*time.Millisecond)

	server.broadcastChatEvent("conv-123", ChatEvent{Kind: "text-delta", ConversationID: "conv-123", Delta: "hi", Role: "assistant"})
	server.closeChatSubscribers("conv-123")
	<-done

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var firstEvent ChatEvent
	err := json.Unmarshal([]byte(lines[0]), &firstEvent)
	require.NoError(t, err)
	assert.Equal(t, "conversation", firstEvent.Kind)

	var secondEvent ChatEvent
	err = json.Unmarshal([]byte(lines[1]), &secondEvent)
	require.NoError(t, err)
	assert.Equal(t, "text-delta", secondEvent.Kind)
	assert.Equal(t, "hi", secondEvent.Delta)
}

func TestServer_handleStreamConversationForwardsUsageEvents(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
		activeChats:         map[string]*activeChatRun{"conv-123": newActiveChatRun(func() {})},
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
	}

	req := httptest.NewRequest("GET", "/api/conversations/conv-123/stream", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.handleStreamConversation(w, req)
		close(done)
	}()

	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 1
	}, time.Second, 10*time.Millisecond)

	server.broadcastChatEvent("conv-123", ChatEvent{
		Kind:           "usage",
		ConversationID: "conv-123",
		Role:           "assistant",
		Usage: &llmtypes.Usage{
			InputTokens:  120,
			OutputTokens: 45,
		},
	})
	server.closeChatSubscribers("conv-123")
	<-done

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var usageEvent ChatEvent
	err := json.Unmarshal([]byte(lines[1]), &usageEvent)
	require.NoError(t, err)
	assert.Equal(t, "usage", usageEvent.Kind)
	if assert.NotNil(t, usageEvent.Usage) {
		assert.Equal(t, 120, usageEvent.Usage.InputTokens)
		assert.Equal(t, 45, usageEvent.Usage.OutputTokens)
	}
}

func TestServer_handleDeleteConversationRejectsActiveRun(t *testing.T) {
	deleteCalled := false

	server := &Server{
		conversationService: &mockConversationService{
			deleteFunc: func(_ context.Context, _ string) error {
				deleteCalled = true
				return nil
			},
		},
		activeChats:     map[string]*activeChatRun{"conv-123": newActiveChatRun(func() {})},
		chatSubscribers: make(map[string]map[*subscriberEventSink]struct{}),
		router:          mux.NewRouter(),
	}

	deleteReq := httptest.NewRequest("DELETE", "/api/conversations/conv-123", nil)
	deleteReq = mux.SetURLVars(deleteReq, map[string]string{"id": "conv-123"})
	deleteW := httptest.NewRecorder()

	server.handleDeleteConversation(deleteW, deleteReq)

	assert.Equal(t, http.StatusConflict, deleteW.Code)
	assert.Contains(t, deleteW.Body.String(), "conversation is actively running")
	assert.False(t, deleteCalled)
}

func TestServer_handleChatThroughMiddleware(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, _ ChatRequest, sink ChatEventSink) (string, error) {
				err := sink.Send(ChatEvent{
					Kind:           "conversation",
					ConversationID: "conv-middleware",
					Role:           "assistant",
				})
				require.NoError(t, err)
				return "conv-middleware", nil
			},
		},
		router: mux.NewRouter(),
	}
	server.setupRoutes()

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello"}`))
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var firstEvent ChatEvent
	err := json.Unmarshal([]byte(lines[0]), &firstEvent)
	require.NoError(t, err)
	assert.Equal(t, "conversation", firstEvent.Kind)
	assert.Equal(t, "conv-middleware", firstEvent.ConversationID)
}

func TestResponseWriter_HijackDelegates(t *testing.T) {
	recorder := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	rw := &responseWriter{ResponseWriter: recorder, statusCode: http.StatusOK}

	conn, _, err := rw.Hijack()
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
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

func TestServer_handleSteerConversation(t *testing.T) {
	homeDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	require.NoError(t, os.Setenv("HOME", homeDir))
	defer func() {
		if originalHome == "" {
			os.Unsetenv("HOME")
			return
		}
		require.NoError(t, os.Setenv("HOME", originalHome))
	}()

	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/conversations/conv-123/steer", strings.NewReader(`{"message":"Please focus on error handling"}`))
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Success        bool   `json:"success"`
		ConversationID string `json:"conversation_id"`
		Queued         bool   `json:"queued"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.Equal(t, "conv-123", response.ConversationID)
	assert.False(t, response.Queued)

	steerStore, err := steer.NewSteerStore()
	require.NoError(t, err)
	pending, err := steerStore.ReadPendingSteer("conv-123")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "Please focus on error handling", pending[0].Content)
}

func TestServer_handleSteerConversationRequiresMessage(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/conversations/conv-123/steer", strings.NewReader(`{"message":"   "}`))
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "message cannot be empty")
}

func TestServer_handleSteerConversationRejectsMessagesThatAreTooLong(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	message := strings.Repeat("a", steer.MaxMessageLength+1)
	req := httptest.NewRequest(
		"POST",
		"/api/conversations/conv-123/steer",
		strings.NewReader(fmt.Sprintf(`{"message":"%s"}`, message)),
	)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "message must be 10000 characters or fewer")
}

func TestParseTerminalSignal(t *testing.T) {
	signal, ok := parseTerminalSignal("sigint")
	assert.True(t, ok)
	assert.Equal(t, syscall.SIGINT, signal)

	_, ok = parseTerminalSignal("nope")
	assert.False(t, ok)
}

func TestTerminalOriginAllowed(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://localhost:3000")
	assert.True(t, terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "example.com:8080"
	req.Header.Set("Origin", "http://evil.com")
	assert.False(t, terminalOriginAllowed(req))
}

func TestBoundedTerminalDimensions(t *testing.T) {
	assert.Equal(t, defaultTerminalRows, boundedTerminalRows(0))
	assert.Equal(t, maxTerminalRows, boundedTerminalRows(maxTerminalRows+50))
	assert.Equal(t, 40, boundedTerminalRows(40))

	assert.Equal(t, defaultTerminalCols, boundedTerminalCols(0))
	assert.Equal(t, maxTerminalCols, boundedTerminalCols(maxTerminalCols+50))
	assert.Equal(t, 80, boundedTerminalCols(80))
}

func TestDisplayProviderName(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		expected string
	}{
		{
			name:     "openai",
			provider: "openai",
			expected: "OpenAI",
		},
		{
			name:     "openai responses legacy",
			provider: "openai-responses",
			expected: "OpenAI",
		},
		{
			name:     "anthropic",
			provider: "anthropic",
			expected: "Anthropic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, displayProviderName(tt.provider))
		})
	}
}

func TestExtractProviderMetadata(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		metadata         map[string]any
		expectedPlatform string
		expectedAPIMode  string
	}{
		{
			name:             "openai metadata normalized",
			provider:         "openai",
			metadata:         map[string]any{"platform": "Fireworks", "api_mode": "responses"},
			expectedPlatform: "fireworks",
			expectedAPIMode:  "responses",
		},
		{
			name:             "legacy openai responses defaults to responses mode",
			provider:         "openai-responses",
			metadata:         map[string]any{},
			expectedPlatform: "",
			expectedAPIMode:  "responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, apiMode := extractProviderMetadata(tt.provider, tt.metadata)
			assert.Equal(t, tt.expectedPlatform, platform)
			assert.Equal(t, tt.expectedAPIMode, apiMode)
		})
	}
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
		expectedTool  string
	}{
		{
			name:          "anthropic messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":[{"type":"text","text":"Let me help"},{"type":"tool_use","id":"tool-123","name":"TestTool","input":{"arg":"value"}}]}]`),
			provider:      "anthropic",
			expectedMsgs:  1,
			checkToolCall: true,
			expectedTool:  "TestTool",
		},
		{
			name:          "openai messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":"Let me help","tool_calls":[{"id":"tool-123","function":{"name":"TestTool","arguments":"{\"arg\":\"value\"}"}}]}]`),
			provider:      "openai",
			expectedMsgs:  1,
			checkToolCall: true,
			expectedTool:  "TestTool",
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
			name:         "openai chat completions tool result messages are filtered out",
			rawMessages:  json.RawMessage(`[{"role":"assistant","content":"","tool_calls":[{"id":"tool-123","function":{"name":"bash","arguments":"{\"command\":\"nproc\"}"}}]},{"role":"tool","tool_call_id":"tool-123","content":"24\nMem: 31Gi"},{"role":"assistant","content":"This machine has 24 CPU cores."}]`),
			provider:     "openai",
			expectedMsgs: 2,
		},
		{
			name:          "openai responses messages with reasoning and tool calls",
			rawMessages:   json.RawMessage(`[{"type":"message","role":"user","content":"Hello"},{"type":"reasoning","role":"assistant","content":"Analyzing request"},{"type":"function_call","call_id":"tool-123","name":"TestTool","arguments":"{\"arg\":\"value\"}"},{"type":"function_call_output","call_id":"tool-123","output":"{\"ok\":true}"},{"type":"message","role":"assistant","content":"Done"}]`),
			provider:      "openai-responses",
			toolResults:   map[string]tools.StructuredToolResult{"tool-123": {ToolName: "TestTool"}},
			expectedMsgs:  4,
			checkToolCall: true,
			expectedTool:  "TestTool",
		},
		{
			name:          "openai responses native web search tool calls",
			rawMessages:   json.RawMessage(`[{"type":"message","role":"user","content":"Look up the latest notes"},{"type":"web_search_call","call_id":"search-123","status":"completed","action":"search","content":"kodelet web ui search"}]`),
			provider:      "openai-responses",
			toolResults:   map[string]tools.StructuredToolResult{"search-123": {ToolName: "openai_web_search"}},
			expectedMsgs:  2,
			checkToolCall: true,
			expectedTool:  "openai_web_search",
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
						assert.Equal(t, tt.expectedTool, msg.ToolCalls[0].Function.Name)
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
