// Package session manages ACP session lifecycle, wrapping kodelet threads
// with ACP session semantics.
package session

import (
	"context"
	"errors"
	"sync"

	"github.com/hashicorp/go-multierror"
	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/bridge"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/mcp"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	pkgerrors "github.com/pkg/errors"
	"github.com/spf13/viper"
)

// ErrNoMCPServers is returned when there are no MCP servers to connect to
var ErrNoMCPServers = errors.New("no MCP servers provided")

var setupMCPExecutionMode = mcp.SetupExecutionMode

// Session represents an ACP session wrapping a kodelet thread
type Session struct {
	ID         acptypes.SessionID
	Thread     llmtypes.Thread
	State      *tools.BasicState
	MCPManager *tools.MCPManager
	CWD        string
	MCPServers []acptypes.MCPServer

	maxTurns           int
	compactRatio       float64
	disableAutoCompact bool

	mu         sync.Mutex
	cancelFunc context.CancelFunc
	cancelled  bool
}

// Cancel cancels the current prompt execution
func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}

// IsCancelled returns whether the session has been cancelled
func (s *Session) IsCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

// Close releases session-scoped resources.
func (s *Session) Close(ctx context.Context) error {
	s.Cancel()
	if s.MCPManager != nil {
		return s.MCPManager.Close(ctx)
	}
	return nil
}

// UpdateSender interface for sending session updates
type UpdateSender interface {
	SendUpdate(sessionID acptypes.SessionID, update any) error
}

// HookConfig mirrors fragments.HookConfig to avoid circular import
type HookConfig struct {
	Handler string
	Once    bool
}

// HandlePrompt processes a prompt and returns the stop reason
// The fragmentHooks parameter contains hook configurations from a recipe/fragment (if any)
func (s *Session) HandlePrompt(ctx context.Context, prompt []acptypes.ContentBlock, sender UpdateSender, fragmentHooks map[string]HookConfig) (acptypes.StopReason, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancelFunc = cancel
	s.cancelled = false
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.cancelFunc = nil
		s.mu.Unlock()
	}()

	message, images := bridge.ContentBlocksToMessage(prompt)

	handler := bridge.NewACPMessageHandler(sender, s.ID)

	// Set recipe hooks if provided
	if len(fragmentHooks) > 0 {
		llmHooks := make(map[string]llmtypes.HookConfig, len(fragmentHooks))
		for k, v := range fragmentHooks {
			llmHooks[k] = llmtypes.HookConfig{
				Handler: v.Handler,
				Once:    v.Once,
			}
		}
		s.Thread.SetRecipeHooks(llmHooks)
	}

	_, err := s.Thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
		PromptCache:        true,
		Images:             images,
		MaxTurns:           s.maxTurns,
		CompactRatio:       s.compactRatio,
		DisableAutoCompact: s.disableAutoCompact,
	})

	// Clear recipe hooks after the prompt is processed
	if len(fragmentHooks) > 0 {
		s.Thread.SetRecipeHooks(nil)
	}

	if s.IsCancelled() {
		return acptypes.StopReasonCancelled, nil
	}

	if err != nil {
		return acptypes.StopReasonEndTurn, err
	}

	return acptypes.StopReasonEndTurn, nil
}

// ManagerConfig holds configuration for the session Manager.
type ManagerConfig struct {
	Provider             string
	Model                string
	MaxTokens            int
	NoSkills             bool
	NoWorkflows          bool
	DisableFSSearchTools bool
	DisableSubagent      bool
	NoHooks              bool
	MaxTurns             int
	CompactRatio         float64
	DisableAutoCompact   bool
}

// Manager manages ACP sessions
type Manager struct {
	config ManagerConfig

	sessions map[acptypes.SessionID]*Session
	store    conversations.ConversationStore
	mu       sync.RWMutex

	kodeletMCPManager *tools.MCPManager
	mcpInitOnce       sync.Once
}

// NewManager creates a new session manager
func NewManager(cfg ManagerConfig) *Manager {
	ctx := context.Background()
	store, _ := conversations.GetConversationStore(ctx)

	return &Manager{
		config:   cfg,
		sessions: make(map[acptypes.SessionID]*Session),
		store:    store,
	}
}

// initMCP initializes kodelet's configured MCP servers (once)
func (m *Manager) initMCP(ctx context.Context) {
	m.mcpInitOnce.Do(func() {
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			if !errors.Is(err, tools.ErrMCPDisabled) {
				logger.G(ctx).WithError(err).Debug("No configured MCP servers")
			}
			return
		}

		m.kodeletMCPManager = mcpManager
	})
}

