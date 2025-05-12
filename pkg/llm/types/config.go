package types

import (
	"github.com/spf13/viper"
)

// Config holds the configuration for the LLM client
type Config struct {
	Model     string
	MaxTokens int
}

// GetConfigFromViper returns a Config object based on the current Viper settings
func GetConfigFromViper() Config {
	return Config{
		Model:     viper.GetString("model"),
		MaxTokens: viper.GetInt("max_tokens"),
	}
}