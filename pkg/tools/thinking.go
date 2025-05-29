package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

type ThinkingTool struct{}

type ThinkingInput struct {
	Thought string `json:"thought" jsonschema:"description=A thought to think about"`
}

func (t *ThinkingTool) Name() string {
	return "thinking"
}

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

func (t *ThinkingTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input ThinkingInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}

	if input.Thought == "" {
		return fmt.Errorf("thought is required")
	}

	return nil
}

func (t *ThinkingTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[ThinkingInput]()
}

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

func (t *ThinkingTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResultInterface {
	return tooltypes.ToolResult{
		Result: "Your thought have been recorded.",
	}
}
