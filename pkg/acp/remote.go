package acp

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// RemoteChatClient is the control-plane API required by server-backed ACP sessions.
type RemoteChatClient interface {
	chat.ChatRunner
	chat.ConversationSource
	ChatSettings(context.Context, string) (chat.ControlPlaneChatSettings, error)
	DiscoverWorkspace(context.Context, chat.WorkspaceTarget) (protocol.WorkspaceDiscoverResult, error)
	SteerConversation(ctx context.Context, conversationID, message string, images []string) (bool, error)
	StopConversation(ctx context.Context, conversationID string) error
	StopConversationTurn(ctx context.Context, conversationID, turnID string) error
}

// RemoteChatProvider returns a daemon client and an optional explicit runner ID.
type RemoteChatProvider interface {
	WaitForRemoteChat(ctx context.Context) (RemoteChatClient, string, error)
}

// RemoteSessionConfig configures ACP sessions whose agentic loop runs on a control plane.
type RemoteSessionConfig struct {
	Provider                   RemoteChatProvider
	Profile                    string
	ReasoningEffort            string
	Options                    *llmtypes.ExecutionOptions
	EnvironmentProfile         string
	EnvironmentProfileExplicit bool
	ReadinessTimeout           time.Duration
}

type remoteSessionManager struct {
	config RemoteSessionConfig

	mu               sync.Mutex
	sessions         map[acptypes.SessionID]*remoteSession
	closed           bool
	attachExtensions func(*remoteSession, map[string]any) error
}

type remoteSession struct {
	id                 acptypes.SessionID
	started            bool
	active             bool
	uncertain          bool
	client             RemoteChatClient
	runnerID           string
	cwd                string
	options            *llmtypes.ExecutionOptions
	environmentProfile string
	initializing       bool
	extensionRelay     chat.SessionExtensionRelay
	extensionErr       error
	extensionChannels  map[extensionChannel]struct{}
}

type remoteOutcomeSink struct {
	chat.ChatEventSink
	done      bool
	cancelled bool
}

func (s *remoteOutcomeSink) Send(event chat.ChatEvent) error {
	if event.Kind == "done" {
		s.done, s.cancelled = true, event.Cancelled
	}
	return s.ChatEventSink.Send(event)
}

func newRemoteSessionManager(config RemoteSessionConfig) *remoteSessionManager {
	config.Options = config.Options.Clone()
	return &remoteSessionManager{
		config:   config,
		sessions: make(map[acptypes.SessionID]*remoteSession),
	}
}

func (m *remoteSessionManager) newSession(ctx context.Context, request acptypes.NewSessionRequest) (acptypes.SessionID, error) {
	if err := m.config.Options.Validate(); err != nil {
		return "", err
	}
	client, runnerID, err := m.waitForClient(ctx)
	if err != nil {
		return "", err
	}
	if runnerID == "" {
		settings, err := client.ChatSettings(ctx, m.config.Profile)
		if err != nil {
			return "", errors.Wrap(err, "could not connect to the server; start 'kodelet serve' or check --server and your authentication settings")
		}
		if !settings.DefaultRunnerReady || strings.TrimSpace(settings.DefaultRunnerID) == "" {
			return "", errors.New("the default runner is unavailable; check 'kodelet runner list' and the server logs, or select another runner with --runner")
		}
		runnerID = settings.DefaultRunnerID
	}
	target := chat.WorkspaceTarget{RunnerID: runnerID, CWD: request.CWD, Profile: m.config.Profile, EnvironmentProfile: m.config.EnvironmentProfile, Options: m.config.Options.Restrictions()}
	discovery, err := client.DiscoverWorkspace(ctx, target)
	if err != nil {
		return "", errors.Wrap(err, "could not check the session directory; verify the path and runner profile, and update the runner if it does not support directory selection")
	}
	if strings.TrimSpace(discovery.CWD) == "" {
		return "", errors.New("the runner did not return a working directory; check the directory and runner logs")
	}
	id := acptypes.SessionID(convtypes.GenerateID())
	if err := m.installSession(&remoteSession{id: id, client: client, runnerID: runnerID, cwd: discovery.CWD, options: m.config.Options.Clone(), environmentProfile: chat.NormalizeEnvironmentProfile(discovery.EnvironmentProfile)}, request.Meta); err != nil {
		return "", err
	}
	return id, nil
}

