package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func addRemoteConversationCommands(parent *cobra.Command) {
	parent.PersistentFlags().String("server", defaultRunnerServer, "Server URL (or KODELET_SERVER)")
	parent.PersistentFlags().String("auth-token", "", "API authentication token (or KODELET_AUTH_TOKEN)")
	for _, cmd := range parent.Commands() {
		if cmd.Name() == "list" || cmd.Name() == "fork" {
			cmd.Flags().String("runner", "", "Filter conversations by exact runner ID (the runner can be offline)")
			cmd.Flags().String("cwd", "", "Filter conversations by their saved absolute directory on the runner")
		}
		cmd.Run = nil
		cmd.RunE = runRemoteConversationCommand
	}
	parent.AddCommand(newConversationAdoptCommand())
}

func runRemoteConversationCommand(cmd *cobra.Command, args []string) error {
	if cmd.Name() == "import" || cmd.Name() == "edit" {
		return errors.Errorf("'kodelet conversation %s' is no longer supported; use 'kodelet conversation export' to save a copy, or 'kodelet conversation adopt' to continue an older conversation", cmd.Name())
	}
	var query conversations.ListConversationsRequest
	if cmd.Name() == "list" {
		config := getConversationListConfigFromFlags(cmd)
		query = conversations.ListConversationsRequest{SearchTerm: config.Search, Provider: config.Provider, Limit: config.Limit, Offset: config.Offset, SortBy: config.SortBy, SortOrder: config.SortOrder}
		for name, value := range map[string]string{"start": config.StartDate, "end": config.EndDate} {
			if value == "" {
				continue
			}
			date, err := time.Parse("2006-01-02", value)
			if err != nil {
				return errors.Wrapf(err, "--%s must use YYYY-MM-DD", name)
			}
			if name == "start" {
				query.StartDate = &date
			} else {
				date = date.Add(24*time.Hour - time.Nanosecond)
				query.EndDate = &date
			}
		}
	}
	query.CWD, _ = cmd.Flags().GetString("cwd")
	query.RunnerID, _ = cmd.Flags().GetString("runner")
	if cmd.Name() == "fork" && len(args) == 0 && query.CWD == "" && query.RunnerID == "" {
		return errors.New("provide a conversation ID, or use --cwd or --runner to find the most recent conversation in that directory or runner")
	}
	if cmd.Name() == "fork" && len(args) > 0 && (cmd.Flags().Changed("cwd") || cmd.Flags().Changed("runner")) {
		return errors.New("--cwd and --runner are only used to find the most recent conversation; omit them when providing a conversation ID")
	}
	if cmd.Name() == "export" {
		config := getConversationExportConfigFromFlags(cmd)
		if config.UseGist && config.UsePublicGist {
			return errors.New("cannot use both --gist and --public-gist")
		}
	}
	server, _ := serverFlagOrConfig(cmd)
	token, _, err := resolveControlPlaneAuthToken(cmd, server)
	if err != nil {
		return err
	}
	client, err := chat.NewControlPlaneChatRunner(server, token, "")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err := executeRemoteConversation(ctx, cmd, args, client, query); err != nil {
		return errors.Wrap(err, "could not complete the conversation command")
	}
	return nil
}

