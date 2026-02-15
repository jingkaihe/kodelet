package base

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// CreateHookTrigger builds a hook trigger for a thread constructor.
// Returns a zero-value trigger when hooks are disabled or unavailable.
func CreateHookTrigger(ctx context.Context, config llmtypes.Config, conversationID string) hooks.Trigger {
	if config.IsSubAgent || config.NoHooks {
		return hooks.Trigger{}
	}

	hookManager, err := hooks.NewHookManager()
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to initialize hook manager, hooks disabled")
		return hooks.Trigger{}
	}

	return hooks.NewTrigger(hookManager, conversationID, config.IsSubAgent, config.RecipeName)
}

// RestoreBackgroundProcesses reattaches alive background processes to state.
// This is best-effort and silently skips processes that can't be reattached.
func RestoreBackgroundProcesses(state tooltypes.State, processes []tooltypes.BackgroundProcess) {
	if state == nil {
		return
	}

	for _, process := range processes {
		if !osutil.IsProcessAlive(process.PID) {
			continue
		}

		restoredProcess, err := osutil.ReattachProcess(process)
		if err != nil {
			continue
		}
		_ = state.AddBackgroundProcess(restoredProcess)
	}
}

// AvailableTools returns tools from state while handling disabled tool use and nil state.
func AvailableTools(state tooltypes.State, noToolUse bool) []tooltypes.Tool {
	if noToolUse || state == nil {
		return []tooltypes.Tool{}
	}

	return state.Tools()
}
