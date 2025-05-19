package main

import (
	"github.com/jingkaihe/kodelet/pkg/tui"
	"github.com/spf13/cobra"
)

// ChatOptions contains all options for the chat command
type ChatOptions struct {
	usePlainUI   bool
	resumeConvID string
	storageType  string
	noSave       bool
}

var chatOptions = &ChatOptions{}

func init() {
	chatCmd.Flags().BoolVar(&chatOptions.usePlainUI, "plain", false, "Use the plain command-line interface instead of the TUI")
	chatCmd.Flags().StringVar(&chatOptions.resumeConvID, "resume", "", "Resume a specific conversation")
	chatCmd.Flags().StringVar(&chatOptions.storageType, "storage", "json", "Specify storage backend (json or sqlite)")
	chatCmd.Flags().BoolVar(&chatOptions.noSave, "no-save", false, "Disable conversation persistence")
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with Kodelet",
	Long:  `Start an interactive chat session with Kodelet through stdin.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		// Start the Bubble Tea UI
		if !chatOptions.usePlainUI {
			tui.StartChatCmd(ctx, chatOptions.resumeConvID, !chatOptions.noSave)
			return
		}

		// Use the plain CLI interface
		plainChatUI(ctx, chatOptions)
	},
}
