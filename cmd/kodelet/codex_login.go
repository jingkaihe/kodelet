package main

import "github.com/spf13/cobra"

var codexLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Connect a ChatGPT subscription to the daemon",
	Long:  "Start daemon-owned device-code sign-in. Credentials are exchanged and stored only by the daemon; no client callback server is started.",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, _ []string) error { return runRemoteProviderDeviceLogin(cmd, "codex") },
}

func init() {
	codexLoginCmd.Flags().Bool("device-auth", true, "Compatibility flag; daemon login always uses device authentication")
	codexLoginCmd.Flags().Bool("no-browser", false, "Print the sign-in URL without opening a browser")
}
