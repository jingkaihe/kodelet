package base

import (
	"context"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

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
		UseWeakModel:       useWeakModel,
		PromptCache:        false,
		NoToolUse:          true,
		DisableAutoCompact: true,
		DisableUsageLog:    true,
		NoSaveConversation: true,
	}
}

// GenerateShortSummary runs a summary prompt using the utility prompt runner.
// If generation fails, it calls onError (if provided) and returns a stable fallback message.
func GenerateShortSummary(
	ctx context.Context,
	prompt string,
	runUtilityPrompt func(ctx context.Context, prompt string, useWeakModel bool) (string, error),
	onError func(err error),
) string {
	summary, err := runUtilityPrompt(ctx, prompt, true)
	if err != nil {
		if onError != nil {
			onError(err)
		}
		return "Could not generate summary."
	}

	return summary
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
