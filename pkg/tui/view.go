package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
)

const (
	tuiLeftMargin                    = 1
	tuiRightMargin                   = 1
	slashCommandBareQueryMaxRows     = 5
	slashCommandFilteredQueryMaxRows = 8
)

var tuiWorkingMessages = []string{
	"Following the thread…",
	"Gathering the next clue…",
	"Composing the next move…",
	"Tracing the shape of the answer…",
	"Pulling the pieces together…",
	"Working through the details…",
}

var tuiFlowFrames = []string{
	"~",
	"≈",
	"≋",
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
	historySearch := m.renderHistorySearch()
	slashSuggestions := m.renderSlashCommandSuggestions()
	profilePicker := m.renderProfilePicker()
	reasoningPicker := m.renderReasoningPicker()
	input := m.renderInputBox()
	parts := []string{transcript}
	if strings.TrimSpace(historySearch) != "" {
		parts = append(parts, historySearch)
	}
	if strings.TrimSpace(slashSuggestions) != "" {
		parts = append(parts, slashSuggestions)
	}
	if strings.TrimSpace(profilePicker) != "" {
		parts = append(parts, profilePicker)
	}
	if strings.TrimSpace(reasoningPicker) != "" {
		parts = append(parts, reasoningPicker)
	}
	parts = append(parts, input)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	content = m.renderUIOverlays(content)
	return leftMarginBlock(content, tuiLeftMargin)
}

func (m model) renderUIOverlays(content string) string {
	lines := strings.Split(content, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padVisible(lines[i], m.contentWidth())
	}

	if len(m.uiNotifications) > 0 {
		lines = m.overlayUINotifications(lines)
	}
	if m.activeUIPrompt != nil {
		lines = m.overlayUIDialog(lines)
	}
	if m.shortcutsOpen {
		lines = m.overlayShortcutsDialog(lines)
	}
	return strings.Join(lines, "\n")
}

func (m model) overlayUIDialog(lines []string) []string {
	dialog := m.renderUIDialog()
	if strings.TrimSpace(dialog) == "" {
		return lines
	}
	dialogLines := strings.Split(dialog, "\n")
	width := m.contentWidth()
	dialogHeight := len(dialogLines)
	startY := max(0, (m.height-dialogHeight)/2)
	for i, line := range dialogLines {
		row := startY + i
		if row < 0 || row >= len(lines) {
			continue
		}
		startX := max(0, (width-lipgloss.Width(line))/2)
		lines[row] = padVisible(strings.Repeat(" ", startX)+line, width)
	}
	return lines
}

func (m model) overlayShortcutsDialog(lines []string) []string {
	dialog := m.renderShortcutsDialog()
	if strings.TrimSpace(dialog) == "" {
		return lines
	}
	dialogLines := strings.Split(dialog, "\n")
	width := m.contentWidth()
	dialogHeight := len(dialogLines)
	startY := max(0, (m.height-dialogHeight)/2)
	for i, line := range dialogLines {
		row := startY + i
		if row < 0 || row >= len(lines) {
			continue
		}
		startX := max(0, (width-lipgloss.Width(line))/2)
		lines[row] = padVisible(strings.Repeat(" ", startX)+line, width)
	}
	return lines
}

func (m model) renderShortcutsDialog() string {
	width := m.uiDialogWidth()
	if width <= 4 {
		return ""
	}
	contentWidth := max(1, width-4)
	shortcutWidth := 14
	if contentWidth < 32 {
		shortcutWidth = 10
	}
	shortcutWidth = min(shortcutWidth, max(1, contentWidth/2))
	descriptionWidth := max(1, contentWidth-shortcutWidth-2)

	rows := []struct {
		shortcut    string
		description string
	}{
		{shortcut: "Enter", description: "Send message"},
		{shortcut: "Shift+Enter", description: "Insert newline"},
		{shortcut: "Ctrl+G", description: "Edit draft in $EDITOR"},
		{shortcut: "Ctrl+R", description: "Search previous sent messages"},
		{shortcut: "Ctrl+T", description: "Change profile before starting"},
		{shortcut: "Ctrl+Y", description: "Change reasoning effort before starting"},
		{shortcut: "Ctrl+O", description: "Toggle thought/tool details"},
		{shortcut: "PgUp/PgDown", description: "Scroll transcript"},
		{shortcut: "Esc", description: "Cancel or dismiss"},
		{shortcut: "Ctrl+C", description: "Cancel run or quit"},
	}

	lines := []string{
		renderPersistentStyle(uiDialogTitleStyle, fitVisible("Shortcuts", contentWidth)),
		"",
	}
	for _, row := range rows {
		shortcut := renderPersistentStyle(uiDialogButtonStyle, padVisible(fitVisible(row.shortcut, shortcutWidth), shortcutWidth))
		description := renderPersistentStyle(uiDialogBodyStyle, fitVisible(row.description, descriptionWidth))
		lines = append(lines, shortcut+"  "+description)
	}
	lines = append(lines, "", renderPersistentStyle(uiDialogMutedStyle, fitVisible("Press Esc, Enter, ?, or q to close.", contentWidth)))

	top := uiDialogBorderStyle.Render("╭" + strings.Repeat("─", width-2) + "╮")
	bottom := uiDialogBorderStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")
	boxLines := []string{top}
	for _, line := range lines {
		boxLines = append(boxLines, uiDialogBorderStyle.Render("│")+" "+padVisible(line, contentWidth)+" "+uiDialogBorderStyle.Render("│"))
	}
	boxLines = append(boxLines, bottom)
	return strings.Join(boxLines, "\n")
}

