package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "Command: ls -la") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "Exit Code: 0") {
			t.Errorf("Expected exit code in output, got: %s", output)
		}
		if !strings.Contains(output, "Working Directory: /home/user") {
			t.Errorf("Expected working directory in output, got: %s", output)
		}
		if !strings.Contains(output, "Execution Time: 150ms") {
			t.Errorf("Expected execution time in output, got: %s", output)
		}
		if !strings.Contains(output, "Output:") {
			t.Errorf("Expected output header in output, got: %s", output)
		}
		if !strings.Contains(output, "total 16") {
			t.Errorf("Expected command output in output, got: %s", output)
		}
		if !strings.Contains(output, "drwxr-xr-x") {
			t.Errorf("Expected detailed output in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Command: grep nonexistent file.txt") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "Exit Code: 1") {
			t.Errorf("Expected non-zero exit code in output, got: %s", output)
		}
		if !strings.Contains(output, "No such file or directory") {
			t.Errorf("Expected error output in output, got: %s", output)
		}
		if !strings.Contains(output, "Error: ") {
			t.Errorf("Expected error prefix in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Command: echo hello") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "Exit Code: 0") {
			t.Errorf("Expected exit code in output, got: %s", output)
		}
		if strings.Contains(output, "Working Directory:") {
			t.Errorf("Should not show working directory when empty, got: %s", output)
		}
		if !strings.Contains(output, "hello") {
			t.Errorf("Expected command output in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Command: touch newfile.txt") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "Exit Code: 0") {
			t.Errorf("Expected exit code in output, got: %s", output)
		}
		if strings.Contains(output, "Output:") {
			t.Errorf("Should not show output section when no output, got: %s", output)
		}
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

		if !strings.Contains(output, "Command: cat script.sh") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "#!/bin/bash") {
			t.Errorf("Expected script shebang in output, got: %s", output)
		}
		if !strings.Contains(output, "for i in {1..3}") {
			t.Errorf("Expected loop in output, got: %s", output)
		}
		if !strings.Contains(output, "Script completed") {
			t.Errorf("Expected end of script in output, got: %s", output)
		}
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

		if !strings.Contains(output, "find /usr -name") {
			t.Errorf("Expected long command in output, got: %s", output)
		}
		if !strings.Contains(output, "Execution Time: 5s") {
			t.Errorf("Expected long execution time in output, got: %s", output)
		}
		if !strings.Contains(output, "libssl.so.1.1") {
			t.Errorf("Expected library output in output, got: %s", output)
		}
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

		if !strings.Contains(output, `grep -r "function.*("`) {
			t.Errorf("Expected command with special characters in output, got: %s", output)
		}
		if !strings.Contains(output, "parseData(input)") {
			t.Errorf("Expected function match in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Error: Command execution failed") {
			t.Errorf("Expected error message in output, got: %s", output)
		}
		if !strings.Contains(output, "nonexistent-command") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "Exit Code: 1") {
			t.Errorf("Expected exit code in output, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for bash") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for bash") {
			t.Errorf("Expected invalid metadata error for nil metadata, got: %s", output)
		}
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

		if !strings.Contains(output, "Command: pwd") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "Execution Time: 1µs") {
			t.Errorf("Expected microsecond execution time in output, got: %s", output)
		}
	})

	t.Run("Background command output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash_background",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BackgroundBashMetadata{
				Command:   "python -m http.server 8000",
				PID:       12345,
				LogPath:   "/tmp/.kodelet/12345/out.log",
				StartTime: time.Date(2023, 1, 1, 10, 30, 45, 0, time.UTC),
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Background Command: python -m http.server 8000") {
			t.Errorf("Expected background command in output, got: %s", output)
		}
		if !strings.Contains(output, "Process ID: 12345") {
			t.Errorf("Expected process ID in output, got: %s", output)
		}
		if !strings.Contains(output, "Log File: /tmp/.kodelet/12345/out.log") {
			t.Errorf("Expected log file path in output, got: %s", output)
		}
		if !strings.Contains(output, "Started: 2023-01-01 10:30:45") {
			t.Errorf("Expected start time in output, got: %s", output)
		}
		if !strings.Contains(output, "running in the background") {
			t.Errorf("Expected background process message in output, got: %s", output)
		}
		if !strings.Contains(output, "Check the log file for output") {
			t.Errorf("Expected log file instruction in output, got: %s", output)
		}
	})

	t.Run("Background command with error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash_background",
			Success:   false,
			Error:     "Failed to start background process",
			Timestamp: time.Now(),
			Metadata: &tools.BackgroundBashMetadata{
				Command:   "invalid-command",
				PID:       0,
				LogPath:   "",
				StartTime: time.Now(),
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Failed to start background process") {
			t.Errorf("Expected error message in output, got: %s", output)
		}
		if !strings.Contains(output, "Background Command: invalid-command") {
			t.Errorf("Expected background command in output, got: %s", output)
		}
		if !strings.Contains(output, "Process ID: 0") {
			t.Errorf("Expected process ID 0 in output, got: %s", output)
		}
	})

	t.Run("Background command with different timestamp format", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash_background",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BackgroundBashMetadata{
				Command:   "tail -f /var/log/syslog",
				PID:       99999,
				LogPath:   "/home/user/.kodelet/99999/out.log",
				StartTime: time.Date(2023, 12, 25, 23, 59, 59, 0, time.UTC),
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Started: 2023-12-25 23:59:59") {
			t.Errorf("Expected formatted start time in output, got: %s", output)
		}
		if !strings.Contains(output, "Process ID: 99999") {
			t.Errorf("Expected large process ID in output, got: %s", output)
		}
		if !strings.Contains(output, "/home/user/.kodelet/99999/out.log") {
			t.Errorf("Expected user-specific log path in output, got: %s", output)
		}
	})

	t.Run("Background command with complex command", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash_background",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BackgroundBashMetadata{
				Command:   `docker run -d -p 8080:80 --name web-server nginx:latest`,
				PID:       54321,
				LogPath:   "/tmp/.kodelet/54321/out.log",
				StartTime: time.Now(),
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Background Command: docker run -d -p 8080:80 --name web-server nginx:latest") {
			t.Errorf("Expected complex docker command in output, got: %s", output)
		}
		if !strings.Contains(output, "Process ID: 54321") {
			t.Errorf("Expected process ID in output, got: %s", output)
		}
	})

	t.Run("Invalid metadata type for background bash", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "bash_background",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for bash") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
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
		if !strings.Contains(output, "Command: echo 'Hello World'") {
			t.Errorf("Expected regular command format, got: %s", output)
		}
		if !strings.Contains(output, "Exit Code: 0") {
			t.Errorf("Expected exit code in output, got: %s", output)
		}
		if strings.Contains(output, "Background Command:") {
			t.Errorf("Should not show background format for regular bash, got: %s", output)
		}
		if strings.Contains(output, "Process ID:") {
			t.Errorf("Should not show process ID for regular bash, got: %s", output)
		}
	})
}
