package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

var accountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "Manage Anthropic subscription accounts",
	Long:  `List, manage, and switch between multiple Anthropic subscription accounts.`,
	Run: func(cmd *cobra.Command, _ []string) {
		cmd.Help()
	},
}

var accountsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all logged-in Anthropic accounts",
	Long:  `Display all Anthropic subscription accounts with their aliases, emails, and token status.`,
	Run: func(_ *cobra.Command, _ []string) {
		listAccountsCmd()
	},
}

var accountsRemoveCmd = &cobra.Command{
	Use:   "remove <alias>",
	Short: "Remove an Anthropic account",
	Long: `Remove a specific Anthropic subscription account by its alias.

If the removed account was the default, another account will be automatically
set as the new default (if any accounts remain).`,
	Args: cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		removeAccountCmd(args[0])
	},
}

var accountsDefaultCmd = &cobra.Command{
	Use:   "default [alias]",
	Short: "Show or set the default Anthropic account",
	Long: `Show the current default account when called without arguments,
or set a new default account when an alias is provided.

Examples:
  kodelet accounts default           # Show current default account
  kodelet accounts default work      # Set 'work' as the default account`,
	Args: cobra.MaximumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		if len(args) == 0 {
			showDefaultAccountCmd()
		} else {
			setDefaultAccountCmd(args[0])
		}
	},
}

func init() {
	accountsCmd.AddCommand(accountsListCmd)
	accountsCmd.AddCommand(accountsRemoveCmd)
	accountsCmd.AddCommand(accountsDefaultCmd)
}

// accountTokenStatus returns the status of an account's token based on expiration time.
func accountTokenStatus(expiresAt int64) string {
	now := time.Now().Unix()
	refreshThreshold := time.Now().Add(10 * time.Minute).Unix()

	if expiresAt > refreshThreshold {
		return "valid"
	} else if expiresAt > now {
		return "needs refresh"
	}
	return "expired"
}

func listAccountsCmd() {
	accounts, err := auth.ListAnthropicAccounts()
	if err != nil {
		presenter.Error(err, "Failed to list accounts")
		os.Exit(1)
	}

	if len(accounts) == 0 {
		presenter.Info("No Anthropic accounts found. Use 'kodelet anthropic-login' to add an account.")
		return
	}

	// Sort accounts by alias for consistent display
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].Alias < accounts[j].Alias
	})

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "ALIAS\tEMAIL\tSTATUS")
	fmt.Fprintln(tw, "-----\t-----\t------")

	for _, account := range accounts {
		alias := account.Alias
		if account.IsDefault {
			alias = "* " + alias
		} else {
			alias = "  " + alias
		}

		status := accountTokenStatus(account.ExpiresAt)

		fmt.Fprintf(tw, "%s\t%s\t%s\n", alias, account.Email, status)
	}

	tw.Flush()

	presenter.Info("\n* indicates the default account")
}

func removeAccountCmd(alias string) {
	// Check if the account exists first to provide a better error message
	accounts, err := auth.ListAnthropicAccounts()
	if err != nil {
		presenter.Error(err, "Failed to list accounts")
		os.Exit(1)
	}

	var accountExists bool
	var wasDefault bool
	for _, account := range accounts {
		if account.Alias == alias {
			accountExists = true
			wasDefault = account.IsDefault
			break
		}
	}

	if !accountExists {
		presenter.Error(fmt.Errorf("account '%s' not found", alias), "Failed to remove account")
		os.Exit(1)
	}

	// Remove the account
	if err := auth.RemoveAnthropicAccount(alias); err != nil {
		presenter.Error(err, "Failed to remove account")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Account '%s' removed successfully", alias))

	// Check if there's a new default set
	if wasDefault {
		newDefault, err := auth.GetDefaultAnthropicAccount()
		if err != nil {
			presenter.Info("No accounts remaining. Use 'kodelet anthropic-login' to add a new account.")
		} else {
			presenter.Info(fmt.Sprintf("Default account changed to '%s'", newDefault))
		}
	}
}

func showDefaultAccountCmd() {
	defaultAlias, err := auth.GetDefaultAnthropicAccount()
	if err != nil {
		presenter.Info("No default account set. Use 'kodelet anthropic-login' to add an account.")
		return
	}

	// Get the account details to show email
	accounts, err := auth.ListAnthropicAccounts()
	if err != nil {
		presenter.Error(err, "Failed to list accounts")
		os.Exit(1)
	}

	for _, account := range accounts {
		if account.Alias == defaultAlias {
			presenter.Info(fmt.Sprintf("Default account: %s (%s)", account.Alias, account.Email))
			return
		}
	}

	// Account not found (shouldn't happen normally)
	presenter.Info(fmt.Sprintf("Default account: %s", defaultAlias))
}

func setDefaultAccountCmd(alias string) {
	// First verify the account exists to provide a good error message
	accounts, err := auth.ListAnthropicAccounts()
	if err != nil {
		presenter.Error(err, "Failed to list accounts")
		os.Exit(1)
	}

	var accountExists bool
	for _, account := range accounts {
		if account.Alias == alias {
			accountExists = true
			break
		}
	}

	if !accountExists {
		presenter.Error(fmt.Errorf("account '%s' not found", alias), "Failed to set default account")
		presenter.Info("Use 'kodelet accounts list' to see available accounts.")
		os.Exit(1)
	}

	if err := auth.SetDefaultAnthropicAccount(alias); err != nil {
		presenter.Error(err, "Failed to set default account")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Default account set to '%s'", alias))
}
