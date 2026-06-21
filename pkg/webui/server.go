// Package webui provides a web server and HTTP API for kodelet's web interface.
// It serves the embedded React frontend and provides REST endpoints for
// conversation management and LLM interactions through a browser interface.
package webui

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gorilla/mux"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/goals"
	openairesponses "github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/steer"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/viper"
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
	terminalSessions    *terminalSessionManager
	terminalSessionsMu  sync.Mutex
	extensionRuntimes   *webExtensionRuntimeManager
	activeChats         map[string]*activeChatRun
	activeChatsMu       sync.Mutex
	chatSubscribers     map[string]map[*subscriberEventSink]struct{}
	chatSubscribersMu   sync.Mutex
}

type activeChatRun struct {
	cancel        context.CancelFunc
	done          chan struct{}
	doneOnce      sync.Once
	stopRequested bool
	uiInput       *webUIInputBroker
}

func newActiveChatRun(cancel context.CancelFunc) *activeChatRun {
	if cancel == nil {
		return nil
	}

	return &activeChatRun{
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

func (r *activeChatRun) markDone() {
	if r == nil {
		return
	}

	r.doneOnce.Do(func() {
		close(r.done)
	})
}

// ServerConfig holds the configuration for the web server
type ServerConfig struct {
	Host         string
	Port         int
	CWD          string
	CompactRatio float64
	AuthToken    string
	CORSOrigins  []string
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

	if c.CompactRatio <= 0.0 || c.CompactRatio > 1.0 {
		return errors.New("compact-ratio must be greater than 0.0 and less than or equal to 1.0")
	}

	if err := ValidateAuthToken(c.AuthToken); err != nil {
		return err
	}

	if _, err := normalizeConfiguredCORSOrigins(c.CORSOrigins); err != nil {
		return err
	}

	if strings.TrimSpace(c.CWD) != "" {
		if _, err := chat.ResolveConfiguredDefaultCWD(c.CWD); err != nil {
			return errors.Wrap(err, "invalid cwd")
		}
	}

	return nil
}

// NewServer creates a new web UI server
func NewServer(ctx context.Context, config *ServerConfig) (*Server, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid server configuration")
	}

	if strings.TrimSpace(config.CWD) != "" {
		normalizedCWD, err := chat.ResolveConfiguredDefaultCWD(config.CWD)
		if err != nil {
			return nil, errors.Wrap(err, "invalid server configuration")
		}
		config.CWD = normalizedCWD
	}
	config.AuthToken = strings.TrimSpace(config.AuthToken)
	normalizedCORSOrigins, err := normalizeConfiguredCORSOrigins(config.CORSOrigins)
	if err != nil {
		return nil, errors.Wrap(err, "invalid server configuration")
	}
	config.CORSOrigins = normalizedCORSOrigins

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
	extensionRuntimes := newWebExtensionRuntimeManager()

	s := &Server{
		router:              mux.NewRouter(),
		conversationService: conversationService,
		chatRunner: &webUIChatRunner{
			defaultCWD:        config.CWD,
			extensionRuntimes: extensionRuntimes,
		},
		config:            config,
		staticFS:          staticFS,
		runCtx:            runCtx,
		runCancel:         runCancel,
		terminalSessions:  newTerminalSessionManager(runCtx),
		extensionRuntimes: extensionRuntimes,
		activeChats:       make(map[string]*activeChatRun),
		chatSubscribers:   make(map[string]map[*subscriberEventSink]struct{}),
	}
	if runner, ok := s.chatRunner.(*webUIChatRunner); ok {
		runner.server = s
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
	api.HandleFunc("/chat/slash-commands", s.handleGetSlashCommands).Methods("GET")
	api.HandleFunc("/chat/cwd-suggestions", s.handleGetCWDHints).Methods("GET")
	api.HandleFunc("/git/diff", s.handleGetGitDiff).Methods("GET")
	api.HandleFunc("/terminal/ws", s.handleTerminalWebsocket).Methods("GET")
	api.HandleFunc("/conversations", s.handleListConversations).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleGetConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/stream", s.handleStreamConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/fork", s.handleForkConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/steer", s.handleGetPendingSteer).Methods("GET")
	api.HandleFunc("/conversations/{id}/steer", s.handleSteerConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/stop", s.handleStopConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/ui-input/{requestId}", s.handleRespondUIInput).Methods("POST")
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
	s.router.Use(s.authMiddleware)
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
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
			return
		}

		if !s.corsOriginAllowed(origin) {
			if r.Method == http.MethodOptions {
				http.Error(w, "CORS origin not allowed", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsOriginAllowed(origin string) bool {
	normalizedOrigin, err := normalizeCORSOrigin(origin)
	if err != nil {
		return false
	}

	if isLoopbackOrigin(normalizedOrigin) {
		return true
	}

	if s.config == nil {
		return false
	}

	for _, allowedOrigin := range s.config.CORSOrigins {
		if normalizedOrigin == allowedOrigin {
			return true
		}
	}

	return false
}

// ValidateCORSOrigins validates caller-provided CORS origins.
func ValidateCORSOrigins(origins []string) error {
	_, err := normalizeConfiguredCORSOrigins(origins)
	return err
}

func normalizeConfiguredCORSOrigins(origins []string) ([]string, error) {
	normalized := make([]string, 0, len(origins))
	seen := map[string]struct{}{}

	for _, rawOrigin := range origins {
		origin := strings.TrimSpace(rawOrigin)
		if origin == "" {
			return nil, errors.New("cors-origin cannot be empty")
		}

		normalizedOrigin, err := normalizeCORSOrigin(origin)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid cors-origin: %s", origin)
		}

		if _, ok := seen[normalizedOrigin]; ok {
			continue
		}
		seen[normalizedOrigin] = struct{}{}
		normalized = append(normalized, normalizedOrigin)
	}

	return normalized, nil
}

func normalizeCORSOrigin(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", err
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("origin must use http:// or https://")
	}
	if parsed.Host == "" {
		return "", errors.New("origin must include a host")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", errors.New("origin must not include path, query, fragment, or userinfo")
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = normalizedURLHost(parsed.Host)
	parsed.Path = ""
	return parsed.String(), nil
}

func normalizedURLHost(host string) string {
	if splitHost, splitPort, err := net.SplitHostPort(host); err == nil {
		hostname := normalizedHostname(splitHost)
		return net.JoinHostPort(hostname, splitPort)
	}

	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if strings.Contains(ip.String(), ":") {
			return "[" + strings.ToLower(ip.String()) + "]"
		}

		return strings.ToLower(ip.String())
	}

	hostname := normalizedHostname(host)
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}

	return hostname
}

func isLoopbackOrigin(origin string) bool {
	normalizedOrigin, err := normalizeCORSOrigin(origin)
	if err != nil {
		return false
	}

	parsed, err := url.Parse(normalizedOrigin)
	if err != nil {
		return false
	}

	hostname := parsed.Hostname()
	if strings.EqualFold(hostname, "localhost") {
		return true
	}

	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

const webUIAuthCookieName = "kodelet_auth_token"

// NewAuthToken generates a random token suitable for protecting the web UI.
func NewAuthToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.Wrap(err, "failed to generate auth token")
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidateAuthToken validates a caller-provided web UI auth token.
func ValidateAuthToken(authToken string) error {
	trimmed := strings.TrimSpace(authToken)
	if authToken == "" {
		return nil
	}
	if trimmed == "" {
		return errors.New("auth-token cannot be empty")
	}
	if trimmed != authToken {
		return errors.New("auth-token cannot contain leading or trailing whitespace")
	}

	for _, r := range authToken {
		if !isAuthTokenRune(r) {
			return errors.New("auth-token can only contain letters, numbers, and URL-safe punctuation (-._~)")
		}
	}

	return nil
}

func isAuthTokenRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' || r == '.' || r == '_' || r == '~'
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if s.config == nil {
		return next
	}

	authToken := strings.TrimSpace(s.config.AuthToken)
	if authToken == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		queryToken, hasQueryToken := authQueryToken(r)
		if hasQueryToken {
			if !constantTimeStringEqual(queryToken, authToken) {
				s.writeAuthError(w, r, http.StatusUnauthorized, "invalid authentication token")
				return
			}

			setWebUIAuthCookie(w, r, authToken)
			if shouldRedirectTokenRequest(r) {
				http.Redirect(w, r, tokenlessURL(r), http.StatusFound)
				return
			}

			next.ServeHTTP(w, r)
			return
		}

		if requestHasAuthToken(r, authToken) {
			next.ServeHTTP(w, r)
			return
		}

		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
	})
}

