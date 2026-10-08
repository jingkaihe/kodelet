// Package codemode provides isolated JavaScript orchestration and authorized tool discovery.
package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// Request is a host operation submitted by an isolated JavaScript invocation.
// Run identity, tool-call authority, and permissions belong to the host, not JS.
type Request struct {
	Operation string          `json:"operation"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Query     string          `json:"query,omitempty"`
	Options   json.RawMessage `json:"options,omitempty"`
}

// Handler executes an authorized tool or reads the invocation's catalog.
// It must honor cancellation and may be called concurrently. An error that
// implements json.Marshaler supplies the fields of the guest's serializable Error.
type Handler func(context.Context, Request) (any, error)

// MaxHostResponseBytes bounds a serialized host response before it enters the VM.
// Tool executors can apply this limit before recording their final child summary.
const MaxHostResponseBytes = 2 << 20

// OutputItem is a selected JSON value, image, or artifact reference. Type is
// "json", "image", or "artifact", set by the host operation rather than inferred
// from an emitted JSON value. Value is used only for JSON; ArtifactID is used
// only for media, with optional Detail "original" for images.
type OutputItem struct {
	Type       string          `json:"type"`
	Value      json.RawMessage `json:"value,omitempty"`
	ArtifactID string          `json:"artifactId,omitempty"`
	Detail     string          `json:"detail,omitempty"`
}

// Result contains only explicitly emitted values and the async body's return value.
// Execute preserves previously emitted outputs when the invocation fails.
type Result struct {
	Outputs []OutputItem `json:"outputs"`
}

// Error is a serializable runtime or host-operation failure.
type Error struct {
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
	Tool    string          `json:"tool,omitempty"`
	CallID  string          `json:"callId,omitempty"`
	Outcome string          `json:"outcome,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

// Error implements error.
func (e *Error) Error() string { return e.Message }

type runtimeLimits struct {
	timeout         time.Duration
	memoryBytes     uint32
	scriptBytes     int
	requestBytes    int
	responseBytes   int
	outputBytes     int
	outputCount     int
	toolCalls       int
	catalogCalls    int
	pendingCalls    int
	toolConcurrency int
	retainedBytes   int
	unhandledCount  int
}

func defaultRuntimeLimits() runtimeLimits {
	return runtimeLimits{
		timeout:         120 * time.Second,
		memoryBytes:     256 << 20,
		scriptBytes:     256 << 10,
		requestBytes:    2 << 20,
		responseBytes:   MaxHostResponseBytes,
		outputBytes:     32 << 10,
		outputCount:     1024,
		toolCalls:       128,
		catalogCalls:    256,
		pendingCalls:    256,
		toolConcurrency: 4,
		retainedBytes:   16 << 20,
		unhandledCount:  256,
	}
}

// Execute runs an async JavaScript function body in a fresh QuickJS/WASM VM.
// It grants no ambient filesystem, network, environment, or module-loading access.
// Host callbacks are bounded and cancel with ctx; Execute does not wait for a
// callback that ignores cancellation, and such a callback can never access the VM.
// Media emissions require authority and are rejected; use ExecuteWithOutputValidator
// to authorize them.
func Execute(ctx context.Context, code string, handler Handler) (Result, error) {
	return ExecuteWithOutputValidator(ctx, code, handler, nil)
}

// ExecuteWithOutputValidator runs code with optional authorization of each output.
// The validator runs synchronously on the VM owner after structural and budget
// validation and before append. It must perform only local, nonblocking checks;
// it must not resolve artifacts, execute tools, or re-enter the VM. A nil validator
// permits JSON but rejects media. Errors are immediately catchable by emit callers.
func ExecuteWithOutputValidator(ctx context.Context, code string, handler Handler, validator func(OutputItem) error) (Result, error) {
	return executeWithLimits(ctx, code, handler, validator, defaultRuntimeLimits())
}

func executeWithLimits(ctx context.Context, code string, handler Handler, validator func(OutputItem) error, limits runtimeLimits) (Result, error) {
	result := Result{Outputs: []OutputItem{}}
	if len(code) > limits.scriptBytes {
		return result, &Error{Kind: "limit", Message: "code exceeds the script byte limit"}
	}
	ctx, cancel := context.WithTimeout(ctx, limits.timeout)
	defer cancel()
	bridge := newRuntimeBridge(ctx, handler, validator, limits, &result)
	vm, err := newRuntimeVM(ctx, bridge, limits)
	if err != nil {
		return result, errors.Wrap(err, "initialize code runtime")
	}
	defer vm.close()
	bridge.startWorkers()
	if err := vm.execute(code); err != nil {
		return result, err
	}
	return result, nil
}

type runtimeRequest struct {
	ID      uint32  `json:"id"`
	Request Request `json:"request"`
}

type runtimeCompletion struct {
	ID       uint32          `json:"id"`
	Success  bool            `json:"success"`
	Value    json.RawMessage `json:"value"`
	reserved bool
	request  Request
}

// Channels are shared with workers; other bridge bookkeeping is VM-owned.
type runtimeBridge struct {
	ctx           context.Context
	handler       Handler
	validator     func(OutputItem) error
	limits        runtimeLimits
	result        *Result
	tools         chan runtimeRequest
	catalog       chan runtimeRequest
	completions   chan runtimeCompletion
	responseSlots chan struct{}
	pending       map[uint32]int
	lastID        uint32
	toolCalls     int
	catalogCalls  int
	requestBytes  int
	outputBytes   int
	mediaCount    int
	closed        bool
}

func newRuntimeBridge(ctx context.Context, handler Handler, validator func(OutputItem) error, limits runtimeLimits, result *Result) *runtimeBridge {
	return &runtimeBridge{
		ctx:           ctx,
		handler:       handler,
		validator:     validator,
		limits:        limits,
		result:        result,
		tools:         make(chan runtimeRequest, limits.pendingCalls),
		catalog:       make(chan runtimeRequest, limits.pendingCalls),
		completions:   make(chan runtimeCompletion, limits.pendingCalls),
		responseSlots: make(chan struct{}, max(1, limits.retainedBytes/limits.responseBytes)),
		pending:       make(map[uint32]int),
	}
}

func (b *runtimeBridge) startWorkers() {
	for range b.limits.toolConcurrency {
		go b.worker(b.tools)
	}
	// Catalog operations share one bounded worker, not the tool semaphore.
	go b.worker(b.catalog)
}

func (b *runtimeBridge) worker(queue <-chan runtimeRequest) {
	for {
		select {
		case <-b.ctx.Done():
			return
		case request := <-queue:
			// Reserve capacity before executing the handler. Queue saturation
			// must not convert a completed tool into an unrecorded delivery error.
			select {
			case b.responseSlots <- struct{}{}:
			case <-b.ctx.Done():
				return
			}
			if b.ctx.Err() != nil {
				<-b.responseSlots
				return
			}
			completion := b.handle(request)
			completion.reserved = true
			if b.ctx.Err() != nil {
				<-b.responseSlots
				return
			}
			select {
			case b.completions <- completion:
			case <-b.ctx.Done():
				<-b.responseSlots
				return
			}
		}
	}
}

func (b *runtimeBridge) handle(request runtimeRequest) (completion runtimeCompletion) {
	completion.ID = request.ID
	completion.request = request.Request
	defer func() {
		if recover() != nil {
			completion.Success = false
			completion.Value = runtimeRequestErrorJSON(request.Request, &Error{
				Kind: "tool_error", Message: "host operation panicked", Outcome: "unknown",
			})
		}
	}()
	if b.handler == nil {
		completion.Value = runtimeRequestErrorJSON(request.Request, &Error{
			Kind: "blocked", Message: "host operations are unavailable", Outcome: "not_started",
		})
		return completion
	}
	value, err := b.handler(b.ctx, request.Request)
	if err != nil {
		completion.Value = runtimeRequestErrorJSON(request.Request, err)
	} else {
		completion.Value, err = json.Marshal(value)
		completion.Success = err == nil
		if err != nil {
			completion.Value = runtimeRequestErrorJSON(request.Request, &Error{
				Kind: "invalid_output", Message: "host result is not JSON serializable", Outcome: "completed",
			})
		}
	}
	if len(completion.Value) > b.limits.responseBytes {
		completion.Value = runtimeOutputErrorJSON(completion, "invalid_output", "host result exceeds the response byte limit; the operation will not be retried")
		completion.Success = false
	}
	return completion
}

func runtimeRequestErrorJSON(request Request, err error) json.RawMessage {
	var marshaler json.Marshaler
	var data []byte
	if errors.As(err, &marshaler) {
		data, _ = marshaler.MarshalJSON()
	} else {
		data, _ = json.Marshal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	if _, ok := fields["message"]; !ok {
		fields["message"], _ = json.Marshal(err.Error())
	}
	if _, ok := fields["kind"]; !ok {
		fields["kind"] = json.RawMessage(`"tool_error"`)
	}
	if request.Operation == "tool.call" {
		if _, exists := fields["tool"]; !exists {
			fields["tool"], _ = json.Marshal(request.Name)
		}
		if _, exists := fields["outcome"]; !exists {
			fields["outcome"] = json.RawMessage(`"unknown"`)
		}
	}
	data, _ = json.Marshal(fields)
	return data
}

func runtimeOutputErrorJSON(completion runtimeCompletion, kind, message string) json.RawMessage {
	failure := &Error{Kind: kind, Message: message}
	if completion.request.Operation == "tool.call" {
		failure.Tool = completion.request.Name
		failure.Outcome = "completed"
		if !completion.Success {
			var previous Error
			if json.Unmarshal(completion.Value, &previous) == nil {
				failure.CallID = previous.CallID
				if previous.Outcome != "" {
					failure.Outcome = previous.Outcome
				}
			}
		}
	}
	data, _ := json.Marshal(failure)
	return data
}

func (b *runtimeBridge) submit(data []byte) (err error) {
	var request runtimeRequest
	defer func() {
		var failure *Error
		if request.Request.Operation == "tool.call" && errors.As(err, &failure) {
			failure.Tool = request.Request.Name
			failure.Outcome = "not_started"
		}
	}()
	if b.closed || b.ctx.Err() != nil {
		return &Error{Kind: "cancelled", Message: "invocation no longer accepts host requests"}
	}
	if len(data) > b.limits.requestBytes {
		return &Error{Kind: "limit", Message: "host request exceeds the byte limit"}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return &Error{Kind: "invalid_input", Message: "host request must be valid JSON with known fields"}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return &Error{Kind: "invalid_input", Message: "host request contains trailing data"}
	}
	if request.ID == 0 || request.ID <= b.lastID {
		return &Error{Kind: "invalid_input", Message: "host request ID is invalid or reused"}
	}
	b.lastID = request.ID
	if err := validateRuntimeRequest(request.Request); err != nil {
		return err
	}
	if len(b.pending) >= b.limits.pendingCalls || b.requestBytes+len(data) > b.limits.retainedBytes {
		return &Error{Kind: "limit", Message: "too many pending host requests"}
	}
	queue := b.catalog
	if request.Request.Operation == "tool.call" {
		if b.toolCalls >= b.limits.toolCalls {
			return &Error{Kind: "limit", Message: "tool call limit exceeded"}
		}
		b.toolCalls++
		queue = b.tools
	} else {
		if b.catalogCalls >= b.limits.catalogCalls {
			return &Error{Kind: "limit", Message: "catalog request limit exceeded"}
		}
		b.catalogCalls++
	}
	// Each admitted request reserves one completion slot. No queue send can
	// block the VM while it is inside its synchronous native submission call.
	b.pending[request.ID] = len(data)
	b.requestBytes += len(data)
	queue <- request
	return nil
}

func validateRuntimeRequest(request Request) error {
	invalid := func(message string) error { return &Error{Kind: "invalid_input", Message: message} }
	isObject := func(raw json.RawMessage) bool {
		trimmed := bytes.TrimSpace(raw)
		return len(trimmed) > 0 && trimmed[0] == '{'
	}
	if len(request.Name) > 512 || strings.ContainsRune(request.Name, '\x00') {
		return invalid("invalid tool name")
	}
	switch request.Operation {
	case "tool.call":
		if request.Name == "" || !isObject(request.Input) || request.Query != "" || len(request.Options) != 0 {
			return invalid("tool calls require a name and a JSON input object")
		}
		if request.Name == "code_execute" {
			return &Error{
				Kind:    "blocked",
				Message: "recursive code_execute is not allowed",
				Tool:    request.Name,
				Outcome: "not_started",
			}
		}
	case "catalog.list", "catalog.search":
		if request.Name != "" || len(request.Input) != 0 || (len(request.Options) != 0 && !isObject(request.Options)) {
			return invalid("catalog options must be a JSON object")
		}
		if len(request.Query) > 4096 || (request.Operation == "catalog.list" && request.Query != "") {
			return invalid("invalid catalog query")
		}
	case "catalog.describe":
		if request.Name == "" || len(request.Input) != 0 || request.Query != "" || len(request.Options) != 0 {
			return invalid("catalog.describe requires only a tool name")
		}
	default:
		return invalid("unknown host operation")
	}
	return nil
}

func (b *runtimeBridge) emit(data json.RawMessage) error {
	if !json.Valid(data) {
		return &Error{Kind: "invalid_output", Message: "output is not valid JSON"}
	}
	return b.appendOutput(OutputItem{Type: "json", Value: bytes.Clone(data)}, len(data))
}

func (b *runtimeBridge) emitMedia(outputType string, data json.RawMessage) error {
	var reference struct {
		ArtifactID string          `json:"artifactId"`
		Detail     json.RawMessage `json:"detail"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil {
		return &Error{Kind: "invalid_output", Message: "media output requires an artifactId and only supported options"}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return &Error{Kind: "invalid_output", Message: "media output contains trailing data"}
	}
	if reference.ArtifactID == "" {
		return &Error{Kind: "invalid_output", Message: "media output requires an exact artifact ID"}
	}
	// IDs are opaque tokens, not paths, URLs, short-link resolution, or image data.
	// The validator supplies exact-ID authority, not a lookup or normalization step.
	for _, char := range reference.ArtifactID {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return &Error{Kind: "invalid_output", Message: "media output requires an exact artifact ID, not a path, URL, or base64 payload"}
	}
	item := OutputItem{Type: outputType, ArtifactID: reference.ArtifactID}
	if len(reference.Detail) != 0 {
		if outputType != "image" || json.Unmarshal(reference.Detail, &item.Detail) != nil || item.Detail != "original" {
			return &Error{Kind: "invalid_output", Message: "image detail must be original; artifact outputs do not accept options"}
		}
	}
	// Charge the canonical descriptor, never the bytes of the referenced artifact.
	descriptor, _ := json.Marshal(item)
	return b.appendOutput(item, len(descriptor))
}

func (b *runtimeBridge) appendOutput(item OutputItem, size int) error {
	if b.outputBytes+size > b.limits.outputBytes || len(b.result.Outputs) >= b.limits.outputCount {
		return &Error{Kind: "limit", Message: "selected output limit exceeded"}
	}
	media := item.Type != "json"
	if media && b.mediaCount >= 8 {
		return &Error{Kind: "limit", Message: "media output limit exceeded (8 items)"}
	}
	if media && b.validator == nil {
		return &Error{Kind: "blocked", Message: "media output requires an output validator"}
	}
	if b.validator != nil {
		if err := b.validator(item); err != nil {
			return err
		}
	}
	b.result.Outputs = append(b.result.Outputs, item)
	b.outputBytes += size
	if media {
		b.mediaCount++
	}
	return nil
}

func (b *runtimeBridge) consume(completion runtimeCompletion) bool {
	size, exists := b.pending[completion.ID]
	if !exists {
		return false
	}
	b.requestBytes -= size
	delete(b.pending, completion.ID)
	if completion.reserved {
		<-b.responseSlots
	}
	return true
}