func (m model) overlayUINotifications(lines []string) []string {
	notifications := m.renderUINotifications()
	if strings.TrimSpace(notifications) == "" {
		return lines
	}
	notificationLines := strings.Split(notifications, "\n")
	width := m.contentWidth()
	for i, line := range notificationLines {
		if i >= len(lines) {
			break
		}
		startX := max(0, width-lipgloss.Width(line))
		lines[i] = padVisible(strings.Repeat(" ", startX)+line, width)
	}
	return lines
}

func (m model) renderSlashCommandSuggestions() string {
	if !m.slashCommandSuggestionsOpen() {
		return ""
	}

	outerWidth := m.slashCommandSuggestionsWidth()
	if outerWidth <= 0 {
		return ""
	}
	contentWidth := outerWidth
	if contentWidth <= 0 {
		return ""
	}

	suggestions := m.filteredSlashCommands()
	if len(suggestions) == 0 {
		return ""
	}
	visibleSuggestions := visibleSlashCommandSuggestions(suggestions, m.slashCommandIndex, m.maxSlashCommandSuggestions())
	lines := make([]string, 0, len(visibleSuggestions)+1)
	for _, suggestion := range visibleSuggestions {
		line := renderSlashCommandSuggestionLine(suggestion.command, contentWidth)
		if suggestion.index == m.slashCommandIndex {
			line = renderPersistentStyle(slashCommandSelectedStyle, padVisible(line, contentWidth))
		} else {
			line = padVisible(line, contentWidth)
		}
		lines = append(lines, line)
	}

	if m.slashCommandErr != nil {
		errorText := fitVisible("slash commands unavailable: "+m.slashCommandErr.Error(), max(1, contentWidth-2))
		line := " " + renderPersistentStyle(slashCommandErrorStyle, errorText) + " "
		lines = append(lines, padVisible(line, contentWidth))
	}

	return strings.Join(lines, "\n")
}

func (m model) renderHistorySearch() string {
	if m.historySearch == nil {
		return ""
	}
	width := m.historySearchWidth()
	if width <= 0 {
		return ""
	}

	query := m.historySearch.query
	labelText := "reverse-i-search: "
	label := renderPersistentStyle(historySearchLabelStyle, labelText)
	available := max(1, width-lipgloss.Width(labelText))
	queryDisplay := query
	if strings.TrimSpace(query) != "" && len(m.historySearch.matches) == 0 {
		queryDisplay = query + "  " + renderPersistentStyle(historySearchErrorStyle, "no matches")
	}
	queryText := renderPersistentStyle(historySearchQueryStyle, fitVisible(queryDisplay, available))
	return padVisible(label+queryText, width)
}

func (m model) renderUIDialog() string {
	if m.activeUIPrompt == nil {
		return ""
	}
	prompt := *m.activeUIPrompt
	width := m.uiDialogWidth()
	if width <= 4 {
		return ""
	}
	contentWidth := max(1, width-4)

	lines := []string{
		renderPersistentStyle(uiDialogTitleStyle, fitVisible(uiPromptTitle(prompt.mode, prompt.title), contentWidth)),
	}
	for _, text := range []string{prompt.message, prompt.helpText} {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		for _, line := range strings.Split(wrapText(text, contentWidth), "\n") {
			lines = append(lines, renderPersistentStyle(uiDialogBodyStyle, fitVisible(line, contentWidth)))
		}
	}

	switch prompt.mode {
	case uiPromptInput:
		if len(lines) > 1 {
			lines = append(lines, "")
		}
		lines = append(lines, m.renderUIInputLine(prompt, contentWidth))
		if prompt.required && strings.TrimSpace(prompt.input.Value()) == "" {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, "Required"))
		}
	case uiPromptConfirm:
		if len(lines) > 1 {
			lines = append(lines, "")
		}
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, "Press Enter/Y to confirm or Esc/N to cancel."))
	case uiPromptSelect:
		if len(lines) > 1 {
			lines = append(lines, "")
		}
		if len(prompt.options) == 0 {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, "No options available."))
		} else {
			lines = append(lines, m.renderUISelectLines(prompt, contentWidth)...)
		}
	}

	lines = append(lines, "", m.renderUIDialogActions(prompt, contentWidth))

	top := uiDialogBorderStyle.Render("╭" + strings.Repeat("─", width-2) + "╮")
	bottom := uiDialogBorderStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")
	boxLines := []string{top}
	for _, line := range lines {
		boxLines = append(boxLines, uiDialogBorderStyle.Render("│")+" "+padVisible(line, contentWidth)+" "+uiDialogBorderStyle.Render("│"))
	}
	boxLines = append(boxLines, bottom)
	return strings.Join(boxLines, "\n")
}

