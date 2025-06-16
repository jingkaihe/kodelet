// Package tools provides the core tool execution framework for Kodelet.
// It defines the available tools, manages tool registration, and handles
// tool execution with proper validation, tracing, and error handling.
package tools

import (
	"context"
	"fmt"

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

// GetMainTools returns the main tools, optionally including browser tools
func GetMainTools(enableBrowserTools bool) []tooltypes.Tool {
	tools := make([]tooltypes.Tool, len(baseMainTools))
	copy(tools, baseMainTools)

	if enableBrowserTools {
		tools = append(tools, browserTools...)
	}

	return tools
}

// GetSubAgentTools returns the sub-agent tools, optionally including browser tools
func GetSubAgentTools(enableBrowserTools bool) []tooltypes.Tool {
	tools := make([]tooltypes.Tool, len(baseSubAgentTools))
	copy(tools, baseSubAgentTools)

	if enableBrowserTools {
		tools = append(tools, browserTools...)
	}

	return tools
}

var baseMainTools = []tooltypes.Tool{
	&BashTool{},
	&FileReadTool{},
	&FileWriteTool{},
	&FileEditTool{},
	&FileMultiEditTool{},
	&ThinkingTool{},
	&SubAgentTool{},
	&GrepTool{},
	&GlobTool{},
	&TodoReadTool{},
	&TodoWriteTool{},
	&BatchTool{},
	&WebFetchTool{},
	&ImageRecognitionTool{},
	&ViewBackgroundProcessesTool{},
}

var browserTools = []tooltypes.Tool{
	&browser.NavigateTool{},
	&browser.GetPageTool{},
	&browser.ClickTool{},
	&browser.TypeTool{},
	&browser.WaitForTool{},
	&browser.ExtractTextTool{},
	&browser.ScreenshotTool{},
	&browser.GoBackTool{},
}

var baseSubAgentTools = []tooltypes.Tool{
	&BashTool{},
	&FileReadTool{},
	&FileWriteTool{},
	&FileEditTool{},
	&FileMultiEditTool{},
	&GrepTool{},
	&GlobTool{},
	&ThinkingTool{},
	&TodoReadTool{},
	&TodoWriteTool{},
	&BatchTool{},
	&WebFetchTool{},
	// &ImageRecognitionTool{}, // subagent will directly recognise the image using api
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
		span.RecordError(fmt.Errorf("%s", result.GetError()))
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return result
}
