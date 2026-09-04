package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRunner struct {
	req            chat.ChatRequest
	conversationID string
	err            error
}

type recordingExtensionResourceRuntimeProvider struct {
	ctx     context.Context
	cwd     string
	runtime *extensions.Runtime
	err     error
}

func (p *recordingExtensionResourceRuntimeProvider) RuntimeForCommandDiscovery(ctx context.Context, cwd string) (*extensions.Runtime, error) {
	p.ctx = ctx
	p.cwd = cwd
	return p.runtime, p.err
}

type remoteControlRecordingRunner struct {
	recordingRunner
	actions             []string
	steerMessage        string
	steerQueued         bool
	steerErr            error
	stopErr             error
	stoppedConversation string
	stoppedTurnID       string
}

func (r *remoteControlRecordingRunner) SteerConversation(_ context.Context, _ string, message string, _ []string) (bool, error) {
	r.actions = append(r.actions, "steer")
	r.steerMessage = message
	return r.steerQueued, r.steerErr
}

func (r *remoteControlRecordingRunner) StopConversation(_ context.Context, conversationID string) error {
	r.actions = append(r.actions, "stop")
	r.stoppedConversation = conversationID
	return r.stopErr
}

func (r *remoteControlRecordingRunner) StopConversationTurn(_ context.Context, conversationID, turnID string) error {
	r.actions = append(r.actions, "stop-turn")
	r.stoppedConversation = conversationID
	r.stoppedTurnID = turnID
	return r.stopErr
}

func (r *recordingRunner) Run(ctx context.Context, req chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
	r.req = req
	if err := sink.Send(chat.ChatEvent{Kind: "text", Delta: "streamed"}); err != nil {
		return "", err
	}
	return r.conversationID, r.err
}

func receiveRunMsg(t *testing.T, ch <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run message")
		return nil
	}
}

func executeRemoteStopCmd(t *testing.T, cmd tea.Cmd) remoteStopMsg {
	t.Helper()
	require.NotNil(t, cmd)
	switch message := cmd().(type) {
	case remoteStopMsg:
		return message
	case tea.BatchMsg:
		for _, child := range message {
			if child == nil {
				continue
			}
			if childMessage, ok := child().(remoteStopMsg); ok {
				return childMessage
			}
		}
	}
	t.Fatal("remote stop command did not produce a remoteStopMsg")
	return remoteStopMsg{}
}

func TestCancelActiveRunFinishesActiveBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	cancelled := false
	m.running = true
	m.activeRunID = 1
	m.cancelRun = func() { cancelled = true }
	m.entries = []chatEntry{
		{kind: entryUser, content: "go on"},
		{
			kind: entryAssistant,
			blocks: []assistantBlock{
				{
					kind: blockThoughts,
					thoughts: []thoughtBlock{{
						text: "still thinking",
						done: false,
					}},
				},
				{
					kind: blockTools,
					tools: []toolCall{{
						name: "bash",
						done: false,
					}},
				},
			},
		},
	}

	m.cancelActiveRun()
	content, _ := m.renderTranscript()

	assert.True(t, cancelled)
	assert.True(t, m.running)
	assert.True(t, m.runCancelling)
	assert.Equal(t, 1, m.activeRunID)
	assert.Equal(t, "cancelling", m.status)
	assert.False(t, hasActiveThought(m.entries[1].blocks[0]))
	assert.False(t, hasActiveTool(m.entries[1].blocks[1]))
	assert.Contains(t, content, "Had 1 Thought")
	assert.Contains(t, content, "Ran 1 command")
	assert.NotContains(t, content, "Thinking")
}

func TestRemoteCancelStopsControlPlaneBeforeClosingStream(t *testing.T) {
	runner := &remoteControlRecordingRunner{}
	m := newModel(context.Background(), Config{Runner: runner, Remote: true})
	t.Cleanup(m.cancel)
	m.running = true
	m.conversationID = "conversation-1"
	m.activeRunID = 1
	m.runs[1] = &conversationRun{conversationKey: m.activeConversationKey, turnID: "turn-1"}
	m.cancelRun = func() { runner.actions = append(runner.actions, "cancel-stream") }

	cmd := m.cancelActiveRun()
	stopMessage := executeRemoteStopCmd(t, cmd)
	assert.NoError(t, stopMessage.err)
	assert.Equal(t, []string{"stop-turn", "cancel-stream"}, runner.actions)
	assert.Equal(t, "conversation-1", runner.stoppedConversation)
	assert.Equal(t, "turn-1", runner.stoppedTurnID)
}

func TestStopSlashCommandStopsActiveRemoteTurn(t *testing.T) {
	runner := &remoteControlRecordingRunner{}
	m := newModel(context.Background(), Config{Runner: runner, Remote: true})
	t.Cleanup(m.cancel)
	m.running = true
	m.conversationID = "conversation-1"
	m.activeRunID = 1
	m.runs[1] = &conversationRun{conversationKey: m.activeConversationKey, turnID: "turn-1"}
	m.cancelRun = func() { runner.actions = append(runner.actions, "cancel-stream") }
	m.textarea.SetValue("/stop")

	stopMessage := executeRemoteStopCmd(t, m.submit())

	assert.NoError(t, stopMessage.err)
	assert.Equal(t, []string{"stop-turn", "cancel-stream"}, runner.actions)
	assert.True(t, m.runCancelling)
	assert.Empty(t, m.textarea.Value())
}

func TestRemoteCtrlCDetachesWithoutStoppingActiveTurn(t *testing.T) {
	runner := &remoteControlRecordingRunner{}
	m := newModel(context.Background(), Config{Runner: runner, Remote: true})
	m.running = true
	m.conversationID = "conversation-1"
	m.activeRunID = 1
	m.runs[1] = &conversationRun{conversationKey: m.activeConversationKey, turnID: "turn-1"}
	m.cancelRun = func() { runner.actions = append(runner.actions, "cancel-stream") }
	m.shortcutsOpen = true

	updated, cmd := m.Update(keyPressWithMod('c', tea.ModCtrl))
	m = updated.(model)

	assert.Empty(t, runner.actions)
	assert.False(t, m.runCancelling)
	assert.ErrorIs(t, m.ctx.Err(), context.Canceled)
	require.NotNil(t, cmd)
	_, ok := cmd().(tea.QuitMsg)
	assert.True(t, ok)
}

func TestRemoteEscapeDoesNotStopActiveTurn(t *testing.T) {
	runner := &remoteControlRecordingRunner{}
	m := newModel(context.Background(), Config{Runner: runner, Remote: true})
	t.Cleanup(m.cancel)
	m.running = true
	m.conversationID = "conversation-1"
	m.activeRunID = 1
	m.runs[1] = &conversationRun{conversationKey: m.activeConversationKey, turnID: "turn-1"}
	m.cancelRun = func() { runner.actions = append(runner.actions, "cancel-stream") }

	updated, cmd := m.Update(keyPress(tea.KeyEsc))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Empty(t, runner.actions)
	assert.False(t, m.runCancelling)
	assert.NoError(t, m.ctx.Err())
}

func TestRemoteCancelledCompletionDiscardsQueuedSlashFollowUps(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}, Remote: true})
	t.Cleanup(m.cancel)
	state := m.conversationState
	state.running = true
	state.activeRunID = 1
	state.queuedFollowUps = []string{"/goal should not run"}
	m.runs[1] = &conversationRun{conversationKey: state.key}
	m.runByState[state.key] = 1

	updated, _ := m.Update(chatEventMsg{
		runID:           1,
		conversationKey: state.key,
		event:           chat.ChatEvent{Kind: "done", ConversationID: state.conversationID, Cancelled: true},
	})
	m = updated.(model)
	updated, _ = m.Update(chatDoneMsg{runID: 1, conversationKey: state.key, conversationID: state.conversationID})
	m = updated.(model)

	assert.False(t, state.running)
	assert.Equal(t, "cancelled", state.status)
	assert.Empty(t, state.queuedFollowUps)
	assert.Zero(t, state.activeRunID)
}

