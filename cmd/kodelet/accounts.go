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

func init() {
	accountsCmd.AddCommand(accountsListCmd)
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