func authQueryToken(r *http.Request) (string, bool) {
	values, ok := r.URL.Query()["token"]
	if !ok || len(values) == 0 {
		return "", false
	}

	return values[0], true
}

func requestHasAuthToken(r *http.Request, authToken string) bool {
	if headerToken := authHeaderToken(r.Header.Get("Authorization")); headerToken != "" {
		return constantTimeStringEqual(headerToken, authToken)
	}

	cookie, err := r.Cookie(webUIAuthCookieName)
	if err == nil && constantTimeStringEqual(cookie.Value, authToken) {
		return true
	}

	return false
}

func authHeaderToken(headerValue string) string {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return ""
	}

	for _, prefix := range []string{"Bearer ", "Token "} {
		if len(headerValue) > len(prefix) && strings.EqualFold(headerValue[:len(prefix)], prefix) {
			return strings.TrimSpace(headerValue[len(prefix):])
		}
	}

	return headerValue
}

func shouldRedirectTokenRequest(r *http.Request) bool {
	if r.Method != http.MethodGet || isWebsocketUpgrade(r) {
		return false
	}

	path := r.URL.Path
	return !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/assets/")
}

func isWebsocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func tokenlessURL(r *http.Request) string {
	redirectURL := *r.URL
	query := redirectURL.Query()
	query.Del("token")
	redirectURL.RawQuery = query.Encode()
	if redirectURL.Path == "" {
		redirectURL.Path = "/"
	}

	return redirectURL.String()
}

