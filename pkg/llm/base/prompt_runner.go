package base

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

const shortSummaryFallbackMaxLength = 100

// UtilityThread is a thread that supports utility-mode preparation.
type UtilityThread interface {
	llmtypes.Thread
	PrepareUtilityMode(ctx context.Context)
}

// RunPreparedPrompt creates a helper thread, prepares it, sends a prompt, and collects text output.
func RunPreparedPrompt(
	ctx context.Context,
	createThread func() (llmtypes.Thread, error),
	prepareThread func(thread llmtypes.Thread) error,
	prompt string,
	opt llmtypes.MessageOpt,
) (string, error) {
	return RunPreparedPromptTyped(ctx, createThread, prepareThread, prompt, opt)
}

// RunPreparedPromptTyped is a typed variant of RunPreparedPrompt that avoids provider-side type assertions.
func RunPreparedPromptTyped[T llmtypes.Thread](
	ctx context.Context,
	createThread func() (T, error),
	prepareThread func(thread T) error,
	prompt string,
	opt llmtypes.MessageOpt,
) (string, error) {
	thread, err := createThread()
	if err != nil {
		return "", err
	}

	if prepareThread != nil {
		if err := prepareThread(thread); err != nil {
			return "", err
		}
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = thread.SendMessage(ctx, prompt, handler, opt)
	if err != nil {
		return "", err
	}

	return handler.CollectedText(), nil
}

// UtilityPromptOptions returns standard options for internal utility prompts (summary/compaction).
func UtilityPromptOptions(useWeakModel bool) llmtypes.MessageOpt {
	return llmtypes.MessageOpt{
		Initiator:          llmtypes.InitiatorAgent,
		UseWeakModel:       useWeakModel,
		PromptCache:        false,
		NoToolUse:          true,
		DisableAutoCompact: true,
		DisableUsageLog:    true,
		NoSaveConversation: true,
	}
}

// GenerateShortSummary runs a summary prompt using the utility prompt runner.
// It returns a normalized summary on success, or an error when generation fails
// or produces an empty result.
func GenerateShortSummary(
	ctx context.Context,
	markdown string,
	runUtilityPrompt func(ctx context.Context, prompt string, useWeakModel bool) (string, error),
) (string, error) {
	prompt := BuildShortSummaryPrompt(markdown)
	summary, err := runUtilityPrompt(ctx, prompt, true)
	if err != nil {
		return "", err
	}

	normalized := normalizeShortSummary(summary)
	if normalized == "" {
		return "", errors.New("generated empty summary")
	}

	return normalized, nil
}

func normalizeShortSummary(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if strings.HasSuffix(trimmed, ".") && !strings.HasSuffix(trimmed, "...") {
		trimmed = strings.TrimSuffix(trimmed, ".")
	}
	return trimmed
}

// FirstUserMessageFallback builds a user-facing conversation title from the first user text message.
func FirstUserMessageFallback(messages []conversations.StreamableMessage) string {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}

		text := strings.TrimSpace(firstUserMessageText(msg))
		if text == "" {
			continue
		}

		return truncateSummaryFallback(text)
	}

	return ""
}

func firstUserMessageText(msg conversations.StreamableMessage) string {
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content
	}

	if len(msg.RawItem) == 0 {
		return ""
	}

	return extractTextFromRawItem(msg.RawItem)
}

func extractTextFromRawItem(raw json.RawMessage) string {
	var payload struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Content) == 0 {
		return ""
	}

	var textContent string
	if err := json.Unmarshal(payload.Content, &textContent); err == nil {
		return strings.TrimSpace(textContent)
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(payload.Content, &parts); err != nil {
		return ""
	}

	textParts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text", "output_text":
			if strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		}
	}

	return strings.TrimSpace(strings.Join(textParts, "\n\n"))
}

func truncateSummaryFallback(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " "))
	if len(trimmed) <= shortSummaryFallbackMaxLength {
		return trimmed
	}

	return trimmed[:shortSummaryFallbackMaxLength-3] + "..."
}

// BuildShortSummaryPrompt wraps rendered conversation markdown in the short-summary instruction.
func BuildShortSummaryPrompt(markdown string) string {
	trimmed := strings.TrimSpace(markdown)
	return strings.TrimSpace(prompts.ShortSummaryPrompt) + "\n\nConversation to summarize:\n\n" + trimmed
}

// RenderMarkdownForSummary converts streamable messages into markdown optimized for summary generation.
func RenderMarkdownForSummary(
	messages []conversations.StreamableMessage,
	toolResults map[string]tooltypes.StructuredToolResult,
) string {
	return conversations.RenderMarkdown(messages, toolResults, conversations.MarkdownOptions{
		TruncateToolResults: true,
		ExcludeThinking:     true,
	})
}

// RunUtilityPrompt creates a helper thread, seeds provider-specific history,
// switches it to utility mode, and sends a prompt.
func RunUtilityPrompt[T UtilityThread](
	ctx context.Context,
	createThread func() (T, error),
	seedThread func(thread T),
	prompt string,
	useWeakModel bool,
) (string, error) {
	return RunPreparedPromptTyped(
		ctx,
		createThread,
		func(thread T) error {
			if seedThread != nil {
				seedThread(thread)
			}
			thread.PrepareUtilityMode(ctx)
			return nil
		},
		prompt,
		UtilityPromptOptions(useWeakModel),
	)
}
