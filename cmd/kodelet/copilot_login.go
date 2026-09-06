package main

import "github.com/spf13/cobra"

var copilotLoginCmd = &cobra.Command{
	Use:               "copilot-login",
	Short:             "Connect a GitHub Copilot subscription",
	Long:              "Sign in using a code in your browser. Your credentials are stored on the selected server.",
	Args:              cobra.NoArgs,
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE:              func(cmd *cobra.Command, _ []string) error { return runRemoteProviderDeviceLogin(cmd, "copilot") },
}

func init() {
	addRemoteAdministrationFlags(copilotLoginCmd)
	copilotLoginCmd.Flags().Bool("no-browser", false, "Print the sign-in URL without opening a browser")
}