func setWebUIAuthCookie(w http.ResponseWriter, r *http.Request, authToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     webUIAuthCookieName,
		Value:    authToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPSRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPSRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) writeAuthError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.writeErrorResponse(w, statusCode, message, nil)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(
		w,
		"%s\n\nOpen the tokenized URL printed by `kodelet serve`, or restart with --skip-auth to disable web UI authentication.\n",
		message,
	)
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

type forkConversationResponse struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id"`
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

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}

	return hijacker.Hijack()
}

func (s *Server) chatExecutionContext(requestCtx context.Context) context.Context {
	baseCtx := s.runCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	return logger.WithLogger(baseCtx, logger.G(requestCtx))
}

func (s *Server) terminalSessionManager() *terminalSessionManager {
	s.terminalSessionsMu.Lock()
	defer s.terminalSessionsMu.Unlock()

	if s.terminalSessions == nil {
		baseCtx := s.runCtx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		s.terminalSessions = newTerminalSessionManager(baseCtx)
	}

	return s.terminalSessions
}

func (s *Server) registerActiveChat(conversationID string, run *activeChatRun) bool {
	if strings.TrimSpace(conversationID) == "" || run == nil || run.cancel == nil {
		return false
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	if s.activeChats == nil {
		s.activeChats = make(map[string]*activeChatRun)
	}

	if _, exists := s.activeChats[conversationID]; exists {
		return false
	}

	s.activeChats[conversationID] = run
	return true
}

func (s *Server) unregisterActiveChat(conversationID string, run *activeChatRun) {
	if strings.TrimSpace(conversationID) == "" || run == nil {
		return
	}

	s.activeChatsMu.Lock()
	registered, ok := s.activeChats[conversationID]
	if ok && registered == run {
		delete(s.activeChats, conversationID)
	}
	s.activeChatsMu.Unlock()

	run.markDone()
}

func (s *Server) cancelActiveChat(conversationID string) (*activeChatRun, bool) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, false
	}

	var cancel context.CancelFunc
	s.activeChatsMu.Lock()
	run, ok := s.activeChats[conversationID]
	if ok && run != nil && !run.stopRequested {
		run.stopRequested = true
		cancel = run.cancel
	}
	s.activeChatsMu.Unlock()

	if cancel != nil {
		cancel()
	}

	return run, ok
}

