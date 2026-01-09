package anthropic

import (
	"github.com/anthropics/anthropic-sdk-go"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func toAnthropicTools(tools []tooltypes.Tool, useSubscription bool) []anthropic.ToolUnionParam {
	anthropicTools := make([]anthropic.ToolUnionParam, len(tools))
	for i, tool := range tools {
		name := tool.Name()
		if useSubscription {
			name = capitalizeToolName(name)
		}
		anthropicTools[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        name,
				Description: anthropic.String(tool.Description()),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: tool.GenerateSchema().Properties,
				},
			},
		}
	}

	return anthropicTools
}
