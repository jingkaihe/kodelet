package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// ReadConversationTool reads a saved conversation and extracts the parts relevant to a goal.
type ReadConversationTool struct{}

// ReadConversationInput reuses the shared read_conversation input schema while preserving pkg/tools schema IDs.
type ReadConversationInput tooltypes.ReadConversationInput

// ReadConversationToolResult represents the extracted conversation content.
type ReadConversationToolResult struct {
	conversationID string
	goal           string
	content        string
	err            string
}

// NewReadConversationTool creates a read_conversation tool backed by the central model helper.
func NewReadConversationTool() *ReadConversationTool {
	return &ReadConversationTool{}
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
- Reads the saved conversation directly from central storage and renders it as markdown
- Runs a goal-based extraction pass using the daemon's weak model, without creating another saved conversation
- Returns only the relevant content

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
func (t *ReadConversationTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
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

	content, err := tooltypes.RunModelHelper(ctx, tooltypes.ModelHelperRequest{
		Operation:      tooltypes.ModelHelperReadConversationExtract,
		ConversationID: input.ConversationID,
		Prompt:         input.Goal,
	})
	content = strings.TrimSpace(content)
	if err == nil && content == "" {
		err = errors.New("empty extraction response")
	}
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
		content:        content,
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
