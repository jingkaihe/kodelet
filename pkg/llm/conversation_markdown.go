package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// RenderConversationMarkdown converts a stored conversation into markdown using the
// same formatting logic as `kodelet conversation show --format markdown`.
func RenderConversationMarkdown(
	provider string,
	rawMessages []byte,
	metadata map[string]any,
	toolResults map[string]tooltypes.StructuredToolResult,
) (string, error) {
	messages, err := ExtractConversationEntries(provider, rawMessages, metadata, toolResults)
	if err != nil {
		return "", err
	}

	return renderConversationEntriesMarkdown(messages, toolResults), nil
}

func renderConversationEntriesMarkdown(messages []conversations.StreamableMessage, toolResults map[string]tooltypes.StructuredToolResult) string {
	var output strings.Builder
	output.WriteString("## Messages\n\n")

	registry := renderers.NewRendererRegistry()
	consumedResults := make(map[int]struct{})

	for i, msg := range messages {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}

		if i > 0 {
			output.WriteString("\n")
		}

		if msg.Kind == "tool-use" {
			resultIndex, resultMsg, hasResult := findMatchingToolResult(messages, i, consumedResults)
			fmt.Fprintf(&output, "### %s\n\n", markdownRoleHeading(msg.Role, "tool-invocation"))
			output.WriteString(renderToolInvocationMarkdown(msg, resultMsg, hasResult, toolResults, registry))
			if hasResult {
				consumedResults[resultIndex] = struct{}{}
			}
			continue
		}

		heading := markdownRoleHeading(msg.Role, msg.Kind)
		fmt.Fprintf(&output, "### %s\n\n", heading)

		switch msg.Kind {
		case "text":
			output.WriteString(renderTextBlockMarkdown(msg))
		case "thinking":
			output.WriteString("<details>\n<summary>Thinking</summary>\n\n")
			output.WriteString(markdownCodeFence("text", msg.Content))
			output.WriteString("\n\n</details>")
		case "tool-result":
			output.WriteString(renderToolResultMarkdown(msg, toolResults, registry))
		default:
			output.WriteString(renderTextBlockMarkdown(msg))
		}
	}

	if len(messages) == 0 {
		output.WriteString("_No messages._\n")
	}

	return strings.TrimRight(output.String(), "\n") + "\n"
}

func markdownRoleHeading(role string, kind string) string {
	base := "Message"
	switch role {
	case "user":
		base = "User"
	case "assistant":
		base = "Assistant"
	case "system":
		base = "System"
	default:
		if role != "" {
			base = strings.ToUpper(role[:1]) + role[1:]
		}
	}

	switch kind {
	case "thinking":
		return base + " · Thinking"
	case "tool-invocation":
		return base + " · Tool"
	case "tool-use":
		return base + " · Tool Call"
	case "tool-result":
		return base + " · Tool Result"
	default:
		return base
	}
}

func renderTextBlockMarkdown(msg conversations.StreamableMessage) string {
	if rendered := renderOpenAIResponsesRawItemMarkdown(msg.RawItem); strings.TrimSpace(rendered) != "" {
		return rendered
	}

	trimmed := strings.TrimSpace(msg.Content)
	if trimmed == "" {
		return "_Empty message._"
	}
	return msg.Content
}

