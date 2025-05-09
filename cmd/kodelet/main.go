package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
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
	// Set default configuration values
	viper.SetDefault("max_tokens", 8192)
	viper.SetDefault("model", anthropic.ModelClaude3_7SonnetLatest)

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
}

func ask(ctx context.Context, state state.State, query string, silent bool) string {
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(query)),
	}

	client := anthropic.NewClient()
	for {
		model := viper.GetString("model")
		message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: int64(viper.GetInt("max_tokens")),
			System: []anthropic.TextBlockParam{
				{
					Text:         sysprompt.SystemPrompt(model),
					CacheControl: anthropic.CacheControlEphemeralParam{},
				},
			},
			Messages: messages,
			Model:    model,
			Tools:    tools.ToAnthropicTools(tools.Tools),
		})
		if err != nil {
			logrus.WithError(err).Error("error asking")
		}

		textOutput := ""
		for _, block := range message.Content {
			switch block := block.AsAny().(type) {
			case anthropic.TextBlock:
				textOutput += block.Text + "\n"
				if !silent {
					println(block.Text)
					println()
				}
			case anthropic.ToolUseBlock:
				inputJSON, _ := json.Marshal(block.Input)
				if !silent {
					println(block.Name + ": " + string(inputJSON))
					println()
				}
			}
		}

		messages = append(messages, message.ToParam())
		toolResults := []anthropic.ContentBlockParamUnion{}

		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				print(color("[user (" + block.Name + ")]: "))

				output := tools.RunTool(ctx, state, block.Name, string(variant.JSON.Input.Raw()))
				println(output.String())

				toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, output.String(), false))
			}

		}
		if len(toolResults) == 0 {
			return textOutput

		}
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
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
	rootCmd.PersistentFlags().String("model", "", "Anthropic model to use (overrides config)")
	rootCmd.PersistentFlags().Int("max-tokens", 0, "Maximum tokens for response (overrides config)")

	// Bind flags to viper
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	viper.BindPFlag("max_tokens", rootCmd.PersistentFlags().Lookup("max-tokens"))

	// Add subcommands
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(commitCmd)
	rootCmd.AddCommand(watchCmd)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
