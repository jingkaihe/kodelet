# Unified Conversation Streaming Implementation Plan

## Overview
Implement unified conversation streaming that works for both:
1. **Live streaming** during execution with `kodelet run --headless`
2. **Historical streaming** of stored conversations with `kodelet conversation stream`

Both use the same underlying streaming logic and output identical structured JSON format.

## Architecture Advantages

### Why Unified Streaming is Better
- **Single Source of Truth**: One implementation for structured output
- **Consistent Format**: Identical JSON output for live and historical data
- **Cleaner Architecture**: No duplicate LogHandler code
- **Reusable Logic**: Same streaming implementation serves both use cases
- **KISS Principle**: Single flag (`--headless`), jq handles all filtering

### Current Conversation Architecture
- **Persistence**: Conversations store `RawMessages` (raw LLM provider messages), `ToolResults` (structured tool results), usage stats, and metadata
- **Existing Parsers**: `anthropic.ExtractMessages()` and `openai.ExtractMessages()` already parse provider-specific formats
- **Storage**: Complete conversation history available immediately after execution

## Implementation Plan

### 1. Create Shared Streaming Infrastructure

**File**: `pkg/conversations/streamer.go`

Create the core streaming logic that both live and historical streaming will use:

```go
// ConversationStreamer handles streaming conversation data in structured JSON format
type ConversationStreamer struct {
    service *ConversationService
}

// StreamEntry represents a single stream entry
type StreamEntry struct {
    Kind      string     `json:"kind"`
    Content   *string    `json:"content,omitempty"`
    ToolName  *string    `json:"tool_name,omitempty"`
    Input     *string    `json:"input,omitempty"`
    Result    *string    `json:"result,omitempty"`
    Role      string     `json:"role,omitempty"` // "user", "assistant"
}

// NewConversationStreamer creates a new conversation streamer
func NewConversationStreamer(service *ConversationService) *ConversationStreamer

// StreamHistoricalData streams all existing conversation data
func (cs *ConversationStreamer) StreamHistoricalData(ctx context.Context, conversationID string) error

// StreamLiveUpdates watches for conversation updates and streams new entries
func (cs *ConversationStreamer) StreamLiveUpdates(ctx context.Context, conversationID string) error
```

### 2. Update `kodelet run` for Headless Mode

**File**: `cmd/kodelet/run.go`

Add headless support to the run command:

```go
type RunConfig struct {
    // ... existing fields ...
    Headless bool // Use structured logging output instead of console formatting
}

// In the command execution:
func runCommand(cmd *cobra.Command, args []string) error {
    // ... existing setup ...

    if config.Headless {
        // Silence all console output
        handler := &llmtypes.ConsoleMessageHandler{Silent: true}
        presenter.SetQuiet(true)
        
        // Run conversation in background
        go func() {
            thread.SendMessage(ctx, query, handler, messageOpts)
        }()
        
        // Stream the conversation using shared streaming logic
        service, err := conversations.GetDefaultConversationService(ctx)
        if err != nil {
            return err
        }
        defer service.Close()
        
        streamer := conversations.NewConversationStreamer(service)
        return streamer.StreamLiveUpdates(ctx, thread.GetConversationID())
    } else {
        // Normal execution with console output
        handler := &llmtypes.ConsoleMessageHandler{Silent: false}
        // ... existing execution logic ...
    }
}
```

Add the headless flag:
```go
func init() {
    // ... existing flags ...
    runCmd.Flags().Bool("headless", defaults.Headless, "Output structured JSON instead of console formatting")
}
```

### 3. Create `conversation stream` Command

**File**: `cmd/kodelet/conversation.go`

Add a conversation stream subcommand that uses the shared streaming infrastructure:

