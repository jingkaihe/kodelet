package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tui"
	"github.com/spf13/cobra"
)

// ChatOptions contains all options for the chat command
type ChatOptions struct {
	usePlainUI         bool
	resumeConvID       string
	follow             bool
	storageType        string
	noSave             bool
	maxTurns           int
	enableBrowserTools bool
}

var chatOptions = &ChatOptions{}

func init() {
	chatCmd.Flags().BoolVar(&chatOptions.usePlainUI, "plain", false, "Use the plain command-line interface instead of the TUI")
	chatCmd.Flags().StringVar(&chatOptions.resumeConvID, "resume", "", "Resume a specific conversation")
	chatCmd.Flags().BoolVarP(&chatOptions.follow, "follow", "f", false, "Follow the most recent conversation")
	chatCmd.Flags().StringVar(&chatOptions.storageType, "storage", "json", "Specify storage backend (json or sqlite)")
	chatCmd.Flags().BoolVar(&chatOptions.noSave, "no-save", false, "Disable conversation persistence")
	chatCmd.Flags().IntVar(&chatOptions.maxTurns, "max-turns", 50, "Maximum number of turns within a single message exchange (0 for no limit)")
	chatCmd.Flags().BoolVar(&chatOptions.enableBrowserTools, "enable-browser-tools", false, "Enable browser automation tools (navigate, click, type, screenshot, etc.)")
}

// setupTUILogRedirection redirects logs to a file for TUI mode to prevent interference
func setupTUILogRedirection(conversationID string) (*os.File, string, error) {
	// Create logs directory if it doesn't exist
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get home directory: %w", err)
	}

	logsDir := filepath.Join(homeDir, ".kodelet", "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create log file with conversation ID
	logFileName := fmt.Sprintf("chat-%s.log", conversationID)
	logFilePath := filepath.Join(logsDir, logFileName)

	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open log file: %w", err)
	}

	// Redirect logger output to file
	logger.L.Logger.SetOutput(logFile)

	return logFile, logFilePath, nil
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with Kodelet",
	Long:  `Start an interactive chat session with Kodelet through stdin.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		if chatOptions.follow {
			if chatOptions.resumeConvID != "" {
				presenter.Error(fmt.Errorf("conflicting options"), "--follow and --resume cannot be used together")
				os.Exit(1)
			}
			var err error
			chatOptions.resumeConvID, err = conversations.GetMostRecentConversationID()
			if err != nil {
				presenter.Warning("No conversations found, starting a new conversation")
			}
		}
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create MCP manager")
			os.Exit(1)
		}
		// Start the Bubble Tea UI
		if !chatOptions.usePlainUI {
			// Ensure non-negative values (treat negative as 0/no limit)
			maxTurns := chatOptions.maxTurns
			if maxTurns < 0 {
				maxTurns = 0
			}

			// Generate or use existing conversation ID for log redirection
			conversationID := chatOptions.resumeConvID
			if conversationID == "" && !chatOptions.noSave {
				conversationID = conversations.GenerateID()
			}

			// Set up TUI log redirection if we have a conversation ID
			var logFile *os.File
			var logFilePath string
			if conversationID != "" {
				var err error
				logFile, logFilePath, err = setupTUILogRedirection(conversationID)
				if err != nil {
					// Print warning but don't fail - continue without log redirection
					presenter.Warning(fmt.Sprintf("Failed to set up log redirection for TUI: %v", err))
				} else {
					defer logFile.Close()
				}
			}

			tui.StartChatCmd(ctx, conversationID, !chatOptions.noSave, mcpManager, maxTurns, chatOptions.enableBrowserTools)

			// Restore stderr logging after TUI exits and show log file location
			if logFile != nil {
				logger.L.Logger.SetOutput(os.Stderr)
				if logFilePath != "" {
					presenter.Info(fmt.Sprintf("Chat logs saved to: %s", logFilePath))
				}
			}

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
