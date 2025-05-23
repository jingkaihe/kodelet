package tools

import (
	"encoding/json"

	"github.com/sashabaranov/go-openai"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ToOpenAITools converts internal tool format to OpenAI's format
func ToOpenAITools(tools []tooltypes.Tool) []openai.Tool {
	openaiTools := make([]openai.Tool, len(tools))
	for i, tool := range tools {
		schema := tool.GenerateSchema()

		// Convert to JSON
		schemaBytes, _ := json.Marshal(schema)
		var jsonSchema map[string]interface{}
		json.Unmarshal(schemaBytes, &jsonSchema)

		openaiTools[i] = openai.Tool{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  jsonSchema,
			},
		}
	}
	return openaiTools
}