func executeRemoteConversation(ctx context.Context, cmd *cobra.Command, args []string, client *chat.ControlPlaneChatRunner, query conversations.ListConversationsRequest) error {
	switch cmd.Name() {
	case "list":
		result, err := client.QueryConversations(ctx, query)
		if err != nil {
			return err
		}
		metadata := make(map[string]map[string]any, len(result.Conversations))
		for _, summary := range result.Conversations {
			metadata[summary.ID] = summary.Metadata
		}
		format := TableFormat
		if getConversationListConfigFromFlags(cmd).JSONOutput {
			format = JSONFormat
		}
		return NewConversationListOutput(result.Conversations, metadata, format).Render(cmd.OutOrStdout())
	case "show", "export":
		record, err := client.LoadConversationRecord(ctx, args[0])
		if err != nil {
			return err
		}
		if cmd.Name() == "show" {
			return renderConversationRecord(cmd.OutOrStdout(), record, getConversationShowConfigFromFlags(cmd))
		}
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed to serialize conversation")
		}
		config := getConversationExportConfigFromFlags(cmd)
		if config.UseGist || config.UsePublicGist {
			return createGist(record.ID, data, config.UseGist)
		}
		path := record.ID + ".json"
		if len(args) > 1 {
			path = args[1]
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return errors.Wrap(err, "failed to write the export file")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Conversation %s exported to %s\n", record.ID, path)
		return nil
	case "delete":
		if !getConversationDeleteConfigFromFlags(cmd).NoConfirm {
			answer := presenter.Prompt(fmt.Sprintf("Delete conversation %s?", args[0]), "y", "N")
			if !strings.EqualFold(answer, "y") {
				fmt.Fprintln(cmd.OutOrStdout(), "Deletion cancelled.")
				return nil
			}
		}
		if err := client.DeleteConversation(ctx, args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Conversation %s deleted successfully\n", args[0])
		return nil
	case "fork":
		var id string
		if len(args) > 0 {
			id = args[0]
		} else {
			query.Limit, query.SortBy, query.SortOrder = 1, "updatedAt", "desc"
			result, err := client.QueryConversations(ctx, query)
			if err != nil {
				return err
			}
			if len(result.Conversations) == 0 {
				return errors.New("no conversation found for the selected runner or directory")
			}
			id = result.Conversations[0].ID
		}
		forked, err := client.ForkConversation(ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Conversation forked successfully. New ID: %s\n", forked)
		return nil
	default:
		return errors.Errorf("unsupported conversation command %q", cmd.Name())
	}
}

func newConversationAdoptCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopt <conversation-id>",
		Short: "Choose a runner for continuing an older conversation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runnerID, _ := cmd.Flags().GetString("runner")
			profile, _ := cmd.Flags().GetString("runner-profile")
			previewOnly, _ := cmd.Flags().GetBool("preview")
			yes, _ := cmd.Flags().GetBool("yes")
			server, _ := serverFlagOrConfig(cmd)
			token, _, err := resolveControlPlaneAuthToken(cmd, server)
			if err != nil {
				return err
			}
			client, err := chat.NewControlPlaneChatRunner(server, token, "")
			if err != nil {
				return err
			}
			params := chat.ConversationAdoptionRequest{RunnerID: runnerID, EnvironmentProfile: profile}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			preview, err := client.AdoptConversation(ctx, args[0], params)
			cancel()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Conversation: %s\nRunner: %s (%s)\nHost: %s [%s]\nDirectory: %s\nRunner profile: %s\nModel: %s/%s (profile %s)\n", preview.ConversationID, preview.RunnerID, preview.RunnerName, preview.Hostname, preview.HostInstanceID, preview.CWD, adoptionProfileLabel(preview.EnvironmentProfile), preview.Provider, preview.Model, adoptionProfileLabel(preview.ModelProfile))
			fmt.Fprintln(cmd.OutOrStdout(), "Check the host and directory above before continuing; the same path on another machine may contain a different workspace.")
			if previewOnly {
				fmt.Fprintln(cmd.OutOrStdout(), "Preview only; the conversation has not been changed.")
				return nil
			}
			if !yes && !strings.EqualFold(presenter.Prompt("Use this runner and directory to continue the conversation?", "y", "N"), "y") {
				fmt.Fprintln(cmd.OutOrStdout(), "Adoption cancelled; history is unchanged.")
				return nil
			}
			params.Confirmation = preview.Confirmation
			ctx, cancel = context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			result, err := client.AdoptConversation(ctx, args[0], params)
			if err != nil {
				return err
			}
			if !result.Adopted {
				return errors.New("could not confirm the runner assignment; check 'kodelet conversation show' before trying again")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Conversation %s is ready to continue; its history and model settings are unchanged.\n", result.ConversationID)
			return nil
		},
	}
	cmd.Flags().String("runner", "", "ID of the runner to use for this conversation (required)")
	cmd.Flags().String("runner-profile", "", "Runner environment profile (inherits stored profile, otherwise default)")
	cmd.Flags().Bool("preview", false, "Preview the runner and directory without changing the conversation")
	cmd.Flags().Bool("yes", false, "Confirm the displayed target without prompting")
	_ = cmd.MarkFlagRequired("runner")
	cmd.MarkFlagsMutuallyExclusive("preview", "yes")
	return cmd
}

func adoptionProfileLabel(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return "default"
	}
	return profile
}
