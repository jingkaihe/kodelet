// Package delegation defines credential-free, scoped child execution contracts.
package delegation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

const (
	StartMethod   = "child.start"
	ReadMethod    = "child.read"
	CancelMethod  = "child.cancel"
	ReleaseMethod = "child.lease.release"
)

// Profile is owned by one extension process, never a daemon profile or Config.
type Profile struct {
	Name             string                     `json:"name"`
	Options          *llmtypes.ExecutionOptions `json:"options,omitempty"`
	SystemPromptPath string                     `json:"systemPromptPath,omitempty"`
	SystemPrompt     string                     `json:"systemPrompt,omitempty"`
}

// Preset is the runner-resolved snapshot advertised with its environment.
type Preset struct {
	Profile
	ExtensionID string `json:"extensionId"`
	Generation  uint64 `json:"generation"`
}

func (p Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 || strings.ContainsAny(p.Name, "/\\\x00") {
		return errors.New("execution preset requires a simple nonempty name")
	}
	if len(p.SystemPrompt) > 256*1024 || len(p.SystemPromptPath) > 8192 {
		return errors.New("execution preset prompt exceeds limit")
	}
	if p.SystemPrompt != "" && p.SystemPromptPath != "" {
		return errors.New("execution preset must use prompt content or a prompt path, not both")
	}
	return p.Options.Validate()
}

// Request never accepts a conversation ID, provider credentials, or an endpoint.
type Request struct {
	RequestID    string                     `json:"requestId"`
	Profile      string                     `json:"profile"`
	Message      string                     `json:"message"`
	Options      *llmtypes.ExecutionOptions `json:"options,omitempty"`
	SystemPrompt string                     `json:"systemPrompt,omitempty"`
	CWD          string                     `json:"cwd,omitempty"`
	LeaseID      string                     `json:"leaseId,omitempty"`
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.RequestID) == "" || len(r.RequestID) > 128 || strings.TrimSpace(r.Profile) == "" || strings.TrimSpace(r.Message) == "" {
		return errors.New("child requestId, profile and message are required")
	}
	if len(r.Message) > 512*1024 || len(r.SystemPrompt) > 256*1024 || len(r.CWD) > 8192 {
		return errors.New("child input exceeds limit")
	}
	return r.Options.Validate()
}

// Params is sent only on the authenticated runner connection.
type Params struct {
	RunID       string  `json:"runId"`
	ToolCallID  string  `json:"toolCallId,omitempty"`
	ExtensionID string  `json:"extensionId"`
	Generation  uint64  `json:"generation"`
	Request     Request `json:"request,omitempty"`
	ChildID     string  `json:"childId,omitempty"`
	LeaseID     string  `json:"leaseId,omitempty"`
	After       uint64  `json:"after,omitempty"`
}

type Identity struct {
	ConversationID       string `json:"conversationId"`
	RunID                string `json:"runId"`
	ParentConversationID string `json:"parentConversationId"`
	ParentRunID          string `json:"parentRunId"`
	ExtensionID          string `json:"extensionId"`
	Profile              string `json:"profile"`
}

type Event struct {
	Sequence   uint64 `json:"sequence"`
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
}

type Result struct {
	Identity
	Done      bool    `json:"done"`
	Cancelled bool    `json:"cancelled,omitempty"`
	Error     string  `json:"error,omitempty"`
	Output    string  `json:"output,omitempty"`
	Events    []Event `json:"events,omitempty"`
}

// Prepare validates and freezes configuration before any child is admitted.
// Run executes the existing persisted chat path, not a second agent loop.
type (
	Run        func(context.Context, func(Event)) error
	Prepare    func(context.Context, Request, Preset, Identity) (Run, error)
	prepareKey struct{}
)

func WithPrepare(ctx context.Context, prepare Prepare) context.Context {
	return context.WithValue(ctx, prepareKey{}, prepare)
}

func PrepareFromContext(ctx context.Context) Prepare {
	prepare, _ := ctx.Value(prepareKey{}).(Prepare)
	return prepare
}

type admissionKey struct{}

// WithAdmission connects durable chat initialization to registry admission.
func WithAdmission(ctx context.Context, admit func() error) context.Context {
	return context.WithValue(ctx, admissionKey{}, admit)
}

// Admit is called after identity persistence, before any runner/provider effects.
func Admit(ctx context.Context) error {
	if admit, ok := ctx.Value(admissionKey{}).(func() error); ok {
		return admit()
	}
	return nil
}

// Decode rejects unknown fields, null options and trailing data at the runner
// boundary. Nested ExecutionOptions perform their own strict validation.
func Decode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing child request data")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("child input must be an object")
	}
	for name, raw := range fields {
		if strings.EqualFold(name, "options") && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("child options must not be null")
		}
		if strings.EqualFold(name, "request") {
			var nested Request
			if err := Decode(raw, &nested); err != nil {
				return err
			}
		}
	}
	return nil
}
