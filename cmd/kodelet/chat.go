package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tui"
	"github.com/spf13/cobra"
)

// ChatOptions contains all options for the chat command
type ChatOptions struct {
	usePlainUI   bool
	resumeConvID string
	storageType  string
	noSave       bool
	maxTurns     int
}

var chatOptions = &ChatOptions{}

func init() {
	chatCmd.Flags().BoolVar(&chatOptions.usePlainUI, "plain", false, "Use the plain command-line interface instead of the TUI")
	chatCmd.Flags().StringVar(&chatOptions.resumeConvID, "resume", "", "Resume a specific conversation")
	chatCmd.Flags().StringVar(&chatOptions.storageType, "storage", "json", "Specify storage backend (json or sqlite)")
	chatCmd.Flags().BoolVar(&chatOptions.noSave, "no-save", false, "Disable conversation persistence")
	chatCmd.Flags().IntVar(&chatOptions.maxTurns, "max-turns", 50, "Maximum number of turns within a single message exchange (0 for no limit)")
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with Kodelet",
	Long:  `Start an interactive chat session with Kodelet through stdin.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			fmt.Printf("Error creating MCP manager: %v\n", err)
			os.Exit(1)
		}
		// Start the Bubble Tea UI
		if !chatOptions.usePlainUI {
			// Ensure non-negative values (treat negative as 0/no limit)
			maxTurns := chatOptions.maxTurns
			if maxTurns < 0 {
				maxTurns = 0
			}
			tui.StartChatCmd(ctx, chatOptions.resumeConvID, !chatOptions.noSave, mcpManager, maxTurns)
			return
		}

		// Use the plain CLI interface
		// Ensure non-negative values (treat negative as 0/no limit)
		if chatOptions.maxTurns < 0 {
			chatOptions.maxTurns = 0
		}
		plainChatUI(ctx, chatOptions)
	},
}
