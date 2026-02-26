// Package webui provides a web server and HTTP API for kodelet's web interface.
// It serves the embedded React frontend and provides REST endpoints for
// conversation management and LLM interactions through a browser interface.
package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	openairesponses "github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

//go:generate bash -c "cd frontend && npm install && npm run build"
//go:embed dist/*
var embedFS embed.FS

// Server represents the web UI server
type Server struct {
	router              *mux.Router
	conversationService conversations.ConversationServiceInterface
	config              *ServerConfig
	server              *http.Server
	staticFS            fs.FS
}

// ServerConfig holds the configuration for the web server
type ServerConfig struct {
	Host string
	Port int
}

// Validate validates the server configuration
func (c *ServerConfig) Validate() error {
	// Validate host
	if c.Host == "" {
		return errors.New("host cannot be empty")
	}

	// Validate port
	if c.Port < 1 || c.Port > 65535 {
		return errors.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}

	return nil
}

// NewServer creates a new web UI server
func NewServer(ctx context.Context, config *ServerConfig) (*Server, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid server configuration")
	}

	// Get the conversation service
	conversationService, err := conversations.GetDefaultConversationService(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create conversation service")
	}

	// Create a sub-filesystem for static files from dist/assets
	staticFS, err := fs.Sub(embedFS, "dist/assets")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create static filesystem")
	}

	s := &Server{
		router:              mux.NewRouter(),
		conversationService: conversationService,
		config:              config,
		staticFS:            staticFS,
	}

	// Setup routes
	s.setupRoutes()

	return s, nil
}

// setupRoutes configures all the HTTP routes
func (s *Server) setupRoutes() {
	// API routes
	api := s.router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/conversations", s.handleListConversations).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleGetConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/tools/{toolCallId}", s.handleGetToolResult).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleDeleteConversation).Methods("DELETE")

	// Static assets from the React build
	s.router.PathPrefix("/assets/").Handler(s.staticFileHandler())

	// All other routes serve the React SPA
	s.router.PathPrefix("/").HandlerFunc(s.handleReactSPA)

	// Add middleware
	s.router.Use(s.loggingMiddleware)
	s.router.Use(s.corsMiddleware)
}

// staticFileHandler serves static files from the embedded filesystem
func (s *Server) staticFileHandler() http.Handler {
	return http.StripPrefix("/assets/", http.FileServer(http.FS(s.staticFS)))
}

// handleReactSPA serves the React single-page application
func (s *Server) handleReactSPA(w http.ResponseWriter, r *http.Request) {
	// Serve index.html for all non-API routes
	indexContent, err := embedFS.ReadFile("dist/index.html")
	if err != nil {
		logger.G(r.Context()).WithError(err).Error("failed to read index.html")
		http.Error(w, "failed to load application", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(indexContent)
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a custom response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: 200}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		logger.G(r.Context()).WithFields(map[string]any{
			"method":      r.Method,
			"path":        r.URL.Path,
			"status":      rw.statusCode,
			"duration":    duration,
			"remote_addr": r.RemoteAddr,
		}).Info("HTTP request")
	})
}

// corsMiddleware adds CORS headers
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only allow localhost for security
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// API Handlers

// handleListConversations handles GET /api/conversations
func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse query parameters
	query := r.URL.Query()
	req := &conversations.ListConversationsRequest{
		SearchTerm: query.Get("search"),
		SortBy:     query.Get("sortBy"),
		SortOrder:  query.Get("sortOrder"),
	}

	// Parse limit
	if limitStr := query.Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			req.Limit = limit
		}
	}

	// Parse offset
	if offsetStr := query.Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil {
			req.Offset = offset
		}
	}

	// Parse date filters
	if startStr := query.Get("startDate"); startStr != "" {
		if start, err := time.Parse("2006-01-02", startStr); err == nil {
			req.StartDate = &start
		}
	}

	if endStr := query.Get("endDate"); endStr != "" {
		if end, err := time.Parse("2006-01-02", endStr); err == nil {
			req.EndDate = &end
		}
	}

	// Get conversations
	response, err := s.conversationService.ListConversations(ctx, req)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to list conversations", err)
		return
	}

	for i := range response.Conversations {
		summary := &response.Conversations[i]
		platform, apiMode := extractProviderMetadata(summary.Provider, summary.Metadata)
		summary.Provider = displayProviderName(summary.Provider)
		if summary.Metadata == nil {
			summary.Metadata = make(map[string]any)
		}
		if platform != "" {
			summary.Metadata["platform"] = platform
		}
		if apiMode != "" {
			summary.Metadata["api_mode"] = apiMode
		}
	}

	s.writeJSONResponse(w, response)
}

