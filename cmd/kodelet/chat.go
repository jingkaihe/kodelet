package main

import (
	"github.com/jingkaihe/kodelet/pkg/tui"
	"github.com/spf13/cobra"
)

var (
	useLegacyUI bool
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with Kodelet",
	Long:  `Start an interactive chat session with Kodelet through stdin.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Start the Bubble Tea UI
		if !useLegacyUI {
			tui.StartChatCmd()
			return
		}

		// Use the legacy CLI interface
		legacyChatUI()
	},
}

func init() {
	chatCmd.Flags().BoolVar(&useLegacyUI, "legacy", false, "Use the legacy command-line interface instead of the TUI")
}
