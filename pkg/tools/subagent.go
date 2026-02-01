package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SubAgentToolResult represents the result of a sub-agent tool execution
type SubAgentToolResult struct {
	result   string
	err      string
	question string
}

// GetResult returns the sub-agent output
func (r *SubAgentToolResult) GetResult() string {
	return r.result
}

// GetError returns the error message
func (r *SubAgentToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *SubAgentToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *SubAgentToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.GetError())
}

// SubAgentTool provides functionality to spawn sub-agents for complex tasks
type SubAgentTool struct{}

// SubAgentInput defines the input parameters for the sub-agent tool
type SubAgentInput struct {
	Question string `json:"question" jsonschema:"description=The question to ask"`
}

// Name returns the name of the tool
func (t *SubAgentTool) Name() string {
	return "subagent"
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *SubAgentTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[SubAgentInput]()
}

// Description returns the description of the tool
func (t *SubAgentTool) Description() string {
	return `Use this tool to delegate tasks to a sub-agent.
This tool is ideal for tasks that involves code searching, architecture analysis, codebase understanding and troubleshooting.

## Input
- question: A description of the question to ask the subagent.

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

// ValidateInput validates the input parameters for the tool
func (t *SubAgentTool) ValidateInput(_ tooltypes.State, parameters string) error {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.Question == "" {
		return errors.New("question is required")
	}

	return nil
}

// TracingKVs returns tracing key-value pairs for observability
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

// Execute runs the sub-agent via shell-out and returns the result
func (t *SubAgentTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &SubAgentToolResult{
			err: err.Error(),
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return &SubAgentToolResult{
			err:      errors.Wrap(err, "failed to get executable path").Error(),
			question: input.Question,
		}
	}

	// Build command with subagent_args from config
	args := []string{"run", "--result-only", "--as-subagent"}

	// Append user-configured subagent args (e.g., "--profile openai --use-weak-model")
	if llmConfig, ok := state.GetLLMConfig().(llmtypes.Config); ok && llmConfig.SubagentArgs != "" {
		parsedArgs := strings.Fields(llmConfig.SubagentArgs)
		args = append(args, parsedArgs...)
	}

	args = append(args, input.Question)

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = os.Environ()

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &SubAgentToolResult{
				err:      string(exitErr.Stderr),
				question: input.Question,
			}
		}
		return &SubAgentToolResult{
			err:      err.Error(),
			question: input.Question,
		}
	}

	return &SubAgentToolResult{
		result:   strings.TrimSpace(string(output)),
		question: input.Question,
	}
}

// StructuredData returns structured metadata about the sub-agent execution
func (r *SubAgentToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "subagent",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.SubAgentMetadata{
		Question: r.question,
		Response: r.result,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}