// WebConversationResponse represents a conversation response for the web UI
type WebConversationResponse struct {
	ID           string       `json:"id"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
	Provider     string       `json:"provider"`
	Summary      string       `json:"summary,omitempty"`
	Usage        any          `json:"usage"`
	Messages     []WebMessage `json:"messages"`
	ToolResults  any          `json:"toolResults,omitempty"`
	MessageCount int          `json:"messageCount"`
}

// WebMessage represents a message with structured tool calls for the web UI
type WebMessage struct {
	Role         string        `json:"role"`
	Content      string        `json:"content"`
	ToolCalls    []WebToolCall `json:"toolCalls,omitempty"`
	ThinkingText string        `json:"thinkingText,omitempty"`
}

// WebToolCall represents a tool call for the web UI
type WebToolCall struct {
	ID       string              `json:"id"`
	Function WebToolCallFunction `json:"function"`
}

// WebToolCallFunction represents the function part of a tool call
type WebToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func normalizeProviderMetadataString(value any) string {
	strValue, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(strValue))
}

func extractProviderMetadata(provider string, metadata map[string]any) (string, string) {
	normalizedProvider := strings.TrimSpace(strings.ToLower(provider))

	platform := ""
	apiMode := ""
	if metadata != nil {
		if platformValue, exists := metadata["platform"]; exists {
			platform = normalizeProviderMetadataString(platformValue)
		}
		if modeValue, exists := metadata["api_mode"]; exists {
			apiMode = normalizeProviderMetadataString(modeValue)
		}
	}

	switch apiMode {
	case "responses_api", "response":
		apiMode = "responses"
	case "chat", "chatcompletions":
		apiMode = "chat_completions"
	}

	if normalizedProvider == "openai-responses" && apiMode == "" {
		apiMode = "responses"
	}

	return platform, apiMode
}

func displayProviderName(provider string) string {
	switch strings.TrimSpace(strings.ToLower(provider)) {
	case "anthropic":
		return "Anthropic"
	case "openai", "openai-responses":
		return "OpenAI"
	case "google":
		return "Google"
	default:
		return provider
	}
}

// handleGetConversation handles GET /api/conversations/{id}
func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]

	// Get conversation
	response, err := s.conversationService.GetConversation(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to get conversation", err)
		return
	}

	_, apiMode := extractProviderMetadata(response.Provider, response.Metadata)
	providerLabel := displayProviderName(response.Provider)

	providerForRender := response.Provider
	if providerForRender == "openai" && apiMode == "responses" {
		providerForRender = "openai-responses"
	}

	// Convert to web messages with tool call structure preserved
	webMessages, err := s.convertToWebMessages(response.RawMessages, providerForRender, response.ToolResults)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to parse conversation messages", err)
		return
	}

	// Convert to web response format

	webResponse := &WebConversationResponse{
		ID:           response.ID,
		CreatedAt:    response.CreatedAt,
		UpdatedAt:    response.UpdatedAt,
		Provider:     providerLabel,
		Summary:      response.Summary,
		Usage:        response.Usage,
		Messages:     webMessages,
		ToolResults:  response.ToolResults,
		MessageCount: len(webMessages),
	}

	s.writeJSONResponse(w, webResponse)
}

// convertToWebMessages converts raw messages to web messages with tool call structure
func (s *Server) convertToWebMessages(rawMessages json.RawMessage, provider string, toolResults map[string]tooltypes.StructuredToolResult) ([]WebMessage, error) {
	if provider == "openai-responses" {
		return s.convertOpenAIResponsesToWebMessages(rawMessages, toolResults)
	}

	var messages []WebMessage

	// Parse the raw JSON messages
	var rawMsgs []json.RawMessage
	if err := json.Unmarshal(rawMessages, &rawMsgs); err != nil {
		return nil, errors.Wrap(err, "failed to parse raw messages")
	}

	for _, rawMsg := range rawMsgs {
		var baseMsg map[string]any
		if err := json.Unmarshal(rawMsg, &baseMsg); err != nil {
			continue
		}

		role, _ := baseMsg["role"].(string)

		webMsg := WebMessage{
			Role:      role,
			Content:   "",
			ToolCalls: []WebToolCall{},
		}

		// Extract tool calls and thinking content based on provider
		switch provider {
		case "anthropic":
			// For Anthropic, we need to use the full raw message to properly deserialize
			if toolCalls, err := s.extractAnthropicToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract thinking content using SDK
			if textContent, thinkingText, err := s.extractAnthropicContent(rawMsg); err == nil {
				webMsg.Content = textContent
				webMsg.ThinkingText = thinkingText
			}
		case "openai":
			if toolCalls, err := s.extractOpenAIToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract content using SDK for consistency
			if textContent, err := s.extractOpenAIContent(rawMsg); err == nil {
				webMsg.Content = textContent
			}
		case "google":
			if toolCalls, err := s.extractGoogleToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract content and thinking text using Google SDK
			if textContent, thinkingText, err := s.extractGoogleContent(rawMsg); err == nil {
				webMsg.Content = textContent
				webMsg.ThinkingText = thinkingText
			}
		}

		// Skip empty messages (no content, no tool calls, and no thinking text)
		// pretty much neglecting the user tool call feedback as it is covered by the toolresult block at
		if webMsg.Content == "" && len(webMsg.ToolCalls) == 0 && webMsg.ThinkingText == "" {
			continue
		}

		messages = append(messages, webMsg)
	}

	return messages, nil
}

// convertOpenAIResponsesToWebMessages converts OpenAI Responses API stored items into web messages.
func (s *Server) convertOpenAIResponsesToWebMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]WebMessage, error) {
	streamableMessages, err := openairesponses.StreamMessages(rawMessages, toolResults)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse OpenAI Responses messages")
	}

	messages := make([]WebMessage, 0, len(streamableMessages))

	for _, msg := range streamableMessages {
		webMsg := WebMessage{
			Role:      msg.Role,
			Content:   "",
			ToolCalls: []WebToolCall{},
		}

		switch msg.Kind {
		case "text":
			webMsg.Content = msg.Content
		case "thinking":
			webMsg.ThinkingText = msg.Content
			if webMsg.Role == "" {
				webMsg.Role = "assistant"
			}
		case "tool-use":
			if webMsg.Role == "" {
				webMsg.Role = "assistant"
			}

			arguments := msg.Input
			if arguments == "" {
				arguments = "{}"
			}

			webMsg.ToolCalls = append(webMsg.ToolCalls, WebToolCall{
				ID: msg.ToolCallID,
				Function: WebToolCallFunction{
					Name:      msg.ToolName,
					Arguments: arguments,
				},
			})
		case "tool-result":
			// Tool results are rendered separately from ToolResults map.
			continue
		default:
			continue
		}

		if webMsg.Content == "" && len(webMsg.ToolCalls) == 0 && webMsg.ThinkingText == "" {
			continue
		}

		messages = append(messages, webMsg)
	}

	return messages, nil
}

// extractAnthropicContent extracts both text content and thinking blocks using Anthropic SDK
func (s *Server) extractAnthropicContent(rawMessage json.RawMessage) (string, string, error) {
	// Deserialize single message using the Anthropic SDK
	var anthropicMessage anthropic.MessageParam
	if err := json.Unmarshal(rawMessage, &anthropicMessage); err != nil {
		return "", "", errors.Wrap(err, "failed to deserialize Anthropic message")
	}

	var textParts []string
	var thinkingText string

	for _, contentBlock := range anthropicMessage.Content {
		// Handle text blocks
		if textBlock := contentBlock.OfText; textBlock != nil {
			textParts = append(textParts, textBlock.Text)
		}
		// Handle thinking blocks
		if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil {
			thinkingText = thinkingBlock.Thinking
		}
	}

	return strings.Join(textParts, "\n"), thinkingText, nil
}

// extractAnthropicToolCalls extracts tool calls from Anthropic content using SDK
func (s *Server) extractAnthropicToolCalls(rawMessage json.RawMessage) ([]WebToolCall, error) {
	// Deserialize single message using the Anthropic SDK
	var anthropicMessage anthropic.MessageParam
	if err := json.Unmarshal(rawMessage, &anthropicMessage); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize Anthropic message")
	}

	var toolCalls []WebToolCall

	for _, contentBlock := range anthropicMessage.Content {
		// Handle tool use blocks using SDK accessors
		if toolUseBlock := contentBlock.OfToolUse; toolUseBlock != nil {
			// Convert input to JSON string using SDK field
			inputJSON := "{}"
			if toolUseBlock.Input != nil {
				if inputBytes, err := json.Marshal(toolUseBlock.Input); err == nil {
					inputJSON = string(inputBytes)
				}
			}

			toolCalls = append(toolCalls, WebToolCall{
				ID: toolUseBlock.ID,
				Function: WebToolCallFunction{
					Name:      toolUseBlock.Name,
					Arguments: inputJSON,
				},
			})
		}
	}

	return toolCalls, nil
}

// extractOpenAIToolCalls extracts tool calls from OpenAI messages using SDK
func (s *Server) extractOpenAIToolCalls(rawMessage json.RawMessage) ([]WebToolCall, error) {
	// Deserialize single message using the OpenAI SDK
	var openaiMessage openai.ChatCompletionMessage
	if err := json.Unmarshal(rawMessage, &openaiMessage); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize OpenAI message")
	}

	var toolCalls []WebToolCall

	// Use SDK ToolCalls field directly
	for _, toolCall := range openaiMessage.ToolCalls {
		toolCalls = append(toolCalls, WebToolCall{
			ID: toolCall.ID,
			Function: WebToolCallFunction{
				Name:      toolCall.Function.Name,
				Arguments: toolCall.Function.Arguments,
			},
		})
	}

	return toolCalls, nil
}

// extractOpenAIContent extracts content from OpenAI messages using SDK
func (s *Server) extractOpenAIContent(rawMessage json.RawMessage) (string, error) {
	// Deserialize single message using the OpenAI SDK
	var openaiMessage openai.ChatCompletionMessage
	if err := json.Unmarshal(rawMessage, &openaiMessage); err != nil {
		return "", errors.Wrap(err, "failed to deserialize OpenAI message")
	}

	// OpenAI messages have simple string content or multimodal content
	if openaiMessage.Content != "" {
		return openaiMessage.Content, nil
	}

	// Handle multimodal content if present
	var textParts []string
	for _, part := range openaiMessage.MultiContent {
		if part.Type == openai.ChatMessagePartTypeText {
			textParts = append(textParts, part.Text)
		}
		// Note: Image parts would need special handling for display
	}

	return strings.Join(textParts, "\n"), nil
}

// handleGetToolResult handles GET /api/conversations/{id}/tools/{toolCallId}
func (s *Server) handleGetToolResult(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]
	toolCallID := vars["toolCallId"]

	// Get tool result
	response, err := s.conversationService.GetToolResult(ctx, id, toolCallID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusNotFound, "tool result not found", err)
		return
	}

	s.writeJSONResponse(w, response)
}

// handleDeleteConversation handles DELETE /api/conversations/{id}
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]

	// Delete conversation
	err := s.conversationService.DeleteConversation(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to delete conversation", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Utility methods

// writeJSONResponse writes a JSON response
func (s *Server) writeJSONResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.G(context.TODO()).WithError(err).Error("failed to encode JSON response")
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// writeErrorResponse writes an error response
func (s *Server) writeErrorResponse(w http.ResponseWriter, statusCode int, message string, err error) {
	if err != nil {
		logger.G(context.TODO()).WithError(err).Error(message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := map[string]any{
		"error":   message,
		"status":  statusCode,
		"success": false,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.G(context.TODO()).WithError(err).Error("failed to encode error response")
	}
}

// Start starts the web server
func (s *Server) Start(ctx context.Context) error {
	address := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)

	s.server = &http.Server{
		Addr:    address,
		Handler: s.router,
	}

	presenter.Info(fmt.Sprintf("Starting web server on http://%s", address))

	// Start server in a goroutine
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.G(ctx).WithError(err).Error("Web server error")
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Shutdown server gracefully
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.server.Shutdown(shutdownCtx)
}

// Stop stops the web server
func (s *Server) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// extractGoogleContent extracts text content and thinking text from Google Content using Google SDK
func (s *Server) extractGoogleContent(rawMessage json.RawMessage) (string, string, error) {
	// Deserialize single message using the Google GenAI SDK
	var googleContent genai.Content
	if err := json.Unmarshal(rawMessage, &googleContent); err != nil {
		return "", "", errors.Wrap(err, "failed to deserialize Google message")
	}

	var textParts []string
	var thinkingText string

	for _, part := range googleContent.Parts {
		// Handle text parts
		if part.Text != "" {
			if part.Thought {
				// This is thinking content
				thinkingText = part.Text
			} else {
				// Regular text content
				textParts = append(textParts, part.Text)
			}
		}
	}

	return strings.Join(textParts, "\n"), thinkingText, nil
}

// extractGoogleToolCalls extracts tool calls from Google Content using Google SDK
func (s *Server) extractGoogleToolCalls(rawMessage json.RawMessage) ([]WebToolCall, error) {
	// Deserialize single message using the Google GenAI SDK
	var googleContent genai.Content
	if err := json.Unmarshal(rawMessage, &googleContent); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize Google message")
	}

	var toolCalls []WebToolCall

	for _, part := range googleContent.Parts {
		// Handle function call parts
		if part.FunctionCall != nil {
			// Convert arguments to JSON string
			inputJSON := "{}"
			if part.FunctionCall.Args != nil {
				if inputBytes, err := json.Marshal(part.FunctionCall.Args); err == nil {
					inputJSON = string(inputBytes)
				}
			}

			toolCalls = append(toolCalls, WebToolCall{
				ID: fmt.Sprintf("google_%s_%d", part.FunctionCall.Name, len(toolCalls)), // Generate ID since Google doesn't provide one
				Function: WebToolCallFunction{
					Name:      part.FunctionCall.Name,
					Arguments: inputJSON,
				},
			})
		}
	}

	return toolCalls, nil
}

// Close closes the server and releases resources
func (s *Server) Close() error {
	if s.conversationService != nil {
		if err := s.conversationService.Close(); err != nil {
			return errors.Wrap(err, "failed to close conversation service")
		}
	}
	return s.Stop()
}
