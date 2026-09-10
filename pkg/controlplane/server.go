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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/artifacts"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/llm"
	openairesponses "github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
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
	remoteTerminals       map[string]int
	remoteTerminalsMu     sync.Mutex
	extensionUI           *webExtensionUIHost
	runnerRegistry        *runnerregistry.Registry
	sessionExtensions     map[string]*sessionExtensionAttachment
	sessionExtensionsMu   sync.Mutex
	extensionProfiles     map[extensionProfileKey]registeredExtensionProfile
	extensionProfilesMu   sync.RWMutex
	turns                 *turnStore
	artifacts             *artifacts.Store
	artifactUploads       artifactUploadManager
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
	copilotAuth           copilotProviderAuthService
	copilotDeviceLogin    *copilotDeviceLoginSession
	copilotDeviceLoginMu  sync.Mutex
	anthropicAuth         anthropicProviderAuthService
	anthropicOAuthLogin   *anthropicOAuthLoginSession
	anthropicOAuthLoginMu sync.Mutex
	shutdownTimeout       time.Duration
	stopping              bool
	embeddedMu            sync.Mutex
	embeddedStatus        EmbeddedRunnerStatus
}

type activeChatRun struct {
	cancel        context.CancelFunc
	done          chan struct{}
	doneOnce      sync.Once
	turnID        string
	stopRequested bool
	uiInput       *webUIInputBroker
	eventSink     chat.ChatEventSink
}

