// Package controlplane provides Kodelet's central HTTP API and server runtime.
// It owns authentication, conversations, chat execution, runner coordination,
// and workspace proxying while accepting an optional frontend HTTP handler.
package controlplane

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/llm"
	openairesponses "github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/steer"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/viper"
)

// FrontendHandler serves an optional browser frontend and identifies static
// frontend resources that must remain public before browser authentication.
type FrontendHandler interface {
	http.Handler
	IsPublicPath(path string) bool
}

type unavailableFrontendHandler struct{}

func (unavailableFrontendHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.NotFound(w, r)
}

func (unavailableFrontendHandler) IsPublicPath(string) bool {
	return false
}

// Server represents the Kodelet control-plane server.
type Server struct {
	router                *mux.Router
	conversationService   conversations.ConversationServiceInterface
	chatRunner            chat.ChatRunner
	config                *ServerConfig
	server                *http.Server
	frontendHandler       FrontendHandler
	runCtx                context.Context
	runCancel             context.CancelFunc
	terminalSessions      *terminalSessionManager
	terminalSessionsMu    sync.Mutex
	remoteTerminals       map[string]int
	remoteTerminalsMu     sync.Mutex
	extensionRuntimes     *extensions.RuntimeManager
	runnerRegistry        *runnerregistry.Registry
	authStore             *authStore
	oidcFlow              OIDCFlow
	activeChats           map[string]*activeChatRun
	pendingChatStops      map[string]time.Time
	deletingConversations map[string]struct{}
	activeChatsMu         sync.Mutex
	chatSubscribers       map[string]map[*subscriberEventSink]struct{}
	chatSubscribersMu     sync.Mutex
	publicAuthRates       map[string]publicAuthRateEntry
	publicAuthRatesMu     sync.Mutex
	codexAuth             codexProviderAuthService
	codexDeviceLogin      *codexDeviceLoginSession
	codexDeviceLoginMu    sync.Mutex
	shutdownTimeout       time.Duration
}

type activeChatRun struct {
	cancel        context.CancelFunc
	done          chan struct{}
	doneOnce      sync.Once
	turnID        string
	stopRequested bool
	uiInput       *webUIInputBroker
}

const (
	pendingChatStopTTL                   = 30 * time.Second
	maxPendingChatStops                  = 1024
	conversationStreamKeepAliveInterval  = 15 * time.Second
	publicAuthRateWindow                 = time.Minute
	maxPublicAuthRateEntries             = 4096
	maxOIDCLoginRequestsPerWindow        = 60
	maxEnrollmentStartsPerWindow         = 30
	maxEnrollmentPollsPerWindow          = 8192
	maxUserLoginStartsPerWindow          = 30
	maxUserLoginPollsPerWindow           = 8192
	defaultHTTPShutdownTimeout           = 30 * time.Second
	controlPlaneWorkspaceDisabledMessage = "control-plane workspace is disabled"
)

type publicAuthRateEntry struct {
	windowStart time.Time
	count       int
}

type httpShutdownError struct {
	err error
}

func (e *httpShutdownError) Error() string {
	return "HTTP server shutdown did not complete: " + e.err.Error()
}