func (m model) renderUIInputLine(prompt uiPromptState, width int) string {
	input := prompt.input.View()
	if lipgloss.Width(input) > width {
		input = fitVisible(input, width)
	}
	return renderPersistentStyle(uiDialogBodyStyle, padVisible(input, width))
}

func (m model) renderUISelectLines(prompt uiPromptState, width int) []string {
	maxRows := m.maxUISelectRows()
	start := visibleUISelectStart(len(prompt.options), prompt.selectIndex, maxRows)
	end := min(len(prompt.options), start+maxRows)
	lines := make([]string, 0, end-start+2)
	if start > 0 {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, "↑ more"))
	}
	for index := start; index < end; index++ {
		prefix := "  "
		if index == prompt.selectIndex {
			prefix = "› "
		}
		line := fitVisible(prefix+prompt.options[index], width)
		if index == prompt.selectIndex {
			line = renderPersistentStyle(uiDialogSelectedStyle, padVisible(line, width))
		} else {
			line = renderPersistentStyle(uiDialogBodyStyle, line)
		}
		lines = append(lines, line)
	}
	if end < len(prompt.options) {
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, "↓ more"))
	}
	return lines
}

func visibleUISelectStart(count, selected, limit int) int {
	if count <= limit || limit <= 0 {
		return 0
	}
	if selected < 0 {
		selected = 0
	}
	start := selected - limit + 1
	if start < 0 {
		return 0
	}
	if start+limit > count {
		return count - limit
	}
	return start
}

func (m model) renderUIDialogActions(prompt uiPromptState, width int) string {
	cancel := "[Esc] " + uiPromptCancelLabel(prompt)
	submit := "[Enter] " + uiPromptSubmitLabel(prompt)
	if prompt.mode == uiPromptConfirm {
		submit = "[Y] " + uiPromptSubmitLabel(prompt)
		cancel = "[N] " + uiPromptCancelLabel(prompt)
	}
	line := renderPersistentStyle(uiDialogCancelStyle, cancel) + "  " + renderPersistentStyle(uiDialogButtonStyle, submit)
	if lipgloss.Width(line) > width {
		return fitVisible(cancel+"  "+submit, width)
	}
	return line
}

func (m model) renderUINotifications() string {
	if len(m.uiNotifications) == 0 {
		return ""
	}
	boxes := make([]string, 0, len(m.uiNotifications))
	for _, notification := range m.uiNotifications {
		box := m.renderUINotification(notification)
		if strings.TrimSpace(box) != "" {
			boxes = append(boxes, box)
		}
	}
	return strings.Join(boxes, "\n")
}

func (m model) renderUINotification(notification uiNotification) string {
	width := m.uiNotificationWidth()
	if width <= 4 {
		return ""
	}
	contentWidth := max(1, width-4)
	lines := []string{}
	borderStyle, titleStyle := uiNotificationStyles(notification.level)
	if title := strings.TrimSpace(notification.title); title != "" {
		lines = append(lines, renderPersistentStyle(titleStyle, fitVisible(title, contentWidth)))
	}
	if message := strings.TrimSpace(notification.message); message != "" {
		for _, line := range strings.Split(wrapText(message, contentWidth), "\n") {
			lines = append(lines, renderPersistentStyle(uiNotificationBodyStyle, fitVisible(line, contentWidth)))
		}
	}
	if len(lines) == 0 {
		return ""
	}

	top := borderStyle.Render("╭" + strings.Repeat("─", width-2) + "╮")
	bottom := borderStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")
	boxLines := []string{top}
	for _, line := range lines {
		boxLines = append(boxLines, borderStyle.Render("│")+" "+padVisible(line, contentWidth)+" "+borderStyle.Render("│"))
	}
	boxLines = append(boxLines, bottom)
	return strings.Join(boxLines, "\n")
}

