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
	parent.PersistentFlags().String("server", defaultRunnerServer, "Conversation authority (or KODELET_SERVER)")
	parent.PersistentFlags().String("auth-token", "", "Control-plane client authentication token (or KODELET_AUTH_TOKEN)")
	for _, cmd := range parent.Commands() {
		if cmd.Name() == "list" || cmd.Name() == "fork" {
			cmd.Flags().String("runner", "", "Filter daemon history by exact runner ID; no online runner required")
			cmd.Flags().String("cwd", "", "Filter daemon history by canonical persisted runner-host directory")
		}
		cmd.Run = nil
		cmd.RunE = runRemoteConversationCommand
	}
	parent.AddCommand(newConversationAdoptCommand())
}

func runRemoteConversationCommand(cmd *cobra.Command, args []string) error {
	if cmd.Name() == "import" || cmd.Name() == "edit" {
		return errors.Errorf("conversation %s is not supported by the daemon API; arbitrary record replacement is disabled. Export a copy to inspect it; legacy history requires validated adoption", cmd.Name())
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
		return errors.New("provide a conversation ID or --cwd/--runner to select the most recent daemon conversation in that scope")
	}
	if cmd.Name() == "fork" && len(args) > 0 && (cmd.Flags().Changed("cwd") || cmd.Flags().Changed("runner")) {
		return errors.New("--cwd/--runner filter implicit fork selection; do not combine them with an explicit conversation ID")
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
		return errors.Wrap(err, "daemon conversation operation failed; check kodelet serve, --server and client authentication (no local database fallback)")
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
			return errors.Wrap(err, "failed to write client export file")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Conversation %s exported to %s\n", record.ID, path)
		return nil
	case "delete":
		if !getConversationDeleteConfigFromFlags(cmd).NoConfirm {
			answer := presenter.Prompt(fmt.Sprintf("Delete daemon conversation %s?", args[0]), "y", "N")
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
				return errors.New("no daemon conversation matches the selected scope")
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
		return errors.Errorf("unsupported daemon conversation operation %q", cmd.Name())
	}
}

func newConversationAdoptCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopt <conversation-id>",
		Short: "Bind legacy daemon history to an explicitly selected runner",
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
			fmt.Fprintln(cmd.OutOrStdout(), "Matching paths on different hosts do not prove workspace identity. Confirm only if this is the intended workspace.")
			if previewOnly {
				fmt.Fprintln(cmd.OutOrStdout(), "Preview only; history remains unbound.")
				return nil
			}
			if !yes && !strings.EqualFold(presenter.Prompt("Adopt this conversation into the displayed environment?", "y", "N"), "y") {
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
				return errors.New("daemon did not confirm adoption; inspect conversation affinity")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Conversation %s adopted; history and model configuration preserved.\n", result.ConversationID)
			return nil
		},
	}
	cmd.Flags().String("runner", "", "Exact runner ID to adopt into (required; never uses a default)")
	cmd.Flags().String("runner-profile", "", "Runner environment profile (inherits stored profile, otherwise default)")
	cmd.Flags().Bool("preview", false, "Validate and display the target without binding history")
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
