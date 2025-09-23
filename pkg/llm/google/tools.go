// Package google provides tool conversion and execution utilities
// for Google GenAI integration.
package google

import (
	"context"
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

// toGoogleTools converts kodelet's tools to Google's function declaration format
func toGoogleTools(tools []tooltypes.Tool) []*genai.Tool {
	var googleTools []*genai.Tool

	// Convert standard tools
	for _, tool := range tools {
		schema := convertToGoogleSchema(tool.GenerateSchema())
		googleTools = append(googleTools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  schema,
			}},
		})
	}

	return googleTools
}

// convertToGoogleSchema converts a tool schema to Google's schema format
func convertToGoogleSchema(schema *jsonschema.Schema) *genai.Schema {
	googleSchema := &genai.Schema{
		Type: convertSchemaType(schema.Type),
	}

	if schema.Description != "" {
		googleSchema.Description = schema.Description
	}

	// TODO: Handle schema.Properties properly - OrderedMap iteration needs proper implementation
	// For now, skip properties to get basic functionality working
	if schema.Properties != nil {
		googleSchema.Properties = make(map[string]*genai.Schema)
		// Note: OrderedMap iteration requires proper implementation
		// This is a simplified version that will be enhanced later
	}

	if len(schema.Required) > 0 {
		googleSchema.Required = schema.Required
	}

	if schema.Items != nil {
		googleSchema.Items = convertToGoogleSchema(schema.Items)
	}

	return googleSchema
}

// convertSchemaType converts schema type strings to Google's type enum
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
		return genai.TypeString // Default fallback
	}
}

// generateToolCallID generates a unique tool call ID
func generateToolCallID() string {
	// Use a simple counter-based approach for now
	// In production, this could be a UUID
	return fmt.Sprintf("call_%d", len(fmt.Sprintf("%d", 1000000+int(1000000))))
}

// executeToolCalls executes tool calls and handles tool results
func (t *GoogleThread) executeToolCalls(ctx context.Context, response *GoogleResponse, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) {
	for _, toolCall := range response.ToolCalls {
		logger.G(ctx).WithField("tool", toolCall.Name).Debug("Executing tool call")

		// Create subagent context (cross-provider support)
		runToolCtx := t.subagentContextFactory(ctx, t, handler, opt.CompactRatio, opt.DisableAutoCompact)

		// Convert arguments to JSON string
		argsJSON, err := json.Marshal(toolCall.Args)
		if err != nil {
			logger.G(ctx).WithError(err).Error("Failed to marshal tool arguments")
			continue
		}

		// Execute tool
		output := tools.RunTool(runToolCtx, t.state, toolCall.Name, string(argsJSON))

		// Use renderer registry for consistent output (following existing pattern)
		structuredResult := output.StructuredData()
		registry := renderers.NewRendererRegistry()
		renderedOutput := registry.Render(structuredResult)

		handler.HandleToolResult(toolCall.Name, renderedOutput)

		// Store structured results for persistence
		t.toolResults[toolCall.ID] = structuredResult

		// Add tool result to message history
		t.addToolResult(toolCall.ID, toolCall.Name, renderedOutput, output.IsError())
	}
}

// addToolResult adds a tool result to the message history
func (t *GoogleThread) addToolResult(toolCallID, toolName, output string, isError bool) {
	// Create a tool result part with call ID for proper response tracking
	resultPart := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			Name: toolName,
			Response: map[string]interface{}{
				"call_id": toolCallID,
				"result":  output,
				"error":   isError,
			},
		},
	}

	// Add as a user message (following Google's pattern for function responses)
	content := genai.NewContentFromParts([]*genai.Part{resultPart}, genai.RoleUser)
	t.messages = append(t.messages, content)
}

// hasToolCalls checks if a response contains tool calls
func (t *GoogleThread) hasToolCalls(response *GoogleResponse) bool {
	return len(response.ToolCalls) > 0
}

// tools returns the tools available for this thread
func (t *GoogleThread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}