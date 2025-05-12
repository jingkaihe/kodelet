package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// StartChat starts the TUI chat interface
func StartChat() error {
	// Check terminal capabilities
	var teaOptions []tea.ProgramOption

	// Always use the full terminal screen
	teaOptions = append(teaOptions, tea.WithAltScreen())

	// Try to enable mouse support, but don't fail if not available
	if isTTY() {
		teaOptions = append(teaOptions, tea.WithMouseCellMotion())
	}

	// Initialize the program variable first
	var p *tea.Program

	// Create model separately to add welcome messages
	model := NewModel()

	// Add welcome message with ASCII art
	kodaletArt := `

	▗▖ ▗▖ ▗▄▖ ▗▄▄▄ ▗▄▄▄▖▗▖   ▗▄▄▄▖▗▄▄▄▖
	▐▌▗▞▘▐▌ ▐▌▐▌  █▐▌   ▐▌   ▐▌     █
	▐▛▚▖ ▐▌ ▐▌▐▌  █▐▛▀▀▘▐▌   ▐▛▀▀▘  █
	▐▌ ▐▌▝▚▄▞▘▐▙▄▄▀▐▙▄▄▖▐▙▄▄▖▐▙▄▄▖  █

`

	// Style the ASCII art to be bold and blood red like Khorne
	styledArt := lipgloss.NewStyle().
		Bold(true).
		Italic(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#990000", Dark: "#FF0000"}).
		Margin(1).
		Render(kodaletArt)

	welcomeMsg := styledArt + "\n\nWelcome to Kodelet Chat! Type your message and press Enter to send."
	if !isTTY() {
		welcomeMsg += "\nLimited terminal capabilities detected. Some features may not work properly."
	}
	model.AddSystemMessage(welcomeMsg)
	model.AddSystemMessage("Press Ctrl+H for help with keyboard shortcuts.")

	// Create a new program with the updated model
	p = tea.NewProgram(model, teaOptions...)

	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("error running program: %w", err)
	}

	// Display final usage statistics on exit
	if model, ok := result.(Model); ok {
		usage := model.assistant.GetUsage()
		if usage.TotalTokens() > 0 {
			fmt.Printf("\n\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\033[0m\n",
				usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens())

			// Display cost information
			fmt.Printf("\033[1;36m[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\033[0m\n",
				usage.InputCost, usage.OutputCost, usage.CacheCreationCost, usage.CacheReadCost, usage.TotalCost())
		}
	}

	return nil
}

// isTTY checks if the terminal supports advanced features
func isTTY() bool {
	// Simple heuristic - if STDIN is a TTY, we assume we have good terminal support
	fileInfo, _ := os.Stdin.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

// StartChatCmd is a wrapper that can be called from a command line
func StartChatCmd() {
	if err := StartChat(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
