package tui

import (
	"context"
	"encoding/binary"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/audiohost"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAudioCommandsAndDraftCommit(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	names := []string{}
	for _, command := range withTUIBuiltInSlashCommands(nil) {
		names = append(names, command.Name)
	}
	assert.Subset(t, names, []string{"voice", "talk"})
	_, handled := m.handleLocalSlashCommand("/voice extra")
	assert.True(t, handled, "audio commands are built in")
	assert.Nil(t, m.audio, "invalid arguments only show usage")

	m.textarea.SetValue("hello ")
	m.textarea.CursorEnd()
	_, cancel := context.WithCancel(m.ctx)
	session := &tuiAudioSession{cancel: cancel, key: m.key, mode: "voice", draft: "hello ", phase: "transcribing"}
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
	for _, test := range []string{"canceled", "different conversation", "changed draft", "old session"} {
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
	m.audio = &tuiAudioSession{cancel: cancel, mode: "voice", phase: "recording", controls: make(chan audiohost.Command, 1)}
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

func TestAudioTalkStreamsBothSpeakersIntoTranscript(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	_, cancel := context.WithCancel(m.ctx)
	defer cancel()
	session := &tuiAudioSession{cancel: cancel, key: m.key, mode: "talk", phase: "connected", started: time.Now()}
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
		Kind: "transcript", ID: "call:assistant", Role: "assistant", Text: "I will check both.", Final: true,
	}})
	require.Len(t, m.entries, 5)
	assert.Equal(t, "I will check both.", m.entries[1].content)
	assert.Equal(t, "Tests passed", m.entries[3].content)
	assert.Equal(t, "And lint?", m.entries[4].content)

	m.updateAudio(tuiAudioMsg{session: session, done: true})
	require.Nil(t, m.audio)
	// The coding-only history refresh must neither erase nor duplicate speech.
	history := initialHistoryMsg{
		conversationKey: m.key, loaded: true,
		entries: []chatEntry{m.entries[2], m.entries[3]},
	}
	for range 2 {
		updated, _ = m.Update(conversationHistoryRefreshMsg{history: history})
		*m = updated.(model)
		require.Len(t, m.entries, 5)
		assert.Equal(t, "call:user", m.entries[0].realtimeID)
		assert.Equal(t, "call:assistant", m.entries[1].realtimeID)
		assert.Equal(t, "call:next", m.entries[4].realtimeID)
		transcript, _ := m.renderTranscript()
		assert.Equal(t, 1, strings.Count(xansi.Strip(transcript), "I will check both."))
	}
}

