package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var codexLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to OpenAI Codex via OAuth",
	Long: `Login to OpenAI Codex via OAuth to access ChatGPT-backed models.

This command will:
1. Start a local OAuth callback server
2. Open your browser to authenticate with OpenAI
3. Save the authentication credentials to ~/.kodelet/codex-credentials.json

The saved credentials will allow you to use ChatGPT-backed Codex models
that are available through the ChatGPT subscription.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		if err := runCodexLogin(ctx); err != nil {
			presenter.Error(err, "Failed to complete Codex login")
			os.Exit(1)
		}
	},
}

func runCodexLogin(ctx context.Context) error {
	authURL, verifier, state, err := auth.GenerateCodexAuthURL()
	if err != nil {
		return errors.Wrap(err, "failed to generate authorization URL")
	}

	presenter.Section("OpenAI Codex OAuth Login")
	presenter.Info("Starting local OAuth callback server...")

	server, err := auth.StartCodexOAuthServer(state)
	if err != nil {
		return errors.Wrap(err, "failed to start OAuth server")
	}
	defer server.Close()

	presenter.Success("OAuth server started on http://localhost:1455")
	fmt.Println()

	presenter.Info("Opening your browser for authentication...")
	if err := osutil.OpenBrowser(authURL); err != nil {
		presenter.Warning("Could not open browser automatically. Please visit the following URL manually:")
		fmt.Printf("\n   %s\n\n", authURL)
	} else {
		presenter.Info("If your browser didn't open automatically, visit this URL:")
		fmt.Printf("   %s\n\n", authURL)
	}

	presenter.Info("Waiting for authentication (timeout: 60 seconds)...")

	code, err := server.WaitForCode(60 * time.Second)
	if err != nil {
		return errors.Wrap(err, "failed to receive authorization code")
	}

	fmt.Println()
	presenter.Info("Exchanging authorization code for access token...")

	creds, err := auth.ExchangeCodexCode(ctx, code, verifier)
	if err != nil {
		return errors.Wrap(err, "failed to exchange authorization code for credentials")
	}

	credentialsPath, err := auth.SaveCodexCredentials(creds)
	if err != nil {
		return errors.Wrap(err, "failed to save credentials")
	}

	fmt.Println()
	presenter.Success("Authentication successful!")
	fmt.Printf("Account ID: %s\n", maskString(creds.AccountID))
	fmt.Printf("Credentials saved to: %s\n", credentialsPath)
	fmt.Println()
	presenter.Info("You can now use Kodelet with Codex models. Add to your config:")
	fmt.Println("  provider: openai")
	fmt.Println("  openai:")
	fmt.Println("    preset: codex")

	return nil
}
