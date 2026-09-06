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
	Short:             "Show how to disconnect GitHub Copilot",
	Args:              cobra.NoArgs,
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(*cobra.Command, []string) error {
		return errors.New("to disconnect GitHub Copilot, stop the server, run 'kodelet host copilot-logout' on that machine, then restart 'kodelet serve'")
	},
}

var hostCopilotLogoutCmd = &cobra.Command{
	Use:   "copilot-logout",
	Short: "Remove this host's Copilot credentials (server must be stopped)",
	Long: `Logout from GitHub Copilot and remove stored credentials.

This command will:
1. Remove the stored authentication credentials from ~/.kodelet/copilot-subscription.json
2. You will need to run 'kodelet copilot-login' again to access subscription-based models

After running this command, you will no longer have access to GitHub Copilot
subscription-based models until you authenticate again.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		noConfirm, _ := cmd.Flags().GetBool("no-confirm")

		if err := runCopilotLogout(ctx, noConfirm); err != nil {
			presenter.Error(err, "Failed to complete GitHub Copilot logout")
			os.Exit(1)
		}
	},
}

func init() {
	addRemoteAdministrationFlags(copilotLogoutCmd)
	copilotLogoutCmd.Flags().Bool("no-confirm", false, "Legacy flag; remote logout is not supported")
	hostCopilotLogoutCmd.Flags().Bool("no-confirm", false, "Explicitly approve deleting credentials on this host")
	hostCmd.AddCommand(hostCopilotLogoutCmd)
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
