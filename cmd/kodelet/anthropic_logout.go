package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var anthropicLogoutCmd = &cobra.Command{
	Use:   "anthropic-logout",
	Short: "Logout from Anthropic and remove stored credentials",
	Long: `Logout from Anthropic and remove stored credentials.

This command will:
1. Remove the stored authentication credentials from ~/.kodelet/anthropic-subscription.json
2. You will need to run 'kodelet anthropic-login' again to access subscription-based models

After running this command, you will no longer have access to subscription-based
Anthropic models until you authenticate again.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		noConfirm, _ := cmd.Flags().GetBool("no-confirm")

		if err := runAnthropicLogout(ctx, noConfirm); err != nil {
			logger.G(ctx).WithField("error", err).Error("Failed to complete Anthropic logout")
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	anthropicLogoutCmd.Flags().Bool("no-confirm", false, "Skip confirmation prompt and logout automatically")
}

func runAnthropicLogout(ctx context.Context, noConfirm bool) error {
	// Get the credentials file path
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(err, "failed to get user home directory")
	}

	credentialsPath := filepath.Join(home, ".kodelet", "anthropic-subscription.json")

	// Check if credentials file exists
	if _, err := os.Stat(credentialsPath); os.IsNotExist(err) {
		fmt.Println("No Anthropic credentials found. You are already logged out.")
		return nil
	} else if err != nil {
		return errors.Wrap(err, "failed to check credentials file")
	}

	// Confirm with user (unless --no-confirm is set)
	if !noConfirm && !confirmLogout() {
		fmt.Println("Logout cancelled.")
		return nil
	}

	// Remove the credentials file
	if err := os.Remove(credentialsPath); err != nil {
		return errors.Wrap(err, "failed to remove credentials file")
	}

	// Success message
	fmt.Println("Anthropic Logout")
	fmt.Println("================")
	fmt.Println()
	fmt.Println("Successfully logged out from Anthropic.")
	fmt.Printf("Removed credentials file: %s\n", credentialsPath)
	fmt.Println()
	fmt.Println("You no longer have access to subscription-based Anthropic models.")
	fmt.Println("Run 'kodelet anthropic-login' to authenticate again.")

	return nil
}

// confirmLogout asks the user to confirm the logout
func confirmLogout() bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Are you sure you want to logout from Anthropic? This will remove your stored credentials. (y/N): ")
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	return response == "y" || response == "yes"
}