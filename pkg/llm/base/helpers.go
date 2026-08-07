package base

import (
	"slices"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const extensionAllowedToolsMetadataKey = "allowed_tools"

// AvailableTools returns tools from state while handling disabled tool use and nil state.
func AvailableTools(state tooltypes.State, noToolUse bool) []tooltypes.Tool {
	return availableTools(state, noToolUse, nil)
}

// AvailableToolsForThread returns tools filtered by any per-turn extension tool-list patch.
func AvailableToolsForThread(thread llmtypes.Thread, state tooltypes.State, noToolUse bool) []tooltypes.Tool {
	if environment := EnvironmentForThread(thread); environment != nil && environment.IsOpen() {
		return filterAvailableTools(environment.Manifest().AvailableTools(), noToolUse, currentAllowedTools(thread))
	}
	return availableTools(state, noToolUse, currentAllowedTools(thread))
}

// AvailableEnvironmentToolsForThread returns tools from the run-pinned environment manifest.
func AvailableEnvironmentToolsForThread(thread llmtypes.Thread, noToolUse bool) []tooltypes.Tool {
	environment := EnvironmentForThread(thread)
	if environment == nil || !environment.IsOpen() {
		return availableTools(threadState(thread), noToolUse, currentAllowedTools(thread))
	}
	return filterAvailableTools(environment.Manifest().AvailableTools(), noToolUse, currentAllowedTools(thread))
}

func availableTools(state tooltypes.State, noToolUse bool, allowed []string) []tooltypes.Tool {
	if noToolUse || state == nil {
		return []tooltypes.Tool{}
	}
	return filterAvailableTools(state.Tools(), false, allowed)
}

func filterAvailableTools(tools []tooltypes.Tool, noToolUse bool, allowed []string) []tooltypes.Tool {
	if noToolUse {
		return []tooltypes.Tool{}
	}
	if allowed == nil {
		return tools
	}

	filtered := make([]tooltypes.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool != nil && slices.Contains(allowed, tool.Name()) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func currentAllowedTools(thread llmtypes.Thread) []string {
	if thread == nil {
		return nil
	}
	metadata := thread.GetMetadata()
	allowed, ok := metadata[extensionAllowedToolsMetadataKey].([]string)
	if ok {
		return allowed
	}
	rawList, ok := metadata[extensionAllowedToolsMetadataKey].([]any)
	if !ok {
		return nil
	}
	converted := make([]string, 0, len(rawList))
	for _, raw := range rawList {
		if name, ok := raw.(string); ok {
			converted = append(converted, name)
		}
	}
	return converted
}
