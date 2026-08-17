# Conversations and steering

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

# Delete or fork
kodelet conversation delete <id>
kodelet conversation fork [id]

# Rename without invoking the model
kodelet run --resume <id> "/rename New conversation name"
kodelet run --follow "/rename New name for the latest conversation"
```

`conversation list --search` matches conversation IDs, working directories, first messages, and summaries.

Persisted conversations are named deterministically from the first user message, with whitespace folded and generated names limited to 100 characters. The generated name remains stable across later saves and context compaction. Use `/rename <name>` in terminal chat, ACP, or the Web UI, or use the `kodelet run --resume/--follow` forms above; an explicit rename takes precedence and does not invoke an LLM.

`conversation fork` is an experimental branching workflow. It copies the specified conversation, or the most recent conversation when no ID is provided, with its transcript and execution context; it resets cumulative usage and does not inherit the source conversation's active thread goal. Typical use:

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

## Steering autonomous work

```bash
kodelet steer --follow "great job, but please add tests"
kodelet steer --conversation-id ID "needs improvement on error handling"
```

## Streamlit ACP example

`examples/streamlit-acp/` communicates with `kodelet acp` using Agent Client Protocol for structured assistant and tool events.

Run from a cloned repo:

```bash
uv run skills/kodelet/examples/streamlit-acp/main.py
```

Run directly from GitHub:

```bash
uv run https://raw.githubusercontent.com/jingkaihe/kodelet/refs/heads/main/skills/kodelet/examples/streamlit-acp/main.py
```
