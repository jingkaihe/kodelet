package sysprompt

import (
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SubAgentPrompt generates a subagent prompt for the given model.
// It delegates to SystemPrompt with IsSubAgent set to true for unified prompt generation.
func SubAgentPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	// Create a copy with IsSubAgent set to true for unified prompt handling
	subagentConfig := llmConfig
	subagentConfig.IsSubAgent = true
	return SystemPrompt(model, subagentConfig, contexts)
}
