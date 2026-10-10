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
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// CodeExecutionContext is host-owned authority for one parent invocation. It is
// never decoded from tool input and cannot be replaced by an input hook.
type CodeExecutionContext struct {
	Definitions []codemode.Definition
	// Call's optional callback carries effective UI-only snapshots, never values
	// exposed to JavaScript. A nil callback disables transient child updates.
	Call func(context.Context, string, string, string, func(CodeToolReply)) (CodeToolReply, error)
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

// encodedCodeToolError gives the bridge the same error reply that was validated,
// without serializing a possibly mutable partial result a second time.
type encodedCodeToolError struct {
	message string
	encoded json.RawMessage
}

func (e *encodedCodeToolError) Error() string { return e.message }

func (e *encodedCodeToolError) MarshalJSON() ([]byte, error) { return e.encoded, nil }

// CodeExecuteTool runs an async JavaScript function body inside the runner.
type CodeExecuteTool struct{}

type codeExecuteInput struct {
	Code string `json:"code" jsonschema:"description=Body of an async JavaScript function. Use await for host calls and return for selected output."`
}

func (*CodeExecuteTool) Name() string { return "code_execute" }

func (*CodeExecuteTool) Description() string {
	return `Run the body of an async JavaScript function in a sandboxed VM; top-level await and return work. No imports, filesystem, network, environment, timers, or state between invocations: use tools instead.

` + codemode.RuntimeDeclaration + `
How to work:
- Find unfamiliar tools with catalog.search or catalog.list (page with nextCursor, keeping the same query and group), and catalog.describe them before relying on their fields. Return discovery results to read them; never guess field names. Example: return await catalog.search("open pull requests");
- Call tools by exact registered name, without the group: tools.get_weather(...), not tools["mcp/weather/get_weather"](...).
- Do as much as you can in one code_execute call: if you already know the next steps, write them into the same script instead of making another call. Skip calls the task does not need.
- Inside a script, run independent calls in parallel with Promise.all, including catalog lookups; up to 8 tool calls run at once and extra calls wait. Use Promise.allSettled when some calls may fail and you still want the others. Run calls in order when one needs another's result or both modify the same thing.
- Only returned values, emit(value), and console.log(...) reach you, as JSON/text. Return or emit only the fields you need, after awaiting them.
- On failure, catch the ToolError and return or emit e.result?.text || e.message instead of rerunning the tool. Nothing is retried or rolled back. A successful reply whose data violates its outputSchema throws invalid_output; the tool already ran.
- Show an image file or artifact with: const r = await tools.view_image({path: "/tmp/chart.png"}); emit.image(r.attachments[0]); Refs are artifactIds or attachments from this invocation, never paths, URLs, or base64. emit.image needs view_image permission; "original" detail needs model support.

Limits: 15 minutes, 128 tool calls, 8 media emissions. Total selected output above ~40 KB is truncated; filter or summarize before returning. Tool replies and individual return/emit values are limited to 2 MiB. Output-limit failures do not roll back completed tool calls; check for side effects before retrying.`
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
	outputSchemas := make(map[string]map[string]any, len(definitions))
	for _, definition := range definitions {
		if allowed[definition.Name] {
			continue // Match the catalog's first-definition-wins rule.
		}
		allowed[definition.Name] = true
		// Use the catalog's immutable snapshot, not the caller's mutable maps.
		outputSchemas[definition.Name] = catalog.OutputSchema(definition.Name)
	}
	var mu sync.Mutex
	lastUpdate := time.Time{}
	active := true
	detailBytes := 0
	var detailSizes []int
	// Each replacement owns a fresh immutable copy and releases the previous
	// snapshot's budget. Redactions and oversized updates must not retain stale data.
	storeDetails := func(index int, reply CodeToolReply) {
		entry := &meta.Calls[index]
		detailBytes -= detailSizes[index]
		detailSizes[index] = 0
		entry.Input, entry.Result, entry.DetailsOmitted = nil, nil, false
		if reply.Result == nil {
			return
		}
		display := *reply.Result
		display.Data, display.Attachments = nil, nil
		body, err := json.Marshal(display)
		size := len(body) + len(reply.Input)
		if err != nil || detailBytes+size > 512*1024 {
			entry.DetailsOmitted = true
			return
		}
		entry.Input = slices.Clone(reply.Input)
		entry.Result = &tooltypes.StructuredToolResult{}
		_ = json.Unmarshal(body, entry.Result)
		detailSizes[index] = size
		detailBytes += size
	}
	inventory := make(map[string]tooltypes.ToolAttachment)
	// One publisher owns both the pending timer and any in-flight hook. Coalesce
	// to the latest snapshot without accumulating goroutines behind slow hooks.
	// Never hold mu while publishing: cancellation must not wait for hooks.
	var pendingUpdate *time.Timer
	publishing, dirty := false, false
	var publishProgress func()
	publishProgress = func() {
		mu.Lock()
		if !active || ctx.Err() != nil {
			pendingUpdate, publishing = nil, false
			mu.Unlock()
			return
		}
		dirty = false
		lastUpdate = time.Now()
		snapshot := meta
		snapshot.Calls = slices.Clone(meta.Calls)
		snapshot.DurationMs = time.Since(start).Milliseconds()
		mu.Unlock()
		update(CodeExecuteResult{Metadata: snapshot})
		mu.Lock()
		pendingUpdate, publishing = nil, false
		if dirty && active && ctx.Err() == nil {
			publishing = true
			pendingUpdate = time.AfterFunc(max(0, 100*time.Millisecond-time.Since(lastUpdate)), publishProgress)
		}
		mu.Unlock()
	}
	progress := func() func() {
		if update == nil || !active || ctx.Err() != nil {
			return func() {}
		}
		dirty = true
		if publishing {
			return func() {}
		}
		publishing = true
		if delay := 100*time.Millisecond - time.Since(lastUpdate); delay > 0 {
			// Flush even without another update, including a silent bash's initial
			// description or a fast child finishing while other children run.
			pendingUpdate = time.AfterFunc(delay, publishProgress)
			return func() {}
		}
		return publishProgress
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
		detailSizes = append(detailSizes, 0)
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
			var childUpdate func(CodeToolReply)
			if update != nil {
				childUpdate = func(reply CodeToolReply) {
					mu.Lock()
					publish := func() {}
					if active && callCtx.Err() == nil && meta.Calls[index].Status == "running" {
						storeDetails(index, reply)
						meta.Calls[index].DurationMs = time.Since(callStart).Milliseconds()
						publish = progress()
					}
					mu.Unlock()
					publish()
				}
			}
			reply, callErr = authority.Call(callCtx, request.Name, string(request.Input), callID, childUpdate)
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
		var outputErr error
		if encodeErr != nil || len(encoded) > codemode.MaxHostResponseBytes {
			outputErr = errors.New("child reply is not valid JSON or exceeds the 2 MiB limit; the tool will not be retried")
		} else if callErr == nil && len(outputSchemas[request.Name]) > 0 {
			// Validate only successful replies, from the serialized snapshot rather
			// than raw reply data. A failure's partial result (ToolError.result, typed
			// unknown) passes through so its diagnostics are never replaced.
			var snapshot struct {
				Data json.RawMessage `json:"data"`
			}
			_ = json.Unmarshal(encoded, &snapshot)
			outputErr = codemode.ValidateOutputData(outputSchemas[request.Name], snapshot.Data)
			var validationErr *codemode.OutputValidationError
			if errors.As(outputErr, &validationErr) {
				// Scripts only see the sanitized message; tool authors need the locations.
				logger.G(ctx).
					WithField("tool", request.Name).
					WithField("call_id", callID).
					WithField("details", validationErr.Details).
					Warn("code execution child reply failed its declared outputSchema")
			}
		}
		if outputErr != nil {
			encoded = nil
			references = nil
			callErr = &CodeToolError{
				Kind:    "invalid_output",
				Tool:    request.Name,
				CallID:  callID,
				Outcome: outcome,
				Message: outputErr.Error(),
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
			storeDetails(index, reply)
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
		if callErr != nil && len(encoded) > 0 {
			return nil, &encodedCodeToolError{message: callErr.Error(), encoded: encoded}
		}
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
	if pendingUpdate != nil {
		pendingUpdate.Stop()
	}
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
