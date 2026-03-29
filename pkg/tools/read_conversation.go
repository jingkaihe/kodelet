package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/shlex"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

const readConversationPromptTemplate = `Here is the mentioned conversation content:

<mentionedConversation>
%s
</mentionedConversation>

You are helping me extract relevant information from the mentioned conversation based on a goal.

## Task

I am talking to another user. They mentioned a conversation in their last message. I turned the conversation into Markdown and provided it to you, along with a goal of what I want you to extract.

Your job is to:
1. Analyze the mentioned conversation's content
2. Identify information that is relevant to the goal
3. Extract and preserve those relevant parts with full fidelity
4. Omit clearly irrelevant content to keep the context concise

## Guidelines

**Preserve Fidelity**: When content IS relevant, include it completely with all important details, code snippets, explanations, and context.
**Be Selective**: When content is clearly NOT relevant to the goal, omit it entirely.
**Maintain Structure**: Keep the extracted content well-organized and coherent. If multiple parts are relevant, preserve their logical flow.
**Technical Precision**: Preserve exact technical details like file paths, function names, error messages, and code snippets that are relevant.

## Examples

### Example 1: Extract implementation details

**Goal**: "Extract the implementation details of the authentication mechanism in the mentioned conversation"
**Good Extraction**:
- Includes: Authentication logic, security considerations, code examples, relevant files
- Omits: Unrelated features, general discussion, tangential topics

### Example 2: Referencing a bug fix

**Goal**: "Extract how the bug was fixed in the mentioned conversation"
**Good Extraction**:
- Includes: The bug description, root cause, the fix or solution, relevant code changes
- Omits: Initial troubleshooting steps, unrelated changes, meeting notes

### Example 3: Learning from past work

**Goal**: "Describe what pattern was used to implement the widget Foo in the mentioned conversation"
**Good Extraction**:
- Includes: The design pattern, implementation approach, example code, key decisions
- Omits: Project-specific details that don't apply, alternative approaches that were rejected

## Goal

%s

## Your Response

Return only the extracted relevant content as markdown.
`

type (
	conversationMarkdownRenderer func(ctx context.Context, conversationID string) (string, error)
	conversationExtractor        func(ctx context.Context, state tooltypes.State, markdown string, goal string) (string, error)
)

// ReadConversationTool reads a saved conversation and extracts the parts relevant to a goal.
type ReadConversationTool struct {
	renderConversation conversationMarkdownRenderer
	extractContent     conversationExtractor
}

// ReadConversationInput defines the input parameters for the read_conversation tool.
type ReadConversationInput struct {
	ConversationID string `json:"conversation_id" jsonschema:"description=The ID of the saved conversation to read"`
	Goal           string `json:"goal" jsonschema:"description=What information to extract from the conversation"`
}

// ReadConversationToolResult represents the extracted conversation content.
type ReadConversationToolResult struct {
	conversationID string
	goal           string
	content        string
	err            string
}

// NewReadConversationTool creates a read_conversation tool with production dependencies.
func NewReadConversationTool() *ReadConversationTool {
	return &ReadConversationTool{
		renderConversation: defaultConversationMarkdownRenderer,
		extractContent:     defaultConversationExtractor,
	}
}

// Name returns the tool name.
func (t *ReadConversationTool) Name() string {
	return "read_conversation"
}

// Description returns the tool description.
func (t *ReadConversationTool) Description() string {
	return `Read a saved conversation by ID and extract only the information relevant to a goal.

Use this when:
- The user references a previous kodelet conversation by ID
- You need details from prior work without loading the full conversation into context
- You want implementation details, bug fixes, decisions, or code snippets from earlier work

Input:
- conversation_id: required saved conversation ID
- goal: required description of what to extract

Behavior:
- Renders the saved conversation to markdown using the built-in CLI view
- Runs a goal-based extraction pass and returns only the relevant content

The result preserves exact technical details when they matter and omits clearly irrelevant parts.`
}

// GenerateSchema generates the JSON schema for the tool input.
func (t *ReadConversationTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[ReadConversationInput]()
}

