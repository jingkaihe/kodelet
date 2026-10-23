package tui

import (
	"context"
	"encoding/binary"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/audiohost"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAudioCompactStatus(t *testing.T) {
	for _, tt := range []struct {
		name     string
		mode     string
		muted    bool
		status   string
		controls string
	}{
		{
			name: "dictation", mode: "voice",
			status:   "Dictation · recording",
			controls: "▰▰▰▰▰▱▱▱▱▱ · Enter: finish · Esc: cancel",
		},
		{
			name: "talk", mode: "talk",
			status:   "Talk · recording",
			controls: "Ctrl+Alt+M: mute · Esc: end",
		},
		{
			name: "muted talk", mode: "talk", muted: true,
			status:   "Talk · recording (muted)",
			controls: "Ctrl+Alt+M: unmute · Esc: end",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			assert.Empty(t, m.renderAudio())
			assert.Zero(t, m.audioHeight())
			m.audio = &tuiAudioSession{
				mode: tt.mode, phase: "recording", started: time.Now(),
				level: 0.5, muted: tt.muted,
			}

			view := m.renderAudio()
			assert.Equal(t, 1, m.audioHeight())
			assert.Equal(t, 1, lineCount(view))
			assert.Contains(t, view, tt.status)
			assert.Contains(t, view, tt.controls)
			assert.Regexp(t, ` · \d{2}:\d{2} · `, view)
			assert.NotContains(t, view, "experimental")

			m.width = 40
			assert.Equal(t, 1, lineCount(m.renderAudio()))
			assert.LessOrEqual(t, xansi.StringWidth(m.renderAudio()), m.contentWidth())
		})
	}
}

func TestAudioCommandsAndDraftCommit(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	_, handled := m.handleLocalSlashCommand("/voice extra")
	assert.True(t, handled, "audio commands are built in")
	assert.Nil(t, m.audio, "invalid arguments only show usage")

	m.textarea.SetValue("hello ")
	m.textarea.CursorEnd()
	_, cancel := context.WithCancel(m.ctx)
	session := &tuiAudioSession{
		cancel: cancel, key: m.key, mode: "voice", draft: "hello ", phase: "transcribing",
	}
	m.audio = session
	cancel() // Worker cleanup may run before Bubble Tea receives completion.
	updated, _ := m.Update(tuiAudioMsg{session: session, done: true, text: "world"})
	result := updated.(model)
	assert.Nil(t, result.audio)
	assert.Equal(t, "hello world", result.textarea.Value())
	assert.False(t, result.running, "dictation must not submit a turn")
}

func TestAudioShortcutsYieldToExtensionShortcuts(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	key := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModAlt}
	require.Equal(t, "ctrl+alt+v", key.String())
	m.extensionShortcuts = []extensions.Shortcut{{Key: "ctrl+alt+v", ExtensionID: "dictation"}}
	_, handled := m.handleAudioInput(key)
	assert.False(t, handled)
	assert.Nil(t, m.audio)

	m.extensionShortcuts = nil
	_, handled = m.handleAudioInput(key)
	assert.True(t, handled, "the built-in shortcut applies without an extension override")
}

func TestAudioDiscardsCanceledStaleAndChangedDraftResults(t *testing.T) {
	for _, test := range []string{
		"canceled", "different conversation", "changed draft", "old session",
	} {
		t.Run(test, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			m.textarea.SetValue("draft")
			_, cancel := context.WithCancel(m.ctx)
			defer cancel()
			session := &tuiAudioSession{cancel: cancel, mode: "voice", key: m.key, draft: "draft"}
			m.audio = session
			switch test {
			case "canceled":
				m.cancelAudio()
			case "different conversation":
				session.key = "another conversation"
			case "changed draft":
				session.draft = "previous draft"
			case "old session":
				m.audio = &tuiAudioSession{}
			}
			m.updateAudio(tuiAudioMsg{session: session, done: true, text: "should not appear"})
			assert.Equal(t, "draft", m.textarea.Value())
		})
	}
}

