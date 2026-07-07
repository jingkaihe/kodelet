package renderers

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"
	"github.com/jingkaihe/kodelet/pkg/diffview"
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
	return r.renderMarkdown(result, true, true)
}

// RenderToolUseMarkdown renders file_read invocation inputs in markdown format.
func (r *FileReadRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.FileReadInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(input.FilePath))
	if input.Offset > 0 {
		fmt.Fprintf(&output, "- **Offset:** %d\n", input.Offset)
	}
	if input.LineLimit > 0 {
		fmt.Fprintf(&output, "- **Line limit:** %d\n", input.LineLimit)
	}

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders file_read results for the merged tool-call view.
func (r *FileReadRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, false, false)
}

func (r *FileReadRenderer) renderMarkdown(result tools.StructuredToolResult, includePath bool, includeOffset bool) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.FileReadMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	if includePath {
		fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(meta.FilePath))
	}
	if includeOffset {
		fmt.Fprintf(&output, "- **Offset:** %d\n", meta.Offset)
	}
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
	return r.renderMarkdown(result, true, true)
}

// RenderToolUseMarkdown renders file_write invocation inputs in markdown format.
func (r *FileWriteRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.FileWriteInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(input.FilePath))
	output.WriteString("\n")
	output.WriteString(markdownDetails("Requested content", fencedCodeBlock("text", input.Text)))

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders file_write results for the merged tool-call view.
func (r *FileWriteRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, false, false)
}

func (r *FileWriteRenderer) renderMarkdown(result tools.StructuredToolResult, includePath bool, includeContent bool) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.FileWriteMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	if includePath {
		fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(meta.FilePath))
	}
	fmt.Fprintf(&output, "- **Size:** %d bytes\n", meta.Size)
	if meta.Language != "" {
		fmt.Fprintf(&output, "- **Language:** %s\n", inlineCode(meta.Language))
	}

	if includeContent && strings.TrimSpace(meta.Content) != "" {
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
	return r.renderMarkdown(result, true)
}

// RenderToolUseMarkdown renders file_edit invocation inputs in markdown format.
func (r *FileEditRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.FileEditInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(input.FilePath))
	if input.ReplaceAll {
		output.WriteString("- **Mode:** replace all\n")
	} else {
		output.WriteString("- **Mode:** targeted edit\n")
	}

	output.WriteString("\n")
	var request strings.Builder
	request.WriteString("**Old text**\n\n")
	request.WriteString(fencedCodeBlock("text", input.OldText))
	request.WriteString("\n\n**New text**\n\n")
	request.WriteString(fencedCodeBlock("text", input.NewText))
	output.WriteString(markdownDetails("Requested edit", request.String()))

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders file_edit results for the merged tool-call view.
func (r *FileEditRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, false)
}

func (r *FileEditRenderer) renderMarkdown(result tools.StructuredToolResult, includeHeader bool) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.FileEditMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	if len(meta.Edits) == 0 {
		if includeHeader {
			return fmt.Sprintf("- **Path:** %s\n- **Changes:** none", inlineCode(meta.FilePath))
		}
		return "- **Changes:** none"
	}

	var output strings.Builder
	if includeHeader {
		if meta.ReplaceAll && meta.ReplacedCount > 1 {
			fmt.Fprintf(&output, "File edited: %s (%d replacements)\n", meta.FilePath, meta.ReplacedCount)
		} else if meta.ReplaceAll {
			fmt.Fprintf(&output, "File edited: %s (1 replacement)\n", meta.FilePath)
		} else {
			fmt.Fprintf(&output, "File edited: %s\n", meta.FilePath)
		}
	}

	for i, edit := range meta.Edits {
		diff := udiff.Unified(meta.FilePath, meta.FilePath, edit.OldContent, edit.NewContent)
		if output.Len() > 0 {
			output.WriteString("\n")
		}
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
	var meta tools.ApplyPatchMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		if !result.Success {
			return fmt.Sprintf("Error: %s", result.Error)
		}
		return "Error: Invalid metadata type for apply_patch"
	}

	var output strings.Builder
	if result.Success {
		output.WriteString("Success. Updated files")
	} else {
		output.WriteString("Patch failed")
	}
	summary := diffview.FromApplyPatchMetadata(meta)
	fmt.Fprintf(&output, " (+%d -%d):", summary.Added, summary.Removed)

	rendered := diffview.RenderedText(diffview.RenderSummary(summary))
	if strings.TrimSpace(rendered) != "" {
		output.WriteString("\n")
		output.WriteString(rendered)
	}
	if !result.Success && strings.TrimSpace(result.Error) != "" {
		output.WriteString("\n\nError: ")
		output.WriteString(strings.TrimSpace(result.Error))
	}

	return strings.TrimSuffix(output.String(), "\n")
}

// RenderMarkdown renders apply_patch results in markdown format.
func (r *ApplyPatchRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result)
}

// RenderToolUseMarkdown renders apply_patch invocation inputs in markdown format.
func (r *ApplyPatchRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.ApplyPatchInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	operations := summarizeApplyPatchInput(input.Input)
	if len(operations) == 0 {
		return markdownDetails("Original patch", fencedCodeBlock("diff", input.Input))
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Patch operations:** %d\n", len(operations))
	for _, op := range operations {
		fmt.Fprintf(&output, "- %s\n", op)
	}
	output.WriteString("\n")
	output.WriteString(markdownDetails("Original patch", fencedCodeBlock("diff", input.Input)))

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders apply_patch results for the merged tool-call view.
func (r *ApplyPatchRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result)
}

func (r *ApplyPatchRenderer) renderMarkdown(result tools.StructuredToolResult) string {
	var meta tools.ApplyPatchMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	summary := diffview.FromApplyPatchMetadata(meta)
	if result.Success {
		fmt.Fprintf(&output, "Success. Updated files (+%d -%d).", summary.Added, summary.Removed)
	} else {
		fmt.Fprintf(&output, "Patch failed (+%d -%d).", summary.Added, summary.Removed)
	}

	for _, file := range summary.Files {
		output.WriteString("\n")
		output.WriteString("\n")
		fmt.Fprintf(&output, "- **%s**\n", file.Header())
		body := diffview.RenderedText(diffview.RenderFileBody(file))
		if strings.TrimSpace(body) != "" {
			output.WriteString(fencedCodeBlock("diff", body))
		}
	}
	if !result.Success && strings.TrimSpace(result.Error) != "" {
		output.WriteString("\n\n")
		fmt.Fprintf(&output, "**Error:** %s", strings.TrimSpace(result.Error))
	}

	return strings.TrimSpace(output.String())
}
