# Kodelet Python SDK

Python SDK for [Kodelet](https://github.com/jingkaihe/kodelet) - AI-assisted software engineering CLI.

## Installation

```bash
# Using uv (recommended)
uv add kodelet-sdk

# Using pip
pip install kodelet-sdk
```

**Note**: The SDK requires the `kodelet` binary to be installed and available in your PATH.

## Quick Start

```python
import asyncio
from kodelet import Kodelet

async def main():
    agent = Kodelet()
    
    async for event in agent.query("Hello World"):
        if event.kind == "text-delta":
            print(event.delta, end="", flush=True)
    print()

asyncio.run(main())
```

## Features

- **Async-first**: Built on `asyncio` for non-blocking I/O
- **Streaming**: Real-time token streaming via async generators
- **Type-safe**: Full type annotations for IDE support
- **Zero dependencies**: Uses only Python standard library
- **Conversation management**: List, show, delete, and resume conversations
- **Hooks support**: Python DSL for lifecycle hooks
- **MCP integration**: Configure MCP servers for custom tools

## Configuration

```python
from kodelet import Kodelet, KodeletConfig

config = KodeletConfig(
    provider="anthropic",
    model="claude-opus-4-5-20251101",
    max_tokens=16000,
    thinking_budget_tokens=8000,
    allowed_tools=["bash", "file_read", "file_write"],
    cwd="/path/to/project",  # Working directory
)

agent = Kodelet(config=config)
```

### Available Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `provider` | str | "anthropic" | LLM provider (anthropic, openai, google) |
| `model` | str | "claude-sonnet-4-5-20250929" | Model to use |
| `weak_model` | str | "claude-haiku-4-5-20251001" | Weak model for simple tasks |
| `max_tokens` | int | 8192 | Maximum tokens for response |
| `thinking_budget_tokens` | int | 4048 | Tokens for thinking capability |
| `allowed_tools` | list[str] | [] | Restrict available tools |
| `allowed_commands` | list[str] | [] | Restrict bash commands |
| `cwd` | Path | None | Working directory |
| `max_turns` | int | 50 | Maximum conversation turns |
| `no_skills` | bool | False | Disable agentic skills |
| `no_hooks` | bool | False | Disable lifecycle hooks |
| `no_mcp` | bool | False | Disable MCP tools |
| `stream_deltas` | bool | True | Enable streaming deltas |

## Conversation Management

```python
from kodelet import Kodelet

agent = Kodelet()

# Send a query
async for event in agent.query("Write a fibonacci function"):
    pass

# Continue the conversation
async for event in agent.query("Add tests for it"):
    pass

# Get conversation ID
print(f"Conversation: {agent.conversation_id}")

# List conversations
conversations = await agent.conversations.list(limit=10)
for conv in conversations:
    print(f"{conv.id}: {conv.preview}")

# Show a specific conversation
conv = await agent.conversations.show("20240101T120000-abc123")
for msg in conv.messages:
    print(f"{msg.role}: {msg.content}")

# Delete a conversation
await agent.conversations.delete("20240101T120000-abc123")

# Resume a specific conversation
agent = Kodelet(resume="20240101T120000-abc123")

# Follow most recent conversation
agent = Kodelet(follow=True)
```

## Hooks

Define Python hooks to intercept and control agent behavior:

```python
from kodelet import Kodelet
from kodelet.hooks import Hook, HookType

@Hook(HookType.BEFORE_TOOL_CALL)
def security_guardrail(payload: dict) -> dict:
    """Block dangerous commands."""
    if payload.get("tool_name") == "Bash":
        command = payload.get("tool_input", {}).get("command", "")
        if "rm -rf" in command:
            return {"blocked": True, "reason": "Dangerous command blocked"}
    return {"blocked": False}

@Hook(HookType.AFTER_TOOL_CALL)
def audit_logger(payload: dict) -> dict:
    """Log all tool calls."""
    print(f"Tool called: {payload.get('tool_name')}")
    return {}

@Hook(HookType.AGENT_STOP)
def cleanup_check(payload: dict) -> dict:
    """Check if cleanup is needed."""
    from pathlib import Path
    if Path("./temp_file.txt").exists():
        return {"follow_up_messages": ["Please remove temp_file.txt"]}
    return {}

agent = Kodelet(hooks=[security_guardrail, audit_logger, cleanup_check])
```

### Hook Types

| Type | Trigger | Can Block | Description |
|------|---------|-----------|-------------|
| `BEFORE_TOOL_CALL` | Before tool execution | Yes | Inspect/block tool calls |
| `AFTER_TOOL_CALL` | After tool execution | No | Audit/log tool results |
| `USER_MESSAGE_SEND` | When user sends message | Yes | Filter user input |
| `AGENT_STOP` | When agent would stop | No | Add follow-up messages |
| `TURN_END` | After assistant response | No | Post-turn processing |

## MCP Integration

Configure MCP servers for custom tools:

```python
from kodelet import Kodelet
from kodelet.mcp import StdioServer, SSEServer

# Stdio-based MCP server
filesystem = StdioServer(
    name="filesystem",
    command="docker",
    args=["run", "-i", "--rm", "mcp/filesystem", "/"],
    tool_whitelist=["list_directory", "read_file"],
)

# SSE-based MCP server
api_server = SSEServer(
    name="my_api",
    base_url="http://localhost:8080",
    headers={"Authorization": "Bearer token"},
)

agent = Kodelet(mcp_servers=[filesystem, api_server])
```

For custom Python tools, create an MCP server using the `mcp` package:

```python
# my_tools_server.py
from mcp.server import Server

server = Server("my-tools")

@server.tool()
async def calculator(expression: str) -> str:
    """Evaluate mathematical expressions."""
    return str(eval(expression, {"__builtins__": {}}, {}))

if __name__ == "__main__":
    import asyncio
    asyncio.run(server.run_stdio())
```

Then configure the SDK:

```python
my_tools = StdioServer(
    name="my_tools",
    command="python",
    args=["my_tools_server.py"],
)

agent = Kodelet(mcp_servers=[my_tools])
```

## Event Types

The `query()` method yields events as they're received:

```python
async for event in agent.query("Hello"):
    match event.kind:
        case "text-delta":
            print(event.delta, end="")
        case "text":
            print(f"Complete: {event.content}")
        case "thinking-start":
            print("[Thinking...]")
        case "thinking-delta":
            pass  # Thinking content
        case "thinking-end":
            print("[Done thinking]")
        case "tool-use":
            print(f"Using tool: {event.tool_name}")
        case "tool-result":
            print(f"Tool result: {event.result[:100]}...")
```

## Error Handling

```python
from kodelet import Kodelet
from kodelet.exceptions import (
    KodeletError,
    BinaryNotFoundError,
    ConversationNotFoundError,
)

try:
    agent = Kodelet()
    async for event in agent.query("Hello"):
        pass
except BinaryNotFoundError:
    print("kodelet binary not found in PATH")
except ConversationNotFoundError as e:
    print(f"Conversation not found: {e}")
except KodeletError as e:
    print(f"Kodelet error: {e}")
```

## Development

See [AGENTS.md](./AGENTS.md) for development setup, testing, and contribution guidelines.

## License

MIT
