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

// FencedCodeBlock wraps content in a markdown code fence longer than any backtick run inside it.
func FencedCodeBlock(language string, content string) string {
	fence := markdownFence(content)
	trimmed := strings.TrimSuffix(content, "\n")
	if language != "" {
		return fmt.Sprintf("%s%s\n%s\n%s", fence, language, trimmed, fence)
	}
	return fmt.Sprintf("%s\n%s\n%s", fence, trimmed, fence)
}

func fencedCodeBlock(language string, content string) string {
	return FencedCodeBlock(language, content)
}

func markdownFence(content string) string {
	maxFenceLen := 3
	for i := 0; i < len(content); {
		if content[i] != '`' {
			i++
			continue
		}

		j := i
		for j < len(content) && content[j] == '`' {
			j++
		}

		if runLen := j - i; runLen >= maxFenceLen {
			maxFenceLen = runLen + 1
		}
		i = j
	}

	return strings.Repeat("`", maxFenceLen)
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
