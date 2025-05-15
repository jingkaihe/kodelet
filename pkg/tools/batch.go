package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

type BatchTool struct{}

type BatchToolInput struct {
	Description string       `json:"description" jsonschema:"description=The description of the batch operation in less than 10 words"`
	Invocations []Invocation `json:"invocations" jsonschema:"description=The list of invocations to be run"`
}

type Invocation struct {
	ToolName   string `json:"tool_name" jsonschema:"description=The name of the tool to invoke"`
	Parameters any    `json:"parameters" jsonschema:"description=The parameters to pass to the tool"`
}

func (t *BatchTool) Name() string {
	return "batch"
}

func (t *BatchTool) Description() string {
	return `Use Batch tool to batch up multiple independent invocations of tools.
This is useful to reduce the amount of back-and-forth between the LLM and the end user, thus reduce the latency and token usage.

## Input
* Description: The description of the batch operation in less than 10 words.
* Invocations: The list of invocations to be run. Each invocation has a tool name which prepresent the tool to invoke and the parameters to pass to the tool. The spec of the parameter MUST be compliant with the tool's jsonschema.

## Output
* It returns the results of all the invocation in the same order as the invocations.

## Common Use Cases
* When you have a list of INDEPENDENT tool calls to make to compelete a task, and these INDEPENDENT tool calls can be run in parallel.
* When you need to reduce the latency and token usage by reducing the number of back-and-forth between the LLM and the end user.

## When NOT to use this tool
* When the tool calls are NOT independent, i.e., one tool call depends on the output of another tool call in the same batch.

## Examples

<example>
{
	"description": "Get the git status and the git diff",
	"invocations": [
		{
			"tool_name": "bash",
			"parameters": {
				"description": "Get the git status",
				"command": "git status"
			}
		},
		{
			"tool_name": "bash",
			"parameters": {
				"description": "Get the git diff",
				"command": "git diff --cached"
			}
		}
	]
}
<reasoning>
The git status and the git diff are independent, so we can run them in parallel.
</reasoning>
</example>

<example>
{
	"description": "Find app.py and print the content of the file",
	"invocations": [
		{
			"tool_name": "bash",
			"parameters": {
				"description": "Find app.py",
				"command": "find ./ -name app.py"
			}
		},
		{
			"tool_name": "file_read",
			"parameters": {
				"description": "Print the content of the file",
				"file_path": "PATH_TO_APP_PY"
			}
		}
	}
</example>
<reasoning>
The find and the file_read are NOT independent, so we can NOT run them in parallel.
</reasoning>
</example>
`
}

func (t *BatchTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[BatchToolInput]()
}

func (t *BatchTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input BatchToolInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to unmarshal input: %w", err)
	}

	return nil
}

func (t *BatchTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input BatchToolInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return tooltypes.ToolResult{Error: fmt.Sprintf("failed to unmarshal input: %v", err)}
	}

	return tooltypes.ToolResult{}
}

func (t *BatchTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	kvs := []attribute.KeyValue{}

	var input BatchToolInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w", err)
	}

	kvs = append(kvs, attribute.String("description", input.Description))
	for idx, invocation := range input.Invocations {
		kvs = append(kvs, attribute.String(fmt.Sprintf("invocation.%d.tool_name", idx), invocation.ToolName))
		kvs = append(kvs, attribute.String(fmt.Sprintf("invocation.%d.parameters", idx), fmt.Sprintf("%v", invocation.Parameters)))
	}

	return kvs, nil
}