func (e *httpShutdownError) Unwrap() error {
	return e.err
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

// ServerConfig holds the configuration for the control-plane server.
type ServerConfig struct {
	Host                         string
	Port                         int
	CWD                          string
	CompactRatio                 float64
	AuthToken                    string
	RunnerAuthToken              string
	WebAuthMode                  WebAuthMode
	RunnerAuthMode               RunnerAuthMode
	OIDC                         OIDCConfig
	DisableControlPlaneWorkspace bool
	CORSOrigins                  []string
}

// Validate validates the server configuration
func (c *ServerConfig) Validate() error {
	if c == nil {
		return errors.New("server configuration is required")
	}
	if err := ValidateAuthToken(c.AuthToken); err != nil {
		return err
	}
	if err := ValidateAuthToken(c.RunnerAuthToken); err != nil {
		return errors.Wrap(err, "invalid runner auth token")
	}
	c.normalizeAuth()

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
	if c.DisableControlPlaneWorkspace && strings.TrimSpace(c.CWD) != "" {
		return errors.New("cwd cannot be set when the control-plane workspace is disabled")
	}

	if c.AuthToken != "" && c.RunnerAuthToken != "" && c.AuthToken == c.RunnerAuthToken {
		return errors.New("runner auth token must differ from the web UI auth token")
	}
	if err := c.validateAuthModes(); err != nil {
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

// NewServer creates a control-plane server. frontendHandler may be nil only for
// API-only token or unauthenticated deployments.
func NewServer(ctx context.Context, config *ServerConfig, frontendHandler FrontendHandler) (*Server, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid server configuration")
	}
	if frontendHandler == nil && (config.resolvedWebAuthMode() == WebAuthModeOIDC || config.resolvedRunnerAuthMode() == RunnerAuthModeEnrollment) {
		return nil, errors.New("frontend handler is required when OIDC authentication or runner enrollment is enabled")
	}

	if strings.TrimSpace(config.CWD) != "" {
		normalizedCWD, err := chat.ResolveConfiguredDefaultCWD(config.CWD)
		if err != nil {
			return nil, errors.Wrap(err, "invalid server configuration")
		}
		config.CWD = normalizedCWD
	}
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

	runCtx, runCancel := context.WithCancel(ctx)
	var extensionRuntimes *extensions.RuntimeManager
	var terminalSessions *terminalSessionManager
	if !config.DisableControlPlaneWorkspace {
		extensionRuntimes = extensions.NewRuntimeManager()
		terminalSessions = newTerminalSessionManager(runCtx)
	}
	dbPath, err := db.DefaultDBPath()
	if err != nil {
		runCancel()
		_ = conversationService.Close()
		return nil, errors.Wrap(err, "failed to resolve runner persistence path")
	}
	var authenticationStore *authStore
	if config.requiresAuthStore() {
		authenticationStore, err = newAuthStore(runCtx, dbPath)
		if err != nil {
			runCancel()
			_ = conversationService.Close()
			return nil, errors.Wrap(err, "failed to open control-plane authentication store")
		}
	}
	var oidcFlow OIDCFlow
	if config.resolvedWebAuthMode() == WebAuthModeOIDC {
		oidcFlow = config.OIDC.Flow
		if oidcFlow == nil {
			oidcFlow, err = newProviderOIDCFlow(runCtx, config.OIDC)
			if err != nil {
				runCancel()
				_ = authenticationStore.Close()
				_ = conversationService.Close()
				return nil, err
			}
		}
	}
	runnerPersistence, err := runnerregistry.NewSQLitePersistence(runCtx, dbPath, "")
	if err != nil {
		runCancel()
		_ = authenticationStore.Close()
		_ = conversationService.Close()
		return nil, errors.Wrap(err, "failed to open runner persistence")
	}
	runnerRegistry, err := runnerregistry.New(runCtx, runnerregistry.Options{Persistence: runnerPersistence, Credentials: authenticationStore})
	if err != nil {
		runCancel()
		_ = authenticationStore.Close()
		_ = conversationService.Close()
		return nil, errors.Wrap(err, "failed to create runner registry")
	}

	s := &Server{
		router:              mux.NewRouter(),
		conversationService: conversationService,
		chatRunner: &serverChatRunner{
			runner: chat.NewDefaultChatRunner(config.CWD, extensionRuntimes),
		},
		config:                config,
		frontendHandler:       frontendHandler,
		runCtx:                runCtx,
		runCancel:             runCancel,
		terminalSessions:      terminalSessions,
		extensionRuntimes:     extensionRuntimes,
		runnerRegistry:        runnerRegistry,
		authStore:             authenticationStore,
		oidcFlow:              oidcFlow,
		activeChats:           make(map[string]*activeChatRun),
		pendingChatStops:      make(map[string]time.Time),
		deletingConversations: make(map[string]struct{}),
		chatSubscribers:       make(map[string]map[*subscriberEventSink]struct{}),
	}
	runnerRegistry.SetEnvironmentErrorHandler(func(conversationID string) {
		s.cancelActiveChat(conversationID)
	})
	if runner, ok := s.chatRunner.(*serverChatRunner); ok {
		runner.server = s
		runner.runner.SetEnvironmentResolver(runner)
	}

	// Setup routes
	s.setupRoutes()

	return s, nil
}

// setupRoutes configures all the HTTP routes
func (s *Server) setupRoutes() {
	// Authentication and browser approval routes.
	s.router.HandleFunc("/auth/login", s.handleOIDCLogin).Methods("GET")
	s.router.HandleFunc(OIDCCallbackPath, s.handleOIDCCallback).Methods("GET")
	s.router.HandleFunc("/auth/logout", s.handleLogout).Methods("GET")
	s.router.HandleFunc(userauth.DeviceVerificationPath, s.handleUserLoginVerificationPage).Methods("GET", "HEAD")
	s.router.HandleFunc(userauth.DeviceStartPath, s.handleStartUserLogin).Methods("POST")
	s.router.HandleFunc(userauth.DevicePollPath, s.handlePollUserLogin).Methods("POST")
	s.router.HandleFunc(userauth.CurrentCredentialPath, s.handleRevokeCurrentUserCredential).Methods("DELETE")
	s.router.HandleFunc("/runner/enroll", s.requireRole(RoleRunnerAdmin, s.handleRunnerEnrollmentPage)).Methods("GET", "HEAD")
	s.router.HandleFunc(protocol.EnrollmentStartPath, s.handleStartRunnerEnrollment).Methods("POST")
	s.router.HandleFunc(protocol.EnrollmentPollPath, s.handlePollRunnerEnrollment).Methods("POST")

	// API routes
	api := s.router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/auth/me", s.handleAuthMe).Methods("GET")
	api.HandleFunc("/auth/v1/device/context", s.handleUserLoginContext).Methods("GET")
	api.HandleFunc("/auth/v1/device/decision", s.handleUserLoginDecision).Methods("POST")
	api.HandleFunc("/providers/codex", s.requireRole(RoleAdmin, s.handleGetCodexProvider)).Methods("GET")
	api.HandleFunc("/providers/codex/device-login", s.requireRole(RoleAdmin, s.handleStartCodexDeviceLogin)).Methods("POST")
	api.HandleFunc("/providers/codex/device-login/{id}", s.requireRole(RoleAdmin, s.handleGetCodexDeviceLogin)).Methods("GET")
	api.HandleFunc("/providers/codex/device-login/{id}", s.requireRole(RoleAdmin, s.handleCancelCodexDeviceLogin)).Methods("DELETE")
	api.HandleFunc("/runner/v1/enrollment/context", s.requireRole(RoleRunnerAdmin, s.handleRunnerEnrollmentContext)).Methods("GET")
	api.HandleFunc("/runner/v1/enrollment/decision", s.requireRole(RoleRunnerAdmin, s.handleRunnerEnrollmentDecision)).Methods("POST")
	api.HandleFunc("/chat/settings", s.handleGetChatSettings).Methods("GET")
	api.HandleFunc("/chat/slash-commands", s.handleGetSlashCommands).Methods("GET")
	api.HandleFunc("/chat/cwd-suggestions", s.handleGetCWDHints).Methods("GET")
	api.HandleFunc("/git/diff", s.handleGetGitDiff).Methods("GET")
	api.HandleFunc("/terminal/ws", s.requireRole(RoleTerminal, s.handleTerminalWebsocket)).Methods("GET")
	api.HandleFunc("/runner/v1/connect", s.handleRunnerWebsocket).Methods("GET")
	api.HandleFunc("/runners", s.requireRole(RoleUser, s.handleListRunners)).Methods("GET")
	api.HandleFunc("/runners/{id}", s.requireRole(RoleRunnerAdmin, s.handleGetRunner)).Methods("GET")
	api.HandleFunc("/runners/{id}", s.requireRole(RoleRunnerAdmin, s.handleDeleteRunner)).Methods("DELETE")
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

	// A frontend is composed by the caller and mounted after every control-plane
	// route so API ownership stays independent from the browser implementation.
	// The unavailable fallback keeps middleware behavior consistent in API-only
	// deployments, including CORS preflight and request logging for unmatched paths.
	s.router.PathPrefix("/").HandlerFunc(s.serveFrontend)

	// Add middleware
	s.router.Use(s.loggingMiddleware)
	s.router.Use(s.corsMiddleware)
	s.router.Use(s.authMiddleware)
}

func (s *Server) serveFrontend(w http.ResponseWriter, r *http.Request) {
	frontendHandler := FrontendHandler(unavailableFrontendHandler{})
	if s != nil && s.frontendHandler != nil {
		frontendHandler = s.frontendHandler
	}
	if r != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		if canonicalPath, ok := canonicalAuthApprovalPath(r.URL.Path); ok && r.URL.Path != canonicalPath {
			target := canonicalPath
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
			return
		}
	}
	frontendHandler.ServeHTTP(w, r)
}

// ServeHTTP serves the configured control-plane routes and browser frontend.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.router == nil {
		http.NotFound(w, r)
		return
	}
	s.router.ServeHTTP(w, r)
}

