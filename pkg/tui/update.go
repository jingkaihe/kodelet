package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/steer"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

func waitForMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func waitForTranscriptRefresh() tea.Cmd {
	return tea.Tick(transcriptRefreshDelay, func(time.Time) tea.Msg {
		return transcriptRefreshMsg{}
	})
}

func isTextareaNewlineKey(key string) bool {
	switch key {
	case "shift+enter", "alt+enter", "ctrl+j":
		return true
	}
	return isShiftEnterCSISequence(key)
}

func isShiftEnterCSISequence(key string) bool {
	sequence, ok := decodeUnknownCSISequence(key)
	if !ok || sequence == "" {
		return false
	}

	final := sequence[len(sequence)-1]
	params := strings.Split(sequence[:len(sequence)-1], ";")
	switch final {
	case 'u':
		if len(params) < 2 {
			return false
		}
		code, err := strconv.Atoi(params[0])
		if err != nil || code != 13 {
			return false
		}
		return hasShiftModifier(params[1])
	case '~':
		if len(params) == 2 {
			code, err := strconv.Atoi(params[0])
			if err != nil || code != 13 {
				return false
			}
			return hasShiftModifier(params[1])
		}
		if len(params) == 3 && params[0] == "27" && params[2] == "13" {
			return hasShiftModifier(params[1])
		}
	}
	return false
}

func decodeUnknownCSISequence(key string) (string, bool) {
	encoded, ok := strings.CutPrefix(key, "?CSI[")
	if !ok {
		return "", false
	}
	encoded, ok = strings.CutSuffix(encoded, "]?")
	if !ok {
		return "", false
	}

	var sequence strings.Builder
	for _, field := range strings.Fields(encoded) {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 || value > 255 {
			return "", false
		}
		sequence.WriteByte(byte(value))
	}
	return sequence.String(), true
}

