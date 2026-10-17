package llm

import (
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// NewTranscriber constructs a speech recognizer using a profile's connection
// settings and an explicit transcription model, resolving its alias once.
// It does not create a chat thread or read process-wide configuration.
func NewTranscriber(config llmtypes.Config, model string) (llmtypes.Transcriber, error) {
	model = resolveModelAlias(strings.TrimSpace(model), config.Aliases)
	switch strings.ToLower(strings.TrimSpace(config.Provider)) {
	case "openai":
		return openai.NewTranscriber(config, model)
	default:
		return nil, errors.New("transcription requires a profile with provider: openai")
	}
}
