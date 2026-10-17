package llm

import (
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// NewRealtimeProvider resolves the independent voice model, never the coding
// profile's chat model. The selected profile supplies only connection settings.
func NewRealtimeProvider(config llmtypes.Config, model, voice string) (llmtypes.RealtimeProvider, error) {
	if strings.ToLower(strings.TrimSpace(config.Provider)) != "openai" {
		return nil, errors.New("realtime requires a voice profile with provider: openai")
	}
	return openai.NewRealtimeProvider(config, resolveModelAlias(strings.TrimSpace(model), config.Aliases), voice)
}