func canonicalAuthApprovalPath(path string) (string, bool) {
	trimmed := strings.TrimRight(path, "/")
	switch {
	case strings.EqualFold(trimmed, userauth.DeviceVerificationPath):
		return userauth.DeviceVerificationPath, true
	case strings.EqualFold(trimmed, "/runner/enroll"):
		return "/runner/enroll", true
	default:
		return "", false
	}
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, "+webCSRFHeaderName)

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
	if (parsed.Scheme == "http" && parsed.Port() == "80") || (parsed.Scheme == "https" && parsed.Port() == "443") {
		parsed.Host = normalizedURLHost(parsed.Hostname())
	}
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

func (s *Server) shouldRedirectTokenRequest(r *http.Request) bool {
	if r.Method != http.MethodGet || isWebsocketUpgrade(r) {
		return false
	}

	path := r.URL.Path
	return !strings.HasPrefix(path, "/api/") && !s.isPublicFrontendPath(path)
}

func (s *Server) isPublicFrontendPath(path string) bool {
	return s != nil && s.frontendHandler != nil && s.frontendHandler.IsPublicPath(path)
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
	return r.TLS != nil || strings.EqualFold(firstForwardedValue(r.Header.Get("X-Forwarded-Proto")), "https")
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
	s.prunePendingChatStopsLocked(time.Now())
	if run.turnID != "" {
		key := pendingChatStopKey(conversationID, run.turnID)
		if _, stopped := s.pendingChatStops[key]; stopped {
			return false
		}
	}

	if _, exists := s.activeChats[conversationID]; exists {
		return false
	}
	if _, deleting := s.deletingConversations[conversationID]; deleting {
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

func (s *Server) cancelActiveChat(conversationID string) bool {
	_, stopped := s.requestActiveChatStop(conversationID, "")
	return stopped
}

func (s *Server) requestActiveChatStop(conversationID, turnID string) (*activeChatRun, bool) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, false
	}
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)

	var cancel context.CancelFunc
	s.activeChatsMu.Lock()
	s.prunePendingChatStopsLocked(time.Now())
	run, ok := s.activeChats[conversationID]
	if ok && run != nil {
		if turnID != "" && run.turnID != turnID {
			s.activeChatsMu.Unlock()
			return nil, false
		}
		if !run.stopRequested {
			run.stopRequested = true
			cancel = run.cancel
		}
	} else if turnID != "" {
		if s.pendingChatStops == nil {
			s.pendingChatStops = make(map[string]time.Time)
		}
		if len(s.pendingChatStops) >= maxPendingChatStops {
			s.removeOldestPendingChatStopLocked()
		}
		s.pendingChatStops[pendingChatStopKey(conversationID, turnID)] = time.Now().Add(pendingChatStopTTL)
		ok = true
	}
	s.activeChatsMu.Unlock()

	if cancel != nil {
		cancel()
	}

	return run, ok
}