func TestObservedRemoteStopFailureKeepsConversationStreamActive(t *testing.T) {
	m := newModel(context.Background(), Config{Remote: true})
	t.Cleanup(m.cancel)
	m.running = true
	m.runCancelling = true
	m.activeRunID = 1
	m.streamRunID = 1
	m.runs[1] = &conversationRun{conversationKey: m.activeConversationKey, observed: true}

	updated, _ := m.Update(remoteStopMsg{conversationKey: m.activeConversationKey, err: errors.New("stop failed")})
	m = updated.(model)

	assert.True(t, m.running)
	assert.False(t, m.runCancelling)
	assert.Equal(t, "cancellation failed", m.status)
	assert.ErrorContains(t, m.err, "stop failed")
}

func TestRemoteSubmitPinsTurnIDForScopedCancellation(t *testing.T) {
	runner := &remoteControlRecordingRunner{}
	m := newModel(context.Background(), Config{ConversationID: "conversation-1", Runner: runner, Remote: true})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("work")

	cmd := m.submit()
	require.NotNil(t, cmd)
	run := m.runs[m.activeRunID]
	require.NotNil(t, run)
	assert.NotEmpty(t, run.turnID)
	assert.Nil(t, cmd())
	receiveRunMsg(t, m.runCh)
	receiveRunMsg(t, m.runCh)
	assert.Equal(t, run.turnID, runner.req.TurnID)
}

func TestRemoteSteeringUsesControlPlaneRunner(t *testing.T) {
	runner := &remoteControlRecordingRunner{steerQueued: true}
	m := newModel(context.Background(), Config{Runner: runner, Remote: true})
	t.Cleanup(m.cancel)
	m.running = true
	m.conversationID = "conversation-1"
	m.textarea.SetValue(" focus on tests ")

	cmd := m.submitSteering()
	require.NotNil(t, cmd)
	message, ok := cmd().(remoteSteerMsg)
	require.True(t, ok)
	updated, _ := m.Update(message)
	m = updated.(model)

	assert.Equal(t, "focus on tests", runner.steerMessage)
	assert.Equal(t, []string{"steer"}, runner.actions)
	assert.Equal(t, []string{"focus on tests"}, m.queuedSteering)
	assert.Equal(t, "steering queued", m.status)
}

func TestRemoteModelUsesControlPlaneProfileSettings(t *testing.T) {
	m := newModel(context.Background(), Config{
		Remote:         true,
		Profile:        "work",
		ProfileOptions: []string{"default", "work"},
		ProfileSettings: map[string]ProfileSettings{
			"default": {ReasoningEffort: "low", ReasoningEffortOptions: []string{"low", "medium"}},
			"work":    {ReasoningEffort: "high", ReasoningEffortOptions: []string{"medium", "high"}},
		},
	})
	t.Cleanup(m.cancel)
	assert.Equal(t, "work", m.profile)
	assert.Equal(t, "high", m.reasoningEffort)
	assert.Equal(t, []string{"medium", "high"}, m.reasoningEffortOptions)

	m.profilePickerOpen = true
	m.selectProfilePickerOption(0)
	assert.Equal(t, "default", m.profile)
	assert.Equal(t, "low", m.reasoningEffort)
	assert.Equal(t, []string{"low", "medium"}, m.reasoningEffortOptions)
}

func TestCancelledRunFinishesOnDone(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.runCancelling = true

	updated, cmd := m.Update(chatDoneMsg{runID: 1, conversationID: "conv-1", err: context.Canceled})
	m = updated.(model)

	assert.NotNil(t, cmd)
	assert.False(t, m.running)
	assert.False(t, m.runCancelling)
	assert.Equal(t, 0, m.activeRunID)
	assert.Equal(t, "conv-1", m.conversationID)
	assert.Equal(t, "cancelled", m.status)
	assert.NoError(t, m.err)
}

func TestCtrlCAfterCancelQuitsAfterRunCleanup(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.runCancelling = true

	updated, cmd := m.Update(keyPressWithMod('c', tea.ModCtrl))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.True(t, m.running)
	assert.True(t, m.runCancelling)
	assert.True(t, m.quitAfterRun)
	assert.Equal(t, "exiting", m.status)

	updated, cmd = m.Update(chatDoneMsg{runID: 1, err: context.Canceled})
	m = updated.(model)

	assert.False(t, m.running)
	assert.False(t, m.runCancelling)
	assert.False(t, m.quitAfterRun)
	assert.Equal(t, "cancelled", m.status)
	require.NotNil(t, cmd)
	_, ok := cmd().(tea.QuitMsg)
	assert.True(t, ok)
}

func TestWaitForMsgAndInitCommands(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: t.TempDir()})
	t.Cleanup(m.cancel)

	cmd := waitForMsg(m.runCh)
	m.runCh <- chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text", Delta: "hello"}}
	_, ok := cmd().(chatEventMsg)
	assert.True(t, ok)

	close(m.runCh)
	assert.Nil(t, waitForMsg(m.runCh)())

	initMsg := m.Init()()
	batch, ok := initMsg.(tea.BatchMsg)
	require.True(t, ok)
	assert.Len(t, batch, 7)
}

func TestInitDefersSlashCommandsUntilResumedHistoryLoads(t *testing.T) {
	workspace := t.TempDir()
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", CWD: workspace})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })

	initMsg := m.Init()()
	batch, ok := initMsg.(tea.BatchMsg)
	require.True(t, ok)
	assert.Len(t, batch, 6)

	updated, cmd := m.Update(initialHistoryMsg{loaded: true, cwd: workspace})
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.False(t, m.initialHistoryPending)
	assert.True(t, m.extensionLifecyclePending)
}

func TestInitRequestsBackgroundColorOnlyForAutoTheme(t *testing.T) {
	workspace := t.TempDir()
	auto := newModel(context.Background(), Config{CWD: workspace, Theme: AutoThemeName})
	explicit := newModel(context.Background(), Config{CWD: workspace, Theme: DefaultThemeName})
	t.Cleanup(auto.cancel)
	t.Cleanup(explicit.cancel)

	autoBatch, ok := auto.Init()().(tea.BatchMsg)
	require.True(t, ok)
	explicitBatch, ok := explicit.Init()().(tea.BatchMsg)
	require.True(t, ok)

	assert.Len(t, autoBatch, len(explicitBatch)+1)
}

func TestSubmitWaitsForPendingStartupLifecycle(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-123"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Runner: runner})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.extensionLifecyclePending = true
	m.textarea.SetValue("continue")

	assert.Nil(t, m.submit())
	assert.Equal(t, "continue", m.submitAfterExtensionLifecycle)
	assert.False(t, m.running)
	assert.Equal(t, "continue", m.textarea.Value())

	updated, cmd := m.Update(extensionLifecycleMsg{conversationID: m.conversationID})
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.False(t, m.extensionLifecyclePending)
	assert.Empty(t, m.submitAfterExtensionLifecycle)
	assert.True(t, m.running)
	assert.Empty(t, m.textarea.Value())
}

func TestQueuedStartupSubmitPreservesLaterDraft(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-123"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Runner: runner})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.extensionLifecyclePending = true
	m.textarea.SetValue("first message")

	assert.Nil(t, m.submit())
	m.textarea.SetValue("next draft")
	updated, cmd := m.Update(extensionLifecycleMsg{conversationID: m.conversationID})
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.True(t, m.running)
	assert.Equal(t, "next draft", m.textarea.Value())
}

func TestHistoryFailureBlocksExtensionDiscovery(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", CWD: t.TempDir()})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })

	updated, cmd := m.Update(initialHistoryMsg{err: conversations.ErrCWDConflict})
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.True(t, m.extensionDiscoveryBlocked)

	updated, cmd = m.Update(slashCommandsMsg{cwd: m.slashCommandCWD()})
	m = updated.(model)
	assert.Nil(t, cmd)
}

func TestUpdateIgnoresStaleRunEvents(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{kind: entryUser, content: "first"}}
	m.activeRunID = 2
	m.running = true

	updated, _ := m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text", Delta: "stale"}})
	m = updated.(model)
	content, _ := m.renderTranscript()
	assert.NotContains(t, content, "stale")

	updated, _ = m.Update(chatEventMsg{runID: 2, event: chat.ChatEvent{Kind: "text", Delta: "fresh"}})
	m = updated.(model)
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "fresh")
}

func TestDoneFinishesActiveBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.entries = []chatEntry{
		{kind: entryUser, content: "go on"},
		{
			kind: entryAssistant,
			blocks: []assistantBlock{{
				kind:     blockThoughts,
				thoughts: []thoughtBlock{{text: "still thinking"}},
			}},
		},
	}

	updated, _ := m.Update(chatDoneMsg{runID: 1, conversationID: "conv-1"})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.False(t, m.running)
	assert.Equal(t, 0, m.activeRunID)
	assert.Equal(t, "conv-1", m.conversationID)
	assert.Equal(t, "ready", m.status)
	assert.False(t, hasActiveThought(m.entries[1].blocks[0]))
	assert.Contains(t, content, "Had 1 Thought")
	assert.NotContains(t, content, "Thinking")
}

func TestTextareaNewlineKeysInsertNewline(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "shift enter", msg: keyPressWithMod(tea.KeyEnter, tea.ModShift)},
		{name: "alt enter", msg: keyPressWithMod(tea.KeyEnter, tea.ModAlt)},
		{name: "ctrl j", msg: keyPressWithMod('j', tea.ModCtrl)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			m.textarea.SetValue("first line")

			updated, cmd := m.Update(tt.msg)
			m = updated.(model)

			require.NotNil(t, cmd)
			assert.Equal(t, "first line\n", m.textarea.Value())
			assert.Empty(t, m.entries)
		})
	}
}

func TestTextareaNewlineKeysReplaceSelection(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "shift enter", msg: keyPressWithMod(tea.KeyEnter, tea.ModShift)},
		{name: "alt enter", msg: keyPressWithMod(tea.KeyEnter, tea.ModAlt)},
		{name: "ctrl j", msg: keyPressWithMod('j', tea.ModCtrl)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			m.textarea.SetValue("replace me")
			m.textarea.SelectAll()
			require.True(t, m.textarea.HasSelection())

			updated, cmd := m.Update(tt.msg)
			m = updated.(model)

			require.NotNil(t, cmd)
			assert.Equal(t, "\n", m.textarea.Value())
			assert.False(t, m.textarea.HasSelection())
		})
	}
}

func TestTextareaNewlineKeysRespectMaxHeight(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "shift enter", msg: keyPressWithMod(tea.KeyEnter, tea.ModShift)},
		{name: "alt enter", msg: keyPressWithMod(tea.KeyEnter, tea.ModAlt)},
		{name: "ctrl j", msg: keyPressWithMod('j', tea.ModCtrl)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			m.textarea.MaxHeight = 2
			m.textarea.SetValue("first\nsecond")

			updated, cmd := m.Update(tt.msg)
			m = updated.(model)

			assert.Nil(t, cmd)
			assert.Equal(t, "first\nsecond", m.textarea.Value())
		})
	}
}

func TestRunningShiftEnterInsertsSteeringNewline(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue("first line")

	updated, cmd := m.Update(keyPressWithMod(tea.KeyEnter, tea.ModShift))
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.True(t, m.running)
	assert.Equal(t, "first line\n", m.textarea.Value())
	assert.Empty(t, m.queuedSteering)
}

func TestBracketedPasteInsertsMultilineComposerText(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("before ")

	updated, _ := m.Update(tea.PasteMsg{Content: "first\nsecond"})
	m = updated.(model)

	assert.Equal(t, "before first\nsecond", m.textarea.Value())
	assert.Empty(t, m.entries)
}

func TestCtrlOTogglesDetails(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind:     blockThoughts,
			thoughts: []thoughtBlock{{text: "toggle me", done: true}},
		}},
	}}
	widgetKey := extensionUIKey{owner: extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}, id: "status"}
	m.extensionWidgets[widgetKey] = tuiExtensionWidget{
		key:       widgetKey,
		placement: extensions.UIWidgetPlacementAboveComposer,
		frame: extensions.UIFrame{Sequence: 1, Lines: []extensions.UIFrameLine{
			{Spans: []extensions.UIStyledSpan{{Text: "Status"}}},
			{Spans: []extensions.UIStyledSpan{{Text: "widget detail"}}},
		}},
	}
	m.rebuildExtensionWidgetOrder()
	m.resize()

	updated, cmd := m.Update(keyPressWithMod('o', tea.ModCtrl))
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.Nil(t, cmd)
	assert.True(t, m.entries[0].blocks[0].expanded)
	assert.False(t, m.collapsedWidgets[widgetKey])
	assert.Contains(t, content, "toggle me")
	assert.Contains(t, xansi.Strip(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer)), "widget detail")

	updated, cmd = m.Update(keyPressWithMod('o', tea.ModCtrl))
	m = updated.(model)
	content, _ = m.renderTranscript()

	assert.Nil(t, cmd)
	assert.False(t, m.entries[0].blocks[0].expanded)
	assert.True(t, m.collapsedWidgets[widgetKey])
	assert.NotContains(t, content, "toggle me")
	assert.NotContains(t, xansi.Strip(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer)), "widget detail")
}

func TestQuestionMarkOpensShortcutsDialogWhenComposerEmpty(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	updated, cmd := m.Update(textKeyPress("?"))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.shortcutsOpen)
	assert.Contains(t, xansi.Strip(m.View().Content), "Shortcuts")

	updated, cmd = m.Update(keyPress(tea.KeyEsc))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.shortcutsOpen)

	m.textarea.SetValue("what")
	updated, _ = m.Update(textKeyPress("?"))
	m = updated.(model)
	assert.False(t, m.shortcutsOpen)
	assert.Equal(t, "what?", m.textarea.Value())
}

func TestApplyEditorResultUpdatesComposerAndCleansUpFile(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	path := filepath.Join(t.TempDir(), "draft.md")
	require.NoError(t, os.WriteFile(path, []byte("edited draft\n"), 0o644))

	cmd := m.applyEditorResult(editorFinishedMsg{path: path})

	assert.NotNil(t, cmd)
	assert.Equal(t, "edited draft", m.textarea.Value())
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestOpenComposerInEditorRequiresEditorAndIgnoresRunning(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")

	cmd := m.openComposerInEditor()

	assert.NotNil(t, cmd)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Equal(t, "Editor unavailable", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "Set $EDITOR or $VISUAL")

	m.steerError = ""
	m.uiNotifications = nil
	m.running = true
	cmd = m.openComposerInEditor()

	assert.NotNil(t, cmd)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Equal(t, "Editor unavailable", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "while Kodelet is running")
}

func TestOpenComposerInEditorCreatesDraftAndClearsTransientUI(t *testing.T) {
	m := newModel(context.Background(), Config{ProfileOptions: []string{"default", "work"}})
	t.Cleanup(m.cancel)
	beforeDrafts, err := filepath.Glob(filepath.Join(os.TempDir(), "kodelet-composer-*.md"))
	require.NoError(t, err)
	beforeDraftSet := map[string]struct{}{}
	for _, path := range beforeDrafts {
		beforeDraftSet[path] = struct{}{}
	}
	t.Cleanup(func() {
		afterDrafts, err := filepath.Glob(filepath.Join(os.TempDir(), "kodelet-composer-*.md"))
		if err != nil {
			return
		}
		for _, path := range afterDrafts {
			if _, ok := beforeDraftSet[path]; !ok {
				_ = os.Remove(path)
			}
		}
	})
	m.width = 80
	m.height = 24
	m.resize()
	m.textarea.SetValue("draft body")
	m.profilePickerOpen = true
	m.shortcutsOpen = true
	m.slashDismissedDraft = "dismissed"
	t.Setenv("EDITOR", "true")
	t.Setenv("VISUAL", "")

	cmd := m.openComposerInEditor()

	require.NotNil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.False(t, m.shortcutsOpen)
	assert.Empty(t, m.steerError)
	assert.Equal(t, "editing", m.status)
	msg := cmd()
	assert.NotNil(t, msg)
}

func TestWriteComposerEditorFileRoundTripsDraft(t *testing.T) {
	path, err := writeComposerEditorFile("draft body")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(path) })

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "draft body", string(content))
}

