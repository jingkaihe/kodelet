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
