package base

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const updateGoalToolName = "update_goal"

// HandleGoalAutoContinuation appends a goal continuation user-context
// message and reports whether the provider loop should continue. It is called
// only when the model would otherwise stop for the current exchange.
func HandleGoalAutoContinuation(ctx context.Context, thread llmtypes.Thread, availableTools []tooltypes.Tool) bool {
	goal, ok := goals.AutoContinuationGoal(thread.GetMetadata())
	if !ok {
		return false
	}
	if !hasTool(availableTools, updateGoalToolName) {
		logger.G(ctx).
			WithField("tool", updateGoalToolName).
			Info("active goal remains but goal update tool is unavailable; skipping automatic continuation")
		return false
	}

	contextText := goals.RenderContext(goal)
	logger.G(ctx).
		Info("active goal remains; starting automatic continuation")
	thread.AddUserMessage(ctx, contextText)
	return true
}

func hasTool(tools []tooltypes.Tool, name string) bool {
	for _, tool := range tools {
		if tool != nil && tool.Name() == name {
			return true
		}
	}
	return false
}
