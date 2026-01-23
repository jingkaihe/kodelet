# ADR 026: Headless Mode Partial Message Streaming

## Status

Accepted

## Context

### Background

Kodelet's `--headless` mode provides structured JSON output for programmatic consumption, enabling third-party UIs and automation workflows. Currently, headless mode streams **complete messages** only—text blocks, tool calls, and tool results are output after they are fully generated and persisted to the conversation database.

Modern chat UIs (like ChatGPT, Claude.io, or Cursor) provide real-time token streaming, where text appears character-by-character as the LLM generates it. This creates a more responsive user experience and provides immediate feedback that the agent is working.

### Current Implementation

The headless mode architecture works as follows:

```
┌─────────────────────────────────────────────────────────────────┐
│                    kodelet run --headless                       │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────────┐                  ┌─────────────────────┐   │
│  │  thread.Send    │──── writes ─────▶│  Conversation DB    │   │
│  │  Message()      │                  │  (SQLite)           │   │
│  └─────────────────┘                  └──────────┬──────────┘   │
│         │                                        │              │
│         │ Silent handler                         │ Polls        │
│         │ (ignores deltas)                       │ every 200ms  │
│         ▼                                        ▼              │
│  ┌─────────────────┐                  ┌─────────────────────┐   │
│  │  Console        │                  │  Conversation       │   │
│  │  MessageHandler │                  │  Streamer           │   │
│  │  {Silent: true} │                  │                     │   │
│  └─────────────────┘                  └──────────┬──────────┘   │
│                                                  │              │
│                                                  ▼              │
│                                       ┌─────────────────────┐   │
│                                       │  stdout (JSON)      │   │
│                                       │  Complete messages  │   │
│                                       └─────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

The LLM layer already supports streaming via the `StreamingMessageHandler` interface:

```go
type StreamingMessageHandler interface {
    MessageHandler
    HandleTextDelta(delta string)      // Called for each text chunk
    HandleThinkingStart()              // Called when thinking block starts
    HandleThinkingDelta(delta string)  // Called for each thinking chunk
    HandleThinkingBlockEnd()           // Called when thinking block ends
    HandleContentBlockEnd()            // Called when any content block ends
}
```

However, the headless mode uses `ConsoleMessageHandler{Silent: true}`, which discards all delta events.

### Problem Statement

1. **Latency**: Users must wait for complete messages before seeing any output
2. **No Progress Indication**: Long responses provide no feedback until complete
3. **UI Limitation**: Third-party UIs cannot implement real-time text streaming
4. **Inconsistent Experience**: Interactive mode shows streaming, headless does not

### Goals

1. Enable real-time token streaming in headless mode via `--stream-deltas` flag
2. Output delta events as structured JSON for third-party UI consumption
3. Maintain backward compatibility—existing headless behavior unchanged by default
4. Support streaming for both text and thinking content
5. Interleave delta events with complete message events (tool calls, tool results)

### Non-Goals

1. Changing the existing headless output format for complete messages
2. Streaming tool call inputs character-by-character (tool calls are atomic)
3. Implementing WebSocket or SSE transport (stdout JSON lines is sufficient)
4. Changing the interactive console streaming behavior

## Decision

Add a `--stream-deltas` flag to headless mode that enables real-time token streaming via a new `HeadlessStreamHandler`. Delta events are output as JSON lines interleaved with complete message events.

### Output Format

With `--stream-deltas` enabled, the output stream includes new event kinds:

| Kind | Description | Fields |
|------|-------------|--------|
| `text-delta` | Partial text content | `delta`, `conversation_id` |
| `thinking-delta` | Partial thinking content | `delta`, `conversation_id` |
| `thinking-start` | Thinking block begins | `conversation_id` |
| `thinking-end` | Thinking block ends | `conversation_id` |
| `content-end` | Content block ends | `conversation_id` |

Existing complete message kinds remain unchanged:
- `text` - Complete text block
- `thinking` - Complete thinking block
- `tool-use` - Tool invocation
- `tool-result` - Tool execution result

### Example Output

```jsonl
{"kind":"thinking-start","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-delta","delta":"Let me","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-delta","delta":" analyze this","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-delta","delta":" code...","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-end","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking","content":"Let me analyze this code...","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":"The","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":" answer","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":" is 42.","conversation_id":"abc123","role":"assistant"}
{"kind":"content-end","conversation_id":"abc123","role":"assistant"}
{"kind":"text","content":"The answer is 42.","conversation_id":"abc123","role":"assistant"}
{"kind":"tool-use","tool_name":"Bash","input":"{\"command\":\"ls\"}","tool_call_id":"tc_123","conversation_id":"abc123","role":"assistant"}
{"kind":"tool-result","tool_name":"Bash","result":"file1.txt\nfile2.txt","tool_call_id":"tc_123","conversation_id":"abc123","role":"assistant"}
```

Note: Complete messages (`text`, `thinking`) are still emitted after their delta streams, ensuring clients that ignore deltas still receive full content.

## Architecture Overview

### Modified Architecture with Delta Streaming

```
┌─────────────────────────────────────────────────────────────────┐
│              kodelet run --headless --stream-deltas             │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────────┐                  ┌─────────────────────┐   │
│  │  thread.Send    │──── writes ─────▶│  Conversation DB    │   │
│  │  Message()      │                  │  (SQLite)           │   │
│  └────────┬────────┘                  └──────────┬──────────┘   │
│           │                                      │              │
│           │ Streaming handler                    │ Polls        │
│           │ (outputs deltas)                     │ every 200ms  │
│           ▼                                      ▼              │
│  ┌─────────────────┐                  ┌─────────────────────┐   │
│  │  Headless       │─── deltas ──────▶│  stdout (JSON)      │   │
│  │  StreamHandler  │                  │  Delta + Complete   │   │
│  └─────────────────┘                  │  messages           │   │
│                                       └─────────────────────┘   │
│                                                 ▲               │
│                                                 │               │
│                                       ┌─────────┴───────────┐   │
│                                       │  Conversation       │   │
│                                       │  Streamer           │   │
│                                       │  (complete msgs)    │   │
│                                       └─────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Package Structure

