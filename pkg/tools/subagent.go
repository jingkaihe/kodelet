package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type SubAgentToolResult struct {
	result   string
	err      string
	question string
}

func (r *SubAgentToolResult) GetResult() string {
	return r.result
}

func (r *SubAgentToolResult) GetError() string {
	return r.err
}

func (r *SubAgentToolResult) IsError() bool {
	return r.err != ""
}

func (r *SubAgentToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.GetError())
}

type SubAgentTool struct{}

type SubAgentInput struct {
	Question string `json:"question" jsonschema:"description=The question to ask"`
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
		return &SubAgentToolResult{
			err: err.Error(),
		}
	}

	// get type.Thread from context
	subAgentConfig, ok := ctx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
	if !ok {
		return &SubAgentToolResult{
			err:      "sub-agent config not found in context",
			question: input.Question,
		}
	}

	handler := subAgentConfig.MessageHandler
	if handler == nil {
		logger.G(ctx).Warn("no message handler found in context, using console handler")
		handler = &llmtypes.ConsoleMessageHandler{}
	}

	text, err := subAgentConfig.Thread.SendMessage(ctx, input.Question, handler, llmtypes.MessageOpt{
		PromptCache:        true,
		UseWeakModel:       false, // explicitly set to false for clarity
		NoSaveConversation: true,
		CompactRatio:       subAgentConfig.CompactRatio,
		DisableAutoCompact: subAgentConfig.DisableAutoCompact,
	})
	if err != nil {
		return &SubAgentToolResult{
			err:      err.Error(),
			question: input.Question,
		}
	}

	return &SubAgentToolResult{
		result:   text,
		question: input.Question,
	}
}

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
