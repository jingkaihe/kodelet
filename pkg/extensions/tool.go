package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/tools"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

var _ tooltypes.StreamingTool = &Tool{}

// Tool is a tool registered by an extension.
type Tool struct {
	extensionID string
	process     *Process
	name        string
	description string
	schema      *jsonschema.Schema
	rawSchema   map[string]any
	timeout     time.Duration
	maxOutput   int
}

func newTool(extensionID string, process *Process, registration ToolRegistration, timeout time.Duration, maxOutput int) (*Tool, error) {
	schemaBytes, err := json.Marshal(registration.InputSchema)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal extension tool schema")
	}
	var rawSchema map[string]any
	if len(schemaBytes) == 0 || string(schemaBytes) == "null" {
		schemaBytes = []byte(`{"type":"object"}`)
	}
	if err := json.Unmarshal(schemaBytes, &rawSchema); err != nil {
		return nil, errors.Wrap(err, "failed to parse raw extension tool schema")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		schema.Type = "object"
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
		rawSchema:   rawSchema,
		timeout:     timeout,
		maxOutput:   maxOutput,
	}, nil
}

// Name returns the extension tool name.
func (t *Tool) Name() string { return t.name }

// Description returns the extension tool description.
func (t *Tool) Description() string { return t.description }

// GenerateSchema returns a typed compatibility view of the extension schema.
func (t *Tool) GenerateSchema() *jsonschema.Schema { return t.schema }

// RawInputSchema returns the extension-provided schema without narrowing it.
func (t *Tool) RawInputSchema() map[string]any { return t.rawSchema }

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
	return t.execute(ctx, parameters, nil)
}

// ExecuteStreaming invokes the extension tool and forwards accumulated snapshots.
func (t *Tool) ExecuteStreaming(ctx context.Context, _ tooltypes.State, parameters string, onUpdate tooltypes.ToolUpdateCallback) tooltypes.ToolResult {
	return t.execute(ctx, parameters, onUpdate)
}

func (t *Tool) execute(ctx context.Context, parameters string, onUpdate tooltypes.ToolUpdateCallback) tooltypes.ToolResult {
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
	result, err := t.process.ExecuteToolStreaming(execCtx, t.name, json.RawMessage(parameters), callCtx, func(update ToolExecutionResult) {
		if onUpdate != nil {
			onUpdate(t.resultFromExecution(update, time.Since(start)))
		}
	})
	executionTime := time.Since(start)
	if err != nil {
		return &ToolResult{toolName: t.name, extensionID: t.extensionID, executionTime: executionTime, err: err.Error()}
	}
	return t.resultFromExecution(*result, executionTime)
}

func (t *Tool) resultFromExecution(result ToolExecutionResult, executionTime time.Duration) *ToolResult {
	content := result.Content
	if t.maxOutput > 0 && len(content) > t.maxOutput {
		content = content[:t.maxOutput] + "\n\n[TRUNCATED - Output exceeded extension max output limit]"
	}
	return &ToolResult{
		toolName:      t.name,
		extensionID:   t.extensionID,
		executionTime: executionTime,
		result:        content,
		err:           result.Error,
		data:          normalizeExtensionResultData(result.Data, t.maxOutput),
	}
}

func normalizeExtensionResultData(data map[string]any, maxOutput int) map[string]any {
	raw, exists := data["presentation"]
	if !exists {
		return data
	}

	normalized := make(map[string]any, len(data))
	for key, value := range data {
		normalized[key] = value
	}

	payload, err := json.Marshal(raw)
	if err != nil {
		delete(normalized, "presentation")
		return normalized
	}
	var presentationData map[string]any
	if err := json.Unmarshal(payload, &presentationData); err != nil || presentationData == nil {
		delete(normalized, "presentation")
		return normalized
	}
	if rawBody, ok := presentationData["body"]; ok {
		body, ok := rawBody.(string)
		if !ok {
			delete(normalized, "presentation")
			return normalized
		}
		presentationData["body"] = truncateExtensionPresentationBody(body, maxOutput)
	}

	presentation, ok := tooltypes.ParseExtensionToolPresentation(presentationData)
	if !ok {
		delete(normalized, "presentation")
		return normalized
	}
	presentationData["summary"] = presentation.Summary
	if _, exists := presentationData["format"]; exists {
		presentationData["format"] = presentation.Format
	}
	if presentation.HasBody() {
		presentationData["body"] = presentation.Body
	}
	normalized["presentation"] = presentationData
	return normalized
}

func normalizeStructuredExtensionPresentation(result tooltypes.StructuredToolResult, maxOutput int) tooltypes.StructuredToolResult {
	var metadata tooltypes.ExtensionToolMetadata
	if !tooltypes.ExtractMetadata(result.Metadata, &metadata) {
		return result
	}
	metadata.Data = normalizeExtensionResultData(metadata.Data, maxOutput)
	if _, ok := result.Metadata.(*tooltypes.ExtensionToolMetadata); ok {
		result.Metadata = &metadata
	} else {
		result.Metadata = metadata
	}
	return result
}

func truncateExtensionPresentationBody(body string, maxOutput int) string {
	limit := tooltypes.MaxExtensionToolPresentationBodyBytes
	if maxOutput > 0 && maxOutput < limit {
		limit = maxOutput
	}
	if len(body) <= limit {
		return body
	}

	const notice = "\n\n[TRUNCATED - Presentation exceeded extension max output limit]"
	if limit <= len(notice) {
		return validUTF8Prefix(body, limit)
	}
	return validUTF8Prefix(body, limit-len(notice)) + notice
}

func validUTF8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
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
		Metadata: &tooltypes.ExtensionToolMetadata{
			ExtensionID:   r.extensionID,
			ToolName:      r.toolName,
			Output:        r.result,
			Data:          r.data,
			ExecutionTime: r.executionTime,
		},
	}
	if r.IsError() {
		result.Error = r.err
	}
	return result
}

func (r *ToolResult) String() string {
	if r.IsError() {
		return fmt.Sprintf("extension tool %s failed: %s", r.toolName, r.err)
	}
	return r.result
}
