package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// ThinkingToolResult represents the result of a thinking tool execution
type ThinkingToolResult struct {
	thought string
	err     string
}

// GetResult returns an empty string as thinking is for the model's internal use
func (r *ThinkingToolResult) GetResult() string {
	return "Your thought have been recorded."
}

// GetError returns the error message
func (r *ThinkingToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *ThinkingToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *ThinkingToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult("Your thought have been recorded.", r.err)
}

// ThinkingTool provides functionality for the model to organize its thoughts
type ThinkingTool struct{}

// ThinkingInput defines the input parameters for the thinking tool
type ThinkingInput struct {
	Thought string `json:"thought" jsonschema:"description=A thought to think about"`
}

// Name returns the name of the tool
func (t *ThinkingTool) Name() string {
	return "thinking"
}

// TracingKVs returns tracing key-value pairs for observability
func (t *ThinkingTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &ThinkingInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("thought", input.Thought),
	}, nil
}

// ValidateInput validates the input parameters for the tool
func (t *ThinkingTool) ValidateInput(_ tooltypes.State, parameters string) error {
	var input ThinkingInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid input")
	}

	if input.Thought == "" {
		return errors.New("thought is required")
	}

	return nil
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *ThinkingTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[ThinkingInput]()
}

// Description returns the description of the thinking tool.
// Thinking tool is inspired by https://www.anthropic.com/engineering/claude-think-tool
func (t *ThinkingTool) Description() string {
	return `Use the tool to think about something.

It will not obtain new information or change the database, but just append the thought to the log. Use it when complex reasoning or some cache memory is needed.

# Common Use Cases
- When troubleshooting a complex issue, use this tool to organise your thoughts and hypothesis.
- When designing a new feature, use this tool to think about architecture choices, pros and cons, and implementation details.
- When you need to perform a complex task, use this tool to break it down into smaller steps.
- When you need to make a decision, use this tool to think about the options and their consequences.
`
}

// Execute processes the thinking and returns an empty result
func (t *ThinkingTool) Execute(_ context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &ThinkingInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &ThinkingToolResult{
			err: err.Error(),
		}
	}

	return &ThinkingToolResult{
		thought: input.Thought,
	}
}

// StructuredData returns structured metadata about the thinking operation
func (r *ThinkingToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "thinking",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	if r.IsError() {
		result.Error = r.GetError()
		return result
	}

	result.Metadata = &tooltypes.ThinkingMetadata{
		Thought: r.thought,
	}

	return result
}