```go
var conversationStreamCmd = &cobra.Command{
    Use:   "stream [conversation-id]",
    Short: "Stream conversation updates in structured JSON format",
    Long:  "Stream conversation entries in real-time. Use --include-history to show historical data first, then stream new entries (like tail -f). All output is JSON - use jq for filtering and analysis.",
    Args:  cobra.ExactArgs(1),
    RunE:  streamConversation,
}

func init() {
    conversationStreamCmd.Flags().Bool("include-history", false, "Include historical conversation data before streaming new entries")
    conversationCmd.AddCommand(conversationStreamCmd)
}

func streamConversation(cmd *cobra.Command, args []string) error {
    conversationID := args[0]
    includeHistory, _ := cmd.Flags().GetBool("include-history")
    
    service, err := conversations.GetDefaultConversationService(ctx)
    if err != nil {
        return err
    }
    defer service.Close()
    
    streamer := conversations.NewConversationStreamer(service)
    
    // Stream historical data first if requested
    if includeHistory {
        if err := streamer.StreamHistoricalData(ctx, conversationID); err != nil {
            return err
        }
    }
    
    // Then stream live updates
    return streamer.StreamLiveUpdates(ctx, conversationID)
}
```

### 4. Provider-Specific Message Streaming

**Files**: `pkg/llm/anthropic/persistence.go`, `pkg/llm/openai/persistence.go`

Extend existing parsing functions to return streaming-compatible data:

```go
// StreamableMessage contains parsed message data for streaming
type StreamableMessage struct {
    Kind        string    // "text", "tool-use", "tool-result", "thinking"
    Role        string    // "user", "assistant", "system"
    Content     string    // Text content
    ToolName    string    // For tool use/result
    ToolCallID  string    // For matching tool results
    Input       string    // For tool use
}

// StreamMessages parses raw messages into streamable format
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tools.StructuredToolResult) ([]StreamableMessage, error)
```

### 5. Update Anthropic Parser

**File**: `pkg/llm/anthropic/persistence.go`

```go
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tools.StructuredToolResult) ([]StreamableMessage, error) {
    messages, err := DeserializeMessages(rawMessages)
    if err != nil {
        return nil, err
    }

    var streamable []StreamableMessage
    
    for _, msg := range messages {
        for _, contentBlock := range msg.Content {
            if textBlock := contentBlock.OfText; textBlock != nil {
                streamable = append(streamable, StreamableMessage{
                    Kind:      "text",
                    Role:      string(msg.Role),
                    Content:   textBlock.Text,
                })
            }
            
            if toolUseBlock := contentBlock.OfToolUse; toolUseBlock != nil {
                inputJSON, _ := json.Marshal(toolUseBlock.Input)
                streamable = append(streamable, StreamableMessage{
                    Kind:       "tool-use",
                    Role:       string(msg.Role),
                    ToolName:   toolUseBlock.Name,
                    ToolCallID: toolUseBlock.ID,
                    Input:      string(inputJSON),
                })
            }
            
            if toolResultBlock := contentBlock.OfToolResult; toolResultBlock != nil {
                result := ""
                if structuredResult, ok := toolResults[toolResultBlock.ToolUseID]; ok {
                    registry := renderers.NewRendererRegistry()
                    result = registry.Render(structuredResult)
                }
                streamable = append(streamable, StreamableMessage{
                    Kind:      "tool-result",
                    Role:      string(msg.Role),
                    ToolName:  extractToolName(toolResultBlock.ToolUseID, toolResults),
                    Result:    result,
                })
            }
        }
    }
    
    return streamable, nil
}
```

### 6. Update OpenAI Parser

**File**: `pkg/llm/openai/persistence.go`

Similar implementation for OpenAI message format parsing.

### 7. Add Tests

**File**: `pkg/conversations/streamer_test.go`

Comprehensive tests for:
- Shared streaming infrastructure
- Anthropic message parsing
- OpenAI message parsing  
- JSON output format
- Live streaming behavior
- Historical data inclusion

## Output Format Specification

All output is JSON format (newline-delimited JSON):