func TestAudioTalkRestoresSavedTranscriptAndReconcilesLiveUpdates(t *testing.T) {
	runner := &conversationSourceRunner{history: chat.ConversationHistory{
		ID: "saved", Messages: []conversations.StreamableMessage{
			{ID: "voice:user", Anchor: new(0), Kind: "text", Role: "user", Content: "spoken request"},
			{Anchor: new(0), Kind: "text", Role: "assistant", Content: "coding reply one"},
			{ID: "voice:assistant", Anchor: new(1), Kind: "text", Role: "assistant", Content: "spoken reply"},
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
	assert.False(t, m.entries[2].realtimeLive)
	view := xansi.Strip(m.viewport.View())
	assert.Contains(t, view, "spoken request")
	assert.Contains(t, view, "spoken reply")

	// An old restored caption cannot overwrite a newer server checkpoint.
	runner.history.Messages[2].Content = "saved complete reply"
	history = load().(initialHistoryMsg)
	m.entries = mergeAudioTranscript(history.entries, m.entries)
	assert.Equal(t, "saved complete reply", m.entries[2].content)
	// But live deltas must survive a snapshot taken before those deltas.
	m.updateAudioTranscript(chat.RealtimeEvent{ID: "voice:assistant", Role: "assistant", Text: "live complete reply", Anchor: new(99)})
	m.updateAudioTranscript(chat.RealtimeEvent{ID: "voice:next", Role: "user", Text: "follow up", Anchor: new(1)})
	for range 2 {
		m.entries = mergeAudioTranscript(history.entries, m.entries)
		require.Len(t, m.entries, 5)
		assert.Equal(t, "live complete reply", m.entries[2].content)
		assert.Equal(t, 1, m.entries[2].realtimeAnchor, "updates never move an existing utterance")
		assert.Equal(t, "follow up", m.entries[3].content)
		assert.Equal(t, "coding reply two", m.entries[4].content)
	}
}

func TestSavedAssistantMessagesMergeLikeLiveRepliesAroundCaptions(t *testing.T) {
	entries := entriesFromHistory([]conversations.StreamableMessage{
		{Anchor: new(0), Kind: "text", Role: "user", Content: "check files"},
		{Anchor: new(1), Kind: "tool-use", Role: "assistant", ToolCallID: "one", ToolName: "file_read", Input: "{}"},
		{Anchor: new(1), Kind: "tool-result", Role: "user", ToolCallID: "one", ToolName: "file_read", Content: "first"},
		{Anchor: new(2), Kind: "tool-use", Role: "assistant", ToolCallID: "two", ToolName: "file_read", Input: "{}"},
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
		inserted, index := upsertAudioTranscript(slices.Clone(entries), chatEntry{kind: entryUser, realtimeID: "live", realtimeAnchor: test.anchor})
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
		m.applyChatEvent(chat.ChatEvent{Kind: "user-message-display", Content: relay, DelegatedUser: true})
		m.updateAudioTranscript(chat.RealtimeEvent{ID: relay, Role: "assistant", Text: "spoken reply", Anchor: new(0)})
		m.applyChatEvent(chat.ChatEvent{Kind: "text-delta", Delta: "coding output"})
		assert.Equal(t, "coding output", m.entries[m.activeAssistantEntry].content, "voice insertion must preserve the coding reply target")
	}
	transcript, _ := m.renderTranscript()
	assert.NotContains(t, transcript, "delegate request")
	assert.NotContains(t, transcript, "steer relay")
	assert.Contains(t, xansi.Strip(transcript), "expanded typed")
	assert.Contains(t, xansi.Strip(transcript), "spoken")
}

func TestAudioTalkCreatesConversationWithDraftSettings(t *testing.T) {
	runner := newStreamingConversationSourceRunner()
	m := newThemeTestModel(t, Config{
		Remote: true, Runner: runner,
		Profile: "coding", Model: "picked-model", ReasoningEffort: "high",
		CWD: "/workspace", EnvironmentProfile: "sandbox",
	})
	m.textarea.SetValue("/talk")
	_, handled := m.handleLocalSlashCommand("/talk")
	assert.True(t, handled)
	require.NotNil(t, m.audio)
	t.Cleanup(m.audio.cancel)
	assert.Equal(t, "starting", m.audio.phase)
	require.NotEmpty(t, m.conversationID)
	assert.Equal(t, chat.ChatRequest{
		ConversationID: m.conversationID, Profile: "coding", CWD: "/workspace",
		ReasoningEffort: "high", EnvironmentProfile: "sandbox",
		Options: &llmtypes.ExecutionOptions{Model: new("picked-model")},
	}, m.conversationRequest(m.conversationState))
	assert.Empty(t, m.textarea.Value())
	assert.Empty(t, m.uiNotifications)
	assert.Empty(t, m.entries, "starting voice must not submit a placeholder message")
	assert.Zero(t, m.streamRunID, "do not watch a conversation before the server saves it")
	require.NotNil(t, m.updateAudio(tuiAudioMsg{session: m.audio, conversationReady: true}))
	assert.NotZero(t, m.streamRunID, "attach coding results after voice creates the conversation")
	m.stopConversationStream(m.conversationState)
}

type testAudioPeer struct {
	events chan audiohost.Event
	sent   []audiohost.Command
	closed bool
}

func (p *testAudioPeer) Send(command audiohost.Command) error {
	p.sent = append(p.sent, command)
	return nil
}
func (p *testAudioPeer) Events() <-chan audiohost.Event           { return p.events }
func (p *testAudioPeer) Close() error                             { p.closed = true; return nil }
func (p *testAudioPeer) start(context.Context) (audioPeer, error) { return p, nil }

type testDictationClient struct {
	caps   transcriptiontypes.Capabilities
	audio  []byte
	upload func()
}

func (c *testDictationClient) TranscriptionCapabilities(context.Context) (transcriptiontypes.Capabilities, error) {
	return c.caps, nil
}

func (c *testDictationClient) TranscribeAudio(_ context.Context, data []byte, _, _, _ string) (transcriptiontypes.Result, error) {
	c.audio = data
	if c.upload != nil {
		c.upload()
	}
	return transcriptiontypes.Result{Text: "spoken words"}, nil
}

func TestDictationWorkerWAVAndRecordingLimits(t *testing.T) {
	for _, test := range []string{"success", "limit", "empty", "malformed PCM", "disabled", "helper error", "EOF"} {
		t.Run(test, func(t *testing.T) {
			peer := &testAudioPeer{events: make(chan audiohost.Event, 4)}
			client := &testDictationClient{caps: transcriptiontypes.Capabilities{
				Enabled: true, Available: true, MaxAudioBytes: 48, MaxDurationSeconds: 5, MimeTypes: []string{"audio/wav"},
			}}
			client.upload = func() { assert.True(t, peer.closed, "release microphone before uploading") }
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
	start  func(context.Context) (chat.RealtimeSession, error)
	stream func(context.Context, func(chat.RealtimeEvent)) error
	end    func(context.Context, string) error
}

func (c testRealtimeAudioClient) RealtimeCapabilities(context.Context) (chat.RealtimeCapabilities, error) {
	return chat.RealtimeCapabilities{Enabled: true, Available: true}, nil
}

func (c testRealtimeAudioClient) StartRealtime(ctx context.Context, _ string, _ chat.ChatRequest, _ string) (chat.RealtimeSession, error) {
	return c.start(ctx)
}

func (c testRealtimeAudioClient) StreamRealtime(ctx context.Context, _ string, emit func(chat.RealtimeEvent)) error {
	return c.stream(ctx, emit)
}

func (c testRealtimeAudioClient) EndRealtime(ctx context.Context, id string) error {
	return c.end(ctx, id)
}

func TestRealtimeAudioContinuesAcrossAgentRunsAndCleansUp(t *testing.T) {
	peer := &testAudioPeer{events: make(chan audiohost.Event, 2)}
	peer.events <- audiohost.Event{Type: "offer", SDP: "offer"}
	peer.events <- audiohost.Event{Type: "connected"}
	closed := false
	client := testRealtimeAudioClient{
		start: func(context.Context) (chat.RealtimeSession, error) {
			return chat.RealtimeSession{ID: "call", ConversationID: "conv", Answer: "answer"}, nil
		},
		stream: func(_ context.Context, emit func(chat.RealtimeEvent)) error {
			emit(chat.RealtimeEvent{Kind: "transcript", Text: "hello", Role: "user"})
			emit(chat.RealtimeEvent{Kind: "agent-started"})
			emit(chat.RealtimeEvent{Kind: "agent-completed"})
			emit(chat.RealtimeEvent{Kind: "transcript", Text: "next request", Role: "user"})
			emit(chat.RealtimeEvent{Kind: "closed"})
			return nil
		},
		end: func(ctx context.Context, id string) error {
			assert.True(t, peer.closed)
			assert.NoError(t, ctx.Err(), "cleanup uses a fresh context")
			assert.Equal(t, "call", id)
			closed = true
			return nil
		},
	}
	var captions []string
	conversationReady := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := runRealtimeAudio(ctx, client, chat.ChatRequest{ConversationID: "conv"}, nil, func(msg tuiAudioMsg) {
		if msg.conversationReady {
			conversationReady = true
			assert.Equal(t, []audiohost.Command{{Type: "createOffer"}}, peer.sent, "attach the conversation before opening devices")
		}
		if msg.caption != nil && msg.caption.Kind == "transcript" {
			captions = append(captions, msg.caption.Text)
		}
	}, peer.start)
	require.NoError(t, err)
	assert.True(t, conversationReady)
	assert.True(t, closed)
	assert.Equal(t, []string{"hello", "next request"}, captions, "agent completion must not end the call")
	assert.Equal(t, []audiohost.Command{{Type: "createOffer"}, {Type: "answer", SDP: "answer"}}, peer.sent)
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
		result <- runRealtimeAudio(ctx, client, chat.ChatRequest{}, nil, func(tuiAudioMsg) {}, peer.start)
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
	assert.True(t, peer.closed)
	close(release)
	select {
	case id := <-ended:
		assert.Equal(t, "late-call", id)
	case <-time.After(time.Second):
		t.Fatal("late call leaked")
	}
}
