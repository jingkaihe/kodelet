package tui

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
)

// StartChat starts the TUI chat interface
func StartChat(ctx context.Context,
	conversationID string,
	enablePersistence bool,
	mcpManager *tools.MCPManager,
	customManager *tools.CustomToolManager,
	maxTurns int,
	compactRatio float64,
	disableAutoCompact bool,
	ideMode bool,
	noSkills bool,
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
	model := NewModel(ctx, conversationID, enablePersistence, mcpManager, customManager, maxTurns, compactRatio, disableAutoCompact, ideMode, noSkills)

	welcomeMsg := fmt.Sprintf(`
Kodelet (%s)
	`, version.Version)

	// Style the banner (Tokyo Night)
	styledBanner := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#7aa2f7", Dark: "#7aa2f7"}). // Blue
		Render(welcomeMsg)

	fullWelcomeMsg := styledBanner + "\nWelcome to Kodelet Chat! Type your message and press Enter to send."
	if !isTTY() {
		fullWelcomeMsg += "\nLimited terminal capabilities detected. Some features may not work properly."
	}

	// Add persistence status
	if enablePersistence {
		fullWelcomeMsg += "\nConversation persistence is enabled."
	} else {
		fullWelcomeMsg += "\nConversation persistence is disabled (--no-save)."
	}

	if ideMode {
		idMsg := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#9ece6a", Dark: "#9ece6a"}).
			Render(fmt.Sprintf("\n📋 Conversation ID: %s", conversationID))
		fullWelcomeMsg += idMsg
		fullWelcomeMsg += "\n💡 Attach your IDE using: :KodeletAttach " + conversationID
	}

	model.AddSystemMessage(fullWelcomeMsg)
	model.AddSystemMessage("Press Ctrl+H for help with keyboard shortcuts.")

	// Create a new program with the updated model
	p = tea.NewProgram(model, teaOptions...)

	result, err := p.Run()
	if err != nil {
		return errors.Wrap(err, "error running program")
	}

	// use a new context to avoid cancellation
	// defer model.assistant.SaveConversation(context.Background())

	// Display final usage statistics on exit
	if model, ok := result.(Model); ok {
		usage := model.assistant.GetUsage()
		if usage.TotalTokens() > 0 {
			fmt.Printf("\n\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\033[0m\n",
				usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens())

			// Display context window information
			if usage.MaxContextWindow > 0 {
				percentage := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow) * 100
				fmt.Printf("\033[1;36m[Context Window] Current: %d | Max: %d | Usage: %.1f%%\033[0m\n",
					usage.CurrentContextWindow, usage.MaxContextWindow, percentage)
			}

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
func StartChatCmd(ctx context.Context, conversationID string, enablePersistence bool, mcpManager *tools.MCPManager, customManager *tools.CustomToolManager, maxTurns int, compactRatio float64, disableAutoCompact bool, ideMode bool, noSkills bool) {
	if err := StartChat(ctx, conversationID, enablePersistence, mcpManager, customManager, maxTurns, compactRatio, disableAutoCompact, ideMode, noSkills); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
