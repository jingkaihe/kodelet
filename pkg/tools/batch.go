package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

type BatchToolResult struct {
	description string
	toolResults []tooltypes.ToolResult
}

func (r *BatchToolResult) GetResult() string {
	var results []string
	for idx, toolResult := range r.toolResults {
		if !toolResult.IsError() {
			results = append(results, fmt.Sprintf(`<invocation.%d.result>
%s
</invocation.%d.result>
`, idx, toolResult.GetResult(), idx))
		}
	}
	return strings.Join(results, "\n")
}

func (r *BatchToolResult) GetError() string {
	var errors []string
	for idx, toolResult := range r.toolResults {
		if toolResult.IsError() {
			errors = append(errors, fmt.Sprintf(`<invocation.%d.error>
%s
</invocation.%d.error>
`, idx, toolResult.GetError(), idx))
		}
	}
	return strings.Join(errors, "\n")
}

func (r *BatchToolResult) IsError() bool {
	for _, toolResult := range r.toolResults {
		if toolResult.IsError() {
			return true
		}
	}
	return false
}

func (r *BatchToolResult) AssistantFacing() string {
	// return tooltypes.StringifyToolResult(r.GetResult(), r.GetError())
	results := bytes.NewBufferString("")
	for idx, toolResult := range r.toolResults {
		if toolResult.GetResult() != "" {
			fmt.Fprintf(results, `<invocation.%d.result>
%s
</invocation.%d.result>
`, idx, toolResult.GetResult(), idx)
		}

		if toolResult.IsError() {
			fmt.Fprintf(results, `<invocation.%d.error>
%s
</invocation.%d.error>
`, idx, toolResult.GetError(), idx)
		}
	}

	return results.String()
}

type BatchTool struct{}

type BatchToolInput struct {
	Description string       `json:"description" jsonschema:"description=The description of the batch operation in less than 10 words"`
	Invocations []Invocation `json:"invocations" jsonschema:"description=The list of invocations to be run"`
}

type Invocation struct {
	ToolName   string `json:"tool_name" jsonschema:"description=The name of the tool to invoke"`
	Parameters any    `json:"parameters" jsonschema:"description=The parameters to pass to the tool"`
}

func (inv *Invocation) invoke(ctx context.Context, state tooltypes.State) tooltypes.ToolResult {
	_, err := findTool(inv.ToolName, state)
	if err != nil {
		return tooltypes.BaseToolResult{Error: errors.Wrap(err, "failed to find tool").Error()}
	}

	p, err := json.Marshal(inv.Parameters)
	if err != nil {
		return tooltypes.BaseToolResult{Error: errors.Wrap(err, "failed to encode parameters").Error()}
	}

	return RunTool(ctx, state, inv.ToolName, string(p))
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

var (
	ErrNestedBatch       = errors.New("nested batch is not allowed")
	ErrToolNotFound      = errors.New("tool not found")
	ErrInvalidParameters = errors.New("invalid parameters")
)

func (t *BatchTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input BatchToolInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to unmarshal input: %w", err)
	}

	if err := noNestedBatch(input); err != nil {
		return err
	}
	for _, invocation := range input.Invocations {
		tool, err := findTool(invocation.ToolName, state)
		if err != nil {
			return err
		}
		p, err := json.Marshal(invocation.Parameters)
		if err != nil {
			return errors.Wrapf(err, "failed to encode parameters")
		}
		if err := tool.ValidateInput(state, string(p)); err != nil {
			return errors.Wrapf(err, "failed to validate parameters")
		}
	}

	return nil
}

func (t *BatchTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input BatchToolInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &BatchToolResult{
			description: input.Description,
			toolResults: []tooltypes.ToolResult{
				tooltypes.BaseToolResult{Error: errors.Wrap(err, "failed to unmarshal input").Error()},
			},
		}
	}

	toolResults := make([]tooltypes.ToolResult, len(input.Invocations))
	wg := sync.WaitGroup{}
	wg.Add(len(input.Invocations))

	for i, invocation := range input.Invocations {
		go func(inv Invocation, i int) {
			defer wg.Done()
			toolResults[i] = inv.invoke(ctx, state)
		}(invocation, i)
	}

	wg.Wait()

	return &BatchToolResult{
		description: input.Description,
		toolResults: toolResults,
	}
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

func (r *BatchToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "batch",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Convert sub-results to structured format
	subResults := make([]tooltypes.StructuredToolResult, 0, len(r.toolResults))
	successCount := 0
	failureCount := 0

	for _, toolResult := range r.toolResults {
		subResult := toolResult.StructuredData()
		subResults = append(subResults, subResult)
		if subResult.Success {
			successCount++
		} else {
			failureCount++
		}
	}

	result.Metadata = &tooltypes.BatchMetadata{
		Description:   r.description,
		SubResults:    subResults,
		ExecutionTime: time.Duration(0), // Batch tool doesn't track execution time currently
		SuccessCount:  successCount,
		FailureCount:  failureCount,
	}

	// If any sub-tool failed, include aggregated errors
	if failureCount > 0 {
		result.Error = r.GetError()
	}

	return result
}