func TestAudioRecordingKeysAndCallMute(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	m.audio = &tuiAudioSession{
		cancel: cancel, mode: "voice", phase: "recording",
		controls: make(chan audiohost.Command, 1),
	}
	_, handled := m.handleAudioInput(tea.KeyPressMsg{Code: 'a', Text: "a"})
	assert.True(t, handled, "recording freezes the draft")
	_, handled = m.handleAudioInput(tea.PasteMsg{Content: "paste"})
	assert.True(t, handled)
	_, handled = m.handleAudioInput(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, handled)
	assert.Equal(t, "transcribing", m.audio.phase)
	assert.Equal(t, "finish", (<-m.audio.controls).Type)

	m.audio.mode, m.audio.phase = "talk", "connected"
	m.muteAudio()
	assert.True(t, m.audio.muted)
	assert.Equal(t, audiohost.Command{Type: "mute", Muted: true}, <-m.audio.controls)
	_, handled = m.handleAudioInput(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.True(t, handled)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.Equal(t, "stopping", m.audio.phase)
}

func TestAudioEscClosesOverlaysBeforeEndingCall(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.slashCommands = withTUIBuiltInSlashCommands(nil)
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	m.audio = &tuiAudioSession{cancel: cancel, mode: "talk", phase: "connected"}
	esc := tea.KeyPressMsg{Code: tea.KeyEscape}

	m.textarea.SetValue("/")
	require.True(t, m.slashCommandSuggestionsOpen())
	updated, _ := m.Update(esc)
	*m = updated.(model)
	assert.False(t, m.slashCommandSuggestionsOpen())
	assert.NoError(t, ctx.Err(), "dismissing suggestions keeps the call")

	m.reasoningPickerOpen = true
	updated, _ = m.Update(esc)
	*m = updated.(model)
	assert.False(t, m.reasoningPickerOpen)
	assert.NoError(t, ctx.Err(), "closing the reasoning picker keeps the call")

	updated, _ = m.Update(esc)
	*m = updated.(model)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.Equal(t, "stopping", m.audio.phase)
}

func TestAudioCtrlDEndsAudioOnlyWhenItQuits(t *testing.T) {
	ctrlD := tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	require.Equal(t, "ctrl+d", ctrlD.String())
	for _, mode := range []string{"voice", "talk"} {
		t.Run(mode, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			ctx, cancel := context.WithCancel(m.ctx)
			defer cancel()
			m.audio = &tuiAudioSession{cancel: cancel, mode: mode, phase: "recording"}

			m.shortcutsOpen = true
			updated, _ := m.Update(ctrlD)
			*m = updated.(model)
			assert.False(t, m.shortcutsOpen)
			assert.NoError(t, ctx.Err(), "dismissing a dialog keeps audio")

			m.running = true
			updated, cmd := m.Update(ctrlD)
			*m = updated.(model)
			assert.Nil(t, cmd)
			assert.NoError(t, ctx.Err(), "Ctrl+D is ignored during a run, so audio continues")

			m.running = false
			updated, cmd = m.Update(ctrlD)
			*m = updated.(model)
			require.NotNil(t, cmd)
			assert.IsType(t, tea.QuitMsg{}, cmd())
			assert.ErrorIs(t, ctx.Err(), context.Canceled, "quitting ends audio")
		})
	}
}

func TestAudioTalkStreamsBothSpeakersIntoTranscript(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	_, cancel := context.WithCancel(m.ctx)
	defer cancel()
	session := &tuiAudioSession{
		cancel: cancel, key: m.key, mode: "talk", phase: "connected", started: time.Now(),
	}
	m.audio = session
	m.textarea.SetValue("unsent draft")
	m.autoFollow = false

	// Both speakers render before Final, replacing cumulative text by utterance ID.
	for _, event := range []chat.RealtimeEvent{
		{Kind: "transcript", ID: "call:user", Role: "user", Text: "Check"},
		{Kind: "transcript", ID: "call:user", Role: "user", Text: "Check the tests"},
		{Kind: "transcript", ID: "call:assistant", Role: "assistant", Text: "I will"},
		{Kind: "transcript", ID: "call:assistant", Role: "assistant", Text: "I will check."},
	} {
		m.updateAudio(tuiAudioMsg{session: session, caption: &event})
	}
	require.Len(t, m.entries, 2)
	assert.Equal(t, entryUser, m.entries[0].kind)
	assert.Equal(t, entryAssistant, m.entries[1].kind)
	assert.True(t, m.pendingRefresh)
	assert.False(t, m.pendingRefreshBottom, "streaming speech must respect scrollback")
	updated, _ := m.Update(transcriptRefreshMsg{})
	*m = updated.(model)
	view := xansi.Strip(m.viewport.View())
	assert.Contains(t, view, "Check the tests")
	assert.Contains(t, view, "I will check.")
	assert.Equal(t, 1, m.audioHeight())
	assert.Equal(t, 1, lineCount(m.renderAudio()))
	assert.NotContains(t, m.renderAudio(), "Check the tests")
	assert.NotContains(t, m.renderAudio(), "I will check.")
	assert.Equal(t, "unsent draft", m.textarea.Value())

	// Delegated coding output and a second utterance must not take over each other.
	m.applyChatEvent(chat.ChatEvent{Kind: "user-message", Content: "Run tests"})
	m.applyChatEvent(chat.ChatEvent{Kind: "text-delta", Delta: "Tests "})
	m.updateAudio(tuiAudioMsg{session: session, caption: &chat.RealtimeEvent{
		Kind: "transcript", ID: "call:next", Role: "user", Text: "And lint?",
	}})
	m.applyChatEvent(chat.ChatEvent{Kind: "text-delta", Delta: "passed"})
	m.updateAudio(tuiAudioMsg{session: session, caption: &chat.RealtimeEvent{
		Kind: "transcript", ID: "call:assistant", Role: "assistant",
		Text: "I will check both.", Final: true,
	}})
	require.Len(t, m.entries, 5)
	assert.Equal(t, "I will check both.", m.entries[1].content)
	assert.Equal(t, "Tests passed", m.entries[3].content)
	assert.Equal(t, "And lint?", m.entries[4].content)

	m.updateAudio(tuiAudioMsg{session: session, done: true})
	require.Nil(t, m.audio)
}

