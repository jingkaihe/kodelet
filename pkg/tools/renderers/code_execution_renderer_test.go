package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestCodeExecutionRenderer(t *testing.T) {
	renderer := &CodeExecutionRenderer{}

	t.Run("Successful code execution with all fields", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "Node.js v22.17.0",
				Code:    "console.log('Hello, World!');",
				Output:  "Hello, World!\n",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Runtime: Node.js v22.17.0", "Expected runtime in output")
		assert.Contains(t, output, "Code:", "Expected code section header in output")
		assert.Contains(t, output, "console.log('Hello, World!');", "Expected code content in output")
		assert.Contains(t, output, "Output:", "Expected output section header in output")
		assert.Contains(t, output, "Hello, World!", "Expected output content in output")
	})

	t.Run("Code execution with multiline code", func(t *testing.T) {
		code := `import * as lsp from './servers/lsp/index.js';

const defs = await lsp.definition({ 
  symbolName: 'MyFunction'
});

console.log('Found definition at:', defs.filePath);`

		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "Node.js with tsx",
				Code:    code,
				Output:  "Found definition at: /path/to/file.ts\n",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Runtime: Node.js with tsx", "Expected runtime in output")
		assert.Contains(t, output, "import * as lsp", "Expected import statement in output")
		assert.Contains(t, output, "await lsp.definition", "Expected function call in output")
		assert.Contains(t, output, "Found definition at: /path/to/file.ts", "Expected output in output")
	})

	t.Run("Code execution with empty output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "Python 3.11",
				Code:    "x = 42",
				Output:  "",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Runtime: Python 3.11", "Expected runtime in output")
		assert.Contains(t, output, "Code:", "Expected code section header in output")
		assert.Contains(t, output, "x = 42", "Expected code content in output")
		assert.NotContains(t, output, "Output:", "Should not show output section for empty output")
	})

	t.Run("Code execution with no runtime specified", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "",
				Code:    "print('test')",
				Output:  "test\n",
			},
		}

		output := renderer.RenderCLI(result)

		assert.NotContains(t, output, "Runtime:", "Should not show runtime when not specified")
		assert.Contains(t, output, "Code:", "Expected code section header in output")
		assert.Contains(t, output, "print('test')", "Expected code content in output")
		assert.Contains(t, output, "Output:", "Expected output section header in output")
		assert.Contains(t, output, "test", "Expected output content in output")
	})

	t.Run("Code execution with long output", func(t *testing.T) {
		longOutput := `Line 1
Line 2
Line 3
Line 4
Line 5
Line 6
Line 7
Line 8
Line 9
Line 10`

		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "Bash",
				Code:    "for i in {1..10}; do echo \"Line $i\"; done",
				Output:  longOutput,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Line 1", "Expected first line in output")
		assert.Contains(t, output, "Line 10", "Expected last line in output")
		assert.Contains(t, output, "for i in {1..10}", "Expected code content in output")
	})

	t.Run("Code execution with error output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "Node.js",
				Code:    "throw new Error('Test error');",
				Output:  "Error: Test error\n    at Object.<anonymous> (/path/to/file.js:1:7)",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "throw new Error('Test error');", "Expected code content in output")
		assert.Contains(t, output, "Error: Test error", "Expected error message in output")
	})

	t.Run("Error handling without metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   false,
			Error:     "Code execution failed: syntax error",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Code execution failed: syntax error", "Expected error message in output")
	})

	t.Run("Error handling with metadata shows stderr output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   false,
			Error:     "execution failed: exit status 1",
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "Node.js",
				Code:    "const x: string = 123;",
				Output:  "TypeError: Type 'number' is not assignable to type 'string'.\n    at file.ts:1:7",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: execution failed: exit status 1", "Expected error message in output")
		assert.Contains(t, output, "Output:", "Expected output section header")
		assert.Contains(t, output, "TypeError:", "Expected TypeScript error in output")
		assert.Contains(t, output, "at file.ts:1:7", "Expected stack trace in output")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BashMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for code_execution", "Expected invalid metadata error")
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for code_execution", "Expected invalid metadata error for nil metadata")
	})

	t.Run("Empty code and output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "code_execution",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CodeExecutionMetadata{
				Runtime: "Test Runtime",
				Code:    "",
				Output:  "",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Runtime: Test Runtime", "Expected runtime in output")
		assert.NotContains(t, output, "Code:", "Should not show code section for empty code")
		assert.NotContains(t, output, "Output:", "Should not show output section for empty output")
	})
}
