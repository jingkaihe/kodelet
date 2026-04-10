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
	"subagent":          NewSubAgentTool(nil, false, false),
	"grep_tool":         &GrepTool{},
	"glob_tool":         &GlobTool{},
	"todo_read":         &TodoReadTool{},
	"todo_write":        &TodoWriteTool{},
	"web_fetch":         &WebFetchTool{},
	"view_image":        NewViewImageTool("", ""),
	"skill":             NewSkillTool(nil, false, false),
}

var virtualToolNames = []string{
	"openai_web_search",
}

// NoToolsMarker is a special value indicating no tools should be enabled
const NoToolsMarker = "none"

// metaTools are enabled by default for basic navigation unless feature toggles disable them.
var metaTools = []string{
	"file_read",
	"grep_tool",
	"glob_tool",
}

// defaultMainTools are the default tools for main agent
var defaultMainTools = []string{
	"bash",
	"file_read",
	"file_write",
	"file_edit",
	"read_conversation",
	"subagent",
	"grep_tool",
	"glob_tool",
	"web_fetch",
	"view_image",
	"skill",
}

var todoTools = []string{
	"todo_read",
	"todo_write",
}

// defaultSubAgentTools are the default tools for subagent
var defaultSubAgentTools = []string{
	"bash",
	"file_read",
	"file_write",
	"file_edit",
	"read_conversation",
	"grep_tool",
	"glob_tool",
	"web_fetch",
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

// getAvailableSubAgentToolNames returns a list of available tool names for subagents (excluding subagent tool)
func getAvailableSubAgentToolNames() []string {
	var tools []string
	for toolName := range toolRegistry {
		if toolName != "subagent" {
			tools = append(tools, toolName)
		}
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

// ValidateSubAgentTools validates that all sub-agent tool names are available
func ValidateSubAgentTools(toolNames []string) error {
	var invalidTools []string
	var subagentToolFound bool

	for _, toolName := range toolNames {
		if toolName == "subagent" {
			subagentToolFound = true
			invalidTools = append(invalidTools, toolName)
		} else if isVirtualToolName(toolName) {
			continue
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

func isVirtualToolName(toolName string) bool {
	for _, virtualToolName := range virtualToolNames {
		if toolName == virtualToolName {
			return true
		}
	}
	return false
}

func metaToolsWithOptions(disableFSSearchTools bool) []string {
	if !disableFSSearchTools {
		return metaTools
	}

	return filterOutFSSearchTools(metaTools)
}

// GetToolsFromNames returns a list of tools from the given tool names
func GetToolsFromNames(toolNames []string) []tooltypes.Tool {
	return getToolsFromNamesWithOptions(toolNames, false)
}

func getToolsFromNamesWithOptions(toolNames []string, disableFSSearchTools bool) []tooltypes.Tool {
	if len(toolNames) == 0 {
		return nil
	}

	toolSet := make(map[string]bool)
	var orderedToolNames []string

	// Always include meta tools first
	for _, metaTool := range metaToolsWithOptions(disableFSSearchTools) {
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
func GetMainTools(ctx context.Context, allowedTools []string, enableTodos bool) []tooltypes.Tool {
	// Special case: "none" means no tools
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return nil
	}

	if len(allowedTools) == 0 {
		allowedTools = append([]string{}, defaultMainTools...)
		if enableTodos {
			allowedTools = append(allowedTools, todoTools...)
		}
	} else if !enableTodos {
		filteredTools := filterOutTodoTools(allowedTools)
		if len(filteredTools) == 0 {
			allowedTools = append([]string{}, metaTools...)
		} else {
			allowedTools = filteredTools
		}
	}

	if err := ValidateTools(allowedTools); err != nil {
		logger.G(ctx).WithError(err).Warn("Invalid main agent tool configuration, falling back to defaults")
		allowedTools = append([]string{}, defaultMainTools...)
		if enableTodos {
			allowedTools = append(allowedTools, todoTools...)
		}
	}

	return getToolsFromNamesWithOptions(allowedTools, false)
}

// GetMainToolsWithOptions returns the main tools available for the agent with feature toggles applied.
func GetMainToolsWithOptions(ctx context.Context, allowedTools []string, enableTodos bool, disableFSSearchTools bool) []tooltypes.Tool {
	explicitAllowlist := len(allowedTools) > 0
	if disableFSSearchTools {
		allowedTools = filterOutFSSearchTools(allowedTools)
	}
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return nil
	}

	if len(allowedTools) == 0 {
		if !explicitAllowlist {
			allowedTools = append([]string{}, defaultMainTools...)
			if enableTodos {
				allowedTools = append(allowedTools, todoTools...)
			}
		}
	} else if !enableTodos {
		filteredTools := filterOutTodoTools(allowedTools)
		if len(filteredTools) == 0 {
			allowedTools = append([]string{}, metaToolsWithOptions(disableFSSearchTools)...)
		} else {
			allowedTools = filteredTools
		}
	}

	if disableFSSearchTools {
		allowedTools = filterOutFSSearchTools(allowedTools)
	}

	if err := ValidateTools(allowedTools); err != nil {
		logger.G(ctx).WithError(err).Warn("Invalid main agent tool configuration, falling back to defaults")
		allowedTools = append([]string{}, defaultMainTools...)
		if enableTodos {
			allowedTools = append(allowedTools, todoTools...)
		}
		if disableFSSearchTools {
			allowedTools = filterOutFSSearchTools(allowedTools)
		}
	}

	return getToolsFromNamesWithOptions(allowedTools, disableFSSearchTools)
}

// GetSubAgentTools returns the tools available for sub-agents
func GetSubAgentTools(ctx context.Context, allowedTools []string) []tooltypes.Tool {
	// Special case: "none" means no tools
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return nil
	}

	if len(allowedTools) == 0 {
		allowedTools = defaultSubAgentTools
	}

	if err := ValidateSubAgentTools(allowedTools); err != nil {
		logger.G(ctx).WithError(err).Warn("Invalid subagent tool configuration, falling back to defaults")
		allowedTools = defaultSubAgentTools
	}

	return getToolsFromNamesWithOptions(allowedTools, false)
}

// GetSubAgentToolsWithOptions returns the sub-agent tools with feature toggles applied.
func GetSubAgentToolsWithOptions(ctx context.Context, allowedTools []string, disableFSSearchTools bool) []tooltypes.Tool {
	explicitAllowlist := len(allowedTools) > 0
	if disableFSSearchTools {
		allowedTools = filterOutFSSearchTools(allowedTools)
	}
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return nil
	}

	if len(allowedTools) == 0 {
		if !explicitAllowlist {
			allowedTools = append([]string{}, defaultSubAgentTools...)
		}
	}

	if disableFSSearchTools {
		allowedTools = filterOutFSSearchTools(allowedTools)
	}

	if err := ValidateSubAgentTools(allowedTools); err != nil {
		logger.G(ctx).WithError(err).Warn("Invalid subagent tool configuration, falling back to defaults")
		allowedTools = append([]string{}, defaultSubAgentTools...)
		if disableFSSearchTools {
			allowedTools = filterOutFSSearchTools(allowedTools)
		}
	}

	return getToolsFromNamesWithOptions(allowedTools, disableFSSearchTools)
}

// filterOutSubagent removes the subagent tool from a tool list
func filterOutSubagent(tools []tooltypes.Tool) []tooltypes.Tool {
	filtered := make([]tooltypes.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Name() != "subagent" {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func filterOutTodoTools(toolNames []string) []string {
	filtered := make([]string, 0, len(toolNames))
	for _, toolName := range toolNames {
		if toolName != "todo_read" && toolName != "todo_write" {
			filtered = append(filtered, toolName)
		}
	}
	return filtered
}

var tracer = telemetry.Tracer("kodelet.tools")

// RunTool executes a tool by name with the given parameters
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
