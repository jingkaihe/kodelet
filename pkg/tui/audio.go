package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/audiohost"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/pkg/errors"
)

// The worker owns media and network I/O. Only Update mutates presentation fields.
type tuiAudioSession struct {
	cancel   context.CancelFunc
	controls chan audiohost.Command
	done     chan struct{}
	key      string
	mode     string
	draft    string
	started  time.Time
	phase    string
	level    float64
	muted    bool
	// The call gives a draft its conversation ID, so it owns that transition.
	createsConversation bool
}

type tuiAudioMsg struct {
	session           *tuiAudioSession
	event             audiohost.Event
	caption           *chat.RealtimeEvent
	text              string
	cwd               string // The workspace the server validated or saved.
	err               error
	done              bool
	conversationReady bool
}

type tuiAudioTick struct{ session *tuiAudioSession }

func audioTick(session *tuiAudioSession) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tuiAudioTick{session} })
}

func (m *model) audioNotice(message string) tea.Cmd {
	return m.addUINotification(uiNotification{
		level: uiNotificationWarning, title: "Experimental audio", message: message,
	})
}

func (m *model) handleAudioCommand(command, args string) tea.Cmd {
	args = strings.TrimSpace(args)
	if command == "voice" && args != "" ||
		command == "talk" && args != "" && args != "stop" && args != "mute" {
		return m.audioNotice("Usage: /voice or /talk [stop|mute]")
	}
	// A slash command is not part of the draft; keyboard shortcuts preserve it.
	m.textarea.Reset()
	m.dismissSlashCommandSuggestions()
	if command == "talk" && args == "stop" {
		m.cancelAudio()
		return nil
	}
	if command == "talk" && args == "mute" {
		return m.muteAudio()
	}
	return m.startAudio(command)
}

