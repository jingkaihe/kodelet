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
	return leftMarginBlock(lipgloss.JoinVertical(lipgloss.Left, transcript, input), tuiLeftMargin)
}

func (m model) renderInputBox() string {
	outerWidth := m.inputOuterWidth()
	contentWidth := m.inputContentWidth()
	bodyLines := strings.Split(m.textarea.View(), "\n")
	for len(bodyLines) < inputHeight {
		bodyLines = append(bodyLines, "")
	}

	lines := []string{renderLabeledBorder("╭", "╮", outerWidth, m.inputTopRightLabel())}
	for i := 0; i < inputHeight; i++ {
		lines = append(lines, inputBorderStyle.Render("│")+" "+padVisible(bodyLines[i], contentWidth)+" "+inputBorderStyle.Render("│"))
	}
	lines = append(lines, renderLabeledBorderPair("╰", "╯", outerWidth, m.inputBottomLeftLabel(), displayCWD(m.cwd)))
	return strings.Join(lines, "\n")
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

func renderLabeledBorder(left, right string, width int, label string) string {
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
		inputLabelStyle.Render(label) +
		inputBorderStyle.Render(strings.Repeat("─", endWidth)+right)
}

func renderLabeledBorderPair(left, right string, width int, leftLabel string, rightLabel string) string {
	if strings.TrimSpace(leftLabel) == "" {
		return renderLabeledBorder(left, right, width, rightLabel)
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
	b.WriteString(inputLabelStyle.Render(leftText))
	position = leftStart + leftWidth
	if rightText != "" {
		writeBorderFill(rightStart - position)
		b.WriteString(inputLabelStyle.Render(rightText))
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

func (m model) inputTopRightLabel() string {
	parts := []string{formatUsage(m.usage), m.profile}
	return strings.Join(parts, " — ")
}

func (m model) inputBottomLeftLabel() string {
	if !m.running {
		return ""
	}
	return m.spinner.View() + " " + m.workingStatusText()
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
