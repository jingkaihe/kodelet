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
  kodelet acp --no-skills

  # Disable workflows
  kodelet acp --no-workflows`,
	RunE: runACP,
}

func init() {
	rootCmd.AddCommand(acpCmd)

	acpCmd.Flags().String("model", "", "LLM model to use")
	acpCmd.Flags().String("provider", "", "LLM provider (anthropic, openai, google)")
	acpCmd.Flags().Int("max-tokens", 0, "Maximum tokens for LLM responses")
	acpCmd.Flags().Bool("no-skills", false, "Disable agentic skills")
	acpCmd.Flags().Bool("no-workflows", false, "Disable subagent workflows")
	acpCmd.Flags().Bool("no-hooks", false, "Disable lifecycle hooks")
	acpCmd.Flags().Int("max-turns", 0, "Maximum number of turns within a single SendMessage call (0 = no limit)")
	acpCmd.Flags().Float64("compact-ratio", 0.8, "Context window utilization ratio to trigger auto-compact (0.0-1.0)")
	acpCmd.Flags().Bool("disable-auto-compact", false, "Disable auto-compact functionality")
}

func runACP(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	logger.SetLogOutput(os.Stderr)
	logger.SetLogLevel(viper.GetString("log_level"))

	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	noSkills, _ := cmd.Flags().GetBool("no-skills")
	noWorkflows, _ := cmd.Flags().GetBool("no-workflows")
	noHooks, _ := cmd.Flags().GetBool("no-hooks")
	maxTurns, _ := cmd.Flags().GetInt("max-turns")
	compactRatio, _ := cmd.Flags().GetFloat64("compact-ratio")
	disableAutoCompact, _ := cmd.Flags().GetBool("disable-auto-compact")

	config := &acp.ServerConfig{
		Provider:           provider,
		Model:              model,
		MaxTokens:          maxTokens,
		NoSkills:           noSkills,
		NoWorkflows:        noWorkflows,
		NoHooks:            noHooks,
		MaxTurns:           maxTurns,
		CompactRatio:       compactRatio,
		DisableAutoCompact: disableAutoCompact,
	}

	server := acp.NewServer(
		acp.WithConfig(config),
		acp.WithContext(ctx),
	)

	return server.Run()
}
