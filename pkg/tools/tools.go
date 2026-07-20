// Package tools provides the core tool execution framework for Kodelet.
// It defines the available tools, manages tool registration, and handles
// tool execution with proper validation, tracing, and error handling.
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// GenerateSchema generates a JSON schema for the given type
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
	"bash":              &BashTool{},
	"apply_patch":       &ApplyPatchTool{},
	"file_read":         &FileReadTool{},
	"file_write":        &FileWriteTool{},
	"file_edit":         &FileEditTool{},
	"read_conversation": NewReadConversationTool(),
	"grep_tool":         &GrepTool{},
	"glob_tool":         &GlobTool{},
	"web_fetch":         &WebFetchTool{},
	"get_goal":          NewGetGoalTool(),
	"update_goal":       NewUpdateGoalTool(),
	"view_image":        NewViewImageTool("", ""),
	"skill":             NewSkillTool(nil, false, false),
}

var virtualToolNames = []string{
	"openai_web_search",
}

// VirtualToolNames returns tool names that are exposed directly by providers
// rather than through the executable tool registry.
func VirtualToolNames() []string {
	return append([]string(nil), virtualToolNames...)
}

// NoToolsMarker is a special value indicating no tools should be enabled
const NoToolsMarker = "none"

// metaTools are enabled by default for basic navigation unless feature toggles disable them.
var metaTools = []string{
	"grep_tool",
	"glob_tool",
}

// mainAgentMetaTools are enabled for the main agent even when allowed_tools is restrictive.
var mainAgentMetaTools = []string{
	"get_goal",
	"update_goal",
}

// defaultMainTools are the default tools for main agent
var defaultMainTools = []string{
	"bash",
	"file_write",
	"file_edit",
	"read_conversation",
	"grep_tool",
	"glob_tool",
	"web_fetch",
	"get_goal",
	"update_goal",
	"view_image",
	"skill",
}

// getAvailableToolNames returns a list of all available tool names
func getAvailableToolNames() []string {
	var tools []string
	for toolName := range toolRegistry {
		tools = append(tools, toolName)
	}
	tools = append(tools, virtualToolNames...)
	return tools
}

// ValidateTools validates that all tool names are available
func ValidateTools(toolNames []string) error {
	var unknownTools []string
	for _, toolName := range toolNames {
		if _, exists := toolRegistry[toolName]; exists {
			continue
		}
		if isVirtualToolName(toolName) {
			continue
		}
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

func isVirtualToolName(toolName string) bool {
	for _, virtualToolName := range virtualToolNames {
		if toolName == virtualToolName {
			return true
		}
	}
	return false
}

func metaToolsWithOptions(enableFSSearchTools bool) []string {
	if enableFSSearchTools {
		return metaTools
	}

	return filterOutFSSearchTools(metaTools)
}

func mainAgentMetaToolsWithOptions(enableFSSearchTools bool) []string {
	tools := append([]string{}, metaToolsWithOptions(enableFSSearchTools)...)
	tools = append(tools, mainAgentMetaTools...)
	return tools
}

// GetToolsFromNames returns a list of tools from the given tool names
func GetToolsFromNames(toolNames []string) []tooltypes.Tool {
	return getToolsFromNamesWithOptions(toolNames, false)
}

func getToolsFromNamesWithOptions(toolNames []string, enableFSSearchTools bool) []tooltypes.Tool {
	return getToolsFromNamesWithMetaTools(toolNames, metaToolsWithOptions(enableFSSearchTools))
}

func getMainToolsFromNamesWithOptions(toolNames []string, enableFSSearchTools bool) []tooltypes.Tool {
	return getToolsFromNamesWithMetaTools(toolNames, mainAgentMetaToolsWithOptions(enableFSSearchTools))
}

func getToolsFromNamesWithMetaTools(toolNames []string, metaTools []string) []tooltypes.Tool {
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

func filterOutFSSearchTools(toolNames []string) []string {
	filtered := make([]string, 0, len(toolNames))
	for _, toolName := range toolNames {
		if toolName != "grep_tool" && toolName != "glob_tool" {
			filtered = append(filtered, toolName)
		}
	}
	return filtered
}

// GetMainTools returns the main tools available for the agent
func GetMainTools(ctx context.Context, allowedTools []string) []tooltypes.Tool {
	return GetMainToolsWithOptions(ctx, allowedTools, false)
}

// GetMainToolsWithOptions returns the main tools available for the agent with feature toggles applied.
func GetMainToolsWithOptions(ctx context.Context, allowedTools []string, enableFSSearchTools bool) []tooltypes.Tool {
	explicitAllowlist := len(allowedTools) > 0
	if !enableFSSearchTools {
		allowedTools = filterOutFSSearchTools(allowedTools)
	}
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return nil
	}

	if len(allowedTools) == 0 {
		if !explicitAllowlist {
			allowedTools = append([]string{}, defaultMainTools...)
		}
	}
	if !enableFSSearchTools {
		allowedTools = filterOutFSSearchTools(allowedTools)
	}

	if err := ValidateTools(allowedTools); err != nil {
		logger.G(ctx).WithError(err).Warn("Invalid main agent tool configuration, falling back to defaults")
		allowedTools = append([]string{}, defaultMainTools...)
		if !enableFSSearchTools {
			allowedTools = filterOutFSSearchTools(allowedTools)
		}
	}

	return getMainToolsFromNamesWithOptions(allowedTools, enableFSSearchTools)
}

var tracer = telemetry.Tracer("kodelet.tools")

// RunTool executes a tool by name with the given parameters
func RunTool(ctx context.Context, state tooltypes.State, toolName string, parameters string) tooltypes.ToolResult {
	return RunToolWithUpdates(ctx, state, toolName, parameters, nil)
}

// RunToolWithUpdates executes a tool and forwards transient result snapshots
// when the selected tool implements tooltypes.StreamingTool.
func RunToolWithUpdates(
	ctx context.Context,
	state tooltypes.State,
	toolName string,
	parameters string,
	onUpdate tooltypes.ToolUpdateCallback,
) tooltypes.ToolResult {
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

	var result tooltypes.ToolResult
	if streamingTool, ok := tool.(tooltypes.StreamingTool); ok && onUpdate != nil {
		result = streamingTool.ExecuteStreaming(ctx, state, parameters, onUpdate)
	} else {
		result = tool.Execute(ctx, state, parameters)
	}

	if result.IsError() {
		span.SetStatus(codes.Error, result.GetError())
		span.RecordError(errors.New(result.GetError()))
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return result
}