const (
	pendingChatStopTTL                  = 30 * time.Second
	maxPendingChatStops                 = 1024
	conversationStreamKeepAliveInterval = 15 * time.Second
	publicAuthRateWindow                = time.Minute
	maxPublicAuthRateEntries            = 4096
	maxOIDCLoginRequestsPerWindow       = 60
	maxEnrollmentStartsPerWindow        = 30
	maxEnrollmentPollsPerWindow         = 8192
	maxUserLoginStartsPerWindow         = 30
	maxUserLoginPollsPerWindow          = 8192
	defaultHTTPShutdownTimeout          = 30 * time.Second
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
	PublicBaseURL   string
	Host            string
	Port            int
	CWD             string // Deprecated: rejected; configure the runner workspace instead.
	CompactRatio    float64
	AuthToken       string
	RunnerAuthToken string
	WebAuthMode     WebAuthMode
	RunnerAuthMode  RunnerAuthMode
	OIDC            OIDCConfig
	CORSOrigins     []string
	EmbeddedRunner  *EmbeddedRunnerConfig
	InstanceID      string             // Identity of this process for local discovery.
	LocalShutdown   context.CancelFunc // Set only by managed loopback server hosts.
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
	if c.Port < 0 || c.Port > 65535 {
		return errors.Errorf("port must be between 0 and 65535, got %d", c.Port)
	}

	if c.CompactRatio <= 0.0 || c.CompactRatio > 1.0 {
		return errors.New("compact-ratio must be greater than 0.0 and less than or equal to 1.0")
	}
	if c.CWD != "" {
		return errors.New("serve --cwd is no longer supported; use --runner-workspace to set the default working directory")
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
	publicURL, err := NormalizePublicBaseURL(c.PublicBaseURL)
	if err != nil {
		return err
	}
	c.PublicBaseURL = publicURL

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

	runCtx, runCancel := context.WithCancel(context.WithoutCancel(ctx))
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
			return nil, errors.Wrap(err, "failed to open server authentication store")
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
	// Registry.Close must persist interrupted runs and offline registrations
	// after execution cancellation; its own close controls its context lifetime.
	runnerRegistry, err := runnerregistry.New(context.WithoutCancel(runCtx), runnerregistry.Options{Persistence: runnerPersistence, Credentials: authenticationStore})
	if err != nil {
		runCancel()
		_ = authenticationStore.Close()
		_ = conversationService.Close()
		return nil, errors.Wrap(err, "failed to create runner registry")
	}

	turns, err := newTurnStore(runCtx, dbPath)
	if err != nil {
		runCancel()
		_ = runnerRegistry.Close()
		_ = authenticationStore.Close()
		_ = conversationService.Close()
		return nil, err
	}
	artifactStore, err := artifacts.Open(runCtx, dbPath)
	if err != nil {
		runCancel()
		_ = turns.db.Close()
		_ = runnerRegistry.Close()
		_ = authenticationStore.Close()
		_ = conversationService.Close()
		return nil, errors.Wrap(err, "failed to open image artifact storage")
	}
	s := &Server{
		router:              mux.NewRouter(),
		conversationService: conversationService,
		chatRunner: &serverChatRunner{
			runner: chat.NewExecutor("", nil),
		},
		config:                config,
		frontendHandler:       frontendHandler,
		runCtx:                runCtx,
		runCancel:             runCancel,
		runnerRegistry:        runnerRegistry,
		turns:                 turns,
		artifacts:             artifactStore,
		authStore:             authenticationStore,
		oidcFlow:              oidcFlow,
		activeChats:           make(map[string]*activeChatRun),
		pendingChatStops:      make(map[string]time.Time),
		deletingConversations: make(map[string]struct{}),
		chatSubscribers:       make(map[string]map[*subscriberEventSink]struct{}),
	}
	s.extensionUI = newWebExtensionUIHost(s.emitExtensionUIEvent)
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
	s.router.HandleFunc("/i/{shortCode}", s.requireRole(RoleUser, s.handleImageLink)).Methods("GET", "HEAD")
	s.router.HandleFunc(runnerpayload.ArtifactUploadPath, s.handleArtifactUpload).Methods("PUT")
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
	api.HandleFunc("/status", s.handleStatus).Methods("GET")
	if s.config != nil && s.config.LocalShutdown != nil {
		api.HandleFunc("/server/stop", s.requireRole(RoleAdmin, s.handleLocalServerStop)).Methods("POST")
	}
	api.HandleFunc("/auth/me", s.handleAuthMe).Methods("GET")
	api.HandleFunc("/auth/v1/device/context", s.handleUserLoginContext).Methods("GET")
	api.HandleFunc("/auth/v1/device/decision", s.handleUserLoginDecision).Methods("POST")
	api.HandleFunc("/providers/codex/status", s.requireRole(RoleAdmin, s.handleCodexStatus)).Methods("GET")
	api.HandleFunc("/providers/codex", s.requireRole(RoleAdmin, s.handleGetCodexProvider)).Methods("GET")
	api.HandleFunc("/providers/codex/device-login", s.requireRole(RoleAdmin, s.handleStartCodexDeviceLogin)).Methods("POST")
	api.HandleFunc("/providers/codex/device-login/{id}", s.requireRole(RoleAdmin, s.handleGetCodexDeviceLogin)).Methods("GET")
	api.HandleFunc("/providers/codex/device-login/{id}", s.requireRole(RoleAdmin, s.handleCancelCodexDeviceLogin)).Methods("DELETE")
	api.HandleFunc("/providers/copilot", s.requireRole(RoleAdmin, s.handleGetCopilotProvider)).Methods("GET")
	api.HandleFunc("/providers/copilot/device-login", s.requireRole(RoleAdmin, s.handleStartCopilotDeviceLogin)).Methods("POST")
	api.HandleFunc("/providers/copilot/device-login/{id}", s.requireRole(RoleAdmin, s.handleGetCopilotDeviceLogin)).Methods("GET")
	api.HandleFunc("/providers/copilot/device-login/{id}", s.requireRole(RoleAdmin, s.handleCancelCopilotDeviceLogin)).Methods("DELETE")
	api.HandleFunc("/providers/anthropic", s.requireRole(RoleAdmin, s.handleGetAnthropicProvider)).Methods("GET")
	api.HandleFunc("/providers/anthropic/accounts", s.requireRole(RoleAdmin, s.handleAnthropicAccounts)).Methods("GET", "POST")
	api.HandleFunc("/providers/anthropic/accounts/usage", s.requireRole(RoleAdmin, s.handleAnthropicAccountUsage)).Methods("POST")
	api.HandleFunc("/providers/anthropic/oauth-login", s.requireRole(RoleAdmin, s.handleStartAnthropicOAuthLogin)).Methods("POST")
	api.HandleFunc("/providers/anthropic/oauth-login/{id}/complete", s.requireRole(RoleAdmin, s.handleCompleteAnthropicOAuthLogin)).Methods("POST")
	api.HandleFunc("/providers/anthropic/oauth-login/{id}", s.requireRole(RoleAdmin, s.handleCancelAnthropicOAuthLogin)).Methods("DELETE")
	api.HandleFunc("/runner/v1/enrollment/context", s.requireRole(RoleRunnerAdmin, s.handleRunnerEnrollmentContext)).Methods("GET")
	api.HandleFunc("/runner/v1/enrollment/decision", s.requireRole(RoleRunnerAdmin, s.handleRunnerEnrollmentDecision)).Methods("POST")
	api.HandleFunc("/chat/workspace-inspection", s.requireRole(RoleUser, s.handleWorkspaceInspection)).Methods("POST")
	api.HandleFunc("/chat/message-history", s.requireRole(RoleUser, s.handleMessageHistory)).Methods("GET", "POST")
	api.HandleFunc("/chat/settings", s.handleGetChatSettings).Methods("GET")
	api.HandleFunc("/chat/slash-commands", s.handleGetSlashCommands).Methods("GET")
	api.HandleFunc("/chat/shortcuts", s.requireRole(RoleUser, s.handleWorkspaceShortcut)).Methods("POST")
	api.HandleFunc("/chat/cwd-suggestions", s.handleGetCWDHints).Methods("GET")
	api.HandleFunc("/git/diff", s.handleGetGitDiff).Methods("GET")
	api.HandleFunc("/git/commit", s.requireRole(RoleUser, s.handleWorkspaceCommit)).Methods("GET", "POST")
	api.HandleFunc("/terminal/ws", s.requireRole(RoleTerminal, s.handleTerminalWebsocket)).Methods("GET")
	api.HandleFunc("/runner/v1/connect", s.handleRunnerWebsocket).Methods("GET")
	api.HandleFunc("/session/extensions", s.requireRole(RoleUser, s.handleSessionExtensionsWebsocket)).Methods("GET")
	api.HandleFunc("/runners", s.requireRole(RoleUser, s.handleListRunners)).Methods("GET")
	api.HandleFunc("/runners/{id}", s.requireRole(RoleRunnerAdmin, s.handleGetRunner)).Methods("GET")
	api.HandleFunc("/runners/{id}", s.requireRole(RoleRunnerAdmin, s.handleDeleteRunner)).Methods("DELETE")
	api.HandleFunc("/conversations", s.handleListConversations).Methods("GET")
	api.HandleFunc("/conversations/{id}", s.handleGetConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/stream", s.handleStreamConversation).Methods("GET")
	api.HandleFunc("/conversations/{id}/fork", s.handleForkConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/move", s.handleMoveConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/steer", s.handleGetPendingSteer).Methods("GET")
	api.HandleFunc("/conversations/{id}/steer", s.handleSteerConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/stop", s.handleStopConversation).Methods("POST")
	api.HandleFunc("/conversations/{id}/turns/{turnId}", s.handleGetTurnReceipt).Methods("GET")
	api.HandleFunc("/conversations/{id}/ui-input/{requestId}", s.handleRespondUIInput).Methods("POST")
	api.HandleFunc("/conversations/{id}/ui-persistent/ack", s.handlePersistentUIAck).Methods("POST")
	api.HandleFunc("/conversations/{id}/ui-persistent/input", s.handlePersistentUIInput).Methods("POST")
	api.HandleFunc("/conversations/{id}/tools/{toolCallId}", s.handleGetToolResult).Methods("GET")
	api.HandleFunc(
		"/conversations/{id}/artifacts/{artifactId}",
		s.requireRole(RoleUser, s.handleConversationArtifact),
	).Methods("GET", "HEAD")
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
	forwardedProto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto"))
	return r.TLS != nil || strings.EqualFold(forwardedProto, "https") || strings.EqualFold(forwardedProto, "wss")
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

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
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
	if principal, ok := principalFromContext(requestCtx); ok {
		baseCtx = contextWithPrincipal(baseCtx, principal)
	}

	return logger.WithLogger(baseCtx, logger.G(requestCtx))
}

func (s *Server) registerActiveChat(conversationID string, run *activeChatRun) bool {
	if strings.TrimSpace(conversationID) == "" || run == nil || run.cancel == nil {
		return false
	}

	s.activeChatsMu.Lock()
	defer s.activeChatsMu.Unlock()
	if s.stopping {
		return false
	}
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

func (s *Server) registerChatSubscriber(conversationID string, sink *subscriberEventSink) (bool, bool) {
	if strings.TrimSpace(conversationID) == "" || sink == nil {
		return false, false
	}

	s.activeChatsMu.Lock()
	if _, deleting := s.deletingConversations[conversationID]; deleting {
		s.activeChatsMu.Unlock()
		return false, false
	}
	active := s.activeChats[conversationID] != nil

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

	return active, true
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

func (s *Server) emitExtensionUIEvent(conversationID string, event chat.ChatEvent) {
	if strings.TrimSpace(conversationID) == "" {
		return
	}

	var primary chat.ChatEventSink
	s.activeChatsMu.Lock()
	if run := s.activeChats[conversationID]; run != nil && !run.stopRequested {
		primary = run.eventSink
	}
	s.activeChatsMu.Unlock()

	if primary != nil {
		if err := primary.Send(event); err == nil {
			s.broadcastChatEvent(conversationID, event)
			return
		}
	}
	s.broadcastChatEvent(conversationID, event)
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
		SearchTerm: searchTerm,
		Provider:   strings.ToLower(strings.TrimSpace(query.Get("provider"))),
		CWD:        strings.TrimSpace(query.Get("cwd")),
		RunnerID:   strings.TrimSpace(query.Get("runnerId")),
		SortBy:     query.Get("sortBy"),
		SortOrder:  query.Get("sortOrder"),
	}

	// All clients receive canonical persisted runner paths, never daemon-home
	// expansion or shortening of paths that may belong to another host.
	raw := query.Get("format") == "raw"
	for name, target := range map[string]*int{"limit": &req.Limit, "offset": &req.Offset} {
		if value := query.Get(name); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				s.writeErrorResponse(w, http.StatusBadRequest, name+" must be a nonnegative integer", nil)
				return
			}
			*target = parsed
		}
	}
	for name, target := range map[string]**time.Time{"startDate": &req.StartDate, "endDate": &req.EndDate} {
		if value := query.Get(name); value != "" {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				parsed, err = time.Parse("2006-01-02", value)
				if err == nil && name == "endDate" {
					parsed = parsed.Add(24*time.Hour - time.Nanosecond)
				}
			}
			if err != nil {
				s.writeErrorResponse(w, http.StatusBadRequest, name+" must be YYYY-MM-DD or RFC3339", nil)
				return
			}
			*target = &parsed
		}
	}
	if req.StartDate != nil && req.EndDate != nil && req.StartDate.After(*req.EndDate) {
		s.writeErrorResponse(w, http.StatusBadRequest, "startDate must not be after endDate", nil)
		return
	}
	switch req.SortBy {
	case "", "updated", "updated_at", "updatedAt":
		req.SortBy = "updatedAt"
	case "created", "created_at", "createdAt":
		req.SortBy = "createdAt"
	case "messages", "messageCount":
		req.SortBy = "messageCount"
	default:
		s.writeErrorResponse(w, http.StatusBadRequest, "unsupported conversation sort field", nil)
		return
	}
	if req.SortOrder != "" && req.SortOrder != "asc" && req.SortOrder != "desc" {
		s.writeErrorResponse(w, http.StatusBadRequest, "sortOrder must be asc or desc", nil)
		return
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
		if !raw {
			summary.Provider = displayProviderName(summary.Provider)
		}
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
				logger.G(ctx).WithError(affinityErr).WithField("conversation_id", summary.ID).Warn("failed to refresh the conversation's runner assignment")
			} else if ok {
				summary.Metadata["runner_id"] = affinity.RunnerID
				if runner, found := s.runnerRegistry.Runner(affinity.RunnerID); found {
					summary.Metadata["runner_status"] = runner.Status
				}
			}
		}
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
	Hidden bool   `json:"hidden,omitempty"`
}

