package llm

import (
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// NewTranscriber constructs a speech recognizer from a model profile.
// It does not create a chat thread or read process-wide configuration.
func NewTranscriber(config llmtypes.Config) (llmtypes.Transcriber, error) {
	config.TranscriptionModel = strings.TrimSpace(config.TranscriptionModel)
	if !config.ModelAliasesResolved {
		config.TranscriptionModel = resolveModelAlias(config.TranscriptionModel, config.Aliases)
	}
	switch strings.ToLower(strings.TrimSpace(config.Provider)) {
	case "openai":
		return openai.NewTranscriber(config)
	default:
		return nil, errors.New("transcription requires a profile with provider: openai")
	}
}
