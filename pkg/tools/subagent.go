package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type SubAgentTool struct{}

type SubAgentInput struct {
	TaskDescription string `json:"task_description" jsonschema:"description=A description of the task to complete"`
}

func (t *SubAgentTool) Name() string {
	return "subagent"
}

func (t *SubAgentTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[SubAgentInput]()
}

func (t *SubAgentTool) Description() string {
	return `Use this tool to delegate tasks to a sub-agent.
This tool is ideal for semantic search, where you are not sure about the exact keyword to use.

## Common Use Cases
* If you want to search for a concept, but are not sure about the exact keyword.

## DO NOT use this tool when:
* You know exactly the keywords to use. e.g. "[Ll]og" - Use ${grep_tool} instead.
* You just want to find where certain files or directories are located - Use find command via ${bash} tool instead.
* You just want to look for the content of a file - Use ${file_read} tool instead.

## Important Notes
1. The subagent does not have any memory of previous invocations, and you cannot talk to it back and forth. As a result, your task description must be:
   - highly detailed and context-rich.
   - state what information you expect to get back.
2. The agent returns a text response back to you, and you have no access to the subagent's internal messages.
`
}

func (t *SubAgentTool) ValidateInput(state tooltypes.State, parameters string) error {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.TaskDescription == "" {
		return errors.New("task_description is required")
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
		attribute.String("task_description", input.TaskDescription),
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

	// handler := subAgentConfig.MessageHandler
	// if handler == nil {
	// 	logrus.Warn("no message handler found in context, using console handler")
	// 	handler = &llmtypes.ConsoleMessageHandler{}
	// }
	handler := &llmtypes.ConsoleMessageHandler{
		Silent: false,
	}
	text, err := subAgentConfig.Thread.SendMessage(ctx, input.TaskDescription, handler, llmtypes.MessageOpt{
		PromptCache:  true,
		UseWeakModel: false,
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
