package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const copilotConfigSuggestion = `provider: "openai"
use_copilot: true
model: "gpt-4.1"
weak_model: "gpt-4.1"
max_tokens: 16000`

var copilotLoginCmd = &cobra.Command{
	Use:   "copilot-login",
	Short: "Login to GitHub Copilot via OAuth to access subscription-based models",
	Long: `Login to GitHub Copilot via OAuth to access subscription-based models.

This command will:
1. Generate a device authorization code
2. Automatically open your browser to authenticate with GitHub
3. Exchange the OAuth token for a GitHub Copilot-specific token
4. Save the authentication credentials to ~/.kodelet/copilot-subscription.json

The saved credentials will allow you to use GitHub Copilot subscription-based models
through Kodelet.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		if err := runCopilotLogin(ctx); err != nil {
			presenter.Error(err, "Failed to complete GitHub Copilot login")
			os.Exit(1)
		}
	},
}

func runCopilotLogin(ctx context.Context) error {
	// Generate device flow
	presenter.Section("GitHub Copilot OAuth Login")
	presenter.Info("Starting GitHub Copilot OAuth device flow...")

	deviceResp, err := auth.GenerateCopilotDeviceFlow(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to start device flow")
	}

	// Display instructions to user
	fmt.Println()
	presenter.Info("To authenticate with GitHub Copilot:")
	fmt.Printf("   1. Open this URL in your browser: %s\n", deviceResp.VerificationURI)
	fmt.Printf("   2. Enter this code when prompted: %s\n", deviceResp.UserCode)
	fmt.Println()

	// Try to open the browser automatically
	presenter.Info("Opening your browser for authentication...")
	if err := utils.OpenBrowser(deviceResp.VerificationURI); err != nil {
		presenter.Warning("Could not open browser automatically. Please visit the URL manually.")
	} else {
		presenter.Info("If your browser didn't open automatically, visit the URL above.")
	}

	presenter.Info("Waiting for authentication to complete...")
	fmt.Println("(You can close this terminal after completing authentication in your browser)")

	// Poll for token with timeout
	pollCtx, cancel := context.WithTimeout(ctx, time.Duration(deviceResp.ExpiresIn)*time.Second)
	defer cancel()

	tokenResp, err := auth.PollCopilotToken(pollCtx, deviceResp.DeviceCode, deviceResp.Interval)
	if err != nil {
		return errors.Wrap(err, "failed to get OAuth access token")
	}

	copilotToken, err := auth.ExchangeCopilotToken(ctx, tokenResp.AccessToken)
	if err != nil {
		return errors.Wrap(err, "failed to exchange token for Copilot access")
	}

	// Create credentials struct
	creds := &auth.CopilotCredentials{
		AccessToken:    tokenResp.AccessToken,
		CopilotToken:   copilotToken.Token,
		Scope:          tokenResp.Scope,
		CopilotExpires: copilotToken.ExpiresAt,
	}

	// Save credentials
	_, err = auth.SaveCopilotCredentials(creds)
	if err != nil {
		return errors.Wrap(err, "failed to save credentials")
	}

	// Success message
	fmt.Println()
	presenter.Success("Authentication successful!")
	fmt.Println()
	presenter.Info("You can now use GitHub Copilot subscription-based models with Kodelet.")
	fmt.Println()
	presenter.Info("To use Copilot features, consider adding the following to your ~/.kodelet/config.yaml:")
	fmt.Println()
	fmt.Println(copilotConfigSuggestion)
	fmt.Println()

	return nil
}
