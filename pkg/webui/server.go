// Package webui provides a web server and HTTP API for kodelet's web interface.
// It serves the embedded React frontend and provides REST endpoints for
// conversation management and LLM interactions through a browser interface.
package webui

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	openairesponses "github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/steer"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/viper"
	"google.golang.org/genai"
)

//go:generate bash -c "cd frontend && npm install && npm run build"
//go:embed dist/*
var embedFS embed.FS

// Server represents the web UI server
type Server struct {
	router              *mux.Router
	conversationService conversations.ConversationServiceInterface
	chatRunner          ChatRunner
	config              *ServerConfig
	server              *http.Server
	staticFS            fs.FS
	runCtx              context.Context
	runCancel           context.CancelFunc
	activeChats         map[string]context.CancelFunc
	activeChatsMu       sync.Mutex
	chatSubscribers     map[string]map[*subscriberEventSink]struct{}
	chatSubscribersMu   sync.Mutex
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

	runCtx, runCancel := context.WithCancel(ctx)

	s := &Server{
		router:              mux.NewRouter(),
		conversationService: conversationService,
		chatRunner:          NewDefaultChatRunner(),
		config:              config,
		staticFS:            staticFS,
		runCtx:              runCtx,
		runCancel:           runCancel,
		activeChats:         make(map[string]context.CancelFunc),
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
	}

	// Setup routes
	s.setupRoutes()

	return s, nil
}

// setupRoutes configures all the HTTP routes
func (s *Server) setupRoutes() {
	// API routes
	api := s.router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/chat/settings", s.handleGetChatSettings).Methods("GET")
	api.HandleFunc("/conversations", s.handleListConversations).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleGetConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/stream", s.handleStreamConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/steer", s.handleSteerConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/stop", s.handleStopConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/tools/{toolCallId}", s.handleGetToolResult).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleDeleteConversation).Methods("DELETE")
	api.HandleFunc("/chat", s.handleChat).Methods("POST")

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

type stopConversationResponse struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id"`
	Stopped        bool   `json:"stopped"`
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) chatExecutionContext(requestCtx context.Context) context.Context {
	baseCtx := s.runCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	return logger.WithLogger(baseCtx, logger.G(requestCtx))
}

