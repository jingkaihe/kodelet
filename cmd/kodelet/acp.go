package main

import (
	"os"

	"github.com/jingkaihe/kodelet/pkg/acp"
	"github.com/jingkaihe/kodelet/pkg/llm"
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
  kodelet acp --model claude-sonnet-4-6

	# Disable skills
	kodelet acp --no-skills

	# Disable extensions
	kodelet acp --no-extensions

	# Disable workflows
	kodelet acp --no-workflows`,
	RunE: runACP,
}

func init() {
	rootCmd.AddCommand(acpCmd)

	defaults := NewRunConfig()
	acpCmd.Flags().String("model", "", "LLM model to use")
	acpCmd.Flags().String("provider", "", "LLM provider (anthropic, openai)")
	acpCmd.Flags().Int("max-tokens", 0, "Maximum tokens for LLM responses")
	acpCmd.Flags().Bool("no-skills", defaults.NoSkills, "Disable agentic skills")
	acpCmd.Flags().Bool("no-extensions", defaults.NoExtensions, "Disable extension runtime")
	acpCmd.Flags().Bool("no-workflows", false, "Disable subagent workflows") // no RunConfig default — ACP-only flag
	acpCmd.Flags().Bool("enable-fs-search-tools", defaults.EnableFSSearchTools, "Enable filesystem search tools (glob_tool and grep_tool)")
	acpCmd.Flags().Bool("disable-subagent", false, "Disable the subagent tool and remove subagent-related system prompt context")
	acpCmd.Flags().Int("max-turns", defaults.MaxTurns, "Maximum number of agentic turns (0 for no limit)")
}

func runACP(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	config, err := buildACPServerConfig(cmd)
	if err != nil {
		return err
	}

	logger.SetLogOutput(os.Stderr)
	logger.SetLogLevel(viper.GetString("log_level"))

	server := acp.NewServer(
		acp.WithConfig(config),
		acp.WithContext(ctx),
	)

	return server.Run()
}

func buildACPServerConfig(cmd *cobra.Command) (*acp.ServerConfig, error) {
	llmConfig, err := llm.GetConfigFromViperWithCmd(cmd)
	if err != nil {
		return nil, err
	}

	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	noSkills, _ := cmd.Flags().GetBool("no-skills")
	noExtensions, _ := cmd.Flags().GetBool("no-extensions")
	noWorkflows, _ := cmd.Flags().GetBool("no-workflows")
	enableFSSearchTools, _ := cmd.Flags().GetBool("enable-fs-search-tools")
	disableSubagent, _ := cmd.Flags().GetBool("disable-subagent")
	maxTurns, _ := cmd.Flags().GetInt("max-turns")
	maxTurns = max(maxTurns, 0)

	config := &acp.ServerConfig{
		Provider:            provider,
		Model:               model,
		MaxTokens:           maxTokens,
		NoSkills:            noSkills,
		NoExtensions:        noExtensions,
		NoWorkflows:         noWorkflows,
		EnableFSSearchTools: enableFSSearchTools || llmConfig.EnableFSSearchTools,
		DisableSubagent:     disableSubagent || llmConfig.DisableSubagent,
		MaxTurns:            maxTurns,
		CompactRatio:        llmConfig.CompactRatio,
	}

	return config, nil
}
