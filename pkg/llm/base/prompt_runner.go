package base

import (
	"context"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// RunPreparedPrompt creates a helper thread, prepares it, sends a prompt, and collects text output.
func RunPreparedPrompt(
	ctx context.Context,
	createThread func() (llmtypes.Thread, error),
	prepareThread func(thread llmtypes.Thread) error,
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
