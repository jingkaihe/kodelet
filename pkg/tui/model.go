// Package tui implements Kodelet's native terminal chat interface.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aymanbagabas/go-udiff"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/pkg/errors"
)

const (
	inputHeight            = 5
	transcriptRefreshDelay = 16 * time.Millisecond
)

// Config configures the native chat TUI.
type Config struct {
	ConversationID string
	Profile        string
	CWD            string
	Runner         webui.ChatRunner
}

// Run starts the native Kodelet chat TUI.
func Run(ctx context.Context, config Config) error {
	initialModel := newModel(ctx, config)

	program := tea.NewProgram(initialModel, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	if final, ok := finalModel.(model); ok {
		if summary := renderExitSummary(final.conversationID, final.usage); summary != "" {
			fmt.Fprintln(os.Stdout, summary)
		}
	}
	return err
}

type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
)

type detailKind int

const (
	detailThoughts detailKind = iota
	detailTools
)

type thoughtBlock struct {
	text string
	done bool
}

type toolCall struct {
	id              string
	name            string
	input           string
	result          string
	done            bool
	failed          bool
	structured      *tooltypes.StructuredToolResult
	expanded        bool
	expandedChanges map[int]bool
}

type assistantBlockKind int

const (
	blockText assistantBlockKind = iota
	blockThoughts
	blockTools
)

type markdownKind int

const (
	markdownAssistant markdownKind = iota
	markdownThought
)

type assistantBlock struct {
	kind     assistantBlockKind
	text     string
	thoughts []thoughtBlock
	tools    []toolCall
	expanded bool
}

type chatEntry struct {
	kind    entryKind
	content string
	blocks  []assistantBlock
}

type detailRegion struct {
	entryIndex  int
	blockIndex  int
	kind        detailKind
	line        int
	toolStart   int
	toolEnd     int
	changeIndex int
}

type model struct {
	ctx    context.Context
	cancel context.CancelFunc
	runner webui.ChatRunner

	conversationID string
	profile        string
	cwd            string

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	width      int
	height     int
	autoFollow bool

	pendingRefresh       bool
	pendingRefreshBottom bool

	entries []chatEntry
	usage   llmtypes.Usage

	assistantMarkdownRenderer      *glamour.TermRenderer
	assistantMarkdownRendererWidth int
	thoughtMarkdownRenderer        *glamour.TermRenderer
	thoughtMarkdownRendererWidth   int

	running     bool
	activeRunID int
	nextRunID   int
	runCh       chan tea.Msg
	cancelRun   context.CancelFunc

	detailRegions  []detailRegion
	queuedSteering []string
	steerError     string
	status         string
	err            error
}

type chatEventMsg struct {
	runID int
	event webui.ChatEvent
}

type chatDoneMsg struct {
	runID          int
	conversationID string
	err            error
}

type initialHistoryMsg struct {
	entries []chatEntry
	usage   llmtypes.Usage
	err     error
}

type transcriptRefreshMsg struct{}

type tuiSink struct {
	ch    chan<- tea.Msg
	runID int
}

func (s tuiSink) Send(event webui.ChatEvent) error {
	s.ch <- chatEventMsg{runID: s.runID, event: event}
	return nil
}

func newModel(ctx context.Context, config Config) model {
	mctx, cancel := context.WithCancel(ctx)

	ta := textarea.New()
	ta.Placeholder = "Ask kodelet..."
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.SetHeight(inputHeight)
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	runner := config.Runner
	if runner == nil {
		runner = webui.NewDefaultChatRunner(config.CWD)
	}
	cwd := strings.TrimSpace(config.CWD)
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	return model{
		ctx:            mctx,
		cancel:         cancel,
		runner:         runner,
		conversationID: strings.TrimSpace(config.ConversationID),
		profile:        displayProfile(config.Profile),
		cwd:            cwd,
		viewport:       vp,
		textarea:       ta,
		spinner:        sp,
		autoFollow:     true,
		runCh:          make(chan tea.Msg, 256),
		status:         "ready",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		waitForMsg(m.runCh),
		loadInitialHistory(m.ctx, m.conversationID),
	)
}

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

func loadInitialHistory(ctx context.Context, conversationID string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(conversationID) == "" {
			return initialHistoryMsg{}
		}

		service, err := conversations.GetDefaultConversationService(ctx)
		if err != nil {
			return initialHistoryMsg{err: errors.Wrap(err, "failed to open conversation store")}
		}
		defer service.Close()

		response, err := service.GetConversation(ctx, conversationID)
		if err != nil {
			return initialHistoryMsg{err: errors.Wrap(err, "failed to load conversation")}
		}

		messages, err := llm.ExtractConversationEntries(response.Provider, response.RawMessages, response.Metadata, response.ToolResults)
		if err != nil {
			return initialHistoryMsg{err: errors.Wrap(err, "failed to parse conversation")}
		}

		return initialHistoryMsg{entries: entriesFromHistory(messages), usage: response.Usage}
	}
}

func entriesFromHistory(messages []conversations.StreamableMessage) []chatEntry {
	entries := make([]chatEntry, 0)
	toolIndex := map[string][2]int{}

	ensureAssistant := func() int {
		if len(entries) == 0 || entries[len(entries)-1].kind != entryAssistant {
			entries = append(entries, chatEntry{kind: entryAssistant})
		}
		return len(entries) - 1
	}

	for _, msg := range messages {
		switch msg.Kind {
		case "text":
			switch msg.Role {
			case "user":
				entries = append(entries, chatEntry{kind: entryUser, content: strings.TrimSpace(msg.Content)})
			case "assistant":
				idx := ensureAssistant()
				appendTextBlock(&entries[idx], msg.Content)
			}
		case "thinking":
			idx := ensureAssistant()
			blockIdx := appendThoughtBlock(&entries[idx])
			entries[idx].blocks[blockIdx].thoughts = append(entries[idx].blocks[blockIdx].thoughts, thoughtBlock{text: msg.Content, done: true})
		case "tool-use":
			idx := ensureAssistant()
			blockIdx := appendToolBlock(&entries[idx])
			toolIndex[msg.ToolCallID] = [2]int{idx, len(entries[idx].blocks[blockIdx].tools)}
			entries[idx].blocks[blockIdx].tools = append(entries[idx].blocks[blockIdx].tools, toolCall{id: msg.ToolCallID, name: msg.ToolName, input: msg.Input})
		case "tool-result":
			idx := ensureAssistant()
			structuredResult, hasStructuredResult := parseStructuredToolResult(msg.Content)
			resultText := msg.Content
			failed := false
			if hasStructuredResult {
				resultText = structuredToolResultText(structuredResult)
				failed = !structuredResult.Success
				if msg.ToolName == "" {
					msg.ToolName = structuredResult.ToolName
				}
			}
			if location, ok := toolIndex[msg.ToolCallID]; ok && location[0] < len(entries) {
				blockIdx, toolIdx := findToolLocation(entries[location[0]], msg.ToolCallID)
				if blockIdx >= 0 && toolIdx >= 0 {
					tool := &entries[location[0]].blocks[blockIdx].tools[toolIdx]
					tool.result = resultText
					tool.done = true
					tool.failed = failed
					if hasStructuredResult {
						tool.structured = structuredResult
						if tool.name == "" {
							tool.name = structuredResult.ToolName
						}
					}
				}
			} else {
				blockIdx := appendToolBlock(&entries[idx])
				entries[idx].blocks[blockIdx].tools = append(entries[idx].blocks[blockIdx].tools, toolCall{id: msg.ToolCallID, name: msg.ToolName, result: resultText, done: true, failed: failed, structured: structuredResult})
			}
		}
	}

	for i := range entries {
		entries[i].content = strings.TrimSpace(entries[i].content)
		trimEntryBlocks(&entries[i])
	}
	return entries
}

