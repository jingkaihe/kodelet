package llm

import (
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/spf13/viper"
)

// GetConfigFromViper returns a Config object based on the current Viper settings
func GetConfigFromViper() types.Config {
	return types.Config{
		Model:     viper.GetString("model"),
		MaxTokens: viper.GetInt("max_tokens"),
		WeakModel: viper.GetString("weak_model"),
	}
}
