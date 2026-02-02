// Package session manages ACP session lifecycle, wrapping kodelet threads
// with ACP session semantics.
package session

import (
	"context"
	"errors"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/bridge"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/mcp"
	"github.com/jingkaihe/kodelet/pkg/mcp/codegen"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	pkgerrors "github.com/pkg/errors"
	"github.com/spf13/viper"
)

// ErrNoMCPServers is returned when there are no MCP servers to connect to
var ErrNoMCPServers = errors.New("no MCP servers provided")

// Session represents an ACP session wrapping a kodelet thread
type Session struct {
	ID         acptypes.SessionID
	Thread     llmtypes.Thread
	State      *tools.BasicState
	CWD        string
	MCPServers []acptypes.MCPServer

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
		MaxTurns:           50,
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

// Manager manages ACP sessions
type Manager struct {
	id        string // unique manager ID for MCP socket isolation
	provider  string
	model     string
	maxTokens int
	noSkills  bool
	noHooks   bool

	compactRatio       float64
	disableAutoCompact bool

	sessions map[acptypes.SessionID]*Session
	store    conversations.ConversationStore
	mu       sync.RWMutex

	kodeletMCPManager *tools.MCPManager
	mcpSetup          *mcp.ExecutionSetup
	mcpInitOnce       sync.Once
}

// NewManager creates a new session manager
func NewManager(provider, model string, maxTokens int, noSkills, noHooks bool, compactRatio float64, disableAutoCompact bool) *Manager {
	ctx := context.Background()
	store, _ := conversations.GetConversationStore(ctx)

	return &Manager{
		id:                 convtypes.GenerateID(),
		provider:           provider,
		model:              model,
		maxTokens:          maxTokens,
		noSkills:           noSkills,
		noHooks:            noHooks,
		compactRatio:       compactRatio,
		disableAutoCompact: disableAutoCompact,
		sessions:           make(map[acptypes.SessionID]*Session),
		store:              store,
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

		mcpSetup, err := mcp.SetupExecutionMode(ctx, mcpManager, m.id)
		if err != nil && !errors.Is(err, mcp.ErrDirectMode) {
			logger.G(ctx).WithError(err).Warn("Failed to set up MCP execution mode")
			return
		}

		if err == nil && mcpSetup != nil {
			m.mcpSetup = mcpSetup
			logger.G(ctx).Info("MCP code execution mode initialized for ACP")
		}
	})
}

// getMCPStateOpts returns the state options for MCP tools
func (m *Manager) getMCPStateOpts() []tools.BasicStateOption {
	if m.mcpSetup != nil {
		return m.mcpSetup.StateOpts
	}
	if m.kodeletMCPManager != nil {
		return []tools.BasicStateOption{tools.WithMCPTools(m.kodeletMCPManager)}
	}
	return nil
}

func (m *Manager) buildLLMConfig() llmtypes.Config {
	config, _ := llm.GetConfigFromViper()

	if m.provider != "" {
		config.Provider = m.provider
	}
	if m.model != "" {
		config.Model = m.model
	}
	if m.maxTokens > 0 {
		config.MaxTokens = m.maxTokens
	}
	config.NoHooks = m.noHooks

	if m.noSkills {
		if config.Skills == nil {
			config.Skills = &llmtypes.SkillsConfig{}
		}
		config.Skills.Enabled = false
	}

	executionMode := viper.GetString("mcp.execution_mode")
	workspaceDir := viper.GetString("mcp.code_execution.workspace_dir")
	if workspaceDir == "" {
		workspaceDir = ".kodelet/mcp"
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
		case "sse", "http":
			serverConfig.ServerType = tools.MCPServerTypeSSE
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
		return nil, pkgerrors.Wrap(err, "failed to initialize MCP servers")
	}

	return manager, nil
}