func TestAudioTalkRestoresSavedTranscriptAndReconcilesLiveUpdates(t *testing.T) {
	runner := &conversationSourceRunner{history: chat.ConversationHistory{
		ID: "saved", Messages: []conversations.StreamableMessage{
			{
				ID: "voice:user", Anchor: new(0), Kind: "text", Role: "user",
				Content: "spoken request",
			},
			{Anchor: new(0), Kind: "text", Role: "assistant", Content: "coding reply one"},
			{
				ID: "voice:assistant", Anchor: new(1), Kind: "text", Role: "assistant",
				Content: "spoken reply",
			},
			{Anchor: new(1), Kind: "text", Role: "assistant", Content: "coding reply two"},
		},
	}}
	m := newThemeTestModel(t, Config{ConversationID: "saved", Remote: true, Runner: runner})
	load := loadConversationHistoryFromSource(m.ctx, m.key, "saved", runner)
	history := load().(initialHistoryMsg)
	updated, _ := m.Update(history)
	*m = updated.(model)
	require.Len(t, m.entries, 4)
	assert.Equal(t, "voice:user", m.entries[0].realtimeID)
	assert.Equal(t, "voice:assistant", m.entries[2].realtimeID)
	assert.Zero(t, m.entries[2].realtimeSeq)
	view := xansi.Strip(m.viewport.View())
	assert.Contains(t, view, "spoken request")
	assert.Contains(t, view, "spoken reply")

	// An old restored caption cannot overwrite a newer server checkpoint.
	runner.history.Messages[2].Content = "saved complete reply"
	history = load().(initialHistoryMsg)
	m.entries = mergeAudioTranscript(history.entries, m.entries, m.realtimeCaptionSeq)
	assert.Equal(t, "saved complete reply", m.entries[2].content)
	// But live deltas must survive a snapshot requested before those deltas.
	requested := m.realtimeCaptionSeq
	m.updateAudioTranscript(chat.RealtimeEvent{
		ID: "voice:assistant", Role: "assistant", Text: "live complete reply", Anchor: new(99),
	})
	m.updateAudioTranscript(chat.RealtimeEvent{
		ID: "voice:next", Role: "user", Text: "follow up", Anchor: new(1),
	})
	for range 2 {
		m.entries = mergeAudioTranscript(history.entries, m.entries, requested)
		require.Len(t, m.entries, 5)
		assert.Equal(t, "live complete reply", m.entries[2].content)
		assert.Equal(t, 1, m.entries[2].realtimeAnchor, "updates never move an existing utterance")
		assert.Equal(t, "follow up", m.entries[3].content)
		assert.Equal(t, "coding reply two", m.entries[4].content)
	}

	// The server saved a final caption that an interrupted call never delivered.
	runner.history.Messages[2].Content = "saved final reply"
	runner.history.Messages = slices.Insert(
		runner.history.Messages,
		3,
		conversations.StreamableMessage{
			ID: "voice:next", Anchor: new(1), Kind: "text", Role: "user", Content: "follow up",
		},
	)
	_, refresh := m.Update(conversationStreamReconcileMsg{conversationKey: m.key})
	// A caption received after the request can still be newer than its snapshot.
	m.updateAudioTranscript(chat.RealtimeEvent{
		ID: "voice:user", Role: "user", Text: "spoken request, continued",
	})
	updated, _ = m.Update(conversationHistoryRefreshFromCmd(t, refresh))
	*m = updated.(model)
	require.Len(t, m.entries, 5)
	assert.Equal(t, "spoken request, continued", m.entries[0].content)
	assert.Equal(t, "saved final reply", m.entries[2].content,
		"history requested after a caption replaces it")
	runner.history.Messages[0].Content = "spoken request, continued"
	runner.history.Messages[2].Content = "saved final reply, edited"
	for range 2 {
		_, refresh = m.Update(conversationStreamReconcileMsg{conversationKey: m.key})
		updated, _ = m.Update(conversationHistoryRefreshFromCmd(t, refresh))
		*m = updated.(model)
		require.Len(t, m.entries, 5)
		assert.Equal(t, "spoken request, continued", m.entries[0].content)
		assert.Equal(t, "saved final reply, edited", m.entries[2].content)
		assert.Equal(t, "follow up", m.entries[3].content)
		assert.Equal(t, "coding reply two", m.entries[4].content)
	}
}

