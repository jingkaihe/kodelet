# LogHandler Implementation Plan

## Overview
Implement a new LogHandler that emits structured log messages with different `kind` fields for different message types. This will be available through a `--headless` command line flag that switches from the default ConsoleMessageHandler to the new LogHandler.

## Current Architecture Analysis

### Handler Interface
The current `MessageHandler` interface in `pkg/types/llm/handler.go` defines:
- `HandleText(text string)`
- `HandleToolUse(toolName string, input string)`
- `HandleToolResult(toolName string, result string)`
- `HandleThinking(thinking string)`
- `HandleDone()`

### Existing Handlers
1. **ConsoleMessageHandler** - Human-readable console output with emojis
2. **ChannelMessageHandler** - Channel-based messages for TUI
3. **StringCollectorHandler** - Collects text responses into a string

### Integration Points
- Handler instantiation: `cmd/kodelet/run.go` line 195
- Command flags defined: `cmd/kodelet/run.go` lines 237-246
- Usage throughout codebase for various message processing scenarios

## Implementation Plan

### 1. Create LogHandler Structure

**File**: `pkg/types/llm/handler.go`

Add a new `LogHandler` struct that outputs structured JSON logs with the required `kind` field:

```go
// LogHandler emits structured log messages in JSON format
type LogHandler struct {
    Silent bool // For consistency with other handlers
}
```

The handler will emit logs in the following JSON format:
```json
{"kind": "text", "content": "message content", "timestamp": "2024-01-01T12:00:00Z"}
{"kind": "tool-use", "tool_name": "bash", "input": "ls -la", "timestamp": "2024-01-01T12:00:00Z"}
{"kind": "tool-result", "tool_name": "bash", "result": "file listing output", "timestamp": "2024-01-01T12:00:00Z"}
{"kind": "thinking", "content": "thinking content", "timestamp": "2024-01-01T12:00:00Z"}
{"kind": "log", "content": "Done", "timestamp": "2024-01-01T12:00:00Z"}
```

### 2. Implement LogHandler Methods

**File**: `pkg/types/llm/handler.go`

Implement the `MessageHandler` interface:

```go
func (h *LogHandler) HandleText(text string)
func (h *LogHandler) HandleToolUse(toolName string, input string)  
func (h *LogHandler) HandleToolResult(toolName string, result string)
func (h *LogHandler) HandleThinking(thinking string)
func (h *LogHandler) HandleDone()
```

Each method will:
- Create a structured log entry with appropriate `kind` and fields
- Include timestamp in ISO 8601 format  
- Output as JSON to stdout
- Use different field structures based on message type:
  - Text/thinking/log: `{"kind": "text", "content": "...", "timestamp": "..."}`
  - Tool use: `{"kind": "tool-use", "tool_name": "...", "input": "...", "timestamp": "..."}`
  - Tool result: `{"kind": "tool-result", "tool_name": "...", "result": "...", "timestamp": "..."}`
- Respect the `Silent` flag for consistency

### 3. Update RunConfig Structure

**File**: `cmd/kodelet/run.go`

Add `Headless` field to `RunConfig` struct:

```go
type RunConfig struct {
    // ... existing fields ...
    Headless bool // Use LogHandler instead of ConsoleMessageHandler
}
```

Update `NewRunConfig()` to initialize `Headless: false` by default.

### 4. Add Command Line Flag

**File**: `cmd/kodelet/run.go`

Add the `--headless` flag after line 246:

```go
runCmd.Flags().Bool("headless", defaults.Headless, "Use structured logging output instead of console formatting")
```

### 5. Update Handler Selection Logic

**File**: `cmd/kodelet/run.go`

Replace the hardcoded ConsoleMessageHandler instantiation (line 195) with conditional logic:

```go
var handler llmtypes.MessageHandler
if config.Headless {
    handler = &llmtypes.LogHandler{Silent: false}
} else {
    handler = &llmtypes.ConsoleMessageHandler{Silent: false}
}
```

### 6. Bind Command Flag to Config

**File**: `cmd/kodelet/run.go`

Add flag binding in the `RunE` function where other flags are processed:

```go
headless, _ := cmd.Flags().GetBool("headless")
config.Headless = headless
```

### 7. Add Tests

**File**: `pkg/types/llm/handler_test.go`

Add comprehensive tests for LogHandler:

```go
func TestLogHandler_HandleText(t *testing.T)
func TestLogHandler_HandleToolUse(t *testing.T)
func TestLogHandler_HandleToolResult(t *testing.T)
func TestLogHandler_HandleThinking(t *testing.T)
func TestLogHandler_HandleDone(t *testing.T)
func TestLogHandler_JSONFormat(t *testing.T)
func TestLogHandler_SilentMode(t *testing.T)
```

