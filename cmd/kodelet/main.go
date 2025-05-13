package main

import (
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	// Set default configuration values
	viper.SetDefault("max_tokens", 8192)
	viper.SetDefault("model", anthropic.ModelClaude3_7SonnetLatest)
	viper.SetDefault("weak_model", anthropic.ModelClaude3_5HaikuLatest)
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

var rootCmd = &cobra.Command{
	Use:   "kodelet",
	Short: "Kodelet is a CLI tool for software engineering and production operations tasks",
	Long:  `Kodelet is a lightweight CLI tool that helps with software engineering and production operations tasks.`,
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
	rootCmd.PersistentFlags().String("model", anthropic.ModelClaude3_7SonnetLatest, "Anthropic model to use (overrides config)")
	rootCmd.PersistentFlags().Int("max-tokens", 8192, "Maximum tokens for response (overrides config)")
	rootCmd.PersistentFlags().String("weak-model", anthropic.ModelClaude3_5HaikuLatest, "Weak model to use (overrides config)")

	// Bind flags to viper
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	viper.BindPFlag("max_tokens", rootCmd.PersistentFlags().Lookup("max-tokens"))
	viper.BindPFlag("weak_model", rootCmd.PersistentFlags().Lookup("weak-model"))
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
