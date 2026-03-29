package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ReadConversationRenderer renders read_conversation tool results.
type ReadConversationRenderer struct{}

// RenderCLI renders read_conversation results in CLI format.
func (r *ReadConversationRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.ReadConversationMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for read_conversation"
	}

	if strings.TrimSpace(meta.Content) == "" {
		return fmt.Sprintf("Read conversation: %s\nGoal: %s", meta.ConversationID, meta.Goal)
	}

	return fmt.Sprintf("Read conversation: %s\nGoal: %s\n\n%s", meta.ConversationID, meta.Goal, meta.Content)
}

// RenderMarkdown renders read_conversation results in markdown format.
func (r *ReadConversationRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, true)
}

// RenderToolUseMarkdown renders read_conversation invocation inputs in markdown format.
func (r *ReadConversationRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.ReadConversationInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Conversation ID:** %s\n", inlineCode(input.ConversationID))
	fmt.Fprintf(&output, "- **Goal:** %s", sanitizeMarkdownText(input.Goal))

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders read_conversation results for the merged tool-call view.
func (r *ReadConversationRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, false)
}

func (r *ReadConversationRenderer) renderMarkdown(result tools.StructuredToolResult, includeContext bool) string {
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.ReadConversationMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	if includeContext {
		fmt.Fprintf(&output, "- **Conversation ID:** %s\n", inlineCode(meta.ConversationID))
		fmt.Fprintf(&output, "- **Goal:** %s\n", sanitizeMarkdownText(meta.Goal))
	}

	if strings.TrimSpace(meta.Content) != "" {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString(meta.Content)
	}

	return strings.TrimSpace(output.String())
}
