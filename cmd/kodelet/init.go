package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up Kodelet configuration",
	Long:  `Interactive setup for Kodelet configuration including API key and model preferences.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Create a reader for user input
		reader := bufio.NewReader(os.Stdin)

		fmt.Println("🚀 Kodelet Configuration Setup")
		fmt.Println("=============================")
		fmt.Println("This wizard will help you set up Kodelet with the recommended configuration.")
		fmt.Println("")

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
				fmt.Println("\n⚠️ Error reading API key:", err)
				return
			}
			apiKey = strings.TrimSpace(string(apiKeyBytes))
			fmt.Println() // Add a newline after the hidden input

			if apiKey != "" {
				fmt.Println("✅ API key received.")
				needsApiKeySetup = true
			} else {
				fmt.Println("\n⚠️ No API key provided. You will need to set ANTHROPIC_API_KEY environment variable to use Kodelet.")
			}
		} else {
			fmt.Println("🔑 Found Anthropic API key in environment variables. ✅")
		}

		// Save API key to shell profile if needed
		if needsApiKeySetup && apiKey != "" {
			// Detect shell and profile path
			shellName, profilePath = detectShell()

			// Check if the API key is already in the profile
			if checkApiKeyInProfile(profilePath) {
				fmt.Println("🔑 API key is already configured in your shell profile.")
			} else {
				// Ask permission to update the shell profile
				if askForPermission(reader, profilePath) {
					err := writeApiKeyToProfile(profilePath, shellName, apiKey)
					if err != nil {
						fmt.Printf("\n⚠️ Failed to update shell profile: %v\n", err)
						fmt.Println("📝 To manually set your API key, add the following to your shell profile:")

						// Show the appropriate export command based on shell
						if shellName == "fish" {
							fmt.Printf("   set -x ANTHROPIC_API_KEY \"%s\"\n\n", apiKey)
						} else {
							fmt.Printf("   export ANTHROPIC_API_KEY=\"%s\"\n\n", apiKey)
						}
					} else {
						fmt.Printf("\n✅ API key added to %s\n", profilePath)
						apiKeyAddedToProfile = true
					}
				} else {
					fmt.Println("\n📝 To manually set your API key, add the following to your shell profile:")

					// Show the appropriate export command based on shell
					if shellName == "fish" {
						fmt.Printf("   set -x ANTHROPIC_API_KEY \"%s\"\n\n", apiKey)
					} else {
						fmt.Printf("   export ANTHROPIC_API_KEY=\"%s\"\n\n", apiKey)
					}
				}
			}
		}

		// Configure models and tokens
		fmt.Println("📋 Let's configure your model preferences:")

		// Model selection
		defaultModel := viper.GetString("model")
		if defaultModel == "" {
			defaultModel = anthropic.ModelClaude3_7SonnetLatest
		}

		fmt.Printf("   Primary model [%s]: ", defaultModel)
		modelInput, _ := reader.ReadString('\n')
		modelInput = strings.TrimSpace(modelInput)

		if modelInput != "" {
			defaultModel = modelInput
		}

		// Weak model selection
		defaultWeakModel := viper.GetString("weak_model")
		if defaultWeakModel == "" {
			defaultWeakModel = anthropic.ModelClaude3_5HaikuLatest
		}

		fmt.Printf("   Secondary (weak) model [%s]: ", defaultWeakModel)
		weakModelInput, _ := reader.ReadString('\n')
		weakModelInput = strings.TrimSpace(weakModelInput)

		if weakModelInput != "" {
			defaultWeakModel = weakModelInput
		}

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

		// Create config directory if it doesn't exist
		configDir := filepath.Join(os.Getenv("HOME"), ".kodelet")
		err := os.MkdirAll(configDir, 0755)
		if err != nil {
			fmt.Printf("\n❌ Failed to create config directory: %v\n", err)
			return
		}

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

		configContent += "# Maximum thinking tokens\n"
		configContent += fmt.Sprintf("thinking_budget_tokens: %d\n", defaultThinkingBudgetTokens)

		// Write the config file
		err = os.WriteFile(configFile, []byte(configContent), 0644)
		if err != nil {
			fmt.Printf("\n❌ Failed to write config file: %v\n", err)
			return
		}

		fmt.Printf("\n✅ Configuration saved to %s\n", configFile)
		fmt.Println("\n🎉 Kodelet setup complete! You can now use Kodelet.")

		// If we added an API key to the profile, remind the user to refresh their shell
		if needsApiKeySetup && apiKey != "" && apiKeyAddedToProfile {
			fmt.Println("\n" + strings.Repeat("═", 50))
			fmt.Println("⚠️  IMPORTANT ACTION REQUIRED ⚠️")
			fmt.Println("To activate your API key, you must restart your terminal")
			fmt.Println("or run the following command:")

			fmt.Printf("   $ source %s\n", profilePath)

			fmt.Println(strings.Repeat("═", 50))
		}

		// Show some example commands
		fmt.Println("\nExample commands to get started:")
		fmt.Println("  kodelet chat                  # Start an interactive chat session")
		fmt.Println("  kodelet run \"your query\"      # Run a one-shot query")
		fmt.Println("  kodelet watch                 # Start watch mode to monitor file changes")
		fmt.Println("  kodelet --help                # Show available commands")
	},
}

// detectShell determines the user's shell and returns the appropriate profile file path
func detectShell() (string, string) {
	// Get the user's shell from the SHELL environment variable
	shell := os.Getenv("SHELL")
	if shell == "" {
		// Default to bash if we can't determine the shell
		return "bash", filepath.Join(os.Getenv("HOME"), ".bashrc")
	}

	// Extract the shell name from the path
	shellName := filepath.Base(shell)

	// Determine the appropriate profile file based on the shell
	switch shellName {
	case "bash":
		// Check if .bash_profile exists, use it for login shells on macOS
		bashProfile := filepath.Join(os.Getenv("HOME"), ".bash_profile")
		if _, err := os.Stat(bashProfile); err == nil {
			return "bash", bashProfile
		}
		return "bash", filepath.Join(os.Getenv("HOME"), ".bashrc")
	case "zsh":
		return "zsh", filepath.Join(os.Getenv("HOME"), ".zshrc")
	case "fish":
		// Fish uses a different directory structure
		fishConfig := filepath.Join(os.Getenv("HOME"), ".config/fish/config.fish")
		return "fish", fishConfig
	default:
		// Default to .profile for unknown shells
		return shellName, filepath.Join(os.Getenv("HOME"), ".profile")
	}
}

// checkApiKeyInProfile checks if the API key is already set in the profile
func checkApiKeyInProfile(profilePath string) bool {
	// Read the profile file
	content, err := os.ReadFile(profilePath)
	if err != nil {
		// If the file doesn't exist or can't be read, assume the key is not there
		return false
	}

	// Check if the API key export is already in the file
	return strings.Contains(string(content), "export ANTHROPIC_API_KEY=") ||
		strings.Contains(string(content), "set -x ANTHROPIC_API_KEY")
}

// writeApiKeyToProfile adds the API key to the shell profile
func writeApiKeyToProfile(profilePath, shellName, apiKey string) error {
	// Open the file in append mode
	file, err := os.OpenFile(profilePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Add a newline for cleanliness
	_, err = file.WriteString("\n# Added by Kodelet\n")
	if err != nil {
		return err
	}

	// Add the export statement based on the shell
	switch shellName {
	case "fish":
		_, err = file.WriteString(fmt.Sprintf("set -x ANTHROPIC_API_KEY \"%s\"\n", apiKey))
	default:
		_, err = file.WriteString(fmt.Sprintf("export ANTHROPIC_API_KEY=\"%s\"\n", apiKey))
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
