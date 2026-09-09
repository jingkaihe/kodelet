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
	Use:               "logout",
	Short:             "Disconnect ChatGPT subscription",
	Long:              "Remove saved sign-in details with --local on the server machine while the server is stopped. Restart the server afterward to apply the change.",
	Args:              cobra.NoArgs,
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, _ []string) error {
		local, _ := cmd.Flags().GetBool("local")
		if !local {
			return errors.New("to disconnect ChatGPT subscription, stop the server, run 'kodelet codex logout --local' on that machine, then restart 'kodelet serve'")
		}
		if err := validateLocalAdministrationFlags(cmd); err != nil {
			return err
		}
		noConfirm, _ := cmd.Flags().GetBool("no-confirm")
		return runCodexLogout(cmd.Context(), noConfirm)
	},
}

func init() {
	codexLogoutCmd.Flags().Bool("local", false, "Remove sign-in details stored on this machine (stop the server first)")
	codexLogoutCmd.Flags().Bool("no-confirm", false, "Skip the confirmation prompt")
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