func TestEditorShortcutUsesCtrlGAndCtrlEPreservesLineEnd(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	m.textarea.SetValue("hello")
	m.textarea.SetCursorColumn(0)

	updated, _ := m.Update(keyPressWithMod('e', tea.ModCtrl))
	m = updated.(model)
	updated, _ = m.Update(textKeyPress("!"))
	m = updated.(model)

	assert.Equal(t, "hello!", m.textarea.Value())
	assert.Empty(t, m.steerError)

	updated, cmd := m.Update(keyPressWithMod('g', tea.ModCtrl))
	m = updated.(model)

	assert.NotNil(t, cmd)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Contains(t, m.uiNotifications[0].message, "Ctrl+G")
}

func TestEditorExecCommandParsesEditorArgs(t *testing.T) {
	cmd, err := editorExecCommand("vim -n", "/tmp/kodelet-draft.md")

	require.NoError(t, err)
	assert.Equal(t, "vim", filepath.Base(cmd.Path))
	assert.Equal(t, []string{"vim", "-n", "/tmp/kodelet-draft.md"}, cmd.Args)

	_, err = editorExecCommand("  ", "/tmp/kodelet-draft.md")
	assert.ErrorContains(t, err, "empty editor command")
	_, err = editorExecCommand("'unterminated", "/tmp/kodelet-draft.md")
	assert.Error(t, err)
}

func TestApplyEditorResultHandlesFailureAndReadError(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	failedPath := filepath.Join(t.TempDir(), "failed.md")
	require.NoError(t, os.WriteFile(failedPath, []byte("ignored"), 0o644))
	cmd := m.applyEditorResult(editorFinishedMsg{path: failedPath, err: errors.New("boom")})
	assert.NotNil(t, cmd)
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationError, m.uiNotifications[0].level)
	assert.Equal(t, "Editor failed", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "boom")
	_, err := os.Stat(failedPath)
	assert.True(t, os.IsNotExist(err))
	m.uiNotifications = nil

	notFoundPath := filepath.Join(t.TempDir(), "not-found.md")
	require.NoError(t, os.WriteFile(notFoundPath, []byte("ignored"), 0o644))
	cmd = m.applyEditorResult(editorFinishedMsg{path: notFoundPath, err: &exec.Error{Name: "missing-editor", Err: exec.ErrNotFound}})
	assert.NotNil(t, cmd)
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Equal(t, "Editor unavailable", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "not found")
	_, err = os.Stat(notFoundPath)
	assert.True(t, os.IsNotExist(err))
	m.uiNotifications = nil

	missingPath := filepath.Join(t.TempDir(), "missing.md")
	cmd = m.applyEditorResult(editorFinishedMsg{path: missingPath})
	assert.NotNil(t, cmd)
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationError, m.uiNotifications[0].level)
	assert.Equal(t, "Editor failed", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "Failed to read edited draft")
}

func TestCtrlTProfilePickerSelectsProfileForNewConversation(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "work", "prod"}, Runner: runner})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	updated, cmd := m.Update(keyPressWithMod('t', tea.ModCtrl))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.profilePickerOpen)
	assert.Equal(t, 0, m.profilePickerIndex)

	updated, cmd = m.Update(keyPress(tea.KeyDown))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, 1, m.profilePickerIndex)

	updated, cmd = m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "work", m.profile)

	m.textarea.SetValue("hello")
	runCmd := m.submit()
	require.NotNil(t, runCmd)
	assert.Nil(t, runCmd())
	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)
	assert.Equal(t, "work", runner.req.Profile)
}

func TestProfileSelectionRefreshesReasoningEffortOptions(t *testing.T) {
	withTUIViper(t, map[string]any{
		"provider":                  "openai",
		"model":                     "base-model",
		"reasoning_effort":          "medium",
		"allowed_reasoning_efforts": []string{"low", "medium"},
		"profiles": map[string]any{
			"work": map[string]any{
				"model":                     "work-model",
				"reasoning_effort":          "max",
				"allowed_reasoning_efforts": []string{"high", "max"},
			},
		},
	})
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "work"}})
	t.Cleanup(m.cancel)

	m.openProfilePicker()
	m.selectProfilePickerOption(1)

	assert.Equal(t, "work", m.profile)
	assert.Equal(t, "max", m.reasoningEffort)
	assert.Equal(t, []string{"high", "max"}, m.reasoningEffortOptions)
}

func TestEmptyAllowedReasoningEffortsUsesProviderOptions(t *testing.T) {
	withTUIViper(t, map[string]any{
		"provider":                  "openai",
		"model":                     "base-model",
		"reasoning_effort":          "medium",
		"allowed_reasoning_efforts": []string{"medium"},
		"profiles": map[string]any{
			"unrestricted": map[string]any{
				"provider":                  "anthropic",
				"model":                     "claude-test",
				"reasoning_effort":          "high",
				"allowed_reasoning_efforts": []string{},
			},
		},
	})
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "unrestricted"}})
	t.Cleanup(m.cancel)

	assert.Equal(t, []string{"medium"}, m.reasoningEffortOptions)
	assert.False(t, m.canChangeReasoningEffort())

	m.openProfilePicker()
	m.selectProfilePickerOption(1)

	assert.Equal(t, "unrestricted", m.profile)
	assert.Equal(t, "high", m.reasoningEffort)
	assert.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, m.reasoningEffortOptions)
	assert.True(t, m.canChangeReasoningEffort())
}

func TestProfileSelectionDropsUnsupportedExplicitReasoningEffort(t *testing.T) {
	withTUIViper(t, map[string]any{
		"provider":                  "openai",
		"model":                     "gpt-test",
		"reasoning_effort":          "medium",
		"allowed_reasoning_efforts": []string{},
		"profiles": map[string]any{
			"anthropic": map[string]any{
				"provider":                  "anthropic",
				"model":                     "claude-test",
				"reasoning_effort":          "high",
				"allowed_reasoning_efforts": []string{},
			},
		},
	})
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "anthropic"}})
	t.Cleanup(m.cancel)

	m.setReasoningEffort("minimal", true)
	assert.Equal(t, "minimal", m.reasoningEffort)

	m.openProfilePicker()
	m.selectProfilePickerOption(1)

	assert.Equal(t, "anthropic", m.profile)
	assert.Equal(t, "high", m.reasoningEffort)
	assert.False(t, m.reasoningEffortExplicit)
	assert.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, m.reasoningEffortOptions)
}

func TestSlashCommandKeyboardCompletion(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{
		{Name: "goal", Description: "Set the active goal"},
		{Name: "review", Description: "Review changes", Hint: "target"},
	}
	m.textarea.SetValue("/")
	m.resize()

	updated, cmd := m.Update(keyPress(tea.KeyDown))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, 0, m.slashCommandIndex)

	updated, cmd = m.Update(keyPress(tea.KeyDown))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, 1, m.slashCommandIndex)

	updated, cmd = m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, "/review ", m.textarea.Value())
	assert.Equal(t, -1, m.slashCommandIndex)
	assert.False(t, m.slashCommandSuggestionsOpen())
}

func TestSlashCommandTabSelectsFirstMatchAndPreservesIndent(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{
		{Name: "goal", Description: "Set the active goal"},
		{Name: "review", Description: "Review changes"},
	}
	m.textarea.SetValue("  /rev")
	m.resize()

	updated, cmd := m.Update(keyPress(tea.KeyTab))
	m = updated.(model)

	require.Nil(t, cmd)
	assert.Equal(t, "  /review ", m.textarea.Value())
}

func TestSlashCommandEscapeDismissesUntilDraftChanges(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{{Name: "goal", Description: "Set the active goal"}}
	m.textarea.SetValue("/")
	m.resize()
	require.True(t, m.slashCommandSuggestionsOpen())

	updated, cmd := m.Update(keyPress(tea.KeyEsc))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.slashCommandSuggestionsOpen())

	updated, cmd = m.Update(textKeyPress("g"))
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.Equal(t, "/g", m.textarea.Value())
	assert.True(t, m.slashCommandSuggestionsOpen())
}

func TestSlashCommandLoaderUsesRequestedCWDRecipes(t *testing.T) {
	workspace := t.TempDir()
	writeTUIRecipe(t, workspace, "workspace-only", `---
description: Workspace recipe
---
Body
`)

	commands, err := listBaseSlashCommands(context.Background(), workspace)

	require.NoError(t, err)
	assert.Contains(t, slashCommandNames(commands), "goal")
	assert.Contains(t, slashCommandNames(commands), "stop")
	assert.Contains(t, slashCommandNames(commands), "theme")
	assert.Contains(t, slashCommandNames(commands), "workspace-only")
}

