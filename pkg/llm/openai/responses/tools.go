package responses

import (
	"encoding/json"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// buildTools creates the tool definitions for the Responses API.
func buildTools(state tooltypes.State, noToolUse bool) []responses.ToolUnionParam {
	if noToolUse {
		return nil
	}

	var llmConfig llmtypesConfig
	if state != nil {
		if cfg, ok := state.GetLLMConfig().(llmtypes.Config); ok {
			llmConfig = llmtypesConfig{
				platform:   resolvePlatformName(cfg),
				baseURL:    getBaseURL(cfg),
				useCopilot: cfg.UseCopilot,
				allowedFile: cfg.AllowedDomainsFile,
			}
			if cfg.OpenAI != nil {
				llmConfig.enableSearch = cfg.OpenAI.EnableSearch
			}
		}
	}

	// Get available tools from the state
	var availableTools []tooltypes.Tool
	if state != nil {
		availableTools = state.Tools()
	}

	result := make([]responses.ToolUnionParam, 0, len(availableTools)+1)
	if shouldEnableNativeOpenAISearch(llmConfig) {
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

// toResponsesAPITools converts internal tool definitions to Responses API format.
func toResponsesAPITools(internalTools []tooltypes.Tool) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, len(internalTools))

	for i, tool := range internalTools {
		schema := tool.GenerateSchema()

		// Convert to JSON and back to map[string]any
		schemaBytes, _ := json.Marshal(schema)
		var jsonSchema map[string]any
		json.Unmarshal(schemaBytes, &jsonSchema)

		result[i] = responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        tool.Name(),
				Description: param.NewOpt(tool.Description()),
				Parameters:  jsonSchema,
				// Note: Strict mode requires ALL properties to be in 'required' array.
				// Our tools have optional parameters, so we disable strict mode.
				Strict: param.NewOpt(false),
			},
		}
	}

	return result
}
