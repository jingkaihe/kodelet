package main

import "github.com/spf13/cobra"

var codexCmd = &cobra.Command{
	Use:               "codex",
	Short:             "Manage the daemon's ChatGPT subscription connection",
	Long:              "Sign in with device authentication and inspect the selected daemon's connection. Requires daemon administrator access. Configure model defaults on the daemon host.",
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
	Long:  "Host-only operator commands. Stop the daemon before deleting credential files; these commands do not target --server or KODELET_SERVER.",
	RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}
