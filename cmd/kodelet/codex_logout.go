package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

var codexLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout from OpenAI Codex and remove stored credentials",
	Long: `Logout from OpenAI Codex and remove stored credentials.

This command will:
1. Remove the stored authentication credentials from ~/.codex/auth.json
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
	codexLogoutCmd.Flags().Bool("no-confirm", false, "Skip confirmation prompt and logout automatically")
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
	presenter.Info("Removed credentials file: ~/.codex/auth.json")
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
