package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	ansiResetSequence           = "\x1b[0m"
	ansiForegroundResetSequence = "\x1b[39m"
)

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
	diffAddedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	diffRemovedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	steeringStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("183")).Italic(true)
	steeringErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	inputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("147"))
)

func renderPersistentStyle(style lipgloss.Style, text string) string {
	rendered := style.Render(text)
	start, end := styleSequences(style)
	if text == "" || start == "" || end == "" {
		return rendered
	}

	return reapplyStyleAfterResets(rendered, start, end)
}

func reapplyStyleAfterResets(rendered, start, end string) string {
	lines := strings.SplitAfter(rendered, "\n")
	for i, line := range lines {
		newline := ""
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			newline = "\n"
		}

		if strings.HasSuffix(line, end) {
			line = strings.TrimSuffix(line, end)
			line = reapplyStyleAfterInnerResets(line, start)
			lines[i] = line + end + newline
			continue
		}

		lines[i] = reapplyStyleAfterInnerResets(line, start) + newline
	}
	return strings.Join(lines, "")
}

func reapplyStyleAfterInnerResets(text, start string) string {
	text = strings.ReplaceAll(text, ansiResetSequence, ansiResetSequence+start)
	return strings.ReplaceAll(text, ansiForegroundResetSequence, ansiForegroundResetSequence+start)
}

func styleSequences(style lipgloss.Style) (start, end string) {
	empty := style.Render("")
	if strings.HasSuffix(empty, ansiResetSequence) {
		return strings.TrimSuffix(empty, ansiResetSequence), ansiResetSequence
	}
	if strings.HasSuffix(empty, ansiForegroundResetSequence) {
		return strings.TrimSuffix(empty, ansiForegroundResetSequence), ansiForegroundResetSequence
	}
	return "", ""
}
