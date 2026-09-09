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
	parent.Args = cobra.NoArgs
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
	parent.AddCommand(newConversationMoveCommand())
}

func runRemoteConversationCommand(cmd *cobra.Command, args []string) error {
	if cmd.Name() == "import" || cmd.Name() == "edit" {
		return errors.Errorf("'kodelet conversation %s' is no longer supported; use 'kodelet conversation export' to save a copy, or 'kodelet conversation move <conversation-id> <runner-id>[:<cwd>]' to assign a runner", cmd.Name())
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
	client, err := chat.NewClient(server, token, "")
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

func executeRemoteConversation(ctx context.Context, cmd *cobra.Command, args []string, client *chat.Client, query conversations.ListConversationsRequest) error {
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

func newConversationMoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move <conversation-id> <runner-id>[:<cwd>]",
		Short: "Move a conversation to another runner or directory",
		Long: "Update a conversation's saved runner and optionally its directory. Omit :<cwd> to keep the saved directory. " +
			"The destination runner can be offline. No files are copied, and the destination directory and runner readiness are not checked. " +
			"History, model settings, and the runner environment profile are preserved. The source and destination are displayed before confirmation.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(2)(cmd, args); err != nil {
				return err
			}
			if strings.TrimSpace(args[0]) == "" {
				return errors.New("conversation ID is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			runnerID, cwd, err := parseConversationMoveTarget(args[1])
			if err != nil {
				return err
			}
			noConfirm, _ := cmd.Flags().GetBool("no-confirm")
			server, _ := serverFlagOrConfig(cmd)
			token, _, err := resolveControlPlaneAuthToken(cmd, server)
			if err != nil {
				return err
			}
			client, err := chat.NewClient(server, token, "")
			if err != nil {
				return err
			}
			params := chat.ConversationMoveRequest{RunnerID: runnerID, CWD: cwd}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			preview, err := client.MoveConversation(ctx, args[0], params)
			cancel()
			if err != nil {
				return err
			}
			sourceRunner := preview.SourceRunnerID
			if sourceRunner == "" {
				sourceRunner = "unassigned"
			}
			targetRunner := preview.RunnerID
			if preview.RunnerName != "" {
				targetRunner += " (" + preview.RunnerName + ")"
			}
			profile := preview.EnvironmentProfile
			if profile == "" {
				profile = "default"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Conversation: %s\nFrom runner: %s\nFrom directory: %s\nTo runner: %s\nTo directory: %s\nRunner profile: %s (unchanged)\n", preview.ConversationID, sourceRunner, preview.SourceCWD, targetRunner, preview.CWD, profile)
			fmt.Fprintln(cmd.OutOrStdout(), "Only conversation metadata is changed. No files are copied; the destination directory and runner readiness are not checked.")
			if !noConfirm {
				answer := presenter.Prompt("Move this conversation?", "y", "N")
				if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
					fmt.Fprintln(cmd.OutOrStdout(), "Move cancelled; the conversation is unchanged.")
					return nil
				}
			}
			params.Confirmation = preview.Confirmation
			ctx, cancel = context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			result, err := client.MoveConversation(ctx, args[0], params)
			if err != nil {
				return err
			}
			if !result.Moved {
				return errors.New("could not confirm the runner assignment; check 'kodelet conversation show' before trying again")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Conversation %s moved; its history and model settings are unchanged.\n", result.ConversationID)
			return nil
		},
	}
	cmd.Flags().Bool("no-confirm", false, "Skip confirmation prompt")
	return cmd
}

func parseConversationMoveTarget(target string) (string, string, error) {
	runnerID, cwd, hasCWD := strings.Cut(target, ":")
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return "", "", errors.New("destination runner ID is required")
	}
	if hasCWD && strings.TrimSpace(cwd) == "" {
		return "", "", errors.New("destination directory after ':' must not be empty; omit ':' to keep the saved directory")
	}
	return runnerID, cwd, nil
}
