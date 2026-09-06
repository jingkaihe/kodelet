package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var codexLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Show how to disconnect a ChatGPT subscription",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return errors.New("to disconnect the ChatGPT subscription, stop the server, run 'kodelet host codex logout' on that machine, then restart 'kodelet serve'")
	},
}

var hostCodexLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove this host's Codex credentials (server must be stopped)",
	Long: `Logout from OpenAI Codex and remove stored credentials.

This command will:
1. Remove the stored authentication credentials from ~/.kodelet/codex-credentials.json
2. You will need to run 'kodelet codex login' again to access ChatGPT-backed models

After running this command, you will no longer have access to ChatGPT-backed
Codex models until you authenticate again.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		noConfirm, _ := cmd.Flags().GetBool("no-confirm")

		if err := runCodexLogout(ctx, noConfirm); err != nil {
			presenter.Error(err, "Failed to complete Codex logout")
			os.Exit(1)
		}
	},
}

func init() {
	codexLogoutCmd.Flags().Bool("no-confirm", false, "Legacy flag; remote logout is not supported")
	hostCodexLogoutCmd.Flags().Bool("no-confirm", false, "Explicitly approve deleting credentials on this host")
	hostCodexCmd.AddCommand(hostCodexLogoutCmd)
	hostCmd.AddCommand(hostCodexCmd)
}

func runCodexLogout(_ context.Context, noConfirm bool) error {
	exists, err := auth.GetCodexCredentialsExists()
	if err != nil {
		return err
	}

	if !exists {
		presenter.Info("No Codex credentials found. You are already logged out.")
		return nil
	}

	if !noConfirm && !confirmCodexLogout() {
		presenter.Info("Logout cancelled.")
		return nil
	}

	if err := auth.DeleteCodexCredentials(); err != nil {
		return err
	}

	presenter.Section("Codex Logout")
	presenter.Success("Successfully logged out from OpenAI Codex.")
	presenter.Info("Removed credentials file: ~/.kodelet/codex-credentials.json")
	presenter.Info("You no longer have access to ChatGPT-backed Codex models.")
	presenter.Info("Run 'kodelet codex login' to authenticate again.")

	return nil
}

func confirmCodexLogout() bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Are you sure you want to logout from OpenAI Codex? This will remove your stored credentials. (y/N): ")
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	return response == "y" || response == "yes"
}
