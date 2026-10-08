package base

import (
	"slices"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const extensionAllowedToolsMetadataKey = "allowed_tools"

// ToolAllowedForThread checks host/request restrictions and extension tool-list patches.
// Provider-native tools must use this gate because they bypass the runner tool catalog.
func ToolAllowedForThread(thread llmtypes.Thread, name string) bool {
	allowed := currentAllowedTools(thread)
	return thread.GetConfig().EnvironmentOptions().ToolAllowed(name) &&
		(allowed == nil || slices.Contains(allowed, name))
}

// AvailableTools returns tools from state while handling disabled tool use and nil state.
func AvailableTools(state tooltypes.State, noToolUse bool) []tooltypes.Tool {
	return availableTools(state, noToolUse, nil)
}

// AvailableToolsForThread returns tools filtered by any per-turn extension tool-list patch.
func AvailableToolsForThread(thread llmtypes.Thread, state tooltypes.State, noToolUse bool) []tooltypes.Tool {
	if environment := EnvironmentForThread(thread); environment != nil && environment.IsOpen() {
		return advertisedTools(thread, filterAvailableTools(environment.Manifest().AvailableTools(), noToolUse, currentAllowedTools(thread)))
	}
	return advertisedTools(thread, availableTools(state, noToolUse, currentAllowedTools(thread)))
}

// AvailableEnvironmentToolsForThread returns tools from the run-pinned environment manifest.
func AvailableEnvironmentToolsForThread(thread llmtypes.Thread, noToolUse bool) []tooltypes.Tool {
	environment := EnvironmentForThread(thread)
	if environment == nil || !environment.IsOpen() {
		return advertisedTools(thread, availableTools(threadState(thread), noToolUse, currentAllowedTools(thread)))
	}
	return advertisedTools(thread, filterAvailableTools(environment.Manifest().AvailableTools(), noToolUse, currentAllowedTools(thread)))
}

func advertisedTools(thread llmtypes.Thread, available []tooltypes.Tool) []tooltypes.Tool {
	if thread == nil || thread.GetConfig().CodeMode != "compact" || !slices.ContainsFunc(available, func(tool tooltypes.Tool) bool {
		return tool != nil && tool.Name() == "code_execute"
	}) {
		return available
	}
	result := make([]tooltypes.Tool, 0, len(available))
	for _, tool := range available {
		if grouped, ok := tool.(tooltypes.ToolGroupProvider); ok && grouped.ToolGroup() != "" {
			continue
		}
		result = append(result, tool)
	}
	return result
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
