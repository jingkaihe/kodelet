package base

import (
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// AvailableTools returns tools from state while handling disabled tool use and nil state.
func AvailableTools(state tooltypes.State, noToolUse bool) []tooltypes.Tool {
	if noToolUse || state == nil {
		return []tooltypes.Tool{}
	}

	return state.Tools()
}
