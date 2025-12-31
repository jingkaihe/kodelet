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
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type ChatOptions struct {
	resumeConvID       string
	follow             bool
	storageType        string
	noSave             bool
	maxTurns           int
	compactRatio       float64
	disableAutoCompact bool
	noHooks            bool
	noMCP              bool
	useWeakModel       bool
}

var chatOptions = &ChatOptions{}

func init() {
	chatCmd.Flags().StringVar(&chatOptions.resumeConvID, "resume", "", "Resume a specific conversation")
	chatCmd.Flags().BoolVarP(&chatOptions.follow, "follow", "f", false, "Follow the most recent conversation")
	chatCmd.Flags().StringVar(&chatOptions.storageType, "storage", "sqlite", "Storage backend (sqlite only)")
	chatCmd.Flags().BoolVar(&chatOptions.noSave, "no-save", false, "Disable conversation persistence")
	chatCmd.Flags().IntVar(&chatOptions.maxTurns, "max-turns", 50, "Maximum number of turns within a single message exchange (0 for no limit)")
	chatCmd.Flags().Float64Var(&chatOptions.compactRatio, "compact-ratio", 0.80, "Context window utilization ratio to trigger auto-compact (0.0-1.0)")
	chatCmd.Flags().BoolVar(&chatOptions.disableAutoCompact, "disable-auto-compact", false, "Disable automatic context compacting")
	chatCmd.Flags().BoolVar(&chatOptions.noHooks, "no-hooks", false, "Disable agent lifecycle hooks")
	chatCmd.Flags().BoolVar(&chatOptions.noMCP, "no-mcp", false, "Disable MCP tools")
	chatCmd.Flags().BoolVar(&chatOptions.useWeakModel, "use-weak-model", false, "Use weak model for processing")
}

// Prevents TUI interference by redirecting logs to file
func setupTUILogRedirection(conversationID string) (*os.File, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to get home directory")
	}

	logsDir := filepath.Join(homeDir, ".kodelet", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, "", errors.Wrap(err, "failed to create logs directory")
	}

	logFileName := fmt.Sprintf("chat-%s.log", conversationID)
	logFilePath := filepath.Join(logsDir, logFileName)

	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to open log file")
	}

	logger.L.Logger.SetOutput(logFile)

	return logFile, logFilePath, nil
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with Kodelet",
	Long:  `Start an interactive chat session with Kodelet through stdin.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// Validate compact ratio
		if chatOptions.compactRatio < 0.0 || chatOptions.compactRatio > 1.0 {
			presenter.Error(errors.New("invalid compact ratio"), "Compact ratio must be between 0.0 and 1.0")
			os.Exit(1)
		}

		if chatOptions.follow {
			if chatOptions.resumeConvID != "" {
				presenter.Error(errors.New("conflicting options"), "--follow and --resume cannot be used together")
				os.Exit(1)
			}
			var err error
			chatOptions.resumeConvID, err = conversations.GetMostRecentConversationID(ctx)
			if err != nil {
				presenter.Warning("No conversations found, starting a new conversation")
			}
		}
		var mcpManager *tools.MCPManager
		var err error
		if !chatOptions.noMCP {
			mcpManager, err = tools.CreateMCPManagerFromViper(ctx)
			if err != nil && !errors.Is(err, tools.ErrMCPDisabled) {
				presenter.Error(err, "Failed to create MCP manager")
				os.Exit(1)
			}
		}

		customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create custom tool manager")
			os.Exit(1)
		}
		// Ensure non-negative values (treat negative as 0/no limit)
		maxTurns := max(chatOptions.maxTurns, 0)

		// Generate or use existing conversation ID for log redirection
		conversationID := chatOptions.resumeConvID
		if conversationID == "" {
			conversationID = convtypes.GenerateID()
		}

		// Set up TUI log redirection if we have a conversation ID
		var logFile *os.File
		var logFilePath string
		if conversationID != "" {
			var err error
			logFile, logFilePath, err = setupTUILogRedirection(conversationID)
			if err != nil {
				presenter.Warning(fmt.Sprintf("Failed to set up log redirection for TUI: %v", err))
			} else {
				defer logFile.Close()
			}
		}

		tui.StartChatCmd(ctx, tui.ChatOpts{
			ConversationID:     conversationID,
			EnablePersistence:  !chatOptions.noSave,
			MCPManager:         mcpManager,
			CustomManager:      customManager,
			MaxTurns:           maxTurns,
			CompactRatio:       chatOptions.compactRatio,
			DisableAutoCompact: chatOptions.disableAutoCompact,
			NoHooks:            chatOptions.noHooks,
			UseWeakModel:       chatOptions.useWeakModel,
		})

		// Restore stderr logging after TUI exits and show log file location
		if logFile != nil {
			logger.L.Logger.SetOutput(os.Stderr)
			if logFilePath != "" {
				presenter.Info(fmt.Sprintf("Chat logs saved to: %s", logFilePath))
			}
		}
	},
}
