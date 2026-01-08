package main

import (
	"github.com/spf13/cobra"
)

var anthropicCmd = &cobra.Command{
	Use:   "anthropic",
	Short: "Manage Anthropic authentication and accounts",
	Long:  `Commands for managing Anthropic OAuth authentication and subscription accounts.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	anthropicCmd.AddCommand(anthropicLoginCmd)
	anthropicCmd.AddCommand(anthropicLogoutCmd)
	anthropicCmd.AddCommand(anthropicAccountsCmd)
}