func (m *model) handleAudioInput(msg tea.Msg) (tea.Cmd, bool) {
	if _, ok := msg.(tea.PasteMsg); ok && m.audio != nil && m.audio.mode == "voice" {
		return nil, true
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || m.startupPending || m.startupErr != nil {
		return nil, false
	}
	switch key.String() {
	case "ctrl+c":
		m.cancelAudio()
		return nil, false
	case "ctrl+d":
		// Ctrl+D also dismisses overlays and is ignored during a run. When it
		// quits, the TUI's exit cleanup ends audio.
		return nil, false
	}
	if m.activeUIPrompt != nil ||
		m.conversationPicker != nil ||
		m.infoDialogOpen() ||
		m.modelPickerOpen ||
		m.reasoningPickerOpen ||
		m.historySearch != nil {
		return nil, false
	}
	if _, focused := m.focusedExtensionSurfaceKey(); focused {
		return nil, false
	}
	if m.audio != nil {
		if key.String() == "esc" {
			if m.slashCommandSuggestionsOpen() {
				return nil, false // Dismiss suggestions first; another Esc stops audio.
			}
			m.cancelAudio()
			return nil, true
		}
		if m.audio.mode == "voice" {
			if key.String() == "enter" && m.audio.phase == "recording" {
				m.audio.phase = "transcribing"
				return m.sendAudioControl(audiohost.Command{Type: "finish"}), true
			}
			if key.String() == "ctrl+l" {
				m.cancelAudio()
				return nil, false
			}
			return nil, true // Freeze the draft/cursor until commit or cancellation.
		}
	}
	// Like other built-in shortcuts, audio keys yield to extension shortcuts.
	if _, overridden := m.extensionShortcutForKey(key.Keystroke()); overridden {
		return nil, false
	}
	switch key.String() {
	case "ctrl+alt+v":
		return m.startAudio("voice"), true
	case "ctrl+alt+r":
		if m.audio != nil {
			m.cancelAudio()
			return nil, true
		}
		return m.startAudio("talk"), true
	case "ctrl+alt+m":
		return m.muteAudio(), true
	}
	return nil, false
}

func (m *model) startAudio(mode string) tea.Cmd {
	if m.audio != nil {
		return m.audioNotice("Finish the active recording or call first.")
	}
	if m.runner == nil ||
		m.conversationState == nil ||
		m.startupPending ||
		m.initialHistoryPending {
		return m.audioNotice("Wait for chat to finish loading.")
	}
	createsConversation := false
	if mode == "talk" {
		if !m.loaded {
			return m.audioNotice("Wait for chat to finish loading.")
		}
		if strings.TrimSpace(m.conversationID) == "" {
			// As for a typed first turn, the ID makes the draft's pending discovery
			// obsolete. Discovery restarts once voice saves the conversation.
			createsConversation = true
			m.resourcesLoading = false
			m.readinessStartedAt = time.Time{}
			m.readyDuration = 0
		}
		m.ensureConversationID(m.conversationState)
	}
	ctx, cancel := context.WithCancel(contextWithTUIConversation(m.ctx, m.key))
	session := &tuiAudioSession{
		cancel: cancel, controls: make(chan audiohost.Command, 8), done: make(chan struct{}),
		key: m.key, mode: mode, draft: m.textarea.Value(), started: time.Now(), phase: "starting",
		createsConversation: createsConversation,
	}
	m.audio = session
	m.resize()
	request := m.conversationRequest(m.conversationState)
	runner, output, uiDone := m.runner, m.runCh, m.ctx.Done()
	start := func() tea.Msg {
		go func() {
			defer close(session.done)
			defer cancel()
			emit := func(msg tuiAudioMsg) {
				msg.session = session
				select {
				case output <- msg:
				case <-uiDone:
				}
			}
			text, err := runTUIAudio(
				ctx, mode, runner, request, session.controls, emit, startAudioPeer,
			)
			emit(tuiAudioMsg{done: true, text: text, err: err})
		}()
		return nil
	}
	return tea.Batch(start, audioTick(session))
}

func (m *model) cancelAudio() {
	if m.audio != nil {
		m.audio.phase = "stopping"
		m.audio.cancel()
	}
}

func (m *model) sendAudioControl(command audiohost.Command) tea.Cmd {
	if m.audio == nil {
		return nil
	}
	select {
	case m.audio.controls <- command:
		return nil
	default:
		m.cancelAudio()
		return m.audioNotice("Audio controls stalled; the session was stopped.")
	}
}

func (m *model) muteAudio() tea.Cmd {
	if m.audio == nil || m.audio.mode != "talk" || m.audio.phase != "connected" {
		return m.audioNotice("There is no connected voice call.")
	}
	// Show requested mute immediately; a failed control terminates the call.
	m.audio.muted = !m.audio.muted
	return m.sendAudioControl(audiohost.Command{Type: "mute", Muted: m.audio.muted})
}

func (m *model) updateAudio(msg tuiAudioMsg) tea.Cmd {
	if m.audio == nil || msg.session != m.audio {
		return nil
	}
	session := m.audio
	if msg.done {
		canceled := session.phase == "stopping" || errors.Is(msg.err, context.Canceled)
		m.audio = nil
		m.resize()
		if canceled || session.key != m.key {
			return nil
		}
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			return m.audioNotice(msg.err.Error())
		}
		if session.mode == "voice" && msg.text != "" {
			if m.textarea.Value() != session.draft {
				return m.audioNotice("The draft changed; dictation was discarded.")
			}
			m.textarea.InsertString(sanitizeExtensionTranscriptText(msg.text))
			m.draft = m.textarea.Value()
			m.resize()
		}
		return nil
	}
	if msg.cwd != "" {
		// Later typed turns and calls must match the server's saved workspace,
		// which an idle conversation stream does not report.
		if state := m.stateForKey(session.key); state != nil {
			state.cwd = msg.cwd
			if m.remote {
				state.requestedCWD = msg.cwd
			}
		}
		return nil
	}
	if msg.conversationReady {
		// The voice POST has now persisted the empty conversation. Subscribe only
		// after that point, so the normal transcript also receives delegated work.
		state := m.stateForKey(session.key)
		stream := m.startConversationStream(state)
		if !session.createsConversation || !m.remote || state == nil {
			return stream
		}
		// Replace the draft discovery that the allocated ID made obsolete.
		return tea.Batch(stream, m.loadRemoteSlashCommands(state))
	}
	if session.phase == "stopping" {
		return nil
	}
	if msg.caption != nil {
		switch msg.caption.Kind {
		case "transcript":
			if session.key == m.key {
				return m.updateAudioTranscript(*msg.caption)
			}
		case "warning":
			return m.audioNotice(msg.caption.Text)
		}
	}
	switch msg.event.Type {
	case "recording", "transcribing", "connected":
		session.phase = msg.event.Type
	case "level":
		session.level = msg.event.Level
	}
	return nil
}

