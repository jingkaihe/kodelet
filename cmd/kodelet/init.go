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
		logger.G(ctx).WithField("operation", "init").Info("Starting Kodelet configuration setup")

		// Create a reader for user input
		reader := bufio.NewReader(os.Stdin)

		presenter.Section("🚀 Kodelet Configuration Setup")
		presenter.Info("This wizard will help you set up Kodelet with the recommended configuration.")
		presenter.Separator()

		// Check for existing API key
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		needsApiKeySetup := false

		// Variables to track shell profile information
		var shellName string
		var profilePath string
		var apiKeyAddedToProfile bool

		if apiKey == "" {
			fmt.Print("🔑 Enter your Anthropic API key: ")
			apiKeyBytes, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				fmt.Println() // Add newline after password prompt
				presenter.Error(err, "Failed to read API key")
				logger.G(ctx).WithError(err).Error("Password input failed during API key entry")
				return
			}
			apiKey = strings.TrimSpace(string(apiKeyBytes))
			fmt.Println() // Add a newline after the hidden input

			if apiKey != "" {
				presenter.Success("API key received")
				logger.G(ctx).Info("API key entered successfully")
				needsApiKeySetup = true
			} else {
				presenter.Warning("No API key provided. You will need to set ANTHROPIC_API_KEY environment variable to use Kodelet")
				logger.G(ctx).Warn("User did not provide API key")
			}
		} else {
			presenter.Success("Found Anthropic API key in environment variables")
			logger.G(ctx).Info("API key found in environment")
		}

		// Save API key to shell profile if needed
		if needsApiKeySetup && apiKey != "" {
			// Detect shell and profile path
			shellName, profilePath = detectShell(ctx)
			logger.G(ctx).WithFields(map[string]interface{}{
				"shell":        shellName,
				"profile_path": profilePath,
			}).Debug("Detected shell configuration")

			// Check if the API key is already in the profile
			if checkApiKeyInProfile(ctx, profilePath) {
				presenter.Info("🔑 API key is already configured in your shell profile")
			} else {
				// Ask permission to update the shell profile
				if askForPermission(reader, profilePath) {
					err := writeApiKeyToProfile(ctx, profilePath, shellName, apiKey)
					if err != nil {
						presenter.Error(err, "Failed to update shell profile")
						logger.G(ctx).WithError(err).WithField("profile_path", profilePath).Error("Shell profile update failed")
						presenter.Info("📝 To manually set your API key, add the following to your shell profile:")

						// Show the appropriate export command based on shell
						if shellName == "fish" {
							presenter.Info(fmt.Sprintf("   set -x ANTHROPIC_API_KEY \"%s\"", apiKey))
						} else {
							presenter.Info(fmt.Sprintf("   export ANTHROPIC_API_KEY=\"%s\"", apiKey))
						}
					} else {
						presenter.Success(fmt.Sprintf("API key added to %s", profilePath))
						logger.G(ctx).WithField("profile_path", profilePath).Info("API key successfully added to shell profile")
						apiKeyAddedToProfile = true
					}
				} else {
					presenter.Info("📝 To manually set your API key, add the following to your shell profile:")

					// Show the appropriate export command based on shell
					if shellName == "fish" {
						presenter.Info(fmt.Sprintf("   set -x ANTHROPIC_API_KEY \"%s\"", apiKey))
					} else {
						presenter.Info(fmt.Sprintf("   export ANTHROPIC_API_KEY=\"%s\"", apiKey))
					}
				}
			}
		}

		// Configure models and tokens
		presenter.Section("📋 Model Configuration")

		// Model selection
		defaultModel := viper.GetString("model")
		if defaultModel == "" {
			defaultModel = string(anthropic.ModelClaudeSonnet4_0)
		}

		fmt.Printf("   Primary model [%s]: ", defaultModel)
		modelInput, _ := reader.ReadString('\n')
		modelInput = strings.TrimSpace(modelInput)

		if modelInput != "" {
			defaultModel = modelInput
		}
		logger.G(ctx).WithField("primary_model", defaultModel).Debug("Primary model configured")

		// Weak model selection
		defaultWeakModel := viper.GetString("weak_model")
		if defaultWeakModel == "" {
			defaultWeakModel = string(anthropic.ModelClaude3_5HaikuLatest)
		}

		fmt.Printf("   Secondary (weak) model [%s]: ", defaultWeakModel)
		weakModelInput, _ := reader.ReadString('\n')
		weakModelInput = strings.TrimSpace(weakModelInput)

		if weakModelInput != "" {
			defaultWeakModel = weakModelInput
		}
		logger.G(ctx).WithField("weak_model", defaultWeakModel).Debug("Secondary model configured")

		// Max tokens
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

		// Weak model max tokens
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

		// Thinking tokens
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

		// Create config directory if it doesn't exist
		configDir := filepath.Join(os.Getenv("HOME"), ".kodelet")
		err := os.MkdirAll(configDir, 0755)
		if err != nil {
			presenter.Error(err, "Failed to create config directory")
			logger.G(ctx).WithError(err).WithField("config_dir", configDir).Error("Config directory creation failed")
			return
		}
		logger.G(ctx).WithField("config_dir", configDir).Debug("Config directory created")

		// Create a config file
		configFile := filepath.Join(configDir, "config.yaml")

		// Build configuration content
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

		// Write the config file
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

		// If we added an API key to the profile, remind the user to refresh their shell
		if needsApiKeySetup && apiKey != "" && apiKeyAddedToProfile {
			presenter.Separator()
			presenter.Warning("⚠️  IMPORTANT ACTION REQUIRED ⚠️")
			presenter.Info("To activate your API key, you must restart your terminal")
			presenter.Info("or run the following command:")
			presenter.Info(fmt.Sprintf("   $ source %s", profilePath))
			presenter.Separator()
		}

		// Show some example commands
		presenter.Section("Example Commands")
		presenter.Info("  kodelet chat                  # Start an interactive chat session")
		presenter.Info("  kodelet run \"your query\"      # Run a one-shot query")
		presenter.Info("  kodelet watch                 # Start watch mode to monitor file changes")
		presenter.Info("  kodelet --help                # Show available commands")
	},
}