```
pkg/types/llm/
├── handler.go           # Existing handlers + new HeadlessStreamHandler

cmd/kodelet/
├── run.go               # Add --stream-deltas flag

pkg/conversations/
├── streamer.go          # Add delta entry kinds (for documentation)
```

## Implementation Design

### New HeadlessStreamHandler

```go
// pkg/types/llm/handler.go

// HeadlessStreamHandler outputs streaming events as JSON to stdout
// for headless mode with --stream-deltas enabled.
type HeadlessStreamHandler struct {
    conversationID string
    mu             sync.Mutex
}

// DeltaEntry represents a streaming delta event
type DeltaEntry struct {
    Kind           string `json:"kind"`
    Delta          string `json:"delta,omitempty"`
    Content        string `json:"content,omitempty"`
    ConversationID string `json:"conversation_id"`
    Role           string `json:"role"`
}

func NewHeadlessStreamHandler(conversationID string) *HeadlessStreamHandler {
    return &HeadlessStreamHandler{
        conversationID: conversationID,
    }
}

func (h *HeadlessStreamHandler) output(entry DeltaEntry) {
    h.mu.Lock()
    defer h.mu.Unlock()
    
    data, _ := json.Marshal(entry)
    fmt.Fprintf(os.Stdout, "%s\n", data)
}

// HandleTextDelta outputs text delta events
func (h *HeadlessStreamHandler) HandleTextDelta(delta string) {
    h.output(DeltaEntry{
        Kind:           "text-delta",
        Delta:          delta,
        ConversationID: h.conversationID,
        Role:           "assistant",
    })
}

// HandleThinkingStart outputs thinking block start event
func (h *HeadlessStreamHandler) HandleThinkingStart() {
    h.output(DeltaEntry{
        Kind:           "thinking-start",
        ConversationID: h.conversationID,
        Role:           "assistant",
    })
}

// HandleThinkingDelta outputs thinking delta events
func (h *HeadlessStreamHandler) HandleThinkingDelta(delta string) {
    h.output(DeltaEntry{
        Kind:           "thinking-delta",
        Delta:          delta,
        ConversationID: h.conversationID,
        Role:           "assistant",
    })
}

// HandleThinkingBlockEnd outputs thinking block end event
func (h *HeadlessStreamHandler) HandleThinkingBlockEnd() {
    h.output(DeltaEntry{
        Kind:           "thinking-end",
        ConversationID: h.conversationID,
        Role:           "assistant",
    })
}

// HandleContentBlockEnd outputs content block end event
func (h *HeadlessStreamHandler) HandleContentBlockEnd() {
    h.output(DeltaEntry{
        Kind:           "content-end",
        ConversationID: h.conversationID,
        Role:           "assistant",
    })
}

// HandleText outputs complete text (for consistency with complete message stream)
func (h *HeadlessStreamHandler) HandleText(text string) {
    // Complete text is handled by ConversationStreamer
    // No-op here to avoid duplication
}

// HandleToolUse outputs tool use events
func (h *HeadlessStreamHandler) HandleToolUse(toolCallID, toolName, input string) {
    // Tool calls are handled by ConversationStreamer
    // No-op here to avoid duplication
}

// HandleToolResult outputs tool result events
func (h *HeadlessStreamHandler) HandleToolResult(toolCallID, toolName string, result tooltypes.ToolResult) {
    // Tool results are handled by ConversationStreamer
    // No-op here to avoid duplication
}

// HandleThinking outputs complete thinking (for consistency)
func (h *HeadlessStreamHandler) HandleThinking(thinking string) {
    // Complete thinking is handled by ConversationStreamer
    // No-op here to avoid duplication
}

// HandleDone is called when message processing is complete
func (h *HeadlessStreamHandler) HandleDone() {
    // No action needed
}
```

