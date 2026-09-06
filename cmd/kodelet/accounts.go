package main

import (
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/spf13/cobra"
)

var anthropicAccountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "Manage Anthropic subscription accounts",
	RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

func init() {
	for _, spec := range []struct {
		use, short string
		args       cobra.PositionalArgs
	}{
		{"list", "List accounts and token status", cobra.NoArgs},
		{"default [alias]", "Show or set the default account", cobra.MaximumNArgs(1)},
		{"remove <alias>", "Remove an account from the server", cobra.ExactArgs(1)},
		{"rename <old-alias> <new-alias>", "Rename an account", cobra.ExactArgs(2)},
	} {
		anthropicAccountsCmd.AddCommand(&cobra.Command{Use: spec.use, Short: spec.short, Args: spec.args, RunE: runRemoteAnthropicAccounts})
	}
}

// tokenRefreshThreshold matches the threshold used in pkg/auth for token refresh decisions.
const tokenRefreshThreshold = 10 * time.Minute

func accountTokenStatus(expiresAt int64) string {
	if expiresAt > time.Now().Add(tokenRefreshThreshold).Unix() {
		return "valid"
	}
	if expiresAt > time.Now().Unix() {
		return "needs refresh"
	}
	return "expired"
}

func runRemoteAnthropicAccounts(cmd *cobra.Command, args []string) error {
	var mutation chat.AnthropicAccountMutation
	if len(args) > 0 {
		mutation = chat.AnthropicAccountMutation{Action: cmd.Name(), Alias: args[0]}
		if len(args) > 1 {
			mutation.NewAlias = args[1]
		}
		if err := mutation.Validate(); err != nil {
			return err
		}
	}
	client, err := remoteAdministrationClient(cmd)
	if err != nil {
		return err
	}
	if mutation.Action != "" {
		if err := client.MutateAnthropicAccount(cmd.Context(), mutation); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Account updated.")
		return err
	}
	result, err := client.AnthropicAccounts(cmd.Context())
	if err != nil {
		return err
	}
	if cmd.Name() == "default" {
		for _, account := range result.Accounts {
			if account.IsDefault {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Default account: %s (%s)\n", account.Alias, account.Email)
				return err
			}
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No default account is set.")
		return err
	}
	if len(result.Accounts) == 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No Anthropic accounts found. Use 'kodelet anthropic login' to add one.")
		return err
	}
	sort.Slice(result.Accounts, func(i, j int) bool { return result.Accounts[i].Alias < result.Accounts[j].Alias })
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ALIAS\tEMAIL\tSTATUS")
	for _, account := range result.Accounts {
		alias := "  " + account.Alias
		if account.IsDefault {
			alias = "* " + account.Alias
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\n", alias, account.Email, accountTokenStatus(account.ExpiresAt))
	}
	return writer.Flush()
}
