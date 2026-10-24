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
	"github.com/jingkaihe/kodelet/pkg/audio"
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
			name:     "dictation",
			mode:     "voice",
			status:   "Dictation · recording",
			controls: "▰▰▰▰▰▱▱▱▱▱ · Enter: finish · Esc: cancel",
		},
		{
			name:     "talk",
			mode:     "talk",
			status:   "Talk · connected",
			controls: "▰▰▰▰▰▱▱▱▱▱ · Ctrl+Alt+M: mute · Esc: end",
		},
		{
			name:     "muted talk",
			mode:     "talk",
			muted:    true,
			status:   "Talk · connected (muted)",
			controls: "▱▱▱▱▱▱▱▱▱▱ · Ctrl+Alt+M: unmute · Esc: end",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			assert.Empty(t, m.renderAudio())
			assert.Zero(t, m.audioHeight())
			m.audio = &tuiAudioSession{
				mode:    tt.mode,
				phase:   "recording",
				started: time.Now(),
				muted:   tt.muted,
			}
			if tt.mode == "talk" {
				m.audio.phase = "connected"
			}
			m.updateAudio(tuiAudioMsg{
				session: m.audio,
				event:   audio.Event{Type: "level", Level: 0.5},
			})

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
		cancel:          cancel,
		key:             m.key,
		mode:            "voice",
		draft:           "hello ",
		phase:           "transcribing",
		finishRequested: true,
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
			session := &tuiAudioSession{
				cancel:          cancel,
				mode:            "voice",
				key:             m.key,
				draft:           "draft",
				finishRequested: true,
			}
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

func TestAudioDictationPreviewPreservesDraftUntilFinish(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.textarea.SetValue("before after")
	m.textarea.SetCursorColumn(7)
	_, cancel := context.WithCancel(m.ctx)
	defer cancel()
	session := &tuiAudioSession{
		cancel:   cancel,
		mode:     "voice",
		key:      m.key,
		draft:    m.textarea.Value(),
		phase:    "recording",
		started:  time.Now(),
		controls: make(chan audio.Command, 1),
	}
	m.audio = session
	m.resize()
	height := m.viewport.Height()
	preview := func(text string) {
		m.updateAudio(tuiAudioMsg{
			session: session,
			event:   audio.Event{Type: "preview"},
			text:    text,
		})
	}
	preview("first draft")
	preview("corrected \x1b[31mwords\x1b[0m")
	assert.Equal(t, "corrected words", session.preview)
	assert.NotContains(t, m.renderAudio(), "first draft")
	assert.Contains(t, m.renderAudio(), "corrected words")
	assert.Equal(t, "recording", session.phase, "preview uploads do not end capture")
	assert.Equal(t, "before after", m.textarea.Value())
	assert.False(t, m.running)

	m.updateAudio(tuiAudioMsg{
		session: session,
		event:   audio.Event{Type: "preview"},
		err:     errors.New("upload failed"),
	})
	assert.Equal(t, "corrected words", session.preview, "a failed upload keeps the preview")
	preview("one\ntwo\nthree\nfour")
	assert.Equal(t, 4, m.audioHeight())
	assert.Equal(t, m.audioHeight(), lineCount(m.renderAudio()))
	assert.Equal(t, height-3, m.viewport.Height())
	assert.Equal(t, []string{"two", "three", "four"}, m.audioPreviewLines())

	_, handled := m.handleAudioInput(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	assert.Equal(t, "finish", (<-session.controls).Type)
	m.updateAudio(tuiAudioMsg{session: session, done: true, text: "final words "})
	assert.Nil(t, m.audio)
	assert.Equal(t, "before final words after", m.textarea.Value(),
		"preserve the insertion point")
	assert.False(t, m.running, "dictation must never submit a turn")
}

func TestAudioDictationReviewRequiresExplicitAcceptance(t *testing.T) {
	for _, reason := range []string{"limit", "upload failure"} {
		for _, action := range []string{"keep", "cancel"} {
			t.Run(reason+"/"+action, func(t *testing.T) {
				m := newThemeTestModel(t, Config{})
				m.textarea.SetValue("draft ")
				m.textarea.CursorEnd()
				_, cancel := context.WithCancel(m.ctx)
				defer cancel()
				session := &tuiAudioSession{
					cancel:  cancel,
					key:     m.key,
					mode:    "voice",
					draft:   "draft ",
					phase:   "transcribing",
					started: time.Now(),
				}
				m.audio = session
				msg := tuiAudioMsg{session: session, done: true, text: "last preview"}
				if reason == "upload failure" {
					session.finishRequested = true
					msg.err = errors.New("upload failed")
				}
				m.updateAudio(msg)
				require.Same(t, session, m.audio)
				assert.Equal(t, "review", session.phase)
				assert.Equal(t, "draft ", m.textarea.Value())
				assert.Contains(t, m.renderAudio(), "Enter: keep")
				key := tea.KeyEscape
				want := "draft "
				if action == "keep" {
					key = tea.KeyEnter
					want = "draft last preview"
				}
				_, handled := m.handleAudioInput(tea.KeyPressMsg{Code: key})
				assert.True(t, handled)
				assert.Nil(t, m.audio)
				assert.Zero(t, m.audioHeight())
				assert.Equal(t, want, m.textarea.Value())
				assert.False(t, m.running)
			})
		}
	}
}

func TestAudioRecordingKeysAndCallMute(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	m.audio = &tuiAudioSession{
		cancel: cancel, mode: "voice", phase: "recording",
		controls: make(chan audio.Command, 1),
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
	assert.Equal(t, audio.Command{Type: "mute", Muted: true}, <-m.audio.controls)
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
	peer := &testAudioPeer{events: make(chan audio.Event, 1)}
	peer.events <- audio.Event{Type: "offer", SDP: "offer"}
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
	events chan audio.Event
	sent   []audio.Command
	closed atomic.Bool
	// The helper context must still be live at the first Close for the helper
	// to shut down gracefully rather than being killed.
	graceful atomic.Bool
	ctx      context.Context
}

func (p *testAudioPeer) Send(command audio.Command) error {
	p.sent = append(p.sent, command)
	return nil
}
func (p *testAudioPeer) Events() <-chan audio.Event { return p.events }

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
			peer := &testAudioPeer{events: make(chan audio.Event, 4)}
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
				peer.events <- audio.Event{Type: "error", Message: "device unavailable"}
			case "EOF":
				close(peer.events)
			}
			if test != "EOF" {
				peer.events <- audio.Event{Type: "recording"}
				peer.events <- audio.Event{Type: "audio", Audio: pcm}
				peer.events <- audio.Event{Type: "recorded"}
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
	peer := &testAudioPeer{events: make(chan audio.Event, 3)}
	peer.events <- audio.Event{Type: "offer", SDP: "offer"}
	peer.events <- audio.Event{Type: "connected"}
	peer.events <- audio.Event{Type: "level", Level: 0.5}
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
	var levels []float64
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
				assert.Equal(t, []audio.Command{{Type: "createOffer"}}, peer.sent,
					"attach the conversation before opening devices")
			}
			if msg.caption != nil && msg.caption.Kind == "transcript" {
				captions = append(captions, msg.caption.Text)
			}
			if msg.event.Type == "level" {
				levels = append(levels, msg.event.Level)
			}
		},
		peer.start,
	)
	require.NoError(t, err)
	assert.True(t, conversationReady)
	assert.Equal(t, []float64{0.5}, levels, "forward microphone levels to the call UI")
	assert.Equal(t, "/resolved", cwd)
	assert.True(t, peer.closed.Load(), "release the microphone")
	assert.True(t, closed, "cleanup waits for the call to end")
	assert.Equal(t, []string{"hello", "next request"}, captions,
		"agent completion must not end the call")
	assert.Equal(t, []audio.Command{
		{Type: "createOffer"},
		{Type: "answer", SDP: "answer"},
	}, peer.sent)
}

