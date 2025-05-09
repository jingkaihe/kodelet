package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func color(s string) string {
	return fmt.Sprintf("\033[1;%sm%s\033[0m", "33", s)
}

func init() {
	// Environment variables
	viper.SetEnvPrefix("KODELET")
	viper.AutomaticEnv()

	// Config file support
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.kodelet")
	viper.AddConfigPath(".")

	// Load config file if it exists (ignore errors if it doesn't)
	_ = viper.ReadInConfig()

	// Initialize LLM configuration
	llm.InitConfig()
}

func ask(ctx context.Context, state state.State, query string, silent bool) string {
	// Get the configured LLM provider
	provider, err := llm.GetProviderFromConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to initialize LLM provider")
		return "Error: " + err.Error()
	}

	// Initialize conversation with user query
	messages := []llm.Message{
		{
			Role:    "user",
			Content: query,
		},
	}

	// Get the model name for system prompt
	modelName := viper.GetString("model")
	if modelName == "" {
		// Try provider-specific model
		providerName := viper.GetString("provider")
		modelName = viper.GetString(fmt.Sprintf("providers.%s.model", providerName))
	}

	for {
		resp, err := provider.SendMessage(
			ctx,
			messages,
			sysprompt.SystemPrompt(modelName),
			tools.Tools,
		)
		if err != nil {
			logrus.WithError(err).Error("error asking LLM")
			return "Error: " + err.Error()
		}

		// Handle text response
		textOutput := resp.Content
		if !silent && textOutput != "" {
			println(textOutput)
			println()
		}

		// Add assistant response to messages
		if textOutput != "" {
			messages = append(messages, llm.Message{
				Role:    "assistant",
				Content: textOutput,
			})
		}

		// Handle tool calls
		if len(resp.ToolCalls) == 0 {
			return textOutput
		}

		// Process tool calls
		var toolResults []llm.ToolResult
		for _, tc := range resp.ToolCalls {
			print(color("[user (" + tc.Name + ")]: "))

			// Convert parameters to JSON
			paramsJSON, _ := json.Marshal(tc.Parameters)

			println(string(paramsJSON))
			output := tools.RunTool(ctx, state, tc.Name, string(paramsJSON))
			println(output.String())

			toolResults = append(toolResults, llm.ToolResult{
				CallID:  tc.ID,
				Content: output.String(),
				Error:   false,
			})
		}

		// Add tool results to messages
		toolMessage := provider.AddToolResults(toolResults)
		messages = append(messages, toolMessage)
	}
}

var rootCmd = &cobra.Command{
	Use:   "kodelet",
	Short: "Kodelet CLI tool for site reliability engineering tasks",
	Long:  `Kodelet is a lightweight CLI tool that helps with site reliability and platform engineering tasks.`,
	// Default behavior is to show help if no arguments are provided
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 0 {
			// If arguments are provided but no subcommand, forward to run command
			runCmd.Run(cmd, args)
		} else {
			cmd.Help()
			os.Exit(1)
		}
	},
}

func main() {
	// Add global flags
	rootCmd.PersistentFlags().String("provider", "", "LLM provider to use (anthropic or openai)")
	rootCmd.PersistentFlags().String("model", "", "LLM model to use (overrides config)")
	rootCmd.PersistentFlags().Int("max-tokens", 0, "Maximum tokens for response (overrides config)")

	// Bind flags to viper
	viper.BindPFlag("provider", rootCmd.PersistentFlags().Lookup("provider"))
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	viper.BindPFlag("max_tokens", rootCmd.PersistentFlags().Lookup("max-tokens"))

	// Add subcommands
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(commitCmd)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