func (m *remoteSessionManager) loadSession(ctx context.Context, request acptypes.LoadSessionRequest) (chat.ConversationHistory, error) {
	if err := m.config.Options.Validate(); err != nil {
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
		return chat.ConversationHistory{}, errors.Errorf("server returned conversation %s while loading %s", history.ID, request.SessionID)
	}
	if strings.TrimSpace(history.RunnerID) == "" {
		return chat.ConversationHistory{}, errors.New("conversation is not bound to a workspace runner")
	}
	if runnerID != "" && history.RunnerID != runnerID {
		return chat.ConversationHistory{}, errors.Errorf("conversation is bound to runner %s, not this workspace runner %s", history.RunnerID, runnerID)
	}
	if m.config.EnvironmentProfileExplicit {
		expected := chat.NormalizeEnvironmentProfile(m.config.EnvironmentProfile)
		actual := chat.NormalizeEnvironmentProfile(history.EnvironmentProfile)
		if actual != expected {
			return chat.ConversationHistory{}, errors.Errorf("conversation uses runner profile %q, not requested profile %q", actual, expected)
		}
	}
	if m.config.Profile != "" && chat.NormalizeRequestedProfile(m.config.Profile) != chat.NormalizeRequestedProfile(history.Profile) {
		return chat.ConversationHistory{}, errors.New("the model profile cannot be changed when resuming; start a new session to use another profile")
	}
	if strings.TrimSpace(history.CWD) == "" {
		return chat.ConversationHistory{}, errors.New("the conversation has no saved working directory on its runner; adopt it before resuming")
	}
	if strings.TrimSpace(request.CWD) != "" && strings.TrimSpace(request.CWD) != history.CWD {
		return chat.ConversationHistory{}, errors.Errorf("this conversation uses %s and cannot resume in %s; start a new session to use another directory", history.CWD, request.CWD)
	}
	discovery, err := client.DiscoverWorkspace(ctx, chat.WorkspaceTarget{ConversationID: history.ID, Options: m.config.Options.Restrictions()})
	if err != nil {
		return chat.ConversationHistory{}, errors.Wrap(err, "failed to verify the conversation's saved runner and directory")
	}
	if discovery.CWD != history.CWD || chat.NormalizeEnvironmentProfile(discovery.EnvironmentProfile) != chat.NormalizeEnvironmentProfile(history.EnvironmentProfile) {
		return chat.ConversationHistory{}, errors.New("the runner returned a different directory or environment profile than this conversation saved")
	}
	if err := m.installSession(&remoteSession{
		id:                 request.SessionID,
		started:            true,
		client:             client,
		runnerID:           history.RunnerID,
		cwd:                history.CWD,
		options:            m.config.Options.Clone(),
		environmentProfile: chat.NormalizeEnvironmentProfile(history.EnvironmentProfile),
	}, request.Meta); err != nil {
		return chat.ConversationHistory{}, err
	}
	return history, nil
}

func (m *remoteSessionManager) installSession(session *remoteSession, meta map[string]any) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("ACP client connection is closed")
	}
	previous := m.sessions[session.id]
	if previous != nil && (previous.active || previous.initializing) {
		m.mu.Unlock()
		return errors.New("cannot reload a session with an active prompt or attachment")
	}
	session.initializing = true
	m.sessions[session.id] = session
	m.mu.Unlock()
	if previous != nil && previous.extensionRelay != nil {
		_ = previous.extensionRelay.Close()
	}
	var err error
	if m.attachExtensions != nil {
		err = m.attachExtensions(session, meta)
	}
	m.mu.Lock()
	if err == nil && m.closed {
		err = errors.New("ACP client connection is closed")
	}
	if err == nil && session.extensionErr != nil {
		err = session.extensionErr
	}
	session.initializing = false
	if err != nil {
		delete(m.sessions, session.id)
	}
	m.mu.Unlock()
	if err != nil && session.extensionRelay != nil {
		_ = session.extensionRelay.Close()
	}
	return err
}

