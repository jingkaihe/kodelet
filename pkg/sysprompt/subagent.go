package sysprompt

import (
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SubAgentPrompt generates a subagent prompt for the given model.
// Deprecated: use SystemPrompt with llm.Config.IsSubAgent set to true.
func SubAgentPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	return BuildPrompt(model, llmConfig, contexts, buildOptions{IsSubagent: true})
}