func appendTextBlock(entry *chatEntry, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if len(entry.blocks) > 0 && entry.blocks[len(entry.blocks)-1].kind == blockText {
		last := len(entry.blocks) - 1
		entry.blocks[last].text += text
		entry.content += text
		return
	}
	entry.blocks = append(entry.blocks, assistantBlock{kind: blockText, text: text})
	entry.content += text
}

func appendThoughtBlock(entry *chatEntry) int {
	if len(entry.blocks) > 0 && entry.blocks[len(entry.blocks)-1].kind == blockThoughts {
		return len(entry.blocks) - 1
	}
	entry.blocks = append(entry.blocks, assistantBlock{kind: blockThoughts})
	return len(entry.blocks) - 1
}

func appendToolBlock(entry *chatEntry) int {
	if len(entry.blocks) > 0 && entry.blocks[len(entry.blocks)-1].kind == blockTools {
		return len(entry.blocks) - 1
	}
	entry.blocks = append(entry.blocks, assistantBlock{kind: blockTools})
	return len(entry.blocks) - 1
}

func findToolLocation(entry chatEntry, toolCallID string) (int, int) {
	for blockIdx, block := range entry.blocks {
		if block.kind != blockTools {
			continue
		}
		for toolIdx, tool := range block.tools {
			if tool.id == toolCallID {
				return blockIdx, toolIdx
			}
		}
	}
	return -1, -1
}

func trimEntryBlocks(entry *chatEntry) {
	for i := range entry.blocks {
		entry.blocks[i].text = strings.TrimSpace(entry.blocks[i].text)
	}
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
			m.refreshViewport(m.autoFollow)
		}
		cmds = append(cmds, cmd)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	var vpCmd tea.Cmd
	m.updateViewport(msg, &vpCmd)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
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
		case "up", "pgup", "ctrl+u", "down", "pgdown":
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

func (m *model) queueTranscriptRefresh(scrollBottom bool) tea.Cmd {
	m.pendingRefreshBottom = m.pendingRefreshBottom || scrollBottom
	if m.pendingRefresh {
		return nil
	}
	m.pendingRefresh = true
	return waitForTranscriptRefresh()
}