### CLI Flag Addition

```go
// cmd/kodelet/run.go

type RunConfig struct {
    // ... existing fields ...
    StreamDeltas bool // Stream partial text deltas in headless mode
}

func init() {
    // ... existing flags ...
    runCmd.Flags().Bool("stream-deltas", false, 
        "Stream partial text deltas in headless mode (requires --headless)")
}

func getRunConfigFromFlags(ctx context.Context, cmd *cobra.Command) *RunConfig {
    // ... existing code ...
    
    if streamDeltas, err := cmd.Flags().GetBool("stream-deltas"); err == nil {
        config.StreamDeltas = streamDeltas
    }
    
    // Validate: --stream-deltas requires --headless
    if config.StreamDeltas && !config.Headless {
        presenter.Error(errors.New("invalid flags"), 
            "--stream-deltas requires --headless mode")
        os.Exit(1)
    }
    
    return config
}
```

### Headless Mode Modification

```go
// cmd/kodelet/run.go (in runCmd.Run)

if config.Headless {
    presenter.SetQuiet(true)
    logger.SetLogFormat("json")
    logger.SetLogLevel("error")
    
    thread, err := llm.NewThread(llmConfig)
    if err != nil {
        presenter.Error(err, "Failed to create LLM thread")
        return
    }
    thread.SetState(appState)
    thread.SetConversationID(sessionID)
    thread.EnablePersistence(ctx, !config.NoSave)
    
    // Choose handler based on --stream-deltas flag
    var handler llmtypes.MessageHandler
    if config.StreamDeltas {
        handler = llmtypes.NewHeadlessStreamHandler(sessionID)
    } else {
        handler = &llmtypes.ConsoleMessageHandler{Silent: true}
    }
    
    streamer, closeFunc, err := llm.NewConversationStreamer(ctx)
    if err != nil {
        presenter.Error(err, "Failed to create conversation streamer")
        return
    }
    defer closeFunc()
    
    conversationID := thread.GetConversationID()
    done := make(chan error, 1)
    
    go func() {
        _, err := thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
            PromptCache:        true,
            Images:             config.Images,
            MaxTurns:           config.MaxTurns,
            CompactRatio:       config.CompactRatio,
            DisableAutoCompact: config.DisableAutoCompact,
            UseWeakModel:       config.UseWeakModel,
        })
        done <- err
    }()
    
    // ... rest of streaming logic unchanged ...
}
```

## Implementation Phases

### Phase 1: Core Implementation (1-2 days)
- [ ] Add `HeadlessStreamHandler` to `pkg/types/llm/handler.go`
- [ ] Add `--stream-deltas` flag to `cmd/kodelet/run.go`
- [ ] Wire up handler selection in headless mode
- [ ] Add flag validation (requires `--headless`)

### Phase 2: Testing (1 day)
- [ ] Unit tests for `HeadlessStreamHandler`
- [ ] Integration test verifying delta output format
- [ ] Test interleaving of deltas and complete messages
- [ ] Test with thinking-enabled models

### Phase 3: Documentation (0.5 days)
- [ ] Update `docs/MANUAL.md` with `--stream-deltas` documentation
- [ ] Add usage examples for third-party UI integration
- [ ] Update `AGENTS.md` if applicable

## Testing Strategy

### Unit Tests

