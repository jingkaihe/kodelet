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
	"github.com/jingkaihe/kodelet/pkg/tools/browser"
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
	"bash":                        &BashTool{},
	"file_read":                   &FileReadTool{},
	"file_write":                  &FileWriteTool{},
	"file_edit":                   &FileEditTool{},
	"thinking":                    &ThinkingTool{},
	"subagent":                    &SubAgentTool{},
	"grep_tool":                   &GrepTool{},
	"glob_tool":                   &GlobTool{},
	"todo_read":                   &TodoReadTool{},
	"todo_write":                  &TodoWriteTool{},
	"web_fetch":                   &WebFetchTool{},
	"image_recognition":           &ImageRecognitionTool{},
	"view_background_processes":   &ViewBackgroundProcessesTool{},
	"browser_navigate":            &browser.NavigateTool{},
	"browser_get_page":            &browser.GetPageTool{},
	"browser_click":               &browser.ClickTool{},
	"browser_type":                &browser.TypeTool{},
	"browser_wait_for":            &browser.WaitForTool{},
	"browser_screenshot":          &browser.ScreenshotTool{},
}

// metaTools are always enabled regardless of configuration
var metaTools = []string{
	"file_read",
	"grep_tool", 
	"glob_tool",
	"thinking",
}

// browserToolNames are the available browser tools
var browserToolNames = []string{
	"browser_navigate",
	"browser_get_page", 
	"browser_click",
	"browser_type",
	"browser_wait_for",
	"browser_screenshot",
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

func ValidateTools(toolNames []string) error {
	for _, toolName := range toolNames {
		if _, exists := toolRegistry[toolName]; !exists {
			return errors.Errorf("unknown tool: %s", toolName)
		}
	}
	return nil
}

func ValidateSubAgentTools(toolNames []string) error {
	for _, toolName := range toolNames {
		if toolName == "subagent" {
			return errors.New("subagent tool cannot be used by subagent to prevent infinite recursion")
		}
		if _, exists := toolRegistry[toolName]; !exists {
			return errors.Errorf("unknown tool: %s", toolName)
		}
	}
	return nil
}

func GetToolsFromNames(toolNames []string, enableBrowserTools bool) []tooltypes.Tool {
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

	// Add browser tools if enabled
	if enableBrowserTools {
		for _, browserTool := range browserToolNames {
			if !toolSet[browserTool] {
				toolSet[browserTool] = true
				orderedToolNames = append(orderedToolNames, browserTool)
			}
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

func ParseAllowedToolsString(allowedToolsStr string) []string {
	if allowedToolsStr == "" {
		return []string{}
	}
	
	var tools []string
	for _, tool := range strings.Split(allowedToolsStr, ",") {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			tools = append(tools, tool)
		}
	}
	return tools
}

func GetMainTools(allowedTools []string, enableBrowserTools bool) []tooltypes.Tool {
	if len(allowedTools) == 0 {
		allowedTools = defaultMainTools
	}

	if err := ValidateTools(allowedTools); err != nil {
		allowedTools = defaultMainTools
	}

	return GetToolsFromNames(allowedTools, enableBrowserTools)
}

func GetSubAgentTools(allowedTools []string, enableBrowserTools bool) []tooltypes.Tool {
	if len(allowedTools) == 0 {
		allowedTools = defaultSubAgentTools
	}

	if err := ValidateSubAgentTools(allowedTools); err != nil {
		allowedTools = defaultSubAgentTools
	}

	return GetToolsFromNames(allowedTools, enableBrowserTools)
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
