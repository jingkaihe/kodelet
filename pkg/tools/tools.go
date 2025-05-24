package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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

var MainTools = []tooltypes.Tool{
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
}

var SubAgentTools = []tooltypes.Tool{
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
		return tooltypes.ToolResult{
			Error: errors.Wrap(err, "failed to find tool").Error(),
		}
	}

	kvs, err := tool.TracingKVs(parameters)
	if err != nil {
		logrus.WithError(err).Error("failed to get tracing kvs")
	}

	ctx, span := tracer.Start(
		ctx,
		fmt.Sprintf("tools.run_tool.%s", toolName),
		trace.WithAttributes(kvs...),
	)
	defer span.End()

	err = tool.ValidateInput(state, parameters)
	if err != nil {
		return tooltypes.ToolResult{
			Error: err.Error(),
		}
	}
	result := tool.Execute(ctx, state, parameters)

	if result.Error != "" {
		span.SetStatus(codes.Error, result.Error)
		span.RecordError(fmt.Errorf("%s", result.Error))
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return result
}
