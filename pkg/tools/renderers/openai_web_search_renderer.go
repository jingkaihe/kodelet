package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// OpenAIWebSearchRenderer renders native OpenAI web search results.
type OpenAIWebSearchRenderer struct{}

// RenderCLI renders native OpenAI web search results in CLI format.
func (r *OpenAIWebSearchRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return result.Error
	}

	var meta tools.OpenAIWebSearchMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for openai_web_search"
	}

	lines := []string{fmt.Sprintf("OpenAI Web Search (%s)", strings.TrimSpace(meta.Status))}
	if meta.Action != "" {
		lines = append(lines, fmt.Sprintf("Action: %s", meta.Action))
	}
	if len(meta.Queries) > 0 {
		lines = append(lines, fmt.Sprintf("Queries: %s", strings.Join(meta.Queries, ", ")))
	}
	if meta.URL != "" {
		lines = append(lines, fmt.Sprintf("URL: %s", meta.URL))
	}
	if meta.Pattern != "" {
		lines = append(lines, fmt.Sprintf("Pattern: %s", meta.Pattern))
	}
	if len(meta.Sources) > 0 {
		lines = append(lines, "Sources:")
		for _, source := range meta.Sources {
			lines = append(lines, fmt.Sprintf("- %s", source))
		}
	}
	if len(meta.Results) > 0 {
		lines = append(lines, "Results:")
		for _, resultURL := range meta.Results {
			lines = append(lines, fmt.Sprintf("- %s", resultURL))
		}
	}

	return strings.Join(lines, "\n")
}