func (m *Manager) buildLLMConfig(projectDir string) llmtypes.Config {
	config, _ := llm.GetConfigFromViper()

	if m.config.Provider != "" {
		config.Provider = m.config.Provider
	}
	if m.config.Model != "" {
		config.Model = m.config.Model
	}
	if m.config.MaxTokens > 0 {
		config.MaxTokens = m.config.MaxTokens
	}
	config.NoHooks = m.config.NoHooks
	config.DisableFSSearchTools = m.config.DisableFSSearchTools
	config.DisableSubagent = m.config.DisableSubagent
	config.WorkingDirectory = projectDir

	if m.config.NoSkills {
		if config.Skills == nil {
			config.Skills = &llmtypes.SkillsConfig{}
		}
		config.Skills.Enabled = false
	}

	executionMode := viper.GetString("mcp.execution_mode")
	workspaceDir, err := mcp.ResolveWorkspaceDir(projectDir)
	if err != nil {
		logger.G(context.Background()).WithError(err).Warn("failed to resolve MCP workspace directory, using empty workspace")
		workspaceDir = ""
	}
	config.MCPExecutionMode = executionMode
	config.MCPWorkspaceDir = workspaceDir

	return config
}

// convertMCPServers converts ACP MCP server definitions to kodelet's MCPConfig
func convertMCPServers(servers []acptypes.MCPServer) tools.MCPConfig {
	config := tools.MCPConfig{
		Servers: make(map[string]tools.MCPServerConfig),
	}

	for _, server := range servers {
		serverConfig := tools.MCPServerConfig{
			Command: server.Command,
			Args:    server.Args,
			Envs:    server.Env,
		}

		switch server.Type {
		case "stdio", "":
			serverConfig.ServerType = tools.MCPServerTypeStdio
		case "sse":
			serverConfig.ServerType = tools.MCPServerTypeSSE
			serverConfig.BaseURL = server.URL
			if server.AuthHeader != "" {
				serverConfig.Headers = map[string]string{
					"Authorization": server.AuthHeader,
				}
			}
		case "http", "streamable_http", "streamable-http":
			serverConfig.ServerType = tools.MCPServerTypeHTTP
			serverConfig.BaseURL = server.URL
			if server.AuthHeader != "" {
				serverConfig.Headers = map[string]string{
					"Authorization": server.AuthHeader,
				}
			}
		}

		config.Servers[server.Name] = serverConfig
	}

	return config
}

// connectMCPServers creates an MCPManager from ACP MCP server definitions
func (m *Manager) connectMCPServers(ctx context.Context, servers []acptypes.MCPServer) (*tools.MCPManager, error) {
	if len(servers) == 0 {
		return nil, ErrNoMCPServers
	}

	config := convertMCPServers(servers)

	manager, err := tools.NewMCPManager(config)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create MCP manager")
	}

	if err := manager.Initialize(ctx); err != nil {
		_ = manager.Close(ctx)
		return nil, pkgerrors.Wrap(err, "failed to initialize MCP servers")
	}

	return manager, nil
}

func (m *Manager) buildSessionMCPManager(ctx context.Context, servers []acptypes.MCPServer) *tools.MCPManager {
	switch {
	case m.kodeletMCPManager == nil && len(servers) == 0:
		return nil
	case m.kodeletMCPManager == nil:
		clientMCPManager, err := m.connectMCPServers(ctx, servers)
		if err != nil {
			if !errors.Is(err, ErrNoMCPServers) {
				logger.G(ctx).WithError(err).Warn("Failed to connect to client MCP servers")
			}
			return nil
		}
		return clientMCPManager
	case len(servers) == 0:
		return m.kodeletMCPManager.Clone()
	}

	clientMCPManager, err := m.connectMCPServers(ctx, servers)
	if err != nil {
		if !errors.Is(err, ErrNoMCPServers) {
			logger.G(ctx).WithError(err).Warn("Failed to connect to client MCP servers")
		}
		return m.kodeletMCPManager.Clone()
	}

	combinedManager := m.kodeletMCPManager.Clone()
	combinedManager.Merge(clientMCPManager)
	return combinedManager
}

// buildSessionMCPStateOpts returns session-scoped MCP state options.
func (m *Manager) buildSessionMCPStateOpts(ctx context.Context, sessionID string, projectDir string, sessionMCPManager *tools.MCPManager) []tools.BasicStateOption {
	if sessionMCPManager == nil {
		return nil
	}

	mcpSetup, err := setupMCPExecutionMode(ctx, sessionMCPManager, sessionID, projectDir)
	if err != nil && !errors.Is(err, mcp.ErrDirectMode) {
		logger.G(ctx).WithError(err).Warn("Failed to set up MCP execution mode for ACP session")
		return []tools.BasicStateOption{tools.WithMCPTools(sessionMCPManager)}
	}

	if err == nil && mcpSetup != nil {
		logger.G(ctx).WithField("session_id", sessionID).Info("MCP code execution mode initialized for ACP session")
		return mcpSetup.StateOpts
	}

	return []tools.BasicStateOption{tools.WithMCPTools(sessionMCPManager)}
}