```json
{"kind": "text", "content": "Hello, I'll help you deploy the app", "role": "assistant"}
{"kind": "tool-use", "tool_name": "bash", "input": "kubectl get pods", "role": "assistant"}
{"kind": "tool-result", "tool_name": "bash", "result": "NAME                 READY   STATUS\napp-123              1/1     Running", "role": "assistant"}
{"kind": "thinking", "content": "The deployment looks successful", "role": "assistant"}
```

## Usage Examples

### Live Streaming with `kodelet run --headless`

```bash
# Live execution with structured JSON output
kodelet run --headless "deploy the application"

# Filter live output with jq - only show tool usage
kodelet run --headless "deploy app" | jq -r 'select(.kind=="tool-use" or .kind=="tool-result")'

# Monitor tool failures in real-time
kodelet run --headless "run tests" | jq -r 'select(.kind=="tool-result") | select(.result | contains("error") or contains("failed"))'
```

### Historical Streaming with `conversation stream`

```bash
# Stream new entries only from existing conversation
kodelet conversation stream conv-123

# Include historical context + stream new entries (like tail -f)
kodelet conversation stream --include-history conv-123
```

### Advanced Analysis with jq

```bash
# Filter with jq - only show tool usage
kodelet run --headless "deploy" | jq -r 'select(.kind=="tool-use" or .kind=="tool-result")'

# Filter out thinking blocks
kodelet conversation stream conv-123 | jq -r 'select(.kind != "thinking")'

# Real-time tool analysis
kodelet run --headless "complex task" | jq -r 'select(.kind=="tool-use") | .tool_name' | sort | uniq -c

# Complex filtering - only bash commands that failed
kodelet conversation stream --include-history conv-123 | jq -r 'select(.kind=="tool-result" and .tool_name=="bash" and (.result | contains("error") or contains("failed")))'
```

### CI/CD Integration

```bash
# Live monitoring during deployment
kodelet run --headless "run tests and deploy" | jq -r 'select(.kind=="tool-result" and .tool_name=="bash") | .result' > deployment.log

# Monitor specific conversation
kodelet conversation stream --include-history conv-123 | jq -r 'select(.kind=="tool-result" and .tool_name=="bash") | .result' > deployment.log
```

## Benefits

1. **Single Source of Truth** - One streaming implementation for both live and historical use cases
2. **Consistent Output** - Identical JSON format whether streaming live or historical data
3. **Clean Architecture** - No duplicate handler code, reuses conversation storage system
4. **KISS Principle** - Simple flags (`--headless`, `--include-history`), jq handles all filtering
5. **Unix Philosophy** - Plays well with standard tools (jq, grep, etc.)
6. **Powerful Analysis** - jq enables complex filtering and data extraction
7. **CI/CD Integration** - Perfect for automated deployment monitoring with custom filtering

## Implementation Order

1. Create shared streaming infrastructure in `pkg/conversations/streamer.go`
2. Update `kodelet run` with `--headless` support using the shared streamer
3. Add `conversation stream` command using the same shared streamer
4. Implement `StreamMessages` functions for Anthropic and OpenAI
5. Implement live streaming mechanism (polling or file watching)
6. Write comprehensive tests
7. Document usage patterns with jq examples

## Success Criteria

- `kodelet run --headless "query"` outputs structured JSON logs in real-time
- `kodelet conversation stream conv-123` streams live updates from stored conversation
- `kodelet conversation stream --include-history conv-123` shows historical data then streams new entries (like `tail -f`)
- Both commands output identical JSON format for the same conversation data
- All conversation data (including thinking) is streamed - users filter with jq as needed
- Tool usage analysis works in real-time with jq: `| jq -r 'select(.kind=="tool-use") | .tool_name'`
- Integration with CI/CD monitoring pipelines is straightforward using jq filtering
- Tests achieve >90% coverage for new code
- All existing conversation functionality remains unchanged