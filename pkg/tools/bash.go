package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/pkg/errors"

	"github.com/gobwas/glob"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

var (
	// bashMaxOutputTokens to preserve context window usage
	bashMaxOutputTokens = 10_000

	// BannedCommands lists commands that are not allowed to run through the bash tool
	BannedCommands = []string{
		"vim",
		"view",
		"less",
		"more",
		"cd",
	}

	descriptionTemplate = `Run a bash command in a persistent shell session.

# Restrictions
{{if .AllowedCommands}}
Allowed command patterns:
{{range .AllowedCommands}}- {{.}}
{{end}}
Commands outside these patterns are rejected.
{{else}}
Banned commands:
{{range .BannedCommands}}- {{.}}
{{end}}
{{end}}

# Input
- command: required single-line bash command
- description: required, 5-10 words
- timeout: required, 10-120

# Rules
- Use parallel tool calling for independent commands.
- Do not run interactive commands.
- For multiple commands, use ';' or '&&' on one line.
- Avoid direct cd; use absolute paths or subshell: (cd /path && cmd).
{{if .DisableFSSearchTools}}- For filesystem search activities, use fd and rg via this tool only.
{{else}}- Prefer grep_tool/glob_tool over grep/find in bash.
{{end}}- Do not use heredoc; use file_write or apply_patch instead.

Examples:
- (cd /repo && mise run test)
`
)

const bashApproxBytesPerToken = 4

// BashTool executes bash commands with configurable restrictions and timeout support
type BashTool struct {
	allowedCommands      []string
	compiledGlobs        []glob.Glob
	disableFSSearchTools bool
}

// NewBashTool creates a new BashTool with the specified allowed commands
func NewBashTool(allowedCommands []string, disableFSSearchTools bool) *BashTool {
	globs := make([]glob.Glob, len(allowedCommands))
	for i, pattern := range allowedCommands {
		// Compile glob patterns without custom separators (default behavior)
		globs[i] = glob.MustCompile(pattern)
	}
	return &BashTool{
		allowedCommands:      allowedCommands,
		compiledGlobs:        globs,
		disableFSSearchTools: disableFSSearchTools,
	}
}

// MatchesCommand checks if a command matches any of the compiled glob patterns
func (b *BashTool) MatchesCommand(command string) bool {
	for _, c := range b.allowedCommands {
		if c != "" && strings.Contains(command, c) {
			return true
		}
	}

	for _, g := range b.compiledGlobs {
		if g.Match(command) {
			return true
		}
	}
	return false
}

// BashInput defines the input parameters for the bash tool
type BashInput struct {
	Description string `json:"description" jsonschema:"description=A description of the command to run"`
	Command     string `json:"command" jsonschema:"description=The bash command to run"`
	Timeout     int    `json:"timeout" jsonschema:"description=Timeout in seconds (10-120)"`
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (b *BashTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[BashInput]()
}

// Name returns the name of the tool
func (b *BashTool) Name() string {
	return "bash"
}

// TracingKVs returns tracing key-value pairs for observability
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
	}, nil
}