func TestSavedAssistantMessagesMergeLikeLiveRepliesAroundCaptions(t *testing.T) {
	entries := entriesFromHistory([]conversations.StreamableMessage{
		{Anchor: new(0), Kind: "text", Role: "user", Content: "check files"},
		{
			Anchor: new(1), Kind: "tool-use", Role: "assistant",
			ToolCallID: "one", ToolName: "file_read", Input: "{}",
		},
		{
			Anchor: new(1), Kind: "tool-result", Role: "user",
			ToolCallID: "one", ToolName: "file_read", Content: "first",
		},
		{
			Anchor: new(2), Kind: "tool-use", Role: "assistant",
			ToolCallID: "two", ToolName: "file_read", Input: "{}",
		},
		{Anchor: new(2), Kind: "text", Role: "assistant", Content: "both read"},
		{ID: "voice:user", Anchor: new(3), Kind: "text", Role: "user", Content: "spoken follow-up"},
		{Anchor: new(3), Kind: "text", Role: "assistant", Content: "after the caption"},
	})
	require.Len(t, entries, 4)
	assert.Equal(t, &codingSpan{first: 1, last: 2}, entries[1].codingSpan)
	require.Len(t, entries[1].blocks, 2)
	assert.Len(t, entries[1].blocks[0].tools, 2, "consecutive saved tool calls stay grouped")
	assert.Equal(t, "voice:user", entries[2].realtimeID, "captions still split saved replies")
	assert.Equal(t, &codingSpan{first: 3, last: 3}, entries[3].codingSpan)

	for _, test := range []struct {
		anchor int
		index  int
	}{
		{anchor: 1, index: 1}, // Before the merged reply.
		{anchor: 2, index: 2}, // Inside it: after the reply until history reloads.
		{anchor: 3, index: 3}, // Equal anchors keep arrival order.
		{anchor: 4, index: 4},
	} {
		inserted, index := upsertAudioTranscript(slices.Clone(entries), chatEntry{
			kind: entryUser, realtimeID: "live", realtimeAnchor: test.anchor,
		})
		assert.Equal(t, test.index, index, "anchor %d", test.anchor)
		assert.Equal(t, "live", inserted[index].realtimeID)
	}
}

func TestAudioTalkDoesNotReplaceTypedUserEntries(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.applyChatEvent(chat.ChatEvent{Kind: "user-message", Content: "typed"})
	m.updateAudioTranscript(chat.RealtimeEvent{ID: "voice", Role: "user", Text: "spoken"})
	m.applyChatEvent(chat.ChatEvent{Kind: "user-message-display", Content: "expanded typed"})
	m.applyChatEvent(chat.ChatEvent{Kind: "user-message", Content: "expanded typed"})
	require.Len(t, m.entries, 2)
	assert.Equal(t, "expanded typed", m.entries[0].content)
	assert.Equal(t, "spoken", m.entries[1].content)

	for _, relay := range []string{"delegate request", "steer relay"} {
		m.applyChatEvent(chat.ChatEvent{Kind: "user-message", Content: relay, DelegatedUser: true})
		m.applyChatEvent(chat.ChatEvent{
			Kind: "user-message-display", Content: relay, DelegatedUser: true,
		})
		m.updateAudioTranscript(chat.RealtimeEvent{
			ID: relay, Role: "assistant", Text: "spoken reply", Anchor: new(0),
		})
		m.applyChatEvent(chat.ChatEvent{Kind: "text-delta", Delta: "coding output"})
		assert.Equal(t, "coding output", m.entries[m.activeAssistantEntry].content,
			"voice insertion must preserve the coding reply target")
	}
	transcript, _ := m.renderTranscript()
	assert.NotContains(t, transcript, "delegate request")
	assert.NotContains(t, transcript, "steer relay")
	assert.Contains(t, xansi.Strip(transcript), "expanded typed")
	assert.Contains(t, xansi.Strip(transcript), "spoken")
}

type audioDiscoveryRunner struct {
	*streamingConversationSourceRunner
	targets []chat.WorkspaceTarget
}

func (r *audioDiscoveryRunner) DiscoverWorkspace(
	_ context.Context,
	target chat.WorkspaceTarget,
) (protocol.WorkspaceDiscoverResult, error) {
	r.targets = append(r.targets, target)
	return protocol.WorkspaceDiscoverResult{
		CWD:      "/resolved/project",
		Commands: []slashcommands.Command{{Name: "extension-command"}},
		Shortcuts: []protocol.ShortcutDescriptor{
			{Key: "ctrl+k", ExtensionID: "extension", Generation: 1},
		},
	}, nil
}

