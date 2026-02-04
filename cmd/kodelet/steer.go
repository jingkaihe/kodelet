package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type SteerConfig struct {
	ConversationID string
	Follow         bool
}

func NewSteerConfig() *SteerConfig {
	return &SteerConfig{
		ConversationID: "",
		Follow:         false,
	}
}

var steerCmd = &cobra.Command{
	Use:   "steer [message]",
	Short: "Steer a running conversation",
	Long: `Steer a running conversation by conversation ID.
This allows you to provide guidance to a conversation that is currently running
in autonomous mode via 'kodelet run'.

Example:
  kodelet steer --conversation-id 20231201T120000-a1b2c3d4e5f67890 "Please focus on error handling"
  kodelet steer --conversation-id 20231201T120000-a1b2c3d4e5f67890 "That approach looks good, continue"
  kodelet steer -f "Please focus on error handling"
  kodelet steer --follow "That approach looks good, continue"`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getSteerConfigFromFlags(ctx, cmd)
		sendSteerCmd(ctx, config.ConversationID, args[0], config.Follow)
	},
}

func init() {
	steerDefaults := NewSteerConfig()
	steerCmd.Flags().StringVar(&steerDefaults.ConversationID, "conversation-id", steerDefaults.ConversationID, "ID of the conversation to steer")
	steerCmd.Flags().BoolP("follow", "f", steerDefaults.Follow, "Steer the most recent conversation")
}

func getSteerConfigFromFlags(ctx context.Context, cmd *cobra.Command) *SteerConfig {
	config := NewSteerConfig()

	if conversationID, err := cmd.Flags().GetString("conversation-id"); err == nil {
		config.ConversationID = conversationID
	}
	if follow, err := cmd.Flags().GetBool("follow"); err == nil {
		config.Follow = follow
	}

	if config.Follow {
		if config.ConversationID != "" {
			presenter.Error(errors.New("conflicting flags"), "--follow and --conversation-id cannot be used together")
			os.Exit(1)
		}
		var err error
		config.ConversationID, err = conversations.GetMostRecentConversationID(ctx)
		if err != nil {
			presenter.Error(err, "Failed to get most recent conversation")
			presenter.Info("Use 'kodelet conversation list' to see available conversations")
			os.Exit(1)
		}
	}

	return config
}

func sendSteerCmd(ctx context.Context, conversationID, message string, isFollow bool) {
	if conversationID == "" {
		presenter.Error(errors.New("conversation ID is required"), "Please provide a conversation ID using --conversation-id or use -f to target the most recent conversation")
		os.Exit(1)
	}

	if message == "" {
		presenter.Error(errors.New("message is required"), "Please provide a steering message")
		os.Exit(1)
	}

	if len(message) > 10000 {
		presenter.Error(errors.New("message too long"), "Steering message must be less than 10,000 characters")
		os.Exit(1)
	}

	if len(conversationID) < 10 {
		presenter.Error(errors.New("invalid conversation ID format"), "Conversation ID appears to be invalid (too short)")
		os.Exit(1)
	}

	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	_, err = store.Load(ctx, conversationID)
	if err != nil {
		presenter.Error(err, fmt.Sprintf("Failed to find conversation with ID: %s", conversationID))
		presenter.Info("Use 'kodelet conversation list' to see available conversations")
		os.Exit(1)
	}

	steerStore, err := steer.NewSteerStore()
	if err != nil {
		presenter.Error(err, "Failed to initialize steer store")
		os.Exit(1)
	}

	if steerStore.HasPendingSteer(conversationID) {
		presenter.Warning("There is already pending steering for this conversation. The new message will be queued.")
	}

	err = steerStore.WriteSteer(conversationID, message)
	if err != nil {
		presenter.Error(err, "Failed to write steering message")
		os.Exit(1)
	}

	if isFollow {
		presenter.Success(fmt.Sprintf("Steering sent to most recent conversation: %s", conversationID))
	} else {
		presenter.Success(fmt.Sprintf("Steering sent to conversation %s", conversationID))
	}
	presenter.Info(fmt.Sprintf("Message: %s", message))

	presenter.Info("The steering will be processed when the conversation makes its next API call.")
	presenter.Info("If the conversation is not currently running, start it with:")
	presenter.Info(fmt.Sprintf("  kodelet run --resume %s \"continue\"", conversationID))
}
