package base

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const conversationNameMaxLength = 100

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
	if closer, ok := any(thread).(interface{ Close() error }); ok {
		defer func() {
			_ = closer.Close()
		}()
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

// UtilityPromptOptions returns standard options for internal utility prompts such as compaction.
func UtilityPromptOptions(useWeakModel bool) llmtypes.MessageOpt {
	return llmtypes.MessageOpt{
		Initiator:          llmtypes.InitiatorAgent,
		UseWeakModel:       useWeakModel,
		PromptCache:        false,
		NoToolUse:          true,
		DisableUsageLog:    true,
		NoSaveConversation: true,
	}
}

// FirstUserMessageName builds a deterministic conversation name from the first user text message.
func FirstUserMessageName(messages []conversations.StreamableMessage) string {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}

		text := strings.TrimSpace(firstUserMessageText(msg))
		if text == "" {
			continue
		}

		return truncateConversationName(text)
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

func truncateConversationName(text string) string {
	trimmed := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(trimmed) <= conversationNameMaxLength {
		return trimmed
	}

	runes := []rune(trimmed)
	return string(runes[:conversationNameMaxLength-3]) + "..."
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
