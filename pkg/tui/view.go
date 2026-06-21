package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	tuiLeftMargin  = 1
	tuiRightMargin = 1
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
	picker := m.renderProfilePicker()
	input := m.renderInputBox()
	parts := []string{transcript}
	if strings.TrimSpace(picker) != "" {
		parts = append(parts, picker)
	}
	parts = append(parts, input)
	return leftMarginBlock(lipgloss.JoinVertical(lipgloss.Left, parts...), tuiLeftMargin)
}

func (m model) renderInputBox() string {
	outerWidth := m.inputOuterWidth()
	contentWidth := m.inputContentWidth()
	bodyLines := strings.Split(m.textarea.View(), "\n")
	for len(bodyLines) < inputHeight {
		bodyLines = append(bodyLines, "")
	}

	lines := []string{m.renderInputTopBorder()}
	for i := 0; i < inputHeight; i++ {
		lines = append(lines, inputBorderStyle.Render("│")+" "+padVisible(bodyLines[i], contentWidth)+" "+inputBorderStyle.Render("│"))
	}
	lines = append(lines, renderLabeledBorderPair("╰", "╯", outerWidth, m.inputBottomLeftLabel(), displayCWD(m.cwd)))
	return strings.Join(lines, "\n")
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
		{text: " — ", style: inputLabelStyle},
		{text: m.profile, style: m.profileStyle(m.profileIndex)},
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
	inputTopY := m.viewport.Height + m.profilePickerHeight()
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
	parts := []string{formatUsage(m.usage), m.profile}
	return strings.Join(parts, " — ")
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
	profile := m.profile
	if !strings.HasSuffix(visibleLabel, profile) {
		return 0, 0, false
	}

	labelWidth := lipgloss.Width(visibleLabel) + 2
	labelStart := fillWidth - labelWidth - 1
	if labelStart < 0 {
		labelStart = 0
	}
	profileOffset := lipgloss.Width(strings.TrimSuffix(visibleLabel, profile))
	startX = 1 + labelStart + 1 + profileOffset
	endX = startX + lipgloss.Width(profile)
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
