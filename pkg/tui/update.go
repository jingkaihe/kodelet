package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/shlex"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
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
	if stringer, ok := msg.(fmt.Stringer); ok && m.activeUIPrompt == nil && isTextareaNewlineKey(stringer.String()) {
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

	case uiPromptRequestMsg:
		if msg.runID != m.activeRunID {
			return m, waitForMsg(m.runCh)
		}
		cmd := m.openUIPrompt(msg.prompt)
		return m, tea.Batch(waitForMsg(m.runCh), cmd)

	case uiNotificationMsg:
		if msg.runID != m.activeRunID {
			return m, waitForMsg(m.runCh)
		}
		cmd := m.addUINotification(msg.notification)
		return m, tea.Batch(waitForMsg(m.runCh), cmd)

	case uiNotificationExpiredMsg:
		m.removeUINotification(msg.id)
		return m, nil

	case editorFinishedMsg:
		cmd := m.applyEditorResult(msg)
		return m, cmd

	case initialHistoryMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "history load failed"
			m.profilePickerOpen = false
			m.entries = append(m.entries, chatEntry{
				kind: entryAssistant,
				blocks: []assistantBlock{{
					kind: blockText,
					text: fmt.Sprintf("Failed to resume conversation: %v", msg.err),
				}},
			})
		} else if msg.loaded {
			reloadSlashCommands := false
			if strings.TrimSpace(m.conversationID) != "" {
				m.setProfile(msg.profile)
				m.profilePickerOpen = false
			}
			if len(m.entries) != 0 {
				m.refreshViewport(true)
				break
			}
			if strings.TrimSpace(msg.cwd) != "" {
				reloadSlashCommands = strings.TrimSpace(m.requestedCWD) == "" && strings.TrimSpace(m.cwd) != strings.TrimSpace(msg.cwd)
				m.cwd = strings.TrimSpace(msg.cwd)
			}
			if len(msg.entries) > 0 {
				m.entries = msg.entries
				m.usage = msg.usage
				m.status = fmt.Sprintf("resumed %s", shortID(m.conversationID))
			}
			if reloadSlashCommands {
				cmds = append(cmds, loadSlashCommands(m.ctx, m.slashCommandCWD()))
			}
		}
		m.refreshViewport(true)

	case slashCommandsMsg:
		if strings.TrimSpace(msg.cwd) != strings.TrimSpace(m.slashCommandCWD()) {
			break
		}
		if msg.extensionsOnly {
			m.slashCommands = mergeSlashCommands(m.slashCommands, msg.commands)
		} else {
			m.slashCommands = msg.commands
			cmds = append(cmds, loadExtensionSlashCommands(m.ctx, m.slashCommandCWD()))
		}
		m.slashCommandErr = msg.err
		m.resetSlashCommandIndex()
		m.resize()
		m.refreshViewport(false)

	case tea.KeyMsg:
		key := msg.String()
		if m.shortcutsOpen {
			switch key {
			case "esc", "enter", "?", "q", "Q", "ctrl+c", "ctrl+d":
				m.shortcutsOpen = false
				m.refreshViewport(false)
				return m, nil
			default:
				return m, nil
			}
		}
		if m.activeUIPrompt != nil {
			cmd := m.updateUIPromptKey(msg)
			return m, cmd
		}
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
			if m.slashCommandSuggestionsOpen() {
				m.dismissSlashCommandSuggestions()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.closeProfilePicker()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.running {
				m.cancelActiveRun()
			}
			return m, nil
		case "ctrl+t":
			if m.canChangeProfile() {
				m.toggleProfilePickerFromKeyboard()
				m.resize()
				m.refreshViewport(false)
			}
			return m, nil
		case "ctrl+o":
			m.toggleAllDetails()
			m.refreshViewport(false)
			return m, nil
		case "ctrl+e":
			if cmd := m.openComposerInEditor(); cmd != nil {
				return m, cmd
			}
			return m, nil
		case "?":
			if strings.TrimSpace(m.textarea.Value()) == "" {
				m.openShortcutsDialog()
				return m, nil
			}
		case "up", "shift+tab":
			if m.slashCommandSuggestionsOpen() {
				m.moveSlashCommandSelection(-1)
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.moveProfilePicker(-1)
				m.refreshViewport(false)
				return m, nil
			}
		case "down", "tab":
			if m.slashCommandSuggestionsOpen() {
				if key == "tab" {
					m.selectSlashCommand()
					m.resize()
					m.refreshViewport(false)
				} else {
					m.moveSlashCommandSelection(1)
					m.refreshViewport(false)
				}
				return m, nil
			}
			if m.profilePickerOpen {
				m.moveProfilePicker(1)
				m.refreshViewport(false)
				return m, nil
			}
		case "enter":
			if m.slashCommandSuggestionsOpen() {
				m.selectSlashCommand()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.selectProfilePickerOption(m.profilePickerIndex)
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
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
		if m.shortcutsOpen {
			if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
				m.shortcutsOpen = false
				m.refreshViewport(false)
			}
			return m, nil
		}
		if m.activeUIPrompt != nil {
			return m, nil
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if optionIndex, ok := m.profilePickerOptionAt(msg.X, msg.Y); ok {
				m.selectProfilePickerOption(optionIndex)
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profileComposerRegionContains(msg.X, msg.Y) {
				m.toggleProfilePickerFromClick()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.closeProfilePicker()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
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
		if cmd, handled := m.handleUIChatEvent(msg.event); handled {
			return m, tea.Batch(waitForMsg(m.runCh), cmd)
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
		if m.activeUIPrompt != nil {
			m.resolveUIPrompt(extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
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

	if m.activeUIPrompt != nil {
		if m.activeUIPrompt.mode == uiPromptInput {
			var cmd tea.Cmd
			m.activeUIPrompt.input, cmd = m.activeUIPrompt.input.Update(msg)
			m.refreshViewport(false)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	previousTextareaValue := m.textarea.Value()
	previousSlashCommandHeight := m.slashCommandSuggestionsHeight()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)
	if m.textarea.Value() != previousTextareaValue {
		if m.slashDismissedDraft != "" && m.slashDismissedDraft != m.textarea.Value() {
			m.slashDismissedDraft = ""
		}
		m.slashCommandIndex = -1
	}
	if m.slashCommandSuggestionsHeight() != previousSlashCommandHeight {
		m.resize()
		m.refreshViewport(false)
	}

	if shouldUpdateViewport(msg) {
		var vpCmd tea.Cmd
		m.updateViewport(msg, &vpCmd)
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) updateUIPromptKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+d":
		m.dismissUIPrompt()
		return nil
	case "enter":
		m.submitUIPrompt()
		return nil
	case "up", "shift+tab", "left":
		if m.moveUISelect(-1) {
			m.refreshViewport(false)
			return nil
		}
	case "down", "tab", "right":
		if m.moveUISelect(1) {
			m.refreshViewport(false)
			return nil
		}
	case "y", "Y":
		if m.activeUIPrompt.mode == uiPromptConfirm {
			m.submitUIPrompt()
			return nil
		}
	case "n", "N":
		if m.activeUIPrompt.mode == uiPromptConfirm {
			m.dismissUIPrompt()
			return nil
		}
	}

	if m.activeUIPrompt.mode != uiPromptInput {
		return nil
	}
	var cmd tea.Cmd
	m.activeUIPrompt.input, cmd = m.activeUIPrompt.input.Update(msg)
	m.refreshViewport(false)
	return cmd
}

func (m *model) handleUIChatEvent(event chat.ChatEvent) (tea.Cmd, bool) {
	if prompt, ok := promptFromChatEvent(event); ok {
		return m.openUIPrompt(prompt), true
	}
	switch event.Kind {
	case "ui-notify", "ui-notification":
		if event.UINotify == nil {
			return nil, false
		}
		return m.addUINotification(uiNotification{title: event.UINotify.Title, message: event.UINotify.Message}), true
	default:
		return nil, false
	}
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

func (m *model) openShortcutsDialog() {
	m.profilePickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.shortcutsOpen = true
	m.resize()
	m.refreshViewport(false)
}

func (m *model) openComposerInEditor() tea.Cmd {
	if m.running {
		m.steerError = "Cannot edit in $EDITOR while Kodelet is running."
		m.refreshViewport(false)
		return nil
	}

	editorCommand := strings.TrimSpace(os.Getenv("EDITOR"))
	if editorCommand == "" {
		editorCommand = strings.TrimSpace(os.Getenv("VISUAL"))
	}
	if editorCommand == "" {
		m.steerError = "Set $EDITOR to use Ctrl+E."
		m.refreshViewport(false)
		return nil
	}

	path, err := writeComposerEditorFile(m.textarea.Value())
	if err != nil {
		m.steerError = "Failed to prepare $EDITOR: " + err.Error()
		m.refreshViewport(false)
		return nil
	}

	cmd, err := editorExecCommand(editorCommand, path)
	if err != nil {
		_ = os.Remove(path)
		m.steerError = "Failed to launch $EDITOR: " + err.Error()
		m.refreshViewport(false)
		return nil
	}
	m.profilePickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.shortcutsOpen = false
	m.steerError = ""
	m.status = "editing"
	m.refreshViewport(false)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorFinishedMsg{path: path, err: err}
	})
}

func writeComposerEditorFile(value string) (string, error) {
	file, err := os.CreateTemp("", "kodelet-composer-*.md")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.WriteString(value); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func editorExecCommand(editorCommand, path string) (*exec.Cmd, error) {
	parts, err := shlex.Split(editorCommand)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return nil, errors.New("empty editor command")
	}
	args := append([]string{}, parts[1:]...)
	args = append(args, path)
	cmd := exec.Command(parts[0], args...) //nolint:gosec // user-provided editor command is intentional.
	return cmd, nil
}

func (m *model) applyEditorResult(msg editorFinishedMsg) tea.Cmd {
	defer func() { _ = os.Remove(msg.path) }()

	if msg.err != nil {
		m.status = "ready"
		m.steerError = "Editor failed: " + msg.err.Error()
		m.refreshViewport(false)
		return nil
	}

	content, err := os.ReadFile(filepath.Clean(msg.path))
	if err != nil {
		m.status = "ready"
		m.steerError = "Failed to read edited draft: " + err.Error()
		m.refreshViewport(false)
		return nil
	}

	m.status = "ready"
	m.steerError = ""
	m.textarea.SetValue(strings.TrimRight(string(content), "\n"))
	m.resize()
	m.refreshViewport(false)
	return textarea.Blink
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
	slashCommandHeight := m.slashCommandSuggestionsHeight()
	profilePickerHeight := m.profilePickerHeight()
	footerHeight := 0
	viewportHeight := m.height - inputOuterHeight - slashCommandHeight - profilePickerHeight - footerHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.viewport.Width = m.contentWidth()
	m.viewport.Height = viewportHeight
	m.textarea.SetWidth(max(1, m.inputContentWidth()))
	m.textarea.SetHeight(inputHeight)
	if m.activeUIPrompt != nil && m.activeUIPrompt.mode == uiPromptInput {
		m.activeUIPrompt.input.Width = m.uiDialogInputWidth()
	}
}

func (m *model) submit() tea.Cmd {
	message := strings.TrimSpace(m.textarea.Value())
	if message == "" {
		return nil
	}
	m.profilePickerOpen = false
	m.dismissSlashCommandSuggestions()
	if strings.TrimSpace(m.conversationID) == "" {
		m.conversationID = convtypes.GenerateID()
	}

	m.textarea.Reset()
	m.entries = append(m.entries, chatEntry{kind: entryUser, content: userDisplayMessage(message)})
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
	uiBroker := newTUIUIBroker(runCh, runID)
	runCtx = extensions.ContextWithUIInputBroker(runCtx, uiBroker)
	req := chat.ChatRequest{
		Message:        message,
		ConversationID: m.conversationID,
		Profile:        profileForRequest(m.profile),
		CWD:            m.requestedCWD,
	}

	return func() tea.Msg {
		go func() {
			defer uiBroker.close()
			conversationID, err := runner.Run(runCtx, req, tuiSink{ch: runCh, runID: runID})
			runCh <- chatDoneMsg{runID: runID, conversationID: strings.TrimSpace(conversationID), err: err}
		}()
		return nil
	}
}

func (m model) slashCommandQuery() (string, bool) {
	draft := strings.TrimLeft(m.textarea.Value(), " \t\r\n")
	if !strings.HasPrefix(draft, "/") {
		return "", false
	}
	withoutSlash := strings.TrimPrefix(draft, "/")
	if strings.ContainsAny(withoutSlash, " \t\r\n") {
		return "", false
	}
	return strings.ToLower(withoutSlash), true
}

func (m model) slashCommandSuggestionsOpen() bool {
	if m.running || m.profilePickerOpen {
		return false
	}
	if m.textarea.Value() == m.slashDismissedDraft {
		return false
	}
	_, ok := m.slashCommandQuery()
	return ok && len(m.filteredSlashCommands()) > 0
}

func (m model) filteredSlashCommands() []slashcommands.Command {
	query, ok := m.slashCommandQuery()
	if !ok {
		return nil
	}
	commands := make([]slashcommands.Command, 0, len(m.slashCommands))
	for _, command := range m.slashCommands {
		name := strings.ToLower(command.Name)
		description := strings.ToLower(command.Description)
		if query == "" || strings.Contains(name, query) || strings.Contains(description, query) {
			commands = append(commands, command)
		}
	}
	return commands
}

func (m *model) resetSlashCommandIndex() {
	if len(m.filteredSlashCommands()) == 0 {
		m.slashCommandIndex = -1
		return
	}
	if m.slashCommandIndex >= len(m.filteredSlashCommands()) {
		m.slashCommandIndex = -1
	}
}

func (m *model) dismissSlashCommandSuggestions() {
	m.slashCommandIndex = -1
	m.slashDismissedDraft = m.textarea.Value()
}

func (m *model) moveSlashCommandSelection(delta int) {
	suggestions := m.filteredSlashCommands()
	if len(suggestions) == 0 {
		m.slashCommandIndex = -1
		return
	}
	next := m.slashCommandIndex + delta
	if delta > 0 {
		if next >= len(suggestions) {
			next = -1
		}
	} else if delta < 0 {
		if m.slashCommandIndex < 0 {
			next = len(suggestions) - 1
		} else if next < 0 {
			next = -1
		}
	}
	m.slashCommandIndex = next
}

func (m *model) selectSlashCommand() {
	suggestions := m.filteredSlashCommands()
	if len(suggestions) == 0 {
		return
	}
	index := m.slashCommandIndex
	if index < 0 || index >= len(suggestions) {
		index = 0
	}
	m.textarea.SetValue(insertSlashCommand(m.textarea.Value(), suggestions[index].Name))
	m.slashCommandIndex = -1
	m.slashDismissedDraft = ""
}

func insertSlashCommand(draft, commandName string) string {
	leadingWhitespaceLength := 0
	for leadingWhitespaceLength < len(draft) {
		switch draft[leadingWhitespaceLength] {
		case ' ', '\t', '\r', '\n':
			leadingWhitespaceLength++
		default:
			return draft[:leadingWhitespaceLength] + "/" + strings.TrimSpace(commandName) + " "
		}
	}
	return draft[:leadingWhitespaceLength] + "/" + strings.TrimSpace(commandName) + " "
}

func userDisplayMessage(message string) string {
	command, args, found := slashcommands.Parse(message)
	if !found {
		return strings.TrimSpace(message)
	}

	update, handled, err := goals.ParseSlashCommand(command, args, time.Now())
	if handled && err == nil {
		return update.Display
	}

	return strings.TrimSpace(message)
}

func mergeSlashCommands(base, additions []slashcommands.Command) []slashcommands.Command {
	merged := make([]slashcommands.Command, 0, len(base)+len(additions))
	seen := map[string]struct{}{}
	for _, command := range base {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, command)
	}
	for _, command := range additions {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, command)
	}
	return merged
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
	if m.activeUIPrompt != nil {
		m.resolveUIPrompt(extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
	}
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