// detectShell determines the user's shell and returns the appropriate profile file path
func detectShell(ctx context.Context) (string, string) {
	// Get the user's shell from the SHELL environment variable
	shell := os.Getenv("SHELL")
	if shell == "" {
		// Default to bash if we can't determine the shell
		logger.G(ctx).Warn("SHELL environment variable not set, defaulting to bash")
		return "bash", filepath.Join(os.Getenv("HOME"), ".bashrc")
	}

	// Extract the shell name from the path
	shellName := filepath.Base(shell)
	logger.G(ctx).WithField("detected_shell", shellName).Debug("Shell detected from SHELL environment variable")

	// Determine the appropriate profile file based on the shell
	switch shellName {
	case "bash":
		// Check if .bash_profile exists, use it for login shells on macOS
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
		// Default to .profile for unknown shells
		profile := filepath.Join(os.Getenv("HOME"), ".profile")
		logger.G(ctx).WithFields(map[string]interface{}{
			"unknown_shell": shellName,
			"profile_file":  profile,
		}).Warn("Unknown shell detected, using .profile")
		return shellName, profile
	}
}

// checkApiKeyInProfile checks if the API key is already set in the profile
func checkApiKeyInProfile(ctx context.Context, profilePath string) bool {
	// Read the profile file
	content, err := os.ReadFile(profilePath)
	if err != nil {
		// If the file doesn't exist or can't be read, assume the key is not there
		logger.G(ctx).WithError(err).WithField("profile_path", profilePath).Debug("Could not read profile file, assuming API key not present")
		return false
	}

	// Check if the API key export is already in the file
	hasAPIKey := strings.Contains(string(content), "export ANTHROPIC_API_KEY=") ||
		strings.Contains(string(content), "set -x ANTHROPIC_API_KEY")

	logger.G(ctx).WithFields(map[string]interface{}{
		"profile_path": profilePath,
		"has_api_key":  hasAPIKey,
	}).Debug("Checked for existing API key in profile")

	return hasAPIKey
}

// writeApiKeyToProfile adds the API key to the shell profile
func writeApiKeyToProfile(ctx context.Context, profilePath, shellName, apiKey string) error {
	logger.G(ctx).WithFields(map[string]interface{}{
		"profile_path": profilePath,
		"shell":        shellName,
	}).Debug("Writing API key to profile")

	// Open the file in append mode
	file, err := os.OpenFile(profilePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		logger.G(ctx).WithError(err).WithField("profile_path", profilePath).Error("Failed to open profile file for writing")
		return err
	}
	defer file.Close()

	// Add a newline for cleanliness
	_, err = file.WriteString("\n# Added by Kodelet\n")
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to write header comment to profile")
		return err
	}

	// Add the export statement based on the shell
	switch shellName {
	case "fish":
		_, err = file.WriteString(fmt.Sprintf("set -x ANTHROPIC_API_KEY \"%s\"\n", apiKey))
	default:
		_, err = file.WriteString(fmt.Sprintf("export ANTHROPIC_API_KEY=\"%s\"\n", apiKey))
	}

	if err != nil {
		logger.G(ctx).WithError(err).WithField("shell", shellName).Error("Failed to write API key export to profile")
	} else {
		logger.G(ctx).WithField("shell", shellName).Info("Successfully wrote API key to profile")
	}

	return err
}

// askForPermission asks the user for permission to update their shell profile
func askForPermission(reader *bufio.Reader, profilePath string) bool {
	fmt.Printf("📝 Would you like to add your API key to %s? [Y/n] ", profilePath)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes" || response == ""
}

func init() {
	// No additional flags needed for init command
}
