package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

const (
	conversationPickerLimit             = 200
	conversationPickerPreferredMinWidth = 112
	conversationPickerWidthPercent      = 80
	conversationPickerStatusWidth       = 4
	conversationPickerAgeWidth          = 8
	conversationPickerWorkspaceMaxWidth = 40
	conversationPickerWorkspaceMinWidth = 8
	conversationPickerTitleMinimumWidth = 20
	conversationPickerColumnGap         = "  "
)

type conversationPickerState struct {
	query       string
	summaries   []convtypes.ConversationSummary
	selected    int
	selectedKey string
	loading     bool
	err         error
	requestID   int
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
	oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
	var oldFocus tuiExtensionSurface
	if oldFocused {
		oldFocus = m.extensionSurfaces[oldFocusKey]
	}
	m.profilePickerOpen = false
	m.reasoningPickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.cancelHistorySearch()
	m.shortcutsOpen = false
	m.nextConversationListRequestID++
	requestID := m.nextConversationListRequestID
	m.conversationPicker = &conversationPickerState{
		query:     strings.TrimSpace(query),
		loading:   !m.remote,
		requestID: requestID,
	}
	m.clampConversationPickerSelection()
	focusTransition := tea.Sequence(m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...)
	if m.remote {
		return focusTransition
	}
	return tea.Batch(loadConversationList(m.ctx, requestID), focusTransition)
}

func (m *model) applyConversationList(msg conversationListMsg) {
	if m.conversationPicker == nil || msg.requestID != m.conversationPicker.requestID {
		return
	}
	m.rememberConversationPickerSelection()
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
	items := m.filteredConversationPickerItems()
	count := len(items)
	if count == 0 {
		m.conversationPicker.selected = 0
		m.conversationPicker.selectedKey = ""
		return
	}
	if selectedKey := m.conversationPicker.selectedKey; selectedKey != "" {
		for index, item := range items {
			if conversationPickerSelectionKey(item) == selectedKey {
				m.conversationPicker.selected = index
				return
			}
		}
	}
	if m.conversationPicker.selected < 0 {
		m.conversationPicker.selected = count - 1
	} else if m.conversationPicker.selected >= count {
		m.conversationPicker.selected = 0
	}
	m.conversationPicker.selectedKey = conversationPickerSelectionKey(items[m.conversationPicker.selected])
}

func (m *model) rememberConversationPickerSelection() {
	if m.conversationPicker == nil {
		return
	}
	items := m.filteredConversationPickerItems()
	if len(items) == 0 {
		return
	}
	index := min(max(0, m.conversationPicker.selected), len(items)-1)
	m.conversationPicker.selectedKey = conversationPickerSelectionKey(items[index])
}

func conversationPickerSelectionKey(item conversationPickerItem) string {
	if item.isNew {
		return "new"
	}
	if item.key != "" {
		return "conversation:" + item.key
	}
	return "conversation:" + item.id
}

func (m model) conversationPickerSelectedIndex(items []conversationPickerItem) int {
	if m.conversationPicker == nil || len(items) == 0 {
		return 0
	}
	if selectedKey := m.conversationPicker.selectedKey; selectedKey != "" {
		for index, item := range items {
			if conversationPickerSelectionKey(item) == selectedKey {
				return index
			}
		}
	}
	return min(max(0, m.conversationPicker.selected), len(items)-1)
}

func (m *model) closeConversationPicker() tea.Cmd {
	if m.conversationPicker == nil {
		return nil
	}
	oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
	var oldFocus tuiExtensionSurface
	if oldFocused {
		oldFocus = m.extensionSurfaces[oldFocusKey]
	}
	m.conversationPicker = nil
	return tea.Sequence(m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...)
}

