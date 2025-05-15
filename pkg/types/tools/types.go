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

type ToolResult struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

func (t *ToolResult) String() string {
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

type State interface {
	SetFileLastAccessed(path string, lastAccessed time.Time) error
	GetFileLastAccessed(path string) (time.Time, error)
	ClearFileLastAccessed(path string) error
	TodoFilePath() string
	SetTodoFilePath(path string)
}
