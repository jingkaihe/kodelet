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
	if !result.Success {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var meta tools.ReadConversationMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	fmt.Fprintf(&output, "- **Conversation ID:** %s\n", inlineCode(meta.ConversationID))
	fmt.Fprintf(&output, "- **Goal:** %s\n", meta.Goal)

	if strings.TrimSpace(meta.Content) != "" {
		output.WriteString("\n")
		output.WriteString(meta.Content)
	}

	return strings.TrimSpace(output.String())
}
