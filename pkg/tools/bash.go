package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/glob"
	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

var (
	BannedCommands = []string{
		"vim",
		"view",
		"less",
		"more",
		"cd",
	}
)

type BashTool struct {
	allowedCommands []string
	compiledGlobs   []glob.Glob
}

func NewBashTool(allowedCommands []string) *BashTool {
	globs := make([]glob.Glob, len(allowedCommands))
	for i, pattern := range allowedCommands {
		// Compile glob patterns without custom separators (default behavior)
		globs[i] = glob.MustCompile(pattern)
	}
	return &BashTool{
		allowedCommands: allowedCommands,
		compiledGlobs:   globs,
	}
}

// MatchesCommand checks if a command matches any of the compiled glob patterns
func (b *BashTool) MatchesCommand(command string) bool {
	for _, g := range b.compiledGlobs {
		if g.Match(command) {
			return true
		}
	}
	return false
}

type BashInput struct {
	Description string `json:"description" jsonschema:"description=A description of the command to run"`
	Command     string `json:"command" jsonschema:"description=The bash command to run"`
	Timeout     int    `json:"timeout" jsonschema:"description=The timeout for the command in seconds,default=10"`
	Background  bool   `json:"background" jsonschema:"description=Whether to run the command in the background,default=false"`
}

func (b *BashTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[BashInput]()
}

func (b *BashTool) Name() string {
	return "bash"
}

func (b *BashTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &BashInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("command", input.Command),
		attribute.String("description", input.Description),
		attribute.Int("timeout", input.Timeout),
		attribute.Bool("background", input.Background),
	}, nil
}

func (b *BashTool) ValidateInput(state tooltypes.State, parameters string) error {
	input := &BashInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.Command == "" {
		return errors.New("command is required")
	}

	if input.Description == "" {
		return errors.New("description is required")
	}

	// For background processes, timeout must be 0 (no timeout)
	if input.Background {
		if input.Timeout != 0 {
			return errors.New("background processes must have timeout=0 (no timeout)")
		}
	} else {
		if input.Timeout < 10 || input.Timeout > 120 {
			return errors.New("timeout must be between 10 and 120 seconds")
		}
	}

	validateCommand := func(command string) error {
		command = strings.TrimSpace(command)
		if command == "" {
			return nil
		}

		splitted := strings.Split(command, " ")
		if len(splitted) == 0 {
			return errors.New("command must contain at least one word")
		}

		firstWord := splitted[0]
		
		// Check if allowed commands are configured
		if len(b.allowedCommands) > 0 {
			// If allowed commands are configured, only allow commands that match patterns
			if !b.MatchesCommand(command) {
				return fmt.Errorf("command not in allowed list: %s", command)
			}
		} else {
			// If no allowed commands configured, fall back to banned commands check
			if slices.Contains(BannedCommands, firstWord) {
				return errors.New("command is banned: " + firstWord)
			}
		}

		return nil
	}

	// Split by all operators and validate each command
	operators := []string{"&&", "||", ";"}
	commands := []string{input.Command}

	for _, op := range operators {
		var newCommands []string
		for _, cmd := range commands {
			newCommands = append(newCommands, strings.Split(cmd, op)...)
		}
		commands = newCommands
	}

	for _, command := range commands {
		if err := validateCommand(command); err != nil {
			return err
		}
	}

	return nil
}

