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

	mu       sync.Mutex
	sessions map[acptypes.SessionID]*remoteSession
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
			return "", errors.Wrap(err, "cannot reach daemon; start kodelet serve or check --server and client authentication")
		}
		if !settings.DefaultRunnerReady || strings.TrimSpace(settings.DefaultRunnerID) == "" {
			return "", errors.New("daemon has no ready default runner; configure one on serve or select --runner")
		}
		runnerID = settings.DefaultRunnerID
	}
	target := chat.WorkspaceTarget{RunnerID: runnerID, CWD: request.CWD, Profile: m.config.Profile, EnvironmentProfile: m.config.EnvironmentProfile, Options: m.config.Options.Restrictions()}
	discovery, err := client.DiscoverWorkspace(ctx, target)
	if err != nil {
		return "", errors.Wrap(err, "runner workspace discovery is required for ACP session directories; check the path/profile and upgrade the runner if unsupported")
	}
	if strings.TrimSpace(discovery.CWD) == "" {
		return "", errors.New("runner discovery returned no validated directory")
	}
	id := acptypes.SessionID(convtypes.GenerateID())
	m.mu.Lock()
	m.sessions[id] = &remoteSession{id: id, client: client, runnerID: runnerID, cwd: discovery.CWD, options: m.config.Options.Clone(), environmentProfile: chat.NormalizeEnvironmentProfile(discovery.EnvironmentProfile)}
	m.mu.Unlock()
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
		return chat.ConversationHistory{}, errors.Errorf("control plane returned conversation %s while loading %s", history.ID, request.SessionID)
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
		return chat.ConversationHistory{}, errors.New("conversation model profile is locked; cannot replace it on ACP resume")
	}
	if strings.TrimSpace(history.CWD) == "" {
		return chat.ConversationHistory{}, errors.New("conversation has no validated runner directory; adopt it before resuming")
	}
	if strings.TrimSpace(request.CWD) != "" && strings.TrimSpace(request.CWD) != history.CWD {
		return chat.ConversationHistory{}, errors.Errorf("conversation directory is locked to %s; cannot resume with %s", history.CWD, request.CWD)
	}
	discovery, err := client.DiscoverWorkspace(ctx, chat.WorkspaceTarget{ConversationID: history.ID, Options: m.config.Options.Restrictions()})
	if err != nil {
		return chat.ConversationHistory{}, errors.Wrap(err, "failed to validate stored runner affinity")
	}
	if discovery.CWD != history.CWD || chat.NormalizeEnvironmentProfile(discovery.EnvironmentProfile) != chat.NormalizeEnvironmentProfile(history.EnvironmentProfile) {
		return chat.ConversationHistory{}, errors.New("runner discovery does not match stored conversation affinity")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if previous := m.sessions[request.SessionID]; previous != nil && previous.active {
		return chat.ConversationHistory{}, errors.New("cannot reload a session with an active prompt")
	}
	m.sessions[request.SessionID] = &remoteSession{
		id:                 request.SessionID,
		started:            true,
		client:             client,
		runnerID:           history.RunnerID,
		cwd:                history.CWD,
		options:            m.config.Options.Clone(),
		environmentProfile: chat.NormalizeEnvironmentProfile(history.EnvironmentProfile),
	}
	return history, nil
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
	if session.uncertain {
		return false, errors.New("previous submission outcome is uncertain; inspect daemon history and reload the session before submitting again")
	}
	session.active = true
	return !session.started, nil
}

func (m *remoteSessionManager) promptTarget(sessionID acptypes.SessionID) (RemoteChatClient, chat.ChatRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionID]
	return session.client, chat.ChatRequest{RunnerID: session.runnerID, CWD: session.cwd, EnvironmentProfile: session.environmentProfile, Options: session.options.Clone()}
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
		return nil, "", errors.New("daemon client is unavailable")
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
