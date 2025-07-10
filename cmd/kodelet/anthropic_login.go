package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var anthropicLoginCmd = &cobra.Command{
	Use:   "anthropic-login",
	Short: "Login to Anthropic via OAuth to access subscription-based models",
	Long: `Login to Anthropic via OAuth to access subscription-based models.

This command will:
1. Generate a secure authorization URL
2. Automatically open your browser to authenticate with Anthropic
3. Save the authentication credentials to ~/.kodelet/anthropic-subscription.json

The saved credentials will allow you to use subscription-based Anthropic models
that are not available via the standard API key authentication.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		if err := runAnthropicLogin(ctx); err != nil {
			presenter.Error(err, "Failed to complete Anthropic login")
			os.Exit(1)
		}
	},
}

func runAnthropicLogin(ctx context.Context) error {
	// Generate authorization URL
	authURL, verifier, err := auth.GenerateAnthropicAuthURL()
	if err != nil {
		return errors.Wrap(err, "failed to generate authorization URL")
	}

	// Display instructions to user
	presenter.Section("Anthropic OAuth Login")
	presenter.Info("To authenticate with Anthropic and access subscription-based models:")
	fmt.Println()

	// Try to open the browser automatically
	presenter.Info("Opening your browser for authentication...")
	if err := openBrowser(authURL); err != nil {
		presenter.Warning("Could not open browser automatically. Please visit the following URL manually:")
		fmt.Printf("\n   %s\n\n", authURL)
	} else {
		presenter.Info("If your browser didn't open automatically, visit this URL:")
		fmt.Printf("   %s\n\n", authURL)
	}

	presenter.Info("Instructions:")
	fmt.Println("1. Complete the authentication process in your browser")
	fmt.Println("2. After authorization, you'll be redirected to a page")
	fmt.Println("3. Copy the authorization code displayed on that page")
	fmt.Println()

	// Read authorization code from user
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter the authorization code: ")
	code, err := reader.ReadString('\n')
	if err != nil {
		return errors.Wrap(err, "failed to read authorization code")
	}
	code = strings.TrimSpace(code)

	if code == "" {
		return errors.New("authorization code cannot be empty")
	}

	// Exchange code for credentials
	fmt.Println()
	presenter.Info("Exchanging authorization code for access token...")

	creds, err := auth.ExchangeAnthropicCode(ctx, code, verifier)
	if err != nil {
		return errors.Wrap(err, "failed to exchange authorization code for credentials")
	}

	// Save credentials
	credentialsPath, err := auth.SaveAnthropicCredentials(creds)
	if err != nil {
		return errors.Wrap(err, "failed to save credentials")
	}

	// Success message
	fmt.Println()
	presenter.Success("Authentication successful!")
	fmt.Printf("Logged in as: %s\n", creds.Email)
	fmt.Printf("Scopes: %s\n", creds.Scope)
	fmt.Printf("Credentials saved to: %s\n", credentialsPath)
	fmt.Println()
	presenter.Info("You can now use subscription-based Anthropic models with Kodelet.")

	return nil
}

// openBrowser attempts to open the default browser with the given URL
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		return errors.New("unsupported operating system")
	}

	return exec.Command(cmd, args...).Start()
}
