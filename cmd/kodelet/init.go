package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up Kodelet configuration",
	Long:  `Interactive setup for Kodelet configuration including API key and model preferences.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		reader := bufio.NewReader(os.Stdin)

		presenter.Section("🚀 Kodelet Configuration Setup")
		presenter.Info("This wizard will help you set up Kodelet with the recommended configuration.")
		presenter.Separator()

		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		needsAPIKeySetup := false

		var shellName string
		var profilePath string
		var apiKeyAddedToProfile bool

		if apiKey == "" {
			fmt.Print("🔑 Enter your Anthropic API key: ")
			apiKeyBytes, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				fmt.Println()
				presenter.Error(err, "Failed to read API key")
				return
			}
			apiKey = strings.TrimSpace(string(apiKeyBytes))
			fmt.Println()

			if apiKey != "" {
				presenter.Success("API key received")
				needsAPIKeySetup = true
			} else {
				presenter.Warning("No API key provided. You will need to set ANTHROPIC_API_KEY environment variable to use Kodelet")
			}
		} else {
			presenter.Success("Found Anthropic API key in environment variables")
		}

		if needsAPIKeySetup && apiKey != "" {
			shellName, profilePath = detectShell(ctx)

			if checkAPIKeyInProfile(ctx, profilePath) {
				presenter.Info("🔑 API key is already configured in your shell profile")
			} else {
				if askForPermission(reader, profilePath) {
					err := writeAPIKeyToProfile(ctx, profilePath, shellName, apiKey)
					if err != nil {
						presenter.Error(err, "Failed to update shell profile")
						presenter.Info("📝 To manually set your API key, add the following to your shell profile:")

						if shellName == "fish" {
							presenter.Info(fmt.Sprintf("   set -x ANTHROPIC_API_KEY \"%s\"", apiKey))
						} else {
							presenter.Info(fmt.Sprintf("   export ANTHROPIC_API_KEY=\"%s\"", apiKey))
						}
					} else {
						presenter.Success(fmt.Sprintf("API key added to %s", profilePath))
						apiKeyAddedToProfile = true
					}
				} else {
					presenter.Info("📝 To manually set your API key, add the following to your shell profile:")

					if shellName == "fish" {
						presenter.Info(fmt.Sprintf("   set -x ANTHROPIC_API_KEY \"%s\"", apiKey))
					} else {
						presenter.Info(fmt.Sprintf("   export ANTHROPIC_API_KEY=\"%s\"", apiKey))
					}
				}
			}
		}

		presenter.Section("📋 Model Configuration")

		defaultModel := viper.GetString("model")
		if defaultModel == "" {
			defaultModel = string(anthropic.ModelClaudeSonnet4_20250514)
		}

		fmt.Printf("   Primary model [%s]: ", defaultModel)
		modelInput, _ := reader.ReadString('\n')
		modelInput = strings.TrimSpace(modelInput)

		if modelInput != "" {
			defaultModel = modelInput
		}

		defaultWeakModel := viper.GetString("weak_model")
		if defaultWeakModel == "" {
			defaultWeakModel = string(anthropic.ModelClaude3_5Haiku20241022)
		}

		fmt.Printf("   Secondary (weak) model [%s]: ", defaultWeakModel)
		weakModelInput, _ := reader.ReadString('\n')
		weakModelInput = strings.TrimSpace(weakModelInput)

		if weakModelInput != "" {
			defaultWeakModel = weakModelInput
		}

		defaultMaxTokens := viper.GetInt("max_tokens")
		if defaultMaxTokens == 0 {
			defaultMaxTokens = 8192
		}

		fmt.Printf("   Maximum output tokens [%d]: ", defaultMaxTokens)
		maxTokensInput, _ := reader.ReadString('\n')
		maxTokensInput = strings.TrimSpace(maxTokensInput)

		if maxTokensInput != "" {
			maxTokens, err := strconv.Atoi(maxTokensInput)
			if err == nil {
				defaultMaxTokens = maxTokens
			}
		}

		defaultWeakModelMaxTokens := viper.GetInt("weak_model_max_tokens")
		if defaultWeakModelMaxTokens == 0 {
			defaultWeakModelMaxTokens = 8192
		}

		fmt.Printf("   Maximum weak model output tokens [%d]: ", defaultWeakModelMaxTokens)
		weakModelMaxTokensInput, _ := reader.ReadString('\n')
		weakModelMaxTokensInput = strings.TrimSpace(weakModelMaxTokensInput)

		if weakModelMaxTokensInput != "" {
			weakModelMaxTokens, err := strconv.Atoi(weakModelMaxTokensInput)
			if err == nil {
				defaultWeakModelMaxTokens = weakModelMaxTokens
			}
		}

		defaultThinkingBudgetTokens := viper.GetInt("thinking_budget_tokens")
		if defaultThinkingBudgetTokens == 0 {
			defaultThinkingBudgetTokens = 4048
		}

		fmt.Printf("   Maximum thinking tokens [%d]: ", defaultThinkingBudgetTokens)
		thinkingBudgetTokensInput, _ := reader.ReadString('\n')
		thinkingBudgetTokensInput = strings.TrimSpace(thinkingBudgetTokensInput)

		if thinkingBudgetTokensInput != "" {
			thinkingBudgetTokens, err := strconv.Atoi(thinkingBudgetTokensInput)
			if err == nil {
				defaultThinkingBudgetTokens = thinkingBudgetTokens
			}
		}

		logger.G(ctx).WithFields(map[string]interface{}{
			"max_tokens":             defaultMaxTokens,
			"weak_model_max_tokens":  defaultWeakModelMaxTokens,
			"thinking_budget_tokens": defaultThinkingBudgetTokens,
		}).Debug("Token limits configured")

		configDir := filepath.Join(os.Getenv("HOME"), ".kodelet")
		err := os.MkdirAll(configDir, 0755)
		if err != nil {
			presenter.Error(err, "Failed to create config directory")
			logger.G(ctx).WithError(err).WithField("config_dir", configDir).Error("Config directory creation failed")
			return
		}
		logger.G(ctx).WithField("config_dir", configDir).Debug("Config directory created")

		configFile := filepath.Join(configDir, "config.yaml")

		configContent := "# Kodelet Configuration\n\n"
		configContent += "# Primary model for standard requests\n"
		configContent += fmt.Sprintf("model: \"%s\"\n\n", defaultModel)

		configContent += "# Secondary model for lightweight tasks\n"
		configContent += fmt.Sprintf("weak_model: \"%s\"\n\n", defaultWeakModel)

		configContent += "# Maximum output tokens\n"
		configContent += fmt.Sprintf("max_tokens: %d\n\n", defaultMaxTokens)

		configContent += "# Maximum output tokens for weak model\n"
		configContent += fmt.Sprintf("weak_model_max_tokens: %d\n\n", defaultWeakModelMaxTokens)

		configContent += "# Maximum thinking tokens\n"
		configContent += fmt.Sprintf("thinking_budget_tokens: %d\n", defaultThinkingBudgetTokens)

		err = os.WriteFile(configFile, []byte(configContent), 0644)
		if err != nil {
			presenter.Error(err, "Failed to write config file")
			logger.G(ctx).WithError(err).WithField("config_file", configFile).Error("Config file write failed")
			return
		}

		presenter.Success(fmt.Sprintf("Configuration saved to %s", configFile))
		logger.G(ctx).WithField("config_file", configFile).Info("Configuration file created successfully")
		presenter.Separator()
		presenter.Success("🎉 Kodelet setup complete! You can now use Kodelet")
		logger.G(ctx).Info("Kodelet initialization completed successfully")

		if needsAPIKeySetup && apiKey != "" && apiKeyAddedToProfile {
			presenter.Separator()
			presenter.Warning("⚠️  IMPORTANT ACTION REQUIRED ⚠️")
			presenter.Info("To activate your API key, you must restart your terminal")
			presenter.Info("or run the following command:")
			presenter.Info(fmt.Sprintf("   $ source %s", profilePath))
			presenter.Separator()
		}

		presenter.Section("Example Commands")
		presenter.Info("  kodelet chat                  # Start an interactive chat session")
		presenter.Info("  kodelet run \"your query\"      # Run a one-shot query")
		presenter.Info("  kodelet watch                 # Start watch mode to monitor file changes")
		presenter.Info("  kodelet --help                # Show available commands")
	},
}

func detectShell(ctx context.Context) (string, string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		logger.G(ctx).Warn("SHELL environment variable not set, defaulting to bash")
		return "bash", filepath.Join(os.Getenv("HOME"), ".bashrc")
	}

	shellName := filepath.Base(shell)
	logger.G(ctx).WithField("detected_shell", shellName).Debug("Shell detected from SHELL environment variable")

	switch shellName {
	case "bash":
		bashProfile := filepath.Join(os.Getenv("HOME"), ".bash_profile")
		if _, err := os.Stat(bashProfile); err == nil {
			logger.G(ctx).WithField("profile_file", bashProfile).Debug("Using .bash_profile for bash shell")
			return "bash", bashProfile
		}
		bashrc := filepath.Join(os.Getenv("HOME"), ".bashrc")
		logger.G(ctx).WithField("profile_file", bashrc).Debug("Using .bashrc for bash shell")
		return "bash", bashrc
	case "zsh":
		zshrc := filepath.Join(os.Getenv("HOME"), ".zshrc")
		logger.G(ctx).WithField("profile_file", zshrc).Debug("Using .zshrc for zsh shell")
		return "zsh", zshrc
	case "fish":
		// Fish uses a different directory structure
		fishConfig := filepath.Join(os.Getenv("HOME"), ".config/fish/config.fish")
		logger.G(ctx).WithField("profile_file", fishConfig).Debug("Using config.fish for fish shell")
		return "fish", fishConfig
	default:
		return shellName, filepath.Join(os.Getenv("HOME"), ".profile")
	}
}

func checkAPIKeyInProfile(_ context.Context, profilePath string) bool {
	content, err := os.ReadFile(profilePath)
	if err != nil {
		return false
	}

	return strings.Contains(string(content), "export ANTHROPIC_API_KEY=") ||
		strings.Contains(string(content), "set -x ANTHROPIC_API_KEY")
}

func writeAPIKeyToProfile(_ context.Context, profilePath, shellName, apiKey string) error {
	file, err := os.OpenFile(profilePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString("\n# Added by Kodelet\n")
	if err != nil {
		return err
	}

	switch shellName {
	case "fish":
		_, err = fmt.Fprintf(file, "set -x ANTHROPIC_API_KEY \"%s\"\n", apiKey)
	default:
		_, err = fmt.Fprintf(file, "export ANTHROPIC_API_KEY=\"%s\"\n", apiKey)
	}

	return err
}

func askForPermission(reader *bufio.Reader, profilePath string) bool {
	fmt.Printf("📝 Would you like to add your API key to %s? [Y/n] ", profilePath)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes" || response == ""
}

func init() {
}