// ChatSettingsResponse contains new-conversation settings for the web chat composer.
type ChatSettingsResponse struct {
	CurrentProfile         string              `json:"currentProfile,omitempty"`
	Profiles               []ChatProfileOption `json:"profiles"`
	ReasoningEffort        string              `json:"reasoningEffort"`
	ReasoningEffortOptions []string            `json:"reasoningEffortOptions"`
	DefaultCWD             string              `json:"defaultCWD,omitempty"`
	DefaultRunnerID        string              `json:"defaultRunnerId,omitempty"`
	DefaultRunnerReady     bool                `json:"defaultRunnerReady"`
	DefaultRunnerHostID    string              `json:"defaultRunnerHostId,omitempty"`
}

const (
	webUIBuiltInProfileScope       = "built-in"
	webUIRepoProfileScope          = "repo"
	webUIGlobalProfileScope        = "global"
	webUIOverrideProfileScope      = "override"
	webUIRepoOverridesProfileScope = "repo (overrides global)"
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

func getWebUIProfileOptions() []ChatProfileOption {
	profileSources := llm.ProfileSources()
	activeProfile := strings.TrimSpace(viper.GetString("profile"))
	if strings.EqualFold(activeProfile, "default") {
		activeProfile = ""
	}

	profiles := []ChatProfileOption{{
		Name:   "default",
		Scope:  webUIBuiltInProfileScope,
		Active: activeProfile == "",
	}}

	names := make([]string, 0, len(profileSources))
	for name := range profileSources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if llm.IsProfileHidden(name) {
			continue
		}
		source := profileSources[name]
		scope := webUIRepoProfileScope
		switch source {
		case llm.ProfileSourceRepoOverridesGlobal:
			scope = webUIRepoOverridesProfileScope
		case llm.ProfileSourceGlobal:
			scope = webUIGlobalProfileScope
		case llm.ProfileSourceOverride:
			scope = webUIOverrideProfileScope
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

	runnerID := strings.TrimSpace(r.URL.Query().Get("runnerId"))
	if runnerID == "" {
		runnerID = s.EmbeddedRunnerStatus().RunnerID
	}
	config, err := s.resolveModelProfile(r.Context(), runnerID, profile, "")
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "failed to resolve chat settings", err)
		return
	}

	status := s.EmbeddedRunnerStatus()
	defaultCWD, hostID := "", ""
	if status.Ready && s.runnerRegistry != nil {
		if runner, found := s.runnerRegistry.Runner(status.RunnerID); found && runner.Connected {
			defaultCWD, hostID = runner.Workspace.Path, runner.Host.InstanceID
		}
	}

	s.writeJSONResponse(w, ChatSettingsResponse{
		CurrentProfile:         profile,
		Profiles:               s.modelProfileOptions(r.Context(), runnerID, profile, r.URL.Query().Get("includeHidden") == "true"),
		ReasoningEffort:        config.ReasoningEffort,
		ReasoningEffortOptions: llmtypes.ReasoningEffortOptions(config),
		DefaultCWD:             defaultCWD,
		DefaultRunnerID:        status.RunnerID,
		DefaultRunnerReady:     status.Ready && hostID != "",
		DefaultRunnerHostID:    hostID,
	})
}