func pendingChatStopKey(conversationID, turnID string) string {
	return conversationID + "\x00" + turnID
}

func (s *Server) prunePendingChatStopsLocked(now time.Time) {
	for key, expiresAt := range s.pendingChatStops {
		if !expiresAt.After(now) {
			delete(s.pendingChatStops, key)
		}
	}
}

func (s *Server) removeOldestPendingChatStopLocked() {
	oldestKey := ""
	var oldestExpiry time.Time
	for key, expiresAt := range s.pendingChatStops {
		if oldestKey == "" || expiresAt.Before(oldestExpiry) {
			oldestKey = key
			oldestExpiry = expiresAt
		}
	}
	if oldestKey != "" {
		delete(s.pendingChatStops, oldestKey)
	}
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

func (s *Server) reserveConversationDeletion(conversationID string) bool {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return false
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	if run := s.activeChats[conversationID]; run != nil {
		return false
	}
	if s.deletingConversations == nil {
		s.deletingConversations = make(map[string]struct{})
	}
	if _, exists := s.deletingConversations[conversationID]; exists {
		return false
	}
	s.deletingConversations[conversationID] = struct{}{}
	return true
}

func (s *Server) releaseConversationDeletion(conversationID string) {
	s.activeChatsMu.Lock()
	delete(s.deletingConversations, strings.TrimSpace(conversationID))
	s.activeChatsMu.Unlock()
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
	if _, deleting := s.deletingConversations[conversationID]; deleting {
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

func (s *Server) broadcastChatEvent(conversationID string, event chat.ChatEvent) {
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
	searchTerm := strings.TrimSpace(query.Get("search"))
	req := &conversations.ListConversationsRequest{
		SearchTerm:    searchTerm,
		SearchCWDTerm: expandCompactHomePath(searchTerm),
		CWD:           expandCompactHomePath(query.Get("cwd")),
		RunnerID:      strings.TrimSpace(query.Get("runnerId")),
		SortBy:        query.Get("sortBy"),
		SortOrder:     query.Get("sortOrder"),
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
		summary.CWD = compactHomePath(summary.CWD)
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
		if s.runnerRegistry != nil {
			affinity, ok, affinityErr := s.runnerRegistry.ResolveConversationAffinity(ctx, summary.ID)
			if affinityErr != nil {
				logger.G(ctx).WithError(affinityErr).WithField("conversation_id", summary.ID).Warn("failed to refresh runner affinity")
			} else if ok {
				summary.Metadata["runner_id"] = affinity.RunnerID
				if runner, found := s.runnerRegistry.Runner(affinity.RunnerID); found {
					summary.Metadata["runner_status"] = runner.Status
				}
			}
		}
	}
	for i := range response.CWDs {
		response.CWDs[i] = compactHomePath(response.CWDs[i])
	}

	s.writeJSONResponse(w, response)
}

// WebConversationResponse represents a conversation response for the web UI.
type WebConversationResponse struct {
	ID                    string                 `json:"id"`
	CreatedAt             time.Time              `json:"createdAt"`
	UpdatedAt             time.Time              `json:"updatedAt"`
	Provider              string                 `json:"provider"`
	CWD                   string                 `json:"cwd,omitempty"`
	CWDLocked             bool                   `json:"cwdLocked,omitempty"`
	Profile               string                 `json:"profile,omitempty"`
	ProfileLocked         bool                   `json:"profileLocked,omitempty"`
	ReasoningEffort       string                 `json:"reasoningEffort,omitempty"`
	ReasoningEffortLocked bool                   `json:"reasoningEffortLocked,omitempty"`
	RunnerID              string                 `json:"runnerId,omitempty"`
	EnvironmentProfile    string                 `json:"environmentProfile,omitempty"`
	Runner                *runnerregistry.Runner `json:"runner,omitempty"`
	Summary               string                 `json:"summary,omitempty"`
	IsRunning             bool                   `json:"isRunning,omitempty"`
	Usage                 any                    `json:"usage"`
	Messages              []WebMessage           `json:"messages"`
	PendingSteer          []WebMessage           `json:"pendingSteer,omitempty"`
	ToolResults           any                    `json:"toolResults,omitempty"`
	MessageCount          int                    `json:"messageCount"`
}

type conversationHistoryResponse struct {
	ID                 string                                    `json:"id"`
	UpdatedAt          time.Time                                 `json:"updatedAt"`
	Provider           string                                    `json:"provider"`
	CWD                string                                    `json:"cwd,omitempty"`
	Profile            string                                    `json:"profile,omitempty"`
	ReasoningEffort    string                                    `json:"reasoningEffort,omitempty"`
	RunnerID           string                                    `json:"runnerId,omitempty"`
	EnvironmentProfile string                                    `json:"environmentProfile,omitempty"`
	Summary            string                                    `json:"summary,omitempty"`
	Usage              llmtypes.Usage                            `json:"usage"`
	Entries            []conversations.StreamableMessage         `json:"entries"`
	ToolResults        map[string]tooltypes.StructuredToolResult `json:"toolResults,omitempty"`
}

// ChatProfileOption represents a selectable profile in the web UI.
type ChatProfileOption struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Active bool   `json:"active,omitempty"`
}

// ChatSettingsResponse contains new-conversation settings for the web chat composer.
type ChatSettingsResponse struct {
	CurrentProfile               string              `json:"currentProfile,omitempty"`
	Profiles                     []ChatProfileOption `json:"profiles"`
	ReasoningEffort              string              `json:"reasoningEffort"`
	ReasoningEffortOptions       []string            `json:"reasoningEffortOptions"`
	DefaultCWD                   string              `json:"defaultCWD,omitempty"`
	ControlPlaneWorkspaceEnabled bool                `json:"controlPlaneWorkspaceEnabled"`
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
type WebContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Command  string          `json:"command,omitempty"`
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

func compactHomePath(path string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return strings.TrimSpace(path)
	}
	return compactPathForHome(path, homeDir)
}

func expandCompactHomePath(path string) string {
	path = strings.TrimSpace(path)
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return filepath.Clean(homeDir)
	}

	return filepath.Join(homeDir, filepath.FromSlash(strings.TrimPrefix(path, "~/")))
}

func compactPathForHome(path, homeDir string) string {
	path = strings.TrimSpace(path)
	homeDir = strings.TrimSpace(homeDir)
	if path == "" || homeDir == "" || path == "~" || strings.HasPrefix(path, "~/") {
		return path
	}
	if !filepath.IsAbs(path) || !filepath.IsAbs(homeDir) {
		return path
	}

	cleanPath := filepath.Clean(path)
	cleanHomeDir := filepath.Clean(homeDir)
	pathFromHome, err := filepath.Rel(cleanHomeDir, cleanPath)
	if err != nil {
		return path
	}
	if pathFromHome == "." {
		return "~"
	}
	if pathFromHome == ".." || strings.HasPrefix(pathFromHome, ".."+string(filepath.Separator)) {
		return path
	}

	return "~/" + filepath.ToSlash(pathFromHome)
}

func resolveConversationProfile(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if snapshot, hasSnapshot, err := conversations.ConfigSnapshotFromMetadata(metadata); err == nil && hasSnapshot {
		profile := strings.TrimSpace(snapshot.Profile)
		if profile == "" || strings.EqualFold(profile, "default") {
			return ""
		}
		return profile
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

func resolveConversationReasoningEffort(response *conversations.GetConversationResponse) string {
	if response == nil {
		return ""
	}

	if snapshot, hasSnapshot, err := conversations.ConfigSnapshotFromMetadata(response.Metadata); err == nil && hasSnapshot {
		effort, normalizeErr := llmtypes.NormalizeReasoningEffort(snapshot.ReasoningEffort)
		if normalizeErr == nil {
			return effort
		}
	}

	config, err := chat.ResolveConfigForExistingConversation(response)
	if err != nil {
		return ""
	}
	return config.ReasoningEffort
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
func (s *Server) handleGetChatSettings(w http.ResponseWriter, r *http.Request) {
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profile == "" {
		profile = getCurrentWebUIProfile()
	} else if strings.EqualFold(profile, "default") {
		profile = "default"
	}

	config, err := chat.ResolveConfigForNewConversation(profile)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "failed to resolve chat settings", err)
		return
	}

	controlPlaneWorkspaceEnabled := s.controlPlaneWorkspaceEnabled()
	defaultCWD := ""
	if controlPlaneWorkspaceEnabled {
		defaultCWD, err = s.defaultCWD()
		if err != nil {
			defaultCWD = ""
		}
	}

	s.writeJSONResponse(w, ChatSettingsResponse{
		CurrentProfile:               profile,
		Profiles:                     getWebUIProfileOptions(),
		ReasoningEffort:              config.ReasoningEffort,
		ReasoningEffortOptions:       llmtypes.ReasoningEffortOptions(config),
		DefaultCWD:                   compactHomePath(defaultCWD),
		ControlPlaneWorkspaceEnabled: controlPlaneWorkspaceEnabled,
	})
}

func (s *Server) handleGetSlashCommands(w http.ResponseWriter, r *http.Request) {
	if !s.requireControlPlaneWorkspace(w) {
		return
	}
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
		extensionRuntime, err = s.extensionRuntimes.RuntimeForCommandDiscovery(r.Context(), resolvedCWD)
	} else {
		runtimeManager := extensions.NewRuntimeManager()
		defer func() { _ = runtimeManager.Close() }()
		extensionRuntime, err = runtimeManager.RuntimeForCommandDiscovery(r.Context(), resolvedCWD)
	}
	if err != nil {
		logger.G(r.Context()).WithError(err).Warn("Failed to initialize extensions for slash command discovery")
	} else if extensionRuntime != nil {
		commands = append(commands, extensionRuntime.SlashCommands()...)
	}

	s.writeJSONResponse(w, SlashCommandsResponse{Commands: commands})
}

func (s *Server) handleGetCWDHints(w http.ResponseWriter, r *http.Request) {
	if !s.requireControlPlaneWorkspace(w) {
		return
	}
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
	for i := range hints {
		hints[i].Path = compactHomePath(hints[i].Path)
	}

	s.writeJSONResponse(w, CWDHintsResponse{
		BaseDir: compactHomePath(baseDir),
		Query:   compactHomePath(query),
		Hints:   hints,
	})
}

func (s *Server) defaultCWD() (string, error) {
	if !s.controlPlaneWorkspaceEnabled() {
		return "", errors.New(controlPlaneWorkspaceDisabledMessage)
	}
	configuredCWD := ""
	if s != nil && s.config != nil {
		configuredCWD = s.config.CWD
	}

	return chat.ResolveConfiguredDefaultCWD(configuredCWD)
}

func (s *Server) controlPlaneWorkspaceEnabled() bool {
	return s == nil || s.config == nil || !s.config.DisableControlPlaneWorkspace
}

func (s *Server) requireControlPlaneWorkspace(w http.ResponseWriter) bool {
	if s.controlPlaneWorkspaceEnabled() {
		return true
	}
	s.writeErrorResponse(w, http.StatusForbidden, controlPlaneWorkspaceDisabledMessage, nil)
	return false
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
	if r.URL.Query().Get("format") == "stream" {
		s.writeConversationHistoryResponse(w, r, response)
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

	pendingSteer, err := pendingSteerWebMessages(ctx, id)
	if err != nil {
		logger.G(ctx).WithError(err).WithField("conversation_id", id).Warn("failed to read pending steering messages")
	}

	// Convert to web response format

	webResponse := &WebConversationResponse{
		ID:                    response.ID,
		CreatedAt:             response.CreatedAt,
		UpdatedAt:             response.UpdatedAt,
		Provider:              providerLabel,
		CWD:                   compactHomePath(response.CWD),
		CWDLocked:             response.ID != "" && strings.TrimSpace(response.CWD) != "",
		Profile:               resolveConversationProfile(response.Metadata),
		ProfileLocked:         response.ID != "",
		ReasoningEffort:       resolveConversationReasoningEffort(response),
		ReasoningEffortLocked: response.ID != "",
		Summary:               response.Summary,
		IsRunning:             s.isActiveChat(response.ID),
		Usage:                 response.Usage,
		Messages:              webMessages,
		PendingSteer:          pendingSteer,
		ToolResults:           response.ToolResults,
		MessageCount:          len(webMessages),
	}
	if s.runnerRegistry != nil {
		affinity, ok, affinityErr := s.runnerRegistry.ResolveConversationAffinity(ctx, response.ID)
		if affinityErr != nil {
			logger.G(ctx).WithError(affinityErr).WithField("conversation_id", response.ID).Warn("failed to refresh runner affinity")
		} else if ok {
			webResponse.RunnerID = affinity.RunnerID
			webResponse.EnvironmentProfile = affinity.EnvironmentProfile
			if runner, found := s.runnerRegistry.Runner(affinity.RunnerID); found {
				webResponse.Runner = &runner
			}
		}
	}

	s.writeJSONResponse(w, webResponse)
}

func (s *Server) writeConversationHistoryResponse(w http.ResponseWriter, r *http.Request, response *conversations.GetConversationResponse) {
	entries, err := llm.ExtractConversationEntries(response.Provider, response.RawMessages, response.Metadata, response.ToolResults)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to parse conversation history", err)
		return
	}
	history := conversationHistoryResponse{
		ID:              response.ID,
		UpdatedAt:       response.UpdatedAt,
		Provider:        response.Provider,
		CWD:             response.CWD,
		Profile:         resolveConversationProfile(response.Metadata),
		ReasoningEffort: resolveConversationReasoningEffort(response),
		Summary:         response.Summary,
		Usage:           response.Usage,
		Entries:         entries,
	}
	if s.runnerRegistry != nil {
		affinity, ok, affinityErr := s.runnerRegistry.ResolveConversationAffinity(r.Context(), response.ID)
		if affinityErr != nil {
			s.writeErrorResponse(w, http.StatusInternalServerError, "failed to resolve conversation runner", affinityErr)
			return
		}
		if ok {
			history.RunnerID = affinity.RunnerID
			history.EnvironmentProfile = affinity.EnvironmentProfile
		}
	}
	s.writeJSONResponse(w, history)
}

func pendingSteerWebMessages(ctx context.Context, conversationID string) ([]WebMessage, error) {
	steerStore, err := steer.NewSteerStore(ctx)
	if err != nil {
		return nil, err
	}
	defer steerStore.Close()

	messages, err := steerStore.Peek(ctx, conversationID)
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
					contentBlocks = append(contentBlocks, WebContentBlock{Type: "image", Source: webImageSource(source)})
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
					contentBlocks = append(contentBlocks, WebContentBlock{Type: "image", Source: webImageSource(source)})
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
	if s.conversationService == nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "conversation service is unavailable", nil)
		return
	}
	if _, err := s.conversationService.GetConversation(r.Context(), conversationID); err != nil {
		s.writeErrorResponse(w, http.StatusNotFound, "conversation not found", err)
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
		s.writeErrorResponse(w, http.StatusConflict, "conversation is unavailable", nil)
		return
	}
	defer func() {
		s.removeChatSubscriber(conversationID, subscriber)
		subscriber.Close()
	}()

	if s.isActiveChat(conversationID) {
		_ = sink.Send(chat.ChatEvent{
			Kind:           "conversation",
			ConversationID: conversationID,
			Role:           "assistant",
		})
	} else {
		_ = sink.KeepAlive()
	}
	keepAlive := time.NewTicker(conversationStreamKeepAliveInterval)
	defer keepAlive.Stop()
	var serverDone <-chan struct{}
	if s.runCtx != nil {
		serverDone = s.runCtx.Done()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-serverDone:
			return
		case <-keepAlive.C:
			if err := sink.KeepAlive(); err != nil {
				return
			}
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

	messages, err := pendingSteerWebMessages(r.Context(), conversationID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read pending steering messages", err)
		return
	}

	s.writeJSONResponse(w, messages)
}