// ValidateInput validates the input parameters for the tool
func (b *BashTool) ValidateInput(_ tooltypes.State, parameters string) error {
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

	if input.Timeout < 10 || input.Timeout > 120 {
		return errors.New("timeout must be between 10 and 120 seconds")
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

		// DENY FIRST: Check if command is banned - if yes, deny it regardless of allowed commands
		if slices.Contains(BannedCommands, firstWord) {
			return errors.New("command is banned: " + firstWord)
		}

		// Check if allowed commands are configured
		if len(b.allowedCommands) > 0 {
			// If allowed commands are configured, only allow commands that match patterns
			if !b.MatchesCommand(command) {
				return errors.Errorf("command not in allowed list: %s", command)
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

// Description returns the description of the tool
func (b *BashTool) Description() string {
	tmpl, err := template.New("bash_description").Parse(descriptionTemplate)
	if err != nil {
		// Fallback to a simple description if template parsing fails
		return "Executes bash commands with configurable restrictions."
	}

	data := struct {
		AllowedCommands      []string
		BannedCommands       []string
		DisableFSSearchTools bool
	}{
		AllowedCommands:      b.allowedCommands,
		BannedCommands:       BannedCommands,
		DisableFSSearchTools: b.disableFSSearchTools,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		// Fallback to a simple description if template execution fails
		return "Executes bash commands with configurable restrictions."
	}

	return buf.String()
}

// BashToolResult represents the result of a bash command execution
type BashToolResult struct {
	command        string
	combinedOutput string
	error          string
	exitCode       int
	executionTime  time.Duration
	workingDir     string
}

// GetResult returns the command output
func (r *BashToolResult) GetResult() string {
	return r.combinedOutput
}

// GetError returns the error message
func (r *BashToolResult) GetError() string {
	return r.error
}

// IsError returns true if the result contains an error
func (r *BashToolResult) IsError() bool {
	return r.error != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *BashToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(truncateBashOutputForModel(r.combinedOutput), r.GetError())
}

// StructuredData returns structured metadata about the tool execution
func (r *BashToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "bash",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.BashMetadata{
		Command:       r.command,
		Output:        truncateBashOutputForModel(r.combinedOutput),
		ExitCode:      r.exitCode,
		ExecutionTime: r.executionTime,
		WorkingDir:    r.workingDir,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}

// Execute runs the bash command and returns the result
func (b *BashTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	_ = state
	input := &BashInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		workingDir := ""
		return &BashToolResult{
			command:    input.Command,
			workingDir: workingDir,
			error:      err.Error(),
		}
	}
	return b.executeForeground(ctx, input)
}

func (b *BashTool) executeForeground(ctx context.Context, input *BashInput) tooltypes.ToolResult {
	startTime := time.Now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()

	// Get current working directory
	workingDir, _ := os.Getwd()

	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)
	if env, err := bashEnvWithPreferredBinDirs(); err == nil {
		cmd.Env = env
	}
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	output, err := cmd.CombinedOutput()
	executionTime := time.Since(startTime)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &BashToolResult{
				command:       input.Command,
				executionTime: executionTime,
				workingDir:    workingDir,
				error:         "Command timed out after " + strconv.Itoa(input.Timeout) + " seconds",
			}
		}
		if status, ok := err.(*exec.ExitError); ok {
			return &BashToolResult{
				command:        input.Command,
				combinedOutput: string(output),
				exitCode:       status.ExitCode(),
				executionTime:  executionTime,
				workingDir:     workingDir,
				error:          fmt.Sprintf("Command exited with status %d", status.ExitCode()),
			}
		}
		return &BashToolResult{
			command:       input.Command,
			executionTime: executionTime,
			workingDir:    workingDir,
			error:         err.Error(),
		}
	}

	return &BashToolResult{
		command:        input.Command,
		combinedOutput: string(output),
		exitCode:       0, // Success
		executionTime:  executionTime,
		workingDir:     workingDir,
	}
}

func bashEnvWithPreferredBinDirs() ([]string, error) {
	return binaries.EnvWithPreferredBinDirs(os.Environ())
}

func truncateBashOutputForModel(content string) string {
	maxBytes := approxBytesForTokens(bashMaxOutputTokens)
	if len(content) <= maxBytes {
		return content
	}

	totalLines := countOutputLines(content)
	truncated := truncateMiddleWithTokenBudget(content, bashMaxOutputTokens)
	return fmt.Sprintf("Total output lines: %d\n\n%s", totalLines, truncated)
}

func countOutputLines(content string) int {
	if content == "" {
		return 0
	}

	lineCount := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lineCount++
	}

	return lineCount
}

func truncateMiddleWithTokenBudget(content string, maxTokens int) string {
	if content == "" {
		return ""
	}

	maxBytes := approxBytesForTokens(maxTokens)
	if maxTokens > 0 && len(content) <= maxBytes {
		return content
	}

	return truncateMiddleByBytesEstimate(content, maxBytes, true)
}

func approxBytesForTokens(tokens int) int {
	if tokens <= 0 {
		return 0
	}

	return tokens * bashApproxBytesPerToken
}

func approxTokensFromByteCount(bytes int) int {
	if bytes <= 0 {
		return 0
	}

	return (bytes + bashApproxBytesPerToken - 1) / bashApproxBytesPerToken
}

func truncateMiddleByBytesEstimate(content string, maxBytes int, useTokens bool) string {
	if content == "" {
		return ""
	}

	if maxBytes <= 0 {
		return formatBashTruncationMarker(useTokens, removedUnits(useTokens, len(content), utf8.RuneCountInString(content)))
	}

	if len(content) <= maxBytes {
		return content
	}

	leftBudget, rightBudget := splitBashBudget(maxBytes)
	prefixEnd, suffixStart, removedRunes := splitBashString(content, leftBudget, rightBudget)
	prefix := content[:prefixEnd]
	suffix := content[suffixStart:]
	removedBytes := len(content) - len(prefix) - len(suffix)
	marker := formatBashTruncationMarker(useTokens, removedUnits(useTokens, removedBytes, removedRunes))

	return prefix + marker + suffix
}

func splitBashBudget(budget int) (int, int) {
	left := budget / 2
	return left, budget - left
}

func splitBashString(content string, beginningBytes, endBytes int) (int, int, int) {
	contentLen := len(content)
	tailStartTarget := max(contentLen-endBytes, 0)
	prefixEnd := 0
	suffixStart := contentLen
	removedRunes := 0
	suffixStarted := false

	for idx, char := range content {
		charLen := utf8.RuneLen(char)
		if charLen < 0 {
			charLen = 0
		}
		charEnd := idx + charLen
		if charEnd <= beginningBytes {
			prefixEnd = charEnd
			continue
		}

		if idx >= tailStartTarget {
			if !suffixStarted {
				suffixStart = idx
				suffixStarted = true
			}
			continue
		}

		removedRunes++
	}

	if suffixStart < prefixEnd {
		suffixStart = prefixEnd
	}

	return prefixEnd, suffixStart, removedRunes
}

func removedUnits(useTokens bool, removedBytes, removedRunes int) int {
	if useTokens {
		return approxTokensFromByteCount(removedBytes)
	}

	return removedRunes
}

func formatBashTruncationMarker(useTokens bool, removedCount int) string {
	if useTokens {
		return fmt.Sprintf("…%d tokens truncated…", removedCount)
	}

	return fmt.Sprintf("…%d chars truncated…", removedCount)
}