func (s *Server) handleGetSlashCommands(w http.ResponseWriter, r *http.Request) {
	s.handleRunnerDiscovery(w, r, protocol.MethodWorkspaceDiscover)
}

func (s *Server) handleGetCWDHints(w http.ResponseWriter, r *http.Request) {
	s.handleRunnerDiscovery(w, r, protocol.MethodWorkspaceCWDHints)
}

// handleGetConversation handles GET /api/conversations/{id}
func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	id := vars["id"]

	// Get conversation
	response, err := s.conversationService.GetConversation(ctx, id)
	if err != nil {
		if errors.Is(err, conversationtypes.ErrConversationNotFound) {
			s.writeErrorResponse(w, http.StatusNotFound, "conversation not found", err)
			return
		}
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to get conversation", err)
		return
	}
	for toolCallID, result := range response.ToolResults {
		response.ToolResults[toolCallID] = s.decorateImageAttachments(result)
	}
	if r.URL.Query().Get("format") == "raw" {
		s.writeJSONResponse(w, conversationtypes.ConversationRecord{
			ID: response.ID, CWD: response.CWD, Provider: response.Provider,
			CreatedAt: response.CreatedAt, UpdatedAt: response.UpdatedAt,
			RawMessages: response.RawMessages, Summary: response.Summary,
			Usage: response.Usage, Metadata: response.Metadata, ToolResults: response.ToolResults,
		})
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
		CWD:                   response.CWD,
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
			logger.G(ctx).WithError(affinityErr).WithField("conversation_id", response.ID).Warn("failed to refresh the conversation's runner assignment")
		} else if ok {
			webResponse.RunnerID = affinity.RunnerID
			// Runner paths must not be shortened using the daemon host's home directory.
			webResponse.CWD = response.CWD
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

	response.Result = s.decorateImageAttachments(response.Result)
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
	defer sink.Close()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	subscriber := newSubscriberEventSink()
	active, registered := s.registerChatSubscriber(conversationID, subscriber)
	if !registered {
		subscriber.Close()
		s.writeErrorResponse(w, http.StatusConflict, "conversation is unavailable", nil)
		return
	}
	defer func() {
		s.removeChatSubscriber(conversationID, subscriber)
		subscriber.Close()
	}()

	w.Header().Set(chat.ConversationStreamActiveHeader, strconv.FormatBool(active))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if active {
		_ = sink.Send(chat.ChatEvent{
			Kind:           "conversation",
			ConversationID: conversationID,
			Role:           "assistant",
		})
	}
	if s.extensionUI != nil {
		revision, widgets := s.extensionUI.Snapshot(conversationID)
		_ = sink.Send(chat.ChatEvent{
			Kind:             "ui-widgets",
			ConversationID:   conversationID,
			UIWidgets:        widgets,
			UIWidgetRevision: revision,
		})
	} else if !active {
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

	_, err = steerStore.Enqueue(ctx, conversationID, message, imageInputs)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to queue steering message", err)
		return
	}

	s.writeJSONResponse(w, steerConversationResponse{
		Success:        true,
		ConversationID: conversationID,
		Queued:         true,
	})
}

