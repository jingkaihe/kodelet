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

var codexLoginUseDeviceAuth bool

var codexLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to OpenAI Codex",
	Long: `Login to OpenAI Codex to access ChatGPT-backed models.

This command will:
1. Start a local OAuth callback server and open your browser, or use device code auth with --device-auth
2. Authenticate with OpenAI using your ChatGPT account
3. Save the authentication credentials to ~/.kodelet/codex-credentials.json

The saved credentials will allow you to use ChatGPT-backed Codex models
that are available through the ChatGPT subscription.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		var err error
		if codexLoginUseDeviceAuth {
			err = runCodexDeviceLogin(ctx)
		} else {
			err = runCodexLogin(ctx)
		}

		if err != nil {
			presenter.Error(err, "Failed to complete Codex login")
			os.Exit(1)
		}
	},
}

func init() {
	codexLoginCmd.Flags().BoolVar(&codexLoginUseDeviceAuth, "device-auth", false, "Use device code authentication instead of the browser redirect flow")
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
	presenter.Info("On a remote or headless machine, use 'kodelet codex login --device-auth' instead.")
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
	fmt.Println("    platform: codex")

	return nil
}

func runCodexDeviceLogin(ctx context.Context) error {
	presenter.Section("OpenAI Codex Device Login")
	presenter.Info("Requesting a one-time device code...")

	deviceCode, err := auth.RequestCodexDeviceCode(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to start device code flow")
	}

	fmt.Println()
	presenter.Info("Follow these steps to sign in with ChatGPT:")
	fmt.Printf("1. Open this URL in your browser: %s\n", deviceCode.VerificationURL)
	fmt.Printf("2. Enter this one-time code: %s\n", deviceCode.UserCode)
	fmt.Println()
	presenter.Warning("Device codes are a common phishing target. Never share this code.")
	fmt.Println()
	presenter.Info("Waiting for authentication to complete (timeout: 15 minutes)...")

	creds, err := auth.CompleteCodexDeviceCodeLogin(ctx, deviceCode)
	if err != nil {
		return errors.Wrap(err, "failed to complete device code login")
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
	fmt.Println("    platform: codex")

	return nil
}