func hasShiftModifier(value string) bool {
	modifierText, _, _ := strings.Cut(value, ":")
	modifier, err := strconv.Atoi(modifierText)
	if err != nil {
		return false
	}
	const shiftModifierBit = 1
	return modifier > 1 && (modifier-1)&shiftModifierBit != 0
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if stringer, ok := msg.(fmt.Stringer); ok && isTextareaNewlineKey(stringer.String()) {
		m.insertTextareaNewline()
		return m, nil
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshViewport(true)

	case initialHistoryMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "history load failed"
			m.entries = append(m.entries, chatEntry{
				kind: entryAssistant,
				blocks: []assistantBlock{{
					kind: blockText,
					text: fmt.Sprintf("Failed to resume conversation: %v", msg.err),
				}},
			})
		} else if len(msg.entries) > 0 {
			if len(m.entries) == 0 {
				m.entries = msg.entries
				m.usage = msg.usage
				m.status = fmt.Sprintf("resumed %s", shortID(m.conversationID))
			}
		}
		m.refreshViewport(true)

	case tea.KeyMsg:
		key := msg.String()
		switch key {
		case "ctrl+c", "ctrl+d":
			if m.running {
				if key == "ctrl+d" {
					return m, nil
				}
				m.cancelActiveRun()
				return m, nil
			}
			m.cancel()
			return m, tea.Quit
		case "esc":
			if m.running {
				m.cancelActiveRun()
			}
			return m, nil
		case "ctrl+o":
			m.toggleAllDetails()
			m.refreshViewport(false)
			return m, nil
		case "enter":
			if m.running {
				m.submitSteering()
				return m, nil
			}
			if cmd := m.submit(); cmd != nil {
				return m, cmd
			}
			return m, nil
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.toggleDetailAt(msg.Y) {
				m.refreshViewport(false)
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.updateViewport(msg, &cmd)
		return m, cmd

	case chatEventMsg:
		if msg.runID != m.activeRunID {
			return m, waitForMsg(m.runCh)
		}
		m.applyChatEvent(msg.event)
		if shouldDebounceChatEvent(msg.event) {
			return m, tea.Batch(waitForMsg(m.runCh), m.queueTranscriptRefresh(m.autoFollow))
		}
		m.refreshViewport(m.autoFollow)
		return m, waitForMsg(m.runCh)

	case chatDoneMsg:
		if msg.runID != m.activeRunID {
			return m, waitForMsg(m.runCh)
		}
		if msg.conversationID != "" {
			m.conversationID = msg.conversationID
		}
		m.finishActiveBlocks()
		m.running = false
		m.cancelRun = nil
		m.activeRunID = 0
		if msg.err != nil {
			m.err = msg.err
			m.status = "error"
			idx := m.ensureAssistantEntry()
			appendTextBlock(&m.entries[idx], fmt.Sprintf("Error: %v", msg.err))
		} else {
			m.status = "ready"
		}
		m.refreshViewport(m.autoFollow)
		return m, waitForMsg(m.runCh)

	case transcriptRefreshMsg:
		if m.pendingRefresh {
			m.refreshViewport(m.pendingRefreshBottom)
		}
		m.pendingRefresh = false
		m.pendingRefreshBottom = false
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.running {
			m.workingFrame++
			m.refreshViewport(m.autoFollow)
		}
		cmds = append(cmds, cmd)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	if shouldUpdateViewport(msg) {
		var vpCmd tea.Cmd
		m.updateViewport(msg, &vpCmd)
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

func shouldUpdateViewport(msg tea.Msg) bool {
	if isVerticalViewportNavigation(msg) {
		return true
	}

	if msg, ok := msg.(tea.MouseMsg); ok {
		return isHorizontalViewportMouseNavigation(msg)
	}

	return false
}

func (m *model) updateViewport(msg tea.Msg, cmd *tea.Cmd) {
	before := m.viewport.YOffset
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	if cmd != nil {
		*cmd = vpCmd
	}
	if before != m.viewport.YOffset && isVerticalViewportNavigation(msg) {
		m.autoFollow = m.viewport.AtBottom()
	}
}

func isVerticalViewportNavigation(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "pgup", "pgdown":
			return true
		}
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Shift {
			return false
		}
		return msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown
	}
	return false
}

func isHorizontalViewportMouseNavigation(msg tea.MouseMsg) bool {
	if msg.Action != tea.MouseActionPress {
		return false
	}
	switch msg.Button {
	case tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		return true
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		return msg.Shift
	default:
		return false
	}
}

func (m *model) queueTranscriptRefresh(scrollBottom bool) tea.Cmd {
	m.pendingRefreshBottom = m.pendingRefreshBottom || scrollBottom
	if m.pendingRefresh {
		return nil
	}
	m.pendingRefresh = true
	return waitForTranscriptRefresh()
}

func shouldDebounceChatEvent(event chat.ChatEvent) bool {
	switch event.Kind {
	case "text-delta", "thinking-delta":
		return true
	default:
		return false
	}
}

func (m *model) resize() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	inputOuterHeight := inputHeight + 2
	footerHeight := 0
	viewportHeight := m.height - inputOuterHeight - footerHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.viewport.Width = m.contentWidth()
	m.viewport.Height = viewportHeight
	m.textarea.SetWidth(max(1, m.inputContentWidth()))
	m.textarea.SetHeight(inputHeight)
}

func (m *model) submit() tea.Cmd {
	message := strings.TrimSpace(m.textarea.Value())
	if message == "" {
		return nil
	}
	if strings.TrimSpace(m.conversationID) == "" {
		m.conversationID = convtypes.GenerateID()
	}

	m.textarea.Reset()
	m.entries = append(m.entries, chatEntry{kind: entryUser, content: message})
	m.running = true
	m.workingFrame = 0
	m.nextRunID++
	m.activeRunID = m.nextRunID
	m.status = "working"
	m.err = nil
	m.refreshViewport(true)

	runCtx, cancel := context.WithCancel(m.ctx)
	m.cancelRun = cancel
	runID := m.activeRunID
	runCh := m.runCh
	runner := m.runner
	req := chat.ChatRequest{
		Message:        message,
		ConversationID: m.conversationID,
		Profile:        profileForRequest(m.profile),
		CWD:            m.requestedCWD,
	}

	return func() tea.Msg {
		go func() {
			conversationID, err := runner.Run(runCtx, req, tuiSink{ch: runCh, runID: runID})
			runCh <- chatDoneMsg{runID: runID, conversationID: strings.TrimSpace(conversationID), err: err}
		}()
		return nil
	}
}

func (m *model) submitSteering() {
	message := strings.TrimSpace(m.textarea.Value())
	if message == "" {
		return
	}

	if len(message) > steer.MaxMessageLength {
		m.err = errors.New("steering message too long")
		m.steerError = "Steering message must be less than 10,000 characters."
		m.status = "steering failed"
		m.refreshViewport(true)
		return
	}

	if strings.TrimSpace(m.conversationID) == "" {
		m.conversationID = convtypes.GenerateID()
	}

	steerStore, err := steer.NewSteerStore()
	if err != nil {
		m.err = errors.Wrap(err, "failed to initialize steer store")
		m.steerError = fmt.Sprintf("Failed to queue steering: %v", err)
		m.status = "steering failed"
		m.refreshViewport(true)
		return
	}

	if err := steerStore.WriteSteer(m.conversationID, message); err != nil {
		m.err = errors.Wrap(err, "failed to write steering message")
		m.steerError = fmt.Sprintf("Failed to queue steering: %v", err)
		m.status = "steering failed"
		m.refreshViewport(true)
		return
	}

	m.textarea.Reset()
	m.queuedSteering = append(m.queuedSteering, message)
	m.steerError = ""
	m.status = "steering queued"
	m.refreshViewport(true)
}

func (m *model) stopRun() {
	if m.cancelRun != nil {
		m.cancelRun()
		m.cancelRun = nil
	}
}

func (m *model) cancelActiveRun() {
	m.stopRun()
	m.finishActiveBlocks()
	m.status = "cancelled"
	m.running = false
	m.activeRunID = 0
	m.refreshViewport(true)
}

func (m *model) finishActiveBlocks() {
	for entryIdx := range m.entries {
		if m.entries[entryIdx].kind != entryAssistant {
			continue
		}
		for blockIdx := range m.entries[entryIdx].blocks {
			block := &m.entries[entryIdx].blocks[blockIdx]
			switch block.kind {
			case blockThoughts:
				for thoughtIdx := range block.thoughts {
					block.thoughts[thoughtIdx].done = true
				}
			case blockTools:
				for toolIdx := range block.tools {
					block.tools[toolIdx].done = true
				}
			}
		}
	}
}

func (m *model) insertTextareaNewline() {
	m.textarea.InsertString("\n")
}