func renderOpenAIResponsesRawItemMarkdown(rawItem json.RawMessage) string {
	if len(rawItem) == 0 {
		return ""
	}

	var rawMessage struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(rawItem, &rawMessage); err != nil || len(rawMessage.Content) == 0 {
		return ""
	}

	var textContent string
	if err := json.Unmarshal(rawMessage.Content, &textContent); err == nil {
		return textContent
	}

	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL string `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(rawMessage.Content, &parts); err != nil {
		return ""
	}

	renderedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			if strings.TrimSpace(part.Text) != "" {
				renderedParts = append(renderedParts, part.Text)
			}
		case "input_image":
			if imageMarkdown := renderInputImageMarkdown(part.ImageURL); imageMarkdown != "" {
				renderedParts = append(renderedParts, imageMarkdown)
			}
		}
	}

	return strings.Join(renderedParts, "\n\n")
}

func renderInputImageMarkdown(imageURL string) string {
	if imageURL == "" {
		return ""
	}

	if strings.HasPrefix(imageURL, "data:") {
		mediaType := mediaTypeFromDataURL(imageURL)
		if mediaType == "" {
			return "_Inline image input._"
		}
		return fmt.Sprintf("_Inline image input (%s)._", mediaType)
	}

	return fmt.Sprintf("Image input: <%s>", imageURL)
}

func mediaTypeFromDataURL(dataURL string) string {
	if !strings.HasPrefix(dataURL, "data:") {
		return ""
	}

	metadata, _, found := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
	if !found {
		return ""
	}

	mediaType, _, _ := strings.Cut(metadata, ";")
	return mediaType
}

func renderToolInvocationMarkdown(
	toolUse conversations.StreamableMessage,
	toolResult conversations.StreamableMessage,
	hasResult bool,
	toolResults map[string]tooltypes.StructuredToolResult,
	registry *renderers.RendererRegistry,
) string {
	var output strings.Builder
	toolName := toolUse.ToolName
	if toolName == "" && hasResult {
		toolName = inferToolNameFromResult(toolResult, toolResults)
	}

	if toolName != "" {
		fmt.Fprintf(&output, "- **Tool:** %s\n", inlineMarkdownCode(toolName))
	}
	if toolUse.ToolCallID != "" {
		fmt.Fprintf(&output, "- **Call ID:** %s\n", inlineMarkdownCode(toolUse.ToolCallID))
	}

	renderedInput := renderToolInputMarkdown(toolUse.ToolName, toolUse.Input)
	if strings.TrimSpace(renderedInput) != "" {
		output.WriteString("\n")
		output.WriteString(renderedInput)
	}

	if hasResult {
		resultBody := renderMergedToolResultMarkdown(toolUse, toolResult, toolResults, registry)
		if strings.TrimSpace(resultBody) != "" {
			output.WriteString("\n\n**Result**\n\n")
			output.WriteString(resultBody)
		}
	}

	return strings.TrimSpace(output.String())
}

func renderMergedToolResultMarkdown(
	toolUse conversations.StreamableMessage,
	toolResult conversations.StreamableMessage,
	toolResults map[string]tooltypes.StructuredToolResult,
	registry *renderers.RendererRegistry,
) string {
	structuredResult, ok := lookupStructuredToolResult(toolResult, toolResults)
	if !ok {
		trimmed := strings.TrimSpace(toolResult.Content)
		if trimmed == "" {
			return ""
		}
		language := "text"
		if json.Valid([]byte(trimmed)) {
			language = "json"
			trimmed = trimJSONForMarkdown(trimmed)
		}
		return markdownCodeFence(language, trimmed)
	}

	if structuredResult.ToolName == "bash" {
		return renderMergedBashResultMarkdown(toolUse, structuredResult)
	}

	resultBody := registry.RenderMarkdown(structuredResult)
	return stripLeadingMarkdownMetadata(resultBody, map[string]struct{}{
		"Tool":    {},
		"Call ID": {},
	})
}

func renderMergedBashResultMarkdown(toolUse conversations.StreamableMessage, result tooltypes.StructuredToolResult) string {
	var meta tooltypes.BashMetadata
	if !tooltypes.ExtractMetadata(result.Metadata, &meta) {
		return stripLeadingMarkdownMetadata(renderers.NewRendererRegistry().RenderMarkdown(result), map[string]struct{}{
			"Tool":    {},
			"Call ID": {},
			"Command": {},
		})
	}

	var output strings.Builder
	status := "success"
	if !result.Success {
		status = "failed"
	}
	fmt.Fprintf(&output, "- **Status:** %s\n", status)
	fmt.Fprintf(&output, "- **Exit code:** %d\n", meta.ExitCode)
	if meta.WorkingDir != "" {
		fmt.Fprintf(&output, "- **Working directory:** %s\n", inlineMarkdownCode(meta.WorkingDir))
	}
	fmt.Fprintf(&output, "- **Execution time:** %s\n", inlineMarkdownCode(meta.ExecutionTime.String()))
	if result.Error != "" {
		fmt.Fprintf(&output, "- **Error:** %s\n", inlineMarkdownCode(result.Error))
	}

	if strings.TrimSpace(meta.Output) != "" {
		output.WriteString("\n**Output**\n\n")
		output.WriteString(markdownCodeFence("text", meta.Output))
	}

	_ = toolUse
	return strings.TrimSpace(output.String())
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

func findMatchingToolResult(
	messages []conversations.StreamableMessage,
	toolUseIndex int,
	consumedResults map[int]struct{},
) (int, conversations.StreamableMessage, bool) {
	toolUse := messages[toolUseIndex]
	for i := toolUseIndex + 1; i < len(messages); i++ {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}
		candidate := messages[i]
		if candidate.Kind != "tool-result" {
			continue
		}
		if toolUse.ToolCallID != "" && candidate.ToolCallID == toolUse.ToolCallID {
			return i, candidate, true
		}
		if toolUse.ToolCallID == "" && toolUse.ToolName != "" && candidate.ToolName == toolUse.ToolName {
			return i, candidate, true
		}
	}

	return -1, conversations.StreamableMessage{}, false
}

func renderToolResultMarkdown(msg conversations.StreamableMessage, toolResults map[string]tooltypes.StructuredToolResult, registry *renderers.RendererRegistry) string {
	var output strings.Builder
	toolName := msg.ToolName
	if toolName == "" {
		toolName = inferToolNameFromResult(msg, toolResults)
	}
	if toolName != "" {
		fmt.Fprintf(&output, "- **Tool:** %s\n", inlineMarkdownCode(toolName))
	}
	if msg.ToolCallID != "" {
		fmt.Fprintf(&output, "- **Call ID:** %s\n", inlineMarkdownCode(msg.ToolCallID))
	}

	if structuredResult, ok := lookupStructuredToolResult(msg, toolResults); ok {
		output.WriteString("\n")
		output.WriteString(registry.RenderMarkdown(structuredResult))
		return strings.TrimSpace(output.String())
	}

	trimmed := strings.TrimSpace(msg.Content)
	if trimmed != "" {
		output.WriteString("\n")
		language := "text"
		if json.Valid([]byte(trimmed)) {
			language = "json"
			trimmed = trimJSONForMarkdown(trimmed)
		}
		output.WriteString(markdownCodeFence(language, trimmed))
	}

	return strings.TrimSpace(output.String())
}

func renderToolInputMarkdown(toolName string, rawInput string) string {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return ""
	}

	type bashInput struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		Timeout     int    `json:"timeout"`
	}
	type fileReadInput struct {
		FilePath  string `json:"file_path"`
		Offset    int    `json:"offset"`
		LineLimit int    `json:"line_limit"`
	}
	type fileWriteInput struct {
		FilePath string `json:"file_path"`
		Text     string `json:"text"`
	}
	type fileEditInput struct {
		FilePath   string `json:"file_path"`
		OldText    string `json:"old_text"`
		NewText    string `json:"new_text"`
		ReplaceAll bool   `json:"replace_all"`
	}
	type applyPatchInput struct {
		Input string `json:"input"`
	}
	type grepInput struct {
		Pattern       string `json:"pattern"`
		Path          string `json:"path"`
		Include       string `json:"include"`
		IgnoreCase    bool   `json:"ignore_case"`
		FixedStrings  bool   `json:"fixed_strings"`
		SurroundLines int    `json:"surround_lines"`
		MaxResults    int    `json:"max_results"`
	}
	type globInput struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		IgnoreGitignore bool   `json:"ignore_gitignore"`
	}

	var output strings.Builder
	switch toolName {
	case "bash":
		var input bashInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			if input.Description != "" {
				fmt.Fprintf(&output, "- **Description:** %s\n", sanitizeMarkdownText(input.Description))
			}
			if input.Timeout > 0 {
				fmt.Fprintf(&output, "- **Timeout:** %d seconds\n", input.Timeout)
			}
			output.WriteString("\n**Command**\n\n")
			output.WriteString(markdownCodeFence("bash", input.Command))
			return strings.TrimSpace(output.String())
		}
	case "file_read":
		var input fileReadInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.FilePath))
			if input.Offset > 0 {
				fmt.Fprintf(&output, "- **Offset:** %d\n", input.Offset)
			}
			if input.LineLimit > 0 {
				fmt.Fprintf(&output, "- **Line limit:** %d\n", input.LineLimit)
			}
			return strings.TrimSpace(output.String())
		}
	case "file_write":
		var input fileWriteInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.FilePath))
			output.WriteString("\n")
			output.WriteString(markdownDetails("Requested content", markdownCodeFence("text", input.Text)))
			return strings.TrimSpace(output.String())
		}
	case "file_edit":
		var input fileEditInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.FilePath))
			if input.ReplaceAll {
				output.WriteString("- **Mode:** replace all\n")
			} else {
				output.WriteString("- **Mode:** targeted edit\n")
			}
			output.WriteString("\n")
			var request strings.Builder
			request.WriteString("**Old text**\n\n")
			request.WriteString(markdownCodeFence("text", input.OldText))
			request.WriteString("\n\n**New text**\n\n")
			request.WriteString(markdownCodeFence("text", input.NewText))
			output.WriteString(markdownDetails("Requested edit", request.String()))
			return strings.TrimSpace(output.String())
		}
	case "apply_patch":
		var input applyPatchInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			operations := summarizeApplyPatchInput(input.Input)
			if len(operations) == 0 {
				return markdownDetails("Original patch", markdownCodeFence("diff", input.Input))
			}

			fmt.Fprintf(&output, "- **Patch operations:** %d\n", len(operations))
			for _, op := range operations {
				fmt.Fprintf(&output, "- %s\n", op)
			}
			output.WriteString("\n")
			output.WriteString(markdownDetails("Original patch", markdownCodeFence("diff", input.Input)))
			return strings.TrimSpace(output.String())
		}
	case "grep_tool":
		var input grepInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineMarkdownCode(input.Pattern))
			if input.Path != "" {
				fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.Path))
			}
			if input.Include != "" {
				fmt.Fprintf(&output, "- **Include:** %s\n", inlineMarkdownCode(input.Include))
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
	case "glob_tool":
		var input globInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineMarkdownCode(input.Pattern))
			if input.Path != "" {
				fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.Path))
			}
			if input.IgnoreGitignore {
				output.WriteString("- **Ignore .gitignore:** true\n")
			}
			return strings.TrimSpace(output.String())
		}
	}

	return markdownCodeFence("json", trimJSONForMarkdown(trimmed))
}

func lookupStructuredToolResult(msg conversations.StreamableMessage, toolResults map[string]tooltypes.StructuredToolResult) (tooltypes.StructuredToolResult, bool) {
	if msg.ToolCallID != "" {
		if result, ok := toolResults[msg.ToolCallID]; ok {
			return result, true
		}
	}
	if msg.ToolName != "" {
		if result, ok := toolResults[msg.ToolName]; ok {
			return result, true
		}
	}
	return tooltypes.StructuredToolResult{}, false
}

func inferToolNameFromResult(msg conversations.StreamableMessage, toolResults map[string]tooltypes.StructuredToolResult) string {
	if result, ok := lookupStructuredToolResult(msg, toolResults); ok {
		return result.ToolName
	}
	return msg.ToolName
}

func markdownCodeFence(language string, content string) string {
	return renderers.FencedCodeBlock(language, content)
}

func markdownDetails(summary string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n\n</details>", summary, body)
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
				operations = append(operations, fmt.Sprintf("Add %s", inlineMarkdownCode(path)))
			}
			currentUpdatePath = ""
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if path != "" {
				operations = append(operations, fmt.Sprintf("Delete %s", inlineMarkdownCode(path)))
			}
			currentUpdatePath = ""
		case strings.HasPrefix(line, "*** Update File: "):
			currentUpdatePath = strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			if currentUpdatePath != "" {
				operations = append(operations, fmt.Sprintf("Update %s", inlineMarkdownCode(currentUpdatePath)))
			}
		case strings.HasPrefix(line, "*** Move to: "):
			movePath := strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
			if currentUpdatePath != "" && movePath != "" && len(operations) > 0 {
				operations[len(operations)-1] = fmt.Sprintf("Update %s → %s", inlineMarkdownCode(currentUpdatePath), inlineMarkdownCode(movePath))
			}
		}
	}

	return operations
}

func inlineMarkdownCode(value string) string {
	if strings.Contains(value, "`") {
		return fmt.Sprintf("``%s``", value)
	}
	return fmt.Sprintf("`%s`", value)
}

func sanitizeMarkdownText(value string) string {
	return strings.ReplaceAll(value, "\n", " ")
}

func trimJSONForMarkdown(value string) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(value), "", "  "); err == nil {
		return pretty.String()
	}
	return value
}
