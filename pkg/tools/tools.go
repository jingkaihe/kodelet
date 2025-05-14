package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Tool interface {
	GenerateSchema() *jsonschema.Schema
	Name() string
	Description() string
	ValidateInput(state state.State, parameters string) error
	Execute(ctx context.Context, state state.State, parameters string) ToolResult
	TracingKVs(parameters string) ([]attribute.KeyValue, error)
}

type ToolResult struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

func (t *ToolResult) String() string {
	out := ""
	if t.Error != "" {
		out = fmt.Sprintf(`<error>
%s
</error>
`, t.Error)
	}
	if t.Result != "" {
		out += fmt.Sprintf(`<result>
%s
</result>
`, t.Result)
	}
	return out
}

func GenerateSchema[T any]() *jsonschema.Schema {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T

	return reflector.Reflect(v)
}

var MainTools = []Tool{
	&BashTool{},
	&FileReadTool{},
	&FileWriteTool{},
	&FileEditTool{},
	&ThinkingTool{},
	&SubAgentTool{},
	&CodeSearchTool{},
	&TodoReadTool{},
	&TodoWriteTool{},
}

var SubAgentTools = []Tool{
	&BashTool{},
	&FileReadTool{},
	&FileWriteTool{},
	&FileEditTool{},
	&CodeSearchTool{},
	&ThinkingTool{},
	&TodoReadTool{},
	&TodoWriteTool{},
}

func ToAnthropicTools(tools []Tool) []anthropic.ToolUnionParam {
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

func RunTool(ctx context.Context, state state.State, toolName string, parameters string, tools []Tool) ToolResult {
	tool := findTool(tools, toolName)
	if tool == nil {
		return ToolResult{
			Error: fmt.Sprintf("tool not found: %s", toolName),
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
		return ToolResult{
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

func findTool(tools []Tool, toolName string) Tool {
	for _, tool := range tools {
		if tool.Name() == toolName {
			return tool
		}
	}
	return nil
}
