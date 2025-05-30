package tui

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/tools"
)

// StartChat starts the TUI chat interface
func StartChat(ctx context.Context,
	conversationID string,
	enablePersistence bool,
	mcpManager *tools.MCPManager,
	maxTurns int,
) error {
	// Check terminal capabilities
	var teaOptions []tea.ProgramOption

	// Always use the full terminal screen
	teaOptions = append(teaOptions, tea.WithAltScreen())

	// disable mouse cell motion to allow text selection/copying
	// Try to enable mouse support, but don't fail if not available
	// if isTTY() {
	// 	teaOptions = append(teaOptions, tea.WithMouseCellMotion())
	// }

	// Initialize the program variable first
	var p *tea.Program

	// Create model separately to add welcome messages
	model := NewModel(ctx, conversationID, enablePersistence, mcpManager, maxTurns)

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

	// Add persistence status
	if enablePersistence {
		welcomeMsg += "\nConversation persistence is enabled."
	} else {
		welcomeMsg += "\nConversation persistence is disabled (--no-save)."
	}

	model.AddSystemMessage(welcomeMsg)
	model.AddSystemMessage("Press Ctrl+H for help with keyboard shortcuts.")

	// Create a new program with the updated model
	p = tea.NewProgram(model, teaOptions...)

	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("error running program: %w", err)
	}

	// use a new context to avoid cancellation
	defer model.assistant.SaveConversation(context.Background())

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

		// Display conversation ID if persistence was enabled
		if model.assistant.IsPersisted() {
			fmt.Printf("\033[1;36m[Conversation] ID: %s\033[0m\n", model.assistant.GetConversationID())
			fmt.Printf("To resume this conversation: kodelet chat --resume %s\n", model.assistant.GetConversationID())
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
func StartChatCmd(ctx context.Context, conversationID string, enablePersistence bool, mcpManager *tools.MCPManager, maxTurns int) {
	if err := StartChat(ctx, conversationID, enablePersistence, mcpManager, maxTurns); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
