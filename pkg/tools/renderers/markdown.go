package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// MarkdownRenderer renders structured tool results to markdown.
type MarkdownRenderer interface {
	RenderMarkdown(result tools.StructuredToolResult) string
}

func fencedCodeBlock(language string, content string) string {
	trimmed := strings.TrimSuffix(content, "\n")
	if language != "" {
		return fmt.Sprintf("```%s\n%s\n```", language, trimmed)
	}
	return fmt.Sprintf("```\n%s\n```", trimmed)
}

func inlineCode(value string) string {
	if strings.Contains(value, "`") {
		return fmt.Sprintf("``%s``", value)
	}
	return fmt.Sprintf("`%s`", value)
}

func markdownDetails(summary string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}

	return fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n\n</details>", summary, body)
}

func renderMarkdownFromCLI(result tools.StructuredToolResult, cliOutput string) string {
	var output strings.Builder
	status := "success"
	if !result.Success {
		status = "failed"
	}

	fmt.Fprintf(&output, "- **Status:** %s\n", status)
	if result.Error != "" {
		fmt.Fprintf(&output, "- **Error:** %s\n", inlineCode(result.Error))
	}

	trimmedCLI := strings.TrimSpace(cliOutput)
	if trimmedCLI != "" {
		output.WriteString("\n")
		output.WriteString(fencedCodeBlock("text", trimmedCLI))
	}

	return strings.TrimSpace(output.String())
}
