package tui

import "github.com/charmbracelet/lipgloss"

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
