package renderers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// MarkdownRenderer renders structured tool results to markdown.
type MarkdownRenderer interface {
	RenderMarkdown(result tools.StructuredToolResult) string
}

// ToolUseMarkdownRenderer renders tool invocation inputs to markdown.
type ToolUseMarkdownRenderer interface {
	RenderToolUseMarkdown(rawInput string) string
}

// MergedMarkdownRenderer renders a tool result for the merged tool-call view.
type MergedMarkdownRenderer interface {
	RenderMergedMarkdown(result tools.StructuredToolResult) string
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

func decodeToolInput(rawInput string, dest any) bool {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return false
	}

	return json.Unmarshal([]byte(trimmed), dest) == nil
}

func renderToolUseJSONFallback(rawInput string) string {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return ""
	}

	return fencedCodeBlock("json", trimJSONForMarkdown(trimmed))
}

func trimJSONForMarkdown(value string) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(value), "", "  "); err == nil {
		return pretty.String()
	}

	return value
}

func stripLeadingMarkdownMetadata(input string, keys map[string]struct{}) string {
	lines := strings.Split(input, "\n")
	trimmed := make([]string, 0, len(lines))
	stripping := true

	for _, line := range lines {
		if !stripping {
			trimmed = append(trimmed, line)
			continue
		}

		lineTrimmed := strings.TrimSpace(line)
		if lineTrimmed == "" {
			continue
		}
		if !strings.HasPrefix(lineTrimmed, "- **") {
			stripping = false
			trimmed = append(trimmed, line)
			continue
		}

		key, ok := parseMarkdownMetadataKey(lineTrimmed)
		if !ok {
			stripping = false
			trimmed = append(trimmed, line)
			continue
		}
		if _, skip := keys[key]; skip {
			continue
		}
		trimmed = append(trimmed, line)
	}

	return strings.TrimSpace(strings.Join(trimmed, "\n"))
}

func parseMarkdownMetadataKey(line string) (string, bool) {
	if !strings.HasPrefix(line, "- **") {
		return "", false
	}

	rest := strings.TrimPrefix(line, "- **")
	idx := strings.Index(rest, ":**")
	if idx < 0 {
		return "", false
	}

	return rest[:idx], true
}

func sanitizeMarkdownText(value string) string {
	return strings.ReplaceAll(value, "\n", " ")
}

func summarizeApplyPatchInput(input string) []string {
	lines := strings.Split(input, "\n")
	operations := make([]string, 0)
	currentUpdatePath := ""

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			if path != "" {
				operations = append(operations, fmt.Sprintf("Add %s", inlineCode(path)))
			}
			currentUpdatePath = ""
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if path != "" {
				operations = append(operations, fmt.Sprintf("Delete %s", inlineCode(path)))
			}
			currentUpdatePath = ""
		case strings.HasPrefix(line, "*** Update File: "):
			currentUpdatePath = strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			if currentUpdatePath != "" {
				operations = append(operations, fmt.Sprintf("Update %s", inlineCode(currentUpdatePath)))
			}
		case strings.HasPrefix(line, "*** Move to: "):
			movePath := strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
			if currentUpdatePath != "" && movePath != "" && len(operations) > 0 {
				operations[len(operations)-1] = fmt.Sprintf("Update %s -> %s", inlineCode(currentUpdatePath), inlineCode(movePath))
			}
		}
	}

	return operations
}
