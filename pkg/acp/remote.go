package acp

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// RemoteChatClient is the control-plane API required by server-backed ACP sessions.
type RemoteChatClient interface {
	chat.ChatRunner
	chat.ConversationSource
	StopConversation(ctx context.Context, conversationID string) error
	StopConversationTurn(ctx context.Context, conversationID, turnID string) error
}

// RemoteChatProvider waits until the embedded workspace runner has a ready control-plane client.
type RemoteChatProvider interface {
	WaitForRemoteChat(ctx context.Context) (RemoteChatClient, string, error)
}

// RemoteCommandSource discovers slash commands owned by the embedded workspace runner.
type RemoteCommandSource interface {
	Commands(ctx context.Context, environmentProfile string) ([]slashcommands.Command, error)
}

// RemoteSessionConfig configures ACP sessions whose agentic loop runs on a control plane.
type RemoteSessionConfig struct {
	Provider                   RemoteChatProvider
	Workspace                  string
	Profile                    string
	ReasoningEffort            string
	EnvironmentProfile         string
	EnvironmentProfileExplicit bool
	CommandSource              RemoteCommandSource
	ReadinessTimeout           time.Duration
}

type remoteSessionManager struct {
	config RemoteSessionConfig

	mu       sync.Mutex
	sessions map[acptypes.SessionID]*remoteSession
	active   acptypes.SessionID
}

type remoteSession struct {
	id                 acptypes.SessionID
	started            bool
	environmentProfile string
}

func newRemoteSessionManager(config RemoteSessionConfig) *remoteSessionManager {
	return &remoteSessionManager{
		config:   config,
		sessions: make(map[acptypes.SessionID]*remoteSession),
	}
}

func (m *remoteSessionManager) newSession(ctx context.Context, request acptypes.NewSessionRequest) (acptypes.SessionID, error) {
	if err := m.validateCWD(request.CWD); err != nil {
		return "", err
	}
	if _, _, err := m.waitForClient(ctx); err != nil {
		return "", err
	}
	id := acptypes.SessionID(convtypes.GenerateID())
	m.mu.Lock()
	m.sessions[id] = &remoteSession{id: id, environmentProfile: chat.NormalizeEnvironmentProfile(m.config.EnvironmentProfile)}
	m.mu.Unlock()
	return id, nil
}

func (m *remoteSessionManager) loadSession(ctx context.Context, request acptypes.LoadSessionRequest) (chat.ConversationHistory, error) {
	if err := m.validateCWD(request.CWD); err != nil {
		return chat.ConversationHistory{}, err
	}
	client, runnerID, err := m.waitForClient(ctx)
	if err != nil {
		return chat.ConversationHistory{}, err
	}
	history, err := client.LoadConversation(ctx, string(request.SessionID))
	if err != nil {
		return chat.ConversationHistory{}, err
	}
	if strings.TrimSpace(history.ID) != string(request.SessionID) {
		return chat.ConversationHistory{}, errors.Errorf("control plane returned conversation %s while loading %s", history.ID, request.SessionID)
	}
	if strings.TrimSpace(history.RunnerID) == "" {
		return chat.ConversationHistory{}, errors.New("conversation is not bound to a workspace runner")
	}
	if history.RunnerID != runnerID {
		return chat.ConversationHistory{}, errors.Errorf("conversation is bound to runner %s, not this workspace runner %s", history.RunnerID, runnerID)
	}
	if m.config.EnvironmentProfileExplicit {
		expected := chat.NormalizeEnvironmentProfile(m.config.EnvironmentProfile)
		actual := chat.NormalizeEnvironmentProfile(history.EnvironmentProfile)
		if actual != expected {
			return chat.ConversationHistory{}, errors.Errorf("conversation uses runner profile %q, not requested profile %q", actual, expected)
		}
	}
	m.mu.Lock()
	m.sessions[request.SessionID] = &remoteSession{
		id:                 request.SessionID,
		started:            true,
		environmentProfile: chat.NormalizeEnvironmentProfile(history.EnvironmentProfile),
	}
	m.mu.Unlock()
	return history, nil
}

func (m *remoteSessionManager) commands(ctx context.Context, sessionID acptypes.SessionID) ([]slashcommands.Command, error) {
	commands := slashcommands.BuiltIns()
	if m == nil || m.config.CommandSource == nil {
		return commands, nil
	}
	m.mu.Lock()
	session := m.sessions[sessionID]
	m.mu.Unlock()
	if session == nil {
		return nil, errors.Errorf("session not found: %s", sessionID)
	}
	workspaceCommands, err := m.config.CommandSource.Commands(ctx, session.environmentProfile)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(commands)+len(workspaceCommands))
	result := make([]slashcommands.Command, 0, len(commands)+len(workspaceCommands))
	for _, command := range append(commands, workspaceCommands...) {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, command)
	}
	return result, nil
}

func (m *remoteSessionManager) beginPrompt(sessionID acptypes.SessionID) (bool, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionID]
	if session == nil {
		return false, "", errors.Errorf("session not found: %s", sessionID)
	}
	if m.active != "" {
		return false, "", errors.Errorf("workspace agent is already running session %s", m.active)
	}
	m.active = sessionID
	return !session.started, session.environmentProfile, nil
}

func (m *remoteSessionManager) finishPrompt(sessionID acptypes.SessionID, succeeded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == sessionID {
		m.active = ""
	}
	if succeeded {
		if session := m.sessions[sessionID]; session != nil {
			session.started = true
		}
	}
}

func (m *remoteSessionManager) isActive(sessionID acptypes.SessionID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active == sessionID
}

func (m *remoteSessionManager) client(ctx context.Context) (RemoteChatClient, string, error) {
	if m == nil || m.config.Provider == nil {
		return nil, "", errors.New("remote ACP chat provider is unavailable")
	}
	client, runnerID, err := m.config.Provider.WaitForRemoteChat(ctx)
	if err != nil {
		return nil, "", err
	}
	if client == nil || strings.TrimSpace(runnerID) == "" {
		return nil, "", errors.New("embedded workspace runner is not ready")
	}
	return client, strings.TrimSpace(runnerID), nil
}

func (m *remoteSessionManager) waitForClient(ctx context.Context) (RemoteChatClient, string, error) {
	timeout := m.config.ReadinessTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, runnerID, err := m.client(waitCtx)
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, "", errors.Errorf("embedded workspace runner did not become ready within %s", timeout)
	}
	return client, runnerID, err
}

func (m *remoteSessionManager) validateCWD(requested string) error {
	workspace, err := conversations.NormalizeCWD(m.config.Workspace)
	if err != nil {
		return errors.Wrap(err, "failed to resolve embedded runner workspace")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil
	}
	resolved, err := conversations.NormalizeCWD(requested)
	if err != nil {
		return err
	}
	if resolved != workspace {
		return errors.Errorf("server-backed ACP is bound to workspace %s, not %s", workspace, resolved)
	}
	return nil
}