type steerConversationRequest struct {
	Message string                  `json:"message"`
	Content []chat.ChatContentBlock `json:"content,omitempty"`
}

type steerConversationResponse struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id"`
	Queued         bool   `json:"queued"`
}

// handleSteerConversation handles POST /api/conversations/{id}/steer
func (s *Server) handleSteerConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	message, imageInputs, err := chat.NormalizeRequest(chat.ChatRequest{
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

	steerStore, err := steer.NewSteerStore(ctx)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to initialize steer store", err)
		return
	}
	defer steerStore.Close()

	queued, err := steerStore.Enqueue(ctx, conversationID, message, imageInputs)
	if err != nil {
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

	run, stopped := s.requestActiveChatStop(conversationID, r.URL.Query().Get("turnId"))
	if run != nil && run.done != nil {
		select {
		case <-run.done:
		case <-r.Context().Done():
			s.writeErrorResponse(w, http.StatusRequestTimeout, "timed out waiting for conversation to stop", r.Context().Err())
			return
		}
	}
	s.writeJSONResponse(w, stopConversationResponse{
		Success:        true,
		ConversationID: conversationID,
		Stopped:        stopped,
	})
}

type uiInputResponseRequest struct {
	Status    string `json:"status"`
	Value     string `json:"value,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Reason    string `json:"reason,omitempty"`
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
	case extensions.UIInputStatusSubmitted,
		extensions.UIInputStatusDismissed,
		extensions.UIInputStatusTimeout,
		extensions.UIInputStatusUnavailable:
	default:
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid ui input status", nil)
		return
	}

	response := extensions.UIInputResponse{Status: status, Value: req.Value, Confirmed: req.Confirmed, Reason: req.Reason}
	if !response.Confirmed && strings.EqualFold(strings.TrimSpace(req.Value), "true") {
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
		if errors.Is(err, conversationtypes.ErrConversationNotFound) {
			s.writeErrorResponse(w, http.StatusNotFound, "conversation not found", err)
			return
		}
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

	if !s.reserveConversationDeletion(id) {
		s.writeErrorResponse(w, http.StatusConflict, "conversation is actively running", nil)
		return
	}
	defer s.releaseConversationDeletion(id)

	// Delete conversation
	err := s.conversationService.DeleteConversation(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to delete conversation", err)
		return
	}
	if s.runnerRegistry != nil {
		s.runnerRegistry.ForgetConversation(id)
	}
	if closer, ok := s.chatRunner.(interface{ CloseConversation(string) error }); ok {
		if err := closer.CloseConversation(id); err != nil {
			logger.G(ctx).WithError(err).WithField("conversation_id", id).Warn("failed to close cached conversation thread")
		}
	}
	s.closeChatSubscribers(id)

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

	return s.shutdownHTTPServer()
}

func (s *Server) shutdownHTTPServer() error {
	if s.runCancel != nil {
		s.runCancel()
	}
	if s.server == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.httpShutdownTimeout())
	defer cancel()
	return s.server.Shutdown(shutdownCtx)
}

// Stop stops the web server
func (s *Server) Stop() error {
	var firstErr error
	if err := s.shutdownHTTPServer(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return &httpShutdownError{err: err}
	}

	s.terminalSessionsMu.Lock()
	terminalSessions := s.terminalSessions
	s.terminalSessionsMu.Unlock()
	if terminalSessions != nil {
		terminalSessions.Close()
	}
	if closer, ok := s.chatRunner.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.extensionRuntimes != nil {
		if err := s.extensionRuntimes.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.runnerRegistry != nil {
		if err := s.runnerRegistry.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.authStore != nil {
		if err := s.authStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) httpShutdownTimeout() time.Duration {
	if s != nil && s.shutdownTimeout > 0 {
		return s.shutdownTimeout
	}
	return defaultHTTPShutdownTimeout
}

func normalizeWebContent(textParts []string, blocks []WebContentBlock) any {
	if len(blocks) == 0 {
		return strings.Join(textParts, "\n")
	}
	return blocks
}

func webImageSource(source *chat.ChatImageSource) *WebImageSource {
	if source == nil {
		return nil
	}
	return &WebImageSource{
		Data:      source.Data,
		MediaType: source.MediaType,
	}
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
	var firstErr error
	if err := s.Stop(); err != nil {
		firstErr = err
		var shutdownErr *httpShutdownError
		if errors.As(err, &shutdownErr) {
			return firstErr
		}
	}
	if s.conversationService != nil {
		if err := s.conversationService.Close(); err != nil && firstErr == nil {
			firstErr = errors.Wrap(err, "failed to close conversation service")
		}
	}
	return firstErr
}
