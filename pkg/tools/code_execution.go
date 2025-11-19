package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/mcp/runtime"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed descriptions/code_execution.txt
var codeExecutionDescription string

// CodeExecutionTool enables TypeScript code execution with MCP tool access
type CodeExecutionTool struct {
	runtime *runtime.NodeRuntime
}

// CodeExecutionInput represents the input parameters for code execution
type CodeExecutionInput struct {
	CodePath    string `json:"code_path" jsonschema:"required,description=Path to the TypeScript/JavaScript file to execute (relative to .kodelet/mcp/)"`
	Description string `json:"description,omitempty" jsonschema:"description=Brief description of what this code does"`
}

// CodeExecutionResult holds the result of code execution
type CodeExecutionResult struct {
	code    string
	output  string
	err     string
	runtime string
}

// GetResult returns the tool output
func (r *CodeExecutionResult) GetResult() string {
	return r.output
}

// GetError returns the error message
func (r *CodeExecutionResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *CodeExecutionResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *CodeExecutionResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.output, r.err)
}

// StructuredData returns structured metadata about the execution
func (r *CodeExecutionResult) StructuredData() tooltypes.StructuredToolResult {
	return tooltypes.StructuredToolResult{
		ToolName:  "code_execution",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
		Error:     r.err,
		Metadata: &tooltypes.CodeExecutionMetadata{
			Code:    r.code,
			Output:  r.output,
			Runtime: r.runtime,
		},
	}
}

// NewCodeExecutionTool creates a new code execution tool
func NewCodeExecutionTool(runtime *runtime.NodeRuntime) *CodeExecutionTool {
	return &CodeExecutionTool{
		runtime: runtime,
	}
}

// Name returns the name of the tool
func (t *CodeExecutionTool) Name() string {
	return "code_execution"
}

// Description returns the description of the tool for the LLM
func (t *CodeExecutionTool) Description() string {
	return codeExecutionDescription
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *CodeExecutionTool) GenerateSchema() *jsonschema.Schema {
	reflector := jsonschema.Reflector{
		DoNotReference: true,
	}
	return reflector.Reflect(&CodeExecutionInput{})
}

// TracingKVs returns tracing key-value pairs
func (t *CodeExecutionTool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
	return nil, nil
}

// ValidateInput validates the input parameters
func (t *CodeExecutionTool) ValidateInput(_ tooltypes.State, _ string) error {
	return nil
}

// Execute runs the code execution tool
func (t *CodeExecutionTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	var input CodeExecutionInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &CodeExecutionResult{
			err:     fmt.Sprintf("invalid parameters: %v", err),
			runtime: t.runtime.Name(),
		}
	}

	// Read code from file
	// CodePath is relative to .kodelet/mcp/ workspace
	fullPath := filepath.Join(".kodelet", "mcp", input.CodePath)
	codeBytes, err := os.ReadFile(fullPath)
	if err != nil {
		return &CodeExecutionResult{
			err:     fmt.Sprintf("failed to read file %s: %v", input.CodePath, err),
			runtime: t.runtime.Name(),
		}
	}
	code := string(codeBytes)

	// Execute code
	output, err := t.runtime.Execute(ctx, code)
	if err != nil {
		return &CodeExecutionResult{
			code:    code,
			output:  output,
			err:     fmt.Sprintf("execution failed: %v", err),
			runtime: t.runtime.Name(),
		}
	}

	return &CodeExecutionResult{
		code:    code,
		output:  output,
		runtime: t.runtime.Name(),
	}
}