func (m *model) updateConversationPickerKey(msg tea.KeyMsg) tea.Cmd {
	if m.conversationPicker == nil {
		return nil
	}
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+d", "ctrl+l":
		return m.closeConversationPicker()
	case "up", "shift+tab":
		m.clampConversationPickerSelection()
		m.conversationPicker.selected--
		m.conversationPicker.selectedKey = ""
		m.clampConversationPickerSelection()
		return nil
	case "down", "tab":
		m.clampConversationPickerSelection()
		m.conversationPicker.selected++
		m.conversationPicker.selectedKey = ""
		m.clampConversationPickerSelection()
		return nil
	case "enter":
		return m.selectConversationPickerItem()
	case "backspace":
		m.conversationPicker.query = trimLastRune(m.conversationPicker.query)
		m.conversationPicker.selected = 0
		m.conversationPicker.selectedKey = ""
		m.clampConversationPickerSelection()
		return nil
	case "ctrl+u":
		m.conversationPicker.query = ""
		m.conversationPicker.selected = 0
		m.conversationPicker.selectedKey = ""
		m.clampConversationPickerSelection()
		return nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.conversationPicker.query += string(msg.Runes)
		m.conversationPicker.selected = 0
		m.conversationPicker.selectedKey = ""
		m.clampConversationPickerSelection()
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
	index := m.conversationPickerSelectedIndex(items)
	item := items[index]
	if item.isNew {
		return m.createNewConversation()
	}
	if activated, cmd := m.activateConversation(item.key); activated {
		return tea.Batch(cmd, m.closeConversationPicker())
	}

	state := newConversationState(item.key, item.id, true, m.conversationDefaults)
	state.initialHistoryPending = true
	state.deferSubmitUntilHistory = true
	state.requestedCWD = ""
	state.title = item.title
	state.updatedAt = item.updatedAt
	if item.cwd != "" {
		state.cwd = item.cwd
	}
	m.conversations[item.key] = state
	_, activateCmd := m.activateConversation(item.key)
	return tea.Batch(activateCmd, m.closeConversationPicker(), loadConversationHistory(m.ctx, item.key, item.id, state.requestedCWD))
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
	selected := m.conversationPickerSelectedIndex(items)
	if m.conversationPicker.loading {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("Loading saved conversations…", contentWidth)))
	}
	if m.conversationPicker.err != nil {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible(m.conversationPicker.err.Error(), contentWidth)))
	}
	if len(items) == 0 {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("No matching conversations.", contentWidth)))
	} else {
		fixedRows := len(lines) + 2
		rowBudget := max(0, m.height-2-fixedRows)
		start, end, showMoreAbove, showMoreBelow := conversationPickerVisibleWindow(len(items), selected, rowBudget)
		now := time.Now()
		if showMoreAbove {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("↑ more", contentWidth)))
		}
		for index := start; index < end; index++ {
			line := m.renderConversationPickerItemAt(items[index], contentWidth, now)
			if index == selected {
				line = renderPersistentStyle(uiDialogSelectedStyle, padVisible(line, contentWidth))
			} else {
				line = renderPersistentStyle(uiDialogBodyStyle, line)
			}
			lines = append(lines, line)
		}
		if showMoreBelow {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible("↓ more", contentWidth)))
		}
	}
	lines = append(lines, "", renderPersistentStyle(uiDialogMutedStyle, fitVisible("Enter open · Esc close · /new creates immediately", contentWidth)))
	if maxContentRows := max(0, m.height-2); len(lines) > maxContentRows {
		lines = lines[:maxContentRows]
	}

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
	available := m.contentWidth()
	if available <= 4 {
		return available
	}
	target := available * conversationPickerWidthPercent / 100
	return min(available, max(conversationPickerPreferredMinWidth, target))
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