// setupClientMCP sets up client-provided MCP servers with code generation
func (m *Manager) setupClientMCP(ctx context.Context, servers []acptypes.MCPServer) []tools.BasicStateOption {
	if len(servers) == 0 {
		return nil
	}

	clientMCPManager, err := m.connectMCPServers(ctx, servers)
	if err != nil {
		if !errors.Is(err, ErrNoMCPServers) {
			logger.G(ctx).WithError(err).Warn("Failed to connect to client MCP servers")
		}
		return nil
	}

	// If kodelet's MCP already has code execution mode set up, merge client MCP into it
	// and regenerate TypeScript code to include the new tools
	if m.mcpSetup != nil && m.kodeletMCPManager != nil {
		m.kodeletMCPManager.Merge(clientMCPManager)

		workspaceDir := viper.GetString("mcp.code_execution.workspace_dir")
		if workspaceDir == "" {
			workspaceDir = ".kodelet/mcp"
		}

		logger.G(ctx).Info("Regenerating MCP tool TypeScript API with client servers...")
		generator := codegen.NewMCPCodeGenerator(m.kodeletMCPManager, workspaceDir)
		if err := generator.Generate(ctx); err != nil {
			logger.G(ctx).WithError(err).Warn("Failed to regenerate MCP tool code, using direct mode")
			return []tools.BasicStateOption{tools.WithMCPTools(clientMCPManager)}
		}

		logger.G(ctx).Info("Client MCP servers merged into code execution mode")
		return nil // No additional state opts needed, existing Code_execution tool will work
	}

	// No kodelet MCP or not in code execution mode - set up code execution for client MCP
	mcpSetup, err := mcp.SetupExecutionMode(ctx, clientMCPManager, m.id)
	if err != nil && !errors.Is(err, mcp.ErrDirectMode) {
		logger.G(ctx).WithError(err).Warn("Failed to set up MCP execution mode for client servers")
		return []tools.BasicStateOption{tools.WithMCPTools(clientMCPManager)}
	}

	if err == nil && mcpSetup != nil {
		logger.G(ctx).Info("MCP code execution mode initialized for client servers")
		return mcpSetup.StateOpts
	}

	return []tools.BasicStateOption{tools.WithMCPTools(clientMCPManager)}
}

// NewSession creates a new session
func (m *Manager) NewSession(ctx context.Context, req acptypes.NewSessionRequest) (*Session, error) {
	m.initMCP(ctx)

	llmConfig := m.buildLLMConfig()

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create LLM thread")
	}

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
	stateOpts = append(stateOpts, tools.WithMainTools())

	if !m.noSkills {
		stateOpts = append(stateOpts, tools.WithSkillTool())
	}

	// Initialize workflows for subagent
	stateOpts = append(stateOpts, tools.WithSubAgentTool())

	if mcpOpts := m.getMCPStateOpts(); mcpOpts != nil {
		stateOpts = append(stateOpts, mcpOpts...)
	}

	if clientMCPOpts := m.setupClientMCP(ctx, req.MCPServers); clientMCPOpts != nil {
		stateOpts = append(stateOpts, clientMCPOpts...)
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:                 acptypes.SessionID(thread.GetConversationID()),
		Thread:             thread,
		State:              state,
		CWD:                req.CWD,
		MCPServers:         req.MCPServers,
		compactRatio:       m.compactRatio,
		disableAutoCompact: m.disableAutoCompact,
	}

	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()

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

	llmConfig := m.buildLLMConfig()

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create LLM thread")
	}

	thread.SetConversationID(string(req.SessionID))

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
	stateOpts = append(stateOpts, tools.WithMainTools())

	if !m.noSkills {
		stateOpts = append(stateOpts, tools.WithSkillTool())
	}

	// Initialize workflows for subagent
	stateOpts = append(stateOpts, tools.WithSubAgentTool())

	if mcpOpts := m.getMCPStateOpts(); mcpOpts != nil {
		stateOpts = append(stateOpts, mcpOpts...)
	}

	if clientMCPOpts := m.setupClientMCP(ctx, req.MCPServers); clientMCPOpts != nil {
		stateOpts = append(stateOpts, clientMCPOpts...)
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:                 req.SessionID,
		Thread:             thread,
		State:              state,
		CWD:                req.CWD,
		MCPServers:         req.MCPServers,
		compactRatio:       m.compactRatio,
		disableAutoCompact: m.disableAutoCompact,
	}

	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()

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