func (m *remoteSessionManager) commands(ctx context.Context, sessionID acptypes.SessionID) ([]slashcommands.Command, error) {
	commands := slashcommands.BuiltIns()
	m.mu.Lock()
	session := m.sessions[sessionID]
	if session == nil {
		m.mu.Unlock()
		return nil, errors.Errorf("session not found: %s", sessionID)
	}
	client := session.client
	target := chat.WorkspaceTarget{ConversationID: string(sessionID), Options: session.options.Restrictions()}
	if !session.started {
		target.ConversationID = ""
		target.RunnerID, target.CWD = session.runnerID, session.cwd
		target.Profile, target.EnvironmentProfile = m.config.Profile, session.environmentProfile
	}
	m.mu.Unlock()
	discovery, err := client.DiscoverWorkspace(ctx, target)
	if err != nil {
		return nil, err
	}
	workspaceCommands := discovery.Commands
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

func (m *remoteSessionManager) beginPrompt(sessionID acptypes.SessionID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionID]
	if session == nil {
		return false, errors.Errorf("session not found: %s", sessionID)
	}
	if session.active {
		return false, errors.Errorf("session %s already has an active prompt", sessionID)
	}
	if session.initializing {
		return false, errors.New("session attachment is not ready")
	}
	if session.extensionErr != nil {
		return false, errors.Wrap(session.extensionErr, "inline extensions disconnected; reload the session with callbacks before sending another message")
	}
	if session.extensionRelay != nil {
		select {
		case <-session.extensionRelay.Done():
			return false, errors.New("inline extensions disconnected; reload the session with callbacks before sending another message")
		default:
		}
	}
	if session.uncertain {
		return false, errors.New("the previous message may still be running; check the conversation history and reload the session before sending another message")
	}
	session.active = true
	return !session.started, nil
}

func (m *remoteSessionManager) promptTarget(sessionID acptypes.SessionID) (RemoteChatClient, chat.ChatRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionID]
	request := chat.ChatRequest{RunnerID: session.runnerID, CWD: session.cwd, EnvironmentProfile: session.environmentProfile, Options: session.options.Clone()}
	if session.extensionRelay != nil {
		request.SessionExtensionsID = session.extensionRelay.Attachment().ID
	}
	return session.client, request
}

func (m *remoteSessionManager) finishPrompt(sessionID acptypes.SessionID, succeeded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session := m.sessions[sessionID]; session != nil {
		session.active = false
		if succeeded {
			session.started = true
			// Model semantics are now frozen by the daemon. Keep turn limits,
			// weak selection and restrictions, not request alias spellings.
			if session.options != nil {
				restrictions := session.options.Restrictions()
				restrictions.MaxTurns = session.options.MaxTurns
				restrictions.UseWeakModel = session.options.UseWeakModel
				session.options = restrictions
			}
		}
	}
}

func (m *remoteSessionManager) isActive(sessionID acptypes.SessionID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionID]
	return session != nil && session.active
}

func (m *remoteSessionManager) hasSession(sessionID acptypes.SessionID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionID] != nil
}

func (m *remoteSessionManager) client(ctx context.Context) (RemoteChatClient, string, error) {
	if m == nil || m.config.Provider == nil {
		return nil, "", errors.New("remote ACP chat provider is unavailable")
	}
	client, runnerID, err := m.config.Provider.WaitForRemoteChat(ctx)
	if err != nil {
		return nil, "", err
	}
	if client == nil {
		return nil, "", errors.New("the server connection is unavailable")
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
		return nil, "", errors.Errorf("workspace runner did not become ready within %s", timeout)
	}
	return client, runnerID, err
}

func (m *remoteSessionManager) markUncertain(sessionID acptypes.SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session := m.sessions[sessionID]; session != nil {
		session.uncertain = true
	}
}
