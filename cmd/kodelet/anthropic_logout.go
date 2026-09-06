package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/spf13/cobra"
)

var anthropicLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove all Anthropic accounts stored on the server",
	Long:  "Remove the server's saved Anthropic subscription accounts. This does not revoke provider-issued tokens or interrupt already-running conversations.",
	Args:  cobra.NoArgs,
	RunE:  runRemoteAnthropicLogout,
}

func init() {
	anthropicLogoutCmd.Flags().Bool("no-confirm", false, "Remove all Anthropic accounts without prompting")
}

func runRemoteAnthropicLogout(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := remoteAdministrationClient(cmd)
	if err != nil {
		return err
	}
	noConfirm, _ := cmd.Flags().GetBool("no-confirm")
	if !noConfirm {
		fmt.Fprint(cmd.OutOrStdout(), "Remove all Anthropic accounts on the selected server? (y/N): ")
		answer, err := readProviderInput(ctx, cmd.InOrStdin())
		if err != nil {
			return err
		}
		if answer = strings.ToLower(answer); answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "Logout canceled.")
			return nil
		}
	}
	if err := client.MutateAnthropicAccount(ctx, chat.AnthropicAccountMutation{Action: "logout"}); err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "Anthropic accounts removed.")
	return err
}
