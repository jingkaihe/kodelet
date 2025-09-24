package google

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
	"github.com/invopop/jsonschema"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func toGoogleTools(tools []tooltypes.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	// Google GenAI expects all function declarations grouped under a single Tool
	var functionDeclarations []*genai.FunctionDeclaration
	for _, tool := range tools {
		schema := convertToGoogleSchema(tool.GenerateSchema())
		functionDeclarations = append(functionDeclarations, &genai.FunctionDeclaration{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  schema,
		})
	}

	return []*genai.Tool{{
		FunctionDeclarations: functionDeclarations,
	}}
}
func convertToGoogleSchema(schema *jsonschema.Schema) *genai.Schema {
	googleSchema := &genai.Schema{
		Type: convertSchemaType(schema.Type),
	}

	if schema.Description != "" {
		googleSchema.Description = schema.Description
	}

	if schema.Properties != nil {
		googleSchema.Properties = make(map[string]*genai.Schema)
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			propName := pair.Key
			propSchema := pair.Value
			googleSchema.Properties[propName] = convertToGoogleSchema(propSchema)
		}
	}

	if len(schema.Required) > 0 {
		googleSchema.Required = schema.Required
	}

	if schema.Items != nil {
		googleSchema.Items = convertToGoogleSchema(schema.Items)
	}

	return googleSchema
}

func convertSchemaType(schemaType string) genai.Type {
	switch strings.ToLower(schemaType) {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeString
	}
}

func generateToolCallID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("call_%d", len(fmt.Sprintf("%d", 1000000)))
	}
	return fmt.Sprintf("call_%s", hex.EncodeToString(bytes))
}

func (t *GoogleThread) executeToolCalls(ctx context.Context, response *GoogleResponse, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) {
	var toolResultParts []*genai.Part
	
	for _, toolCall := range response.ToolCalls {
		logger.G(ctx).WithField("tool", toolCall.Name).Debug("Executing tool call")

		runToolCtx := t.subagentContextFactory(ctx, t, handler, opt.CompactRatio, opt.DisableAutoCompact)

		argsJSON, err := json.Marshal(toolCall.Args)
		if err != nil {
			logger.G(ctx).WithError(err).Error("Failed to marshal tool arguments")
			continue
		}

		output := tools.RunTool(runToolCtx, t.state, toolCall.Name, string(argsJSON))

		structuredResult := output.StructuredData()
		registry := renderers.NewRendererRegistry()
		renderedOutput := registry.Render(structuredResult)

		handler.HandleToolResult(toolCall.Name, renderedOutput)

		t.toolResults[toolCall.ID] = structuredResult
		resultPart := &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				Name: toolCall.Name,
				Response: map[string]interface{}{
					"call_id": toolCall.ID,
					"result":  renderedOutput,
					"error":   output.IsError(),
				},
			},
		}
		toolResultParts = append(toolResultParts, resultPart)
	}
	
	// Google GenAI requires all function responses for a turn in one message
	if len(toolResultParts) > 0 {
		content := genai.NewContentFromParts(toolResultParts, genai.RoleUser)
		t.messages = append(t.messages, content)
	}
}

func (t *GoogleThread) hasToolCalls(response *GoogleResponse) bool {
	return len(response.ToolCalls) > 0
}

func (t *GoogleThread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}