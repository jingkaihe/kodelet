// Package tools provides the core tool execution framework for Kodelet.
// It defines the available tools, manages tool registration, and handles
// tool execution with proper validation, tracing, and error handling.
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func GenerateSchema[T any]() *jsonschema.Schema {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T

	return reflector.Reflect(v)
}

// toolRegistry holds all available tools mapped by their names
var toolRegistry = map[string]tooltypes.Tool{
	"bash":                      &BashTool{},
	"file_read":                 &FileReadTool{},
	"file_write":                &FileWriteTool{},
	"file_edit":                 &FileEditTool{},
	"thinking":                  &ThinkingTool{},
	"subagent":                  &SubAgentTool{},
	"grep_tool":                 &GrepTool{},
	"glob_tool":                 &GlobTool{},
	"todo_read":                 &TodoReadTool{},
	"todo_write":                &TodoWriteTool{},
	"web_fetch":                 &WebFetchTool{},
	"image_recognition":         &ImageRecognitionTool{},
	"view_background_processes": &ViewBackgroundProcessesTool{},
}

// metaTools are always enabled regardless of configuration
var metaTools = []string{
	"file_read",
	"grep_tool",
	"glob_tool",
	"thinking",
}



// defaultMainTools are the default tools for main agent
var defaultMainTools = []string{
	"bash",
	"file_read",
	"file_write",
	"file_edit",
	"thinking",
	"subagent",
	"grep_tool",
	"glob_tool",
	"todo_read",
	"todo_write",
	"web_fetch",
	"image_recognition",
	"view_background_processes",
}

// defaultSubAgentTools are the default tools for subagent
var defaultSubAgentTools = []string{
	"bash",
	"file_read",
	"file_write",
	"file_edit",
	"grep_tool",
	"glob_tool",
	"thinking",
	"todo_read",
	"todo_write",
	"web_fetch",
}

// getAvailableToolNames returns a list of all available tool names
func getAvailableToolNames() []string {
	var tools []string
	for toolName := range toolRegistry {
		tools = append(tools, toolName)
	}
	return tools
}

// getAvailableSubAgentToolNames returns a list of available tool names for subagents (excluding subagent tool)
func getAvailableSubAgentToolNames() []string {
	var tools []string
	for toolName := range toolRegistry {
		if toolName != "subagent" {
			tools = append(tools, toolName)
		}
	}
	return tools
}

func ValidateTools(toolNames []string) error {
	var unknownTools []string
	for _, toolName := range toolNames {
		if _, exists := toolRegistry[toolName]; !exists {
			unknownTools = append(unknownTools, toolName)
		}
	}

	if len(unknownTools) > 0 {
		availableTools := getAvailableToolNames()
		if len(unknownTools) == 1 {
			return errors.Errorf("unknown tool: %s\nAvailable tools: %s", unknownTools[0], strings.Join(availableTools, ", "))
		}
		return errors.Errorf("unknown tools: %s\nAvailable tools: %s", strings.Join(unknownTools, ", "), strings.Join(availableTools, ", "))
	}
	return nil
}

func ValidateSubAgentTools(toolNames []string) error {
	var invalidTools []string
	var subagentToolFound bool

	for _, toolName := range toolNames {
		if toolName == "subagent" {
			subagentToolFound = true
			invalidTools = append(invalidTools, toolName)
		} else if _, exists := toolRegistry[toolName]; !exists {
			invalidTools = append(invalidTools, toolName)
		}
	}

	if len(invalidTools) > 0 {
		availableTools := getAvailableSubAgentToolNames()

		if subagentToolFound && len(invalidTools) == 1 {
			return errors.Errorf("subagent tool cannot be used by subagent to prevent infinite recursion\nAvailable tools: %s", strings.Join(availableTools, ", "))
		}

		if subagentToolFound {
			return errors.Errorf("invalid tools: %s (subagent tool cannot be used by subagent to prevent infinite recursion)\nAvailable tools: %s", strings.Join(invalidTools, ", "), strings.Join(availableTools, ", "))
		}

		if len(invalidTools) == 1 {
			return errors.Errorf("unknown tool: %s\nAvailable tools: %s", invalidTools[0], strings.Join(availableTools, ", "))
		}
		return errors.Errorf("unknown tools: %s\nAvailable tools: %s", strings.Join(invalidTools, ", "), strings.Join(availableTools, ", "))
	}
	return nil
}

func GetToolsFromNames(toolNames []string) []tooltypes.Tool {
	if len(toolNames) == 0 {
		return nil
	}

	toolSet := make(map[string]bool)
	var orderedToolNames []string

	// Always include meta tools first
	for _, metaTool := range metaTools {
		if !toolSet[metaTool] {
			toolSet[metaTool] = true
			orderedToolNames = append(orderedToolNames, metaTool)
		}
	}

	// Add requested tools in the order provided
	for _, toolName := range toolNames {
		if !toolSet[toolName] {
			toolSet[toolName] = true
			orderedToolNames = append(orderedToolNames, toolName)
		}
	}

	// Convert ordered names to tools
	var tools []tooltypes.Tool
	for _, toolName := range orderedToolNames {
		if tool, exists := toolRegistry[toolName]; exists {
			tools = append(tools, tool)
		}
	}

	return tools
}

func GetMainTools(ctx context.Context, allowedTools []string) []tooltypes.Tool {
	if len(allowedTools) == 0 {
		allowedTools = defaultMainTools
	}

	if err := ValidateTools(allowedTools); err != nil {
		logger.G(ctx).WithError(err).Warn("Invalid main agent tool configuration, falling back to defaults")
		allowedTools = defaultMainTools
	}

	return GetToolsFromNames(allowedTools)
}

func GetSubAgentTools(ctx context.Context, allowedTools []string) []tooltypes.Tool {
	if len(allowedTools) == 0 {
		allowedTools = defaultSubAgentTools
	}

	if err := ValidateSubAgentTools(allowedTools); err != nil {
		logger.G(ctx).WithError(err).Warn("Invalid subagent tool configuration, falling back to defaults")
		allowedTools = defaultSubAgentTools
	}

	return GetToolsFromNames(allowedTools)
}

func ToAnthropicTools(tools []tooltypes.Tool) []anthropic.ToolUnionParam {
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

var (
	tracer = telemetry.Tracer("kodelet.tools")
)

func RunTool(ctx context.Context, state tooltypes.State, toolName string, parameters string) tooltypes.ToolResult {
	tool, err := findTool(toolName, state)
	if err != nil {
		return tooltypes.BaseToolResult{
			Error: errors.Wrap(err, "failed to find tool").Error(),
		}
	}

	kvs, err := tool.TracingKVs(parameters)
	if err != nil {
		logger.G(ctx).WithError(err).Error("failed to get tracing kvs")
	}

	ctx, span := tracer.Start(
		ctx,
		fmt.Sprintf("tools.run_tool.%s", toolName),
		trace.WithAttributes(kvs...),
	)
	defer span.End()

	err = tool.ValidateInput(state, parameters)
	if err != nil {
		return tooltypes.BaseToolResult{
			Error: err.Error(),
		}
	}
	result := tool.Execute(ctx, state, parameters)

	if result.IsError() {
		span.SetStatus(codes.Error, result.GetError())
		span.RecordError(errors.New(result.GetError()))
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return result
}