func uiNotificationStyles(level uiNotificationLevel) (lipgloss.Style, lipgloss.Style) {
	switch level {
	case uiNotificationWarning:
		return uiNotificationWarningBorderStyle, uiNotificationWarningTitleStyle
	case uiNotificationError:
		return uiNotificationErrorBorderStyle, uiNotificationErrorTitleStyle
	default:
		return uiNotificationBorderStyle, uiNotificationTitleStyle
	}
}

func (m model) uiDialogWidth() int {
	return max(4, min(m.contentWidth(), 72))
}

func (m model) uiDialogInputWidth() int {
	return max(1, m.uiDialogWidth()-4)
}

func (m model) maxUISelectRows() int {
	available := max(1, m.height-10)
	return min(8, available)
}

func (m model) uiNotificationWidth() int {
	return max(4, min(m.contentWidth(), 42))
}

type visibleSlashCommandSuggestion struct {
	command slashcommands.Command
	index   int
}

func visibleSlashCommandSuggestions(commands []slashcommands.Command, selectedIndex, limit int) []visibleSlashCommandSuggestion {
	if limit <= 0 || len(commands) == 0 {
		return nil
	}
	if len(commands) <= limit {
		visible := make([]visibleSlashCommandSuggestion, 0, len(commands))
		for i, command := range commands {
			visible = append(visible, visibleSlashCommandSuggestion{command: command, index: i})
		}
		return visible
	}

	start := 0
	if selectedIndex >= 0 {
		start = selectedIndex - limit + 1
		if start < 0 {
			start = 0
		}
		if start+limit > len(commands) {
			start = len(commands) - limit
		}
	}

	visible := make([]visibleSlashCommandSuggestion, 0, limit)
	for i := start; i < min(len(commands), start+limit); i++ {
		visible = append(visible, visibleSlashCommandSuggestion{command: commands[i], index: i})
	}
	return visible
}

func renderSlashCommandSuggestionLine(command slashcommands.Command, width int) string {
	if width <= 0 {
		return ""
	}

	name := "/" + strings.TrimSpace(command.Name)
	description := strings.TrimSpace(command.Description)

	leftWidth := min(max(14, width/4), max(1, width/2))
	if leftWidth >= width {
		return renderPersistentStyle(slashCommandNameStyle, fitVisible(name, width))
	}
	rightWidth := width - leftWidth - 1
	if rightWidth <= 0 {
		return renderPersistentStyle(slashCommandNameStyle, fitVisible(name, width))
	}

	left := renderPersistentStyle(slashCommandNameStyle, padVisible(fitVisible(name, leftWidth), leftWidth))
	rightText := description
	if rightText == "" {
		return left
	}
	rightText = fitVisible(rightText, rightWidth)
	right := renderPersistentStyle(slashCommandDescriptionStyle, rightText)

	return left + " " + right
}

func (m model) slashCommandSuggestionsWidth() int {
	return m.inputOuterWidth()
}

func (m model) historySearchWidth() int {
	return m.inputOuterWidth()
}

func (m model) historySearchHeight() int {
	if m.historySearch == nil {
		return 0
	}
	return 1
}

func (m model) maxSlashCommandSuggestions() int {
	availableHeight := m.height - inputHeight - 2 - m.profilePickerHeight() - m.reasoningPickerHeight() - m.historySearchHeight() - 1
	if availableHeight < 1 {
		return 1
	}
	rowLimit := slashCommandFilteredQueryMaxRows
	if query, ok := m.slashCommandQuery(); ok && query == "" {
		rowLimit = slashCommandBareQueryMaxRows
	}
	return min(rowLimit, availableHeight)
}

func (m model) slashCommandSuggestionsHeight() int {
	if !m.slashCommandSuggestionsOpen() {
		return 0
	}
	suggestionCount := min(len(m.filteredSlashCommands()), m.maxSlashCommandSuggestions())
	if suggestionCount == 0 {
		return 0
	}
	height := suggestionCount
	if m.slashCommandErr != nil {
		height++
	}
	return height
}

func (m model) renderInputBox() string {
	outerWidth := m.inputOuterWidth()
	contentWidth := m.inputContentWidth()
	bodyLines := strings.Split(m.textarea.View(), "\n")
	for len(bodyLines) < inputHeight {
		bodyLines = append(bodyLines, "")
	}
	m.applySlashCommandUsageHint(bodyLines, contentWidth)

	lines := []string{m.renderInputTopBorder()}
	for i := 0; i < inputHeight; i++ {
		lines = append(lines, inputBorderStyle.Render("│")+" "+padVisible(bodyLines[i], contentWidth)+" "+inputBorderStyle.Render("│"))
	}
	lines = append(lines, renderLabeledBorderPair("╰", "╯", outerWidth, m.inputBottomLeftLabel(), displayCWD(m.cwd)))
	return strings.Join(lines, "\n")
}