func shouldDebounceChatEvent(event webui.ChatEvent) bool {
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
	m.viewport.Width = m.width
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
	req := webui.ChatRequest{
		Message:        message,
		ConversationID: m.conversationID,
		Profile:        profileForRequest(m.profile),
		CWD:            m.cwd,
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

func (m *model) applyChatEvent(event webui.ChatEvent) {
	if event.ConversationID != "" {
		m.conversationID = event.ConversationID
	}

	switch event.Kind {
	case "conversation":
		return
	case "user-message":
		content := userMessageContentText(event.Content)
		if content == "" {
			return
		}
		m.removeQueuedSteering(content)
		if m.hasLastUserEntry(content) {
			return
		}
		m.entries = append(m.entries, chatEntry{kind: entryUser, content: content})
		return
	case "text", "text-delta":
		idx := m.ensureAssistantEntry()
		text := event.Delta
		if text == "" {
			text, _ = event.Content.(string)
		}
		appendTextBlock(&m.entries[idx], text)
	case "thinking-start":
		idx := m.ensureAssistantEntry()
		blockIdx := appendThoughtBlock(&m.entries[idx])
		m.entries[idx].blocks[blockIdx].thoughts = append(m.entries[idx].blocks[blockIdx].thoughts, thoughtBlock{})
	case "thinking", "thinking-delta":
		idx := m.ensureAssistantEntry()
		text := event.Delta
		if text == "" {
			text, _ = event.Content.(string)
		}
		blockIdx := appendThoughtBlock(&m.entries[idx])
		if len(m.entries[idx].blocks[blockIdx].thoughts) == 0 {
			m.entries[idx].blocks[blockIdx].thoughts = append(m.entries[idx].blocks[blockIdx].thoughts, thoughtBlock{})
		}
		last := len(m.entries[idx].blocks[blockIdx].thoughts) - 1
		m.entries[idx].blocks[blockIdx].thoughts[last].text += text
	case "thinking-end":
		idx := m.ensureAssistantEntry()
		for blockIdx := len(m.entries[idx].blocks) - 1; blockIdx >= 0; blockIdx-- {
			block := &m.entries[idx].blocks[blockIdx]
			if block.kind != blockThoughts || len(block.thoughts) == 0 {
				continue
			}
			block.thoughts[len(block.thoughts)-1].done = true
			break
		}
	case "tool-use":
		idx := m.ensureAssistantEntry()
		blockIdx := appendToolBlock(&m.entries[idx])
		m.entries[idx].blocks[blockIdx].tools = append(m.entries[idx].blocks[blockIdx].tools, toolCall{
			id:    event.ToolCallID,
			name:  event.ToolName,
			input: event.Input,
		})
	case "tool-result":
		idx := m.ensureAssistantEntry()
		resultText := structuredToolResultText(event.ToolResult)
		if blockIdx, toolIdx := findToolLocation(m.entries[idx], event.ToolCallID); blockIdx >= 0 && toolIdx >= 0 {
			tool := &m.entries[idx].blocks[blockIdx].tools[toolIdx]
			tool.result = resultText
			tool.done = true
			if event.ToolResult != nil {
				tool.failed = !event.ToolResult.Success
				tool.structured = event.ToolResult
				if tool.name == "" {
					tool.name = event.ToolResult.ToolName
				}
			}
			return
		}
		for blockIdx := len(m.entries[idx].blocks) - 1; blockIdx >= 0; blockIdx-- {
			if m.entries[idx].blocks[blockIdx].kind != blockTools {
				continue
			}
			for toolIdx := range m.entries[idx].blocks[blockIdx].tools {
				if m.entries[idx].blocks[blockIdx].tools[toolIdx].id == event.ToolCallID {
					tool := &m.entries[idx].blocks[blockIdx].tools[toolIdx]
					tool.result = resultText
					tool.done = true
					if event.ToolResult != nil {
						tool.failed = !event.ToolResult.Success
						tool.structured = event.ToolResult
						if tool.name == "" {
							tool.name = event.ToolResult.ToolName
						}
					}
					return
				}
			}
		}
		failed := false
		if event.ToolResult != nil {
			failed = !event.ToolResult.Success
		}
		toolName := event.ToolName
		if event.ToolResult != nil && strings.TrimSpace(toolName) == "" {
			toolName = event.ToolResult.ToolName
		}
		blockIdx := appendToolBlock(&m.entries[idx])
		m.entries[idx].blocks[blockIdx].tools = append(m.entries[idx].blocks[blockIdx].tools, toolCall{
			id:         event.ToolCallID,
			name:       toolName,
			result:     resultText,
			done:       true,
			failed:     failed,
			structured: event.ToolResult,
		})
	case "usage":
		if event.Usage != nil {
			m.usage = *event.Usage
		}
	case "ui-input", "ui-confirm", "ui-select", "ui-notify":
		idx := m.ensureAssistantEntry()
		appendTextBlock(&m.entries[idx], "Extension requested interactive input; TUI prompt handling is not implemented yet.")
	case "error":
		idx := m.ensureAssistantEntry()
		appendTextBlock(&m.entries[idx], event.Error)
	}
}

func (m *model) ensureAssistantEntry() int {
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].kind != entryAssistant {
		m.entries = append(m.entries, chatEntry{kind: entryAssistant})
	}
	return len(m.entries) - 1
}

func (m *model) refreshViewport(scrollBottom bool) {
	content, regions := m.renderTranscript()
	m.detailRegions = regions
	m.viewport.SetContent(content)
	m.pendingRefresh = false
	m.pendingRefreshBottom = false
	if scrollBottom {
		m.autoFollow = true
		m.viewport.GotoBottom()
	}
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	transcript := m.viewport.View()
	input := m.renderInputBox()
	return lipgloss.JoinVertical(lipgloss.Left, transcript, input)
}

func (m *model) renderTranscript() (string, []detailRegion) {
	var b strings.Builder
	regions := []detailRegion{}
	line := 0

	if len(m.entries) == 0 {
		intro := mutedStyle.Render("Hello! What would you like me to work on?")
		b.WriteString("\n")
		b.WriteString(intro)
		b.WriteString("\n")
		line += lineCount(intro) + 2
		m.renderQueuedSteering(&b, &line)
		return b.String(), regions
	}

	for i, entry := range m.entries {
		if i > 0 {
			b.WriteString("\n")
			line++
		}

		switch entry.kind {
		case entryUser:
			block := userStyle.Render("│ " + strings.ReplaceAll(wrapText(strings.TrimSpace(entry.content), m.transcriptTextWidth()-2), "\n", "\n│ "))
			b.WriteString(block)
			b.WriteString("\n")
			line += lineCount(block)
		case entryAssistant:
			renderedAssistantBlock := false
			for blockIdx, block := range entry.blocks {
				switch block.kind {
				case blockThoughts:
					if len(block.thoughts) == 0 {
						continue
					}
					m.renderAssistantBlockSeparator(&b, &line, &renderedAssistantBlock)
					header := m.renderThoughtHeader(block)
					b.WriteString(header)
					b.WriteString("\n")
					regions = append(regions, detailRegion{entryIndex: i, blockIndex: blockIdx, kind: detailThoughts, line: line})
					line++
					if block.expanded || hasActiveThought(block) {
						body := indentText(m.renderMarkdown(joinThoughts(block.thoughts), m.transcriptTextWidth()-2, markdownThought), "  ")
						if strings.TrimSpace(body) != "" {
							b.WriteString("\n")
							line++
							rendered := thoughtBodyStyle.Render(body)
							b.WriteString(rendered)
							b.WriteString("\n")
							line += lineCount(rendered)
						}
					}
				case blockTools:
					if len(block.tools) == 0 {
						continue
					}
					for _, group := range m.toolRenderGroups(block) {
						m.renderAssistantBlockSeparator(&b, &line, &renderedAssistantBlock)
						header := m.renderToolGroupHeader(group)
						b.WriteString(header)
						b.WriteString("\n")
						regions = append(regions, detailRegion{entryIndex: i, blockIndex: blockIdx, kind: detailTools, line: line, toolStart: group.toolStart, toolEnd: group.toolEnd, changeIndex: group.changeIndex})
						line++
						if group.expanded || group.active {
							body := group.body
							if group.wrapBody {
								body = wrapText(body, m.transcriptTextWidth()-2)
							}
							body = indentText(body, "  ")
							if strings.TrimSpace(body) != "" {
								rendered := toolBodyStyle.Render(body)
								b.WriteString(rendered)
								b.WriteString("\n")
								line += lineCount(rendered)
							}
						}
					}
				case blockText:
					trimmed := strings.TrimSpace(block.text)
					if trimmed != "" {
						m.renderAssistantBlockSeparator(&b, &line, &renderedAssistantBlock)
						renderedMarkdown := m.renderMarkdown(trimmed, m.transcriptTextWidth(), markdownAssistant)
						rendered := assistantStyle.Render(renderedMarkdown)
						b.WriteString(rendered)
						b.WriteString("\n")
						line += lineCount(rendered)
					}
				}
			}
		}
	}

	if m.running && len(m.entries) > 0 && m.entries[len(m.entries)-1].kind != entryAssistant {
		lineText := mutedStyle.Render(m.spinner.View() + " working…")
		b.WriteString("\n")
		b.WriteString(lineText)
		b.WriteString("\n")
		line += lineCount(lineText) + 2
	}

	m.renderQueuedSteering(&b, &line)

	return b.String(), regions
}

func (m model) renderAssistantBlockSeparator(b *strings.Builder, line *int, renderedBlock *bool) {
	if !*renderedBlock {
		*renderedBlock = true
		return
	}
	b.WriteString("\n")
	(*line)++
}

func (m model) renderQueuedSteering(b *strings.Builder, line *int) {
	if len(m.queuedSteering) == 0 && strings.TrimSpace(m.steerError) == "" {
		return
	}

	b.WriteString("\n")
	(*line)++
	for _, message := range m.queuedSteering {
		rendered := steeringStyle.Render("↳ queued steering: " + wrapText(strings.TrimSpace(message), m.transcriptTextWidth()-20))
		b.WriteString(rendered)
		b.WriteString("\n")
		*line += lineCount(rendered)
	}
	if trimmed := strings.TrimSpace(m.steerError); trimmed != "" {
		rendered := steeringErrorStyle.Render("⚠ " + wrapText(trimmed, m.transcriptTextWidth()-2))
		b.WriteString(rendered)
		b.WriteString("\n")
		*line += lineCount(rendered)
	}
}

func (m model) renderThoughtHeader(block assistantBlock) string {
	active := hasActiveThought(block)
	count := len(block.thoughts)
	word := "Thoughts"
	if count == 1 {
		word = "Thought"
	}
	if active {
		return thoughtHeaderStyle.Render(fmt.Sprintf("%s Thinking… ▾", m.spinner.View()))
	}
	chevron := "▸"
	if block.expanded {
		chevron = "▾"
	}
	return thoughtHeaderStyle.Render(fmt.Sprintf("✓ Had %d %s %s", count, word, chevron))
}

func (m model) renderToolGroupHeader(group toolRenderGroup) string {
	if group.active {
		return toolHeaderStyle.Render(fmt.Sprintf("%s %s… ▾", m.spinner.View(), group.runningLabel))
	}

	chevron := "▸"
	if group.expanded {
		chevron = "▾"
	}
	return toolHeaderStyle.Render(fmt.Sprintf("✓ %s %s", group.label, chevron))
}

type toolRenderGroup struct {
	toolStart    int
	toolEnd      int
	changeIndex  int
	label        string
	runningLabel string
	body         string
	wrapBody     bool
	expanded     bool
	active       bool
}

func (m model) toolRenderGroups(block assistantBlock) []toolRenderGroup {
	groups := []toolRenderGroup{}

	for idx := 0; idx < len(block.tools); {
		tool := block.tools[idx]
		switch {
		case isBashTool(tool):
			end := idx + 1
			for end < len(block.tools) && isBashTool(block.tools[end]) {
				end++
			}
			groups = append(groups, buildBashToolGroup(block, idx, end))
			idx = end

		case isApplyPatchTool(tool):
			applyGroups := buildApplyPatchToolGroups(block, idx)
			groups = append(groups, applyGroups...)
			idx++

		case isDedicatedBuiltinTool(tool):
			groups = append(groups, buildDedicatedBuiltinToolGroup(block, idx))
			idx++

		default:
			end := idx + 1
			for end < len(block.tools) && isFallbackAggregateTool(block.tools[end]) {
				end++
			}
			groups = append(groups, buildFallbackToolGroup(block, idx, end))
			idx = end
		}
	}

	return groups
}

func buildBashToolGroup(block assistantBlock, start, end int) toolRenderGroup {
	count := end - start
	return toolRenderGroup{
		toolStart:    start,
		toolEnd:      end - 1,
		changeIndex:  -1,
		label:        fmt.Sprintf("ran %d %s", count, pluralize(count, "command", "commands")),
		runningLabel: fmt.Sprintf("running %d %s", count, pluralize(count, "command", "commands")),
		body:         joinTools(block.tools[start:end]),
		wrapBody:     true,
		expanded:     block.expanded || anyExpandedTool(block.tools[start:end]),
		active:       hasActiveToolRange(block.tools[start:end]),
	}
}

func buildFallbackToolGroup(block assistantBlock, start, end int) toolRenderGroup {
	count := end - start
	return toolRenderGroup{
		toolStart:    start,
		toolEnd:      end - 1,
		changeIndex:  -1,
		label:        fmt.Sprintf("ran %d %s", count, pluralize(count, "tool", "tools")),
		runningLabel: fmt.Sprintf("running %d %s", count, pluralize(count, "tool", "tools")),
		body:         joinTools(block.tools[start:end]),
		wrapBody:     true,
		expanded:     block.expanded || anyExpandedTool(block.tools[start:end]),
		active:       hasActiveToolRange(block.tools[start:end]),
	}
}

func buildDedicatedBuiltinToolGroup(block assistantBlock, idx int) toolRenderGroup {
	tool := block.tools[idx]
	label, runningLabel := dedicatedBuiltinToolLabels(tool)
	return toolRenderGroup{
		toolStart:    idx,
		toolEnd:      idx,
		changeIndex:  -1,
		label:        label,
		runningLabel: runningLabel,
		body:         joinTools([]toolCall{tool}),
		wrapBody:     true,
		expanded:     block.expanded || tool.expanded,
		active:       !tool.done,
	}
}

func buildApplyPatchToolGroups(block assistantBlock, idx int) []toolRenderGroup {
	tool := block.tools[idx]
	changes, hasMetadata := applyPatchChanges(tool)
	if len(changes) == 0 {
		label := "Applied patch"
		if tool.failed {
			label = "Apply patch failed"
		}
		return []toolRenderGroup{{
			toolStart:    idx,
			toolEnd:      idx,
			changeIndex:  -1,
			label:        label,
			runningLabel: "Applying patch",
			body:         joinTools([]toolCall{tool}),
			wrapBody:     true,
			expanded:     block.expanded || tool.expanded,
			active:       !tool.done,
		}}
	}

	groups := make([]toolRenderGroup, 0, len(changes))
	for changeIdx, change := range changes {
		body := applyPatchChangeDiff(change)
		wrapBody := false
		if strings.TrimSpace(body) == "" && !hasMetadata {
			body = joinTools([]toolCall{tool})
			wrapBody = true
		}
		if strings.TrimSpace(body) == "" && strings.TrimSpace(tool.result) != "" {
			body = strings.TrimSpace(tool.result)
			wrapBody = true
		}

		groups = append(groups, toolRenderGroup{
			toolStart:    idx,
			toolEnd:      idx,
			changeIndex:  changeIdx,
			label:        applyPatchChangeLabel(change),
			runningLabel: "Applying patch",
			body:         body,
			wrapBody:     wrapBody,
			expanded:     block.expanded || tool.expanded || tool.expandedChanges[changeIdx],
			active:       !tool.done,
		})
	}

	return groups
}

func applyPatchChanges(tool toolCall) ([]tooltypes.ApplyPatchChange, bool) {
	if tool.structured == nil {
		return nil, false
	}

	var meta tooltypes.ApplyPatchMetadata
	if !tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) {
		return nil, false
	}

	if len(meta.Changes) > 0 {
		return meta.Changes, true
	}

	changes := make([]tooltypes.ApplyPatchChange, 0, len(meta.Added)+len(meta.Modified)+len(meta.Deleted))
	for _, path := range meta.Added {
		changes = append(changes, tooltypes.ApplyPatchChange{Path: path, Operation: tooltypes.ApplyPatchOperationAdd})
	}
	for _, path := range meta.Modified {
		changes = append(changes, tooltypes.ApplyPatchChange{Path: path, Operation: tooltypes.ApplyPatchOperationUpdate})
	}
	for _, path := range meta.Deleted {
		changes = append(changes, tooltypes.ApplyPatchChange{Path: path, Operation: tooltypes.ApplyPatchOperationDelete})
	}
	return changes, true
}

