package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// WebFetchRenderer renders web fetch results
type WebFetchRenderer struct{}

func (r *WebFetchRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.WebFetchMetadata)
	if !ok {
		return "Error: Invalid metadata type for web_fetch"
	}

	output := fmt.Sprintf("Web Fetch: %s\n", meta.URL)
	output += fmt.Sprintf("Type: %s\n", meta.ProcessedType)

	if meta.SavedPath != "" {
		output += fmt.Sprintf("Saved to: %s\n", meta.SavedPath)
	}
	if meta.Prompt != "" {
		output += fmt.Sprintf("Prompt: %s\n", meta.Prompt)
	}
	if meta.Size > 0 {
		output += fmt.Sprintf("Size: %d bytes\n", meta.Size)
	}

	return output
}
