package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/audiohost"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/pkg/errors"
)

// The worker owns media and network I/O. Only Update mutates presentation fields.
type tuiAudioSession struct {
	cancel    context.CancelFunc
	controls  chan audiohost.Command
	done      chan struct{}
	key       string
	mode      string
	draft     string
	started   time.Time
	phase     string
	level     float64
	muted     bool
	user      string
	assistant string
}

type tuiAudioMsg struct {
	session           *tuiAudioSession
	event             audiohost.Event
	caption           *chat.RealtimeEvent
	text              string
	err               error
	done              bool
	conversationReady bool
}

type tuiAudioTick struct{ session *tuiAudioSession }

func (m model) audioSlashCommands() []slashcommands.Command {
	if !m.experimentalAudio {
		return nil
	}
	return []slashcommands.Command{
		{Name: "voice", Description: "Dictate into the draft (experimental)", Placeholder: "/voice"},
		{Name: "talk", Description: "Start a realtime voice call (experimental)", Hint: "stop|mute (optional)", Placeholder: "/talk [stop|mute]"},
	}
}

func audioTick(session *tuiAudioSession) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tuiAudioTick{session} })
}

func (m *model) audioNotice(message string) tea.Cmd {
	return m.addUINotification(uiNotification{
		level: uiNotificationWarning, title: "Experimental audio", message: message,
	})
}

func (m *model) handleAudioCommand(command, args string) tea.Cmd {
	if !m.experimentalAudio {
		return m.audioNotice("Start chat with --experimental-audio and install the helper with mise run audio-install.")
	}
	args = strings.TrimSpace(args)
	if command == "voice" && args != "" || command == "talk" && args != "" && args != "stop" && args != "mute" {
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
	if !m.experimentalAudio {
		return nil, false
	}
	if _, ok := msg.(tea.PasteMsg); ok && m.audio != nil && m.audio.mode == "voice" {
		return nil, true
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || m.startupPending || m.startupErr != nil {
		return nil, false
	}
	if key.String() == "ctrl+c" || key.String() == "ctrl+d" {
		m.cancelAudio()
		return nil, false
	}
	if m.activeUIPrompt != nil || m.conversationPicker != nil || m.infoDialogOpen() || m.modelPickerOpen || m.historySearch != nil {
		return nil, false
	}
	if _, focused := m.focusedExtensionSurfaceKey(); focused {
		return nil, false
	}
	if m.audio != nil {
		if key.String() == "esc" {
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
	if m.runner == nil || m.conversationState == nil || m.startupPending || m.initialHistoryPending {
		return m.audioNotice("Wait for chat to finish loading.")
	}
	if mode == "talk" {
		if !m.loaded {
			return m.audioNotice("Wait for chat to finish loading.")
		}
		m.ensureConversationID(m.conversationState)
	}
	ctx, cancel := context.WithCancel(contextWithTUIConversation(m.ctx, m.key))
	session := &tuiAudioSession{
		cancel: cancel, controls: make(chan audiohost.Command, 8), done: make(chan struct{}),
		key: m.key, mode: mode, draft: m.textarea.Value(), started: time.Now(), phase: "starting",
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
			text, err := runTUIAudio(ctx, mode, runner, request, session.controls, emit, startAudioPeer)
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
	if msg.conversationReady {
		// The voice POST has now persisted the empty conversation. Subscribe only
		// after that point, so the normal transcript also receives delegated work.
		return m.startConversationStream(m.stateForKey(session.key))
	}
	if session.phase == "stopping" {
		return nil
	}
	if msg.caption != nil {
		switch msg.caption.Kind {
		case "transcript":
			text := sanitizeExtensionTranscriptText(msg.caption.Text)
			if msg.caption.Role == "user" {
				session.user = text
			} else {
				session.assistant = text
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

func (m model) audioHeight() int {
	if m.audio == nil {
		return 0
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
	header := fmt.Sprintf("%s (experimental) · %s · %02d:%02d · %s", label, state, seconds/60, seconds%60, controls)
	lines := []string{header, "", ""}
	if s.mode == "talk" {
		lines[1], lines[2] = "You: "+s.user, "Kodelet: "+s.assistant
	} else {
		level := int(min(1, max(0, s.level)) * 20)
		lines[1] = "Microphone: " + strings.Repeat("▰", level) + strings.Repeat("▱", 20-level)
		lines[2] = "Text will be inserted into your draft, never sent automatically."
	}
	for i := range lines {
		lines[i] = fitVisible(normalizeSingleLinePaste(lines[i]), m.contentWidth())
	}
	return strings.Join(lines, "\n")
}