func audioTranscriptEntry(event chat.RealtimeEvent) chatEntry {
	text := strings.TrimSpace(sanitizeExtensionTranscriptText(event.Text))
	entry := chatEntry{kind: entryUser, content: text, realtimeID: event.ID}
	if event.Anchor != nil {
		entry.realtimeAnchor = *event.Anchor
	}
	if event.Role == "assistant" {
		entry.kind = entryAssistant
		entry.blocks = []assistantBlock{{kind: blockText, text: text}}
	}
	return entry
}

func (m *model) updateAudioTranscript(event chat.RealtimeEvent) tea.Cmd {
	if event.ID == "" || (event.Role != "user" && event.Role != "assistant") {
		return nil
	}
	entry := audioTranscriptEntry(event)
	m.realtimeCaptionSeq++
	entry.realtimeSeq = m.realtimeCaptionSeq
	if event.Anchor == nil {
		// Older servers without positions can still append live captions.
		for _, existing := range m.entries {
			if existing.realtimeID == "" {
				entry.realtimeAnchor++
			}
		}
	}
	before := len(m.entries)
	var index int
	m.entries, index = upsertAudioTranscript(m.entries, entry)
	if len(m.entries) > before && m.hasActiveAssistantEntry && m.activeAssistantEntry >= index {
		m.activeAssistantEntry++
	}
	// Do not replace the active coding reply: its text/tools can stream while
	// either speaker is talking. Respect scrolling and the usual render debounce.
	return m.queueTranscriptRefresh(m.autoFollow)
}

// The server saves each caption before streaming it, so history requested after
// a caption arrived has that text or newer. Reapply only captions received after
// the request (captionSeq), which may have raced it; history replaces the rest.
func mergeAudioTranscript(history, current []chatEntry, captionSeq uint64) []chatEntry {
	result := slices.Clone(history)
	for _, entry := range current {
		if entry.realtimeSeq > captionSeq {
			result, _ = upsertAudioTranscript(result, entry)
		}
	}
	return result
}

func upsertAudioTranscript(entries []chatEntry, entry chatEntry) ([]chatEntry, int) {
	index := slices.IndexFunc(entries, func(existing chatEntry) bool {
		return existing.realtimeID == entry.realtimeID
	})
	if index >= 0 {
		if entries[index].kind == entry.kind {
			entry.realtimeAnchor = entries[index].realtimeAnchor
			entries[index] = entry
		}
		return entries, index
	}
	position := 0 // Coding messages before the current entry.
	for index, existing := range entries {
		if existing.realtimeID != "" {
			if existing.realtimeAnchor <= entry.realtimeAnchor {
				continue
			}
		} else {
			if existing.kind == entryInfo ||
				(existing.codingSpan == nil && len(existing.blocks) > 0 &&
					(existing.blocks[0].kind == blockCompaction ||
						existing.blocks[0].kind == blockResourcesReloaded)) {
				continue
			}
			// Live entries have no saved span and count as one coding message.
			first, last := position, position
			if existing.codingSpan != nil {
				first, last = existing.codingSpan.first, existing.codingSpan.last
			}
			// A caption positioned inside a merged saved reply follows that reply;
			// the next history load places it between the reply's messages.
			if first < entry.realtimeAnchor {
				position = last + 1
				continue
			}
		}
		return slices.Insert(entries, index, entry), index
	}
	return append(entries, entry), len(entries)
}

func (m model) audioHeight() int {
	if m.audio == nil {
		return 0
	}
	if m.audio.mode == "talk" {
		return 1
	}
	return 3
}

func (m model) renderAudio() string {
	if m.audio == nil {
		return ""
	}
	s := m.audio
	label, controls := "Dictation", "Enter: finish · Esc: cancel"
	if s.mode == "talk" {
		label, controls = "Talk", "Ctrl+Alt+M: mute · Esc: end call"
	}
	state := s.phase
	if s.muted {
		state += " (microphone muted)"
	}
	seconds := int(time.Since(s.started).Seconds())
	header := fmt.Sprintf(
		"%s (experimental) · %s · %02d:%02d · %s",
		label, state, seconds/60, seconds%60, controls,
	)
	lines := []string{header}
	if s.mode != "talk" {
		level := int(min(1, max(0, s.level)) * 20)
		lines = append(lines,
			"Microphone: "+strings.Repeat("▰", level)+strings.Repeat("▱", 20-level),
			"Text will be inserted into your draft, never sent automatically.",
		)
	}
	for i := range lines {
		lines[i] = fitVisible(normalizeSingleLinePaste(lines[i]), m.contentWidth())
	}
	return strings.Join(lines, "\n")
}
