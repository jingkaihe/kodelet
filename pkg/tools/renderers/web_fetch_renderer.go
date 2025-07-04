package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// WebFetchRenderer renders web fetch results
type WebFetchRenderer struct{}

func (r *WebFetchRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return result.Error
	}

	var meta tools.WebFetchMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for web_fetch"
	}

	if meta.SavedPath != "" {
		return fmt.Sprintf("Web Fetch: %s\nSaved to: %s\n%s", meta.URL, meta.SavedPath, meta.Content)
	}
	if meta.Prompt != "" {
		return fmt.Sprintf("Web Fetch: %s\nPrompt: %s\n%s", meta.URL, meta.Prompt, meta.Content)
	}
	return fmt.Sprintf("Web Fetch: %s\n%s", meta.URL, meta.Content)
}
