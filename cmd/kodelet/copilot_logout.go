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

var copilotLogoutCmd = &cobra.Command{
	Use:               "copilot-logout",
	Short:             "Disconnect GitHub Copilot",
	Long:              "Remove saved sign-in details with --local on the server machine while the server is stopped. Restart the server afterward to apply the change.",
	Args:              cobra.NoArgs,
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, _ []string) error {
		local, _ := cmd.Flags().GetBool("local")
		if !local {
			return errors.New("to disconnect GitHub Copilot, stop the server, run 'kodelet copilot-logout --local' on that machine, then restart 'kodelet serve'")
		}
		if err := validateLocalAdministrationFlags(cmd); err != nil {
			return err
		}
		noConfirm, _ := cmd.Flags().GetBool("no-confirm")
		return runCopilotLogout(cmd.Context(), noConfirm)
	},
}

func init() {
	addRemoteAdministrationFlags(copilotLogoutCmd)
	copilotLogoutCmd.Flags().Bool("local", false, "Remove sign-in details stored on this machine (stop the server first)")
	copilotLogoutCmd.Flags().Bool("no-confirm", false, "Skip the confirmation prompt")
}

func runCopilotLogout(_ context.Context, noConfirm bool) error {
	exists, err := auth.GetCopilotCredentialsExists()
	if err != nil {
		return errors.Wrap(err, "failed to check credentials file")
	}

	if !exists {
		presenter.Info("No GitHub Copilot credentials found. You are already logged out.")
		return nil
	}

	if !noConfirm && !confirmCopilotLogout() {
		presenter.Info("Logout cancelled.")
		return nil
	}

	if err := auth.DeleteCopilotCredentials(); err != nil {
		return errors.Wrap(err, "failed to remove credentials file")
	}

	presenter.Section("GitHub Copilot Logout")
	presenter.Success("Successfully logged out from GitHub Copilot.")
	presenter.Info("Removed credentials file: ~/.kodelet/copilot-subscription.json")
	presenter.Info("You no longer have access to GitHub Copilot subscription-based models.")
	presenter.Info("Run 'kodelet copilot-login' to authenticate again.")

	return nil
}

func confirmCopilotLogout() bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Are you sure you want to logout from GitHub Copilot? This will remove your stored credentials. (y/N): ")
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	return response == "y" || response == "yes"
}