// ValidateInput validates the tool input.
func (t *ReadConversationTool) ValidateInput(_ tooltypes.State, parameters string) error {
	input := &ReadConversationInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return err
	}

	if strings.TrimSpace(input.ConversationID) == "" {
		return errors.New("conversation_id is required")
	}
	if strings.TrimSpace(input.Goal) == "" {
		return errors.New("goal is required")
	}

	return nil
}

// TracingKVs returns tracing attributes for observability.
func (t *ReadConversationTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &ReadConversationInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("conversation_id", strings.TrimSpace(input.ConversationID)),
		attribute.String("goal", strings.TrimSpace(input.Goal)),
	}, nil
}

// Execute executes the read_conversation tool.
func (t *ReadConversationTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &ReadConversationInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return &ReadConversationToolResult{
			conversationID: input.ConversationID,
			goal:           input.Goal,
			err:            err.Error(),
		}
	}

	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.Goal = strings.TrimSpace(input.Goal)

	markdown, err := t.renderConversation(ctx, input.ConversationID)
	if err != nil {
		return &ReadConversationToolResult{
			conversationID: input.ConversationID,
			goal:           input.Goal,
			err:            fmt.Sprintf("Failed to render conversation: %s", err),
		}
	}

	content, err := t.extractContent(ctx, state, markdown, input.Goal)
	if err != nil {
		return &ReadConversationToolResult{
			conversationID: input.ConversationID,
			goal:           input.Goal,
			err:            fmt.Sprintf("Failed to extract relevant content: %s", err),
		}
	}

	return &ReadConversationToolResult{
		conversationID: input.ConversationID,
		goal:           input.Goal,
		content:        strings.TrimSpace(content),
	}
}

// AssistantFacing returns the assistant-visible tool output.
func (r *ReadConversationToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.content, r.err)
}

// GetResult returns the extracted content.
func (r *ReadConversationToolResult) GetResult() string {
	return r.content
}

// GetError returns the tool error.
func (r *ReadConversationToolResult) GetError() string {
	return r.err
}

// IsError returns whether the result is an error.
func (r *ReadConversationToolResult) IsError() bool {
	return r.err != ""
}

// StructuredData returns structured metadata about the read_conversation result.
func (r *ReadConversationToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "read_conversation",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
		Metadata: &tooltypes.ReadConversationMetadata{
			ConversationID: r.conversationID,
			Goal:           r.goal,
			Content:        r.content,
		},
	}

	if r.IsError() {
		result.Error = r.err
	}

	return result
}

func defaultConversationMarkdownRenderer(ctx context.Context, conversationID string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", errors.Wrap(err, "failed to get executable path")
	}

	cmd := exec.CommandContext(ctx, exe, "conversation", "show", conversationID, "--format", "markdown", "--truncate-tool-results")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", errors.Errorf("conversation show failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func defaultConversationExtractor(ctx context.Context, state tooltypes.State, markdown string, goal string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", errors.Wrap(err, "failed to get executable path")
	}

	args := []string{"run", "--result-only", "--as-subagent", "--use-weak-model", "--no-tools"}
	if llmConfig, ok := state.GetLLMConfig().(llmtypes.Config); ok && llmConfig.SubagentArgs != "" {
		parsedArgs, err := shlex.Split(llmConfig.SubagentArgs)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("failed to parse subagent_args, ignoring")
		} else {
			args = append(args, parsedArgs...)
		}
	}

	prompt := buildReadConversationPrompt(markdown, goal)
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = strings.NewReader(prompt)

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", errors.Errorf("content extraction failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}

	content := strings.TrimSpace(string(output))
	if content == "" {
		return "", errors.New("empty extraction response")
	}

	return content, nil
}

func buildReadConversationPrompt(markdown string, goal string) string {
	return fmt.Sprintf(
		readConversationPromptTemplate,
		strings.TrimSpace(markdown),
		strings.TrimSpace(goal),
	)
}
