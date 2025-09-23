// Package google provides tool conversion and execution utilities
// for Google GenAI integration.
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

// toGoogleTools converts kodelet's tools to Google's function declaration format
func toGoogleTools(tools []tooltypes.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	// Google GenAI expects all function declarations to be grouped under a single Tool
	// This is different from OpenAI which expects separate tools for each function
	var functionDeclarations []*genai.FunctionDeclaration
	for _, tool := range tools {
		schema := convertToGoogleSchema(tool.GenerateSchema())
		functionDeclarations = append(functionDeclarations, &genai.FunctionDeclaration{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  schema,
		})
	}

	// Return a single Tool containing all function declarations
	return []*genai.Tool{{
		FunctionDeclarations: functionDeclarations,
	}}
}

// convertToGoogleSchema converts a tool schema to Google's schema format
func convertToGoogleSchema(schema *jsonschema.Schema) *genai.Schema {
	googleSchema := &genai.Schema{
		Type: convertSchemaType(schema.Type),
	}

	if schema.Description != "" {
		googleSchema.Description = schema.Description
	}

	// Handle schema properties properly
	if schema.Properties != nil {
		googleSchema.Properties = make(map[string]*genai.Schema)
		// jsonschema.Properties is an *orderedmap.OrderedMap
		// Iterate through the ordered map properly
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
	// Generate a random 8-byte hex string for unique tool call IDs
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID if random generation fails
		return fmt.Sprintf("call_%d", len(fmt.Sprintf("%d", 1000000)))
	}
	return fmt.Sprintf("call_%s", hex.EncodeToString(bytes))
}

// executeToolCalls executes tool calls and handles tool results
func (t *GoogleThread) executeToolCalls(ctx context.Context, response *GoogleResponse, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) {
	var toolResultParts []*genai.Part
	
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

		// Collect tool result part for batch addition
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
	
	// Add all tool results as a single user message
	// This is required by Google GenAI - all function responses for a turn must be in one message
	if len(toolResultParts) > 0 {
		content := genai.NewContentFromParts(toolResultParts, genai.RoleUser)
		t.messages = append(t.messages, content)
	}
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