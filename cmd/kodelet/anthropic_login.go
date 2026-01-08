package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var anthropicLoginAlias string

var anthropicLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Anthropic via OAuth to access subscription-based models",
	Long: `Login to Anthropic via OAuth to access subscription-based models.

This command will:
1. Generate a secure authorization URL
2. Automatically open your browser to authenticate with Anthropic
3. Save the authentication credentials to ~/.kodelet/anthropic-credentials.json

The saved credentials will allow you to use subscription-based Anthropic models
that are not available via the standard API key authentication.

You can use --alias to name the account for easy reference when using multiple accounts.
If no alias is provided, the email prefix will be used as the alias.
The first account logged in will automatically become the default.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		if err := runAnthropicLogin(ctx, anthropicLoginAlias); err != nil {
			presenter.Error(err, "Failed to complete Anthropic login")
			os.Exit(1)
		}
	},
}

func init() {
	anthropicLoginCmd.Flags().StringVar(&anthropicLoginAlias, "alias", "", "Alias for this account (default: email prefix)")
}

func runAnthropicLogin(ctx context.Context, alias string) error {
	authURL, verifier, err := auth.GenerateAnthropicAuthURL()
	if err != nil {
		return errors.Wrap(err, "failed to generate authorization URL")
	}

	presenter.Section("Anthropic OAuth Login")
	presenter.Info("To authenticate with Anthropic and access subscription-based models:")
	fmt.Println()

	presenter.Info("Opening your browser for authentication...")
	if err := osutil.OpenBrowser(authURL); err != nil {
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

	fmt.Println()
	presenter.Info("Exchanging authorization code for access token...")

	creds, err := auth.ExchangeAnthropicCode(ctx, code, verifier)
	if err != nil {
		return errors.Wrap(err, "failed to exchange authorization code for credentials")
	}

	// Determine the alias to use: provided alias, or email prefix
	effectiveAlias := alias
	if effectiveAlias == "" {
		effectiveAlias = auth.GenerateAliasFromEmail(creds.Email)
	}

	credentialsPath, err := auth.SaveAnthropicCredentialsWithAlias(effectiveAlias, creds)
	if err != nil {
		return errors.Wrap(err, "failed to save credentials")
	}

	// Check if this account became the default
	defaultAlias, _ := auth.GetDefaultAnthropicAccount()
	isDefault := defaultAlias == effectiveAlias

	fmt.Println()
	presenter.Success("Authentication successful!")
	fmt.Printf("Logged in as: %s\n", creds.Email)
	fmt.Printf("Account alias: %s\n", effectiveAlias)
	if isDefault {
		fmt.Printf("Default account: yes (first account or set as default)\n")
	}
	fmt.Printf("Scopes: %s\n", creds.Scope)
	fmt.Printf("Credentials saved to: %s\n", credentialsPath)
	fmt.Println()
	presenter.Info("You can now use subscription-based Anthropic models with Kodelet.")
	if !isDefault {
		presenter.Info(fmt.Sprintf("To use this account, run: kodelet run --account %s \"your query\"", effectiveAlias))
	}

	return nil
}
