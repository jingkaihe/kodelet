package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/codemode"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// CodeExecutionContext is host-owned authority for one parent invocation. It is
// never decoded from tool input and cannot be replaced by an input hook.
type CodeExecutionContext struct {
	Definitions []codemode.Definition
	Call        func(context.Context, string, string, string) (CodeToolReply, error)
	// ValidateImage checks current host permissions and model detail support.
	// It must not perform I/O: emissions run on the VM's owning goroutine.
	ValidateImage func(detail string) error
}

type codeExecutionContextKey struct{}

// ContextWithCodeExecution installs the authorized runner-local child dispatcher.
func ContextWithCodeExecution(ctx context.Context, execution CodeExecutionContext) context.Context {
	return context.WithValue(ctx, codeExecutionContextKey{}, execution)
}

// CodeToolReply is the uniform, post-policy value exposed to JavaScript.
type CodeToolReply struct {
	Data        any                        `json:"data"`
	Text        string                     `json:"text"`
	Attachments []tooltypes.ToolAttachment `json:"attachments"`
	Truncated   bool                       `json:"truncated"`
	// Host-only, effective display details; never exposed to JavaScript.
	Input  json.RawMessage                 `json:"-"`
	Result *tooltypes.StructuredToolResult `json:"-"`
}

// CodeToolError preserves known execution outcomes without implying rollback.
type CodeToolError struct {
	Kind    string         `json:"kind"`
	Tool    string         `json:"tool"`
	CallID  string         `json:"callId"`
	Outcome string         `json:"outcome"`
	Message string         `json:"message"`
	Result  *CodeToolReply `json:"result,omitempty"`
}

func (e *CodeToolError) Error() string { return e.Message }

// MarshalJSON exposes serializable error details to the runtime bridge.
func (e *CodeToolError) MarshalJSON() ([]byte, error) {
	type wire CodeToolError
	return json.Marshal((*wire)(e))
}

// CodeExecuteTool runs an async JavaScript function body inside the runner.
type CodeExecuteTool struct{}

type codeExecuteInput struct {
	Code string `json:"code" jsonschema:"description=Body of an async JavaScript function. Use await for host calls and return for selected output."`
}

func (*CodeExecuteTool) Name() string { return "code_execute" }

func (*CodeExecuteTool) Description() string {
	return `Execute the body of an async JavaScript function in a fresh runner-local VM. Kodelet supplies the function wrapper: await and return are valid in the body. No imports, filesystem, network, environment, timers, or persistent globals are available directly.
Discover authorized tools with these asynchronous APIs:
  await catalog.list({group?, limit?, cursor?}) -> {tools: [{name, description, group}], nextCursor?}
  await catalog.search(query, {group?, limit?, cursor?}) -> the same page shape
  await catalog.describe(name) -> documentation, input/output schemas, and declaration
list and search return one-line summaries in pages of 20 (limit up to 100); when nextCursor is present, pass it as cursor with the same query and group to see more. describe returns the full description, input/output schemas, and declaration. Use catalog.describe(name) for a tool's full rules, and catalog.search or catalog.list to find tools you do not already know. Return discovery results to read them. Example: return await catalog.search("open pull requests");
Call tools with await tools[exact_registered_name](input). Names never include their catalog group: use tools.get_weather(...), not tools["mcp/weather/get_weather"](...). Successful calls return {data, text, attachments, truncated}; data is null when unavailable. Failures throw serializable errors with kind, tool, callId, outcome, message, and optional result (the effective ToolReply). To inspect failure output, catch the error and explicitly emit e.result?.text ?? e.message; do not rerun a failed tool just to recover diagnostics.
Prefer batching independent tool calls and catalog queries in a single invocation with Promise.all to reduce round trips; up to 8 tool calls run at once and the rest queue. Use Promise.allSettled when you need every outcome even if some calls fail. Keep dependent calls or operations that could conflict on shared state sequential; batch only work needed for the task.
Only return values, emit(value), and console.log(...) are included as JSON/text. Await values before emitting them. Intermediate tool results stay local. Calls retain existing permissions and hooks. There is no automatic retry or rollback; a caught child error remains in the execution summary. Recursive code_execute is forbidden.
Use emit.image(ref, {detail?: "original"}) to send image pixels to the model, or emit.artifact(ref) to retain an image artifact without sending pixels. A ref is an artifactId string or an attachment descriptor from an effective child reply in this invocation. For an existing artifact or local path, call tools.view_image first. Image emission requires view_image permission; original detail must be supported by the active model. Binary data, paths, and URLs are not accepted. Returning IDs or image-shaped JSON does not select media. Example: const r = await tools.view_image({path: "/tmp/chart.png"}); emit.image(r.attachments[0]);
Each invocation allows 120 seconds, 128 tool calls, 32 KiB of selected output (filter or summarize before returning), and 8 image or artifact emissions. A tool reply over 2 MiB fails after the tool has run; narrow the request instead of retrying.`
}

