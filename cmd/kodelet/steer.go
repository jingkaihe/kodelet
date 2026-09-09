package main

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type SteerConfig struct {
	ConversationID string
	Follow         bool
	Images         []string
}

func NewSteerConfig() *SteerConfig {
	return &SteerConfig{
		ConversationID: "",
		Follow:         false,
		Images:         []string{},
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
  kodelet steer --conversation-id 20231201T120000-a1b2c3d4e5f67890 --image ./screenshot.png "Use this screenshot as context"
  kodelet steer -f "Please focus on error handling"
  kodelet steer --follow "That approach looks good, continue"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return sendRemoteSteer(cmd, args[0]) },
}

func init() {
	steerDefaults := NewSteerConfig()
	steerCmd.Flags().StringVar(&steerDefaults.ConversationID, "conversation-id", steerDefaults.ConversationID, "ID of the conversation to steer")
	steerCmd.Flags().BoolP("follow", "f", steerDefaults.Follow, "Steer the most recent conversation")
	steerCmd.Flags().StringSliceP("image", "I", steerDefaults.Images, "Add image input (can be used multiple times)")
	addRemoteRunFlags(steerCmd)
	steerCmd.Flags().String("cwd", "", "Find the most recent conversation in this directory with --follow")
}

func sendRemoteSteer(cmd *cobra.Command, message string) error {
	ctx := cmd.Context()
	id, _ := cmd.Flags().GetString("conversation-id")
	id = strings.TrimSpace(id)
	follow, _ := cmd.Flags().GetBool("follow")
	if follow && id != "" {
		return errors.New("--follow and --conversation-id cannot be used together")
	}
	if !follow && id == "" {
		return errors.New("provide --conversation-id, or use --follow with --runner or --cwd")
	}
	if strings.TrimSpace(message) == "" || len(message) > steer.MaxMessageLength {
		return errors.New("steering message must be nonempty and at most 10,000 characters")
	}
	if cmd.Flags().Changed("runner-profile") {
		return errors.New("--runner-profile cannot replace the stored profile of a running conversation")
	}
	selector, _ := cmd.Flags().GetString("runner")
	cwd, _ := cmd.Flags().GetString("cwd")
	if follow && strings.TrimSpace(selector) == "" && strings.TrimSpace(cwd) == "" {
		return errors.New("--follow requires --runner or --cwd to choose which conversation history to search")
	}
	server, _ := serverFlagOrConfig(cmd)
	token, _, err := resolveControlPlaneAuthToken(cmd, server)
	if err != nil {
		return err
	}
	var runnerID string
	if selector != "" {
		runners, _, err := fetchRunners(ctx, server, token)
		if err != nil {
			return err
		}
		runner, err := selectRunner(runners, selector)
		if err != nil {
			return err
		}
		runnerID = runner.ID
		if cwd == "" {
			cwd = runner.Workspace.Path
		}
	}
	client, err := chat.NewClient(server, token, runnerID)
	if err != nil {
		return err
	}
	if follow {
		history, err := client.ListConversationsInCWD(ctx, 1, cwd)
		if err != nil {
			return err
		}
		if len(history) == 0 {
			return errors.New("no conversation found for the selected runner or directory")
		}
		id = history[0].ID
	}
	if runnerID != "" {
		history, err := client.LoadConversation(ctx, id)
		if err != nil {
			return err
		}
		if history.RunnerID != runnerID {
			return errors.New("this conversation uses a different runner; omit --runner to use its saved runner")
		}
	}
	paths, _ := cmd.Flags().GetStringSlice("image")
	images := make([]string, 0, len(paths))
	for _, path := range paths {
		image, err := remoteRunImage(path)
		if err != nil {
			return err
		}
		images = append(images, image.ImageURL.URL)
	}
	queued, err := client.SteerConversation(ctx, id, message, images)
	if err != nil {
		return errors.Wrap(err, "could not confirm that the steering message was received; check the conversation before sending it again")
	}
	if queued {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Your steering message is queued behind an earlier message.")
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Steering sent to conversation %s\n", id)
	return err
}
