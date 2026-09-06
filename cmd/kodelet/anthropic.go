package main

import (
	"github.com/spf13/cobra"
)

var anthropicCmd = &cobra.Command{
	Use:               "anthropic",
	Short:             "Manage the daemon's Anthropic authentication and accounts",
	Long:              `Manage Anthropic subscription accounts on the selected daemon. Requires daemon administrator access; provider credentials never reside on this client.`,
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	addRemoteAdministrationFlags(anthropicCmd)
	anthropicCmd.AddCommand(anthropicLoginCmd)
	anthropicCmd.AddCommand(anthropicLogoutCmd)
	anthropicCmd.AddCommand(anthropicAccountsCmd)
}
