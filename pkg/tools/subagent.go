package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/invopop/jsonschema"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type SubAgentTool struct{}

type ModelStrength string

const (
	ModelStrengthWeak   ModelStrength = "weak"
	ModelStrengthStrong ModelStrength = "strong"
)

type SubAgentInput struct {
	Question      string        `json:"question" jsonschema:"description=The question to ask in 15 words or less"`
	ModelStrength ModelStrength `json:"model_strength" jsonschema:"description=The strength of the model to use, it can be 'weak' or 'strong'"`
}

func (t *SubAgentTool) Name() string {
	return "subagent"
}

func (t *SubAgentTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[SubAgentInput]()
}

func (t *SubAgentTool) Description() string {
	return `Use this tool to delegate tasks to a sub-agent.
This tool is ideal for tasks that involves code searching, architecture analysis, codebase understanding and troubleshooting.

## Input
- question: A description of the question to ask the subagent.
- model_strength: The strength of the model to use, it can be "weak" or "strong".

Use "weak" model when you want it to perform simple multi-turn search and information summary.
Use "strong" model when you want it to perform strong architecture thinking and reasoning.

## Common Use Cases
* If you want to do multi-turn search using grep_tool and file_read, and you don't know exactly what keywords to use. You should use this subagent tool.

## DO NOT use this tool when:
* You are 100% sure about the keywords to use. e.g. "[Ll]og" - Use ${grep_tool} instead.
* You just want to find where certain files or directories are located - Use find command via ${bash} tool instead.
* You just want to look for the content of a file - Use ${file_read} tool instead.

## Important Notes
1. The subagent does not have any memory of previous invocations, and you cannot talk to it back and forth. As a result, your question must be concise and to the point.
   - contain a short and concise problem statement.
   - state what information you expect to get back.
   - state the format of the output in detail.
2. The agent returns a text response back to you, and you have no access to the subagent's internal messages.
3. The agent's response is not visible to the user. To show user the result you must send the result from the subagent back to the user.
`
}

func (t *SubAgentTool) ValidateInput(state tooltypes.State, parameters string) error {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.Question == "" {
		return errors.New("question is required")
	}

	if input.ModelStrength != ModelStrengthWeak && input.ModelStrength != ModelStrengthStrong {
		return errors.New("model_strength must be either 'weak' or 'strong'")
	}

	return nil
}

func (t *SubAgentTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("question", input.Question),
	}, nil
}

func (t *SubAgentTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return tooltypes.ToolResult{
			Error: err.Error(),
		}
	}

	// get type.Thread from context
	subAgentConfig, ok := ctx.Value(llmtypes.SubAgentConfig{}).(llmtypes.SubAgentConfig)
	if !ok {
		return tooltypes.ToolResult{
			Error: "sub-agent config not found in context",
		}
	}

	handler := subAgentConfig.MessageHandler
	if handler == nil {
		logrus.Warn("no message handler found in context, using console handler")
		handler = &llmtypes.ConsoleMessageHandler{}
	}

	text, err := subAgentConfig.Thread.SendMessage(ctx, input.Question, handler, llmtypes.MessageOpt{
		PromptCache:  input.ModelStrength == ModelStrengthStrong,
		UseWeakModel: input.ModelStrength == ModelStrengthWeak,
	})
	if err != nil {
		return tooltypes.ToolResult{
			Error: err.Error(),
		}
	}

	return tooltypes.ToolResult{
		Result: text,
	}
}
