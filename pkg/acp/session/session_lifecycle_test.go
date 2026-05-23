package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/mcp"
	"github.com/jingkaihe/kodelet/pkg/tools"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeThread struct {
	mu sync.Mutex

	sendMessageFunc func(context.Context, string, llmtypes.MessageHandler, llmtypes.MessageOpt) (string, error)

	state              tooltypes.State
	conversationID     string
	persistenceEnabled bool
	messages           []llmtypes.Message
	usage              llmtypes.Usage
	config             llmtypes.Config
	metadata           map[string]any

	lastMessage string
	lastHandler llmtypes.MessageHandler
	lastOpt     llmtypes.MessageOpt
}

func (f *fakeThread) SetState(s tooltypes.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = s
}

func (f *fakeThread) GetState() tooltypes.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeThread) AddUserMessage(_ context.Context, message string, imagePaths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, llmtypes.Message{Role: llmtypes.InitiatorUser, Content: message})
	for _, imagePath := range imagePaths {
		f.messages = append(f.messages, llmtypes.Message{Role: llmtypes.InitiatorUser, Content: imagePath})
	}
}

func (f *fakeThread) SendMessage(ctx context.Context, message string, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (string, error) {
	f.mu.Lock()
	f.lastMessage = message
	f.lastHandler = handler
	f.lastOpt = opt
	f.mu.Unlock()

	if f.sendMessageFunc != nil {
		return f.sendMessageFunc(ctx, message, handler, opt)
	}

	return "", nil
}

func (f *fakeThread) GetUsage() llmtypes.Usage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.usage
}

func (f *fakeThread) GetConversationID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conversationID
}

func (f *fakeThread) SetConversationID(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conversationID = id
}

func (f *fakeThread) SaveConversation(context.Context, bool) error { return nil }

func (f *fakeThread) IsPersisted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.persistenceEnabled
}

func (f *fakeThread) EnablePersistence(_ context.Context, enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistenceEnabled = enabled
}

func (f *fakeThread) Provider() string { return "fake" }

func (f *fakeThread) GetMessages() ([]llmtypes.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llmtypes.Message(nil), f.messages...), nil
}

func (f *fakeThread) GetConfig() llmtypes.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.config
}

func (f *fakeThread) AggregateSubagentUsage(usage llmtypes.Usage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usage.InputTokens += usage.InputTokens
	f.usage.OutputTokens += usage.OutputTokens
}

func (f *fakeThread) SetMetadataValue(key string, value any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.metadata == nil {
		f.metadata = make(map[string]any)
	}
	f.metadata[key] = value
}

func (f *fakeThread) GetMetadata() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	metadata := make(map[string]any, len(f.metadata))
	for key, value := range f.metadata {
		metadata[key] = value
	}
	return metadata
}

func (f *fakeThread) lastSend() (string, llmtypes.MessageHandler, llmtypes.MessageOpt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastMessage, f.lastHandler, f.lastOpt
}

type sentUpdate struct {
	sessionID acptypes.SessionID
	update    any
}

type fakeUpdateSender struct {
	mu      sync.Mutex
	updates []sentUpdate
	err     error
}

func (f *fakeUpdateSender) SendUpdate(sessionID acptypes.SessionID, update any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, sentUpdate{sessionID: sessionID, update: update})
	return f.err
}

func (f *fakeUpdateSender) snapshot() []sentUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentUpdate(nil), f.updates...)
}

type fakeConversationStore struct {
	mu       sync.Mutex
	closed   bool
	closeErr error
	loads    map[string]conversationtypes.ConversationRecord
	loadErr  error
}

func (f *fakeConversationStore) Save(context.Context, conversationtypes.ConversationRecord) error {
	return nil
}

func (f *fakeConversationStore) Load(_ context.Context, id string) (conversationtypes.ConversationRecord, error) {
	if f.loadErr != nil {
		return conversationtypes.ConversationRecord{}, f.loadErr
	}
	if f.loads != nil {
		if record, ok := f.loads[id]; ok {
			return record, nil
		}
	}
	return conversationtypes.ConversationRecord{}, errors.New("not found")
}

func (f *fakeConversationStore) Delete(context.Context, string) error { return nil }

func (f *fakeConversationStore) Query(context.Context, conversationtypes.QueryOptions) (conversationtypes.QueryResult, error) {
	return conversationtypes.QueryResult{}, nil
}

func (f *fakeConversationStore) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}

func (f *fakeConversationStore) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func newEmptyMCPManager(t *testing.T) *tools.MCPManager {
	t.Helper()
	mcpManager, err := tools.NewMCPManager(tools.MCPConfig{Servers: map[string]tools.MCPServerConfig{}})
	require.NoError(t, err)
	return mcpManager
}

func TestSessionCancelAndIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{cancelFunc: cancel}

	assert.False(t, session.IsCancelled())

	session.Cancel()

	assert.True(t, session.IsCancelled())
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestSessionCloseCancelsAndClosesResources(t *testing.T) {
	t.Run("without MCP manager", func(t *testing.T) {
		session := &Session{}

		require.NoError(t, session.Close(context.Background()))

		assert.True(t, session.IsCancelled())
	})

	t.Run("with MCP manager", func(t *testing.T) {
		session := &Session{MCPManager: newEmptyMCPManager(t)}

		require.NoError(t, session.Close(context.Background()))

		assert.True(t, session.IsCancelled())
	})
}

func TestSessionHandlePromptSendsMessageAndUpdates(t *testing.T) {
	thread := &fakeThread{}
	thread.sendMessageFunc = func(_ context.Context, _ string, handler llmtypes.MessageHandler, _ llmtypes.MessageOpt) (string, error) {
		handler.HandleText("agent response")
		return "agent response", nil
	}
	sender := &fakeUpdateSender{}
	session := &Session{
		ID:           "session-1",
		Thread:       thread,
		maxTurns:     7,
		compactRatio: 0.65,
	}

	stopReason, err := session.HandlePrompt(context.Background(), []acptypes.ContentBlock{
		{Type: acptypes.ContentTypeText, Text: "hello"},
		{Type: acptypes.ContentTypeImage, MimeType: "image/png", Data: "abc123"},
		{Type: acptypes.ContentTypeResource, Resource: &acptypes.EmbeddedResource{URI: "file:///tmp/context.txt", Text: "context"}},
		{Type: acptypes.ContentTypeResourceLink, URI: "file:///tmp/linked.txt"},
	}, sender)

	require.NoError(t, err)
	assert.Equal(t, acptypes.StopReasonEndTurn, stopReason)

	message, handler, opt := thread.lastSend()
	assert.Equal(t, "hello\n\n--- file:///tmp/context.txt ---\ncontext\n\n[Resource: file:///tmp/linked.txt]", message)
	assert.NotNil(t, handler)
	assert.True(t, opt.PromptCache)
	assert.Equal(t, []string{"data:image/png;base64,abc123"}, opt.Images)
	assert.Equal(t, 7, opt.MaxTurns)
	assert.Equal(t, 0.65, opt.CompactRatio)

	updates := sender.snapshot()
	require.Len(t, updates, 1)
	assert.Equal(t, acptypes.SessionID("session-1"), updates[0].sessionID)
	update, ok := updates[0].update.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, update["sessionUpdate"])
	content, ok := update["content"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, acptypes.ContentTypeText, content["type"])
	assert.Equal(t, "agent response", content["text"])

	assert.False(t, session.IsCancelled())
	assert.Nil(t, session.cancelFunc)
}

func TestSessionHandlePromptReturnsThreadError(t *testing.T) {
	expectedErr := errors.New("send failed")
	thread := &fakeThread{
		sendMessageFunc: func(context.Context, string, llmtypes.MessageHandler, llmtypes.MessageOpt) (string, error) {
			return "", expectedErr
		},
	}
	session := &Session{ID: "session-1", Thread: thread}

	stopReason, err := session.HandlePrompt(context.Background(), []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "hello"}}, &fakeUpdateSender{})

	assert.Equal(t, acptypes.StopReasonEndTurn, stopReason)
	assert.ErrorIs(t, err, expectedErr)
	assert.False(t, session.IsCancelled())
	assert.Nil(t, session.cancelFunc)
}

func TestSessionHandlePromptReturnsCancelledWhenCancelCalled(t *testing.T) {
	started := make(chan struct{})
	thread := &fakeThread{
		sendMessageFunc: func(ctx context.Context, _ string, _ llmtypes.MessageHandler, _ llmtypes.MessageOpt) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	session := &Session{ID: "session-1", Thread: thread}
	type result struct {
		stopReason acptypes.StopReason
		err        error
	}
	done := make(chan result, 1)

	go func() {
		stopReason, err := session.HandlePrompt(context.Background(), []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "hello"}}, &fakeUpdateSender{})
		done <- result{stopReason: stopReason, err: err}
	}()

	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	session.Cancel()

	select {
	case result := <-done:
		assert.Equal(t, acptypes.StopReasonCancelled, result.stopReason)
		assert.NoError(t, result.err)
	case <-time.After(time.Second):
		t.Fatal("HandlePrompt did not return after cancellation")
	}
	assert.True(t, session.IsCancelled())
	assert.Nil(t, session.cancelFunc)
}

