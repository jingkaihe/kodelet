package anthropic

import (
	"unicode"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// capitalizeToolName capitalizes the first letter of a tool name.
// Uses Unicode-safe rune handling for proper multi-byte character support.
func capitalizeToolName(name string) string {
	if name == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(r)) + name[size:]
}

// decapitalizeToolName lowercases the first letter of a tool name.
// Uses Unicode-safe rune handling for proper multi-byte character support.
func decapitalizeToolName(name string) string {
	if name == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToLower(r)) + name[size:]
}

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
