package tools

import (
	"context"
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

type ToolResult interface {
	AssistantFacing() string
	UserFacing() string
	IsError() bool
	GetError() string  // xxx: to be removed
	GetResult() string // xxx: to be removed
}

type BaseToolResult struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

func (t BaseToolResult) AssistantFacing() string {
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

func (t BaseToolResult) UserFacing() string {
	return t.AssistantFacing()
}

func (t BaseToolResult) IsError() bool {
	return t.Error != ""
}

func (t BaseToolResult) GetError() string {
	return t.Error
}

func (t BaseToolResult) GetResult() string {
	return t.Result
}

func StringifyToolResult(result, err string) string {
	out := ""
	if err != "" {
		out = fmt.Sprintf(`<error>
%s
</error>
`, err)
	}
	if result != "" {
		out += fmt.Sprintf(`<result>
%s
</result>
`, result)
	}
	return out
}

type State interface {
	SetFileLastAccessed(path string, lastAccessed time.Time) error
	GetFileLastAccessed(path string) (time.Time, error)
	ClearFileLastAccessed(path string) error
	TodoFilePath() (string, error)
	SetTodoFilePath(path string)
	SetFileLastAccess(fileLastAccess map[string]time.Time)
	FileLastAccess() map[string]time.Time
	BasicTools() []Tool
	MCPTools() []Tool
	Tools() []Tool
}
