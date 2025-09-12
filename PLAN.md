# Conversation Stream Implementation Plan

## Overview
Implement a new `kodelet conversation stream CONV_ID` command that outputs stored conversations in structured JSON format with different `kind` fields for different message types. This approach leverages the existing conversation storage system without modifying the live execution path.

## Architecture Advantages

### Why `conversation stream` is Better Than LogHandler
- **Cleaner Architecture**: No changes to complex MessageHandler/execution system
- **Separation of Concerns**: Execution vs structured output are independent
- **True Streaming**: Always live by default, like `tail -f` for conversations
- **Historical Context**: Option to include past data before streaming new entries
- **Focused Output**: Clean JSON format optimized for real-time analysis and automation
- **Simpler Implementation**: Uses existing conversation parsing functions

### Current Conversation Architecture
- **Persistence**: Conversations store `RawMessages` (raw LLM provider messages), `ToolResults` (structured tool results), usage stats, and metadata
- **Existing Parsers**: `anthropic.ExtractMessages()` and `openai.ExtractMessages()` already parse provider-specific formats
- **Storage**: Complete conversation history available immediately after execution

## Implementation Plan

### 1. Create Stream Command Structure

**File**: `cmd/kodelet/conversation.go`

Add a new subcommand for conversation streaming:

```go
var conversationStreamCmd = &cobra.Command{
    Use:   "stream [conversation-id]",
    Short: "Live stream conversation updates in structured JSON format",
    Long:  "Stream new conversation entries in real-time. Use --include-history to show historical data first, then stream new entries (like tail -f). All output is JSON - use jq for filtering and analysis.",
    Args:  cobra.ExactArgs(1),
    RunE:  streamConversation,
}

func init() {
    conversationStreamCmd.Flags().Bool("include-history", false, "Include historical conversation data before streaming new entries")
    conversationCmd.AddCommand(conversationStreamCmd)
}
```

### 2. Add Stream Service Method

**File**: `pkg/conversations/service.go`

Add streaming capability to the ConversationService:

```go
// StreamRequest represents a request to stream a conversation
type StreamRequest struct {
    ConversationID   string `json:"conversationId"`
    IncludeHistory   bool   `json:"includeHistory"`  // Include historical data before streaming
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

func (s *ConversationService) GetHistoricalEntries(ctx context.Context, req *StreamRequest) ([]StreamEntry, error)
func (s *ConversationService) StreamNewEntries(ctx context.Context, req *StreamRequest) error
```

### 3. Provider-Specific Message Streaming

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

### 4. Update Anthropic Parser

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

### 5. Update OpenAI Parser

**File**: `pkg/llm/openai/persistence.go`

Similar implementation for OpenAI message format parsing.

### 6. Stream Command Implementation

**File**: `cmd/kodelet/conversation.go`

```go
func streamConversation(cmd *cobra.Command, args []string) error {
    conversationID := args[0]
    
    // Parse flags
    includeHistory, _ := cmd.Flags().GetBool("include-history")
    
    service, err := conversations.GetDefaultConversationService(ctx)
    if err != nil {
        return err
    }
    defer service.Close()
    
    req := &conversations.StreamRequest{
        ConversationID: conversationID,
        IncludeHistory: includeHistory,
    }
    
    return streamLive(ctx, service, req)
}

func streamLive(ctx context.Context, service *conversations.ConversationService, req *conversations.StreamRequest) error {
    // If include-history is true, first output historical data
    if req.IncludeHistory {
        entries, err := service.GetHistoricalEntries(ctx, req)
        if err != nil {
            return err
        }
        
        for _, entry := range entries {
            data, _ := json.Marshal(entry)
            fmt.Println(string(data))
        }
    }
    
    // Then start live streaming of new entries
    // Implementation: watch for conversation updates and stream new entries
    // Could use file watching, polling, or database triggers
    return streamNewEntries(ctx, service, req)
}

func streamLive(ctx context.Context, service *conversations.ConversationService, req *conversations.StreamRequest) error {
    // Implementation for live streaming with conversation updates
    // Could use file watching or polling mechanism
    return fmt.Errorf("live streaming not yet implemented")
}
```

### 7. Add Tests

**File**: `pkg/conversations/stream_test.go`

