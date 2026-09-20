package llm

import (
	"github.com/jingkaihe/kodelet/pkg/conversations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const (
	DefaultMaxToolResultCharacters = conversations.DefaultMaxToolResultCharacters
	DefaultMaxToolResultBytes      = conversations.DefaultMaxToolResultBytes
	toolResultTruncationMarker     = conversations.ToolResultTruncationMarker
)

// ConversationMarkdownOptions controls markdown rendering for stored conversations.
type ConversationMarkdownOptions = conversations.MarkdownOptions

// RenderConversationRecordMarkdown renders archived and active display history.
func RenderConversationRecordMarkdown(record convtypes.ConversationRecord, opts ConversationMarkdownOptions) (string, error) {
	messages, err := ExtractConversationRecordEntries(record)
	if err != nil {
		return "", err
	}
	return conversations.RenderMarkdown(messages, record.ToolResults, opts), nil
}

// RenderConversationMarkdown converts a stored conversation into markdown using the
// same formatting logic as `kodelet conversation show --format markdown`.
func RenderConversationMarkdown(
	provider string,
	rawMessages []byte,
	metadata map[string]any,
	toolResults map[string]tooltypes.StructuredToolResult,
) (string, error) {
	return RenderConversationMarkdownWithOptions(provider, rawMessages, metadata, toolResults, ConversationMarkdownOptions{})
}

// RenderConversationMarkdownWithOptions converts a stored conversation into markdown
// with optional output-shaping controls.
func RenderConversationMarkdownWithOptions(
	provider string,
	rawMessages []byte,
	metadata map[string]any,
	toolResults map[string]tooltypes.StructuredToolResult,
	opts ConversationMarkdownOptions,
) (string, error) {
	messages, err := ExtractConversationEntries(provider, rawMessages, metadata, toolResults)
	if err != nil {
		return "", err
	}

	return conversations.RenderMarkdown(messages, toolResults, opts), nil
}

func renderConversationEntriesMarkdown(
	messages []conversations.StreamableMessage,
	toolResults map[string]tooltypes.StructuredToolResult,
	opts ConversationMarkdownOptions,
) string {
	return conversations.RenderMarkdown(messages, toolResults, opts)
}
