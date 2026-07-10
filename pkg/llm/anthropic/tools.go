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
				InputSchema: anthropicInputSchema(tool),
			},
		}
	}

	return anthropicTools
}

func anthropicInputSchema(tool tooltypes.Tool) anthropic.ToolInputSchemaParam {
	raw := tooltypes.JSONSchemaForTool(tool)
	schema := anthropic.ToolInputSchemaParam{
		Properties:  raw["properties"],
		Required:    schemaRequiredStrings(raw["required"]),
		ExtraFields: make(map[string]any),
	}
	for key, value := range raw {
		switch key {
		case "type", "properties", "required":
			continue
		default:
			schema.ExtraFields[key] = value
		}
	}
	return schema
}

func schemaRequiredStrings(value any) []string {
	values, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return strings
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok {
			result = append(result, item)
		}
	}
	return result
}
