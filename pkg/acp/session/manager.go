// Package session manages ACP session lifecycle, wrapping kodelet threads
// with ACP session semantics.
package session

import (
	"context"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/bridge"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/skills"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// Session represents an ACP session wrapping a kodelet thread
type Session struct {
	ID         acptypes.SessionID
	Thread     llmtypes.Thread
	State      *tools.BasicState
	CWD        string
	MCPServers []acptypes.MCPServer

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

// HandlePrompt processes a prompt and returns the stop reason
func (s *Session) HandlePrompt(ctx context.Context, prompt []acptypes.ContentBlock, sender UpdateSender) (acptypes.StopReason, error) {
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

	_, err := s.Thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
		PromptCache: true,
		Images:      images,
		MaxTurns:    50,
	})

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
	provider  string
	model     string
	maxTokens int
	noSkills  bool
	noHooks   bool

	sessions map[acptypes.SessionID]*Session
	store    conversations.ConversationStore
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager(provider, model string, maxTokens int, noSkills, noHooks bool) *Manager {
	ctx := context.Background()
	store, _ := conversations.GetConversationStore(ctx)

	return &Manager{
		provider:  provider,
		model:     model,
		maxTokens: maxTokens,
		noSkills:  noSkills,
		noHooks:   noHooks,
		sessions:  make(map[acptypes.SessionID]*Session),
		store:     store,
	}
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

	return config
}

// NewSession creates a new session
func (m *Manager) NewSession(ctx context.Context, req acptypes.NewSessionRequest) (*Session, error) {
	llmConfig := m.buildLLMConfig()

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create LLM thread")
	}

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
	stateOpts = append(stateOpts, tools.WithMainTools())

	if !m.noSkills {
		discoveredSkills, skillsEnabled := skills.Initialize(ctx, llmConfig)
		stateOpts = append(stateOpts, tools.WithSkillTool(discoveredSkills, skillsEnabled))
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:         acptypes.SessionID(thread.GetConversationID()),
		Thread:     thread,
		State:      state,
		CWD:        req.CWD,
		MCPServers: req.MCPServers,
	}

	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()

	return session, nil
}

// LoadSession loads an existing session
func (m *Manager) LoadSession(ctx context.Context, req acptypes.LoadSessionRequest) (*Session, error) {
	if m.store == nil {
		return nil, errors.New("conversation store not available")
	}

	_, err := m.store.Load(ctx, string(req.SessionID))
	if err != nil {
		return nil, errors.Wrap(err, "failed to load conversation")
	}

	llmConfig := m.buildLLMConfig()

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create LLM thread")
	}

	thread.SetConversationID(string(req.SessionID))

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
	stateOpts = append(stateOpts, tools.WithMainTools())

	if !m.noSkills {
		discoveredSkills, skillsEnabled := skills.Initialize(ctx, llmConfig)
		stateOpts = append(stateOpts, tools.WithSkillTool(discoveredSkills, skillsEnabled))
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:         req.SessionID,
		Thread:     thread,
		State:      state,
		CWD:        req.CWD,
		MCPServers: req.MCPServers,
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
		return nil, errors.Errorf("session not found: %s", id)
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
