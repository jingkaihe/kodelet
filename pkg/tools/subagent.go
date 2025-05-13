package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
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
* You know exactly the keywords to use. e.g. "[Ll]og" - Use ${code_search} instead.
* You just want to find where certain files or directories are located - Use find command via ${bash} tool instead.

## Important Notes
1. The subagent does not have any memory of previous invocations, and you cannot talk to it back and forth. As a result, your task description must be:
   - highly detailed and context-rich.
   - state what information you expect to get back.
2. The agent returns a text response back to you, and you have no access to the subagent's internal messages.
`
}

func (t *SubAgentTool) ValidateInput(state state.State, parameters string) error {
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

func (t *SubAgentTool) Execute(ctx context.Context, state state.State, parameters string) ToolResult {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return ToolResult{
			Error: err.Error(),
		}
	}

	// get type.Thread from context
	thread, ok := ctx.Value(types.ThreadKey{}).(types.Thread)
	if !ok {
		return ToolResult{
			Error: "thread not found in context",
		}
	}

	text, err := thread.SendMessage(ctx, input.TaskDescription, &types.ConsoleMessageHandler{}, types.MessageOpt{})
	if err != nil {
		return ToolResult{
			Error: err.Error(),
		}
	}

	return ToolResult{
		Result: text,
	}
}
