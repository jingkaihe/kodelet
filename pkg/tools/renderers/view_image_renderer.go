package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ViewImageRenderer renders view_image results.
type ViewImageRenderer struct{}

// RenderCLI renders view_image results in CLI format.
func (r *ViewImageRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.ViewImageMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for view_image"
	}

	dimensions := ""
	if meta.ImageSize.Width > 0 && meta.ImageSize.Height > 0 {
		dimensions = fmt.Sprintf("\nDimensions: %dx%d", meta.ImageSize.Width, meta.ImageSize.Height)
	}
	detail := ""
	if meta.Detail != "" {
		detail = fmt.Sprintf("\nDetail: %s", meta.Detail)
	}
	mimeType := ""
	if meta.MimeType != "" {
		mimeType = fmt.Sprintf("\nType: %s", meta.MimeType)
	}

	return fmt.Sprintf("Image: %s%s%s%s", meta.Path, mimeType, dimensions, detail)
}
