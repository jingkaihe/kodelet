package base

import (
	"slices"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/codemode"
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
	allowed := currentAllowedTools(thread)
	var available []tooltypes.Tool
	if environment := EnvironmentForThread(thread); environment != nil && environment.IsOpen() {
		available = filterAvailableTools(environment.Manifest().AvailableTools(), noToolUse, allowed)
	} else {
		available = availableTools(state, noToolUse, allowed)
	}
	return advertisedTools(thread, available)
}

// AvailableEnvironmentToolsForThread returns tools from the run-pinned environment manifest.
func AvailableEnvironmentToolsForThread(thread llmtypes.Thread, noToolUse bool) []tooltypes.Tool {
	return AvailableToolsForThread(thread, threadState(thread), noToolUse)
}

func advertisedTools(thread llmtypes.Thread, available []tooltypes.Tool) []tooltypes.Tool {
	if thread == nil || thread.GetConfig().CodeMode != "only" {
		return available
	}
	// Advertisement is separate from authorization: the full permitted catalog
	// remains callable inside code_execute, including core tools. No fallback
	// exposes those tools directly when the parent is unavailable or denied.
	// Model-only tools, such as skill, are never script-callable, so they stay
	// declared directly whenever they are permitted.
	var advertised []tooltypes.Tool
	for _, tool := range available {
		switch {
		case tool == nil:
		case tool.Name() == "code_execute":
			// The hidden tools are listed in its description instead, so the
			// model knows every callable tool without a discovery round trip.
			advertised = append(advertised, indexedCodeExecuteTool{
				Tool:        tool,
				description: tool.Description() + "\n\n" + codemode.ToolIndex(codeIndexDefinitions(thread, available)),
			})
		case tooltypes.IsModelOnly(tool):
			advertised = append(advertised, tool)
		}
	}
	return advertised
}

// indexedCodeExecuteTool advertises code_execute with the turn's callable tool
// index appended to its description. Execution still uses the pinned tool.
type indexedCodeExecuteTool struct {
	tooltypes.Tool
	description string
}

func (t indexedCodeExecuteTool) Description() string { return t.description }

// RawInputSchema keeps the parent's schema exactly as it would be advertised unwrapped.
func (t indexedCodeExecuteTool) RawInputSchema() map[string]any {
	return tooltypes.JSONSchemaForTool(t.Tool)
}

// codeCallableDefinitions returns the pinned definitions a code_execute script
// may call this turn. It drives both the callable set sent with the parent and
// the tool index in its description, so the two can never disagree.
func codeCallableDefinitions(thread llmtypes.Thread, manifest agentenv.Manifest) []agentenv.ToolDefinition {
	var callable []agentenv.ToolDefinition
	for _, definition := range manifest.Tools {
		// Model-only tools are declared directly and never callable from scripts.
		if definition.Name != "code_execute" && !definition.ModelOnly && ToolAllowedForThread(thread, definition.Name) {
			callable = append(callable, definition)
		}
	}
	return callable
}

// codeIndexDefinitions describes the tools listed in the code_execute
// description. With an open environment it uses the same pinned definitions as
// the callable set; otherwise it falls back to the advertised state tools.
func codeIndexDefinitions(thread llmtypes.Thread, available []tooltypes.Tool) []codemode.Definition {
	var definitions []codemode.Definition
	if environment := EnvironmentForThread(thread); environment != nil && environment.IsOpen() {
		for _, definition := range codeCallableDefinitions(thread, environment.Manifest()) {
			definitions = append(definitions, codemode.Definition{
				Name:         definition.Name,
				Description:  definition.Description,
				Group:        definition.Group,
				Short:        definition.Short,
				InputSchema:  definition.InputSchema,
				OutputSchema: definition.OutputSchema,
			})
		}
		return definitions
	}
	for _, tool := range available {
		if tool == nil || tool.Name() == "code_execute" || tooltypes.IsModelOnly(tool) || !ToolAllowedForThread(thread, tool.Name()) {
			continue
		}
		definition := codemode.Definition{
			Name:         tool.Name(),
			Description:  tool.Description(),
			Short:        tooltypes.ShortForTool(tool),
			InputSchema:  tooltypes.JSONSchemaForTool(tool),
			OutputSchema: tooltypes.OutputSchemaForTool(tool),
		}
		if grouped, ok := tool.(tooltypes.ToolGroupProvider); ok {
			definition.Group = grouped.ToolGroup()
		}
		definitions = append(definitions, definition)
	}
	return definitions
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