func TestSlashCommandLoadCommandsAndCWDHelpers(t *testing.T) {
	workspace := t.TempDir()
	withTUIViper(t, map[string]any{
		"extensions.enabled":         false,
		"extensions.local_dir":       filepath.Join(workspace, ".kodelet", "extensions"),
		"extensions.global_dir":      filepath.Join(t.TempDir(), "global-extensions"),
		"extensions.max_output_size": 102400,
	})

	baseMsg, ok := loadSlashCommands(context.Background(), "  "+workspace+"  ")().(slashCommandsMsg)
	require.True(t, ok)
	assert.Equal(t, workspace, baseMsg.cwd)
	assert.NoError(t, baseMsg.err)
	assert.Contains(t, slashCommandNames(baseMsg.commands), "goal")
	assert.False(t, baseMsg.extensionsOnly)

	runtimeManager := extensions.NewRuntimeManager()
	t.Cleanup(func() { assert.NoError(t, runtimeManager.Close()) })
	extensionMsg, ok := loadExtensionSlashCommands(context.Background(), workspace, runtimeManager)().(slashCommandsMsg)
	require.True(t, ok)
	assert.Equal(t, workspace, extensionMsg.cwd)
	assert.True(t, extensionMsg.extensionsOnly)
	assert.NoError(t, extensionMsg.err)
	assert.Empty(t, extensionMsg.commands)
	assert.Empty(t, extensionMsg.shortcuts)

	m := newModel(context.Background(), Config{CWD: workspace})
	t.Cleanup(m.cancel)
	assert.Equal(t, workspace, m.slashCommandCWD())
	m.requestedCWD = "./requested"
	assert.Equal(t, "./requested", m.slashCommandCWD())
}

func TestListExtensionResourcesReleasesDiscoveryLease(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })
	provider := &recordingExtensionResourceRuntimeProvider{runtime: runtime}

	commands, shortcuts, err := listExtensionResources(t.Context(), workspace, provider)

	require.NoError(t, err)
	assert.Empty(t, commands)
	assert.Empty(t, shortcuts)
	assert.Equal(t, workspace, provider.cwd)
	require.NotNil(t, provider.ctx)
	assert.ErrorIs(t, provider.ctx.Err(), context.Canceled)
}

func TestEffectiveExtensionShortcutsFiltersReservedBindings(t *testing.T) {
	shortcuts := effectiveExtensionShortcuts(context.Background(), []extensions.Shortcut{
		{Key: "ctrl+c", Description: "Unsafe", ExtensionID: "first"},
		{Key: "ctrl+r", Description: "Refresh", ExtensionID: "second"},
		{Key: "F5", Description: "Rebuild", ExtensionID: "third"},
		{Key: "ctrl+i", Description: "Ambiguous", ExtensionID: "fourth"},
		{Key: "shift+r", Description: "Unrepresentable", ExtensionID: "fifth"},
	})

	assert.Equal(t, []extensions.Shortcut{
		{Key: "ctrl+r", Description: "Refresh", ExtensionID: "second"},
		{Key: "f5", Description: "Rebuild", ExtensionID: "third"},
	}, shortcuts)
}

func TestExtensionShortcutOverridesBuiltInComposerBinding(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.extensionShortcuts = []extensions.Shortcut{{Key: "ctrl+r", Description: "Refresh", ExtensionID: "workspace"}}

	updated, cmd := m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.Nil(t, m.historySearch)
	require.Len(t, m.shortcutCalls, 1)
	for _, call := range m.shortcutCalls {
		call.cancel()
	}
}

func TestExtensionShortcutDispatchesSupportedTeaKeyMessages(t *testing.T) {
	for _, test := range []struct {
		name string
		key  string
		msg  tea.KeyPressMsg
	}{
		{name: "control letter", key: "ctrl+r", msg: keyPressWithMod('r', tea.ModCtrl)},
		{name: "alt letter", key: "alt+p", msg: textKeyPressWithMod("p", tea.ModAlt)},
		{name: "alt digit", key: "alt+5", msg: textKeyPressWithMod("5", tea.ModAlt)},
		{name: "control alt letter", key: "ctrl+alt+r", msg: keyPressWithMod('r', tea.ModCtrl|tea.ModAlt)},
		{name: "function key", key: "f5", msg: keyPress(tea.KeyF5)},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
			m.extensionShortcuts = []extensions.Shortcut{{Key: test.key, Description: "Action", ExtensionID: "workspace"}}

			updated, cmd := m.Update(test.msg)
			m = updated.(model)

			require.NotNil(t, cmd)
			require.Len(t, m.shortcutCalls, 1)
			for _, call := range m.shortcutCalls {
				call.cancel()
			}
		})
	}
}

func TestExtensionShortcutCommandExecutesWithResolvedContext(t *testing.T) {
	workspace := t.TempDir()
	extensionRoot := t.TempDir()
	contextPath := filepath.Join(t.TempDir(), "shortcut-context.json")
	runner := &recordingRunner{conversationID: "conversation-shortcut"}
	writeTUIShortcutExtension(t, extensionRoot)
	t.Setenv("KODELET_TUI_SHORTCUT_CONTEXT_PATH", contextPath)
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	withTUIViper(t, map[string]any{
		"provider":                   "anthropic",
		"model":                      "claude-test",
		"recipe_name":                "review",
		"extensions.enabled":         true,
		"extensions.local_dir":       extensionRoot,
		"extensions.global_dir":      t.TempDir(),
		"extensions.max_output_size": 102400,
	})

	m := newModel(t.Context(), Config{CWD: workspace, Profile: "default", Runner: runner})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.extensionShortcuts = []extensions.Shortcut{{Key: "ctrl+r", Description: "Refresh", ExtensionID: "shortcut-test"}}

	updated, cmd := m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	require.NotNil(t, cmd)
	done, ok := cmd().(extensionShortcutDoneMsg)
	require.True(t, ok)
	require.NoError(t, done.err)
	assert.True(t, done.matched)
	assert.Equal(t, &extensions.ShortcutResult{Action: extensions.ShortcutActionSubmit, Message: "/dictate"}, done.result)

	payload, err := os.ReadFile(contextPath)
	require.NoError(t, err)
	var request struct {
		Key     string                          `json:"key"`
		Context extensions.ExtensionCallContext `json:"context"`
	}
	require.NoError(t, json.Unmarshal(payload, &request))
	assert.Equal(t, "ctrl+r", request.Key)
	assert.Equal(t, workspace, request.Context.CWD)
	assert.Equal(t, "anthropic", request.Context.Provider)
	assert.Equal(t, "claude-test", request.Context.Model)
	assert.Equal(t, "default", request.Context.Profile)
	assert.Equal(t, "review", request.Context.RecipeName)
	assert.Equal(t, "main", request.Context.InvokedBy)
	assert.Equal(t, m.key, request.Context.UIScopeID)

	updated, followUp := m.Update(done)
	m = updated.(model)
	require.NotNil(t, followUp)
	assert.Empty(t, m.shortcutCalls)
	assert.True(t, m.running)
	require.Len(t, m.entries, 1)
	assert.Equal(t, chatEntry{kind: entryUser, content: "/dictate"}, m.entries[0])
}

func TestExtensionShortcutSubmitPreservesComposerAndQueuesDuringRun(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.running = true
	m.textarea.SetValue("keep this draft")
	m.shortcutCalls[7] = &extensionShortcutCall{conversationKey: m.key}

	updated, cmd := m.Update(extensionShortcutDoneMsg{
		callID:          7,
		conversationKey: m.key,
		key:             "ctrl+alt+r",
		extensionID:     "dictate",
		matched:         true,
		result:          &extensions.ShortcutResult{Action: extensions.ShortcutActionSubmit, Message: "/dictate"},
	})
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Equal(t, "keep this draft", m.textarea.Value())
	assert.Equal(t, []string{"/dictate"}, m.queuedFollowUps)
	assert.Equal(t, "command queued", m.status)
}