func (m *Manager) storeSession(ctx context.Context, session *Session) {
	var previous *Session

	m.mu.Lock()
	previous = m.sessions[session.ID]
	m.sessions[session.ID] = session
	m.mu.Unlock()

	if previous != nil {
		if err := previous.Close(ctx); err != nil {
			logger.G(ctx).WithField("session_id", session.ID).WithError(err).Warn("Failed to close replaced ACP session resources")
		}
	}
}

// NewSession creates a new session
func (m *Manager) NewSession(ctx context.Context, req acptypes.NewSessionRequest) (*Session, error) {
	m.initMCP(ctx)

	llmConfig := m.buildLLMConfig(req.CWD)

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create LLM thread")
	}

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithWorkingDirectory(req.CWD))
	stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
	stateOpts = append(stateOpts, tools.WithMainTools())

	if !m.config.NoSkills {
		stateOpts = append(stateOpts, tools.WithSkillTool())
	}

	// Initialize workflows for subagent (if not disabled)
	if !m.config.NoWorkflows && !m.config.DisableSubagent {
		stateOpts = append(stateOpts, tools.WithSubAgentTool())
	}

	sessionMCPManager := m.buildSessionMCPManager(ctx, req.MCPServers)
	if mcpOpts := m.buildSessionMCPStateOpts(ctx, thread.GetConversationID(), req.CWD, sessionMCPManager); mcpOpts != nil {
		stateOpts = append(stateOpts, mcpOpts...)
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:                 acptypes.SessionID(thread.GetConversationID()),
		Thread:             thread,
		State:              state,
		MCPManager:         sessionMCPManager,
		CWD:                req.CWD,
		MCPServers:         req.MCPServers,
		maxTurns:           m.config.MaxTurns,
		compactRatio:       m.config.CompactRatio,
		disableAutoCompact: m.config.DisableAutoCompact,
	}

	m.storeSession(ctx, session)

	return session, nil
}

// LoadSession loads an existing session
func (m *Manager) LoadSession(ctx context.Context, req acptypes.LoadSessionRequest) (*Session, error) {
	if m.store == nil {
		return nil, pkgerrors.New("conversation store not available")
	}

	_, err := m.store.Load(ctx, string(req.SessionID))
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to load conversation")
	}

	m.initMCP(ctx)

	llmConfig := m.buildLLMConfig(req.CWD)

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create LLM thread")
	}

	thread.SetConversationID(string(req.SessionID))

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithWorkingDirectory(req.CWD))
	stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
	stateOpts = append(stateOpts, tools.WithMainTools())

	if !m.config.NoSkills {
		stateOpts = append(stateOpts, tools.WithSkillTool())
	}

	// Initialize workflows for subagent (if not disabled)
	if !m.config.NoWorkflows && !m.config.DisableSubagent {
		stateOpts = append(stateOpts, tools.WithSubAgentTool())
	}

	sessionMCPManager := m.buildSessionMCPManager(ctx, req.MCPServers)
	if mcpOpts := m.buildSessionMCPStateOpts(ctx, string(req.SessionID), req.CWD, sessionMCPManager); mcpOpts != nil {
		stateOpts = append(stateOpts, mcpOpts...)
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:                 req.SessionID,
		Thread:             thread,
		State:              state,
		MCPManager:         sessionMCPManager,
		CWD:                req.CWD,
		MCPServers:         req.MCPServers,
		maxTurns:           m.config.MaxTurns,
		compactRatio:       m.config.CompactRatio,
		disableAutoCompact: m.config.DisableAutoCompact,
	}

	m.storeSession(ctx, session)

	return session, nil
}

// GetSession returns a session by ID
func (m *Manager) GetSession(id acptypes.SessionID) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[id]
	if !ok {
		return nil, pkgerrors.Errorf("session not found: %s", id)
	}
	return session, nil
}

// Cancel cancels a session by ID
func (m *Manager) Cancel(id acptypes.SessionID) error {
	session, err := m.GetSession(id)
	if err != nil {
		return err
	}
	session.Cancel()
	return nil
}

// Close releases session-scoped resources, shared MCP clients, and conversation storage.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[acptypes.SessionID]*Session)
	kodeletMCPManager := m.kodeletMCPManager
	m.kodeletMCPManager = nil
	store := m.store
	m.store = nil
	m.mu.Unlock()

	var result error
	for _, session := range sessions {
		if session == nil {
			continue
		}
		result = multierror.Append(result, session.Close(ctx))
	}
	if kodeletMCPManager != nil {
		result = multierror.Append(result, kodeletMCPManager.Close(ctx))
	}
	if store != nil {
		result = multierror.Append(result, store.Close())
	}

	return result
}
