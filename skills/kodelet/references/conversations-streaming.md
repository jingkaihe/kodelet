# Conversations, streaming, and programmatic access

## Conversation management

```bash
# List conversations
kodelet conversation list
kodelet conversation list --search "keyword"

# View conversation
kodelet conversation show <id>
kodelet conversation show <id> --format markdown
kodelet conversation show <id> --format json
kodelet conversation show <id> --format raw
kodelet conversation show <id> --stats-only
kodelet conversation show <id> --no-header

# Stream conversation in real time
kodelet conversation stream <id>
kodelet conversation stream <id> --include-history
kodelet conversation stream <id> --history-only

# Delete or fork
kodelet conversation delete <id>
kodelet conversation fork <id>
```

`conversation fork` is an experimental branching workflow. Typical use:

1. Ensure clean git status.
2. Fork the conversation to try a different approach.
3. If it does not work, reset the worktree and continue with the original.

Output formats for `conversation show`:

| Format | Description |
| --- | --- |
| `text` | Human-readable output (default). |
| `markdown` | Markdown transcript with rendered tool calls/results. |
| `json` | Structured JSON with id, provider, summary, usage, and messages. |
| `raw` | Full `ConversationRecord` dump including raw messages, tool results, and metadata. |

## Headless mode

Headless mode outputs a structured JSON stream:

```bash
kodelet run --headless "analyze this codebase"
kodelet run --headless --include-history "continue analysis"
```

Complete message event examples:

```jsonl
{"kind":"text","role":"user","content":"What files are here?","conversation_id":"conv_123"}
{"kind":"thinking","role":"assistant","content":"User wants to see files..."}
{"kind":"tool-use","tool_name":"bash","input":"{\"command\":\"ls\"}","tool_call_id":"call_456"}
{"kind":"tool-result","tool_name":"bash","result":"file1.txt\nfile2.go"}
{"kind":"text","role":"assistant","content":"Here are the files..."}
```

Process with `jq`:

```bash
# Extract text messages
kodelet run --headless "query" | jq -r 'select(.kind == "text") | .content'

# Monitor tool usage
kodelet conversation stream ID | jq 'select(.kind == "tool-use") | .tool_name'
```

## Partial streaming with `--stream-deltas`

`--stream-deltas` streams partial text/thinking chunks as they are generated, while still emitting complete messages for clients that ignore deltas.

```bash
kodelet run --headless --stream-deltas "explain how TCP works"

kodelet run --headless --stream-deltas "write a poem" | \
  jq -r 'select(.kind == "text-delta") | .delta' | tr -d '\n'

kodelet run --headless --stream-deltas "solve this puzzle" | \
  jq -r 'select(.kind == "thinking-delta") | .delta' | tr -d '\n'
```

Delta event kinds:

| Kind | Description |
| --- | --- |
| `text-delta` | Partial text content chunk. |
| `thinking-delta` | Partial thinking content chunk. |
| `thinking-start` | Thinking block begins. |
| `thinking-end` | Thinking block ends. |
| `content-end` | Content block ends. |

Example delta stream:

```jsonl
{"kind":"thinking-start","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-delta","delta":"Let me analyze...","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-end","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":"The answer","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":" is 42.","conversation_id":"abc123","role":"assistant"}
{"kind":"content-end","conversation_id":"abc123","role":"assistant"}
{"kind":"text","content":"The answer is 42.","conversation_id":"abc123","role":"assistant"}
```

## Steering autonomous work

```bash
kodelet steer --follow "great job, but please add tests"
kodelet steer --conversation-id ID "needs improvement on error handling"
```

## Examples

Example projects in this skill:

- `examples/streamlit/` wraps `kodelet run --headless --stream-deltas` and parses JSON stream events.
- `examples/streamlit-acp/` communicates with `kodelet acp` using Agent Client Protocol for richer event handling.

Run from a cloned repo:

```bash
uv run skills/kodelet/examples/streamlit/main.py
uv run skills/kodelet/examples/streamlit-acp/main.py
```

Run the ACP example directly from GitHub:

```bash
uv run https://raw.githubusercontent.com/jingkaihe/kodelet/refs/heads/main/skills/kodelet/examples/streamlit-acp/main.py
```
