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
	"sync"
	"sync/atomic"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/pkg/errors"

	"github.com/gobwas/glob"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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
- timeout: required, {{.MinTimeoutSeconds}}-{{.MaxTimeoutSeconds}}

# Rules
- Use parallel tool calling for independent commands.
- Do not run interactive commands.
- For multiple commands, use ';' or '&&' on one line.
- Avoid direct cd; use absolute paths or subshell: (cd /path && cmd).
{{if .EnableFSSearchTools}}- Prefer grep_tool/glob_tool over grep/find in bash.
{{else}}- For filesystem search activities, use fd and rg via this tool only.
{{end}}- Do not use heredoc; use file_write or apply_patch instead.

Examples:
- (cd /repo && mise run test)
`
)

const (
	bashApproxBytesPerToken = 4
	bashMinTimeoutSeconds   = int(llmtypes.MinBashTimeout / time.Second)
	bashToolUpdateInterval  = 100 * time.Millisecond
)

// BashTool executes bash commands with configurable restrictions and timeout support
type BashTool struct {
	allowedCommands     []string
	compiledGlobs       []glob.Glob
	enableFSSearchTools bool
	maxTimeout          time.Duration
}

var _ tooltypes.StreamingTool = (*BashTool)(nil)

// NewBashTool creates a new BashTool with the specified allowed commands
func NewBashTool(allowedCommands []string, enableFSSearchTools bool) *BashTool {
	return NewBashToolWithTimeout(allowedCommands, enableFSSearchTools, llmtypes.DefaultBashTimeout)
}

// NewBashToolWithTimeout creates a BashTool with the specified maximum timeout.
func NewBashToolWithTimeout(allowedCommands []string, enableFSSearchTools bool, maxTimeout time.Duration) *BashTool {
	globs := make([]glob.Glob, len(allowedCommands))
	for i, pattern := range allowedCommands {
		// Compile glob patterns without custom separators (default behavior)
		globs[i] = glob.MustCompile(pattern)
	}
	if maxTimeout == 0 {
		maxTimeout = llmtypes.DefaultBashTimeout
	}
	return &BashTool{
		allowedCommands:     allowedCommands,
		compiledGlobs:       globs,
		enableFSSearchTools: enableFSSearchTools,
		maxTimeout:          maxTimeout,
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

// BashInput reuses the shared bash tool input schema while preserving pkg/tools schema IDs.
type BashInput tooltypes.BashInput

// GenerateSchema generates the JSON schema for the tool's input parameters
func (b *BashTool) GenerateSchema() *jsonschema.Schema {
	schema := GenerateSchema[BashInput]()
	if schema.Properties != nil {
		if timeoutSchema, ok := schema.Properties.Get("timeout"); ok {
			timeoutSchema.Description = fmt.Sprintf("Timeout in seconds (%d-%d)", bashMinTimeoutSeconds, b.maxTimeoutSeconds())
			timeoutSchema.Minimum = json.Number(strconv.Itoa(bashMinTimeoutSeconds))
			timeoutSchema.Maximum = json.Number(strconv.Itoa(b.maxTimeoutSeconds()))
		}
	}
	return schema
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

	if input.Timeout < bashMinTimeoutSeconds || input.Timeout > b.maxTimeoutSeconds() {
		return errors.Errorf("timeout must be between %d and %d seconds", bashMinTimeoutSeconds, b.maxTimeoutSeconds())
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
		AllowedCommands     []string
		BannedCommands      []string
		EnableFSSearchTools bool
		MinTimeoutSeconds   int
		MaxTimeoutSeconds   int
	}{
		AllowedCommands:     b.allowedCommands,
		BannedCommands:      BannedCommands,
		EnableFSSearchTools: b.enableFSSearchTools,
		MinTimeoutSeconds:   bashMinTimeoutSeconds,
		MaxTimeoutSeconds:   b.maxTimeoutSeconds(),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		// Fallback to a simple description if template execution fails
		return "Executes bash commands with configurable restrictions."
	}

	return buf.String()
}

func (b *BashTool) maxTimeoutSeconds() int {
	maxTimeout := b.maxTimeout
	if maxTimeout == 0 {
		maxTimeout = llmtypes.DefaultBashTimeout
	}
	seconds := int(maxTimeout.Seconds())
	if seconds < bashMinTimeoutSeconds {
		return bashMinTimeoutSeconds
	}
	return seconds
}

// BashToolResult represents the result of a bash command execution
type BashToolResult struct {
	command            string
	combinedOutput     string
	error              string
	exitCode           int
	executionTime      time.Duration
	workingDir         string
	outputTruncated    bool
	outputTotalLines   int
	outputTotalBytes   int64
	fullOutputPath     string
	fullOutputComplete bool
}

// GetResult returns the command output
func (r *BashToolResult) GetResult() string {
	if r.outputTruncated && r.fullOutputComplete && r.fullOutputPath != "" {
		if output, err := os.ReadFile(r.fullOutputPath); err == nil {
			return string(output)
		}
	}
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

func (r *BashToolResult) outputForModel() string {
	if r.outputTruncated {
		return r.combinedOutput
	}
	return truncateBashOutputForModel(r.combinedOutput)
}

// AssistantFacing returns the string representation for the AI assistant
func (r *BashToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.outputForModel(), r.GetError())
}

// StructuredData returns structured metadata about the tool execution
func (r *BashToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "bash",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Always populate metadata, even for errors
	metadata := &tooltypes.BashMetadata{
		Command:       r.command,
		Output:        r.outputForModel(),
		ExitCode:      r.exitCode,
		ExecutionTime: r.executionTime,
		WorkingDir:    r.workingDir,
	}
	if r.outputTruncated {
		metadata.Truncation = &tooltypes.BashOutputTruncation{
			Truncated:  true,
			TotalLines: r.outputTotalLines,
			TotalBytes: r.outputTotalBytes,
			MaxBytes:   approxBytesForTokens(bashMaxOutputTokens),
		}
		if r.fullOutputComplete {
			metadata.FullOutputPath = r.fullOutputPath
		}
	}
	result.Metadata = metadata

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}

// Execute runs the bash command and returns the result
func (b *BashTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	return b.execute(ctx, state, parameters, nil)
}

// ExecuteStreaming runs the bash command and emits accumulated output snapshots
// while stdout and stderr are still being produced.
func (b *BashTool) ExecuteStreaming(
	ctx context.Context,
	state tooltypes.State,
	parameters string,
	onUpdate tooltypes.ToolUpdateCallback,
) tooltypes.ToolResult {
	return b.execute(ctx, state, parameters, onUpdate)
}

func (b *BashTool) execute(
	ctx context.Context,
	state tooltypes.State,
	parameters string,
	onUpdate tooltypes.ToolUpdateCallback,
) tooltypes.ToolResult {
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
	return b.executeForeground(ctx, input, state.WorkingDirectory(), onUpdate)
}

func (b *BashTool) executeForeground(
	ctx context.Context,
	input *BashInput,
	cwd string,
	onUpdate tooltypes.ToolUpdateCallback,
) tooltypes.ToolResult {
	startTime := time.Now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()

	workingDir := cwd
	if strings.TrimSpace(workingDir) == "" {
		workingDir, _ = os.Getwd()
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)
	cmd.Dir = workingDir
	if env, err := bashEnvWithPreferredBinDirs(); err == nil {
		cmd.Env = env
	}
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	output := newBashOutputAccumulator(approxBytesForTokens(bashMaxOutputTokens))
	var completedExecutionTime atomic.Int64
	currentResult := func() tooltypes.ToolResult {
		executionTime := time.Since(startTime)
		if completed := completedExecutionTime.Load(); completed > 0 {
			executionTime = time.Duration(completed)
		}
		return newBashToolResult(input.Command, workingDir, executionTime, output.snapshot(), false)
	}

	var emitter *bashUpdateEmitter
	if onUpdate != nil {
		emitter = newBashUpdateEmitter(onUpdate, currentResult, currentResult())
		output.onWrite = emitter.markDirty
	}

	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Start()
	if err == nil {
		err = cmd.Wait()
	}
	timedOut := ctx.Err() == context.DeadlineExceeded
	executionTime := time.Since(startTime)
	completedExecutionTime.Store(int64(executionTime))
	finalSnapshot := output.finish()
	if emitter != nil {
		emitter.stopAndFlush()
	}
	result := newBashToolResult(input.Command, workingDir, executionTime, finalSnapshot, true)

	if err != nil {
		if timedOut {
			result.error = "Command timed out after " + strconv.Itoa(input.Timeout) + " seconds"
			return result
		}
		if status, ok := err.(*exec.ExitError); ok {
			result.exitCode = status.ExitCode()
			result.error = fmt.Sprintf("Command exited with status %d", status.ExitCode())
			return result
		}
		result.error = err.Error()
		return result
	}

	return result
}

type bashOutputSnapshot struct {
	output         string
	truncated      bool
	totalLines     int
	totalBytes     int64
	fullOutputPath string
}

type bashOutputAccumulator struct {
	mu             sync.Mutex
	maxBytes       int
	prefixLimit    int
	tailLimit      int
	prefix         []byte
	tail           []byte
	full           []byte
	totalBytes     int64
	newlineCount   int
	lastByte       byte
	hasOutput      bool
	spillAttempted bool
	spillFile      *os.File
	fullOutputPath string
	closed         bool
	onWrite        func()
}

func newBashOutputAccumulator(maxBytes int) *bashOutputAccumulator {
	prefixLimit, tailLimit := splitBashBudget(maxBytes)
	return &bashOutputAccumulator{
		maxBytes:    maxBytes,
		prefixLimit: prefixLimit,
		tailLimit:   tailLimit,
		full:        make([]byte, 0, max(maxBytes, 0)),
	}
}

func (a *bashOutputAccumulator) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return len(p), nil
	}
	a.captureLocked(p)
	onWrite := a.onWrite
	a.mu.Unlock()

	if onWrite != nil {
		onWrite()
	}
	return len(p), nil
}

func (a *bashOutputAccumulator) captureLocked(p []byte) {
	previousOutput := a.full
	a.totalBytes += int64(len(p))
	a.newlineCount += bytes.Count(p, []byte{'\n'})
	a.lastByte = p[len(p)-1]
	a.hasOutput = true

	if remaining := a.prefixLimit - len(a.prefix); remaining > 0 {
		a.prefix = append(a.prefix, p[:min(remaining, len(p))]...)
	}
	a.appendTailLocked(p)

	if a.totalBytes <= int64(a.maxBytes) {
		a.full = append(a.full, p...)
		return
	}

	a.full = nil
	if !a.spillAttempted {
		a.startSpillLocked(previousOutput, p)
		return
	}
	if a.spillFile != nil {
		if _, err := a.spillFile.Write(p); err != nil {
			a.discardSpillLocked()
		}
	}
}

func (a *bashOutputAccumulator) appendTailLocked(p []byte) {
	if a.tailLimit <= 0 {
		return
	}
	if len(p) >= a.tailLimit {
		a.tail = append(a.tail[:0], p[len(p)-a.tailLimit:]...)
		return
	}
	if overflow := len(a.tail) + len(p) - a.tailLimit; overflow > 0 {
		copy(a.tail, a.tail[overflow:])
		a.tail = a.tail[:len(a.tail)-overflow]
	}
	a.tail = append(a.tail, p...)
}

func (a *bashOutputAccumulator) startSpillLocked(previousOutput, p []byte) {
	a.spillAttempted = true
	spillFile, err := os.CreateTemp("", "kodelet-bash-output-*.log")
	if err != nil {
		return
	}
	if _, err := spillFile.Write(previousOutput); err != nil {
		_ = spillFile.Close()
		_ = os.Remove(spillFile.Name())
		return
	}
	if _, err := spillFile.Write(p); err != nil {
		_ = spillFile.Close()
		_ = os.Remove(spillFile.Name())
		return
	}
	a.spillFile = spillFile
	a.fullOutputPath = spillFile.Name()
}

func (a *bashOutputAccumulator) discardSpillLocked() {
	if a.spillFile != nil {
		_ = a.spillFile.Close()
		_ = os.Remove(a.spillFile.Name())
	}
	a.spillFile = nil
	a.fullOutputPath = ""
}

func (a *bashOutputAccumulator) snapshot() bashOutputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked()
}

func (a *bashOutputAccumulator) finish() bashOutputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.spillFile != nil {
		if err := a.spillFile.Close(); err != nil {
			a.discardSpillLocked()
		} else {
			a.spillFile = nil
		}
	}
	a.closed = true
	return a.snapshotLocked()
}

func (a *bashOutputAccumulator) snapshotLocked() bashOutputSnapshot {
	totalLines := a.newlineCount
	if a.hasOutput && a.lastByte != '\n' {
		totalLines++
	}

	snapshot := bashOutputSnapshot{
		totalLines:     totalLines,
		totalBytes:     a.totalBytes,
		fullOutputPath: a.fullOutputPath,
	}
	if a.totalBytes <= int64(a.maxBytes) {
		content := a.full
		if !a.closed {
			content = trimIncompleteUTF8Suffix(content)
		}
		snapshot.output = string(content)
		return snapshot
	}

	snapshot.truncated = true
	prefixBytes := trimIncompleteUTF8Suffix(a.prefix)
	tailBytes := a.tail
	if !a.closed {
		tailBytes = trimIncompleteUTF8Suffix(tailBytes)
	}
	tailBytes = trimUTF8ContinuationPrefix(tailBytes)
	removedBytes := max(a.totalBytes-int64(len(prefixBytes))-int64(len(tailBytes)), 0)
	marker := formatBashTruncationMarker(true, approxTokensFromByteCount(int(removedBytes)))
	prefix := strings.ToValidUTF8(string(prefixBytes), "\uFFFD")
	tail := strings.ToValidUTF8(string(tailBytes), "\uFFFD")
	snapshot.output = fmt.Sprintf("Total output lines: %d\n\n%s%s%s", totalLines, prefix, marker, tail)
	return snapshot
}

func trimIncompleteUTF8Suffix(content []byte) []byte {
	if len(content) == 0 {
		return content
	}
	start := max(len(content)-utf8.UTFMax, 0)
	for i := len(content) - 1; i >= start; i-- {
		if !utf8.RuneStart(content[i]) {
			continue
		}
		if !utf8.FullRune(content[i:]) {
			return content[:i]
		}
		break
	}
	return content
}

func trimUTF8ContinuationPrefix(content []byte) []byte {
	for len(content) > 0 && !utf8.RuneStart(content[0]) {
		content = content[1:]
	}
	return content
}

func newBashToolResult(command, workingDir string, executionTime time.Duration, snapshot bashOutputSnapshot, fullOutputComplete bool) *BashToolResult {
	return &BashToolResult{
		command:            command,
		combinedOutput:     snapshot.output,
		executionTime:      executionTime,
		workingDir:         workingDir,
		outputTruncated:    snapshot.truncated,
		outputTotalLines:   snapshot.totalLines,
		outputTotalBytes:   snapshot.totalBytes,
		fullOutputPath:     snapshot.fullOutputPath,
		fullOutputComplete: fullOutputComplete,
	}
}

type bashUpdateEmitter struct {
	onUpdate tooltypes.ToolUpdateCallback
	snapshot func() tooltypes.ToolResult
	dirty    chan struct{}
	stop     chan struct{}
	done     chan struct{}
}

func newBashUpdateEmitter(onUpdate tooltypes.ToolUpdateCallback, snapshot func() tooltypes.ToolResult, initial tooltypes.ToolResult) *bashUpdateEmitter {
	emitter := &bashUpdateEmitter{
		onUpdate: onUpdate,
		snapshot: snapshot,
		dirty:    make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go emitter.run(initial)
	return emitter
}

func (e *bashUpdateEmitter) run(initial tooltypes.ToolResult) {
	defer close(e.done)
	if initial != nil {
		e.onUpdate(initial)
	}

	var ticker *time.Ticker
	var tickerC <-chan time.Time
	dirty := false
	emit := func() {
		if !dirty {
			return
		}
		dirty = false
		e.onUpdate(e.snapshot())
	}
	drainDirty := func() {
		select {
		case <-e.dirty:
			dirty = true
		default:
		}
	}

	for {
		select {
		case <-e.dirty:
			dirty = true
			if ticker == nil {
				emit()
				ticker = time.NewTicker(bashToolUpdateInterval)
				tickerC = ticker.C
			}
		case <-tickerC:
			drainDirty()
			emit()
		case <-e.stop:
			if ticker != nil {
				ticker.Stop()
			}
			drainDirty()
			emit()
			return
		}
	}
}

func (e *bashUpdateEmitter) markDirty() {
	select {
	case e.dirty <- struct{}{}:
	default:
	}
}

func (e *bashUpdateEmitter) stopAndFlush() {
	close(e.stop)
	<-e.done
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