func (b *BashTool) Description() string {
	return `Executes a given bash command in a persistent shell session with timeout.

Before executing the command, please follow these steps:

# Important
* The command argument is required.
* You must specify a timeout from 10 to 120 seconds (or 0 for no timeout when background=true).
* You **MUST** use batch tool to wrap multiple independent commands together.
* Please provide a clear and concise description of what this command does in 5-10 words.
* If the output exceeds 30000 characters, output will be truncated before being returned to you.
* You **MUST NOT** run commands that require user interaction.
* When issuing multiple commands, use the ';' or '&&' operator to separate them. Command MUST NOT be multiline.
* Try to maintain your current working directory throughout the session by using absolute paths and avoid using cd directly. If you need to use cd please wrap it in parentheses.
* grep_tool and glob_tool are prefered over running grep, egrep and find using the bash tool.
* DO NOT use heredoc. For any command that requires heredoc, use the "file_write" tool instead.

# Background Parameter
* Set background=true to run commands in the background (default is false).
* Background processes are best suited for:
  - Running long-running processes (web servers, database servers, etc.)
  - Running tests or commands that will take a long time
  - Any process you want to continue running while doing other work
* When background=true:
  - The timeout must be 0 (no timeout)
  - Process output is written to .kodelet/{PID}/out.log
  - The tool returns immediately with the PID and log file location
  - The process continues running after the tool returns

# Examples
<good-example>
pytest /foo/bar/tests
</good-example>

<bad-example>
cd /foo/bar && pytest tests
<reasoning>
Using cd directly changes the current working directory.
</reasoning>
</bad-example>

<good-example>
(cd /foo/bar && pytest tests)
<reasoning>
cd command is wrapped in parentheses thus avoid changing the current working directory.
</reasoning>
</good-example>

<good-example>
apt-get install -y python3-pytest
</good-example>

<bad-example>
apt-get install python3-pytest
<reasoning>
The command requires user interaction.
</reasoning>
</bad-example>

<bad-example>
tail -f /var/log/nginx/access.log
<reasoning>
The command is running in interactive mode.
</reasoning>
</bad-example>

<bad-example>
vim /foo/bar/tests.py
<reasoning>
The command is running in interactive mode.
</reasoning>
</bad-example>

<good-example>
echo a; echo b
</good-example>

<bad-example>
echo a
echo b
<reasoning>
The command is multiline.
</reasoning>
</bad-example>

<bad-example>
cat <<EOF > /foo/bar/tests.py
import pytest

def test_foo():
    assert 1 == 1
EOF
<reasoning>
The command is using heredoc.
</reasoning>
</bad-example>

<good-example>
command: python -m http.server 8000
background: true
timeout: 0
<reasoning>
Running a web server in the background with no timeout.
</reasoning>
</good-example>

<good-example>
command: gunicorn app:application --bind 0.0.0.0:5000
background: true
timeout: 0
<reasoning>
Running a gunicorn server in the background.
</reasoning>
</good-example>
`
}

type BashToolResult struct {
	command        string
	combinedOutput string
	error          string
}

func (r *BashToolResult) GetResult() string {
	return r.combinedOutput
}

func (r *BashToolResult) GetError() string {
	return r.error
}

func (r *BashToolResult) IsError() bool {
	return r.error != ""
}

func (r *BashToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.combinedOutput, r.GetError())
}

func (r *BashToolResult) UserFacing() string {
	buf := bytes.NewBufferString(fmt.Sprintf("Command: %s\n", r.command))

	output := r.combinedOutput
	if output == "" {
		buf.WriteString("(no output)")
	} else {
		buf.WriteString(output)
	}

	if r.IsError() {
		buf.WriteString("\nError: " + r.GetError())
	}

	return buf.String()
}

type BackgroundBashToolResult struct {
	command string
	pid     int
	logPath string
	error   string
}

func (r *BackgroundBashToolResult) GetResult() string {
	return fmt.Sprintf("Process is up and running, output of the process can be viewed at %s", r.logPath)
}

func (r *BackgroundBashToolResult) GetError() string {
	return r.error
}

func (r *BackgroundBashToolResult) IsError() bool {
	return r.error != ""
}

func (r *BackgroundBashToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.GetResult(), r.GetError())
}

func (r *BackgroundBashToolResult) UserFacing() string {
	buf := bytes.NewBufferString(fmt.Sprintf("Command: %s\n", r.command))
	buf.WriteString(fmt.Sprintf("PID: %d\n", r.pid))
	buf.WriteString(fmt.Sprintf("Log file: %s\n", r.logPath))

	if r.IsError() {
		buf.WriteString("Error: " + r.GetError())
	} else {
		buf.WriteString("Process started in background successfully")
	}

	return buf.String()
}

