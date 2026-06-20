package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