// handleStopConversation handles POST /api/conversations/{id}/stop
func (s *Server) handleStopConversation(w http.ResponseWriter, r *http.Request) {
	if s.handleDurableTurnStop(w, r) {
		return
	}
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

	clientID := strings.TrimSpace(r.Header.Get(chat.ClientIDHeader))
	if !validUIClientID(clientID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "a valid X-Kodelet-Client-ID header is required", nil)
		return
	}
	broker := s.uiInputBrokerForRun(conversationID)
	if broker == nil || !broker.respondOwned(clientID, requestID, response) {
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
	if s.extensionUI != nil {
		s.extensionUI.RemoveConversation(id)
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
	listener, err := net.Listen("tcp", net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port)))
	if err != nil {
		return errors.Wrap(err, "could not listen on the configured server address; check --host and --port")
	}
	presenter.Info(fmt.Sprintf("Starting Kodelet server on http://%s", listener.Addr()))
	return s.Serve(ctx, listener)
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

	if closer, ok := s.chatRunner.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
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
	if s.turns != nil {
		if err := s.turns.db.Close(); err != nil && firstErr == nil {
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
	if s.artifacts != nil {
		if err := s.artifacts.Close(); err != nil && firstErr == nil {
			firstErr = errors.Wrap(err, "failed to close image artifact storage")
		}
	}
	return firstErr
}
