package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/feedback"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// FeedbackConfig holds configuration for the feedback command
type FeedbackConfig struct {
	ConversationID string
	Follow         bool
}

// NewFeedbackConfig creates a new FeedbackConfig with default values
func NewFeedbackConfig() *FeedbackConfig {
	return &FeedbackConfig{
		ConversationID: "",
		Follow:         false,
	}
}

var feedbackCmd = &cobra.Command{
	Use:   "feedback [message]",
	Short: "Send feedback to a running conversation",
	Long: `Send feedback to a running conversation by conversation ID.
This allows you to provide input to a conversation that is currently running
in autonomous mode via 'kodelet run'.

Example:
  kodelet feedback --conversation-id 20231201T120000-a1b2c3d4e5f67890 "Please focus on error handling"
  kodelet feedback --conversation-id 20231201T120000-a1b2c3d4e5f67890 "That approach looks good, continue"
  kodelet feedback -f "Please focus on error handling"
  kodelet feedback --follow "That approach looks good, continue"`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getFeedbackConfigFromFlags(ctx, cmd)
		sendFeedbackCmd(ctx, config.ConversationID, args[0], config.Follow)
	},
}

func init() {
	// Add feedback command flags
	feedbackDefaults := NewFeedbackConfig()
	feedbackCmd.Flags().StringVar(&feedbackDefaults.ConversationID, "conversation-id", feedbackDefaults.ConversationID, "ID of the conversation to send feedback to")
	feedbackCmd.Flags().BoolP("follow", "f", feedbackDefaults.Follow, "Send feedback to the most recent conversation")
}

// getFeedbackConfigFromFlags extracts feedback configuration from command flags
func getFeedbackConfigFromFlags(ctx context.Context, cmd *cobra.Command) *FeedbackConfig {
	config := NewFeedbackConfig()

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

// sendFeedbackCmd sends feedback to a conversation
func sendFeedbackCmd(ctx context.Context, conversationID, message string, isFollow bool) {
	// Validate input
	if conversationID == "" {
		presenter.Error(errors.New("conversation ID is required"), "Please provide a conversation ID using --conversation-id or use -f to target the most recent conversation")
		os.Exit(1)
	}

	if message == "" {
		presenter.Error(errors.New("message is required"), "Please provide a feedback message")
		os.Exit(1)
	}

	// Additional validation for message length
	if len(message) > 10000 {
		presenter.Error(errors.New("message too long"), "Feedback message must be less than 10,000 characters")
		os.Exit(1)
	}

	// Basic validation for conversation ID format
	if len(conversationID) < 10 {
		presenter.Error(errors.New("invalid conversation ID format"), "Conversation ID appears to be invalid (too short)")
		os.Exit(1)
	}

	// Check if conversation exists
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Try to load the conversation to validate it exists
	_, err = store.Load(ctx, conversationID)
	if err != nil {
		presenter.Error(err, fmt.Sprintf("Failed to find conversation with ID: %s", conversationID))
		presenter.Info("Use 'kodelet conversation list' to see available conversations")
		os.Exit(1)
	}

	// Create feedback store
	feedbackStore, err := feedback.NewFeedbackStore()
	if err != nil {
		presenter.Error(err, "Failed to initialize feedback store")
		os.Exit(1)
	}

	// Check if there's already pending feedback (optional warning)
	if feedbackStore.HasPendingFeedback(conversationID) {
		presenter.Warning("There is already pending feedback for this conversation. The new message will be queued.")
	}

	// Write feedback
	err = feedbackStore.WriteFeedback(conversationID, message)
	if err != nil {
		presenter.Error(err, "Failed to write feedback")
		os.Exit(1)
	}

	// Success message
	if isFollow {
		presenter.Success(fmt.Sprintf("Feedback sent to most recent conversation: %s", conversationID))
	} else {
		presenter.Success(fmt.Sprintf("Feedback sent to conversation %s", conversationID))
	}
	presenter.Info(fmt.Sprintf("Message: %s", message))

	// Show helpful information
	presenter.Info("The feedback will be processed when the conversation makes its next API call.")
	presenter.Info("If the conversation is not currently running, start it with:")
	presenter.Info(fmt.Sprintf("  kodelet run --resume %s \"continue\"", conversationID))
}