func TestExtensionShortcutSubmitDefersUntilLifecycleWithoutClearingDraft(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.extensionLifecyclePending = true
	m.textarea.SetValue("keep this draft")
	m.shortcutCalls[7] = &extensionShortcutCall{conversationKey: m.key}

	updated, cmd := m.Update(extensionShortcutDoneMsg{
		callID:          7,
		conversationKey: m.key,
		key:             "ctrl+alt+r",
		extensionID:     "dictate",
		matched:         true,
		result:          &extensions.ShortcutResult{Action: extensions.ShortcutActionSubmit, Message: "/dictate"},
	})
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Equal(t, "/dictate", m.submitAfterExtensionLifecycle)
	assert.True(t, m.submitAfterExtensionLifecyclePreserveComposer)
	assert.Equal(t, "keep this draft", m.textarea.Value())
}

func TestExtensionShortcutSubmitQueuesWhenLifecycleAlreadyHasDeferredMessage(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.extensionLifecyclePending = true
	m.submitAfterExtensionLifecycle = "first message"
	m.shortcutCalls[7] = &extensionShortcutCall{conversationKey: m.key}

	updated, cmd := m.Update(extensionShortcutDoneMsg{
		callID:          7,
		conversationKey: m.key,
		key:             "ctrl+alt+r",
		extensionID:     "dictate",
		matched:         true,
		result:          &extensions.ShortcutResult{Action: extensions.ShortcutActionSubmit, Message: "/dictate"},
	})
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Equal(t, "first message", m.submitAfterExtensionLifecycle)
	assert.Equal(t, []string{"/dictate"}, m.queuedFollowUps)
	assert.Equal(t, "command queued", m.status)
}

func TestExtensionShortcutRejectsInvalidSubmitResult(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.shortcutCalls[7] = &extensionShortcutCall{conversationKey: m.key}

	updated, cmd := m.Update(extensionShortcutDoneMsg{
		callID:          7,
		conversationKey: m.key,
		key:             "ctrl+alt+r",
		extensionID:     "dictate",
		matched:         true,
		result:          &extensions.ShortcutResult{Action: extensions.ShortcutActionSubmit},
	})
	m = updated.(model)

	require.NotNil(t, cmd)
	require.NotEmpty(t, m.uiNotifications)
	assert.Equal(t, "Extension shortcut failed", m.uiNotifications[len(m.uiNotifications)-1].title)
	assert.Contains(t, m.uiNotifications[len(m.uiNotifications)-1].message, "empty submit message")
}

func TestExtensionShortcutDoesNotOverrideActiveSlashSuggestions(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.extensionShortcuts = []extensions.Shortcut{{Key: "ctrl+r", Description: "Refresh", ExtensionID: "workspace"}}
	m.slashCommands = []slashcommands.Command{{Name: "review", Description: "Review code"}}
	m.textarea.SetValue("/")
	require.True(t, m.slashCommandSuggestionsOpen())

	updated, _ := m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)

	assert.NotNil(t, m.historySearch)
	assert.Empty(t, m.shortcutCalls)
}

func TestExtensionShortcutCompletionReportsErrorsWithoutFailingConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.shortcutCalls[7] = &extensionShortcutCall{conversationKey: m.key}

	updated, cmd := m.Update(extensionShortcutDoneMsg{
		callID:          7,
		conversationKey: m.key,
		key:             "ctrl+r",
		extensionID:     "workspace",
		matched:         true,
		err:             errors.New("refresh failed"),
	})
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.Empty(t, m.shortcutCalls)
	assert.Nil(t, m.err)
	assert.Equal(t, "ready", m.status)
	require.NotEmpty(t, m.uiNotifications)
	assert.Equal(t, "Extension shortcut failed", m.uiNotifications[len(m.uiNotifications)-1].title)
	assert.Contains(t, m.uiNotifications[len(m.uiNotifications)-1].message, "workspace")
}

func TestSlashCommandLoaderErrorsForInvalidCWD(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	baseCommands, err := listBaseSlashCommands(context.Background(), missing)
	assert.ErrorContains(t, err, "cwd directory does not exist")
	assert.Contains(t, slashCommandNames(baseCommands), "goal")
	assert.Contains(t, slashCommandNames(baseCommands), "stop")
	assert.Contains(t, slashCommandNames(baseCommands), "theme")
}

func slashCommandNames(commands []slashcommands.Command) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return names
}

func writeTUIRecipe(t *testing.T, workspace, name, content string) {
	t.Helper()
	recipeDir := filepath.Join(workspace, ".kodelet", "recipes")
	require.NoError(t, os.MkdirAll(recipeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipeDir, name+".md"), []byte(content), 0o644))
}

func TestTUIShortcutExtensionHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_TUI_SHORTCUT_HELPER") != "1" {
		return
	}
	runTUIShortcutExtensionHelper()
	os.Exit(0)
}

func writeTUIShortcutExtension(t *testing.T, root string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\nKODELET_TUI_SHORTCUT_HELPER=1 exec %q -test.run TestTUIShortcutExtensionHelperProcess --\n", executable)
	path := filepath.Join(root, "kodelet-extension-shortcut-test")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func runTUIShortcutExtensionHelper() {
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readTUIShortcutExtensionFrame(reader)
		if err != nil {
			return
		}
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(payload, &request) != nil {
			return
		}

		var result any
		switch request.Method {
		case "extension.initialize":
			result = map[string]any{
				"name": "shortcut-test",
				"shortcuts": []map[string]any{{
					"key":         "ctrl+r",
					"description": "Refresh",
				}},
			}
		case "extension.shortcut.execute":
			if path := os.Getenv("KODELET_TUI_SHORTCUT_CONTEXT_PATH"); path != "" {
				_ = os.WriteFile(path, request.Params, 0o644)
			}
			result = extensions.ShortcutResult{Action: extensions.ShortcutActionSubmit, Message: "/dictate"}
		default:
			result = nil
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response)
	}
}

func readTUIShortcutExtensionFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func withTUIViper(t *testing.T, values map[string]any) {
	t.Helper()
	snapshot := viper.AllSettings()
	viper.Reset()
	for key, value := range values {
		viper.Set(key, value)
	}
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range snapshot {
			viper.Set(key, value)
		}
	})
}

func TestClickProfilePickerSelectsProfileForNewConversation(t *testing.T) {
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "work", "prod"}})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	profileStart, _, ok := m.profileLabelBoundsInBlock()
	require.True(t, ok)
	updated, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin + profileStart,
		Y:      m.viewport.Height(),
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.profilePickerOpen)

	pickerStart, _, ok := m.profilePickerBoundsInBlock()
	require.True(t, ok)
	updated, cmd = m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin + pickerStart,
		Y:      m.viewport.Height() + 2,
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "prod", m.profile)
}

func TestClickReasoningPickerSelectsEffortForNewConversation(t *testing.T) {
	m := newModel(context.Background(), Config{
		ReasoningEffort:        "medium",
		ReasoningEffortOptions: []string{"low", "medium", "high"},
	})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 24
	m.resize()

	effortStart, _, ok := m.reasoningEffortLabelBoundsInBlock()
	require.True(t, ok)
	updated, cmd := m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin + effortStart,
		Y:      m.viewport.Height(),
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.reasoningPickerOpen)

	pickerStart, _, ok := m.reasoningPickerBoundsInBlock()
	require.True(t, ok)
	updated, cmd = m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin + pickerStart,
		Y:      m.viewport.Height() + 2,
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.reasoningPickerOpen)
	assert.Equal(t, "high", m.reasoningEffort)
}

func TestProfilePickerLockedForExistingConversation(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Profile: "work", ProfileOptions: []string{"default", "work", "prod"}})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	updated, cmd := m.Update(keyPressWithMod('t', tea.ModCtrl))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "work", m.profile)

	profileStart, _, ok := m.profileLabelBoundsInBlock()
	require.True(t, ok)
	updated, cmd = m.Update(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin + profileStart,
		Y:      m.viewport.Height(),
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "work", m.profile)
}

