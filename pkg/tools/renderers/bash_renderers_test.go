package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestBashRenderer(t *testing.T) {
	renderer := &BashRenderer{}

	t.Run("Successful bash command execution", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "ls -la",
				ExitCode:      0,
				WorkingDir:    "/home/user",
				ExecutionTime: 150 * time.Millisecond,
				Output:        "total 16\ndrwxr-xr-x 3 user user 4096 Jan 1 12:00 .\ndrwxr-xr-x 5 user user 4096 Jan 1 11:00 ..\n-rw-r--r-- 1 user user  220 Jan 1 12:00 file.txt",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Command: ls -la", "Expected command in output")
		assert.Contains(t, output, "Exit Code: 0", "Expected exit code in output")
		assert.Contains(t, output, "Working Directory: /home/user", "Expected working directory in output")
		assert.Contains(t, output, "Execution Time: 150ms", "Expected execution time in output")
		assert.Contains(t, output, "Output:", "Expected output header in output")
		assert.Contains(t, output, "total 16", "Expected command output in output")
		assert.Contains(t, output, "drwxr-xr-x", "Expected detailed output in output")
	})

	t.Run("Bash command with non-zero exit code", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   false,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "grep nonexistent file.txt",
				ExitCode:      1,
				WorkingDir:    "/tmp",
				ExecutionTime: 50 * time.Millisecond,
				Output:        "grep: nonexistent: No such file or directory",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Command: grep nonexistent file.txt", "Expected command in output")
		assert.Contains(t, output, "Exit Code: 1", "Expected non-zero exit code in output")
		assert.Contains(t, output, "No such file or directory", "Expected error output in output")
		assert.Contains(t, output, "Error: ", "Expected error prefix in output")
	})

	t.Run("Bash command without working directory", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "echo hello",
				ExitCode:      0,
				WorkingDir:    "",
				ExecutionTime: 25 * time.Millisecond,
				Output:        "hello",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Command: echo hello", "Expected command in output")
		assert.Contains(t, output, "Exit Code: 0", "Expected exit code in output")
		assert.NotContains(t, output, "Working Directory:", "Should not show working directory when empty")
		assert.Contains(t, output, "hello", "Expected command output in output")
	})

	t.Run("Bash command without output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "touch newfile.txt",
				ExitCode:      0,
				WorkingDir:    "/tmp",
				ExecutionTime: 10 * time.Millisecond,
				Output:        "",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Command: touch newfile.txt", "Expected command in output")
		assert.Contains(t, output, "Exit Code: 0", "Expected exit code in output")
		assert.NotContains(t, output, "Output:", "Should not show output section when no output")
	})

	t.Run("Bash command with multiline output", func(t *testing.T) {
		multilineOutput := `#!/bin/bash
echo "Starting script"
for i in {1..3}; do
    echo "Iteration $i"
done
echo "Script completed"`

		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "cat script.sh",
				ExitCode:      0,
				WorkingDir:    "/home/user/scripts",
				ExecutionTime: 75 * time.Millisecond,
				Output:        multilineOutput,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Command: cat script.sh", "Expected command in output")
		assert.Contains(t, output, "#!/bin/bash", "Expected script shebang in output")
		assert.Contains(t, output, "for i in {1..3}", "Expected loop in output")
		assert.Contains(t, output, "Script completed", "Expected end of script in output")
	})

	t.Run("Long running command", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "find /usr -name '*.so' -type f | head -100",
				ExitCode:      0,
				WorkingDir:    "/",
				ExecutionTime: 5 * time.Second,
				Output:        "/usr/lib/x86_64-linux-gnu/libssl.so.1.1\n/usr/lib/x86_64-linux-gnu/libcrypto.so.1.1\n/usr/lib/x86_64-linux-gnu/libc.so.6",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "find /usr -name", "Expected long command in output")
		assert.Contains(t, output, "Execution Time: 5s", "Expected long execution time in output")
		assert.Contains(t, output, "libssl.so.1.1", "Expected library output in output")
	})

	t.Run("Command with special characters", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       `grep -r "function.*(" --include="*.js" .`,
				ExitCode:      0,
				WorkingDir:    "/app",
				ExecutionTime: 200 * time.Millisecond,
				Output:        "./src/utils.js:function parseData(input) {\n./src/main.js:function main() {",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, `grep -r "function.*("`, "Expected command with special characters in output")
		assert.Contains(t, output, "parseData(input)", "Expected function match in output")
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   false,
			Error:     "Command execution failed",
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "nonexistent-command",
				ExitCode:      1,
				ExecutionTime: 50 * time.Millisecond,
				Output:        "",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Command execution failed", "Expected error message in output")
		assert.Contains(t, output, "nonexistent-command", "Expected command in output")
		assert.Contains(t, output, "Exit Code: 1", "Expected exit code in output")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for bash", "Expected invalid metadata error")
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for bash", "Expected invalid metadata error for nil metadata")
	})

	t.Run("Command with very short execution time", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "pwd",
				ExitCode:      0,
				WorkingDir:    "/home/user",
				ExecutionTime: 1 * time.Microsecond,
				Output:        "/home/user",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Command: pwd", "Expected command in output")
		assert.Contains(t, output, "Execution Time: 1µs", "Expected microsecond execution time in output")
	})

	t.Run("Regular bash command still works", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "echo 'Hello World'",
				ExitCode:      0,
				WorkingDir:    "/tmp",
				ExecutionTime: 50 * time.Millisecond,
				Output:        "Hello World",
			},
		}

		output := renderer.RenderCLI(result)

		// Should still render as regular bash command, not background
		assert.Contains(t, output, "Command: echo 'Hello World'", "Expected regular command format")
		assert.Contains(t, output, "Exit Code: 0", "Expected exit code in output")
		assert.NotContains(t, output, "Background Command:", "Should not show background format for regular bash")
	})

	t.Run("RenderMarkdown successful command", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "pwd",
				ExitCode:      0,
				WorkingDir:    "/home/user/project",
				ExecutionTime: 25 * time.Millisecond,
				Output:        "/home/user/project",
			},
		}

		output := renderer.RenderMarkdown(result)

		assert.Contains(t, output, "- **Status:** success")
		assert.Contains(t, output, "- **Exit code:** 0")
		assert.Contains(t, output, "- **Working directory:** `/home/user/project`")
		assert.Contains(t, output, "- **Execution time:** `25ms`")
		assert.Contains(t, output, "**Command**")
		assert.Contains(t, output, "```bash\npwd\n```")
		assert.Contains(t, output, "**Output**")
		assert.Contains(t, output, "```text\n/home/user/project\n```")
	})

	t.Run("RenderMarkdown failed command", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   false,
			Error:     "command failed",
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "grep missing file.txt",
				ExitCode:      2,
				WorkingDir:    "/tmp",
				ExecutionTime: 50 * time.Millisecond,
				Output:        "grep: missing: No such file or directory",
			},
		}

		output := renderer.RenderMarkdown(result)

		assert.Contains(t, output, "- **Status:** failed")
		assert.Contains(t, output, "- **Exit code:** 2")
		assert.Contains(t, output, "- **Error:** `command failed`")
		assert.Contains(t, output, "```bash\ngrep missing file.txt\n```")
		assert.Contains(t, output, "No such file or directory")
	})

	t.Run("RenderMarkdown invalid metadata falls back", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{},
		}

		output := renderer.RenderMarkdown(result)

		assert.Contains(t, output, "- **Status:** success")
		assert.Contains(t, output, "Error: Invalid metadata type for bash")
		assert.Contains(t, output, "```text")
	})
}