func (s *Server) registerActiveChat(conversationID string, cancel context.CancelFunc) {
	if strings.TrimSpace(conversationID) == "" || cancel == nil {
		return
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	if s.activeChats == nil {
		s.activeChats = make(map[string]context.CancelFunc)
	}
	s.activeChats[conversationID] = cancel
}

func (s *Server) unregisterActiveChat(conversationID string, cancel context.CancelFunc) {
	if strings.TrimSpace(conversationID) == "" {
		return
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	registered, ok := s.activeChats[conversationID]
	if ok && reflect.ValueOf(registered).Pointer() == reflect.ValueOf(cancel).Pointer() {
		delete(s.activeChats, conversationID)
	}
}

func (s *Server) cancelActiveChat(conversationID string) bool {
	if strings.TrimSpace(conversationID) == "" {
		return false
	}

	s.activeChatsMu.Lock()
	cancel, ok := s.activeChats[conversationID]
	if ok {
		delete(s.activeChats, conversationID)
	}
	s.activeChatsMu.Unlock()

	if ok {
		cancel()
	}

	return ok
}

func (s *Server) isActiveChat(conversationID string) bool {
	if strings.TrimSpace(conversationID) == "" {
		return false
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	_, ok := s.activeChats[conversationID]
	return ok
}

func (s *Server) addChatSubscriber(conversationID string, sink *subscriberEventSink) {
	if strings.TrimSpace(conversationID) == "" || sink == nil {
		return
	}

	s.chatSubscribersMu.Lock()
	defer s.chatSubscribersMu.Unlock()
	if s.chatSubscribers == nil {
		s.chatSubscribers = make(map[string]map[*subscriberEventSink]struct{})
	}
	if s.chatSubscribers[conversationID] == nil {
		s.chatSubscribers[conversationID] = make(map[*subscriberEventSink]struct{})
	}
	s.chatSubscribers[conversationID][sink] = struct{}{}
}

func (s *Server) removeChatSubscriber(conversationID string, sink *subscriberEventSink) {
	if strings.TrimSpace(conversationID) == "" || sink == nil {
		return
	}

	s.chatSubscribersMu.Lock()
	defer s.chatSubscribersMu.Unlock()
	subscribers := s.chatSubscribers[conversationID]
	if subscribers == nil {
		return
	}
	delete(subscribers, sink)
	if len(subscribers) == 0 {
		delete(s.chatSubscribers, conversationID)
	}
}

func (s *Server) broadcastChatEvent(conversationID string, event ChatEvent) {
	if strings.TrimSpace(conversationID) == "" {
		return
	}

	s.chatSubscribersMu.Lock()
	subscribers := make([]*subscriberEventSink, 0, len(s.chatSubscribers[conversationID]))
	for sink := range s.chatSubscribers[conversationID] {
		subscribers = append(subscribers, sink)
	}
	s.chatSubscribersMu.Unlock()

	for _, sink := range subscribers {
		if err := sink.Send(event); err != nil {
			s.removeChatSubscriber(conversationID, sink)
			sink.Close()
		}
	}
}

func (s *Server) closeChatSubscribers(conversationID string) {
	if strings.TrimSpace(conversationID) == "" {
		return
	}

	s.chatSubscribersMu.Lock()
	subscribers := s.chatSubscribers[conversationID]
	delete(s.chatSubscribers, conversationID)
	s.chatSubscribersMu.Unlock()

	for sink := range subscribers {
		sink.Close()
	}
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

// WebConversationResponse represents a conversation response for the web UI.
type WebConversationResponse struct {
	ID            string       `json:"id"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
	Provider      string       `json:"provider"`
	Profile       string       `json:"profile,omitempty"`
	ProfileLocked bool         `json:"profileLocked,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	Usage         any          `json:"usage"`
	Messages      []WebMessage `json:"messages"`
	ToolResults   any          `json:"toolResults,omitempty"`
	MessageCount  int          `json:"messageCount"`
}

// ChatProfileOption represents a selectable profile in the web UI.
type ChatProfileOption struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Active bool   `json:"active,omitempty"`
}

// ChatSettingsResponse contains profile settings for the web chat composer.
type ChatSettingsResponse struct {
	CurrentProfile string              `json:"currentProfile,omitempty"`
	Profiles       []ChatProfileOption `json:"profiles"`
}

const (
	webUIBuiltInProfileScope       = "built-in"
	webUIRepoProfileScope          = "repo"
	webUIGlobalProfileScope        = "global"
	webUIRepoOverridesProfileScope = "repo (overrides global)"
	webUIProfileSourceRepo         = "repo"
	webUIProfileSourceGlobal       = "global"
	webUIProfileSourceBoth         = "both"
)

// WebMessage represents a message with structured tool calls for the web UI
type WebMessage struct {
	Role         string        `json:"role"`
	Content      any           `json:"content"`
	ToolCalls    []WebToolCall `json:"toolCalls,omitempty"`
	ThinkingText string        `json:"thinkingText,omitempty"`
}

// WebContentBlock represents a typed content block rendered by the web UI.
type WebContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Source   *WebImageSource `json:"source,omitempty"`
	ImageURL *WebImageURL    `json:"image_url,omitempty"`
}

// WebImageSource represents inline image data for a web content block.
type WebImageSource struct {
	Data      string `json:"data"`
	MediaType string `json:"media_type"`
}

// WebImageURL represents a remote image URL for a web content block.
type WebImageURL struct {
	URL string `json:"url"`
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

func resolveConversationProfile(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	rawProfile, ok := metadata["profile"]
	if !ok {
		return ""
	}
	profile, ok := rawProfile.(string)
	if !ok {
		return ""
	}
	profile = strings.TrimSpace(profile)
	if profile == "" || strings.EqualFold(profile, "default") {
		return ""
	}
	return profile
}

func getGlobalProfiles() map[string]llmtypes.ProfileConfig {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	v.AddConfigPath(filepath.Join(homeDir, ".kodelet"))

	if err := v.ReadInConfig(); err != nil {
		return nil
	}

	return extractProfiles(v)
}

func getRepoProfiles() map[string]llmtypes.ProfileConfig {
	v := viper.New()
	v.SetConfigName("kodelet-config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return nil
	}

	return extractProfiles(v)
}

func extractProfiles(v *viper.Viper) map[string]llmtypes.ProfileConfig {
	if !v.IsSet("profiles") {
		return nil
	}

	profilesMap := v.GetStringMap("profiles")
	profiles := make(map[string]llmtypes.ProfileConfig)
	for name, profileData := range profilesMap {
		if strings.EqualFold(name, "default") {
			continue
		}
		profileMap, ok := profileData.(map[string]any)
		if !ok {
			continue
		}
		profiles[name] = llmtypes.ProfileConfig(profileMap)
	}

	if len(profiles) == 0 {
		return nil
	}
	return profiles
}

func mergeProfiles(globalProfiles, repoProfiles map[string]llmtypes.ProfileConfig) map[string]string {
	merged := make(map[string]string)

	for name := range globalProfiles {
		merged[name] = webUIProfileSourceGlobal
	}

	for name := range repoProfiles {
		if _, exists := merged[name]; exists {
			merged[name] = webUIProfileSourceBoth
		} else {
			merged[name] = webUIProfileSourceRepo
		}
	}

	return merged
}

func getWebUIProfileOptions() []ChatProfileOption {
	globalProfiles := getGlobalProfiles()
	repoProfiles := getRepoProfiles()
	mergedProfiles := mergeProfiles(globalProfiles, repoProfiles)
	activeProfile := strings.TrimSpace(viper.GetString("profile"))
	if strings.EqualFold(activeProfile, "default") {
		activeProfile = ""
	}

	profiles := []ChatProfileOption{{
		Name:   "default",
		Scope:  webUIBuiltInProfileScope,
		Active: activeProfile == "",
	}}

	names := make([]string, 0, len(mergedProfiles))
	for name := range mergedProfiles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		source := mergedProfiles[name]
		scope := webUIRepoProfileScope
		switch source {
		case webUIProfileSourceBoth:
			scope = webUIRepoOverridesProfileScope
		case webUIProfileSourceGlobal:
			scope = webUIGlobalProfileScope
		}

		profiles = append(profiles, ChatProfileOption{
			Name:   name,
			Scope:  scope,
			Active: name == activeProfile,
		})
	}

	return profiles
}

func getCurrentWebUIProfile() string {
	profile := strings.TrimSpace(viper.GetString("profile"))
	if profile == "" || strings.EqualFold(profile, "default") {
		return "default"
	}
	return profile
}

// handleGetChatSettings handles GET /api/chat/settings.
func (s *Server) handleGetChatSettings(w http.ResponseWriter, _ *http.Request) {
	s.writeJSONResponse(w, ChatSettingsResponse{
		CurrentProfile: getCurrentWebUIProfile(),
		Profiles:       getWebUIProfileOptions(),
	})
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
		ID:            response.ID,
		CreatedAt:     response.CreatedAt,
		UpdatedAt:     response.UpdatedAt,
		Provider:      providerLabel,
		Profile:       resolveConversationProfile(response.Metadata),
		ProfileLocked: response.ID != "",
		Summary:       response.Summary,
		Usage:         response.Usage,
		Messages:      webMessages,
		ToolResults:   response.ToolResults,
		MessageCount:  len(webMessages),
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

		webMsg := WebMessage{Role: role, Content: "", ToolCalls: []WebToolCall{}}

		// Extract tool calls and thinking content based on provider
		switch provider {
		case "anthropic":
			// For Anthropic, we need to use the full raw message to properly deserialize
			if toolCalls, err := s.extractAnthropicToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract thinking content using SDK
			if content, thinkingText, err := s.extractAnthropicContent(rawMsg); err == nil {
				webMsg.Content = content
				webMsg.ThinkingText = thinkingText
			}
		case "openai":
			if toolCalls, err := s.extractOpenAIToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract content using SDK for consistency
			if content, err := s.extractOpenAIContent(rawMsg); err == nil {
				webMsg.Content = content
			}
		case "google":
			if toolCalls, err := s.extractGoogleToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract content and thinking text using Google SDK
			if content, thinkingText, err := s.extractGoogleContent(rawMsg); err == nil {
				webMsg.Content = content
				webMsg.ThinkingText = thinkingText
			}
		}

		// Skip empty messages (no content, no tool calls, and no thinking text)
		// pretty much neglecting the user tool call feedback as it is covered by the toolresult block at
		if isEmptyWebContent(webMsg.Content) && len(webMsg.ToolCalls) == 0 && webMsg.ThinkingText == "" {
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
			if msg.RawItem != nil {
				if content, err := s.extractOpenAIResponsesInputContent(msg.RawItem); err == nil && !isEmptyWebContent(content) {
					webMsg.Content = content
				} else {
					webMsg.Content = msg.Content
				}
			} else {
				webMsg.Content = msg.Content
			}
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

func (s *Server) extractOpenAIResponsesInputContent(rawMessage json.RawMessage) (any, error) {
	var inputItem struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text,omitempty"`
			ImageURL string `json:"image_url,omitempty"`
		} `json:"content"`
	}

	if err := json.Unmarshal(rawMessage, &inputItem); err != nil {
		return "", errors.Wrap(err, "failed to deserialize OpenAI Responses input item")
	}

	if len(inputItem.Content) == 0 {
		return "", nil
	}

	var textParts []string
	var contentBlocks []WebContentBlock
	for _, part := range inputItem.Content {
		switch part.Type {
		case "input_text":
			if part.Text == "" {
				continue
			}
			textParts = append(textParts, part.Text)
			contentBlocks = append(contentBlocks, WebContentBlock{Type: "text", Text: part.Text})
		case "input_image":
			if part.ImageURL == "" {
				continue
			}
			if strings.HasPrefix(part.ImageURL, "data:") {
				if source, ok := parseDataURL(part.ImageURL); ok {
					contentBlocks = append(contentBlocks, WebContentBlock{Type: "image", Source: source})
					continue
				}
			}

			contentBlocks = append(contentBlocks, WebContentBlock{
				Type:     "image",
				ImageURL: &WebImageURL{URL: part.ImageURL},
			})
		}
	}

	return normalizeWebContent(textParts, contentBlocks), nil
}

// extractAnthropicContent extracts both text content and thinking blocks using Anthropic SDK
func (s *Server) extractAnthropicContent(rawMessage json.RawMessage) (any, string, error) {
	// Deserialize single message using the Anthropic SDK
	var anthropicMessage anthropic.MessageParam
	if err := json.Unmarshal(rawMessage, &anthropicMessage); err != nil {
		return "", "", errors.Wrap(err, "failed to deserialize Anthropic message")
	}

	var textParts []string
	var contentBlocks []WebContentBlock
	var thinkingText string

	for _, contentBlock := range anthropicMessage.Content {
		// Handle text blocks
		if textBlock := contentBlock.OfText; textBlock != nil {
			textParts = append(textParts, textBlock.Text)
			contentBlocks = append(contentBlocks, WebContentBlock{Type: "text", Text: textBlock.Text})
		}
		if imageBlock := contentBlock.OfImage; imageBlock != nil {
			if imageBlock.Source.OfBase64 != nil {
				contentBlocks = append(contentBlocks, WebContentBlock{
					Type: "image",
					Source: &WebImageSource{
						Data:      imageBlock.Source.OfBase64.Data,
						MediaType: string(imageBlock.Source.OfBase64.MediaType),
					},
				})
			}
		}
		// Handle thinking blocks
		if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil {
			thinkingText = thinkingBlock.Thinking
		}
	}

	return normalizeWebContent(textParts, contentBlocks), thinkingText, nil
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
func (s *Server) extractOpenAIContent(rawMessage json.RawMessage) (any, error) {
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
	var contentBlocks []WebContentBlock
	for _, part := range openaiMessage.MultiContent {
		if part.Type == openai.ChatMessagePartTypeText {
			textParts = append(textParts, part.Text)
			contentBlocks = append(contentBlocks, WebContentBlock{Type: "text", Text: part.Text})
		}
		if part.Type == openai.ChatMessagePartTypeImageURL && part.ImageURL != nil {
			imageURL := part.ImageURL.URL
			if strings.HasPrefix(imageURL, "data:") {
				if source, ok := parseDataURL(imageURL); ok {
					contentBlocks = append(contentBlocks, WebContentBlock{Type: "image", Source: source})
					continue
				}
			}

			contentBlocks = append(contentBlocks, WebContentBlock{
				Type:     "image",
				ImageURL: &WebImageURL{URL: imageURL},
			})
		}
	}

	return normalizeWebContent(textParts, contentBlocks), nil
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

// handleStreamConversation handles GET /api/conversations/{id}/stream
func (s *Server) handleStreamConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(mux.Vars(r)["id"])
	if conversationID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "conversation ID is required", nil)
		return
	}

	if !s.isActiveChat(conversationID) {
		s.writeErrorResponse(w, http.StatusConflict, "conversation is not actively streaming", nil)
		return
	}

	sink, err := newNDJSONEventSink(w)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to initialize chat stream", err)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_ = sink.Send(ChatEvent{
		Kind:           "conversation",
		ConversationID: conversationID,
		Role:           "assistant",
	})

	subscriber := newSubscriberEventSink()
	s.addChatSubscriber(conversationID, subscriber)
	defer func() {
		s.removeChatSubscriber(conversationID, subscriber)
		subscriber.Close()
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-subscriber.ch:
			if !ok {
				return
			}
			if err := sink.Send(event); err != nil {
				return
			}
		}
	}
}

