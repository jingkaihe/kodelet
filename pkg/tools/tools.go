package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
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

func ToAnthropicTools(tools []Tool) []anthropic.ToolUnionParam {
	anthropicTools := make([]anthropic.ToolUnionParam, len(tools))
	for i, tool := range tools {
		anthropicTools[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name(),
				Description: anthropic.String(tool.Description()),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: tool.GenerateSchema().Properties,
				},
			},
		}
	}

	return anthropicTools
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
