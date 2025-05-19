package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"
)

type Tool interface {
	GenerateSchema() *jsonschema.Schema
	Name() string
	Description() string
	ValidateInput(state State, parameters string) error
	Execute(ctx context.Context, state State, parameters string) ToolResult
	TracingKVs(parameters string) ([]attribute.KeyValue, error)
}

// ToolResult is an interface for handling tool execution results
type ToolResult interface {
	LLMMessage() string  // String representation for LLM consumption
	UserMessage() string // String representation for user display
	IsError() bool       // Whether the result represents an error

	JSONMarshal() ([]byte, error)    // Custom JSON marshaling
	JSONUnmarshal(data []byte) error // Custom JSON unmarshaling
}

// DefaultToolResult is the default implementation of the ToolResult interface
type DefaultToolResult struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

// LLMMessage returns a formatted string for LLM consumption
func (t *DefaultToolResult) LLMMessage() string {
	out := ""
	if t.Error != "" {
		out = fmt.Sprintf(`<error>
%s
</error>
`, t.Error)
	}
	if t.Result != "" {
		out += fmt.Sprintf(`<result>
%s
</result>
`, t.Result)
	}
	return out
}

// UserMessage returns a formatted string for user display
func (t *DefaultToolResult) UserMessage() string {
	if t.Error != "" {
		return fmt.Sprintf("Error: %s", t.Error)
	}
	return t.Result
}

// IsError returns whether the result represents an error
func (t *DefaultToolResult) IsError() bool {
	return t.Error != ""
}

// JSONMarshal implements custom JSON marshaling
func (t *DefaultToolResult) JSONMarshal() ([]byte, error) {
	return json.Marshal(t)
}

// JSONUnmarshal implements custom JSON unmarshaling
func (t *DefaultToolResult) JSONUnmarshal(data []byte) error {
	return json.Unmarshal(data, t)
}

// String implements the Stringer interface (for backward compatibility)
func (t *DefaultToolResult) String() string {
	return t.LLMMessage()
}

type State interface {
	SetFileLastAccessed(path string, lastAccessed time.Time) error
	GetFileLastAccessed(path string) (time.Time, error)
	ClearFileLastAccessed(path string) error
	TodoFilePath() string
	SetTodoFilePath(path string)

	Tools() []Tool
}