type steerConversationRequest struct {
	Message string `json:"message"`
}

type steerConversationResponse struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id"`
	Queued         bool   `json:"queued"`
}

// handleSteerConversation handles POST /api/conversations/{id}/steer
func (s *Server) handleSteerConversation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	conversationID := strings.TrimSpace(vars["id"])
	if conversationID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "conversation ID is required", nil)
		return
	}

	var req steerConversationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid steer request", err)
		return
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "message cannot be empty", nil)
		return
	}
	if len(message) > steer.MaxMessageLength {
		s.writeErrorResponse(
			w,
			http.StatusBadRequest,
			fmt.Sprintf("message must be %d characters or fewer", steer.MaxMessageLength),
			nil,
		)
		return
	}

	steerStore, err := steer.NewSteerStore()
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to initialize steer store", err)
		return
	}

	queued := steerStore.HasPendingSteer(conversationID)
	if err := steerStore.WriteSteer(conversationID, message); err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to queue steering message", err)
		return
	}

	s.writeJSONResponse(w, steerConversationResponse{
		Success:        true,
		ConversationID: conversationID,
		Queued:         queued,
	})
}

// handleStopConversation handles POST /api/conversations/{id}/stop
func (s *Server) handleStopConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(mux.Vars(r)["id"])
	if conversationID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "conversation ID is required", nil)
		return
	}

	stopped := s.cancelActiveChat(conversationID)
	s.writeJSONResponse(w, stopConversationResponse{
		Success:        true,
		ConversationID: conversationID,
		Stopped:        stopped,
	})
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
	if s.runCancel != nil {
		s.runCancel()
	}
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// extractGoogleContent extracts text content and thinking text from Google Content using Google SDK
func (s *Server) extractGoogleContent(rawMessage json.RawMessage) (any, string, error) {
	// Deserialize single message using the Google GenAI SDK
	var googleContent genai.Content
	if err := json.Unmarshal(rawMessage, &googleContent); err != nil {
		return "", "", errors.Wrap(err, "failed to deserialize Google message")
	}

	var textParts []string
	var contentBlocks []WebContentBlock
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
				contentBlocks = append(contentBlocks, WebContentBlock{Type: "text", Text: part.Text})
			}
		}

		if part.InlineData != nil {
			contentBlocks = append(contentBlocks, WebContentBlock{
				Type: "image",
				Source: &WebImageSource{
					Data:      base64.StdEncoding.EncodeToString(part.InlineData.Data),
					MediaType: part.InlineData.MIMEType,
				},
			})
		}
	}

	return normalizeWebContent(textParts, contentBlocks), thinkingText, nil
}

func normalizeWebContent(textParts []string, blocks []WebContentBlock) any {
	if len(blocks) == 0 {
		return strings.Join(textParts, "\n")
	}
	return blocks
}

func isEmptyWebContent(content any) bool {
	switch value := content.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(value) == ""
	case []WebContentBlock:
		return len(value) == 0
	default:
		return false
	}
}

func parseDataURL(dataURL string) (*WebImageSource, bool) {
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, false
	}

	prefix, data, found := strings.Cut(dataURL, ",")
	if !found {
		return nil, false
	}

	mediaType, hasBase64 := strings.CutPrefix(prefix, "data:")
	if !hasBase64 {
		return nil, false
	}

	mediaType = strings.TrimSuffix(mediaType, ";base64")
	if mediaType == "" || !strings.HasSuffix(prefix, ";base64") {
		return nil, false
	}

	return &WebImageSource{Data: data, MediaType: mediaType}, true
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
