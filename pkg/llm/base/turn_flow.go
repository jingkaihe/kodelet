package base

import (
	"context"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// TriggerTurnEnd notifies hooks when assistant output is finalized for a turn.
func TriggerTurnEnd(
	ctx context.Context,
	trigger hooks.Trigger,
	thread llmtypes.Thread,
	finalOutput string,
	turnCount int,
) {
	if finalOutput == "" {
		return
	}
	trigger.TriggerTurnEnd(ctx, thread, finalOutput, turnCount, thread.GetRecipeHooks())
}

// HandleAgentStopFollowUps checks agent_stop hooks and appends any follow-up user messages.
// Returns true when follow-ups were added and the caller should continue the loop.
func HandleAgentStopFollowUps(
	ctx context.Context,
	trigger hooks.Trigger,
	thread llmtypes.Thread,
	handler llmtypes.MessageHandler,
) bool {
	logger.G(ctx).Debug("no tools used, checking agent_stop hook")

	messages, err := thread.GetMessages()
	if err != nil {
		return false
	}

	followUps := trigger.TriggerAgentStop(ctx, thread, messages, thread.GetRecipeHooks())
	if len(followUps) == 0 {
		return false
	}

	logger.G(ctx).WithField("count", len(followUps)).Info("agent_stop hook returned follow-up messages, continuing conversation")
	for _, msg := range followUps {
		thread.AddUserMessage(ctx, msg)
		handler.HandleText(fmt.Sprintf("\n📨 Hook follow-up: %s\n", msg))
	}

	return true
}
