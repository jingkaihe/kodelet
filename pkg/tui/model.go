// Package tui implements Kodelet's native terminal chat interface.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/pkg/errors"
)

const inputHeight = 5

// Config configures the native chat TUI.
type Config struct {
	ConversationID string
	Profile        string
	CWD            string
	Runner         webui.ChatRunner
}

// Run starts the native Kodelet chat TUI.
func Run(ctx context.Context, config Config) error {
	model := newModel(ctx, config)

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := program.Run()
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
	id     string
	name   string
	input  string
	result string
	done   bool
	failed bool
}

type assistantBlockKind int

const (
	blockText assistantBlockKind = iota
	blockThoughts
	blockTools
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
	entryIndex int
	blockIndex int
	kind       detailKind
	line       int
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

	width  int
	height int

	entries []chatEntry
	usage   llmtypes.Usage

	running     bool
	activeRunID int
	nextRunID   int
	runCh       chan tea.Msg
	cancelRun   context.CancelFunc

	detailRegions []detailRegion
	status        string
	err           error
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
			if location, ok := toolIndex[msg.ToolCallID]; ok && location[0] < len(entries) {
				blockIdx, toolIdx := findToolLocation(entries[location[0]], msg.ToolCallID)
				if blockIdx >= 0 && toolIdx >= 0 {
					entries[location[0]].blocks[blockIdx].tools[toolIdx].result = msg.Content
					entries[location[0]].blocks[blockIdx].tools[toolIdx].done = true
				}
			} else {
				blockIdx := appendToolBlock(&entries[idx])
				entries[idx].blocks[blockIdx].tools = append(entries[idx].blocks[blockIdx].tools, toolCall{id: msg.ToolCallID, name: msg.ToolName, result: msg.Content, done: true})
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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		switch {
		case msg.String() == "ctrl+c" || msg.String() == "ctrl+d":
			if m.running {
				if msg.String() == "ctrl+d" {
					return m, nil
				}
				m.cancelActiveRun()
				return m, nil
			}
			m.cancel()
			return m, tea.Quit
		case msg.String() == "esc":
			if m.running {
				m.cancelActiveRun()
			}
			return m, nil
		case msg.String() == "ctrl+t" || msg.String() == "alt+t":
			m.toggleAllDetails()
			m.refreshViewport(false)
			return m, nil
		case msg.String() == "shift+enter":
			if !m.running {
				m.textarea.InsertString("\n")
			}
			return m, nil
		case msg.String() == "enter":
			if !m.running {
				if cmd := m.submit(); cmd != nil {
					return m, cmd
				}
				return m, nil
			}
		case msg.String() == "alt+enter" || msg.String() == "ctrl+j":
			// Let textarea keep the newline binding.
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.toggleDetailAt(msg.Y) {
				m.refreshViewport(false)
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case chatEventMsg:
		if msg.runID != m.activeRunID {
			return m, waitForMsg(m.runCh)
		}
		m.applyChatEvent(msg.event)
		m.refreshViewport(true)
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
		m.refreshViewport(true)
		return m, waitForMsg(m.runCh)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.running {
			m.refreshViewport(true)
		}
		cmds = append(cmds, cmd)
	}

	if !m.running {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
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
		// The TUI already renders the submitted message immediately.
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
		blockIdx := appendToolBlock(&m.entries[idx])
		m.entries[idx].blocks[blockIdx].tools = append(m.entries[idx].blocks[blockIdx].tools, toolCall{
			id:     event.ToolCallID,
			name:   event.ToolName,
			result: resultText,
			done:   true,
			failed: failed,
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
	if scrollBottom {
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

func (m model) renderTranscript() (string, []detailRegion) {
	var b strings.Builder
	regions := []detailRegion{}
	line := 0

	if len(m.entries) == 0 {
		intro := mutedStyle.Render("Hello! What would you like me to work on?")
		b.WriteString("\n")
		b.WriteString(intro)
		b.WriteString("\n")
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
			for blockIdx, block := range entry.blocks {
				switch block.kind {
				case blockThoughts:
					if len(block.thoughts) == 0 {
						continue
					}
					header := m.renderThoughtHeader(block)
					b.WriteString(header)
					b.WriteString("\n")
					regions = append(regions, detailRegion{entryIndex: i, blockIndex: blockIdx, kind: detailThoughts, line: line})
					line++
					if block.expanded || hasActiveThought(block) {
						body := indentText(wrapText(joinThoughts(block.thoughts), m.transcriptTextWidth()-2), "  ")
						if strings.TrimSpace(body) != "" {
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
					header := m.renderToolHeader(block)
					b.WriteString(header)
					b.WriteString("\n")
					regions = append(regions, detailRegion{entryIndex: i, blockIndex: blockIdx, kind: detailTools, line: line})
					line++
					if block.expanded || hasActiveTool(block) {
						body := indentText(wrapText(joinTools(block.tools), m.transcriptTextWidth()-2), "  ")
						if strings.TrimSpace(body) != "" {
							rendered := toolBodyStyle.Render(body)
							b.WriteString(rendered)
							b.WriteString("\n")
							line += lineCount(rendered)
						}
					}
				case blockText:
					trimmed := strings.TrimSpace(block.text)
					if trimmed != "" {
						wrapped := wrapText(trimmed, m.transcriptTextWidth())
						rendered := assistantStyle.Render(wrapped)
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
	}

	return b.String(), regions
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

func (m model) renderToolHeader(block assistantBlock) string {
	active := hasActiveTool(block)
	count := len(block.tools)
	word := "Tools"
	if count == 1 {
		word = "Tool"
	}
	if active {
		return toolHeaderStyle.Render(fmt.Sprintf("%s Running %d %s… ▾", m.spinner.View(), count, word))
	}
	chevron := "▸"
	if block.expanded {
		chevron = "▾"
	}
	verb := "Ran"
	if anyFailedTool(block) {
		verb = "Ran"
	}
	return toolHeaderStyle.Render(fmt.Sprintf("✓ %s %d %s %s", verb, count, word, chevron))
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
		m.entries[region.entryIndex].blocks[region.blockIndex].expanded = !m.entries[region.entryIndex].blocks[region.blockIndex].expanded
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

func anyFailedTool(block assistantBlock) bool {
	for _, tool := range block.tools {
		if tool.failed {
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
	return strings.Join(parts, "\n")
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

	thoughtHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	thoughtBodyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	toolHeaderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("151")).Bold(true)
	toolBodyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))

	inputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("147"))
)