func TestProfilePickerToggleCloseAndWrap(t *testing.T) {
	m := newModel(context.Background(), Config{Profile: "work", ProfileOptions: []string{"default", "work", "prod"}})
	t.Cleanup(m.cancel)

	m.toggleProfilePickerFromKeyboard()
	require.True(t, m.profilePickerOpen)
	m.moveProfilePicker(-1)
	assert.Equal(t, 0, m.profilePickerIndex)
	m.moveProfilePicker(-1)
	assert.Equal(t, 2, m.profilePickerIndex)
	m.toggleProfilePickerFromKeyboard()
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "prod", m.profile)

	m.toggleProfilePickerFromClick()
	require.True(t, m.profilePickerOpen)
	m.closeProfilePicker()
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, m.profileIndex, m.profilePickerIndex)
}

func TestTypingInComposerDoesNotMoveViewport(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)
	bottomOffset := m.viewport.YOffset()
	require.Greater(t, bottomOffset, 0)

	updated, _ := m.Update(keyPress(tea.KeyPgUp))
	m = updated.(model)
	scrolledOffset := m.viewport.YOffset()
	require.Less(t, scrolledOffset, bottomOffset)

	updated, _ = m.Update(textKeyPress("x"))
	m = updated.(model)
	assert.Equal(t, scrolledOffset, m.viewport.YOffset())
	assert.Equal(t, "x", m.textarea.Value())

	updated, _ = m.Update(keyPress(tea.KeyDown))
	m = updated.(model)
	assert.Equal(t, scrolledOffset, m.viewport.YOffset())

	updated, _ = m.Update(keyPressWithMod('u', tea.ModCtrl))
	m = updated.(model)
	assert.Equal(t, scrolledOffset, m.viewport.YOffset())
	assert.Empty(t, m.textarea.Value())

	updated, _ = m.Update(keyPress(tea.KeyPgDown))
	m = updated.(model)
	assert.Greater(t, m.viewport.YOffset(), scrolledOffset)
}

func TestHorizontalViewportMouseNavigation(t *testing.T) {
	assert.True(t, isHorizontalViewportMouseNavigation(tea.MouseWheelMsg{Button: tea.MouseWheelLeft}))
	assert.True(t, isHorizontalViewportMouseNavigation(tea.MouseWheelMsg{Button: tea.MouseWheelRight}))
	assert.True(t, isHorizontalViewportMouseNavigation(tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModShift}))
	assert.True(t, shouldUpdateViewport(tea.MouseWheelMsg{Button: tea.MouseWheelDown, Mod: tea.ModShift}))
	assert.False(t, isHorizontalViewportMouseNavigation(tea.MouseReleaseMsg{Button: tea.MouseWheelLeft}))
	assert.False(t, isHorizontalViewportMouseNavigation(tea.MouseClickMsg{Button: tea.MouseLeft}))
}

func TestSubmitStartsRunAndStreamsRunnerMessages(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Profile: "work", CWD: "/tmp", Runner: runner})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.textarea.SetValue(" hello ")

	cmd := m.submit()
	require.NotNil(t, cmd)

	assert.True(t, m.running)
	assert.Equal(t, 1, m.activeRunID)
	assert.Equal(t, "working", m.status)
	assert.Empty(t, m.textarea.Value())
	require.Len(t, m.entries, 1)
	assert.Equal(t, chatEntry{kind: entryUser, content: "hello"}, m.entries[0])

	assert.Nil(t, cmd())

	event, ok := receiveRunMsg(t, m.runCh).(chatEventMsg)
	require.True(t, ok)
	assert.Equal(t, 1, event.runID)
	assert.Equal(t, "text", event.event.Kind)
	assert.Equal(t, "streamed", event.event.Delta)

	done, ok := receiveRunMsg(t, m.runCh).(chatDoneMsg)
	require.True(t, ok)
	assert.Equal(t, 1, done.runID)
	assert.Equal(t, "conversation-done", done.conversationID)
	assert.NoError(t, done.err)

	assert.Equal(t, chat.ChatRequest{
		Message:        "hello",
		ConversationID: "conversation-123",
		Profile:        "work",
		CWD:            "/tmp",
	}, runner.req)
}

func TestCtrlYReasoningPickerSelectsEffortForNewConversation(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{
		Profile:                "default",
		ProfileOptions:         []string{"default", "work"},
		ReasoningEffort:        "medium",
		ReasoningEffortOptions: []string{"low", "medium", "high"},
		Runner:                 runner,
	})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	updated, cmd := m.Update(keyPressWithMod('y', tea.ModCtrl))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.reasoningPickerOpen)
	assert.Equal(t, 1, m.reasoningPickerIndex)

	updated, cmd = m.Update(keyPress(tea.KeyDown))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, 2, m.reasoningPickerIndex)

	updated, cmd = m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.reasoningPickerOpen)
	assert.Equal(t, "high", m.reasoningEffort)

	m.textarea.SetValue("hello")
	runCmd := m.submit()
	require.NotNil(t, runCmd)
	assert.Nil(t, runCmd())
	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)
	assert.Equal(t, "high", runner.req.ReasoningEffort)
}

func TestReasoningPickerLockedForExistingConversation(t *testing.T) {
	m := newModel(context.Background(), Config{
		ConversationID:         "conversation-123",
		ReasoningEffort:        "high",
		ReasoningEffortOptions: []string{"low", "high"},
	})
	t.Cleanup(m.cancel)

	assert.False(t, m.canChangeReasoningEffort())
	updated, cmd := m.Update(keyPressWithMod('y', tea.ModCtrl))
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.reasoningPickerOpen)
	assert.Equal(t, "high", m.reasoningEffort)
}

func TestSubmitRecordsAndPersistsRawMessageHistory(t *testing.T) {
	basePath := t.TempDir()
	workspace := t.TempDir()
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Profile: "work", CWD: workspace, Runner: runner})
	t.Cleanup(m.cancel)
	m.messageHistoryStore = messagehistory.NewStoreWithBasePath(basePath)
	m.messageHistoryScopeCWD = workspace
	m.width = 100
	m.height = 30
	m.resize()
	m.textarea.SetValue(" /goal ship raw history ")

	cmd := m.submit()
	require.NotNil(t, cmd)

	assert.Equal(t, []string{"/goal ship raw history"}, m.messageHistory)
	assert.Equal(t, chatEntry{kind: entryUser, content: "Objective: ship raw history"}, m.entries[0])

	assert.Nil(t, cmd())
	entries, err := m.messageHistoryStore.List(context.Background(), workspace, messagehistory.MaxEntriesPerScope)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "/goal ship raw history", entries[0].Text)
	assert.Equal(t, "conversation-123", entries[0].ConversationID)
	assert.Equal(t, "work", entries[0].Profile)
}

func TestHistorySearchOpensSearchesCyclesAcceptsAndCancels(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: t.TempDir()})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.messageHistory = []string{
		"run unit tests",
		"check git status",
		"run frontend tests",
		"run unit tests",
		"how many cores and rams on this machine?",
	}
	m.textarea.SetValue("draft")
	m.resize()

	updated, cmd := m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	assert.Nil(t, cmd)
	require.NotNil(t, m.historySearch)
	assert.Equal(t, "draft", m.textarea.Value())
	assert.Equal(t, 1, m.historySearchHeight())

	updated, _ = m.Update(textKeyPress("run"))
	m = updated.(model)
	assert.Equal(t, "run unit tests", m.textarea.Value())
	require.NotNil(t, m.historySearch)
	assert.Equal(t, []string{"run unit tests", "run frontend tests"}, m.historySearch.matches)

	updated, _ = m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	assert.Equal(t, "run frontend tests", m.textarea.Value())

	updated, _ = m.Update(keyPress(tea.KeyDown))
	m = updated.(model)
	assert.Equal(t, "run unit tests", m.textarea.Value())

	updated, _ = m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	assert.Equal(t, "run frontend tests", m.textarea.Value())

	updated, _ = m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)
	assert.Nil(t, m.historySearch)
	assert.Equal(t, "run frontend tests", m.textarea.Value())

	m.textarea.SetValue("another draft")
	updated, _ = m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	updated, _ = m.Update(textKeyPress("missing"))
	m = updated.(model)
	assert.Equal(t, "another draft", m.textarea.Value())
	updated, _ = m.Update(keyPress(tea.KeyEsc))
	m = updated.(model)
	assert.Nil(t, m.historySearch)
	assert.Equal(t, "another draft", m.textarea.Value())

	updated, _ = m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	updated, _ = m.Update(textKeyPress("many"))
	m = updated.(model)
	updated, _ = m.Update(textKeyPress(" "))
	m = updated.(model)
	updated, _ = m.Update(textKeyPress("cores"))
	m = updated.(model)
	assert.Equal(t, "many cores", m.historySearch.query)
	assert.Equal(t, "how many cores and rams on this machine?", m.textarea.Value())
}