func (b *BashTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &BashInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &BashToolResult{
			command: input.Command,
			error:   err.Error(),
		}
	}

	if input.Background {
		return b.executeBackground(ctx, state, input)
	} else {
		return b.executeForeground(ctx, input)
	}
}

func (b *BashTool) executeForeground(ctx context.Context, input *BashInput) tooltypes.ToolResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &BashToolResult{
				command: input.Command,
				error:   "Command timed out after " + strconv.Itoa(input.Timeout) + " seconds",
			}
		}
		if status, ok := err.(*exec.ExitError); ok {
			return &BashToolResult{
				command:        input.Command,
				combinedOutput: string(output),
				error:          fmt.Sprintf("Command exited with status %d", status.ExitCode()),
			}
		}
		return &BashToolResult{
			command: input.Command,
			error:   err.Error(),
		}
	}

	return &BashToolResult{
		command:        input.Command,
		combinedOutput: string(output),
	}
}

func (b *BashTool) executeBackground(ctx context.Context, state tooltypes.State, input *BashInput) tooltypes.ToolResult {
	// Create kodelet directory if it doesn't exist
	pwd, err := os.Getwd()
	if err != nil {
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to get current directory: %v", err),
		}
	}

	kodeletDir := filepath.Join(pwd, ".kodelet")
	if err := os.MkdirAll(kodeletDir, 0755); err != nil {
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to create kodelet directory: %v", err),
		}
	}

	// Create the command - no timeout for background processes
	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)

	// Setup stdout and stderr pipes before starting
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to create stdout pipe: %v", err),
		}
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to create stderr pipe: %v", err),
		}
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to start command: %v", err),
		}
	}

	pid := cmd.Process.Pid

	// Create PID directory
	pidDir := filepath.Join(kodeletDir, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		cmd.Process.Kill()
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to create PID directory: %v", err),
		}
	}

	// Create log file
	logPath := filepath.Join(pidDir, "out.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		cmd.Process.Kill()
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to create log file: %v", err),
		}
	}

	// Add to state tracking
	backgroundProcess := tooltypes.BackgroundProcess{
		PID:       pid,
		Command:   input.Command,
		LogPath:   logPath,
		StartTime: time.Now(),
		Process:   cmd.Process,
	}

	if err := state.AddBackgroundProcess(backgroundProcess); err != nil {
		logFile.Close()
		cmd.Process.Kill()
		return &BackgroundBashToolResult{
			command: input.Command,
			error:   fmt.Sprintf("Failed to track background process: %v", err),
		}
	}

	// Start a goroutine to capture output and wait for completion
	go func() {
		defer logFile.Close()
		defer state.RemoveBackgroundProcess(pid)

		// Use WaitGroup to ensure we capture all output before process exits
		var wg sync.WaitGroup
		wg.Add(2)

		// Create a flushing writer for the log file
		flushingWriter := &flushingWriter{
			writer: bufio.NewWriter(logFile),
			file:   logFile,
		}

		// Copy stdout
		go func() {
			defer wg.Done()
			io.Copy(flushingWriter, stdout)
		}()

		// Copy stderr
		go func() {
			defer wg.Done()
			io.Copy(flushingWriter, stderr)
		}()

		// Wait for all output to be copied
		wg.Wait()

		// Wait for the process to complete and capture exit status
		if err := cmd.Wait(); err != nil {
			flushingWriter.Write([]byte(fmt.Sprintf("Process exited with error: %v\n", err)))
		}
	}()

	return &BackgroundBashToolResult{
		command: input.Command,
		pid:     pid,
		logPath: logPath,
	}
}

// flushingWriter is a wrapper that flushes after each write
type flushingWriter struct {
	writer *bufio.Writer
	file   *os.File
}

func (fw *flushingWriter) Write(p []byte) (n int, err error) {
	n, err = fw.writer.Write(p)
	if err != nil {
		return n, err
	}
	fw.writer.Flush()
	fw.file.Sync()
	return n, nil
}