func TestAudioTalkCreatesConversationWithDraftSettingsAndResolvedWorkspace(t *testing.T) {
	runner := &audioDiscoveryRunner{
		streamingConversationSourceRunner: newStreamingConversationSourceRunner(),
	}
	m := newThemeTestModel(t, Config{
		Remote: true, Runner: runner, CWD: "~/project",
		Profile: "coding", Model: "picked-model", ReasoningEffort: "high",
		EnvironmentProfile: "sandbox",
	})
	draftDiscovery := m.loadRemoteSlashCommands(m.conversationState)
	require.True(t, m.resourcesLoading)
	m.textarea.SetValue("/talk")
	_, handled := m.handleLocalSlashCommand("/talk")
	require.True(t, handled)
	require.NotNil(t, m.audio)
	t.Cleanup(m.audio.cancel)
	session := m.audio
	require.NotEmpty(t, m.conversationID)
	assert.Equal(t, chat.ChatRequest{
		ConversationID: m.conversationID, Profile: "coding", CWD: "~/project",
		ReasoningEffort: "high", EnvironmentProfile: "sandbox",
		Options: &llmtypes.ExecutionOptions{Model: new("picked-model")},
	}, m.conversationRequest(m.conversationState))
	assert.Empty(t, m.textarea.Value())
	assert.Empty(t, m.entries, "starting voice must not submit a placeholder message")
	assert.Zero(t, m.streamRunID, "do not watch a conversation before the server saves it")
	assert.False(t, m.resourcesLoading, "the allocated ID makes the draft's discovery obsolete")
	updated, _ := m.Update(draftDiscovery())
	*m = updated.(model)
	assert.NotContains(t, slashCommandNames(m.slashCommands), "extension-command")

	// The resolved workspace arrives before the call is ready, even while stopping.
	session.phase = "stopping"
	assert.Nil(t, m.updateAudio(tuiAudioMsg{session: session, cwd: "/resolved/project"}))
	assert.Equal(t, "/resolved/project", m.cwd)
	request := m.conversationRequest(m.conversationState)
	assert.Equal(t, "/resolved/project", request.CWD, "later turns must match the saved workspace")

	ready := m.updateAudio(tuiAudioMsg{session: session, conversationReady: true})
	require.NotNil(t, ready)
	assert.NotZero(t, m.streamRunID, "attach coding results after voice creates the conversation")
	t.Cleanup(func() { m.stopConversationStream(m.conversationState) })
	batch, ok := ready().(tea.BatchMsg)
	require.True(t, ok)
	var discovered []slashCommandsMsg
	for _, child := range batch {
		if child == nil {
			continue
		}
		if msg, ok := child().(slashCommandsMsg); ok {
			discovered = append(discovered, msg)
		}
	}
	require.Len(t, discovered, 1, "restart discovery once the conversation is saved")
	assert.Equal(t, []chat.WorkspaceTarget{
		{CWD: "~/project", Profile: "coding", EnvironmentProfile: "sandbox"},
		{ConversationID: m.conversationID},
	}, runner.targets)
	updated, _ = m.Update(discovered[0])
	*m = updated.(model)
	assert.Contains(t, slashCommandNames(m.slashCommands), "extension-command")
	require.Len(t, m.extensionShortcuts, 1)
	assert.False(t, m.resourcesLoading)
}

func TestRealtimeAudioReportsResolvedWorkspaceWhenSetupFails(t *testing.T) {
	peer := &testAudioPeer{events: make(chan audiohost.Event, 1)}
	peer.events <- audiohost.Event{Type: "offer", SDP: "offer"}
	client := testRealtimeAudioClient{
		start: func(context.Context) (chat.RealtimeSession, error) {
			// The server can save a voice-first conversation before negotiation fails.
			session := chat.RealtimeSession{CWD: "/resolved/project"}
			return session, errors.New("could not start live voice")
		},
		end: func(context.Context, string) error { return nil },
	}
	var workspaces []string
	emit := func(msg tuiAudioMsg) {
		assert.False(t, msg.conversationReady)
		if msg.cwd != "" {
			workspaces = append(workspaces, msg.cwd)
		}
	}
	request := chat.ChatRequest{ConversationID: "conv"}
	err := runRealtimeAudio(t.Context(), client, request, nil, emit, peer.start)
	require.ErrorContains(t, err, "could not start live voice")
	assert.Equal(t, []string{"/resolved/project"}, workspaces)
}

type testAudioPeer struct {
	events chan audiohost.Event
	sent   []audiohost.Command
	closed atomic.Bool
	// The helper context must still be live at the first Close for the helper
	// to shut down gracefully rather than being killed.
	graceful atomic.Bool
	ctx      context.Context
}

func (p *testAudioPeer) Send(command audiohost.Command) error {
	p.sent = append(p.sent, command)
	return nil
}
func (p *testAudioPeer) Events() <-chan audiohost.Event { return p.events }

func (p *testAudioPeer) Close() error {
	if !p.closed.Swap(true) {
		p.graceful.Store(p.ctx != nil && p.ctx.Err() == nil)
	}
	return nil
}

func (p *testAudioPeer) start(ctx context.Context) (audioPeer, error) {
	p.ctx = ctx
	return p, nil
}

type testDictationClient struct {
	caps       transcriptiontypes.Capabilities
	audio      []byte
	upload     func()
	discover   func(context.Context) error
	transcribe func(context.Context) error
}

func (c *testDictationClient) TranscriptionCapabilities(
	ctx context.Context,
) (transcriptiontypes.Capabilities, error) {
	if c.discover != nil {
		if err := c.discover(ctx); err != nil {
			return transcriptiontypes.Capabilities{}, err
		}
	}
	return c.caps, nil
}

func (c *testDictationClient) TranscribeAudio(
	ctx context.Context,
	data []byte,
	_, _, _ string,
) (transcriptiontypes.Result, error) {
	c.audio = data
	if c.upload != nil {
		c.upload()
	}
	if c.transcribe != nil {
		if err := c.transcribe(ctx); err != nil {
			return transcriptiontypes.Result{}, err
		}
	}
	return transcriptiontypes.Result{Text: "spoken words"}, nil
}

