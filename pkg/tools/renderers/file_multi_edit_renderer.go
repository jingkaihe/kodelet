package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// FileMultiEditRenderer renders file multi-edit results
type FileMultiEditRenderer struct{}

func (r *FileMultiEditRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.FileMultiEditMetadata
	if !extractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for file_multi_edit"
	}

	output := fmt.Sprintf("File Multi Edit: %s\n", meta.FilePath)
	output += fmt.Sprintf("Total edits: %d\n\n", len(meta.Edits))

	for i, edit := range meta.Edits {
		output += fmt.Sprintf("Edit %d:\n", i+1)
		if edit.OldContent != "" {
			output += fmt.Sprintf("- %s\n", strings.ReplaceAll(edit.OldContent, "\n", "\n- "))
		}
		if edit.NewContent != "" {
			output += fmt.Sprintf("+ %s\n", strings.ReplaceAll(edit.NewContent, "\n", "\n+ "))
		}
		output += "\n"
	}

	return output
}
