package tui

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

const (
	conversationPickerLimit             = 200
	conversationPickerMaxWidth          = 112
	conversationPickerStatusWidth       = 4
	conversationPickerTimestampWidth    = 12
	conversationPickerDirectoryMaxWidth = 18
	conversationPickerDirectoryMinWidth = 8
	conversationPickerTitleMinimumWidth = 20
	conversationPickerColumnGap         = "  "
)

type conversationPickerState struct {
	query     string
	summaries []convtypes.ConversationSummary
	selected  int
	loading   bool
	err       error
	requestID int
}

type conversationPickerItem struct {
	key        string
	id         string
	title      string
	cwd        string
	updatedAt  time.Time
	running    bool
	unread     bool
	needsInput bool
	isNew      bool
}

type conversationListMsg struct {
	requestID int
	summaries []convtypes.ConversationSummary
	err       error
}

func loadConversationList(ctx context.Context, requestID int) tea.Cmd {
	return func() tea.Msg {
		service, err := conversations.GetDefaultConversationService(ctx)
		if err != nil {
			return conversationListMsg{requestID: requestID, err: errors.Wrap(err, "failed to open conversation store")}
		}
		defer service.Close()

		response, err := service.ListConversations(ctx, &conversations.ListConversationsRequest{
			Limit:     conversationPickerLimit,
			SortBy:    "updated",
			SortOrder: "desc",
		})
		if err != nil {
			return conversationListMsg{requestID: requestID, err: errors.Wrap(err, "failed to list conversations")}
		}
		return conversationListMsg{requestID: requestID, summaries: response.Conversations}
	}
}

func (m *model) openConversationPicker(query string) tea.Cmd {
	m.profilePickerOpen = false
	m.reasoningPickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.cancelHistorySearch()
	m.shortcutsOpen = false
	m.nextConversationListRequestID++
	requestID := m.nextConversationListRequestID
	m.conversationPicker = &conversationPickerState{
		query:     strings.TrimSpace(query),
		loading:   true,
		requestID: requestID,
	}
	m.clampConversationPickerSelection()
	return loadConversationList(m.ctx, requestID)
}

func (m *model) applyConversationList(msg conversationListMsg) {
	if m.conversationPicker == nil || msg.requestID != m.conversationPicker.requestID {
		return
	}
	m.conversationPicker.loading = false
	m.conversationPicker.err = msg.err
	m.conversationPicker.summaries = append([]convtypes.ConversationSummary(nil), msg.summaries...)
	m.clampConversationPickerSelection()
}

func (m model) mergeConversationPickerItems(summaries []convtypes.ConversationSummary) []conversationPickerItem {
	itemsByKey := make(map[string]conversationPickerItem, len(summaries)+len(m.conversations))
	for _, summary := range summaries {
		id := strings.TrimSpace(summary.ID)
		if id == "" {
			continue
		}
		fallback := strings.TrimSpace(summary.Summary)
		if fallback == "" {
			fallback = strings.TrimSpace(summary.FirstMessage)
		}
		if fallback == "" {
			fallback = "Untitled conversation"
		}
		itemsByKey[id] = conversationPickerItem{
			key:       id,
			id:        id,
			title:     conversations.ResolveConversationName(summary.Metadata, fallback),
			cwd:       strings.TrimSpace(summary.CWD),
			updatedAt: summary.UpdatedAt,
		}
	}

	for key, state := range m.conversations {
		if state == nil {
			continue
		}
		if conversationID := strings.TrimSpace(state.conversationID); conversationID != "" && conversationID != key {
			delete(itemsByKey, conversationID)
		}
		item := itemsByKey[key]
		item.key = key
		item.id = strings.TrimSpace(state.conversationID)
		item.title = strings.TrimSpace(state.title)
		if item.title == "" {
			item.title = conversationStateFallbackTitle(state)
		}
		if strings.TrimSpace(state.cwd) != "" {
			item.cwd = strings.TrimSpace(state.cwd)
		}
		if !state.updatedAt.IsZero() {
			item.updatedAt = state.updatedAt
		}
		item.running = state.running
		item.unread = state.unread
		item.needsInput = state.activeUIPrompt != nil
		itemsByKey[key] = item
	}

	items := make([]conversationPickerItem, 0, len(itemsByKey)+1)
	items = append(items, conversationPickerItem{title: "New conversation", isNew: true})
	for _, item := range itemsByKey {
		items = append(items, item)
	}
	sort.SliceStable(items[1:], func(i, j int) bool {
		left := items[i+1]
		right := items[j+1]
		if left.running != right.running {
			return left.running
		}
		if left.unread != right.unread {
			return left.unread
		}
		if !left.updatedAt.Equal(right.updatedAt) {
			return left.updatedAt.After(right.updatedAt)
		}
		return strings.ToLower(left.title) < strings.ToLower(right.title)
	})
	return items
}

