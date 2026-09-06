package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var conversationTurnCmd = &cobra.Command{
	Use:   "turn <conversation-id> <turn-id>",
	Short: "Query a submitted turn's durable status and result as JSON",
	Args:  cobra.ExactArgs(2),
	RunE:  runConversationTurnCommand,
}

func runConversationTurnCommand(cmd *cobra.Command, args []string) error {
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
	receipt, err := client.GetTurnReceipt(ctx, args[0], args[1])
	if err != nil {
		return errors.Wrap(err, "failed to query daemon turn receipt (no execution retry or local fallback)")
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return errors.Wrap(encoder.Encode(receipt), "failed to write turn receipt")
}
