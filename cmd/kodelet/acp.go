package main

import (
	"os"

	"github.com/jingkaihe/kodelet/pkg/acp"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Run kodelet as an ACP agent",
	Long: `Run kodelet as an Agent Client Protocol (ACP) agent.

This mode allows kodelet to be embedded in ACP-compatible clients like
Zed, JetBrains IDEs, or any other ACP client. Communication happens
over stdio using JSON-RPC 2.0.

Example:
  # Launch as subprocess from an IDE
  kodelet acp

  # With custom model
  kodelet acp --model claude-sonnet-4-5-20250929

  # Disable skills
  kodelet acp --no-skills`,
	RunE: runACP,
}

func init() {
	rootCmd.AddCommand(acpCmd)

	acpCmd.Flags().String("model", "", "LLM model to use")
	acpCmd.Flags().String("provider", "", "LLM provider (anthropic, openai, google)")
	acpCmd.Flags().Int("max-tokens", 0, "Maximum tokens for LLM responses")
	acpCmd.Flags().Bool("no-skills", false, "Disable agentic skills")
	acpCmd.Flags().Bool("no-hooks", false, "Disable lifecycle hooks")
}

func runACP(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	logger.SetLogOutput(os.Stderr)
	logger.SetLogLevel(viper.GetString("log_level"))

	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	noSkills, _ := cmd.Flags().GetBool("no-skills")
	noHooks, _ := cmd.Flags().GetBool("no-hooks")

	config := &acp.ServerConfig{
		Provider:  provider,
		Model:     model,
		MaxTokens: maxTokens,
		NoSkills:  noSkills,
		NoHooks:   noHooks,
	}

	server := acp.NewServer(
		acp.WithConfig(config),
		acp.WithContext(ctx),
	)

	return server.Run()
}