Comprehensive tests for:
- Anthropic message parsing
- OpenAI message parsing  
- JSON output format
- Historical data inclusion
- Live streaming behavior

## Output Format Specification

All output is JSON format (newline-delimited JSON):

```json
{"kind": "text", "content": "Hello, I'll help you deploy the app", "role": "assistant"}
{"kind": "tool-use", "tool_name": "bash", "input": "kubectl get pods", "role": "assistant"}
{"kind": "tool-result", "tool_name": "bash", "result": "NAME                 READY   STATUS\napp-123              1/1     Running", "role": "assistant"}
{"kind": "thinking", "content": "The deployment looks successful", "role": "assistant"}
```

## Usage Examples

### Basic Streaming
```bash
# Run conversation in one terminal
kodelet run "deploy the app"

# In another terminal, stream new entries only (from now forward)
kodelet conversation stream conv-123

# Or include historical data + stream new entries (like tail -f)
kodelet conversation stream --include-history conv-123
```

### Live Monitoring and Analysis
```bash
# Stream new entries only
kodelet conversation stream conv-123

# Include historical context + live streaming
kodelet conversation stream --include-history conv-123

# Filter with jq - only show tool usage
kodelet conversation stream conv-123 | jq -r 'select(.kind=="tool-use" or .kind=="tool-result")'

# Filter out thinking blocks
kodelet conversation stream conv-123 | jq -r 'select(.kind != "thinking")'

# Real-time tool analysis
kodelet conversation stream --include-history conv-123 | jq -r 'select(.kind=="tool-use") | .tool_name' | sort | uniq -c

# Complex filtering - only bash commands that failed
kodelet conversation stream conv-123 | jq -r 'select(.kind=="tool-result" and .tool_name=="bash" and (.result | contains("error") or contains("failed")))'
```

### CI/CD Integration
```bash
# Terminal 1: Start deployment
kodelet run "run tests and deploy" 

# Terminal 2: Monitor in real-time and capture only bash tool results
kodelet conversation stream --include-history conv-123 | jq -r 'select(.kind=="tool-result" and .tool_name=="bash") | .result' > deployment.log

# Monitor tool failures in real-time
kodelet conversation stream conv-123 | jq -r 'select(.kind=="tool-result") | select(.result | contains("error") or contains("failed"))'
```

## Benefits

1. **Clean Architecture** - No changes to execution path or handlers
2. **Live Streaming** - True real-time monitoring of conversation updates
3. **KISS Principle** - Single flag (`--include-history`), jq handles all filtering
4. **Unix Philosophy** - Plays well with standard tools (jq, grep, etc.)
5. **Powerful Analysis** - jq enables complex filtering and data extraction
6. **CI/CD Integration** - Perfect for automated deployment monitoring with custom filtering

## Implementation Order

1. Create basic `conversation stream` command with `--include-history` flag
2. Implement `StreamMessages` functions for Anthropic and OpenAI  
3. Add service methods for historical entries and live streaming
4. Implement JSON output (no filtering - jq handles that)
5. Implement live streaming mechanism (polling or file watching)
6. Write comprehensive tests
7. Document usage patterns with jq examples

## Success Criteria

- `kodelet conversation stream conv-123` starts live streaming of new entries in structured JSON format
- `kodelet conversation stream --include-history conv-123` shows historical data then streams new entries (like `tail -f`)
- JSON output is valid and parseable with proper field separation
- All conversation data (including thinking) is streamed - users filter with jq as needed
- Tool usage analysis works in real-time with jq: `| jq -r 'select(.kind=="tool-use") | .tool_name'`
- Integration with CI/CD monitoring pipelines is straightforward using jq filtering
- Tests achieve >90% coverage for new code
- All existing conversation functionality remains unchanged

**CI/CD Integration**:
```bash
# In CI pipeline, monitor tool usage and parse results
kodelet run --headless "run the tests and fix any failures" | jq -r 'select(.kind=="tool-use") | .tool_name' | sort | uniq -c
```

**Log Aggregation**:
```bash
# Stream to log aggregation system
kodelet run --headless "deploy the application" | fluent-cat kodelet.operations
```

**Conversation Management**:
```bash
# Resume conversation with structured output
konv_id=$(kodelet conversation list --limit 1 | jq -r '.conversations[0].id')
kodelet run --headless --resume $konv_id "continue with the deployment"
```