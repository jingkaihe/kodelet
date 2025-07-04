package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
)

//go:embed templates/* static/*
var embedFS embed.FS

// Server represents the web UI server
type Server struct {
	router              *mux.Router
	conversationService *conversations.ConversationService
	config              *ServerConfig
	server              *http.Server
}

// ServerConfig holds the configuration for the web server
type ServerConfig struct {
	Host string
	Port int
}

// NewServer creates a new web UI server
func NewServer(config *ServerConfig) (*Server, error) {
	// Get the conversation service
	conversationService, err := conversations.GetDefaultConversationService()
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation service: %w", err)
	}

	s := &Server{
		router:              mux.NewRouter(),
		conversationService: conversationService,
		config:              config,
	}

	// Setup routes
	s.setupRoutes()

	return s, nil
}

// setupRoutes configures all the HTTP routes
func (s *Server) setupRoutes() {
	// Static files
	s.router.PathPrefix("/static/").Handler(s.staticFileHandler())

	// API routes
	api := s.router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/conversations", s.handleListConversations).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleGetConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/tools/{toolCallId}", s.handleGetToolResult).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleDeleteConversation).Methods("DELETE")
	api.HandleFunc("/search", s.handleSearchConversations).Methods("GET")
	api.HandleFunc("/stats", s.handleGetStatistics).Methods("GET")

	// HTML routes
	s.router.HandleFunc("/", s.handleConversationList).Methods("GET")
	s.router.HandleFunc("/c/{id}", s.handleConversationView).Methods("GET")

	// Add middleware
	s.router.Use(s.loggingMiddleware)
	s.router.Use(s.corsMiddleware)
}

// staticFileHandler serves static files from the embedded filesystem
func (s *Server) staticFileHandler() http.Handler {
	// Create a sub-filesystem for static files
	staticFS, err := fs.Sub(embedFS, "static")
	if err != nil {
		panic(fmt.Sprintf("failed to create static filesystem: %v", err))
	}

	return http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a custom response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: 200}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		logger.G(r.Context()).WithFields(map[string]interface{}{
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
		s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to list conversations", err)
		return
	}

	s.writeJSONResponse(w, response)
}

