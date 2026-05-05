package main

import "github.com/spf13/cobra"

var codexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Manage OpenAI Codex authentication",
	Long: `Manage OpenAI Codex authentication for accessing ChatGPT-backed Codex models.

Kodelet can authenticate directly with OpenAI to access the ChatGPT backend API.
This enables access to Codex-optimized models like gpt-5.5 and gpt-5.4.

To use Codex authentication:
1. Run 'kodelet codex login' to authenticate with your ChatGPT account
   - Use 'kodelet codex login --device-auth' on remote or headless machines
2. Use 'kodelet codex status' to verify the credentials are detected
3. Configure openai.platform: codex in your config file`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	codexCmd.AddCommand(codexLoginCmd)
	codexCmd.AddCommand(codexLogoutCmd)
	codexCmd.AddCommand(codexStatusCmd)
	rootCmd.AddCommand(codexCmd)
}
