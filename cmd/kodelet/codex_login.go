package main

import "github.com/spf13/cobra"

var codexLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Connect a ChatGPT subscription",
	Long:  "Sign in using a code in your browser. Your credentials are stored on the selected server.",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, _ []string) error { return runRemoteProviderDeviceLogin(cmd, "codex") },
}

func init() {
	codexLoginCmd.Flags().Bool("device-auth", true, "Accepted for compatibility; sign-in always uses a browser verification code")
	codexLoginCmd.Flags().Bool("no-browser", false, "Print the sign-in URL without opening a browser")
}