func applyPatchChangeLabel(change tooltypes.ApplyPatchChange) string {
	displayPath := change.Path
	if strings.TrimSpace(change.MovePath) != "" {
		displayPath = fmt.Sprintf("%s -> %s", change.Path, change.MovePath)
	}

	switch strings.ToLower(strings.TrimSpace(change.Operation)) {
	case tooltypes.ApplyPatchOperationAdd, "write":
		return fmt.Sprintf("write %s", displayPath)
	case tooltypes.ApplyPatchOperationDelete:
		return fmt.Sprintf("delete %s", displayPath)
	case "move":
		return fmt.Sprintf("move %s", displayPath)
	default:
		if strings.TrimSpace(change.MovePath) != "" {
			return fmt.Sprintf("move %s", displayPath)
		}
		return fmt.Sprintf("edit %s", displayPath)
	}
}

func applyPatchChangeDiff(change tooltypes.ApplyPatchChange) string {
	if strings.TrimSpace(change.UnifiedDiff) != "" {
		return strings.TrimSuffix(change.UnifiedDiff, "\n")
	}

	switch strings.ToLower(strings.TrimSpace(change.Operation)) {
	case tooltypes.ApplyPatchOperationAdd, "write":
		if change.OldContent != "" || change.NewContent != "" {
			return strings.TrimSuffix(udiff.Unified(change.Path, change.Path, change.OldContent, change.NewContent), "\n")
		}
	case tooltypes.ApplyPatchOperationDelete:
		if change.OldContent != "" {
			return strings.TrimSuffix(udiff.Unified(change.Path, change.Path, change.OldContent, ""), "\n")
		}
	case "move", tooltypes.ApplyPatchOperationUpdate:
		if change.OldContent != "" || change.NewContent != "" {
			newPath := change.Path
			if strings.TrimSpace(change.MovePath) != "" {
				newPath = change.MovePath
			}
			return strings.TrimSuffix(udiff.Unified(change.Path, newPath, change.OldContent, change.NewContent), "\n")
		}
	}

	return ""
}