func (*CodeExecuteTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[codeExecuteInput]()
}

func (*CodeExecuteTool) ValidateInput(_ tooltypes.State, parameters string) error {
	if !json.Valid([]byte(parameters)) {
		return errors.New("code execution input must be a JSON object")
	}
	var input codeExecuteInput
	decoder := json.NewDecoder(strings.NewReader(parameters))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return errors.Wrap(err, "invalid code execution input")
	}
	if strings.TrimSpace(input.Code) == "" {
		return errors.New("code is required")
	}
	if len(input.Code) > codemode.MaxScriptBytes {
		return errors.Errorf("code exceeds the %d KiB input limit", codemode.MaxScriptBytes>>10)
	}
	return nil
}

func (*CodeExecuteTool) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

func (t *CodeExecuteTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	return t.ExecuteStreaming(ctx, state, parameters, nil)
}

func (t *CodeExecuteTool) ExecuteStreaming(ctx context.Context, state tooltypes.State, parameters string, update tooltypes.ToolUpdateCallback) tooltypes.ToolResult {
	start := time.Now()
	meta := tooltypes.CodeExecutionMetadata{
		Status: "running",
		Calls:  []tooltypes.CodeExecutionCall{},
	}
	var attachments []tooltypes.ToolAttachment
	finish := func(err error) tooltypes.ToolResult {
		meta.DurationMs = time.Since(start).Milliseconds()
		meta.Status = "completed"
		message := ""
		if err != nil {
			meta.Status, message = "failed", err.Error()
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				meta.Status = "cancelled"
			}
		}
		return CodeExecuteResult{Metadata: meta, Error: message, Attachments: attachments}
	}
	if err := t.ValidateInput(state, parameters); err != nil {
		return finish(err)
	}
	authority, ok := ctx.Value(codeExecutionContextKey{}).(CodeExecutionContext)
	if !ok || authority.Call == nil {
		return finish(errors.New("code execution requires an authorized runner invocation"))
	}
	var input codeExecuteInput
	_ = json.Unmarshal([]byte(parameters), &input)
	definitions := slices.DeleteFunc(slices.Clone(authority.Definitions), func(definition codemode.Definition) bool {
		return definition.Name == t.Name()
	})
	catalog := codemode.NewCatalog(definitions)
	allowed := make(map[string]bool, len(authority.Definitions))
	for _, definition := range definitions {
		allowed[definition.Name] = true
	}
	var mu sync.Mutex
	var updateMu sync.Mutex
	lastUpdate := time.Time{}
	var updateSequence, publishedSequence uint64
	active := true
	detailBytes := 0
	inventory := make(map[string]tooltypes.ToolAttachment)
	// Prepare under mu, then publish outside it. Slow update hooks must neither
	// prevent VM cancellation nor deliver out-of-order or post-completion updates.
	progress := func() func() {
		if update == nil || !active || time.Since(lastUpdate) < 100*time.Millisecond {
			return func() {}
		}
		lastUpdate = time.Now()
		updateSequence++
		sequence := updateSequence
		snapshot := meta
		snapshot.Calls = slices.Clone(meta.Calls)
		for i := range snapshot.Calls {
			snapshot.Calls[i].Input, snapshot.Calls[i].Result = nil, nil
		}
		snapshot.DurationMs = time.Since(start).Milliseconds()
		return func() {
			updateMu.Lock()
			defer updateMu.Unlock()
			mu.Lock()
			publish := active && ctx.Err() == nil && sequence > publishedSequence
			if publish {
				publishedSequence = sequence
			}
			mu.Unlock()
			if publish {
				update(CodeExecuteResult{Metadata: snapshot})
			}
		}
	}
	result, err := codemode.ExecuteWithOutputValidator(ctx, input.Code, func(callCtx context.Context, request codemode.Request) (any, error) {
		var options codemode.CatalogOptions
		if len(request.Options) > 0 {
			if err := json.Unmarshal(request.Options, &options); err != nil {
				return nil, errors.Wrap(err, "invalid catalog options")
			}
		}
		switch request.Operation {
		case "catalog.list":
			return catalog.List(options)
		case "catalog.search":
			return catalog.Search(request.Query, options)
		case "catalog.describe":
			return catalog.Describe(request.Name)
		case "tool.call":
		default:
			return nil, errors.New("unsupported code execution operation")
		}
		callID := "code_" + uuid.NewString()
		mu.Lock()
		if !active {
			mu.Unlock()
			return nil, errors.New("code execution has ended")
		}
		index := len(meta.Calls)
		meta.Calls = append(meta.Calls, tooltypes.CodeExecutionCall{
			CallID:   callID,
			ToolName: request.Name,
			Status:   "running",
		})
		publish := progress()
		mu.Unlock()
		publish()
		callStart := time.Now()
		var reply CodeToolReply
		var callErr error
		if callCtx.Err() != nil {
			callErr = &CodeToolError{
				Kind:    "cancelled",
				Tool:    request.Name,
				CallID:  callID,
				Outcome: "not_started",
				Message: callCtx.Err().Error(),
			}
		} else if !allowed[request.Name] {
			callErr = &CodeToolError{
				Kind:    "blocked",
				Tool:    request.Name,
				CallID:  callID,
				Outcome: "not_started",
				Message: "tool is not in the authorized catalog",
			}
		} else {
			reply, callErr = authority.Call(callCtx, request.Name, string(request.Input), callID)
		}
		// Validate before recording success. Return the encoded bytes so the
		// bridge cannot serialize a mutable tool value a second time.
		var encoded json.RawMessage
		var encodeErr error
		outcome := "completed"
		if callErr == nil {
			encoded, encodeErr = json.Marshal(reply)
		} else {
			reply = CodeToolReply{}
			outcome = "unknown"
			var toolErr *CodeToolError
			if errors.As(callErr, &toolErr) {
				encoded, encodeErr = json.Marshal(toolErr)
				if toolErr.Outcome != "" {
					outcome = toolErr.Outcome
				}
				if toolErr.Result != nil {
					reply = *toolErr.Result
				}
			}
		}
		references := reply.Attachments
		if encodeErr != nil || len(encoded) > codemode.MaxHostResponseBytes {
			references = nil
			callErr = &CodeToolError{
				Kind:    "invalid_output",
				Tool:    request.Name,
				CallID:  callID,
				Outcome: outcome,
				Message: "child reply is not valid JSON or exceeds the 2 MiB limit; the tool will not be retried",
			}
		}
		mu.Lock()
		publish = func() {}
		if active {
			for _, attachment := range references {
				if attachment.Type == "image" && attachment.ArtifactID != "" &&
					attachment.Error == "" && attachment.Path == "" && attachment.Data == "" {
					inventory[attachment.ArtifactID] = attachment
				}
			}
			entry := &meta.Calls[index]
			entry.DurationMs = time.Since(callStart).Milliseconds()
			entry.Status = "completed"
			if reply.Result != nil {
				// Bound persisted UI details across the whole invocation. Do not
				// duplicate machine data or retain unselected image attachments.
				display := *reply.Result
				display.Data, display.Attachments = nil, nil
				body, err := json.Marshal(display)
				size := len(body) + len(reply.Input)
				if err == nil && detailBytes+size <= 512*1024 {
					entry.Input = slices.Clone(reply.Input)
					entry.Result = &tooltypes.StructuredToolResult{}
					_ = json.Unmarshal(body, entry.Result)
					detailBytes += size
				} else {
					entry.DetailsOmitted = true
				}
			}
			if callErr != nil {
				entry.Status, entry.ErrorKind = "failed", "tool_error"
				var toolErr *CodeToolError
				if errors.As(callErr, &toolErr) {
					entry.ErrorKind = toolErr.Kind
					if toolErr.Kind == "blocked" {
						entry.Status = "blocked"
					}
					if toolErr.Outcome == "unknown" {
						entry.Status = "unknown"
					}
				}
			}
			publish = progress()
		}
		mu.Unlock()
		publish()
		return encoded, callErr
	}, func(item codemode.OutputItem) error {
		if item.Type == "json" {
			return nil
		}
		mu.Lock()
		_, available := inventory[item.ArtifactID]
		mu.Unlock()
		if !available {
			return errors.New("media emission requires an artifact from an effective child reply in this invocation")
		}
		if item.Type == "image" {
			if !allowed["view_image"] || authority.ValidateImage == nil {
				return errors.New("image emission requires view_image permission")
			}
			return authority.ValidateImage(item.Detail)
		}
		return nil
	})
	mu.Lock()
	defer mu.Unlock()
	active = false
	for _, item := range result.Outputs {
		meta.Items = append(meta.Items, tooltypes.CodeExecutionOutput{
			Type:       item.Type,
			Value:      item.Value,
			ArtifactID: item.ArtifactID,
			Detail:     item.Detail,
		})
	}
	for i := range meta.Calls {
		if meta.Calls[i].Status == "running" {
			meta.Calls[i].Status = "unknown"
			meta.Calls[i].ErrorKind = "cancelled"
		}
	}
	for _, item := range meta.Items {
		if attachment, exists := inventory[item.ArtifactID]; exists {
			attachments = append(attachments, attachment)
			delete(inventory, item.ArtifactID)
		}
	}
	return finish(err)
}

