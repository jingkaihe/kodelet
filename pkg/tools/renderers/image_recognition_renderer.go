package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ImageRecognitionRenderer renders image recognition results
type ImageRecognitionRenderer struct{}

// RenderCLI renders image recognition results in CLI format, showing the image path,
// type, prompt, and analysis results.
func (r *ImageRecognitionRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.ImageRecognitionMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for image_recognition"
	}

	return fmt.Sprintf("Image Recognition: %s\nType: %s\nPrompt: %s\n\nAnalysis:\n%s",
		meta.ImagePath, meta.ImageType, meta.Prompt, meta.Analysis)
}
