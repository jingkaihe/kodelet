package main

import (
	"github.com/spf13/cobra"
)

var anthropicCmd = &cobra.Command{
	Use:               "anthropic",
	Short:             "Manage Anthropic sign-in and accounts",
	Long:              `Manage Anthropic subscription accounts stored on the selected server. Requires administrator access.`,
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
