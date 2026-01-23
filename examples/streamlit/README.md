# Kodelet Streamlit Chatbot

A Streamlit chatbot interface that wraps kodelet's CLI with real-time streaming output, styled with Kodelet's brand colors.

## Features

- Real-time streaming text output
- Thinking process visualization (expandable)
- Tool call inspection with inputs and results
- Kodelet brand styling (Poppins/Lora fonts, orange/blue/green accents)
- Chat history within session (stateless between messages)

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

The chatbot shells out to `kodelet run --headless --stream-deltas --no-save` for each message, parsing the JSON stream events:

| Event | Description |
|-------|-------------|
| `text-delta` | Streams assistant text in real-time |
| `thinking-delta` | Shows model thinking (if enabled) |
| `tool-use` | Displays tool invocations |
| `tool-result` | Shows tool execution results |

## Brand Colors

The UI uses Kodelet's official color palette:

- **Primary**: `#d97757` (orange)
- **Secondary**: `#6a9bcc` (blue)
- **Accent**: `#788c5d` (green)
- **Background**: `#faf9f5` (light)
- **Text**: `#141413` (dark)
