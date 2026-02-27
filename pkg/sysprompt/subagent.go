package sysprompt

import llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"

func subagentPrompt(model string, llmConfig llmtypes.Config, contexts map[string]string) string {
	return buildPrompt(model, llmConfig, contexts, buildOptions{IsSubagent: true})
}
