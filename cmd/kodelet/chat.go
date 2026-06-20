package main

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tui"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type ChatConfig struct {
	ResumeConvID string
	CWD          string
	Follow       bool
	NoExtensions bool
	NoMCP        bool
	NoTools      bool
}

func NewChatConfig() *ChatConfig {
	return &ChatConfig{}
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive Kodelet chat TUI",
	Long:  `Start an interactive terminal UI for chatting with Kodelet.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getChatConfigFromFlags(ctx, cmd)

		if config.NoExtensions {
			viper.Set("extensions.enabled", false)
		}
		if config.NoMCP || config.NoTools {
			viper.Set("mcp.enabled", false)
		}
		if config.NoTools {
			viper.Set("allowed_tools", []string{"none"})
		}
		logger.SetLogOutput(io.Discard)

		profile, _ := cmd.Flags().GetString("profile")
		if strings.TrimSpace(profile) == "" {
			profile = viper.GetString("profile")
		}

		if err := tui.Run(ctx, tui.Config{
			ConversationID: config.ResumeConvID,
			Profile:        profile,
			CWD:            config.CWD,
		}); err != nil {
			presenter.Error(err, "Chat failed")
			os.Exit(1)
		}
	},
}

func init() {
	defaults := NewChatConfig()
	chatCmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "Resume a specific conversation")
	chatCmd.Flags().String("cwd", defaults.CWD, "Working directory to execute in (defaults to current shell directory for new chats)")
	chatCmd.Flags().BoolP("follow", "f", defaults.Follow, "Follow the most recent conversation")
	chatCmd.Flags().Bool("no-extensions", defaults.NoExtensions, "Disable extension runtime")
	chatCmd.Flags().Bool("no-mcp", defaults.NoMCP, "Disable MCP tools")
	chatCmd.Flags().Bool("no-tools", defaults.NoTools, "Disable all tools (for simple query-response usage)")
}

func getChatConfigFromFlags(ctx context.Context, cmd *cobra.Command) *ChatConfig {
	config := NewChatConfig()

	if resumeConvID, err := cmd.Flags().GetString("resume"); err == nil {
		config.ResumeConvID = strings.TrimSpace(resumeConvID)
	}
	if cwd, err := cmd.Flags().GetString("cwd"); err == nil {
		config.CWD = strings.TrimSpace(cwd)
	}
	if follow, err := cmd.Flags().GetBool("follow"); err == nil {
		config.Follow = follow
	}
	if config.Follow {
		if config.ResumeConvID != "" {
			presenter.Error(errors.New("conflicting flags"), "--follow and --resume cannot be used together")
			os.Exit(1)
		}
		var err error
		config.ResumeConvID, err = conversations.GetMostRecentConversationID(ctx)
		if err != nil {
			presenter.Warning("No conversations found, starting a new conversation")
		}
	}
	if noExtensions, err := cmd.Flags().GetBool("no-extensions"); err == nil {
		config.NoExtensions = noExtensions
	}
	if noMCP, err := cmd.Flags().GetBool("no-mcp"); err == nil {
		config.NoMCP = noMCP
	}
	if noTools, err := cmd.Flags().GetBool("no-tools"); err == nil {
		config.NoTools = noTools
	}
	if config.NoTools && !config.NoMCP {
		config.NoMCP = true
	}

	return config
}
