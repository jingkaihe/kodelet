package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/state"
)

type Tool interface {
	GenerateSchema() *jsonschema.Schema
	Name() string
	Description() string
	ValidateInput(state state.State, parameters string) error
	Execute(ctx context.Context, state state.State, parameters string) ToolResult
}

type ToolResult struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

func (t *ToolResult) String() string {
	out := ""
	if t.Error != "" {
		out = fmt.Sprintf(`<error>
%s
</error>
`, t.Error)
	}
	if t.Result != "" {
		out += fmt.Sprintf(`<result>
%s
</result>
`, t.Result)
	}
	return out
}

func GenerateSchema[T any]() *jsonschema.Schema {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T

	return reflector.Reflect(v)
}

var Tools = []Tool{
	&BashTool{},
	&FileReadTool{},
	&FileWriteTool{},
	&FileEditTool{},
	&CodeSearchTool{},
	&ThinkingTool{},
	&TodoReadTool{},
	&TodoWriteTool{},
}

// GetToolDefinitions converts tools to a generic format for LLM providers
func GetToolDefinitions(tools []Tool) []map[string]interface{} {
	toolDefs := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		schema := tool.GenerateSchema()

		// Convert properties to map
		propertiesMap := make(map[string]interface{})
		if schemaBytes, err := json.Marshal(schema.Properties); err == nil {
			json.Unmarshal(schemaBytes, &propertiesMap)
		}

		toolDefs[i] = map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters": map[string]interface{}{
				"type":       "object",
				"properties": propertiesMap,
			},
		}
	}

	return toolDefs
}

func RunTool(ctx context.Context, state state.State, toolName string, parameters string) ToolResult {
	for _, tool := range Tools {
		if tool.Name() == toolName {
			err := tool.ValidateInput(state, parameters)
			if err != nil {
				return ToolResult{
					Error: err.Error(),
				}
			}
			result := tool.Execute(ctx, state, parameters)
			if result.Error != "" {
				return result
			}
			return result
		}
	}
	return ToolResult{
		Error: fmt.Sprintf("tool not found: %s", toolName),
	}
}
