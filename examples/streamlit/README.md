# Kodelet Streamlit Chatbot

A Streamlit chatbot interface that wraps kodelet's CLI with real-time streaming output.

## Usage

Run with uv:

```bash
uv run main.py
```

The script auto-launches Streamlit when run directly.

## Requirements

- kodelet binary (either `./bin/kodelet` from building or system-installed)
- Valid API keys configured for kodelet (e.g., `ANTHROPIC_API_KEY`)

## How It Works

The chatbot shells out to `kodelet run --headless --stream-deltas` for each message, parsing the JSON stream events:

| Event | Description |
|-------|-------------|
| `text-delta` | Streams assistant text in real-time |
| `thinking-delta` | Shows model thinking (if enabled) |
| `tool-use` | Displays tool invocations |
| `tool-result` | Shows tool execution results |

For follow-up messages, it uses `--resume CONVERSATION_ID` to maintain context.

When loading from URL (`?c=<id>`), it uses `kodelet conversation stream <id> --history-only` to fetch history.