func conversationStateFallbackTitle(state *conversationState) string {
	if state == nil {
		return "Untitled conversation"
	}
	for _, entry := range state.entries {
		if entry.kind == entryUser {
			if title := conversations.NormalizeConversationName(entry.content); title != "" {
				return title
			}
		}
	}
	if strings.TrimSpace(state.draft) != "" {
		return conversations.NormalizeConversationName(state.draft)
	}
	return "Untitled conversation"
}

func (m model) filteredConversationPickerItems() []conversationPickerItem {
	if m.conversationPicker == nil {
		return nil
	}
	items := m.mergeConversationPickerItems(m.conversationPicker.summaries)
	query := strings.ToLower(strings.TrimSpace(m.conversationPicker.query))
	if query == "" {
		return items
	}
	filtered := make([]conversationPickerItem, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{item.title, item.id, item.cwd}, " "))
		if strings.Contains(haystack, query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (m *model) clampConversationPickerSelection() {
	if m.conversationPicker == nil {
		return
	}
	count := len(m.filteredConversationPickerItems())
	if count == 0 {
		m.conversationPicker.selected = 0
		return
	}
	if m.conversationPicker.selected < 0 {
		m.conversationPicker.selected = count - 1
	} else if m.conversationPicker.selected >= count {
		m.conversationPicker.selected = 0
	}
}

func (m *model) updateConversationPickerKey(msg tea.KeyMsg) tea.Cmd {
	if m.conversationPicker == nil {
		return nil
	}
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+d", "ctrl+l":
		m.conversationPicker = nil
		return nil
	case "up", "shift+tab":
		m.conversationPicker.selected--
		m.clampConversationPickerSelection()
		return nil
	case "down", "tab":
		m.conversationPicker.selected++
		m.clampConversationPickerSelection()
		return nil
	case "enter":
		return m.selectConversationPickerItem()
	case "backspace":
		m.conversationPicker.query = trimLastRune(m.conversationPicker.query)
		m.conversationPicker.selected = 0
		return nil
	case "ctrl+u":
		m.conversationPicker.query = ""
		m.conversationPicker.selected = 0
		return nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.conversationPicker.query += string(msg.Runes)
		m.conversationPicker.selected = 0
	}
	return nil
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func (m *model) selectConversationPickerItem() tea.Cmd {
	items := m.filteredConversationPickerItems()
	if m.conversationPicker == nil || len(items) == 0 {
		return nil
	}
	index := m.conversationPicker.selected
	if index < 0 || index >= len(items) {
		index = 0
	}
	item := items[index]
	if item.isNew {
		return m.createNewConversation()
	}
	if activated, cmd := m.activateConversation(item.key); activated {
		m.conversationPicker = nil
		return cmd
	}

	state := newConversationState(item.key, item.id, true, m.conversationDefaults)
	state.initialHistoryPending = true
	state.title = item.title
	state.updatedAt = item.updatedAt
	if item.cwd != "" {
		state.cwd = item.cwd
		state.messageHistoryScopeCWD, _ = messagehistory.ResolveScopeCWD(item.cwd)
	}
	m.conversations[item.key] = state
	_, activateCmd := m.activateConversation(item.key)
	m.conversationPicker = nil
	return tea.Batch(activateCmd, loadConversationHistory(m.ctx, item.key, item.id, state.requestedCWD))
}

func (m model) renderConversationPicker() string {
	if m.conversationPicker == nil {
		return ""
	}
	width := m.conversationPickerDialogWidth()
	if width <= 4 {
		return ""
	}
	contentWidth := max(1, width-4)
	query := m.conversationPicker.query
	if query == "" {
		query = "Type to filter conversations"
	}
	lines := []string{
		renderPersistentStyle(uiDialogTitleStyle, fitVisible("Conversations", contentWidth)),
		renderPersistentStyle(uiDialogBodyStyle, fitVisible("Search: "+query, contentWidth)),
		"",
	}
	items := m.filteredConversationPickerItems()
	if m.conversationPicker.loading {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("Loading saved conversations…", contentWidth)))
	}
	if m.conversationPicker.err != nil {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible(m.conversationPicker.err.Error(), contentWidth)))
	}
	if len(items) == 0 {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("No matching conversations.", contentWidth)))
	} else {
		maxRows := max(1, m.height-9)
		start, end := conversationPickerWindow(len(items), m.conversationPicker.selected, maxRows)
		if start > 0 {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("↑ more", contentWidth)))
		}
		for index := start; index < end; index++ {
			line := m.renderConversationPickerItem(items[index], contentWidth)
			if index == m.conversationPicker.selected {
				line = renderPersistentStyle(uiDialogSelectedStyle, padVisible(line, contentWidth))
			} else {
				line = renderPersistentStyle(uiDialogBodyStyle, line)
			}
			lines = append(lines, line)
		}
		if end < len(items) {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("↓ more", contentWidth)))
		}
	}
	lines = append(lines, "", renderPersistentStyle(uiDialogMutedStyle, fitVisible("Enter open · Esc close · /new creates immediately", contentWidth)))

	top := uiDialogBorderStyle.Render("╭" + strings.Repeat("─", width-2) + "╮")
	bottom := uiDialogBorderStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")
	boxLines := []string{top}
	for _, line := range lines {
		boxLines = append(boxLines, uiDialogBorderStyle.Render("│")+" "+padVisible(line, contentWidth)+" "+uiDialogBorderStyle.Render("│"))
	}
	boxLines = append(boxLines, bottom)
	return strings.Join(boxLines, "\n")
}