func conversationPickerVisibleWindow(count, selected, rowBudget int) (start, end int, showMoreAbove, showMoreBelow bool) {
	if count <= 0 || rowBudget <= 0 {
		return 0, 0, false, false
	}
	for rows := min(count, rowBudget); rows > 0; rows-- {
		start, end = conversationPickerWindow(count, selected, rows)
		showMoreAbove = start > 0
		showMoreBelow = end < count
		indicators := 0
		if showMoreAbove {
			indicators++
		}
		if showMoreBelow {
			indicators++
		}
		if rows+indicators <= rowBudget {
			return start, end, showMoreAbove, showMoreBelow
		}
	}
	start, end = conversationPickerWindow(count, selected, 1)
	return start, end, false, false
}

func (m model) renderConversationPickerItemAt(item conversationPickerItem, width int, now time.Time) string {
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
	titleWidth, workspaceWidth, ageWidth := conversationPickerColumnWidths(width)
	if workspaceWidth == 0 {
		return status + padVisible(fitVisiblePrefix(title, titleWidth), titleWidth)
	}

	workspace := conversationPickerDisplayCWD(item.cwd)
	line := status + padVisible(fitVisiblePrefix(title, titleWidth), titleWidth) + conversationPickerColumnGap + padVisible(fitVisiblePrefix(workspace, workspaceWidth), workspaceWidth)
	if ageWidth == 0 {
		return line
	}
	age := fitVisiblePrefix(conversationPickerRelativeAge(item.updatedAt, now), ageWidth)
	return line + conversationPickerColumnGap + strings.Repeat(" ", max(0, ageWidth-lipgloss.Width(age))) + age
}

func conversationPickerDisplayCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	if cwd == "~" || strings.HasPrefix(cwd, "~/") || strings.HasPrefix(cwd, `~\`) {
		return filepath.ToSlash(cwd)
	}
	return filepath.ToSlash(displayCWD(cwd))
}

func conversationPickerRelativeAge(updatedAt, now time.Time) string {
	if updatedAt.IsZero() {
		return ""
	}
	elapsed := now.Sub(updatedAt)
	if elapsed < time.Minute {
		return "now"
	}
	if elapsed < time.Hour {
		return fmt.Sprintf("%dm ago", elapsed/time.Minute)
	}
	if elapsed < 24*time.Hour {
		return fmt.Sprintf("%dh ago", elapsed/time.Hour)
	}
	if elapsed < 7*24*time.Hour {
		return fmt.Sprintf("%dd ago", elapsed/(24*time.Hour))
	}
	if elapsed < 30*24*time.Hour {
		return fmt.Sprintf("%dw ago", elapsed/(7*24*time.Hour))
	}
	if elapsed < 365*24*time.Hour {
		months := min(11, max(1, int(elapsed/(30*24*time.Hour))))
		return fmt.Sprintf("%dmo ago", months)
	}
	years := max(1, int(elapsed/(365*24*time.Hour)))
	return fmt.Sprintf("%dy ago", years)
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

func conversationPickerColumnWidths(width int) (titleWidth, workspaceWidth, ageWidth int) {
	remaining := max(0, width-conversationPickerStatusWidth)
	gapWidth := lipgloss.Width(conversationPickerColumnGap)
	if remaining < conversationPickerTitleMinimumWidth+gapWidth+conversationPickerWorkspaceMinWidth {
		return remaining, 0, 0
	}

	workspaceCandidate := min(
		conversationPickerWorkspaceMaxWidth,
		max(0, remaining-conversationPickerTitleMinimumWidth-(2*gapWidth)-conversationPickerAgeWidth),
	)
	if workspaceCandidate >= conversationPickerWorkspaceMinWidth {
		workspaceWidth = workspaceCandidate
		ageWidth = conversationPickerAgeWidth
		return remaining - workspaceWidth - ageWidth - (2 * gapWidth), workspaceWidth, ageWidth
	}

	workspaceWidth = min(conversationPickerWorkspaceMaxWidth, remaining-conversationPickerTitleMinimumWidth-gapWidth)
	return remaining - workspaceWidth - gapWidth, workspaceWidth, 0
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
