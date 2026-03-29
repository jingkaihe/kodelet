package tools

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/google/shlex"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed prompts/read_conversation.txt
var readConversationPromptTemplate string

type (
	conversationMarkdownRenderer func(ctx context.Context, conversationID string) (string, error)
	conversationExtractor        func(ctx context.Context, state tooltypes.State, markdown string, goal string) (string, error)
)

// ReadConversationTool reads a saved conversation and extracts the parts relevant to a goal.
type ReadConversationTool struct {
	renderConversation conversationMarkdownRenderer
	extractContent     conversationExtractor
}

// ReadConversationInput reuses the shared read_conversation input schema while preserving pkg/tools schema IDs.
type ReadConversationInput tooltypes.ReadConversationInput

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

	prompt, err := buildReadConversationPrompt(markdown, goal)
	if err != nil {
		return "", errors.Wrap(err, "failed to build read conversation prompt")
	}
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

func buildReadConversationPrompt(markdown string, goal string) (string, error) {
	data := struct {
		Conversation string
		Goal         string
	}{
		Conversation: strings.TrimSpace(markdown),
		Goal:         strings.TrimSpace(goal),
	}

	tmpl, err := template.New("read_conversation_prompt").Parse(readConversationPromptTemplate)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse read_conversation prompt template")
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return "", errors.Wrap(err, "failed to execute read_conversation prompt template")
	}

	return rendered.String(), nil
}