func (m model) conversationPickerDialogWidth() int {
	return max(4, min(m.contentWidth(), conversationPickerMaxWidth))
}

func conversationPickerWindow(count, selected, maxRows int) (int, int) {
	if count <= maxRows {
		return 0, count
	}
	start := selected - maxRows/2
	if start < 0 {
		start = 0
	}
	if start+maxRows > count {
		start = count - maxRows
	}
	return start, start + maxRows
}

func (m model) renderConversationPickerItem(item conversationPickerItem, width int) string {
	if item.isNew {
		return fitVisible("+   New conversation", width)
	}
	title := strings.TrimSpace(item.title)
	if title == "" {
		title = "Untitled conversation"
	}
	status := m.conversationPickerItemStatus(item)
	if width <= conversationPickerStatusWidth {
		return fitVisible(status, width)
	}
	titleWidth, timestampWidth, directoryWidth := conversationPickerColumnWidths(width)
	if timestampWidth == 0 {
		return status + padVisible(fitVisiblePrefix(title, titleWidth), titleWidth)
	}

	timestamp := ""
	if !item.updatedAt.IsZero() {
		timestamp = item.updatedAt.Local().Format("Jan 02 15:04")
	}
	line := status + padVisible(fitVisiblePrefix(title, titleWidth), titleWidth) + conversationPickerColumnGap + padVisible(timestamp, timestampWidth)
	if directoryWidth == 0 {
		return line
	}
	directory := ""
	if item.cwd != "" {
		directory = filepath.Base(item.cwd)
	}
	return line + conversationPickerColumnGap + padVisible(fitVisiblePrefix(directory, directoryWidth), directoryWidth)
}

func (m model) conversationPickerItemStatus(item conversationPickerItem) string {
	current := " "
	if item.key == m.activeConversationKey {
		current = "›"
	}
	activity := " "
	if item.needsInput {
		activity = "!"
	} else if item.running {
		activity = m.spinnerGlyph()
		if activity == "" {
			activity = "•"
		}
	} else if item.unread {
		activity = "•"
	}
	return padVisible(fitVisible(current+" "+activity, conversationPickerStatusWidth), conversationPickerStatusWidth)
}

func conversationPickerColumnWidths(width int) (titleWidth, timestampWidth, directoryWidth int) {
	remaining := max(0, width-conversationPickerStatusWidth)
	if remaining < conversationPickerTitleMinimumWidth+lipgloss.Width(conversationPickerColumnGap)+conversationPickerTimestampWidth {
		return remaining, 0, 0
	}

	timestampWidth = conversationPickerTimestampWidth
	remaining -= lipgloss.Width(conversationPickerColumnGap) + timestampWidth
	directoryCandidate := min(conversationPickerDirectoryMaxWidth, max(0, remaining-lipgloss.Width(conversationPickerColumnGap)-conversationPickerTitleMinimumWidth))
	if directoryCandidate >= conversationPickerDirectoryMinWidth {
		directoryWidth = directoryCandidate
		remaining -= lipgloss.Width(conversationPickerColumnGap) + directoryWidth
	}
	return remaining, timestampWidth, directoryWidth
}

func fitVisiblePrefix(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func (m model) overlayConversationPicker(lines []string) []string {
	dialog := m.renderConversationPicker()
	if strings.TrimSpace(dialog) == "" {
		return lines
	}
	dialogLines := strings.Split(dialog, "\n")
	width := m.contentWidth()
	startY := max(0, (m.height-len(dialogLines))/2)
	for index, line := range dialogLines {
		row := startY + index
		if row < 0 || row >= len(lines) {
			continue
		}
		startX := max(0, (width-lipgloss.Width(line))/2)
		lines[row] = padVisible(strings.Repeat(" ", startX)+line, width)
	}
	return lines
}
