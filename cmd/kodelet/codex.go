package main

import "github.com/spf13/cobra"

var codexCmd = &cobra.Command{
	Use:               "codex",
	Short:             "Manage your ChatGPT subscription connection",
	Long:              "Connect a ChatGPT subscription and check its status on the selected server. Requires administrator access. Configure model defaults on the server host.",
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	addRemoteAdministrationFlags(codexCmd)
	codexCmd.AddCommand(codexLoginCmd)
	codexCmd.AddCommand(codexLogoutCmd)
	codexCmd.AddCommand(codexStatusCmd)
	rootCmd.AddCommand(codexCmd)
}

var hostCodexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Administer Codex credential files on this host",
	Long:  "Manage credentials stored on this machine. Stop the server before deleting its credentials. To change a remote server's credentials, run these commands on that machine.",
	RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}