func (s *Server) isActiveChat(conversationID string) bool {
	if strings.TrimSpace(conversationID) == "" {
		return false
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	run, ok := s.activeChats[conversationID]
	return ok && run != nil && !run.stopRequested
}

func (s *Server) hasActiveChatRun(conversationID string) bool {
	if strings.TrimSpace(conversationID) == "" {
		return false
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	run, ok := s.activeChats[conversationID]
	return ok && run != nil
}

func (s *Server) uiInputBrokerForRun(conversationID string) *webUIInputBroker {
	if strings.TrimSpace(conversationID) == "" {
		return nil
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	run, ok := s.activeChats[conversationID]
	if !ok || run == nil || run.stopRequested {
		return nil
	}
	return run.uiInput
}

func (s *Server) respondToUIInput(conversationID, requestID string, response extensions.UIInputResponse) bool {
	broker := s.uiInputBrokerForRun(conversationID)
	return broker != nil && broker.Respond(requestID, response)
}

func (s *Server) registerChatSubscriber(conversationID string, sink *subscriberEventSink) bool {
	if strings.TrimSpace(conversationID) == "" || sink == nil {
		return false
	}

	s.activeChatsMu.Lock()
	run, ok := s.activeChats[conversationID]
	if !ok || run == nil || run.stopRequested {
		s.activeChatsMu.Unlock()
		return false
	}

	s.chatSubscribersMu.Lock()
	if s.chatSubscribers == nil {
		s.chatSubscribers = make(map[string]map[*subscriberEventSink]struct{})
	}
	if s.chatSubscribers[conversationID] == nil {
		s.chatSubscribers[conversationID] = make(map[*subscriberEventSink]struct{})
	}
	s.chatSubscribers[conversationID][sink] = struct{}{}
	s.chatSubscribersMu.Unlock()
	s.activeChatsMu.Unlock()

	return true
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
		summary.IsRunning = s.isActiveChat(summary.ID)
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
	CWD           string       `json:"cwd,omitempty"`
	CWDLocked     bool         `json:"cwdLocked,omitempty"`
	Profile       string       `json:"profile,omitempty"`
	ProfileLocked bool         `json:"profileLocked,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	IsRunning     bool         `json:"isRunning,omitempty"`
	Usage         any          `json:"usage"`
	Messages      []WebMessage `json:"messages"`
	PendingSteer  []WebMessage `json:"pendingSteer,omitempty"`
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
	DefaultCWD     string              `json:"defaultCWD,omitempty"`
}

type SlashCommandsResponse struct {
	Commands []slashcommands.Command `json:"commands"`
}

type CWDHint struct {
	Path string `json:"path"`
}

type CWDHintsResponse struct {
	BaseDir string    `json:"baseDir,omitempty"`
	Query   string    `json:"query,omitempty"`
	Hints   []CWDHint `json:"hints"`
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
	Role          string        `json:"role"`
	Content       any           `json:"content"`
	ToolCalls     []WebToolCall `json:"toolCalls,omitempty"`
	ThinkingText  string        `json:"thinkingText,omitempty"`
	ThinkingTexts []string      `json:"thinkingTexts,omitempty"`
}

// WebContentBlock represents a typed content block rendered by the web UI.
type WebContentBlock = chat.ChatContentBlock

// WebImageSource represents inline image data for a web content block.
type WebImageSource = chat.ChatImageSource

// WebImageURL represents a remote image URL for a web content block.
type WebImageURL = chat.ChatImageURLSource

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
	defaultCWD, err := s.defaultCWD()
	if err != nil {
		defaultCWD = ""
	}

	s.writeJSONResponse(w, ChatSettingsResponse{
		CurrentProfile: getCurrentWebUIProfile(),
		Profiles:       getWebUIProfileOptions(),
		DefaultCWD:     defaultCWD,
	})
}

func (s *Server) handleGetSlashCommands(w http.ResponseWriter, r *http.Request) {
	resolvedCWD, err := s.resolveRequestedCWD(r.URL.Query().Get("cwd"))
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid cwd", err)
		return
	}

	processor, err := fragments.NewFragmentProcessor(fragments.WithDefaultDirsForCWD(resolvedCWD))
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to initialize slash commands", err)
		return
	}

	commands := slashcommands.List(r.Context(), processor)
	var extensionRuntime *extensions.Runtime
	if s.extensionRuntimes != nil {
		extensionRuntime, err = s.extensionRuntimes.Runtime(r.Context(), resolvedCWD)
	} else {
		extensionRuntime, err = extensions.NewRuntimeFromViper(r.Context(), resolvedCWD)
		if extensionRuntime != nil {
			defer func() { _ = extensionRuntime.Close() }()
		}
	}
	if err != nil {
		logger.G(r.Context()).WithError(err).Warn("Failed to initialize extensions for slash command discovery")
	} else if extensionRuntime != nil {
		commands = append(commands, extensionRuntime.SlashCommands()...)
	}

	s.writeJSONResponse(w, SlashCommandsResponse{Commands: commands})
}

func (s *Server) handleGetCWDHints(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	defaultCWD, err := s.defaultCWD()
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to resolve default cwd", err)
		return
	}

	baseDir, filter, err := resolveSuggestionBaseDir(query, defaultCWD)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid cwd query", err)
		return
	}

	hints, err := listDirectoryHints(baseDir, filter)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "failed to list cwd suggestions", err)
		return
	}

	if chat.IsNaturalDirectoryQuery(query) {
		siblingBaseDir, siblingErr := conversations.NormalizeCWD(filepath.Dir(defaultCWD))
		if siblingErr == nil && siblingBaseDir != baseDir {
			siblingHints, err := listDirectoryHints(siblingBaseDir, filter)
			if err == nil {
				hints = mergeDirectoryHints(hints, siblingHints)
			}
		}
	}

	s.writeJSONResponse(w, CWDHintsResponse{
		BaseDir: baseDir,
		Query:   query,
		Hints:   hints,
	})
}

func (s *Server) defaultCWD() (string, error) {
	configuredCWD := ""
	if s != nil && s.config != nil {
		configuredCWD = s.config.CWD
	}

	return chat.ResolveConfiguredDefaultCWD(configuredCWD)
}

func (s *Server) resolveRequestedCWD(requestedCWD string) (string, error) {
	defaultCWD, err := s.defaultCWD()
	if err != nil {
		return "", err
	}

	expandedRequestedCWD, err := chat.ExpandCWDInput(requestedCWD, defaultCWD)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(expandedRequestedCWD) == "" {
		return defaultCWD, nil
	}

	return conversations.NormalizeCWD(expandedRequestedCWD)
}

func resolveSuggestionBaseDir(query, defaultCWD string) (string, string, error) {
	expandedQuery, err := chat.ExpandCWDInput(query, defaultCWD)
	if err != nil {
		return "", "", err
	}

	if expandedQuery == "" {
		return defaultCWD, "", nil
	}

	hasTrailingSlash := strings.HasSuffix(expandedQuery, string(os.PathSeparator))
	cleanQuery := filepath.Clean(expandedQuery)
	if hasTrailingSlash {
		return cleanQuery, "", nil
	}

	baseDir := filepath.Dir(cleanQuery)
	filter := filepath.Base(cleanQuery)
	if baseDir == "." {
		baseDir = defaultCWD
	}

	baseDir, err = conversations.NormalizeCWD(baseDir)
	if err != nil {
		return "", "", err
	}

	return baseDir, filter, nil
}

func listDirectoryHints(baseDir, filter string) ([]CWDHint, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read suggestion directory")
	}

	filter = strings.ToLower(strings.TrimSpace(filter))
	hints := make([]CWDHint, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if filter != "" && !matchesDirectoryHint(name, filter) {
			continue
		}

		hints = append(hints, CWDHint{Path: filepath.Join(baseDir, name)})
	}

	sort.Slice(hints, func(i, j int) bool {
		left := strings.ToLower(filepath.Base(hints[i].Path))
		right := strings.ToLower(filepath.Base(hints[j].Path))
		return left < right
	})

	if len(hints) > 20 {
		hints = hints[:20]
	}

	return hints, nil
}

func mergeDirectoryHints(groups ...[]CWDHint) []CWDHint {
	merged := make([]CWDHint, 0)
	seen := make(map[string]struct{})

	for _, group := range groups {
		for _, hint := range group {
			if _, ok := seen[hint.Path]; ok {
				continue
			}
			seen[hint.Path] = struct{}{}
			merged = append(merged, hint)
		}
	}

	sort.Slice(merged, func(i, j int) bool {
		left := strings.ToLower(filepath.Base(merged[i].Path))
		right := strings.ToLower(filepath.Base(merged[j].Path))
		if left == right {
			return merged[i].Path < merged[j].Path
		}
		return left < right
	})

	if len(merged) > 20 {
		merged = merged[:20]
	}

	return merged
}

func matchesDirectoryHint(name, filter string) bool {
	lowerName := strings.ToLower(name)
	if strings.HasPrefix(lowerName, filter) || strings.Contains(lowerName, filter) {
		return true
	}

	filterRunes := []rune(filter)
	filterIndex := 0
	for _, char := range lowerName {
		if filterIndex >= len(filterRunes) {
			break
		}
		if char == filterRunes[filterIndex] {
			filterIndex++
		}
	}

	return filterIndex == len(filterRunes)
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
	webMessages, err := s.convertToWebMessages(response.RawMessages, providerForRender, response.Metadata, response.ToolResults)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to parse conversation messages", err)
		return
	}

	pendingSteer, err := pendingSteerWebMessages(id)
	if err != nil {
		logger.G(ctx).WithError(err).WithField("conversation_id", id).Warn("failed to read pending steering messages")
	}

	// Convert to web response format

	webResponse := &WebConversationResponse{
		ID:            response.ID,
		CreatedAt:     response.CreatedAt,
		UpdatedAt:     response.UpdatedAt,
		Provider:      providerLabel,
		CWD:           response.CWD,
		CWDLocked:     response.ID != "" && strings.TrimSpace(response.CWD) != "",
		Profile:       resolveConversationProfile(response.Metadata),
		ProfileLocked: response.ID != "",
		Summary:       response.Summary,
		IsRunning:     s.isActiveChat(response.ID),
		Usage:         response.Usage,
		Messages:      webMessages,
		PendingSteer:  pendingSteer,
		ToolResults:   response.ToolResults,
		MessageCount:  len(webMessages),
	}

	s.writeJSONResponse(w, webResponse)
}

func pendingSteerWebMessages(conversationID string) ([]WebMessage, error) {
	steerStore, err := steer.NewSteerStore()
	if err != nil {
		return nil, err
	}

	messages, err := steerStore.ReadPendingSteer(conversationID)
	if err != nil {
		return nil, err
	}

	webMessages := make([]WebMessage, 0, len(messages))
	for _, message := range messages {
		content := any(message.Content)
		if blocks := chat.ContentBlocksForUserInput(message.Content, message.Images); len(blocks) > 0 {
			content = blocks
		}
		webMessages = append(webMessages, WebMessage{
			Role:    "user",
			Content: content,
		})
	}

	return webMessages, nil
}

// convertToWebMessages converts raw messages to web messages with tool call structure
func (s *Server) convertToWebMessages(rawMessages json.RawMessage, provider string, metadata map[string]any, toolResults map[string]tooltypes.StructuredToolResult) ([]WebMessage, error) {
	if provider == "openai-responses" {
		return s.convertOpenAIResponsesToWebMessages(rawMessages, metadata, toolResults)
	}

	var messages []WebMessage
	consumedDisplays := map[string]struct{}{}

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
		if role == "system" {
			continue
		}
		if provider == "openai" && role == string(openai.ChatMessageRoleTool) {
			// Chat Completions tool results are rendered from the structured ToolResults map.
			// Showing the raw persisted tool message here duplicates the output as plain text.
			continue
		}

		webMsg := WebMessage{Role: role, Content: "", ToolCalls: []WebToolCall{}}

		// Extract tool calls and thinking content based on provider
		switch provider {
		case "anthropic":
			// For Anthropic, we need to use the full raw message to properly deserialize
			if toolCalls, err := s.extractAnthropicToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract thinking content using SDK
			if content, thinkingText, thinkingTexts, err := s.extractAnthropicContent(rawMsg); err == nil {
				webMsg.Content = content
				webMsg.ThinkingText = thinkingText
				webMsg.ThinkingTexts = thinkingTexts
			}
		case "openai":
			if toolCalls, err := s.extractOpenAIToolCalls(rawMsg); err == nil {
				webMsg.ToolCalls = toolCalls
			}
			// Extract content using SDK for consistency
			if content, thinkingText, err := s.extractOpenAIContent(rawMsg); err == nil {
				webMsg.Content = content
				webMsg.ThinkingText = thinkingText
				if thinkingText != "" {
					webMsg.ThinkingTexts = []string{thinkingText}
				}
			}
		}

		if role == "user" {
			webMsg.Content = applyWebContentDisplay(webMsg.Content, metadata, consumedDisplays)
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
func (s *Server) convertOpenAIResponsesToWebMessages(rawMessages json.RawMessage, metadata map[string]any, toolResults map[string]tooltypes.StructuredToolResult) ([]WebMessage, error) {
	streamableMessages, err := openairesponses.StreamMessages(rawMessages, toolResults)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse OpenAI Responses messages")
	}

	messages := make([]WebMessage, 0, len(streamableMessages))
	consumedDisplays := map[string]struct{}{}

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
			if webMsg.Role == "user" {
				webMsg.Content = applyWebContentDisplay(webMsg.Content, metadata, consumedDisplays)
			}
		case "thinking":
			webMsg.ThinkingText = msg.Content
			webMsg.ThinkingTexts = []string{msg.Content}
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
				if source, ok := chat.ParseDataURL(part.ImageURL); ok {
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
func (s *Server) extractAnthropicContent(rawMessage json.RawMessage) (any, string, []string, error) {
	// Deserialize single message using the Anthropic SDK
	var anthropicMessage anthropic.MessageParam
	if err := json.Unmarshal(rawMessage, &anthropicMessage); err != nil {
		return "", "", nil, errors.Wrap(err, "failed to deserialize Anthropic message")
	}

	var textParts []string
	var contentBlocks []WebContentBlock
	var thinkingText string
	var thinkingTexts []string

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
			if strings.TrimSpace(thinkingBlock.Thinking) != "" {
				thinkingTexts = append(thinkingTexts, thinkingBlock.Thinking)
			}
		}
	}

	return normalizeWebContent(textParts, contentBlocks), thinkingText, thinkingTexts, nil
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

// extractOpenAIContent extracts content and reasoning from OpenAI messages using SDK.
func (s *Server) extractOpenAIContent(rawMessage json.RawMessage) (any, string, error) {
	// Deserialize single message using the OpenAI SDK
	var openaiMessage openai.ChatCompletionMessage
	if err := json.Unmarshal(rawMessage, &openaiMessage); err != nil {
		return "", "", errors.Wrap(err, "failed to deserialize OpenAI message")
	}

	thinkingText := strings.TrimLeft(openaiMessage.ReasoningContent, "\n")

	// OpenAI messages have simple string content or multimodal content
	if openaiMessage.Content != "" {
		return openaiMessage.Content, thinkingText, nil
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
				if source, ok := chat.ParseDataURL(imageURL); ok {
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

	return normalizeWebContent(textParts, contentBlocks), thinkingText, nil
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

	sink, err := newNDJSONEventSink(w)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to initialize chat stream", err)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	subscriber := newSubscriberEventSink()
	if !s.registerChatSubscriber(conversationID, subscriber) {
		subscriber.Close()
		s.writeErrorResponse(w, http.StatusConflict, "conversation is not actively streaming", nil)
		return
	}
	defer func() {
		s.removeChatSubscriber(conversationID, subscriber)
		subscriber.Close()
	}()

	_ = sink.Send(ChatEvent{
		Kind:           "conversation",
		ConversationID: conversationID,
		Role:           "assistant",
	})

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

// handleGetPendingSteer handles GET /api/conversations/{id}/steer
func (s *Server) handleGetPendingSteer(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(mux.Vars(r)["id"])
	if conversationID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "conversation ID is required", nil)
		return
	}

	messages, err := pendingSteerWebMessages(conversationID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read pending steering messages", err)
		return
	}

	s.writeJSONResponse(w, messages)
}

type steerConversationRequest struct {
	Message string             `json:"message"`
	Content []ChatContentBlock `json:"content,omitempty"`
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

	message, imageInputs, err := chat.NormalizeRequest(ChatRequest{
		Message: req.Message,
		Content: req.Content,
	})
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid steer request", err)
		return
	}
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
	if err := steerStore.WriteSteerWithImages(conversationID, message, imageInputs); err != nil {
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

	_, stopped := s.cancelActiveChat(conversationID)
	s.writeJSONResponse(w, stopConversationResponse{
		Success:        true,
		ConversationID: conversationID,
		Stopped:        stopped,
	})
}

type uiInputResponseRequest struct {
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
}

func (s *Server) handleRespondUIInput(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	conversationID := strings.TrimSpace(vars["id"])
	requestID := strings.TrimSpace(vars["requestId"])
	if conversationID == "" || requestID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "conversation ID and request ID are required", nil)
		return
	}

	var req uiInputResponseRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid ui input response", err)
		return
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = extensions.UIInputStatusSubmitted
	}
	switch status {
	case extensions.UIInputStatusSubmitted, extensions.UIInputStatusDismissed:
	default:
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid ui input status", nil)
		return
	}

	response := extensions.UIInputResponse{Status: status, Value: req.Value}
	if strings.EqualFold(strings.TrimSpace(req.Value), "true") {
		response.Confirmed = true
	}

	if !s.respondToUIInput(conversationID, requestID, response) {
		s.writeErrorResponse(w, http.StatusNotFound, "ui input request not found", nil)
		return
	}

	s.writeJSONResponse(w, map[string]bool{"success": true})
}

// handleForkConversation handles POST /api/conversations/{id}/fork
func (s *Server) handleForkConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conversationID := strings.TrimSpace(mux.Vars(r)["id"])
	if conversationID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "conversation ID is required", nil)
		return
	}

	response, err := s.conversationService.ForkConversation(ctx, conversationID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to fork conversation", err)
		return
	}

	s.writeJSONResponse(w, forkConversationResponse{
		Success:        true,
		ConversationID: response.ID,
	})
}

// handleDeleteConversation handles DELETE /api/conversations/{id}
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]

	if s.hasActiveChatRun(id) {
		s.writeErrorResponse(w, http.StatusConflict, "conversation is actively running", nil)
		return
	}

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
	s.terminalSessionsMu.Lock()
	terminalSessions := s.terminalSessions
	s.terminalSessionsMu.Unlock()
	var firstErr error
	if terminalSessions != nil {
		terminalSessions.Close()
	}
	if s.extensionRuntimes != nil {
		if err := s.extensionRuntimes.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.runCancel != nil {
		s.runCancel()
	}
	if s.server != nil {
		if err := s.server.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func normalizeWebContent(textParts []string, blocks []WebContentBlock) any {
	if len(blocks) == 0 {
		return strings.Join(textParts, "\n")
	}
	return blocks
}

func applyWebContentDisplay(content any, metadata map[string]any, consumedDisplays map[string]struct{}) any {
	if len(metadata) == 0 {
		return content
	}
	if consumedDisplays == nil {
		consumedDisplays = map[string]struct{}{}
	}

	switch value := content.(type) {
	case string:
		if goals.IsContextText(value) {
			if display, ok := consumeWebContentDisplay(metadata, consumedDisplays, value); ok {
				return []WebContentBlock{webContentBlockForDisplay(display)}
			}
			return ""
		}
		if display, ok := conversations.LookupMessageDisplay(metadata, value); ok {
			return []WebContentBlock{webContentBlockForDisplay(display)}
		}
		return content
	case []WebContentBlock:
		for index, block := range value {
			if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
				continue
			}
			if goals.IsContextText(block.Text) {
				blocks := make([]WebContentBlock, len(value))
				copy(blocks, value)
				if display, ok := consumeWebContentDisplay(metadata, consumedDisplays, block.Text); ok {
					blocks[index] = webContentBlockForDisplay(display)
				} else {
					blocks[index] = WebContentBlock{Type: "text"}
				}
				return blocks
			}
			if display, ok := conversations.LookupMessageDisplay(metadata, block.Text); ok {
				blocks := make([]WebContentBlock, len(value))
				copy(blocks, value)
				blocks[index] = webContentBlockForDisplay(display)
				return blocks
			}
		}
	}

	return content
}

func consumeWebContentDisplay(metadata map[string]any, consumed map[string]struct{}, text string) (conversations.MessageDisplay, bool) {
	key := conversations.MessageDisplayKey(text)
	if _, ok := consumed[key]; ok {
		return conversations.MessageDisplay{}, false
	}
	display, ok := conversations.LookupMessageDisplay(metadata, text)
	if !ok {
		return conversations.MessageDisplay{}, false
	}
	consumed[key] = struct{}{}
	return display, true
}

func webContentBlockForDisplay(display conversations.MessageDisplay) WebContentBlock {
	if display.Kind == conversations.MessageDisplayKindSlashCommand || display.Kind == conversations.MessageDisplayKindGoal {
		return WebContentBlock{
			Type:    display.Kind,
			Text:    display.Text,
			Command: display.Command,
		}
	}
	return WebContentBlock{Type: "text", Text: display.Text}
}

func isEmptyWebContent(content any) bool {
	switch value := content.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(value) == ""
	case []WebContentBlock:
		if len(value) == 0 {
			return true
		}
		for _, block := range value {
			if block.Type != "text" || strings.TrimSpace(block.Text) != "" {
				return false
			}
		}
		return true
	default:
		return false
	}
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