func TestDictationWorkerWAVAndRecordingLimits(t *testing.T) {
	for _, test := range []string{
		"success", "limit", "empty", "malformed PCM", "disabled", "helper error", "EOF",
	} {
		t.Run(test, func(t *testing.T) {
			peer := &testAudioPeer{events: make(chan audiohost.Event, 4)}
			client := &testDictationClient{caps: transcriptiontypes.Capabilities{
				Enabled: true, Available: true, MaxAudioBytes: 48, MaxDurationSeconds: 5,
				MimeTypes: []string{"audio/wav"},
			}}
			client.upload = func() {
				assert.True(t, peer.closed.Load(), "release microphone before uploading")
			}
			pcm := []byte{1, 2, 3, 4}
			switch test {
			case "limit":
				pcm = append(pcm, 5, 6)
			case "empty":
				pcm = nil
			case "malformed PCM":
				pcm = []byte{1}
			case "disabled":
				client.caps.Enabled = false
			case "helper error":
				peer.events <- audiohost.Event{Type: "error", Message: "device unavailable"}
			case "EOF":
				close(peer.events)
			}
			if test != "EOF" {
				peer.events <- audiohost.Event{Type: "recording"}
				peer.events <- audiohost.Event{Type: "audio", Audio: pcm}
				peer.events <- audiohost.Event{Type: "recorded"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			text, err := runDictation(ctx, client, nil, func(tuiAudioMsg) {}, peer.start)
			if test != "success" {
				require.Error(t, err)
				assert.Empty(t, client.audio)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "spoken words", text)
			require.Len(t, peer.sent, 1)
			assert.Equal(t, int64(4), peer.sent[0].MaxAudioBytes)
			assert.Equal(t, 5, peer.sent[0].MaxDurationSeconds)
			wav := client.audio
			require.Len(t, wav, 48)
			assert.Equal(t, "RIFF", string(wav[:4]))
			assert.Equal(t, uint32(40), binary.LittleEndian.Uint32(wav[4:]))
			assert.Equal(t, "WAVEfmt ", string(wav[8:16]))
			assert.Equal(t, uint32(16000), binary.LittleEndian.Uint32(wav[24:]))
			assert.Equal(t, uint16(1), binary.LittleEndian.Uint16(wav[22:]))
			assert.Equal(t, uint16(16), binary.LittleEndian.Uint16(wav[34:]))
			assert.Equal(t, pcm, wav[44:])
		})
	}
}

type testRealtimeAudioClient struct {
	discover func(context.Context) error
	start    func(context.Context) (chat.RealtimeSession, error)
	stream   func(context.Context, func(chat.RealtimeEvent)) error
	end      func(context.Context, string) error
}

func (c testRealtimeAudioClient) RealtimeCapabilities(
	ctx context.Context,
) (chat.RealtimeCapabilities, error) {
	if c.discover != nil {
		if err := c.discover(ctx); err != nil {
			return chat.RealtimeCapabilities{}, err
		}
	}
	return chat.RealtimeCapabilities{Enabled: true, Available: true}, nil
}

func (c testRealtimeAudioClient) StartRealtime(
	ctx context.Context,
	_ string,
	_ chat.ChatRequest,
	_ string,
) (chat.RealtimeSession, error) {
	return c.start(ctx)
}

func (c testRealtimeAudioClient) StreamRealtime(
	ctx context.Context,
	_ string,
	emit func(chat.RealtimeEvent),
) error {
	return c.stream(ctx, emit)
}

func (c testRealtimeAudioClient) EndRealtime(ctx context.Context, id string) error {
	return c.end(ctx, id)
}

func TestRealtimeAudioContinuesAcrossAgentRunsAndCleansUp(t *testing.T) {
	peer := &testAudioPeer{events: make(chan audiohost.Event, 3)}
	peer.events <- audiohost.Event{Type: "offer", SDP: "offer"}
	peer.events <- audiohost.Event{Type: "connected"}
	peer.events <- audiohost.Event{Type: "level", Level: 0.5}
	closed := false
	client := testRealtimeAudioClient{
		start: func(context.Context) (chat.RealtimeSession, error) {
			return chat.RealtimeSession{
				ID: "call", ConversationID: "conv", Answer: "answer", CWD: "/resolved",
			}, nil
		},
		stream: func(_ context.Context, emit func(chat.RealtimeEvent)) error {
			assert.Eventually(t, func() bool { return len(peer.events) == 0 },
				time.Second, time.Millisecond, "consume helper events first")
			emit(chat.RealtimeEvent{Kind: "transcript", Text: "hello", Role: "user"})
			emit(chat.RealtimeEvent{Kind: "agent-started"})
			emit(chat.RealtimeEvent{Kind: "agent-completed"})
			emit(chat.RealtimeEvent{Kind: "transcript", Text: "next request", Role: "user"})
			emit(chat.RealtimeEvent{Kind: "closed"})
			return nil
		},
		end: func(ctx context.Context, id string) error {
			// Ending the call runs alongside the helper close, not after it.
			assert.NoError(t, ctx.Err(), "cleanup uses a fresh context")
			assert.Equal(t, "call", id)
			closed = true
			return nil
		},
	}
	var captions []string
	conversationReady := false
	cwd := ""
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := runRealtimeAudio(
		ctx, client,
		chat.ChatRequest{ConversationID: "conv"},
		nil,
		func(msg tuiAudioMsg) {
			if msg.cwd != "" {
				cwd = msg.cwd
				assert.False(t, conversationReady,
					"sync the workspace before attaching the conversation")
			}
			if msg.conversationReady {
				conversationReady = true
				assert.Equal(t, []audiohost.Command{{Type: "createOffer"}}, peer.sent,
					"attach the conversation before opening devices")
			}
			if msg.caption != nil && msg.caption.Kind == "transcript" {
				captions = append(captions, msg.caption.Text)
			}
			assert.NotEqual(t, "level", msg.event.Type, "calls render no level meter")
		},
		peer.start,
	)
	require.NoError(t, err)
	assert.True(t, conversationReady)
	assert.Equal(t, "/resolved", cwd)
	assert.True(t, peer.closed.Load(), "release the microphone")
	assert.True(t, closed, "cleanup waits for the call to end")
	assert.Equal(t, []string{"hello", "next request"}, captions,
		"agent completion must not end the call")
	assert.Equal(t, []audiohost.Command{
		{Type: "createOffer"},
		{Type: "answer", SDP: "answer"},
	}, peer.sent)
}

func TestAudioCancelClosesHelperGracefully(t *testing.T) {
	for _, mode := range []string{"voice", "talk"} {
		t.Run(mode, func(t *testing.T) {
			peer := &testAudioPeer{events: make(chan audiohost.Event, 2)}
			active := "recording"
			var runner any = &testDictationClient{caps: transcriptiontypes.Capabilities{
				Enabled: true, Available: true, MaxAudioBytes: 48, MaxDurationSeconds: 5,
				MimeTypes: []string{"audio/wav"},
			}}
			if mode == "talk" {
				active = "connected"
				runner = testRealtimeAudioClient{
					start: func(context.Context) (chat.RealtimeSession, error) {
						return chat.RealtimeSession{
							ID: "call", ConversationID: "conv", Answer: "answer",
						}, nil
					},
					stream: func(ctx context.Context, _ func(chat.RealtimeEvent)) error {
						<-ctx.Done()
						return ctx.Err()
					},
					end: func(context.Context, string) error { return nil },
				}
				peer.events <- audiohost.Event{Type: "offer", SDP: "offer"}
			}
			peer.events <- audiohost.Event{Type: active}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			emit := func(msg tuiAudioMsg) {
				if msg.event.Type == active {
					cancel() // Esc, /talk stop, switching conversations, or exiting the TUI.
				}
			}
			_, err := runTUIAudio(
				ctx, mode, runner,
				chat.ChatRequest{ConversationID: "conv"},
				nil, emit, peer.start,
			)
			require.ErrorIs(t, err, context.Canceled)
			assert.True(t, peer.closed.Load())
			assert.True(t, peer.graceful.Load(),
				"closing must not depend on the canceled session context")
		})
	}
}

func TestAudioCancelDuringHelperStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	peer := &testAudioPeer{}
	started, release, err := startSessionPeer(ctx, func(helperCtx context.Context) (audioPeer, error) {
		cancel()
		select {
		case <-helperCtx.Done():
		case <-time.After(time.Second):
			t.Fatal("cancellation did not abort the handshake")
		}
		// The handshake completes just after the user cancels.
		return peer.start(helperCtx)
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, started, "neither worker may use a helper returned after cancellation")
	assert.Nil(t, release)
	assert.True(t, peer.closed.Load())
}

func TestRealtimeAudioConnectionDeadlineStartsAfterSlowOffer(t *testing.T) {
	previousOffer, previousConnect := realtimeOfferTimeout, realtimeConnectTimeout
	t.Cleanup(func() {
		realtimeOfferTimeout, realtimeConnectTimeout = previousOffer, previousConnect
	})
	for _, test := range []struct {
		name         string
		offerTimeout time.Duration
		err          string
		sent         []audiohost.Command
	}{
		{
			name: "slow first offer", offerTimeout: 5 * time.Second,
			err:  "voice call connection timed out",
			sent: []audiohost.Command{{Type: "createOffer"}, {Type: "answer", SDP: "answer"}},
		},
		{
			name: "no offer", offerTimeout: 100 * time.Millisecond,
			err:  "did not prepare the voice call in time",
			sent: []audiohost.Command{{Type: "createOffer"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			realtimeOfferTimeout, realtimeConnectTimeout = test.offerTimeout, 250*time.Millisecond
			peer := &testAudioPeer{events: make(chan audiohost.Event, 1)}
			// A cold GStreamer scan delays the offer beyond the connection deadline.
			offer := time.AfterFunc(400*time.Millisecond, func() {
				peer.events <- audiohost.Event{Type: "offer", SDP: "offer"}
			})
			t.Cleanup(func() { offer.Stop() })
			client := testRealtimeAudioClient{
				start: func(context.Context) (chat.RealtimeSession, error) {
					return chat.RealtimeSession{
						ID: "call", ConversationID: "conv", Answer: "answer",
					}, nil
				},
				stream: func(ctx context.Context, _ func(chat.RealtimeEvent)) error {
					<-ctx.Done()
					return ctx.Err()
				},
				end: func(context.Context, string) error { return nil },
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := runRealtimeAudio(
				ctx, client,
				chat.ChatRequest{ConversationID: "conv"},
				nil, func(tuiAudioMsg) {}, peer.start,
			)
			require.ErrorContains(t, err, test.err)
			assert.Equal(t, test.sent, peer.sent)
			assert.True(t, peer.closed.Load())
		})
	}
}

func TestAudioWorkerTimeoutsExplainWhichDeadlineExpired(t *testing.T) {
	previousDiscovery, previousTranscription, previousCall := audioDiscoveryTimeout, transcriptionTimeout, realtimeCallLimit
	t.Cleanup(func() {
		audioDiscoveryTimeout, transcriptionTimeout, realtimeCallLimit = previousDiscovery, previousTranscription, previousCall
	})
	audioDiscoveryTimeout, transcriptionTimeout, realtimeCallLimit = 50*time.Millisecond, 50*time.Millisecond, 200*time.Millisecond
	blockUntilDone := func(ctx context.Context) error {
		<-ctx.Done()
		return errors.Wrap(ctx.Err(), "request failed")
	}
	caps := transcriptiontypes.Capabilities{
		Enabled: true, Available: true, MaxAudioBytes: 48, MaxDurationSeconds: 5,
		MimeTypes: []string{"audio/wav"},
	}
	dictate := func(ctx context.Context, client *testDictationClient) error {
		peer := &testAudioPeer{events: make(chan audiohost.Event, 3)}
		peer.events <- audiohost.Event{Type: "recording"}
		peer.events <- audiohost.Event{Type: "audio", Audio: []byte{1, 2}}
		peer.events <- audiohost.Event{Type: "recorded"}
		_, err := runDictation(ctx, client, nil, func(tuiAudioMsg) {}, peer.start)
		return err
	}
	call := func(ctx context.Context, discover func(context.Context) error) error {
		peer := &testAudioPeer{events: make(chan audiohost.Event, 2)}
		peer.events <- audiohost.Event{Type: "offer", SDP: "offer"}
		peer.events <- audiohost.Event{Type: "connected"}
		client := testRealtimeAudioClient{
			discover: discover,
			start: func(context.Context) (chat.RealtimeSession, error) {
				return chat.RealtimeSession{
					ID: "call", ConversationID: "conv", Answer: "answer",
				}, nil
			},
			stream: func(ctx context.Context, _ func(chat.RealtimeEvent)) error {
				return blockUntilDone(ctx)
			},
			end: func(context.Context, string) error { return nil },
		}
		return runRealtimeAudio(
			ctx, client,
			chat.ChatRequest{ConversationID: "conv"},
			nil, func(tuiAudioMsg) {}, peer.start,
		)
	}
	for _, test := range []struct {
		name string
		run  func(context.Context) error
		want error
	}{
		{"dictation availability", func(ctx context.Context) error {
			return dictate(ctx, &testDictationClient{caps: caps, discover: blockUntilDone})
		}, errDictationDiscoveryTimeout},
		{"transcription", func(ctx context.Context) error {
			return dictate(ctx, &testDictationClient{caps: caps, transcribe: blockUntilDone})
		}, errTranscriptionTimeout},
		{"voice call availability", func(ctx context.Context) error {
			return call(ctx, blockUntilDone)
		}, errRealtimeDiscoveryTimeout},
		{"call limit", func(ctx context.Context) error {
			return call(ctx, nil)
		}, errRealtimeCallLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := test.run(ctx)
			require.ErrorIs(t, err, test.want)
			assert.Equal(t, test.want.Error(), err.Error())
		})
	}

	t.Run("an expired parent context keeps its error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		err := dictate(ctx, &testDictationClient{caps: caps, discover: blockUntilDone})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotErrorIs(t, err, errDictationDiscoveryTimeout)
	})
}

func TestRealtimeAudioClosesLateAllocationAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	peer := &testAudioPeer{events: make(chan audiohost.Event, 1)}
	peer.events <- audiohost.Event{Type: "offer", SDP: "offer"}
	allocated, release, ended := make(chan struct{}), make(chan struct{}), make(chan string, 1)
	client := testRealtimeAudioClient{
		start: func(ctx context.Context) (chat.RealtimeSession, error) {
			close(allocated)
			<-release
			assert.NoError(t, ctx.Err())
			return chat.RealtimeSession{ID: "late-call"}, errors.New("invalid response")
		},
		end: func(ctx context.Context, id string) error { ended <- id; return ctx.Err() },
	}
	result := make(chan error, 1)
	go func() {
		result <- runRealtimeAudio(
			ctx, client, chat.ChatRequest{}, nil, func(tuiAudioMsg) {}, peer.start,
		)
	}()
	select {
	case <-allocated:
	case <-time.After(time.Second):
		t.Fatal("allocation did not start")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("audio did not stop promptly")
	}
	assert.True(t, peer.closed.Load())
	assert.True(t, peer.graceful.Load(), "cancellation closes the helper gracefully")
	close(release)
	select {
	case id := <-ended:
		assert.Equal(t, "late-call", id)
	case <-time.After(time.Second):
		t.Fatal("late call leaked")
	}
}
