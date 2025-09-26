package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up Kodelet configuration",
	Long:  `Set up Kodelet configuration with sensible defaults.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		override, _ := cmd.Flags().GetBool("override")

		presenter.Section("Kodelet Configuration Setup")
		presenter.Info("Setting up Kodelet with recommended defaults.")
		presenter.Separator()

		// Check for API keys and inform about requirements
		presenter.Section("API Key Requirements")

		anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
		openaiKey := os.Getenv("OPENAI_API_KEY")
		xaiKey := os.Getenv("XAI_API_KEY")
		googleKey := os.Getenv("GOOGLE_API_KEY")

		if anthropicKey != "" {
			presenter.Success("Found ANTHROPIC_API_KEY in environment")
		} else {
			presenter.Info("You will need ANTHROPIC_API_KEY environment variable set to use Claude models")
		}

		if openaiKey != "" {
			presenter.Success("Found OPENAI_API_KEY in environment")
		} else {
			presenter.Info("You will need OPENAI_API_KEY environment variable set to use OpenAI models")
		}

		if xaiKey != "" {
			presenter.Success("Found XAI_API_KEY in environment")
		} else {
			presenter.Info("You will need XAI_API_KEY environment variable set to use X.AI Grok models")
		}

		if googleKey != "" {
			presenter.Success("Found GOOGLE_API_KEY in environment")
		} else {
			presenter.Info("You will need GOOGLE_API_KEY environment variable set to use Gemini models")
		}

		presenter.Separator()

		// Create config directory
		configDir := filepath.Join(os.Getenv("HOME"), ".kodelet")
		err := os.MkdirAll(configDir, 0755)
		if err != nil {
			presenter.Error(err, "Failed to create config directory")
			logger.G(ctx).WithError(err).WithField("config_dir", configDir).Error("Config directory creation failed")
			return
		}
		logger.G(ctx).WithField("config_dir", configDir).Debug("Config directory created")

		configFile := filepath.Join(configDir, "config.yaml")

		// Check if config already exists (unless override is specified)
		if !override {
			if _, err := os.Stat(configFile); err == nil {
				presenter.Warning(fmt.Sprintf("Configuration file already exists at %s", configFile))
				presenter.Info("To overwrite, use the --override flag or remove the file and run 'kodelet init' again")
				return
			}
		}

		// Create config with the excellent defaults
		configContent := `aliases:
    gemini-flash: gemini-2.5-flash
    gemini-pro: gemini-2.5-pro
    haiku-35: claude-3-5-haiku-20241022
    opus-41: claude-opus-4-1-20250805
    sonnet-4: claude-sonnet-4-20250514
max_tokens: 16000
model: sonnet-4
profile: default
thinking_budget_tokens: 8000
weak_model: haiku-35
weak_model_max_tokens: 8192
profiles:
    hybrid:
        max_tokens: 16000
        model: sonnet-4
        subagent:
            allowed_tools:
                - file_read
                - glob_tool
                - grep_tool
            model: o3
            provider: openai
            reasoning_effort: high
        thinking_budget_tokens: 8000
        weak_model: haiku-35
        weak_model_max_tokens: 8192
    openai:
        max_tokens: 16000
        model: gpt-5
        provider: openai
        reasoning_effort: medium
        weak_model: gpt-5
    premium:
        max_tokens: 16000
        model: opus-41
        thinking_budget_tokens: 8000
        weak_model: sonnet-4
        weak_model_max_tokens: 8192
    google:
        max_tokens: 16000
        model: gemini-pro
        provider: google
        weak_model: gemini-flash
        weak_model_max_tokens: 8192
    xai:
        max_tokens: 16000
        model: grok-code-fast-1
        openai:
            preset: xai
        provider: openai
        reasoning_effort: none
        weak_model: grok-code-fast-1
`

		err = os.WriteFile(configFile, []byte(configContent), 0644)
		if err != nil {
			presenter.Error(err, "Failed to write config file")
			logger.G(ctx).WithError(err).WithField("config_file", configFile).Error("Config file write failed")
			return
		}

		if override {
			presenter.Success(fmt.Sprintf("Configuration overwritten at %s", configFile))
		} else {
			presenter.Success(fmt.Sprintf("Configuration saved to %s", configFile))
		}
		presenter.Info("You can modify these settings at any time by editing the config file")
		presenter.Info("Use different profiles with: --profile hybrid|openai|premium|google|xai")
		logger.G(ctx).WithField("config_file", configFile).Info("Configuration file created successfully")

		presenter.Separator()
		presenter.Section("Setup Complete")
		presenter.Success("Kodelet has been configured with sensible defaults")

		// Only show setup instructions if no API keys are found
		if anthropicKey == "" && openaiKey == "" && xaiKey == "" && googleKey == "" {
			presenter.Separator()
			presenter.Warning("No API keys found. Please set at least one of the following environment variables:")
			presenter.Info("  export ANTHROPIC_API_KEY=\"your-key-here\"  # For Claude models")
			presenter.Info("  export OPENAI_API_KEY=\"your-key-here\"     # For OpenAI models")
			presenter.Info("  export XAI_API_KEY=\"your-key-here\"        # For X.AI Grok models")
			presenter.Info("  export GOOGLE_API_KEY=\"your-key-here\"     # For Gemini models")
		}

		presenter.Separator()
		presenter.Section("Getting Started")
		presenter.Info("  kodelet chat                          # Start interactive chat (default profile)")
		presenter.Info("  kodelet run \"your query\"              # Run one-shot query")
		presenter.Info("  kodelet run --profile hybrid \"query\"  # Use hybrid profile (Claude + OpenAI subagent)")
		presenter.Info("  kodelet watch                         # Monitor file changes")
		presenter.Info("  kodelet serve                         # Start web UI server")
		presenter.Info("  kodelet --help                        # Show all available commands")

		logger.G(ctx).Info("Kodelet initialization completed successfully")
	},
}

func init() {
	initCmd.Flags().Bool("override", false, "Overwrite existing configuration file if it exists")
}