```go
// pkg/types/llm/handler_test.go

func TestHeadlessStreamHandler_TextDelta(t *testing.T) {
    // Capture stdout
    old := os.Stdout
    r, w, _ := os.Pipe()
    os.Stdout = w
    
    handler := NewHeadlessStreamHandler("conv-123")
    handler.HandleTextDelta("Hello")
    handler.HandleTextDelta(" World")
    
    w.Close()
    os.Stdout = old
    
    var buf bytes.Buffer
    io.Copy(&buf, r)
    
    lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
    assert.Len(t, lines, 2)
    
    var entry1, entry2 DeltaEntry
    json.Unmarshal([]byte(lines[0]), &entry1)
    json.Unmarshal([]byte(lines[1]), &entry2)
    
    assert.Equal(t, "text-delta", entry1.Kind)
    assert.Equal(t, "Hello", entry1.Delta)
    assert.Equal(t, "conv-123", entry1.ConversationID)
    
    assert.Equal(t, "text-delta", entry2.Kind)
    assert.Equal(t, " World", entry2.Delta)
}

func TestHeadlessStreamHandler_ThinkingFlow(t *testing.T) {
    // Test thinking-start -> thinking-delta -> thinking-end sequence
}
```

### Integration Tests

```go
// cmd/kodelet/run_test.go

func TestHeadlessStreamDeltas(t *testing.T) {
    // Run kodelet with --headless --stream-deltas
    // Verify output contains both delta and complete message events
    // Verify correct ordering (deltas before complete)
}

func TestStreamDeltasRequiresHeadless(t *testing.T) {
    // Verify --stream-deltas without --headless fails
}
```

## Usage Examples

### Basic Usage

```bash
# Stream deltas with headless output
kodelet run --headless --stream-deltas "explain how TCP works"
```

### Filtering Deltas with jq

```bash
# Show only text deltas (real-time text streaming)
kodelet run --headless --stream-deltas "write a poem" | \
    jq -r 'select(.kind == "text-delta") | .delta' | tr -d '\n'

# Show thinking in real-time
kodelet run --headless --stream-deltas "solve this puzzle" | \
    jq -r 'select(.kind == "thinking-delta") | .delta' | tr -d '\n'
```

### Third-Party UI Integration (Python)

```python
import subprocess
import json

process = subprocess.Popen(
    ["kodelet", "run", "--headless", "--stream-deltas", "explain recursion"],
    stdout=subprocess.PIPE,
    text=True
)

current_text = ""
for line in process.stdout:
    event = json.loads(line)
    
    if event["kind"] == "text-delta":
        # Real-time text update
        current_text += event["delta"]
        update_ui(current_text)
    
    elif event["kind"] == "thinking-start":
        show_thinking_indicator()
    
    elif event["kind"] == "thinking-delta":
        update_thinking_panel(event["delta"])
    
    elif event["kind"] == "thinking-end":
        hide_thinking_indicator()
    
    elif event["kind"] == "tool-use":
        show_tool_execution(event["tool_name"], event["input"])
    
    elif event["kind"] == "tool-result":
        show_tool_result(event["tool_name"], event["result"])
```

## Security Considerations

1. **No New Attack Surface**: Delta streaming uses the same stdout channel as existing headless mode
2. **Same Content**: Deltas contain the same content that would be in complete messages
3. **No Sensitive Data Exposure**: Tool inputs/results still use existing complete message format

## Backward Compatibility

1. **Default Behavior Unchanged**: Without `--stream-deltas`, headless mode works exactly as before
2. **Additive Change**: New flag adds functionality without breaking existing integrations
3. **Complete Messages Still Emitted**: Even with `--stream-deltas`, complete messages are still output by the ConversationStreamer, ensuring clients that ignore deltas still work

## Conclusion

The `--stream-deltas` flag provides a clean, backward-compatible way to enable real-time token streaming in headless mode. The implementation:

1. **Leverages Existing Infrastructure**: Uses the `StreamingMessageHandler` interface already implemented in the LLM layer
2. **Maintains Separation of Concerns**: Delta handler and ConversationStreamer work independently
3. **Provides Clean Output Format**: JSON lines with clear event types for easy parsing
4. **Enables Rich UI Experiences**: Third-party UIs can implement ChatGPT-style streaming

The feature fills an important gap in kodelet's programmatic interface, enabling more responsive and modern user experiences for integrations.

## References

- [ADR 006: Conversation Persistence](006-conversation-persistence.md)
- [ADR 022: Agent Client Protocol](022-agent-client-protocol.md)
- [Claude Agent SDK - Partial Message Streaming](https://github.com/anthropics/claude-code/blob/main/packages/claude-agent-sdk-python/examples/include_partial_messages.py)
- [Anthropic Streaming API](https://docs.anthropic.com/en/api/streaming)
