package responses

import (
	"slices"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// buildToolsForThread creates tool definitions and honors per-turn extension tool-list patches.
func buildToolsForThread(thread llmtypes.Thread, state tooltypes.State, noToolUse bool) []responses.ToolUnionParam {
	return buildToolsWithAllowed(state, noToolUse, currentAllowedTools(thread))
}

// buildTools creates the tool definitions for the Responses API.
func buildTools(state tooltypes.State) []responses.ToolUnionParam {
	return buildToolsWithAllowed(state, false, nil)
}

func buildToolsWithAllowed(state tooltypes.State, noToolUse bool, extensionAllowedTools []string) []responses.ToolUnionParam {
	if noToolUse {
		return nil
	}

	var llmConfig llmtypesConfig
	if state != nil {
		if cfg, ok := state.GetLLMConfig().(llmtypes.Config); ok {
			platform := resolvePlatformName(cfg)
			llmConfig = llmtypesConfig{
				platform:    platform,
				baseURL:     getBaseURL(cfg),
				useCopilot:  platform == "copilot",
				allowedFile: cfg.AllowedDomainsFile,
			}
			if cfg.OpenAI != nil {
				llmConfig.enableSearch = cfg.OpenAI.EnableSearch
			}
			if len(cfg.AllowedTools) > 0 {
				llmConfig.allowedTools = append([]string(nil), cfg.AllowedTools...)
			}
		}
	}

	// Get available tools from the state
	var availableTools []tooltypes.Tool
	if state != nil {
		availableTools = state.Tools()
	}
	if extensionAllowedTools != nil {
		availableTools = filterAvailableTools(availableTools, extensionAllowedTools)
		llmConfig.allowedTools = extensionAllowedTools
	}

	result := make([]responses.ToolUnionParam, 0, len(availableTools)+1)
	if shouldEnableNativeOpenAISearch(llmConfig) && nativeOpenAISearchAllowed(llmConfig.allowedTools) {
		result = append(result, buildNativeOpenAISearchTool(llmConfig))
	}

	if len(availableTools) > 0 {
		result = append(result, toResponsesAPITools(availableTools)...)
	}

	if len(result) == 0 {
		return nil
	}

	return result
}

func filterAvailableTools(tools []tooltypes.Tool, allowed []string) []tooltypes.Tool {
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
	allowed, ok := metadata["allowed_tools"].([]string)
	if ok {
		return allowed
	}
	rawList, ok := metadata["allowed_tools"].([]any)
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

func nativeOpenAISearchAllowed(allowedTools []string) bool {
	if len(allowedTools) == 0 {
		return true
	}

	for _, toolName := range allowedTools {
		if strings.EqualFold(strings.TrimSpace(toolName), openAISearchToolName) {
			return true
		}
	}

	return false
}

// toResponsesAPITools converts internal tool definitions to Responses API format.
func toResponsesAPITools(internalTools []tooltypes.Tool) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, len(internalTools))

	for i, tool := range internalTools {
		result[i] = responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        tool.Name(),
				Description: param.NewOpt(tool.Description()),
				Parameters:  tooltypes.JSONSchemaForTool(tool),
				// Note: Strict mode requires ALL properties to be in 'required' array.
				// Our tools have optional parameters, so we disable strict mode.
				Strict: param.NewOpt(false),
			},
		}
	}

	return result
}
