package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/tools"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

var _ tooltypes.Tool = &Tool{}

// Tool is a tool registered by an extension.
type Tool struct {
	extensionID string
	process     *Process
	name        string
	description string
	schema      *jsonschema.Schema
	timeout     time.Duration
	maxOutput   int
}

func newTool(extensionID string, process *Process, registration ToolRegistration, timeout time.Duration, maxOutput int) (*Tool, error) {
	schemaBytes, err := json.Marshal(registration.InputSchema)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal extension tool schema")
	}
	var schema jsonschema.Schema
	if len(schemaBytes) == 0 || string(schemaBytes) == "null" {
		schema.Type = "object"
	} else if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil, errors.Wrap(err, "failed to parse extension tool schema")
	}
	if registration.Name == "" {
		return nil, errors.New("extension tool name is required")
	}
	if registration.Description == "" {
		return nil, errors.New("extension tool description is required")
	}
	return &Tool{
		extensionID: extensionID,
		process:     process,
		name:        registration.Name,
		description: registration.Description,
		schema:      &schema,
		timeout:     timeout,
		maxOutput:   maxOutput,
	}, nil
}

// Name returns the extension tool name.
func (t *Tool) Name() string { return t.name }

// Description returns the extension tool description.
func (t *Tool) Description() string { return t.description }

// GenerateSchema returns the extension-provided schema.
func (t *Tool) GenerateSchema() *jsonschema.Schema { return t.schema }

// ValidateInput validates JSON syntax. Schema validation is delegated to the extension SDK/runtime.
func (t *Tool) ValidateInput(_ tooltypes.State, parameters string) error {
	var input map[string]any
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid JSON input")
	}
	return nil
}

// Execute invokes the extension tool over JSON-RPC.
func (t *Tool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	start := time.Now()
	toolCtx := tools.ToolContextFromContext(ctx)
	callCtx := ExtensionCallContext{
		ConversationID: toolCtx.ConversationID,
		CWD:            toolCtx.WorkingDir,
		Provider:       toolCtx.Provider,
		Model:          toolCtx.Model,
		Profile:        toolCtx.Profile,
		RecipeName:     toolCtx.RecipeName,
		InvokedBy:      "main",
	}

	execCtx, cancel := contextWithOptionalDuration(ctx, t.timeout)
	defer cancel()
	result, err := t.process.ExecuteTool(execCtx, t.name, json.RawMessage(parameters), callCtx)
	executionTime := time.Since(start)
	if err != nil {
		return &ToolResult{toolName: t.name, extensionID: t.extensionID, executionTime: executionTime, err: err.Error()}
	}
	if result.Error != "" {
		return &ToolResult{toolName: t.name, extensionID: t.extensionID, executionTime: executionTime, err: result.Error, data: result.Data}
	}
	content := result.Content
	if t.maxOutput > 0 && len(content) > t.maxOutput {
		content = content[:t.maxOutput] + "\n\n[TRUNCATED - Output exceeded extension max output limit]"
	}
	return &ToolResult{toolName: t.name, extensionID: t.extensionID, executionTime: executionTime, result: content, data: result.Data}
}

// TracingKVs returns tracing attributes for the tool.
func (t *Tool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
	return []attribute.KeyValue{
		attribute.String("tool.type", "extension"),
		attribute.String("tool.name", t.name),
		attribute.String("extension.id", t.extensionID),
	}, nil
}

// ToolResult is the result of an extension tool execution.
type ToolResult struct {
	toolName      string
	extensionID   string
	executionTime time.Duration
	result        string
	err           string
	data          map[string]any
}

// AssistantFacing returns the result for the assistant.
func (r *ToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
}

// IsError returns true when execution failed.
func (r *ToolResult) IsError() bool { return r.err != "" }

// GetError returns the error string.
func (r *ToolResult) GetError() string { return r.err }

// GetResult returns the result string.
func (r *ToolResult) GetResult() string { return r.result }

// StructuredData returns structured metadata.
func (r *ToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  r.toolName,
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}
	if r.IsError() {
		result.Error = r.err
	} else {
		result.Metadata = &tooltypes.ExtensionToolMetadata{
			ExtensionID:   r.extensionID,
			ToolName:      r.toolName,
			Output:        r.result,
			Data:          r.data,
			ExecutionTime: r.executionTime,
		}
	}
	return result
}

func (r *ToolResult) String() string {
	if r.IsError() {
		return fmt.Sprintf("extension tool %s failed: %s", r.toolName, r.err)
	}
	return r.result
}