func dedicatedBuiltinToolLabels(tool toolCall) (string, string) {
	switch normalizedToolName(tool) {
	case "openai_web_search", "web_search":
		return webSearchToolLabel(tool), "Searching web"
	case "web_fetch":
		return webFetchToolLabel(tool), "Fetching web page"
	case "view_image":
		return viewImageToolLabel(tool), "Viewing image"
	case "skill":
		return skillToolLabel(tool), "Loading skill"
	default:
		name := normalizedToolName(tool)
		if name == "" {
			name = "tool"
		}
		return fmt.Sprintf("ran %s", name), fmt.Sprintf("running %s", name)
	}
}

func webSearchToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.OpenAIWebSearchMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) {
			switch strings.ToLower(strings.TrimSpace(meta.Action)) {
			case "open_page":
				return fmt.Sprintf("Opened %s", firstNonEmpty(meta.URL, "web page"))
			case "find_in_page":
				if meta.Pattern != "" {
					return fmt.Sprintf("Searched %s for %q", firstNonEmpty(meta.URL, "web page"), meta.Pattern)
				}
				return fmt.Sprintf("Searched %s", firstNonEmpty(meta.URL, "web page"))
			case "search":
				if len(meta.Queries) > 0 {
					return fmt.Sprintf("Searched web for %q", meta.Queries[0])
				}
			}
		}
	}

	fields := toolInputFields(tool.input)
	if queries, ok := stringSliceField(fields, "queries"); ok && len(queries) > 0 {
		return fmt.Sprintf("Searched web for %q", queries[0])
	}
	if query := stringField(fields, "query"); query != "" {
		return fmt.Sprintf("Searched web for %q", query)
	}
	if url := stringField(fields, "url"); url != "" {
		return fmt.Sprintf("Opened %s", url)
	}
	return "Searched web"
}

func webFetchToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.WebFetchMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) && strings.TrimSpace(meta.URL) != "" {
			return fmt.Sprintf("Fetched %s", meta.URL)
		}
	}
	if url := stringField(toolInputFields(tool.input), "url"); url != "" {
		return fmt.Sprintf("Fetched %s", url)
	}
	return "Fetched web page"
}

func viewImageToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.ViewImageMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) && strings.TrimSpace(meta.Path) != "" {
			return fmt.Sprintf("Viewed image %s", meta.Path)
		}
	}
	if path := stringField(toolInputFields(tool.input), "path"); path != "" {
		return fmt.Sprintf("Viewed image %s", path)
	}
	return "Viewed image"
}

func skillToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.SkillMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) && strings.TrimSpace(meta.SkillName) != "" {
			return fmt.Sprintf("Loaded skill %s", meta.SkillName)
		}
	}
	if skillName := stringField(toolInputFields(tool.input), "skill_name"); skillName != "" {
		return fmt.Sprintf("Loaded skill %s", skillName)
	}
	if skillName := stringField(toolInputFields(tool.input), "skillName"); skillName != "" {
		return fmt.Sprintf("Loaded skill %s", skillName)
	}
	return "Loaded skill"
}

func isFallbackAggregateTool(tool toolCall) bool {
	return !isBashTool(tool) && !isApplyPatchTool(tool) && !isDedicatedBuiltinTool(tool)
}

func isBashTool(tool toolCall) bool {
	return normalizedToolName(tool) == "bash"
}

func isApplyPatchTool(tool toolCall) bool {
	return normalizedToolName(tool) == "apply_patch"
}

func isDedicatedBuiltinTool(tool toolCall) bool {
	switch normalizedToolName(tool) {
	case "openai_web_search", "web_search", "web_fetch", "view_image", "skill":
		return true
	default:
		return false
	}
}

func normalizedToolName(tool toolCall) string {
	if tool.structured != nil {
		if tool.structured.Metadata != nil {
			if metadataType := strings.TrimSpace(tool.structured.Metadata.ToolType()); metadataType != "" {
				return strings.ToLower(metadataType)
			}
		}
		if name := strings.TrimSpace(tool.structured.ToolName); name != "" {
			return strings.ToLower(name)
		}
	}
	return strings.ToLower(strings.TrimSpace(tool.name))
}

func anyExpandedTool(tools []toolCall) bool {
	for _, tool := range tools {
		if tool.expanded {
			return true
		}
	}
	return false
}

func hasActiveToolRange(tools []toolCall) bool {
	for _, tool := range tools {
		if !tool.done {
			return true
		}
	}
	return false
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func toolInputFields(input string) map[string]any {
	var fields map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(input)), &fields); err != nil {
		return nil
	}
	return fields
}

func stringField(fields map[string]any, key string) string {
	if len(fields) == 0 {
		return ""
	}
	value, _ := fields[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceField(fields map[string]any, key string) ([]string, bool) {
	if len(fields) == 0 {
		return nil, false
	}
	value, ok := fields[key]
	if !ok {
		return nil, false
	}

	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				items = append(items, strings.TrimSpace(text))
			}
		}
		return items, len(items) > 0
	default:
		return nil, false
	}
}

func (m model) renderInputBox() string {
	outerWidth := max(4, m.width-2)
	contentWidth := m.inputContentWidth()
	bodyLines := strings.Split(m.textarea.View(), "\n")
	for len(bodyLines) < inputHeight {
		bodyLines = append(bodyLines, "")
	}

	lines := []string{inputBorderStyle.Render(rightLabeledBorder("╭", "╮", outerWidth, m.inputTopRightLabel()))}
	for i := 0; i < inputHeight; i++ {
		lines = append(lines, inputBorderStyle.Render("│")+" "+padVisible(bodyLines[i], contentWidth)+" "+inputBorderStyle.Render("│"))
	}
	lines = append(lines, inputBorderStyle.Render(rightLabeledBorder("╰", "╯", outerWidth, displayCWD(m.cwd))))
	return strings.Join(lines, "\n")
}

func (m model) inputTopRightLabel() string {
	parts := []string{formatUsage(m.usage), m.profile}
	return strings.Join(parts, " — ")
}

func (m model) inputContentWidth() int {
	outerWidth := max(1, m.width-2)
	paddingWidth := 2
	borderWidth := 2
	return max(1, outerWidth-paddingWidth-borderWidth)
}

func (m model) transcriptTextWidth() int {
	if m.viewport.Width > 0 {
		return max(20, m.viewport.Width-2)
	}
	return max(20, m.width-2)
}

func (m *model) renderMarkdown(text string, width int, kind markdownKind) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	renderer, err := m.markdownRenderer(max(10, width), kind)
	if err != nil {
		return wrapText(text, width)
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return wrapText(text, width)
	}
	return strings.TrimSpace(rendered)
}

func (m *model) markdownRenderer(width int, kind markdownKind) (*glamour.TermRenderer, error) {
	if kind == markdownThought {
		if m.thoughtMarkdownRenderer != nil && m.thoughtMarkdownRendererWidth == width {
			return m.thoughtMarkdownRenderer, nil
		}
		renderer, err := newMarkdownRenderer(width, thoughtMarkdownStyle)
		if err != nil {
			return nil, err
		}
		m.thoughtMarkdownRenderer = renderer
		m.thoughtMarkdownRendererWidth = width
		return renderer, nil
	}

	if m.assistantMarkdownRenderer != nil && m.assistantMarkdownRendererWidth == width {
		return m.assistantMarkdownRenderer, nil
	}
	renderer, err := newMarkdownRenderer(width, assistantMarkdownStyle)
	if err != nil {
		return nil, err
	}
	m.assistantMarkdownRenderer = renderer
	m.assistantMarkdownRendererWidth = width
	return renderer, nil
}

func newMarkdownRenderer(width int, style ansi.StyleConfig) (*glamour.TermRenderer, error) {
	return glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(max(10, width)),
		glamour.WithPreservedNewLines(),
	)
}