Tests should verify:
- Proper JSON structure
- Correct `kind` field values
- Timestamp format validation
- Silent mode functionality
- Proper handling of newlines and special characters

### 8. Update Documentation

**Files**: `AGENTS.md`, `docs/MANUAL.md`

Add documentation for:
- `--headless` flag usage
- LogHandler output format
- Use cases for structured logging
- Integration with log aggregation systems

### 9. Consider Additional Integration Points

**Files**: Various tool implementations

Review other locations where handlers are used to ensure consistency:
- `pkg/tools/subagent.go` - May benefit from headless mode
- `pkg/tools/web_fetch.go` - Consider structured output option
- `pkg/tools/image_recognition.go` - Consider structured output option

## Output Format Specification

The LogHandler will emit newline-delimited JSON (NDJSON) with the following structure:

```typescript
interface BaseLogEntry {
  kind: 'text' | 'tool-use' | 'tool-result' | 'thinking' | 'log';
  timestamp: string; // ISO 8601 format
}

interface TextLogEntry extends BaseLogEntry {
  kind: 'text' | 'thinking' | 'log';
  content: string;
}

interface ToolUseLogEntry extends BaseLogEntry {
  kind: 'tool-use';
  tool_name: string;
  input: string;
}

interface ToolResultLogEntry extends BaseLogEntry {
  kind: 'tool-result';
  tool_name: string;
  result: string;
}

type LogEntry = TextLogEntry | ToolUseLogEntry | ToolResultLogEntry;
```

### Kind Mapping and Field Structure
- `text` - Regular LLM text output (HandleText)
  - Fields: `kind`, `content`, `timestamp`
- `tool-use` - Tool invocation (HandleToolUse)  
  - Fields: `kind`, `tool_name`, `input`, `timestamp`
- `tool-result` - Tool execution result (HandleToolResult)
  - Fields: `kind`, `tool_name`, `result`, `timestamp`
- `thinking` - LLM thinking/reasoning (HandleThinking)
  - Fields: `kind`, `content`, `timestamp`
- `log` - System messages like "Done" (HandleDone)
  - Fields: `kind`, `content`, `timestamp`

### Benefits of Structured Fields
Having separate fields for `tool_name`, `input`, and `result` provides several advantages:
- **Easy filtering** - Query logs by specific tool types (`tool_name="bash"`)
- **Better analytics** - Analyze tool usage patterns and performance
- **Structured parsing** - No need to parse combined strings
- **Database integration** - Direct mapping to database columns
- **Log aggregation** - Better support for tools like Elasticsearch, Splunk, etc.

## Backward Compatibility

- Default behavior unchanged (ConsoleMessageHandler remains default)
- All existing command line flags continue to work
- No breaking changes to API or interface
- Silent mode maintained for consistency with other handlers

## Testing Strategy

1. **Unit Tests** - Test each handler method individually
2. **Integration Tests** - Test flag parsing and handler selection
3. **Format Tests** - Validate JSON structure and content
4. **End-to-End Tests** - Test complete workflow with `--headless` flag

## Future Considerations

- **Log Levels** - Consider adding log level support (debug, info, warn, error)
- **Structured Data** - Consider adding metadata fields (conversation ID, turn number)
- **Output Destinations** - Consider adding file output option instead of just stdout
- **Filtering** - Consider allowing filtering of specific message kinds
- **Batching** - Consider batching multiple log entries for high-throughput scenarios

## Implementation Order

1. Create LogHandler struct and methods
2. Add command line flag and config field
3. Update handler selection logic
4. Write comprehensive tests
5. Update documentation
6. Test end-to-end functionality
7. Consider additional integration points

## Success Criteria

- `kodelet run --headless "query"` produces structured JSON logs with separate fields for tool operations:
  ```
  {"kind": "tool-use", "tool_name": "bash", "input": "pwd", "timestamp": "2024-01-01T12:00:00Z"}
  {"kind": "tool-result", "tool_name": "bash", "result": "/home/user", "timestamp": "2024-01-01T12:00:01Z"}
  {"kind": "text", "content": "You are currently in /home/user", "timestamp": "2024-01-01T12:00:02Z"}
  ```
- All existing functionality works unchanged when `--headless` is not used
- JSON output is valid and parseable with proper field separation for tools
- Tests achieve >90% coverage for new code
- Documentation clearly explains the feature and use cases