func (m model) applySlashCommandUsageHint(bodyLines []string, contentWidth int) {
	if len(bodyLines) == 0 || contentWidth <= 0 {
		return
	}
	suffix, prefixWidth := m.activeSlashCommandPlaceholderSuffix()
	if suffix == "" {
		return
	}
	if prefixWidth >= contentWidth {
		return
	}
	bodyLines[0] = xansi.Cut(bodyLines[0], 0, prefixWidth) + renderPersistentStyle(inputPlaceholderStyle, fitVisible(suffix, contentWidth-prefixWidth))
}

func (m model) activeSlashCommandPlaceholderSuffix() (string, int) {
	value := strings.TrimLeft(m.textarea.Value(), " \t\r\n")
	if !strings.Contains(value, " ") {
		return "", 0
	}
	typed := strings.TrimRight(value, " \t\r\n")
	if typed == "" {
		return "", 0
	}
	typedForPrefix := value
	if strings.HasSuffix(value, " ") || strings.HasSuffix(value, "\t") {
		typedForPrefix = typed + " "
	}

	command, ok := m.activeSlashCommand()
	if !ok {
		return "", 0
	}
	placeholder := ""
	if placeholderValue := strings.TrimSpace(command.Placeholder); placeholderValue != "" {
		placeholder = placeholderValue
	}
	if placeholder == "" {
		placeholder = "/" + strings.TrimSpace(command.Name)
		if hint := strings.TrimSpace(command.Hint); hint != "" {
			placeholder += " " + hint
		}
	}

	if !strings.HasPrefix(placeholder, typedForPrefix) {
		return "", 0
	}
	return strings.TrimPrefix(placeholder, typedForPrefix), lipgloss.Width(typedForPrefix)
}

func (m model) activeSlashCommand() (slashcommands.Command, bool) {
	commandName, _, found := slashcommands.Parse(m.textarea.Value())
	if !found {
		return slashcommands.Command{}, false
	}
	for _, command := range m.slashCommands {
		if command.Name == commandName {
			return command, true
		}
	}
	return slashcommands.Command{}, false
}

type styledLabelPart struct {
	text  string
	style lipgloss.Style
}

func (m model) renderInputTopBorder() string {
	outerWidth := m.inputOuterWidth()
	if outerWidth <= 2 {
		return inputBorderStyle.Render("╭╮")
	}

	plainLabel := m.inputTopRightLabel()
	fillWidth := outerWidth - 2
	if strings.TrimSpace(plainLabel) == "" || fillWidth <= 2 {
		return inputBorderStyle.Render("╭" + strings.Repeat("─", fillWidth) + "╮")
	}

	visibleLabel := fitVisible(plainLabel, fillWidth-2)
	labelWidth := lipgloss.Width(visibleLabel) + 2
	start := fillWidth - labelWidth - 1
	if start < 0 {
		start = 0
	}
	endWidth := fillWidth - start - labelWidth
	if endWidth < 0 {
		endWidth = 0
	}

	return inputBorderStyle.Render("╭"+strings.Repeat("─", start)) +
		" " + m.renderInputTopLabel(visibleLabel) + " " +
		inputBorderStyle.Render(strings.Repeat("─", endWidth)+"╮")
}

func (m model) renderInputTopLabel(visibleLabel string) string {
	fullLabel := m.inputTopRightLabel()
	if visibleLabel != fullLabel {
		return inputLabelStyle.Render(visibleLabel)
	}

	parts := []styledLabelPart{
		{text: formatUsage(m.usage), style: inputLabelStyle},
		{text: " - ", style: inputLabelStyle},
		{text: m.profile, style: m.profileStyle(m.profileIndex)},
	}
	if strings.HasSuffix(fullLabel, " - "+m.reasoningEffortLabel()) {
		parts = append(parts,
			styledLabelPart{text: " - ", style: inputLabelStyle},
			styledLabelPart{text: m.reasoningEffortLabel(), style: m.reasoningEffortStyle(m.reasoningEffortIndex)},
		)
	}

	var b strings.Builder
	for _, part := range parts {
		if part.text == "" {
			continue
		}
		b.WriteString(renderPersistentStyle(part.style, part.text))
	}
	return b.String()
}