func compactMarkdownStyle() ansi.StyleConfig {
	style := styles.DarkStyleConfig
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Color = nil
	style.Document.Margin = uintPtr(0)
	style.BlockQuote.Color = stringPtr("245")
	style.Paragraph.Margin = uintPtr(0)
	style.Heading.Color = stringPtr("147")
	style.Heading.Margin = uintPtr(0)
	style.H1.Margin = uintPtr(0)
	style.H1.Color = stringPtr("183")
	style.H1.BackgroundColor = nil
	style.H2.Color = stringPtr("147")
	style.H3.Color = stringPtr("147")
	style.H4.Color = stringPtr("147")
	style.H5.Color = stringPtr("147")
	style.H6.Color = stringPtr("245")
	style.HorizontalRule.Color = stringPtr("240")
	style.Link.Color = stringPtr("147")
	style.LinkText.Color = stringPtr("151")
	style.Image.Color = stringPtr("147")
	style.ImageText.Color = stringPtr("151")
	style.Code.Color = stringPtr("151")
	style.Code.BackgroundColor = nil
	style.H2.Margin = uintPtr(0)
	style.H3.Margin = uintPtr(0)
	style.H4.Margin = uintPtr(0)
	style.H5.Margin = uintPtr(0)
	style.H6.Margin = uintPtr(0)
	style.List.Margin = uintPtr(0)
	style.CodeBlock.Margin = uintPtr(0)
	style.CodeBlock.Color = stringPtr("248")
	if style.CodeBlock.Chroma != nil {
		chroma := *style.CodeBlock.Chroma
		style.CodeBlock.Chroma = &chroma
		style.CodeBlock.Chroma.Text.Color = stringPtr("252")
		style.CodeBlock.Chroma.Error.Color = stringPtr("252")
		style.CodeBlock.Chroma.Error.BackgroundColor = stringPtr("240")
		style.CodeBlock.Chroma.Comment.Color = stringPtr("244")
		style.CodeBlock.Chroma.CommentPreproc.Color = stringPtr("151")
		style.CodeBlock.Chroma.Keyword.Color = stringPtr("147")
		style.CodeBlock.Chroma.KeywordReserved.Color = stringPtr("147")
		style.CodeBlock.Chroma.KeywordNamespace.Color = stringPtr("147")
		style.CodeBlock.Chroma.KeywordType.Color = stringPtr("151")
		style.CodeBlock.Chroma.Operator.Color = stringPtr("147")
		style.CodeBlock.Chroma.Punctuation.Color = stringPtr("245")
		style.CodeBlock.Chroma.Name.Color = stringPtr("252")
		style.CodeBlock.Chroma.NameBuiltin.Color = stringPtr("151")
		style.CodeBlock.Chroma.NameTag.Color = stringPtr("147")
		style.CodeBlock.Chroma.NameAttribute.Color = stringPtr("151")
		style.CodeBlock.Chroma.NameClass.Color = stringPtr("252")
		style.CodeBlock.Chroma.NameClass.Underline = nil
		style.CodeBlock.Chroma.NameClass.Bold = nil
		style.CodeBlock.Chroma.NameDecorator.Color = stringPtr("151")
		style.CodeBlock.Chroma.NameFunction.Color = stringPtr("151")
		style.CodeBlock.Chroma.LiteralNumber.Color = stringPtr("183")
		style.CodeBlock.Chroma.LiteralString.Color = stringPtr("187")
		style.CodeBlock.Chroma.LiteralStringEscape.Color = stringPtr("151")
		style.CodeBlock.Chroma.GenericDeleted.Color = stringPtr("183")
		style.CodeBlock.Chroma.GenericInserted.Color = stringPtr("151")
		style.CodeBlock.Chroma.GenericSubheading.Color = stringPtr("147")
		style.CodeBlock.Chroma.Background.BackgroundColor = nil
	}
	return style
}

func uintPtr(value uint) *uint {
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func rightLabeledBorder(left, right string, width int, label string) string {
	if width <= 2 {
		return left + right
	}
	fill := []rune(strings.Repeat("─", width-2))
	label = strings.TrimSpace(label)
	if label != "" && len(fill) > 2 {
		label = " " + fitVisible(label, len(fill)-2) + " "
		labelRunes := []rune(label)
		start := len(fill) - len(labelRunes) - 1
		if start < 0 {
			start = 0
		}
		for i, r := range labelRunes {
			pos := start + i
			if pos >= len(fill) {
				break
			}
			fill[pos] = r
		}
	}
	return left + string(fill) + right
}

func padVisible(text string, width int) string {
	textWidth := lipgloss.Width(text)
	if textWidth >= width {
		return text
	}
	return text + strings.Repeat(" ", width-textWidth)
}

func fitVisible(text string, width int) string {
	if width <= 0 || lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

func wrapText(text string, width int) string {
	width = max(10, width)
	paragraphs := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if paragraph == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, wrapLine(paragraph, width)...)
	}
	return strings.Join(wrapped, "\n")
}

func wrapLine(line string, width int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{line}
	}
	lines := []string{}
	current := ""
	for _, word := range words {
		if current == "" {
			current = word
			continue
		}
		candidate := current + " " + word
		if lipgloss.Width(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		for lipgloss.Width(current) > width {
			chunk, rest := splitVisible(current, width)
			lines = append(lines, chunk)
			current = rest
		}
		if current != "" {
			lines = append(lines, current)
		}
	}
	return lines
}

func splitVisible(text string, width int) (string, string) {
	runes := []rune(text)
	for i := 1; i <= len(runes); i++ {
		if lipgloss.Width(string(runes[:i])) > width {
			return string(runes[:i-1]), string(runes[i-1:])
		}
	}
	return text, ""
}

func (m *model) toggleAllDetails() {
	shouldExpand := false
	for _, entry := range m.entries {
		if entry.kind != entryAssistant {
			continue
		}
		for _, block := range entry.blocks {
			if !isDetailBlock(block) {
				continue
			}
			if !block.expanded {
				shouldExpand = true
				break
			}
		}
		if shouldExpand {
			break
		}
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].kind != entryAssistant {
			continue
		}
		for blockIdx := range m.entries[i].blocks {
			if isDetailBlock(m.entries[i].blocks[blockIdx]) {
				m.entries[i].blocks[blockIdx].expanded = shouldExpand
			}
		}
	}
}

func isDetailBlock(block assistantBlock) bool {
	return (block.kind == blockThoughts && len(block.thoughts) > 0) || (block.kind == blockTools && len(block.tools) > 0)
}

func (m *model) toggleDetailAt(screenY int) bool {
	viewportY := screenY
	if viewportY < 0 || viewportY >= m.viewport.Height {
		return false
	}
	contentLine := m.viewport.YOffset + viewportY
	for _, region := range m.detailRegions {
		if region.line != contentLine || region.entryIndex < 0 || region.entryIndex >= len(m.entries) || region.blockIndex < 0 || region.blockIndex >= len(m.entries[region.entryIndex].blocks) {
			continue
		}
		block := &m.entries[region.entryIndex].blocks[region.blockIndex]
		if region.kind == detailTools && region.toolStart >= 0 && region.toolStart < len(block.tools) {
			if region.changeIndex >= 0 {
				tool := &block.tools[region.toolStart]
				if tool.expandedChanges == nil {
					tool.expandedChanges = map[int]bool{}
				}
				tool.expandedChanges[region.changeIndex] = !tool.expandedChanges[region.changeIndex]
				return true
			}

			shouldExpand := true
			end := min(region.toolEnd, len(block.tools)-1)
			for toolIdx := region.toolStart; toolIdx <= end; toolIdx++ {
				if !block.tools[toolIdx].expanded {
					shouldExpand = true
					break
				}
				shouldExpand = false
			}
			for toolIdx := region.toolStart; toolIdx <= end; toolIdx++ {
				block.tools[toolIdx].expanded = shouldExpand
			}
			return true
		}

		block.expanded = !block.expanded
		return true
	}
	return false
}