func TestManagerStoreSessionGetSessionAndReplacement(t *testing.T) {
	manager := &Manager{sessions: make(map[acptypes.SessionID]*Session)}
	previous := &Session{ID: "session-1"}
	replacement := &Session{ID: "session-1"}

	manager.storeSession(context.Background(), previous)
	got, err := manager.GetSession("session-1")
	require.NoError(t, err)
	assert.Same(t, previous, got)
	assert.False(t, previous.IsCancelled())

	manager.storeSession(context.Background(), replacement)
	got, err = manager.GetSession("session-1")
	require.NoError(t, err)
	assert.Same(t, replacement, got)
	assert.True(t, previous.IsCancelled())
	assert.False(t, replacement.IsCancelled())
}

func TestManagerGetSessionMissingReturnsError(t *testing.T) {
	manager := &Manager{sessions: make(map[acptypes.SessionID]*Session)}

	session, err := manager.GetSession("missing")

	assert.Nil(t, session)
	assert.ErrorContains(t, err, "session not found: missing")
}

func TestManagerCancel(t *testing.T) {
	t.Run("cancels existing session", func(t *testing.T) {
		session := &Session{ID: "session-1"}
		manager := &Manager{sessions: map[acptypes.SessionID]*Session{session.ID: session}}

		require.NoError(t, manager.Cancel(session.ID))

		assert.True(t, session.IsCancelled())
	})

	t.Run("returns error for missing session", func(t *testing.T) {
		manager := &Manager{sessions: make(map[acptypes.SessionID]*Session)}

		err := manager.Cancel("missing")

		assert.ErrorContains(t, err, "session not found: missing")
	})
}

func TestManagerCloseClosesSessionsStoreAndSharedMCPManager(t *testing.T) {
	store := &fakeConversationStore{}
	session := &Session{ID: "session-1", MCPManager: newEmptyMCPManager(t)}
	manager := &Manager{
		sessions: map[acptypes.SessionID]*Session{
			session.ID: session,
			"nil":      nil,
		},
		store:             store,
		kodeletMCPManager: newEmptyMCPManager(t),
	}

	require.NoError(t, manager.Close(context.Background()))

	assert.True(t, session.IsCancelled())
	assert.True(t, store.isClosed())
	assert.Empty(t, manager.sessions)
	assert.Nil(t, manager.store)
	assert.Nil(t, manager.kodeletMCPManager)

	got, err := manager.GetSession(session.ID)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, "session not found")
}

func TestManagerCloseReturnsStoreCloseError(t *testing.T) {
	expectedErr := errors.New("close store failed")
	manager := &Manager{
		sessions: make(map[acptypes.SessionID]*Session),
		store:    &fakeConversationStore{closeErr: expectedErr},
	}

	err := manager.Close(context.Background())

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, manager.store)
}

func TestBuildSessionMCPStateOptsFallbacks(t *testing.T) {
	originalSetup := setupMCPExecutionMode
	t.Cleanup(func() {
		setupMCPExecutionMode = originalSetup
	})

	t.Run("nil session MCP manager returns nil and skips setup", func(t *testing.T) {
		setupMCPExecutionMode = func(context.Context, *tools.MCPManager, string, string) (*mcp.ExecutionSetup, error) {
			t.Fatal("setupMCPExecutionMode should not be called for nil session MCP manager")
			return nil, nil
		}

		opts := (&Manager{}).buildSessionMCPStateOpts(context.Background(), "session-1", t.TempDir(), nil)

		assert.Nil(t, opts)
	})

	t.Run("direct mode falls back to MCP tools", func(t *testing.T) {
		setupMCPExecutionMode = func(context.Context, *tools.MCPManager, string, string) (*mcp.ExecutionSetup, error) {
			return nil, mcp.ErrDirectMode
		}

		opts := (&Manager{}).buildSessionMCPStateOpts(context.Background(), "session-1", t.TempDir(), newEmptyMCPManager(t))

		require.Len(t, opts, 1)
		assert.NotNil(t, opts[0])
	})

	t.Run("setup error falls back to MCP tools", func(t *testing.T) {
		setupMCPExecutionMode = func(context.Context, *tools.MCPManager, string, string) (*mcp.ExecutionSetup, error) {
			return &mcp.ExecutionSetup{StateOpts: []tools.BasicStateOption{func(context.Context, *tools.BasicState) error { return nil }}}, errors.New("setup failed")
		}

		opts := (&Manager{}).buildSessionMCPStateOpts(context.Background(), "session-1", t.TempDir(), newEmptyMCPManager(t))

		require.Len(t, opts, 1)
		assert.NotNil(t, opts[0])
	})

	t.Run("nil setup result falls back to MCP tools", func(t *testing.T) {
		setupMCPExecutionMode = func(context.Context, *tools.MCPManager, string, string) (*mcp.ExecutionSetup, error) {
			return nil, nil
		}

		opts := (&Manager{}).buildSessionMCPStateOpts(context.Background(), "session-1", t.TempDir(), newEmptyMCPManager(t))

		require.Len(t, opts, 1)
		assert.NotNil(t, opts[0])
	})
}
