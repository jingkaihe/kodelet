package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// GrepRenderer renders grep search results
type GrepRenderer struct{}

// RenderCLI renders grep search results in CLI format, showing the search pattern, path,
// include filter, matched files with line numbers, and truncation status.
func (r *GrepRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.GrepMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for grep_tool"
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Pattern: %s\n", meta.Pattern)

	if meta.Path != "" {
		fmt.Fprintf(&output, "Path: %s\n", meta.Path)
	}
	if meta.Include != "" {
		fmt.Fprintf(&output, "Include: %s\n", meta.Include)
	}

	fmt.Fprintf(&output, "\nFound %d files with matches:\n", len(meta.Results))

	for _, res := range meta.Results {
		fmt.Fprintf(&output, "\n%s:\n", res.FilePath)
		for _, match := range res.Matches {
			if match.IsContext {
				fmt.Fprintf(&output, "  %d- %s\n", match.LineNumber, match.Content)
			} else {
				fmt.Fprintf(&output, "  %d: %s\n", match.LineNumber, match.Content)
			}
		}
	}

	if meta.Truncated {
		switch meta.TruncationReason {
		case tools.GrepTruncatedByFileLimit:
			maxResults := meta.MaxResults
			if maxResults == 0 {
				maxResults = 100
			}
			fmt.Fprintf(&output, "\n... [truncated: max %d files]", maxResults)
		case tools.GrepTruncatedByOutputSize:
			output.WriteString("\n... [truncated: output size limit (50KB)]")
		default:
			output.WriteString("\n... [results truncated]")
		}
	}

	return output.String()
}

// RenderMarkdown renders grep search results in markdown format.
func (r *GrepRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, true)
}

// RenderToolUseMarkdown renders grep_tool invocation inputs in markdown format.
func (r *GrepRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.CodeSearchInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineCode(input.Pattern))
	if input.Path != "" {
		fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(input.Path))
	}
	if input.Include != "" {
		fmt.Fprintf(&output, "- **Include:** %s\n", inlineCode(input.Include))
	}
	if input.SurroundLines > 0 {
		fmt.Fprintf(&output, "- **Context lines:** %d\n", input.SurroundLines)
	}
	if input.MaxResults > 0 {
		fmt.Fprintf(&output, "- **Max results:** %d\n", input.MaxResults)
	}
	if input.FixedStrings {
		output.WriteString("- **Fixed strings:** true\n")
	}
	if input.IgnoreCase {
		output.WriteString("- **Ignore case:** true\n")
	}

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders grep search results for the merged tool-call view.
func (r *GrepRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, false)
}

func (r *GrepRenderer) renderMarkdown(result tools.StructuredToolResult, includeQueryMetadata bool) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.GrepMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	if includeQueryMetadata {
		fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineCode(meta.Pattern))
		if meta.Path != "" {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(meta.Path))
		}
		if meta.Include != "" {
			fmt.Fprintf(&output, "- **Include:** %s\n", inlineCode(meta.Include))
		}
	}
	fmt.Fprintf(&output, "- **Files with matches:** %d\n", len(meta.Results))

	if meta.Truncated {
		summary := "results truncated"
		switch meta.TruncationReason {
		case tools.GrepTruncatedByFileLimit:
			maxResults := meta.MaxResults
			if maxResults == 0 {
				maxResults = 100
			}
			summary = fmt.Sprintf("truncated at %d files", maxResults)
		case tools.GrepTruncatedByOutputSize:
			summary = "truncated at output size limit"
		}
		fmt.Fprintf(&output, "- **Truncated:** %s\n", summary)
	}

	for _, res := range meta.Results {
		var fileOutput strings.Builder
		for _, match := range res.Matches {
			if match.IsContext {
				fmt.Fprintf(&fileOutput, "%d- %s\n", match.LineNumber, match.Content)
			} else {
				fmt.Fprintf(&fileOutput, "%d: %s\n", match.LineNumber, match.Content)
			}
		}

		output.WriteString("\n")
		fmt.Fprintf(&output, "#### %s\n\n", inlineCode(res.FilePath))
		output.WriteString(fencedCodeBlock("text", strings.TrimSuffix(fileOutput.String(), "\n")))
		output.WriteString("\n")
	}

	return strings.TrimSpace(output.String())
}

// GlobRenderer renders glob file pattern results
type GlobRenderer struct{}

// RenderCLI renders glob pattern search results in CLI format, showing the pattern, path,
// matched files with sizes, type indicators, and truncation status.
func (r *GlobRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.GlobMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for glob_tool"
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Pattern: %s\n", meta.Pattern)

	if meta.Path != "" {
		fmt.Fprintf(&output, "Path: %s\n", meta.Path)
	}

	fmt.Fprintf(&output, "\nFound %d files:\n", len(meta.Files))

	for _, file := range meta.Files {
		typeIndicator := ""
		if file.Type == "directory" {
			typeIndicator = "/"
		}
		fmt.Fprintf(&output, "  %s%s (%d bytes)\n", file.Path, typeIndicator, file.Size)
	}

	if meta.Truncated {
		output.WriteString("\n... [results truncated]")
	}

	return output.String()
}

// RenderMarkdown renders glob pattern results in markdown format.
func (r *GlobRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, true)
}

// RenderToolUseMarkdown renders glob_tool invocation inputs in markdown format.
func (r *GlobRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.GlobInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineCode(input.Pattern))
	if input.Path != "" {
		fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(input.Path))
	}
	if input.IgnoreGitignore {
		output.WriteString("- **Ignore .gitignore:** true\n")
	}

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders glob pattern results for the merged tool-call view.
func (r *GlobRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, false)
}

func (r *GlobRenderer) renderMarkdown(result tools.StructuredToolResult, includePattern bool) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.GlobMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	if includePattern {
		fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineCode(meta.Pattern))
		if meta.Path != "" {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineCode(meta.Path))
		}
	}
	fmt.Fprintf(&output, "- **Matches:** %d\n", len(meta.Files))
	if meta.Truncated {
		output.WriteString("- **Truncated:** yes\n")
	}

	if len(meta.Files) > 0 {
		output.WriteString("\n**Files**\n")
		for _, file := range meta.Files {
			typeLabel := file.Type
			if typeLabel == "" {
				typeLabel = "file"
			}
			path := file.Path
			if file.Type == "directory" {
				path += "/"
			}
			fmt.Fprintf(&output, "- %s (%s, %d bytes)\n", inlineCode(path), typeLabel, file.Size)
		}
	}

	return strings.TrimSpace(output.String())
}
