package renderers

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// FileReadRenderer renders file read results
type FileReadRenderer struct{}

// RenderCLI renders file read results in CLI format with line numbers, showing the file path,
// offset, content, and truncation status if applicable.
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
	buf.WriteString(osutil.ContentWithLineNumber(meta.Lines, meta.Offset))

	if meta.Truncated {
		buf.WriteString("\n... [truncated]")
	}

	return buf.String()
}

// RenderMarkdown renders file read results in markdown format.
func (r *FileReadRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.FileReadMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(meta.FilePath))
	fmt.Fprintf(&output, "- **Offset:** %d\n", meta.Offset)
	fmt.Fprintf(&output, "- **Lines:** %d\n", len(meta.Lines))
	if meta.Language != "" {
		fmt.Fprintf(&output, "- **Language:** %s\n", inlineCode(meta.Language))
	}
	if meta.Truncated {
		fmt.Fprintf(&output, "- **Truncated:** yes")
		if meta.RemainingLines > 0 {
			fmt.Fprintf(&output, " (%d lines remaining)", meta.RemainingLines)
		}
		output.WriteString("\n")
	}

	content := osutil.ContentWithLineNumber(meta.Lines, meta.Offset)
	if strings.TrimSpace(content) != "" {
		output.WriteString("\n")
		output.WriteString(fencedCodeBlock("text", content))
	}

	return strings.TrimSpace(output.String())
}

// FileWriteRenderer renders file write results
type FileWriteRenderer struct{}

// RenderCLI renders file write results in CLI format, showing the file path and size.
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

// RenderMarkdown renders file write results in markdown format.
func (r *FileWriteRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.FileWriteMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(meta.FilePath))
	fmt.Fprintf(&output, "- **Size:** %d bytes\n", meta.Size)
	if meta.Language != "" {
		fmt.Fprintf(&output, "- **Language:** %s\n", inlineCode(meta.Language))
	}

	if strings.TrimSpace(meta.Content) != "" {
		output.WriteString("\n")
		output.WriteString(markdownDetails("Written content", fencedCodeBlock(meta.Language, meta.Content)))
	}

	return strings.TrimSpace(output.String())
}

// FileEditRenderer renders file edit results
type FileEditRenderer struct{}

// RenderCLI renders file edit results in CLI format with unified diff output, showing the
// file path, number of replacements, and the changes made to the file.
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

// RenderMarkdown renders file edit results in markdown format.
func (r *FileEditRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.FileEditMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	if len(meta.Edits) == 0 {
		return fmt.Sprintf("- **Path:** %s\n- **Changes:** none", inlineCode(meta.FilePath))
	}

	var output strings.Builder
	if meta.ReplaceAll && meta.ReplacedCount > 1 {
		fmt.Fprintf(&output, "File edited: %s (%d replacements)\n", meta.FilePath, meta.ReplacedCount)
	} else if meta.ReplaceAll {
		fmt.Fprintf(&output, "File edited: %s (1 replacement)\n", meta.FilePath)
	} else {
		fmt.Fprintf(&output, "File edited: %s\n", meta.FilePath)
	}

	for i, edit := range meta.Edits {
		diff := udiff.Unified(meta.FilePath, meta.FilePath, edit.OldContent, edit.NewContent)
		output.WriteString("\n")
		if len(meta.Edits) > 1 {
			fmt.Fprintf(&output, "Edit %d (lines %d-%d):\n\n", i+1, edit.StartLine, edit.EndLine)
		} else {
			fmt.Fprintf(&output, "Lines %d-%d:\n\n", edit.StartLine, edit.EndLine)
		}
		output.WriteString(fencedCodeBlock("diff", diff))
	}

	return strings.TrimSpace(output.String())
}

// ApplyPatchRenderer renders apply_patch results.
type ApplyPatchRenderer struct{}

// RenderCLI renders apply_patch results with a summary and unified diffs.
func (r *ApplyPatchRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.ApplyPatchMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for apply_patch"
	}

	var output bytes.Buffer
	output.WriteString("Success. Updated the following files:\n")
	for _, path := range meta.Added {
		fmt.Fprintf(&output, "A %s\n", path)
	}
	for _, path := range meta.Modified {
		fmt.Fprintf(&output, "M %s\n", path)
	}
	for _, path := range meta.Deleted {
		fmt.Fprintf(&output, "D %s\n", path)
	}

	for _, change := range meta.Changes {
		if change.Operation == tools.ApplyPatchOperationUpdate && change.UnifiedDiff != "" {
			output.WriteString("\n")
			output.WriteString(change.UnifiedDiff)
		}
		if change.Operation == tools.ApplyPatchOperationAdd && change.NewContent != "" {
			output.WriteString("\n")
			diff := udiff.Unified(change.Path, change.Path, "", change.NewContent)
			output.WriteString(diff)
		}
		if change.Operation == tools.ApplyPatchOperationDelete && change.OldContent != "" {
			output.WriteString("\n")
			diff := udiff.Unified(change.Path, change.Path, change.OldContent, "")
			output.WriteString(diff)
		}
	}

	return strings.TrimSuffix(output.String(), "\n")
}

// RenderMarkdown renders apply_patch results in markdown format.
func (r *ApplyPatchRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.ApplyPatchMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	output.WriteString("Success. Updated the following files:\n")

	if len(meta.Added)+len(meta.Modified)+len(meta.Deleted) > 0 {
		for _, path := range meta.Added {
			fmt.Fprintf(&output, "A %s\n", path)
		}
		for _, path := range meta.Modified {
			fmt.Fprintf(&output, "M %s\n", path)
		}
		for _, path := range meta.Deleted {
			fmt.Fprintf(&output, "D %s\n", path)
		}
	}

	for _, change := range meta.Changes {
		var diff string
		switch change.Operation {
		case tools.ApplyPatchOperationUpdate:
			diff = change.UnifiedDiff
		case tools.ApplyPatchOperationAdd:
			if change.NewContent != "" {
				diff = udiff.Unified(change.Path, change.Path, "", change.NewContent)
			}
		case tools.ApplyPatchOperationDelete:
			if change.OldContent != "" {
				diff = udiff.Unified(change.Path, change.Path, change.OldContent, "")
			}
		}

		if strings.TrimSpace(diff) == "" {
			continue
		}

		output.WriteString("\n")
		output.WriteString(fencedCodeBlock("diff", diff))
	}

	return strings.TrimSpace(output.String())
}
