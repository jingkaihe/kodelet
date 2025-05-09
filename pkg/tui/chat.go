package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
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
	
	// Add welcome message
	welcomeMsg := "Welcome to Kodelet Chat! Type your message and press Enter to send."
	if !isTTY() {
		welcomeMsg += "\nLimited terminal capabilities detected. Some features may not work properly."
	}
	model.AddSystemMessage(welcomeMsg)
	model.AddSystemMessage("Press Ctrl+H for help with keyboard shortcuts.")
	
	// Create a new program with the updated model
	p = tea.NewProgram(model, teaOptions...)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running program: %w", err)
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