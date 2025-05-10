package llm

import (
	"github.com/spf13/viper"
)

// GetConfigFromViper returns a Config object based on the current Viper settings
func GetConfigFromViper() Config {
	return Config{
		Model:     viper.GetString("model"),
		MaxTokens: viper.GetInt("max_tokens"),
	}
}
