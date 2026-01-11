package main

import "github.com/spf13/cobra"

var codexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Manage Codex CLI authentication",
	Long: `Manage Codex CLI authentication for accessing ChatGPT-backed Codex models.

Kodelet can use credentials from the official Codex CLI to access the ChatGPT
backend API. This enables access to Codex-optimized models like gpt-5.1-codex-max.

To use Codex authentication:
1. Install the Codex CLI: https://github.com/openai/codex
2. Run 'codex login' to authenticate with your ChatGPT account
3. Use 'kodelet codex status' to verify the credentials are detected
4. Run kodelet with --provider=codex or KODELET_PROVIDER=codex`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	codexCmd.AddCommand(codexStatusCmd)
	rootCmd.AddCommand(codexCmd)
}