func (m model) renderProfilePicker() string {
	if !m.profilePickerOpen || len(m.profileOptions) == 0 {
		return ""
	}

	_, profileEnd, ok := m.profileLabelBoundsInBlock()
	if !ok {
		profileEnd = m.inputOuterWidth() - 1
	}
	optionWidth := m.profilePickerWidth()
	start := profileEnd - optionWidth
	if start < 0 {
		start = 0
	}
	if start+optionWidth > m.inputOuterWidth() {
		start = max(0, m.inputOuterWidth()-optionWidth)
	}

	lines := make([]string, 0, len(m.profileOptions))
	for index, profile := range m.profileOptions {
		label := fitVisible(profile, optionWidth)
		style := m.profileStyle(index)
		if index == m.profilePickerIndex {
			style = style.Background(themeColor(m.theme.ProfileSelected))
		}
		line := strings.Repeat(" ", start) + renderPersistentStyle(style, padVisible(label, optionWidth))
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m model) profilePickerWidth() int {
	width := lipgloss.Width(m.profile)
	for _, profile := range m.profileOptions {
		width = max(width, lipgloss.Width(profile))
	}
	return max(1, min(width, m.inputOuterWidth()))
}

func (m model) profilePickerHeight() int {
	if !m.profilePickerOpen || len(m.profileOptions) == 0 {
		return 0
	}
	return len(m.profileOptions)
}

func (m model) renderReasoningPicker() string {
	if !m.reasoningPickerOpen || len(m.reasoningEffortOptions) == 0 {
		return ""
	}

	_, effortEnd, ok := m.reasoningEffortLabelBoundsInBlock()
	if !ok {
		effortEnd = m.inputOuterWidth() - 1
	}
	optionWidth := m.reasoningPickerWidth()
	start := effortEnd - optionWidth
	if start < 0 {
		start = 0
	}
	if start+optionWidth > m.inputOuterWidth() {
		start = max(0, m.inputOuterWidth()-optionWidth)
	}

	lines := make([]string, 0, len(m.reasoningEffortOptions))
	for index, effort := range m.reasoningEffortOptions {
		label := fitVisible(effort, optionWidth)
		style := m.reasoningEffortStyle(index)
		if index == m.reasoningPickerIndex {
			style = style.Background(themeColor(m.theme.ProfileSelected))
		}
		line := strings.Repeat(" ", start) + renderPersistentStyle(style, padVisible(label, optionWidth))
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m model) reasoningPickerWidth() int {
	width := lipgloss.Width(m.reasoningEffortLabel())
	for _, effort := range m.reasoningEffortOptions {
		width = max(width, lipgloss.Width(effort))
	}
	return max(1, min(width, m.inputOuterWidth()))
}

func (m model) reasoningPickerHeight() int {
	if !m.reasoningPickerOpen || len(m.reasoningEffortOptions) == 0 {
		return 0
	}
	return len(m.reasoningEffortOptions)
}

func (m model) reasoningPickerBoundsInBlock() (startX, endX int, ok bool) {
	if !m.reasoningPickerOpen || len(m.reasoningEffortOptions) == 0 {
		return 0, 0, false
	}
	_, effortEnd, effortOK := m.reasoningEffortLabelBoundsInBlock()
	if !effortOK {
		effortEnd = m.inputOuterWidth() - 1
	}
	width := m.reasoningPickerWidth()
	start := effortEnd - width
	if start < 0 {
		start = 0
	}
	if start+width > m.inputOuterWidth() {
		start = max(0, m.inputOuterWidth()-width)
	}
	return start, start + width, width > 0
}

func (m model) reasoningPickerOptionAt(screenX, screenY int) (int, bool) {
	if !m.reasoningPickerOpen {
		return 0, false
	}
	blockX := screenX - tuiLeftMargin
	startX, endX, ok := m.reasoningPickerBoundsInBlock()
	if !ok || blockX < startX || blockX >= endX {
		return 0, false
	}
	optionIndex := screenY - m.viewport.Height
	if optionIndex < 0 || optionIndex >= len(m.reasoningEffortOptions) {
		return 0, false
	}
	return optionIndex, true
}

func (m model) reasoningComposerRegionContains(screenX, screenY int) bool {
	if !m.canChangeReasoningEffort() {
		return false
	}
	blockX := screenX - tuiLeftMargin
	inputTopY := m.viewport.Height + m.profilePickerHeight() + m.reasoningPickerHeight()
	if screenY != inputTopY {
		return false
	}
	startX, endX, ok := m.reasoningEffortLabelBoundsInBlock()
	return ok && blockX >= startX && blockX < endX
}

func (m model) profilePickerBoundsInBlock() (startX, endX int, ok bool) {
	if !m.profilePickerOpen || len(m.profileOptions) == 0 {
		return 0, 0, false
	}
	_, profileEnd, profileOK := m.profileLabelBoundsInBlock()
	if !profileOK {
		profileEnd = m.inputOuterWidth() - 1
	}
	width := m.profilePickerWidth()
	start := profileEnd - width
	if start < 0 {
		start = 0
	}
	if start+width > m.inputOuterWidth() {
		start = max(0, m.inputOuterWidth()-width)
	}
	return start, start + width, width > 0
}

func (m model) profilePickerOptionAt(screenX, screenY int) (int, bool) {
	if !m.profilePickerOpen {
		return 0, false
	}
	blockX := screenX - tuiLeftMargin
	startX, endX, ok := m.profilePickerBoundsInBlock()
	if !ok || blockX < startX || blockX >= endX {
		return 0, false
	}
	optionIndex := screenY - m.viewport.Height
	if optionIndex < 0 || optionIndex >= len(m.profileOptions) {
		return 0, false
	}
	return optionIndex, true
}

func (m model) profileComposerRegionContains(screenX, screenY int) bool {
	if !m.canChangeProfile() {
		return false
	}
	blockX := screenX - tuiLeftMargin
	inputTopY := m.viewport.Height + m.profilePickerHeight() + m.reasoningPickerHeight()
	if screenY != inputTopY {
		return false
	}
	startX, endX, ok := m.profileLabelBoundsInBlock()
	return ok && blockX >= startX && blockX < endX
}

func leftMarginBlock(text string, width int) string {
	if text == "" || width <= 0 {
		return text
	}
	prefix := strings.Repeat(" ", width)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func renderLabeledBorderPair(left, right string, width int, leftLabel string, rightLabel string) string {
	if strings.TrimSpace(leftLabel) == "" {
		return renderStyledLabeledBorder(left, right, width, rightLabel, composerLabelStyle)
	}

	if width <= 2 {
		return inputBorderStyle.Render(left + right)
	}

	fillWidth := width - 2
	rightLabel = strings.TrimSpace(rightLabel)
	if fillWidth <= 2 {
		return inputBorderStyle.Render(left + strings.Repeat("─", fillWidth) + right)
	}

	leftText := formatBorderLabel(leftLabel, fillWidth-1)
	leftWidth := lipgloss.Width(leftText)
	leftStart := 1
	if leftStart+leftWidth > fillWidth {
		leftStart = 0
	}

	rightText := ""
	rightWidth := 0
	rightStart := fillWidth
	if rightLabel != "" {
		maxRightWidth := fillWidth - leftStart - leftWidth - 2
		if maxRightWidth > 0 {
			rightText = formatBorderLabel(rightLabel, maxRightWidth)
			rightWidth = lipgloss.Width(rightText)
			rightStart = fillWidth - rightWidth - 1
			if rightStart < leftStart+leftWidth+1 || rightStart+rightWidth > fillWidth {
				rightText = ""
				rightWidth = 0
				rightStart = fillWidth
			}
		}
	}

	var b strings.Builder
	b.WriteString(inputBorderStyle.Render(left))
	position := 0
	writeBorderFill := func(count int) {
		if count > 0 {
			b.WriteString(inputBorderStyle.Render(strings.Repeat("─", count)))
			position += count
		}
	}

	writeBorderFill(leftStart - position)
	b.WriteString(renderComposerBottomLeftLabel(leftText))
	position = leftStart + leftWidth
	if rightText != "" {
		writeBorderFill(rightStart - position)
		b.WriteString(renderPersistentStyle(composerLabelStyle, rightText))
		position = rightStart + rightWidth
	}
	writeBorderFill(fillWidth - position)
	b.WriteString(inputBorderStyle.Render(right))
	return b.String()
}

func formatBorderLabel(label string, width int) string {
	label = strings.TrimSpace(label)
	if label == "" || width <= 0 {
		return ""
	}
	if width <= 2 {
		return fitVisible(label, width)
	}
	return " " + fitVisible(label, width-2) + " "
}

func renderStyledLabeledBorder(left, right string, width int, label string, labelStyle lipgloss.Style) string {
	if width <= 2 {
		return inputBorderStyle.Render(left + right)
	}

	fillWidth := width - 2
	label = strings.TrimSpace(label)
	if label == "" || fillWidth <= 2 {
		return inputBorderStyle.Render(left + strings.Repeat("─", fillWidth) + right)
	}

	label = " " + fitVisible(label, fillWidth-2) + " "
	labelWidth := lipgloss.Width(label)
	start := fillWidth - labelWidth - 1
	if start < 0 {
		start = 0
	}
	endWidth := fillWidth - start - labelWidth
	if endWidth < 0 {
		endWidth = 0
	}

	return inputBorderStyle.Render(left+strings.Repeat("─", start)) +
		renderPersistentStyle(labelStyle, label) +
		inputBorderStyle.Render(strings.Repeat("─", endWidth)+right)
}

func renderComposerBottomLeftLabel(label string) string {
	flowStart := 0
	for flowStart < len(label) && label[flowStart] == ' ' {
		flowStart++
	}
	if flowStart >= len(label) {
		return renderPersistentStyle(composerLabelStyle, label)
	}

	flowEnd := len(label)
	if nextSpace := strings.IndexByte(label[flowStart:], ' '); nextSpace >= 0 {
		flowEnd = flowStart + nextSpace
	}

	var b strings.Builder
	if flowStart > 0 {
		b.WriteString(renderPersistentStyle(composerLabelStyle, label[:flowStart]))
	}
	b.WriteString(composerFlowStyle.Render(label[flowStart:flowEnd]))
	if flowEnd < len(label) {
		b.WriteString(renderPersistentStyle(composerLabelStyle, label[flowEnd:]))
	}
	return b.String()
}

func (m model) inputTopRightLabel() string {
	base := strings.Join([]string{formatUsage(m.usage), m.profile}, " - ")
	full := base + " - " + m.reasoningEffortLabel()
	if lipgloss.Width(full) <= max(1, m.inputOuterWidth()-6) {
		return full
	}
	return base
}

func (m model) profileLabelBoundsInBlock() (startX, endX int, ok bool) {
	outerWidth := m.inputOuterWidth()
	if outerWidth <= 2 || strings.TrimSpace(m.profile) == "" {
		return 0, 0, false
	}

	fillWidth := outerWidth - 2
	if fillWidth <= 2 {
		return 0, 0, false
	}

	plainLabel := m.inputTopRightLabel()
	visibleLabel := fitVisible(plainLabel, fillWidth-2)
	prefix := formatUsage(m.usage) + " - "
	if visibleLabel != plainLabel {
		return 0, 0, false
	}

	labelWidth := lipgloss.Width(visibleLabel) + 2
	labelStart := fillWidth - labelWidth - 1
	if labelStart < 0 {
		labelStart = 0
	}
	profileOffset := lipgloss.Width(prefix)
	startX = 1 + labelStart + 1 + profileOffset
	endX = startX + lipgloss.Width(m.profile)
	return startX, endX, startX < endX
}

func (m model) reasoningEffortLabel() string {
	return "effort:" + normalizeReasoningEffort(m.reasoningEffort)
}

func (m model) reasoningEffortLabelBoundsInBlock() (startX, endX int, ok bool) {
	outerWidth := m.inputOuterWidth()
	if outerWidth <= 2 {
		return 0, 0, false
	}
	fillWidth := outerWidth - 2
	if fillWidth <= 2 {
		return 0, 0, false
	}

	plainLabel := m.inputTopRightLabel()
	if !strings.HasSuffix(plainLabel, " - "+m.reasoningEffortLabel()) {
		return 0, 0, false
	}
	visibleLabel := fitVisible(plainLabel, fillWidth-2)
	if visibleLabel != plainLabel {
		return 0, 0, false
	}
	labelWidth := lipgloss.Width(visibleLabel) + 2
	labelStart := fillWidth - labelWidth - 1
	if labelStart < 0 {
		labelStart = 0
	}
	prefix := formatUsage(m.usage) + " - " + m.profile + " - "
	startX = 1 + labelStart + 1 + lipgloss.Width(prefix)
	endX = startX + lipgloss.Width(m.reasoningEffortLabel())
	return startX, endX, startX < endX
}

func (m model) profileStyle(index int) lipgloss.Style {
	colors := m.theme.ProfileColors
	if len(colors) == 0 {
		return composerFlowStyle
	}
	if index < 0 {
		index = 0
	}
	return lipgloss.NewStyle().Foreground(themeColor(colors[index%len(colors)]))
}

func (m model) reasoningEffortStyle(index int) lipgloss.Style {
	return m.profileStyle(index + 1)
}

func (m model) inputBottomLeftLabel() string {
	if !m.running {
		return ""
	}
	return m.flowingWaterFrame() + " " + m.workingStatusText()
}

func (m model) flowingWaterFrame() string {
	if len(tuiFlowFrames) == 0 {
		return "~"
	}
	const framesPerFlowStep = 8
	frame := m.workingFrame
	if frame < 0 {
		frame = 0
	}
	return tuiFlowFrames[(frame/framesPerFlowStep)%len(tuiFlowFrames)]
}

func (m model) workingStatusText() string {
	if len(tuiWorkingMessages) == 0 {
		return "Working…"
	}
	const framesPerWorkingMessage = 36
	frame := m.workingFrame
	if frame < 0 {
		frame = 0
	}
	messageIndex := (frame / framesPerWorkingMessage) % len(tuiWorkingMessages)
	return tuiWorkingMessages[messageIndex]
}

func (m model) inputContentWidth() int {
	outerWidth := max(1, m.inputOuterWidth())
	paddingWidth := 2
	borderWidth := 2
	return max(1, outerWidth-paddingWidth-borderWidth)
}

func (m model) inputOuterWidth() int {
	return max(4, m.contentWidth())
}

func (m model) contentWidth() int {
	return max(1, m.width-tuiLeftMargin-tuiRightMargin)
}

func (m model) transcriptTextWidth() int {
	if m.viewport.Width > 0 {
		return max(20, m.viewport.Width-2)
	}
	return max(20, m.width-2)
}
