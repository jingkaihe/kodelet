// Package tools defines interfaces and types for kodelet's tool system
// including tool execution, result structures, state management,
// and JSON schema generation for LLM tool integration.
package tools

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"
)

// Tool defines the interface for all kodelet tools
type Tool interface {
	GenerateSchema() *jsonschema.Schema
	Name() string
	Description() string
	ValidateInput(state State, parameters string) error
	Execute(ctx context.Context, state State, parameters string) ToolResult
	TracingKVs(parameters string) ([]attribute.KeyValue, error)
}

// ToolResult represents the outcome of a tool execution
type ToolResult interface {
	AssistantFacing() string
	IsError() bool
	GetError() string  // xxx: to be removed
	GetResult() string // xxx: to be removed
	StructuredData() StructuredToolResult
}

// BaseToolResult provides a basic implementation of the ToolResult interface
type BaseToolResult struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

// AssistantFacing returns a formatted string representation of the result for the LLM
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

// IsError returns true if the tool execution resulted in an error
func (t BaseToolResult) IsError() bool {
	return t.Error != ""
}

// GetError returns the error message if any
func (t BaseToolResult) GetError() string {
	return t.Error
}

// GetResult returns the result string
func (t BaseToolResult) GetResult() string {
	return t.Result
}

// StructuredData returns a structured representation of the tool result
func (t BaseToolResult) StructuredData() StructuredToolResult {
	return StructuredToolResult{
		ToolName:  "unknown", // This will be overridden by specific tool implementations
		Success:   !t.IsError(),
		Error:     t.Error,
		Timestamp: time.Now(),
		// Metadata will be nil for BaseToolResult
	}
}

// StringifyToolResult formats a tool result and optional error into a string representation
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

// BlockedToolResult represents a tool that was blocked by a lifecycle hook
type BlockedToolResult struct {
	ToolName string `json:"tool_name"`
	Reason   string `json:"reason"`
}

// NewBlockedToolResult creates a new BlockedToolResult with the given tool name and reason
func NewBlockedToolResult(toolName, reason string) BlockedToolResult {
	return BlockedToolResult{ToolName: toolName, Reason: reason}
}

// AssistantFacing returns a formatted string representation of the blocked result for the LLM
func (t BlockedToolResult) AssistantFacing() string {
	return fmt.Sprintf(`<error>
Tool execution was blocked by security hook: %s
</error>
`, t.Reason)
}

// IsError returns true as blocked tools are treated as errors
func (t BlockedToolResult) IsError() bool {
	return true
}

// GetError returns the blocked reason as an error message
func (t BlockedToolResult) GetError() string {
	return fmt.Sprintf("blocked by hook: %s", t.Reason)
}

// GetResult returns an empty string as blocked tools have no result
func (t BlockedToolResult) GetResult() string {
	return ""
}

// StructuredData returns a structured representation of the blocked tool result
func (t BlockedToolResult) StructuredData() StructuredToolResult {
	return StructuredToolResult{
		ToolName:  t.ToolName,
		Success:   false,
		Error:     t.GetError(),
		Timestamp: time.Now(),
		Metadata:  BlockedMetadata(t),
	}
}

// BlockedMetadata contains metadata about a blocked tool invocation
type BlockedMetadata struct {
	ToolName string `json:"tool_name"`
	Reason   string `json:"reason"`
}

// ToolType returns the tool type identifier for blocked tools
func (m BlockedMetadata) ToolType() string { return "blocked" }

// BackgroundProcess represents a process running in the background
type BackgroundProcess struct {
	PID       int         `json:"pid"`
	Command   string      `json:"command"`
	LogPath   string      `json:"log_path"`
	StartTime time.Time   `json:"start_time"`
	Process   *os.Process `json:"-"` // Not serialized
}

// State defines the interface for managing tool execution state and context
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
	DiscoverContexts() map[string]string

	// LLM configuration access
	GetLLMConfig() any // Returns llmtypes.Config but using any to avoid circular import

	// File locking for atomic operations
	// LockFile acquires an exclusive lock for the given file path to prevent race conditions
	// during read-modify-write operations. Must be paired with UnlockFile.
	LockFile(path string)
	// UnlockFile releases the lock for the given file path
	UnlockFile(path string)
}