// WebConversationResponse represents a conversation response for the web UI
type WebConversationResponse struct {
	ID           string       `json:"id"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
	ModelType    string       `json:"modelType"`
	Summary      string       `json:"summary,omitempty"`
	Usage        interface{}  `json:"usage"`
	Messages     []WebMessage `json:"messages"`
	ToolResults  interface{}  `json:"toolResults,omitempty"`
	MessageCount int          `json:"messageCount"`
}

// WebMessage represents a message with structured tool calls for the web UI
type WebMessage struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []WebToolCall `json:"toolCalls,omitempty"`
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

// handleGetConversation handles GET /api/conversations/{id}
func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]

	// Resolve conversation ID (supports short IDs)
	resolvedID, err := s.conversationService.ResolveConversationID(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusNotFound, "Conversation not found", err)
		return
	}

	// Get conversation
	response, err := s.conversationService.GetConversation(ctx, resolvedID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to get conversation", err)
		return
	}

	// Convert to web messages with tool call structure preserved
	webMessages, err := s.convertToWebMessages(response.RawMessages, response.ModelType)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to parse conversation messages", err)
		return
	}

	// Convert to web response format
	webResponse := &WebConversationResponse{
		ID:           response.ID,
		CreatedAt:    response.CreatedAt,
		UpdatedAt:    response.UpdatedAt,
		ModelType:    response.ModelType,
		Summary:      response.Summary,
		Usage:        response.Usage,
		Messages:     webMessages,
		ToolResults:  response.ToolResults,
		MessageCount: len(webMessages),
	}

	s.writeJSONResponse(w, webResponse)
}

// convertToWebMessages converts raw messages to web messages with tool call structure
func (s *Server) convertToWebMessages(rawMessages json.RawMessage, modelType string) ([]WebMessage, error) {
	var messages []WebMessage

	// Parse the raw JSON messages
	var rawMsgs []json.RawMessage
	if err := json.Unmarshal(rawMessages, &rawMsgs); err != nil {
		return nil, fmt.Errorf("failed to parse raw messages: %w", err)
	}

	for _, rawMsg := range rawMsgs {
		var baseMsg map[string]interface{}
		if err := json.Unmarshal(rawMsg, &baseMsg); err != nil {
			continue
		}

		role, _ := baseMsg["role"].(string)
		content := s.extractContentString(baseMsg["content"])

		webMsg := WebMessage{
			Role:      role,
			Content:   content,
			ToolCalls: []WebToolCall{},
		}

		// Extract tool calls based on provider
		if modelType == "anthropic" {
			webMsg.ToolCalls = s.extractAnthropicToolCalls(baseMsg["content"])
		} else if modelType == "openai" {
			webMsg.ToolCalls = s.extractOpenAIToolCalls(baseMsg)
		}

		messages = append(messages, webMsg)
	}

	return messages, nil
}

// extractContentString extracts string content from various content formats
func (s *Server) extractContentString(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		// Handle array of content blocks
		var textParts []string
		for _, block := range c {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockType, ok := blockMap["type"].(string); ok && blockType == "text" {
					if text, ok := blockMap["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}
		}
		return strings.Join(textParts, "\n")
	default:
		return ""
	}
}

// extractAnthropicToolCalls extracts tool calls from Anthropic content
func (s *Server) extractAnthropicToolCalls(content interface{}) []WebToolCall {
	var toolCalls []WebToolCall

	if contentArray, ok := content.([]interface{}); ok {
		for _, block := range contentArray {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockType, _ := blockMap["type"].(string); blockType == "tool_use" {
					if id, ok := blockMap["id"].(string); ok {
						if name, ok := blockMap["name"].(string); ok {
							// Convert input to JSON string
							inputJSON := "{}"
							if input := blockMap["input"]; input != nil {
								if inputBytes, err := json.Marshal(input); err == nil {
									inputJSON = string(inputBytes)
								}
							}

							toolCalls = append(toolCalls, WebToolCall{
								ID: id,
								Function: WebToolCallFunction{
									Name:      name,
									Arguments: inputJSON,
								},
							})
						}
					}
				}
			}
		}
	}

	return toolCalls
}

// extractOpenAIToolCalls extracts tool calls from OpenAI messages
func (s *Server) extractOpenAIToolCalls(message map[string]interface{}) []WebToolCall {
	var toolCalls []WebToolCall

	if toolCallsData, ok := message["tool_calls"]; ok {
		if toolCallsArray, ok := toolCallsData.([]interface{}); ok {
			for _, tc := range toolCallsArray {
				if tcMap, ok := tc.(map[string]interface{}); ok {
					if id, ok := tcMap["id"].(string); ok {
						if function, ok := tcMap["function"].(map[string]interface{}); ok {
							name, _ := function["name"].(string)
							arguments, _ := function["arguments"].(string)

							toolCalls = append(toolCalls, WebToolCall{
								ID: id,
								Function: WebToolCallFunction{
									Name:      name,
									Arguments: arguments,
								},
							})
						}
					}
				}
			}
		}
	}

	return toolCalls
}

// handleGetToolResult handles GET /api/conversations/{id}/tools/{toolCallId}
func (s *Server) handleGetToolResult(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]
	toolCallID := vars["toolCallId"]

	// Resolve conversation ID
	resolvedID, err := s.conversationService.ResolveConversationID(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusNotFound, "Conversation not found", err)
		return
	}

	// Get tool result
	response, err := s.conversationService.GetToolResult(ctx, resolvedID, toolCallID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusNotFound, "Tool result not found", err)
		return
	}

	s.writeJSONResponse(w, response)
}

// handleDeleteConversation handles DELETE /api/conversations/{id}
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]

	// Resolve conversation ID
	resolvedID, err := s.conversationService.ResolveConversationID(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusNotFound, "Conversation not found", err)
		return
	}

	// Delete conversation
	err = s.conversationService.DeleteConversation(ctx, resolvedID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to delete conversation", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleSearchConversations handles GET /api/search
func (s *Server) handleSearchConversations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	searchTerm := query.Get("q")
	if searchTerm == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "Search term is required", nil)
		return
	}

	limit := 50 // Default limit
	if limitStr := query.Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
			limit = parsedLimit
		}
	}

	// Search conversations
	response, err := s.conversationService.SearchConversations(ctx, searchTerm, limit)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to search conversations", err)
		return
	}

	s.writeJSONResponse(w, response)
}

// handleGetStatistics handles GET /api/stats
func (s *Server) handleGetStatistics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get statistics
	response, err := s.conversationService.GetConversationStatistics(ctx)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to get statistics", err)
		return
	}

	s.writeJSONResponse(w, response)
}

// HTML Handlers

// handleConversationList handles GET /
func (s *Server) handleConversationList(w http.ResponseWriter, r *http.Request) {
	tmpl, err := s.loadTemplate("index.html")
	if err != nil {
		http.Error(w, "Failed to load template", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title string
	}{
		Title: "Kodelet Conversations",
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		logger.G(r.Context()).WithError(err).Error("Failed to execute template")
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

// handleConversationView handles GET /c/{id}
func (s *Server) handleConversationView(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	tmpl, err := s.loadTemplate("conversation.html")
	if err != nil {
		http.Error(w, "Failed to load template", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title          string
		ConversationID string
	}{
		Title:          fmt.Sprintf("Conversation %s", id),
		ConversationID: id,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		logger.G(r.Context()).WithError(err).Error("Failed to execute template")
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

// Utility methods

// loadTemplate loads a template from the embedded filesystem
func (s *Server) loadTemplate(name string) (*template.Template, error) {
	templatePath := filepath.Join("templates", name)
	content, err := embedFS.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template %s: %w", name, err)
	}

	tmpl, err := template.New(name).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse template %s: %w", name, err)
	}

	return tmpl, nil
}

// writeJSONResponse writes a JSON response
func (s *Server) writeJSONResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.G(context.Background()).WithError(err).Error("Failed to encode JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// writeErrorResponse writes an error response
func (s *Server) writeErrorResponse(w http.ResponseWriter, statusCode int, message string, err error) {
	if err != nil {
		logger.G(context.Background()).WithError(err).Error(message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := map[string]interface{}{
		"error":   message,
		"status":  statusCode,
		"success": false,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.G(context.Background()).WithError(err).Error("Failed to encode error response")
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

// Close closes the server and releases resources
func (s *Server) Close() error {
	if s.conversationService != nil {
		if err := s.conversationService.Close(); err != nil {
			return err
		}
	}
	return s.Stop()
}