func TestAudioCancelClosesHelperGracefully(t *testing.T) {
	for _, mode := range []string{"voice", "talk"} {
		t.Run(mode, func(t *testing.T) {
			peer := &testAudioPeer{events: make(chan audio.Event, 2)}
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
				peer.events <- audio.Event{Type: "offer", SDP: "offer"}
			}
			peer.events <- audio.Event{Type: active}
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
		sent         []audio.Command
	}{
		{
			name: "slow first offer", offerTimeout: 5 * time.Second,
			err:  "voice call connection timed out",
			sent: []audio.Command{{Type: "createOffer"}, {Type: "answer", SDP: "answer"}},
		},
		{
			name: "no offer", offerTimeout: 100 * time.Millisecond,
			err:  "did not prepare the voice call in time",
			sent: []audio.Command{{Type: "createOffer"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			realtimeOfferTimeout, realtimeConnectTimeout = test.offerTimeout, 250*time.Millisecond
			peer := &testAudioPeer{events: make(chan audio.Event, 1)}
			// A cold GStreamer scan delays the offer beyond the connection deadline.
			offer := time.AfterFunc(400*time.Millisecond, func() {
				peer.events <- audio.Event{Type: "offer", SDP: "offer"}
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
	previousDiscovery := audioDiscoveryTimeout
	previousTranscription := transcriptionTimeout
	previousCall := realtimeCallLimit
	t.Cleanup(func() {
		audioDiscoveryTimeout = previousDiscovery
		transcriptionTimeout = previousTranscription
		realtimeCallLimit = previousCall
	})
	audioDiscoveryTimeout = 50 * time.Millisecond
	transcriptionTimeout = 50 * time.Millisecond
	realtimeCallLimit = 200 * time.Millisecond
	blockUntilDone := func(ctx context.Context) error {
		<-ctx.Done()
		return errors.Wrap(ctx.Err(), "request failed")
	}
	caps := transcriptiontypes.Capabilities{
		Enabled: true, Available: true, MaxAudioBytes: 48, MaxDurationSeconds: 5,
		MimeTypes: []string{"audio/wav"},
	}
	dictate := func(ctx context.Context, client *testDictationClient) error {
		peer := &testAudioPeer{events: make(chan audio.Event, 3)}
		peer.events <- audio.Event{Type: "recording"}
		peer.events <- audio.Event{Type: "audio", Audio: []byte{1, 2}}
		peer.events <- audio.Event{Type: "recorded"}
		_, err := runDictation(ctx, client, nil, func(tuiAudioMsg) {}, peer.start)
		return err
	}
	call := func(ctx context.Context, discover func(context.Context) error) error {
		peer := &testAudioPeer{events: make(chan audio.Event, 2)}
		peer.events <- audio.Event{Type: "offer", SDP: "offer"}
		peer.events <- audio.Event{Type: "connected"}
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
	peer := &testAudioPeer{events: make(chan audio.Event, 1)}
	peer.events <- audio.Event{Type: "offer", SDP: "offer"}
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

type continuousDictationClient func(context.Context, []byte) (string, error)

func (c continuousDictationClient) TranscriptionCapabilities(
	context.Context,
) (transcriptiontypes.Capabilities, error) {
	return transcriptiontypes.Capabilities{
		Enabled:            true,
		Available:          true,
		MaxAudioBytes:      transcriptiontypes.MaxAudioBytes,
		MaxDurationSeconds: 60,
		MimeTypes:          []string{"audio/wav"},
	}, nil
}

func (c continuousDictationClient) TranscribeAudio(
	ctx context.Context,
	audio []byte,
	_, _, _ string,
) (transcriptiontypes.Result, error) {
	text, err := c(ctx, audio)
	return transcriptiontypes.Result{Text: text}, err
}

func dictationTestPCM(milliseconds int, amplitude int16) []byte {
	pcm := make([]byte, milliseconds*16*2)
	for offset := 0; offset < len(pcm); offset += 2 {
		binary.LittleEndian.PutUint16(pcm[offset:], uint16(amplitude))
		amplitude = -amplitude
	}
	return pcm
}

func dictationTestPhrase() []byte {
	speech := dictationTestPCM(200, 4000)
	pause := dictationTestPCM(2500, 0)
	return append(speech, pause...)
}

func TestDictationContinuousSnapshotsReplaceAndDeduplicate(t *testing.T) {
	for _, chunkBytes := range []int{222, 640, 19998} {
		// A snapshot boundary is determined by sample counts, not IPC frames.
		phrase := dictationTestPhrase()
		peer := &testAudioPeer{events: make(chan audio.Event, len(phrase)/chunkBytes+4)}
		peer.events <- audio.Event{Type: "recording"}
		for offset := 0; offset < len(phrase); offset += chunkBytes {
			peer.events <- audio.Event{
				Type:  "audio",
				Audio: phrase[offset:min(offset+chunkBytes, len(phrase))],
			}
		}
		var uploads [][]byte
		var previews []string
		client := continuousDictationClient(func(_ context.Context, audio []byte) (string, error) {
			assert.False(t, peer.closed.Load(), "pause snapshots must not stop capture")
			uploads = append(uploads, audio)
			if len(uploads) == 1 {
				return "first words", nil
			}
			return "corrected full sentence", nil
		})
		emit := func(msg tuiAudioMsg) {
			switch msg.event.Type {
			case "transcribing":
				assert.True(t, peer.closed.Load(), "only finishing changes phase")
			case "preview":
				assert.NoError(t, msg.err)
				previews = append(previews, msg.text)
				if len(previews) == 1 {
					peer.events <- audio.Event{Type: "audio", Audio: phrase}
				} else {
					peer.events <- audio.Event{
						Type:  "audio",
						Audio: dictationTestPCM(500, 0),
					}
					peer.events <- audio.Event{Type: "recorded"}
				}
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		text, err := runDictation(ctx, client, nil, emit, peer.start)
		cancel()
		require.NoError(t, err)
		assert.Equal(t, "corrected full sentence", text)
		assert.Equal(t, []string{"first words", "corrected full sentence"}, previews)
		require.Len(t, uploads, 2,
			"silence and finish must not repeat a successful snapshot")
		assert.Equal(t, dictationWAV(phrase), uploads[0])
		pcm := append(slices.Clone(phrase), phrase...)
		assert.Equal(t, dictationWAV(pcm), uploads[1])
		assert.True(t, peer.closed.Load())
	}
}

type finishingDictationPeer struct {
	*testAudioPeer
	tail []byte
}

func (p *finishingDictationPeer) Send(command audio.Command) error {
	if err := p.testAudioPeer.Send(command); err != nil {
		return err
	}
	if command.Type == "finish" {
		p.events <- audio.Event{Type: "audio", Audio: p.tail}
		p.events <- audio.Event{Type: "recorded"}
	}
	return nil
}

func (p *finishingDictationPeer) start(ctx context.Context) (audioPeer, error) {
	p.ctx = ctx
	return p, nil
}

func TestDictationDrainsCaptureAndCoalescesPendingUploads(t *testing.T) {
	peer := &finishingDictationPeer{
		testAudioPeer: &testAudioPeer{events: make(chan audio.Event, 8)},
		tail:          dictationTestPCM(5, 6000),
	}
	phrase := dictationTestPhrase()
	peer.events <- audio.Event{Type: "recording"}
	var pcm []byte
	for range 3 {
		peer.events <- audio.Event{Type: "audio", Audio: phrase}
		pcm = append(pcm, phrase...)
	}
	peer.events <- audio.Event{Type: "level", Level: 0.75}
	controls := make(chan audio.Command, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	var uploads [][]byte
	client := continuousDictationClient(func(ctx context.Context, audio []byte) (string, error) {
		uploads = append(uploads, audio)
		if len(uploads) == 1 {
			close(started)
			select {
			case <-release:
				return "initial words", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		assert.True(t, peer.closed.Load(), "release the device before the final upload")
		return "latest complete transcript", nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	emit := func(msg tuiAudioMsg) {
		switch msg.event.Type {
		case "level":
			// Reaching this event proves capture kept draining during the blocked
			// upload; the intermediate snapshots must stay queued, not run in parallel.
			select {
			case <-started:
			case <-ctx.Done():
				t.Error("upload did not start")
				return
			}
			assert.Len(t, uploads, 1)
			assert.False(t, peer.closed.Load())
			controls <- audio.Command{Type: "finish"}
		case "transcribing":
			assert.True(t, peer.closed.Load())
			close(release)
		}
	}
	text, err := runDictation(ctx, client, controls, emit, peer.start)
	require.NoError(t, err)
	assert.Equal(t, "latest complete transcript", text)
	require.Len(t, uploads, 2,
		"only one in-flight request and the newest waiting snapshot")
	assert.Equal(t, dictationWAV(phrase), uploads[0])
	pcm = append(pcm, peer.tail...)
	assert.Equal(t, dictationWAV(pcm), uploads[1], "include the final flush")
	require.Len(t, peer.sent, 2)
	assert.Equal(t, "finish", peer.sent[1].Type)
	assert.True(t, peer.graceful.Load())
}

func TestDictationCancelAbortsUploadAndDropsPendingSnapshot(t *testing.T) {
	peer := &testAudioPeer{events: make(chan audio.Event, 4)}
	phrase := dictationTestPhrase()
	peer.events <- audio.Event{Type: "recording"}
	peer.events <- audio.Event{Type: "audio", Audio: phrase}
	peer.events <- audio.Event{Type: "audio", Audio: phrase}
	peer.events <- audio.Event{Type: "level"}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	started := make(chan struct{})
	aborted := make(chan struct{})
	var calls atomic.Int32
	client := continuousDictationClient(func(ctx context.Context, _ []byte) (string, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		close(aborted)
		return "", ctx.Err()
	})
	emit := func(msg tuiAudioMsg) {
		if msg.event.Type == "level" {
			select {
			case <-started:
			case <-ctx.Done():
				t.Error("upload did not start")
			}
			cancel()
		}
	}
	text, err := runDictation(ctx, client, nil, emit, peer.start)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, text)
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("transcription request was not canceled")
	}
	assert.Equal(t, int32(1), calls.Load())
	assert.True(t, peer.closed.Load())
	assert.True(t, peer.graceful.Load())
}

func TestDictationRecoversFromInterimUploadFailure(t *testing.T) {
	peer := &testAudioPeer{events: make(chan audio.Event, 3)}
	phrase := dictationTestPhrase()
	peer.events <- audio.Event{Type: "recording"}
	peer.events <- audio.Event{Type: "audio", Audio: phrase}
	wantErr := errors.New("temporary transcription failure")
	calls := 0
	client := continuousDictationClient(func(_ context.Context, audio []byte) (string, error) {
		calls++
		assert.Len(t, audio, 44+calls*len(phrase))
		if calls == 2 {
			return "", wantErr
		}
		if calls == 1 {
			return "first preview", nil
		}
		return "recovered full transcript", nil
	})
	var previews []tuiAudioMsg
	emit := func(msg tuiAudioMsg) {
		if msg.event.Type != "preview" {
			return
		}
		previews = append(previews, msg)
		if len(previews) < 3 {
			peer.events <- audio.Event{Type: "audio", Audio: phrase}
		} else {
			peer.events <- audio.Event{Type: "recorded"}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	text, err := runDictation(ctx, client, nil, emit, peer.start)
	require.NoError(t, err)
	assert.Equal(t, "recovered full transcript", text)
	require.Len(t, previews, 3)
	assert.Equal(t, "first preview", previews[0].text)
	assert.ErrorIs(t, previews[1].err, wantErr)
	assert.Equal(t, "recovered full transcript", previews[2].text)
	assert.Equal(t, 3, calls)
}

func TestDictationFinishRetriesFailedSnapshotAndPreservesPartialText(t *testing.T) {
	type response struct {
		text string
		err  error
	}
	partial := response{text: "partial transcript"}
	complete := response{text: "complete transcript"}
	failed := response{err: errors.New("transcription unavailable")}

	for _, tt := range []struct {
		name      string
		responses []response
		want      response
	}{
		{
			name:      "retry succeeds",
			responses: []response{partial, failed, complete},
			want:      complete,
		},
		{
			name:      "retry fails with partial text",
			responses: []response{partial, failed, failed},
			want:      response{text: partial.text, err: failed.err},
		},
		{
			name:      "retry fails without partial text",
			responses: []response{failed, failed},
			want:      failed,
		},
		{
			name:      "first success on finish",
			responses: []response{failed, complete},
			want:      complete,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			peer := &testAudioPeer{events: make(chan audio.Event, 3)}
			phrase := dictationTestPhrase()
			pcm := slices.Clone(phrase)
			peer.events <- audio.Event{Type: "recording"}
			peer.events <- audio.Event{Type: "audio", Audio: phrase}
			var uploads [][]byte
			client := continuousDictationClient(func(
				_ context.Context,
				audio []byte,
			) (string, error) {
				index := len(uploads)
				uploads = append(uploads, audio)
				if !assert.Less(t, index, len(tt.responses), "unexpected upload") {
					return "", failed.err
				}
				assert.Equal(t, len(uploads) == len(tt.responses), peer.closed.Load(),
					"only the final retry runs after capture closes")
				result := tt.responses[index]
				return result.text, result.err
			})
			emit := func(msg tuiAudioMsg) {
				if msg.event.Type != "preview" {
					return
				}
				if msg.err == nil {
					pcm = append(pcm, phrase...)
					peer.events <- audio.Event{Type: "audio", Audio: phrase}
					return
				}
				assert.ErrorIs(t, msg.err, failed.err)
				tail := dictationTestPCM(500, 0)
				pcm = append(pcm, tail...)
				peer.events <- audio.Event{Type: "audio", Audio: tail}
				peer.events <- audio.Event{Type: "recorded"}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			text, err := runDictation(ctx, client, nil, emit, peer.start)
			require.ErrorIs(t, err, tt.want.err)
			assert.Equal(t, tt.want.text, text)
			require.Len(t, uploads, len(tt.responses))
			assert.Equal(t, dictationWAV(pcm), uploads[len(uploads)-1],
				"retry includes the whole recording and trailing silence")
		})
	}
}

func TestDictationSpeechDetectorSilenceAndNoise(t *testing.T) {
	for _, tt := range []struct {
		name      string
		segments  [][2]int
		snapshots []int
	}{
		{
			name: "ignore silence and isolated clicks",
			segments: [][2]int{
				{3000, 0}, {100, 16000}, {2000, 0}, {100, 10000}, {3000, 0},
			},
		},
		{
			name:     "require 180 ms initial speech",
			segments: [][2]int{{160, 4000}, {3000, 0}},
		},
		{
			name:      "snapshot after exactly 2.5 seconds, without repeats",
			segments:  [][2]int{{180, 4000}, {2480, 0}, {20, 0}, {6000, 0}},
			snapshots: []int{2680},
		},
		{
			name: "learn background noise and detect louder speech",
			segments: [][2]int{
				{3000, 197}, {2000, 393}, {300, 2621}, {2500, 393},
			},
			snapshots: []int{7800},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			detector := dictationSpeechDetector{noiseFloor: 0.003}
			var snapshots []int
			elapsed := 0
			for _, segment := range tt.segments {
				pcm := dictationTestPCM(segment[0], int16(segment[1]))
				for offset := 0; offset < len(pcm); offset += 640 {
					elapsed += 20
					if detector.push(pcm[offset : offset+640]) {
						snapshots = append(snapshots, elapsed)
						detector.dirty = false
					}
				}
			}
			assert.Equal(t, tt.snapshots, snapshots)
		})
	}
}
