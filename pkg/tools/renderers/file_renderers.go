package renderers

import (
	"bytes"
	"fmt"

	"github.com/aymanbagabas/go-udiff"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
)

// FileReadRenderer renders file read results
type FileReadRenderer struct{}

func (r *FileReadRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.FileReadMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for file_read"
	}

	buf := bytes.NewBufferString(fmt.Sprintf("File Read: %s\n", meta.FilePath))
	fmt.Fprintf(buf, "Offset: %d\n", meta.Offset)
	buf.WriteString(utils.ContentWithLineNumber(meta.Lines, meta.Offset))

	if meta.Truncated {
		buf.WriteString("\n... [truncated]")
	}

	return buf.String()
}

// FileWriteRenderer renders file write results
type FileWriteRenderer struct{}

func (r *FileWriteRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.FileWriteMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for file_write"
	}

	return fmt.Sprintf("File written successfully: %s\nSize: %d bytes",
		meta.FilePath, meta.Size)
}

// FileEditRenderer renders file edit results
type FileEditRenderer struct{}

func (r *FileEditRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.FileEditMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for file_edit"
	}

	// For file edits, we expect at least one edit
	if len(meta.Edits) == 0 {
		return fmt.Sprintf("File edited: %s (no changes)", meta.FilePath)
	}

	var output bytes.Buffer
	if meta.ReplaceAll && meta.ReplacedCount > 1 {
		fmt.Fprintf(&output, "File edited: %s (%d replacements)\n\n", meta.FilePath, meta.ReplacedCount)
		
		// Show all edits
		for i, edit := range meta.Edits {
			fmt.Fprintf(&output, "Edit %d (lines %d-%d):\n", i+1, edit.StartLine, edit.EndLine)
			diff := udiff.Unified(meta.FilePath, meta.FilePath, edit.OldContent, edit.NewContent)
			output.WriteString(diff)
			if i < len(meta.Edits)-1 {
				output.WriteString("\n")
			}
		}
	} else {
		// Single edit or replace_all with single occurrence
		edit := meta.Edits[0]
		if meta.ReplaceAll {
			fmt.Fprintf(&output, "File edited: %s (1 replacement)\n\n", meta.FilePath)
		} else {
			fmt.Fprintf(&output, "File edited: %s\n\n", meta.FilePath)
		}
		diff := udiff.Unified(meta.FilePath, meta.FilePath, edit.OldContent, edit.NewContent)
		output.WriteString(diff)
	}
	
	return output.String()
}
