# Kodelet Streamlit Chatbot (ACP)

A Streamlit chatbot interface that communicates with kodelet via the Agent Client Protocol (ACP).

## Quick Start (One-Liner)

Run directly from GitHub without cloning:

```bash
uv run https://raw.githubusercontent.com/jingkaihe/kodelet/refs/heads/main/examples/streamlit-acp/main.py
```

## Local Usage

If you've cloned the repository:

```bash
uv run main.py
```

The script auto-launches Streamlit when run directly.

## Requirements

- kodelet binary with ACP support (either `./bin/kodelet` from building or system-installed)
- Valid API keys configured for kodelet (e.g., `ANTHROPIC_API_KEY`)

## How It Works

The chatbot communicates with kodelet using the Agent Client Protocol (ACP), which provides a structured way to:

- Send messages and receive streaming responses
- Handle tool calls with progress updates
- Manage conversation state
- Support thinking/reasoning visualization

Unlike the CLI-based streamlit example, this version uses ACP for more robust communication and richer event handling.
