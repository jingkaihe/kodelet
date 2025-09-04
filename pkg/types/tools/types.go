package tools

import (
	"context"
	"fmt"
	"os"
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
	IsError() bool
	GetError() string  // xxx: to be removed
	GetResult() string // xxx: to be removed
	StructuredData() StructuredToolResult
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

func (t BaseToolResult) IsError() bool {
	return t.Error != ""
}

func (t BaseToolResult) GetError() string {
	return t.Error
}

func (t BaseToolResult) GetResult() string {
	return t.Result
}

func (t BaseToolResult) StructuredData() StructuredToolResult {
	return StructuredToolResult{
		ToolName:  "unknown", // This will be overridden by specific tool implementations
		Success:   !t.IsError(),
		Error:     t.Error,
		Timestamp: time.Now(),
		// Metadata will be nil for BaseToolResult
	}
}

func StringifyToolResult(result, err string) string {
	out := ""
	if err != "" {
		out = fmt.Sprintf(`<error>
%s
</error>
`, err)
	}
	if result == "" {
		result = "(No output)"
	}
	out += fmt.Sprintf("<result>\n%s\n</result>\n", result)
	return out
}

type BackgroundProcess struct {
	PID       int         `json:"pid"`
	Command   string      `json:"command"`
	LogPath   string      `json:"log_path"`
	StartTime time.Time   `json:"start_time"`
	Process   *os.Process `json:"-"` // Not serialized
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
	// Background process management
	AddBackgroundProcess(process BackgroundProcess) error
	GetBackgroundProcesses() []BackgroundProcess
	RemoveBackgroundProcess(pid int) error

	// Context discovery
	GetRelevantContexts() map[string]string

	// LLM configuration access
	GetLLMConfig() interface{} // Returns llmtypes.Config but using interface{} to avoid circular import
}