func hasActiveThought(block assistantBlock) bool {
	return len(block.thoughts) > 0 && !block.thoughts[len(block.thoughts)-1].done
}

func hasActiveTool(block assistantBlock) bool {
	for _, tool := range block.tools {
		if !tool.done {
			return true
		}
	}
	return false
}

func joinThoughts(thoughts []thoughtBlock) string {
	parts := make([]string, 0, len(thoughts))
	for _, thought := range thoughts {
		if trimmed := strings.TrimSpace(thought.text); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}

func joinTools(tools []toolCall) string {
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		status := "running"
		if tool.done {
			status = "done"
			if tool.failed {
				status = "failed"
			}
		}
		title := tool.name
		if title == "" {
			title = "tool"
		}
		part := fmt.Sprintf("%s — %s", title, status)
		if strings.TrimSpace(tool.input) != "" {
			part += "\ninput: " + compactJSON(tool.input)
		}
		if strings.TrimSpace(tool.result) != "" {
			part += "\nresult: " + strings.TrimSpace(tool.result)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n\n")
}

func structuredToolResultText(result *tooltypes.StructuredToolResult) string {
	if result == nil {
		return ""
	}
	rendered := renderers.NewRendererRegistry().Render(*result)
	if strings.TrimSpace(rendered) != "" {
		return rendered
	}
	if strings.TrimSpace(result.Error) != "" {
		return result.Error
	}
	return ""
}

func parseStructuredToolResult(content string) (*tooltypes.StructuredToolResult, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, false
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, false
	}
	if _, ok := raw["success"].(bool); !ok {
		return nil, false
	}

	var result tooltypes.StructuredToolResult
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false
	}
	if result.ToolName == "" && result.Metadata == nil {
		return nil, false
	}
	return &result, true
}

func userMessageContentText(content any) string {
	switch content := content.(type) {
	case string:
		return strings.TrimSpace(content)
	case []webui.WebContentBlock:
		return strings.TrimSpace(textFromWebContentBlocks(content))
	case []any:
		return strings.TrimSpace(textFromAnyContentBlocks(content))
	default:
		return ""
	}
}

func textFromWebContentBlocks(blocks []webui.WebContentBlock) string {
	parts := make([]string, 0, len(blocks))
	imageCount := 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		case "image":
			imageCount++
		}
	}
	if imageCount > 0 {
		parts = append(parts, fmt.Sprintf("[%d image%s]", imageCount, pluralSuffix(imageCount)))
	}
	return strings.Join(parts, "\n")
}

func textFromAnyContentBlocks(blocks []any) string {
	parts := make([]string, 0, len(blocks))
	imageCount := 0
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		typeValue, _ := block["type"].(string)
		switch typeValue {
		case "text":
			if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		case "image":
			imageCount++
		}
	}
	if imageCount > 0 {
		parts = append(parts, fmt.Sprintf("[%d image%s]", imageCount, pluralSuffix(imageCount)))
	}
	return strings.Join(parts, "\n")
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (m model) hasLastUserEntry(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" || len(m.entries) == 0 {
		return false
	}
	last := m.entries[len(m.entries)-1]
	return last.kind == entryUser && strings.TrimSpace(last.content) == content
}

func (m *model) removeQueuedSteering(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	for i, queued := range m.queuedSteering {
		if strings.TrimSpace(queued) != content {
			continue
		}
		m.queuedSteering = append(m.queuedSteering[:i], m.queuedSteering[i+1:]...)
		return
	}
}

func (m *model) insertTextareaNewline() {
	m.textarea.InsertString("\n")
}

func compactJSON(input string) string {
	var v any
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		return strings.TrimSpace(input)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(input)
	}
	return string(data)
}

func indentText(text, prefix string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = prefix
		} else {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func displayProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "default"
	}
	return profile
}

func profileForRequest(profile string) string {
	if strings.TrimSpace(profile) == "default" {
		return ""
	}
	return strings.TrimSpace(profile)
}

func formatUsage(usage llmtypes.Usage) string {
	cost := usage.TotalCost()
	if usage.MaxContextWindow <= 0 {
		return fmt.Sprintf("$%.2f", cost)
	}
	pct := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow) * 100
	return fmt.Sprintf("%s/%s (%.0f%%) · $%.2f", formatTokenCount(usage.CurrentContextWindow), formatTokenCount(usage.MaxContextWindow), pct, cost)
}

func renderExitSummary(conversationID string, usage llmtypes.Usage) string {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}

	lines := []string{
		fmt.Sprintf("Conversation ID: %s", conversationID),
		fmt.Sprintf("Token usage: %s input · %s output · %s cache write · %s cache read · %s total", formatTokenCount(usage.InputTokens), formatTokenCount(usage.OutputTokens), formatTokenCount(usage.CacheCreationInputTokens), formatTokenCount(usage.CacheReadInputTokens), formatTokenCount(usage.TotalTokens())),
	}
	if usage.MaxContextWindow > 0 {
		pct := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow) * 100
		lines = append(lines, fmt.Sprintf("Context window: %s/%s (%.0f%%)", formatTokenCount(usage.CurrentContextWindow), formatTokenCount(usage.MaxContextWindow), pct))
	}
	lines = append(lines,
		fmt.Sprintf("Cost: $%.4f", usage.TotalCost()),
		fmt.Sprintf("Resume: kodelet chat -r %s", conversationID),
	)
	return strings.Join(lines, "\n")
}

func formatTokenCount(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fK", float64(tokens)/1_000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

func displayCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "~"
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cwd); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + rel
		}
	}
	return cwd
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if utf8.RuneCountInString(id) <= 8 {
		return id
	}
	runes := []rune(id)
	return string(runes[:8])
}

var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Italic(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	assistantMarkdownStyle = compactMarkdownStyle()
	thoughtMarkdownStyle   = compactMarkdownStyle()

	thoughtHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	thoughtBodyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	toolHeaderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("151"))
	toolBodyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	steeringStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("183")).Italic(true)
	steeringErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	inputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("147"))
)
