package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// GrepRenderer renders grep search results
type GrepRenderer struct{}

func (r *GrepRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.GrepMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for grep_tool"
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Pattern: %s\n", meta.Pattern))

	if meta.Path != "" {
		output.WriteString(fmt.Sprintf("Path: %s\n", meta.Path))
	}
	if meta.Include != "" {
		output.WriteString(fmt.Sprintf("Include: %s\n", meta.Include))
	}

	output.WriteString(fmt.Sprintf("\nFound %d files with matches:\n", len(meta.Results)))

	for _, result := range meta.Results {
		output.WriteString(fmt.Sprintf("\n%s:\n", result.FilePath))
		for _, match := range result.Matches {
			output.WriteString(fmt.Sprintf("  %d: %s\n", match.LineNumber, match.Content))
		}
	}

	if meta.Truncated {
		output.WriteString("\n... [results truncated]")
	}

	return output.String()
}

// GlobRenderer renders glob file pattern results
type GlobRenderer struct{}

func (r *GlobRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.GlobMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for glob_tool"
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Pattern: %s\n", meta.Pattern))

	if meta.Path != "" {
		output.WriteString(fmt.Sprintf("Path: %s\n", meta.Path))
	}

	output.WriteString(fmt.Sprintf("\nFound %d files:\n", len(meta.Files)))

	for _, file := range meta.Files {
		typeIndicator := ""
		if file.Type == "directory" {
			typeIndicator = "/"
		}
		output.WriteString(fmt.Sprintf("  %s%s (%d bytes)\n", file.Path, typeIndicator, file.Size))
	}

	if meta.Truncated {
		output.WriteString("\n... [results truncated]")
	}

	return output.String()
}
