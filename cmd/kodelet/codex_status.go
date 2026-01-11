package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

var codexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Codex authentication status",
	Long: `Show the current OpenAI Codex authentication status.

This command checks if valid Codex credentials are available at ~/.codex/auth.json.
These credentials are created by running 'kodelet codex login'.

If credentials are found, you can use Kodelet with the Codex provider to access
ChatGPT-backed models like gpt-5.1-codex-max, gpt-5.2-codex, etc.`,
	Run: func(_ *cobra.Command, _ []string) {
		runCodexStatus()
	},
}

func runCodexStatus() {
	exists, err := auth.GetCodexCredentialsExists()
	if err != nil {
		presenter.Error(err, "Failed to check Codex credentials")
		os.Exit(1)
	}

	if !exists {
		presenter.Warning("Codex credentials not found")
		fmt.Println()
		presenter.Info("To enable Codex authentication:")
		fmt.Println("1. Run 'kodelet codex login' to authenticate with your ChatGPT account")
		fmt.Println("2. Run 'kodelet codex status' again to verify")
		fmt.Println()
		presenter.Info("Once authenticated, add to your config:")
		fmt.Println("  provider: openai")
		fmt.Println("  openai:")
		fmt.Println("    preset: codex")
		return
	}

	creds, err := auth.GetCodexCredentials()
	if err != nil {
		presenter.Error(err, "Failed to read Codex credentials")
		os.Exit(1)
	}

	presenter.Success("Codex credentials found")
	fmt.Println()

	if auth.IsCodexOAuthEnabled(creds) {
		presenter.Info("Authentication type: OAuth (ChatGPT account)")
		fmt.Printf("Account ID: %s\n", maskString(creds.AccountID))

		if creds.ExpiresAt > 0 {
			expiresAt := time.Unix(creds.ExpiresAt, 0)
			now := time.Now()
			if expiresAt.After(now) {
				remaining := expiresAt.Sub(now).Round(time.Minute)
				fmt.Printf("Token expires: %s (in %s)\n", expiresAt.Format(time.RFC3339), remaining)
			} else {
				presenter.Warning("Token has expired")
				if creds.RefreshToken != "" {
					presenter.Info("Token will be automatically refreshed on next use")
				} else {
					presenter.Info("Please run 'kodelet codex login' to re-authenticate")
				}
			}
		}

		if creds.RefreshToken != "" {
			fmt.Println("Refresh token: available")
		}
	} else if creds.APIKey != "" {
		presenter.Info("Authentication type: API Key")
		fmt.Printf("API Key: %s\n", maskString(creds.APIKey))
	}

	fmt.Println()
	presenter.Info("You can now use Kodelet with Codex models. Add to your config:")
	fmt.Println("  provider: openai")
	fmt.Println("  openai:")
	fmt.Println("    preset: codex")
	fmt.Println()
	presenter.Info("Available Codex models:")
	fmt.Println("  - gpt-5.1-codex-max (default)")
	fmt.Println("  - gpt-5.2-codex")
	fmt.Println("  - gpt-5.2")
	fmt.Println("  - gpt-5.1-codex-mini")
}

// maskString masks a string, showing only the first and last 4 characters.
func maskString(s string) string {
	if len(s) <= 12 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
