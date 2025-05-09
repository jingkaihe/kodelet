package llm

import (
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/viper"
)

// InitConfig initializes the LLM provider configuration
func InitConfig() {
	// Set default values
	viper.SetDefault("provider", "anthropic")
	viper.SetDefault("max_tokens", 8192)
	viper.SetDefault("providers.anthropic.model", anthropic.ModelClaude3_7SonnetLatest)
	viper.SetDefault("providers.openai.model", openai.GPT4Dot1)
}

// GetProviderFromConfig returns the appropriate LLM provider based on configuration
func GetProviderFromConfig() (Provider, error) {
	providerName := viper.GetString("provider")
	// Check for provider-specific API keys
	var apiKey string
	switch providerName {
	case "anthropic":
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			// Try environment variable directly if not in viper
			apiKey = viper.GetString("providers.anthropic.api_key")
		}
	case "openai":
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			// Try environment variable directly if not in viper
			apiKey = viper.GetString("providers.openai.api_key")
		}
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}

	if apiKey == "" {
		return nil, fmt.Errorf("API key not found for provider: %s", providerName)
	}

	// Create provider-specific options
	options := ProviderOptions{
		APIKey:     apiKey,
		MaxTokens:  viper.GetInt("max_tokens"),
		Parameters: make(map[string]interface{}),
	}

	// Set provider-specific model
	switch providerName {
	case "anthropic":
		options.Model = viper.GetString("providers.anthropic.model")
		if options.Model == "" {
			options.Model = viper.GetString("model") // Fallback to the global model setting
		}
	case "openai":
		options.Model = viper.GetString("providers.openai.model")
		if options.Model == "" {
			options.Model = viper.GetString("model") // Fallback to the global model setting
		}

		// Add additional OpenAI parameters
		if reasoning := viper.GetString("providers.openai.reasoning_effort"); reasoning != "" {
			options.Parameters["reasoning_effort"] = reasoning
		}
	}

	return NewProvider(providerName, options)
}
