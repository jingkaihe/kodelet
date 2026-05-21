package base

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// HandleGoalAutoContinuation appends a goal continuation user-context
// message and reports whether the provider loop should continue. It is called
// only when the model would otherwise stop for the current exchange.
func HandleGoalAutoContinuation(ctx context.Context, thread llmtypes.Thread) bool {
	goal, ok := goals.AutoContinuationGoal(thread.GetMetadata())
	if !ok {
		return false
	}

	contextText := goals.RenderContext(goal)
	logger.G(ctx).
		Info("active goal remains; starting automatic continuation")
	thread.AddUserMessage(ctx, contextText)
	return true
}
