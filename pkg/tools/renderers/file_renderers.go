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

	meta, ok := result.Metadata.(*tools.FileReadMetadata)
	if !ok {
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

	meta, ok := result.Metadata.(*tools.FileWriteMetadata)
	if !ok {
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

	meta, ok := result.Metadata.(*tools.FileEditMetadata)
	if !ok {
		return "Error: Invalid metadata type for file_edit"
	}

	// For file edits, we expect a single edit that represents the whole file change
	if len(meta.Edits) == 0 {
		return fmt.Sprintf("File edited: %s (no changes)", meta.FilePath)
	}

	// Use the first edit which should contain the full old/new content
	edit := meta.Edits[0]
	out := udiff.Unified(meta.FilePath, meta.FilePath, edit.OldContent, edit.NewContent)
	return out
}
