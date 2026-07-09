package main

import (
	"context"
	"io"
	stdlog "log"
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
	Theme        string
	Follow       bool
	NoExtensions bool
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
		if config.NoTools {
			viper.Set("allowed_tools", []string{"none"})
		}
		if err := tui.ValidateThemeName(config.Theme); err != nil {
			presenter.Error(err, "Invalid TUI theme")
			os.Exit(1)
		}
		if err := validateChatResumeConversation(ctx, config.ResumeConvID); err != nil {
			presenter.Error(err, "Failed to resume conversation")
			os.Exit(1)
		}
		logger.SetLogOutput(io.Discard)
		stdlog.SetOutput(io.Discard)

		profile, _ := cmd.Flags().GetString("profile")
		if strings.TrimSpace(profile) == "" {
			profile = viper.GetString("profile")
		}

		if err := tui.Run(ctx, tui.Config{
			ConversationID: config.ResumeConvID,
			Profile:        profile,
			CWD:            config.CWD,
			Theme:          config.Theme,
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
	chatCmd.Flags().String("theme", tui.DefaultThemeName, "TUI theme (available: "+strings.Join(tui.AvailableThemeNames(), ", ")+")")
	chatCmd.Flags().BoolP("follow", "f", defaults.Follow, "Follow the most recent conversation")
	chatCmd.Flags().Bool("no-extensions", defaults.NoExtensions, "Disable extension runtime")
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
	if theme, err := cmd.Flags().GetString("theme"); err == nil {
		config.Theme = strings.TrimSpace(theme)
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
	if noTools, err := cmd.Flags().GetBool("no-tools"); err == nil {
		config.NoTools = noTools
	}

	return config
}

func validateChatResumeConversation(ctx context.Context, conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}

	service, err := conversations.GetDefaultConversationService(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to open conversation store")
	}
	defer func() {
		_ = service.Close()
	}()

	if _, err := service.GetConversation(ctx, conversationID); err != nil {
		return errors.Wrapf(err, "conversation not found: %s", conversationID)
	}
	return nil
}