func TestHistorySearchPasteAlwaysInsertsText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "enter", content: "enter", want: "prefix-enter"},
		{name: "escape", content: "esc", want: "prefix-esc"},
		{name: "up", content: "up", want: "prefix-up"},
		{name: "down", content: "down", want: "prefix-down"},
		{name: "backspace", content: "backspace", want: "prefix-backspace"},
		{name: "clear", content: "ctrl+u", want: "prefix-ctrl+u"},
		{name: "multiline", content: " first\r\nsecond\tthird ", want: "prefix-first second third"},
		{name: "terminal controls", content: " \x1b[31mred\x1b[0m\x07\x00 alert ", want: "prefix-red alert"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
			m.textarea.SetValue("draft")
			m.historySearch = &historySearchState{originalDraft: "draft", query: "prefix-"}

			updated, cmd := m.Update(tea.PasteMsg{Content: tt.content})
			m = updated.(model)

			assert.Nil(t, cmd)
			require.NotNil(t, m.historySearch)
			assert.Equal(t, tt.want, m.historySearch.query)
			assert.Equal(t, "draft", m.textarea.Value())
		})
	}
}

func TestHistorySearchRenderingUsesSingleThemedLine(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: t.TempDir(), Theme: "tokyo-night"})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.messageHistory = []string{"run themed tests"}
	m.resize()

	updated, _ := m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	updated, _ = m.Update(textKeyPress("theme"))
	m = updated.(model)

	rendered := m.renderHistorySearch()
	assert.Contains(t, rendered, "reverse-i-search:")
	assert.Contains(t, rendered, "theme")
	assert.NotContains(t, rendered, "1/1")
	assert.NotContains(t, rendered, "run themed tests")
	assert.NotContains(t, rendered, "\n")
	labelStart, _ := styleSequences(historySearchLabelStyle)
	assert.Contains(t, rendered, labelStart)
	queryStart, _ := styleSequences(historySearchQueryStyle)
	assert.Contains(t, rendered, queryStart)

	updated, _ = m.Update(textKeyPress(" missing"))
	m = updated.(model)
	rendered = m.renderHistorySearch()
	assert.Contains(t, rendered, "no matches")
	errorStart, _ := styleSequences(historySearchErrorStyle)
	assert.Contains(t, rendered, errorStart)
}

func TestSubmitGoalSlashCommandDisplaysObjectiveImmediately(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Runner: runner})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.textarea.SetValue("/goal run ls -la")

	cmd := m.submit()
	require.NotNil(t, cmd)
	require.Len(t, m.entries, 1)
	assert.Equal(t, chatEntry{kind: entryUser, content: "Objective: run ls -la"}, m.entries[0])

	assert.Nil(t, cmd())
	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)
	assert.Equal(t, "/goal run ls -la", runner.req.Message)
}

func TestSubmitWithDefaultRunnerKeepsRelativeCWDAsRequestOnly(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	capturedDefaultCWD := "unset"
	previous := newDefaultChatRunner
	newDefaultChatRunner = func(defaultCWD string, _ chat.ExtensionRuntimeProvider) chat.ChatRunner {
		capturedDefaultCWD = defaultCWD
		return runner
	}
	t.Cleanup(func() {
		newDefaultChatRunner = previous
	})

	m := newModel(context.Background(), Config{ConversationID: "conversation-123", CWD: "./backend"})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("hello")

	cmd := m.submit()
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())

	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)

	assert.Empty(t, capturedDefaultCWD)
	assert.Equal(t, "./backend", runner.req.CWD)
}

func TestSubmitResumedChatWithoutExplicitCWDDoesNotSendCurrentDirectory(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Runner: runner})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("hello")

	cmd := m.submit()
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())

	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)

	assert.Equal(t, "conversation-123", runner.req.ConversationID)
	assert.Empty(t, runner.req.CWD)
	assert.NotEmpty(t, m.cwd)
}

func TestSubmitIgnoresEmptyComposer(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("   ")

	cmd := m.submit()

	assert.Nil(t, cmd)
	assert.False(t, m.running)
	assert.Empty(t, m.entries)
	assert.Empty(t, m.conversationID)
}

func TestSlashCommandIndexMovementAndMergeHelpers(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.slashCommands = []slashcommands.Command{{Name: "goal", Description: "Set goal"}, {Name: "review", Description: "Review"}}
	m.textarea.SetValue("/")

	m.resetSlashCommandIndex()
	assert.Equal(t, -1, m.slashCommandIndex)
	m.slashCommandIndex = 4
	m.resetSlashCommandIndex()
	assert.Equal(t, -1, m.slashCommandIndex)
	m.moveSlashCommandSelection(-1)
	assert.Equal(t, 1, m.slashCommandIndex)
	m.moveSlashCommandSelection(-1)
	assert.Equal(t, 0, m.slashCommandIndex)
	m.moveSlashCommandSelection(-1)
	assert.Equal(t, -1, m.slashCommandIndex)

	m.textarea.SetValue("no slash")
	m.moveSlashCommandSelection(1)
	assert.Equal(t, -1, m.slashCommandIndex)

	merged := mergeSlashCommands(
		[]slashcommands.Command{{Name: "goal"}, {Name: " "}, {Name: "review"}},
		[]slashcommands.Command{{Name: "review"}, {Name: "custom"}, {Name: ""}},
	)
	assert.Equal(t, []string{"goal", "review", "custom"}, slashCommandNames(merged))
}

func TestUserDisplayMessageFallsBackForInvalidGoalCommand(t *testing.T) {
	invalidGoal := "  /goal   "
	assert.Equal(t, strings.TrimSpace(invalidGoal), userDisplayMessage(invalidGoal))
}

func TestStreamingDeltasAreDebouncedBeforeViewportRefresh(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.refreshViewport(true)
	initialContent := m.viewport.View()

	updated, cmd := m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text-delta", Delta: "**hello**"}})
	m = updated.(model)

	require.NotNil(t, cmd)
	require.True(t, m.pendingRefresh)
	require.Len(t, m.entries, 1)
	assert.Equal(t, "**hello**", m.entries[0].blocks[0].text)
	assert.Equal(t, initialContent, m.viewport.View())

	updated, _ = m.Update(transcriptRefreshMsg{})
	m = updated.(model)

	assert.False(t, m.pendingRefresh)
	assert.Contains(t, xansi.Strip(m.viewport.View()), "hello")
}

func TestStreamingPreservesViewportAfterUserScrollsUp(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)
	bottomOffset := m.viewport.YOffset()
	require.Greater(t, bottomOffset, 0)
	require.True(t, m.autoFollow)

	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = updated.(model)
	scrolledOffset := m.viewport.YOffset()
	require.Less(t, scrolledOffset, bottomOffset)
	assert.False(t, m.autoFollow)

	m.running = true
	m.activeRunID = 1
	updated, _ = m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text-delta", Delta: "\nstill streaming"}})
	m = updated.(model)

	assert.Equal(t, scrolledOffset, m.viewport.YOffset())
	assert.False(t, m.autoFollow)
}

func TestScrollingBackToBottomResumesStreamingAutoFollow(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)

	updated, _ := m.Update(keyPress(tea.KeyPgUp))
	m = updated.(model)
	require.False(t, m.autoFollow)
	require.False(t, m.viewport.AtBottom())

	for range 10 {
		updated, _ = m.Update(keyPress(tea.KeyPgDown))
		m = updated.(model)
		if m.viewport.AtBottom() {
			break
		}
	}
	require.True(t, m.viewport.AtBottom())
	require.True(t, m.autoFollow)

	m.running = true
	m.activeRunID = 1
	updated, _ = m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text-delta", Delta: "\nnew bottom line"}})
	m = updated.(model)

	assert.True(t, m.viewport.AtBottom())
	assert.True(t, m.autoFollow)
}
