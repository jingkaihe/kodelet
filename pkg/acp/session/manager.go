// Package session manages ACP session lifecycle, wrapping kodelet threads
// with ACP session semantics.
package session

import (
	"context"
	"strings"
	"sync"

	"github.com/hashicorp/go-multierror"
	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/bridge"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	pkgerrors "github.com/pkg/errors"
)

// Session represents an ACP session wrapping a kodelet thread
type Session struct {
	ID         acptypes.SessionID
	Thread     llmtypes.Thread
	State      *tools.BasicState
	Extensions *extensions.Runtime
	CWD        string

	maxTurns     int
	compactRatio float64

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
	var result error
	if err := llm.CloseThread(s.Thread); err != nil {
		result = multierror.Append(result, err)
	}
	if s.Extensions != nil {
		if err := s.Extensions.Close(); err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
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
		PromptCache:  true,
		Images:       images,
		MaxTurns:     s.maxTurns,
		CompactRatio: s.compactRatio,
	})

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
	Provider            string
	Model               string
	MaxTokens           int
	NoSkills            bool
	NoExtensions        bool
	EnableFSSearchTools bool
	MaxTurns            int
	CompactRatio        float64
}

// Manager manages ACP sessions
type Manager struct {
	config ManagerConfig

	sessions map[acptypes.SessionID]*Session
	store    conversations.ConversationStore
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager(cfg ManagerConfig) *Manager {
	ctx := context.Background()
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to create conversation store for ACP sessions")
	}

	return &Manager{
		config:   cfg,
		sessions: make(map[acptypes.SessionID]*Session),
		store:    store,
	}
}

func (m *Manager) buildLLMConfig(projectDir string) llmtypes.Config {
	config, _ := llm.GetConfigFromViper()
	return m.applyManagerConfig(config, projectDir)
}

func (m *Manager) applyManagerConfig(config llmtypes.Config, projectDir string) llmtypes.Config {
	if m.config.Provider != "" {
		config.Provider = m.config.Provider
	}
	if m.config.Model != "" {
		config.Model = m.config.Model
	}
	if m.config.MaxTokens > 0 {
		config.MaxTokens = m.config.MaxTokens
	}
	config.EnableFSSearchTools = m.config.EnableFSSearchTools
	config.WorkingDirectory = projectDir

	if m.config.NoSkills {
		if config.Skills == nil {
			config.Skills = &llmtypes.SkillsConfig{}
		}
		config.Skills.Enabled = false
	}

	return config
}

func (m *Manager) buildLLMConfigForRecord(record convtypes.ConversationRecord, projectDir string) (llmtypes.Config, error) {
	snapshot, hasSnapshot, err := conversations.ConfigSnapshotFromMetadata(record.Metadata)
	if err != nil {
		return llmtypes.Config{}, pkgerrors.Wrap(err, "failed to load conversation config snapshot")
	}
	if hasSnapshot {
		profileName := strings.TrimSpace(snapshot.Profile)
		var config llmtypes.Config
		if profileName != "" && !strings.EqualFold(profileName, "default") && llm.HasConfiguredProfile(profileName) {
			config, err = llm.GetConfigFromViperWithProfile(profileName)
		} else {
			config, err = llm.GetConfigFromViperWithoutProfile()
		}
		if err != nil {
			return llmtypes.Config{}, err
		}
		config = m.applyManagerConfig(config, projectDir)
		return snapshot.Apply(config)
	}

	config := m.buildLLMConfig(projectDir)
	if strings.TrimSpace(record.Provider) != "" {
		config.Provider = strings.TrimSpace(record.Provider)
	}
	if model, ok := record.Metadata["model"].(string); ok && strings.TrimSpace(model) != "" {
		config.Model = strings.TrimSpace(model)
	}
	return config, nil
}

func (m *Manager) buildExtensionRuntime(ctx context.Context, projectDir string) *extensions.Runtime {
	if m.config.NoExtensions {
		return nil
	}
	runtime, err := extensions.NewRuntimeFromViper(ctx, projectDir)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to initialize extension runtime for ACP session")
		return nil
	}
	return runtime
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
	llmConfig := m.buildLLMConfig(req.CWD)
	extensionRuntime := m.buildExtensionRuntime(ctx, req.CWD)
	llmConfig.Extensions = extensionRuntime

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		if extensionRuntime != nil {
			_ = extensionRuntime.Close()
		}
		return nil, pkgerrors.Wrap(err, "failed to create LLM thread")
	}

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithWorkingDirectory(req.CWD))
	stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
	stateOpts = append(stateOpts, tools.WithMainTools())

	if !m.config.NoSkills {
		stateOpts = append(stateOpts, tools.WithSkillTool())
	}

	if extensionRuntime != nil {
		stateOpts = append(stateOpts, tools.WithExtensionTools(extensionRuntime.Tools()))
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:           acptypes.SessionID(thread.GetConversationID()),
		Thread:       thread,
		State:        state,
		Extensions:   extensionRuntime,
		CWD:          req.CWD,
		maxTurns:     m.config.MaxTurns,
		compactRatio: m.config.CompactRatio,
	}

	m.storeSession(ctx, session)

	return session, nil
}

// LoadSession loads an existing session
func (m *Manager) LoadSession(ctx context.Context, req acptypes.LoadSessionRequest) (*Session, error) {
	if m.store == nil {
		return nil, pkgerrors.New("conversation store not available")
	}

	record, err := m.store.Load(ctx, string(req.SessionID))
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to load conversation")
	}

	llmConfig, err := m.buildLLMConfigForRecord(record, req.CWD)
	if err != nil {
		return nil, err
	}
	extensionRuntime := m.buildExtensionRuntime(ctx, req.CWD)
	llmConfig.Extensions = extensionRuntime

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		if extensionRuntime != nil {
			_ = extensionRuntime.Close()
		}
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

	if extensionRuntime != nil {
		stateOpts = append(stateOpts, tools.WithExtensionTools(extensionRuntime.Tools()))
	}

	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)
	thread.EnablePersistence(ctx, true)

	session := &Session{
		ID:           req.SessionID,
		Thread:       thread,
		State:        state,
		Extensions:   extensionRuntime,
		CWD:          req.CWD,
		maxTurns:     m.config.MaxTurns,
		compactRatio: m.config.CompactRatio,
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

// Close releases session-scoped resources and conversation storage.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[acptypes.SessionID]*Session)
	store := m.store
	m.store = nil
	m.mu.Unlock()

	var result error
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if err := session.Close(ctx); err != nil {
			result = multierror.Append(result, err)
		}
	}
	if store != nil {
		if err := store.Close(); err != nil {
			result = multierror.Append(result, err)
		}
	}

	return result
}