// CodeExecuteResult persists selected output, artifact references, and bounded UI child details.
type CodeExecuteResult struct {
	Metadata    tooltypes.CodeExecutionMetadata
	Error       string
	Attachments []tooltypes.ToolAttachment
}

func (r CodeExecuteResult) IsError() bool    { return r.Error != "" }
func (r CodeExecuteResult) GetError() string { return r.Error }
func (r CodeExecuteResult) GetResult() string {
	return renderers.NewRendererRegistry().Render(r.StructuredData())
}

func (r CodeExecuteResult) AssistantFacing() string {
	var output strings.Builder
	fmt.Fprintf(&output, "Code execution %s; %d child calls.\n", r.Metadata.Status, len(r.Metadata.Calls))
	for _, value := range r.Metadata.Outputs {
		output.WriteString(codeOutputText(value))
		output.WriteByte('\n')
	}
	for _, item := range r.Metadata.Items {
		switch item.Type {
		case "json":
			output.WriteString(codeOutputText(item.Value))
			output.WriteByte('\n')
		case "image":
			fmt.Fprintf(&output, "Image: %s\n", item.ArtifactID)
		case "artifact":
			fmt.Fprintf(&output, "Retained artifact (pixels not sent): %s\n", item.ArtifactID)
		}
	}
	failed := 0
	for _, call := range r.Metadata.Calls {
		if call.Status != "completed" && call.Status != "running" {
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintf(&output, "%d child calls did not succeed (including handled failures).\n", failed)
	}
	return tooltypes.StringifyToolResult(output.String(), r.Error)
}

// ContentParts keeps selected text and images in emission order. Only explicit
// image items with retained host-owned descriptors can become image content.
func (r CodeExecuteResult) ContentParts() []tooltypes.ToolResultContentPart {
	attachments := make(map[string]tooltypes.ToolAttachment, len(r.Attachments))
	for _, attachment := range r.Attachments {
		if attachment.Type == "image" && attachment.Error == "" && attachment.ArtifactID != "" {
			attachments[attachment.ArtifactID] = attachment
		}
	}
	summary := r
	summary.Metadata.Items, summary.Metadata.Outputs = nil, nil
	parts := []tooltypes.ToolResultContentPart{{
		Type: tooltypes.ToolResultContentPartTypeText,
		Text: summary.AssistantFacing(),
	}}
	hasImage := false
	for _, item := range r.Metadata.Items {
		switch item.Type {
		case "json":
			parts = append(parts, tooltypes.ToolResultContentPart{
				Type: tooltypes.ToolResultContentPartTypeText,
				Text: codeOutputText(item.Value),
			})
		case "image", "artifact":
			attachment, ok := attachments[item.ArtifactID]
			if !ok {
				continue
			}
			label := "Retained artifact (pixels not sent): "
			if item.Type == "image" {
				label = "Image artifact: "
			}
			parts = append(parts, tooltypes.ToolResultContentPart{
				Type: tooltypes.ToolResultContentPartTypeText,
				Text: label + item.ArtifactID,
			})
			if item.Type == "image" {
				hasImage = true
				parts = append(parts, tooltypes.ToolResultContentPart{
					Type:       tooltypes.ToolResultContentPartTypeImage,
					ArtifactID: item.ArtifactID,
					MimeType:   attachment.MimeType,
					Detail:     item.Detail,
				})
			}
		}
	}
	if !hasImage {
		return nil
	}
	return parts
}

// codeOutputText sends a selected JSON string, including console.log output,
// to the model as plain text rather than quoted JSON with escaped newlines.
// Other values stay compact JSON so their types remain unambiguous.
func codeOutputText(value json.RawMessage) string {
	text := strings.TrimSpace(string(value))
	if strings.HasPrefix(text, `"`) {
		var decoded string
		if json.Unmarshal([]byte(text), &decoded) == nil {
			return decoded
		}
	}
	return string(value)
}

func (r CodeExecuteResult) StructuredData() tooltypes.StructuredToolResult {
	return tooltypes.StructuredToolResult{
		ToolName:    "code_execute",
		Success:     !r.IsError(),
		Error:       r.Error,
		Metadata:    r.Metadata,
		Timestamp:   time.Now(),
		Attachments: slices.Clone(r.Attachments),
	}
}

// PruneCodeExecutionAttachments retains only host-owned references still selected
// after parent result hooks. Hook-provided descriptors cannot create authority.
func PruneCodeExecutionAttachments(result, original tooltypes.StructuredToolResult) tooltypes.StructuredToolResult {
	var metadata, before tooltypes.CodeExecutionMetadata
	if !tooltypes.ExtractMetadata(result.Metadata, &metadata) || !tooltypes.ExtractMetadata(original.Metadata, &before) {
		result.Attachments = nil
		return result
	}
	retained := make(map[string]bool, len(result.Attachments))
	for _, attachment := range result.Attachments {
		if attachment.Error == "" {
			retained[attachment.ArtifactID] = true
		}
	}
	authorized := make(map[string]tooltypes.ToolAttachment, len(original.Attachments))
	for _, attachment := range original.Attachments {
		if retained[attachment.ArtifactID] && attachment.Type == "image" && attachment.ArtifactID != "" && attachment.Error == "" {
			authorized[attachment.ArtifactID] = attachment
		}
	}
	// Count exact selections, not just IDs: hooks cannot upgrade retention to
	// pixels, change image detail, or multiply the original media budget.
	type selection struct{ kind, id, detail string }
	selections := make(map[selection]int)
	for _, item := range before.Items {
		if item.Type == "image" || item.Type == "artifact" {
			selections[selection{item.Type, item.ArtifactID, item.Detail}]++
		}
	}
	var items []tooltypes.CodeExecutionOutput
	result.Attachments = nil
	attached := make(map[string]bool)
	for _, item := range metadata.Items {
		if item.Type == "json" {
			items = append(items, tooltypes.CodeExecutionOutput{Type: "json", Value: item.Value})
			continue
		}
		key := selection{item.Type, item.ArtifactID, item.Detail}
		attachment, ok := authorized[item.ArtifactID]
		if !ok || selections[key] == 0 {
			continue
		}
		selections[key]--
		items = append(items, tooltypes.CodeExecutionOutput{
			Type:       item.Type,
			ArtifactID: item.ArtifactID,
			Detail:     item.Detail,
		})
		if !attached[item.ArtifactID] {
			result.Attachments = append(result.Attachments, attachment)
			attached[item.ArtifactID] = true
		}
	}
	metadata.Items = items
	result.Metadata = metadata
	return result
}
