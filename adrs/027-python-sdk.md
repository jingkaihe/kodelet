# ADR 027: Python SDK for Kodelet

## Status

Proposed

## Context

Kodelet provides a powerful CLI for AI-assisted software engineering tasks. While the CLI is effective for interactive use and shell scripting, programmatic integration from Python applications requires a cleaner interface than subprocess calls with manual JSON parsing.

### Background

The existing `kodelet run --headless --stream-deltas` mode provides structured JSON output suitable for programmatic consumption, as demonstrated in the `examples/streamlit/main.py` example. However, building applications on top of this requires:

1. **Manual process management**: Starting, monitoring, and cleaning up kodelet processes
2. **JSON parsing**: Handling the various event types in the streaming protocol
3. **Configuration management**: Translating Python parameters to CLI flags
4. **Error handling**: Interpreting exit codes and stderr output
5. **Conversation management**: Tracking conversation IDs for follow-ups

A Python SDK would abstract these concerns and provide a Pythonic interface for building AI-powered applications.

### Goals

1. **Simple Query Interface**: Async generator-based API for streaming responses
2. **Full CLI Feature Parity**: Support all `kodelet run` options programmatically
3. **Conversation Management**: List, show, delete, and resume conversations
4. **MCP Integration**: Configure MCP servers programmatically (custom tools via MCP)
5. **Type Safety**: Full type hints for IDE support and type checking
6. **Modern Python**: Python 3.12+ with async/await patterns
7. **Zero Dependencies**: Use only Python standard library

### Non-Goals

1. **Reimplementing kodelet in Python**: SDK wraps the CLI binary
2. **Direct LLM API access**: All LLM calls go through kodelet
3. **Binary distribution**: SDK requires kodelet binary to be installed

## Decision

Create a Python SDK (`kodelet-sdk`) that wraps the kodelet CLI binary, providing a Pythonic async interface for all kodelet functionality.

### Core Design Principles

1. **Thin Wrapper**: The SDK is a thin wrapper around the CLI, not a reimplementation
2. **Async-First**: Use `asyncio` for all I/O operations
3. **Streaming by Default**: Return async generators for real-time output
4. **Type-Safe**: Full type annotations with dataclasses/Pydantic models
5. **Configuration Hierarchy**: Support programmatic, file-based, and environment config

## Architecture Overview

### Package Structure

```
sdk/python/
├── pyproject.toml           # uv-managed project configuration
├── README.md                # SDK documentation
├── src/
│   └── kodelet/
│       ├── __init__.py      # Public API exports
│       ├── client.py        # Main Kodelet client class
│       ├── config.py        # Configuration management
│       ├── events.py        # Streaming event types
│       ├── conversation.py  # Conversation management
│       ├── mcp.py           # MCP server configuration
│       └── exceptions.py    # Custom exceptions
├── tests/
│   ├── __init__.py
│   ├── test_client.py
│   ├── test_conversation.py
│   └── test_mcp.py
└── examples/
    ├── simple_query.py
    ├── mcp_tools.py
    └── streamlit_app.py
```

### Core API Design

#### Simple Query

```python
from kodelet import Kodelet

async def main():
    agent = Kodelet()  # Uses default config
    
    async for event in agent.query("Hello World"):
        print(event)

asyncio.run(main())
```

#### Custom Configuration

```python
from kodelet import Kodelet, KodeletConfig

config = KodeletConfig(
    provider="anthropic",
    model="claude-opus-4-5-20251101",
    weak_model="claude-haiku-4-5-20251001",
    thinking_budget_tokens=8000,
    max_tokens=16000,
    weak_model_max_tokens=8000,
    allowed_tools=["bash", "file_read", "file_write", "file_edit"],
    cwd="/path/to/project",  # Working directory for agent
)

agent = Kodelet(config=config)

async for event in agent.query("Write a fibonacci sequence"):
    if event.kind == "text-delta":
        print(event.delta, end="", flush=True)
```

#### Conversation Management

```python
from kodelet import Kodelet

agent = Kodelet()

# Start a conversation
async for event in agent.query("Write a fibonacci function"):
    pass  # Process events

# Continue the same conversation
async for event in agent.query("Add tests for it"):
    pass

# Access conversation ID
print(f"Conversation: {agent.conversation_id}")

# List conversations
conversations = await agent.conversations.list(limit=10)
for conv in conversations:
    print(f"{conv.id}: {conv.summary} ({conv.message_count} messages)")

# Show a specific conversation
conv = await agent.conversations.show("20240101T120000-abc123")
for message in conv.messages:
    print(f"{message.role}: {message.content}")

# Delete a conversation
await agent.conversations.delete("20240101T120000-abc123")

# Resume a specific conversation
agent = Kodelet(resume="20240101T120000-abc123")
async for event in agent.query("Continue from where we left off"):
    pass

# Follow most recent conversation
agent = Kodelet(follow=True)
async for event in agent.query("Continue"):
    pass
```

### Event Types

```python
from dataclasses import dataclass
from typing import Literal, Optional, Any
from datetime import datetime

@dataclass
class BaseEvent:
    """Base class for all streaming events"""
    kind: str
    conversation_id: str
    role: str = "assistant"

@dataclass
class TextDeltaEvent(BaseEvent):
    """Partial text content"""
    kind: Literal["text-delta"] = "text-delta"
    delta: str = ""

@dataclass
class TextEvent(BaseEvent):
    """Complete text block"""
    kind: Literal["text"] = "text"
    content: str = ""

@dataclass
class ThinkingStartEvent(BaseEvent):
    """Thinking block begins"""
    kind: Literal["thinking-start"] = "thinking-start"

@dataclass
class ThinkingDeltaEvent(BaseEvent):
    """Partial thinking content"""
    kind: Literal["thinking-delta"] = "thinking-delta"
    delta: str = ""

@dataclass
class ThinkingEndEvent(BaseEvent):
    """Thinking block ends"""
    kind: Literal["thinking-end"] = "thinking-end"

@dataclass
class ThinkingEvent(BaseEvent):
    """Complete thinking block"""
    kind: Literal["thinking"] = "thinking"
    content: str = ""

@dataclass
class ToolUseEvent(BaseEvent):
    """Tool invocation"""
    kind: Literal["tool-use"] = "tool-use"
    tool_name: str = ""
    tool_call_id: str = ""
    input: str = ""  # JSON string

@dataclass
class ToolResultEvent(BaseEvent):
    """Tool execution result"""
    kind: Literal["tool-result"] = "tool-result"
    tool_name: str = ""
    tool_call_id: str = ""
    result: str = ""

@dataclass
class ContentEndEvent(BaseEvent):
    """Content block ends"""
    kind: Literal["content-end"] = "content-end"

# Union type for all events
Event = (
    TextDeltaEvent | TextEvent | 
    ThinkingStartEvent | ThinkingDeltaEvent | ThinkingEndEvent | ThinkingEvent |
    ToolUseEvent | ToolResultEvent | ContentEndEvent
)
```

### Configuration

```python
from dataclasses import dataclass, field
from typing import Optional
from pathlib import Path

@dataclass
class KodeletConfig:
    """Configuration for Kodelet client"""
    
    # LLM Configuration
    provider: str = "anthropic"
    model: str = "claude-sonnet-4-5-20250929"
    weak_model: str = "claude-haiku-4-5-20251001"
    max_tokens: int = 8192
    weak_model_max_tokens: int = 8192
    thinking_budget_tokens: int = 4048
    reasoning_effort: str = "medium"  # For OpenAI models
    
    # Tool Configuration
    allowed_tools: list[str] = field(default_factory=list)
    allowed_commands: list[str] = field(default_factory=list)
    
    # Execution Configuration
    cwd: Optional[Path] = None  # Working directory
    max_turns: int = 50
    compact_ratio: float = 0.8
    disable_auto_compact: bool = False
    
    # Output Configuration
    stream_deltas: bool = True
    include_history: bool = False
    
    # Feature Flags
    no_skills: bool = False
    no_hooks: bool = False
    no_mcp: bool = False
    no_save: bool = False
    
    # Binary Path
    kodelet_path: Optional[Path] = None  # Auto-detect if not specified
    
    # Anthropic-specific
    account: Optional[str] = None  # Anthropic account alias
```

### Hooks Support

The SDK provides a Python DSL for defining lifecycle hooks that automatically integrate with kodelet's binary hook protocol:

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

**How it works:**
1. Python functions decorated with `@Hook` are registered with their hook type
2. When `query()` is called, the SDK generates executable Python scripts in `.kodelet/hooks/`
3. These scripts implement the binary hook protocol (responding to `hook` and `run` commands)
4. After the query completes, the generated scripts are cleaned up

**Hook Types:**
- `BEFORE_TOOL_CALL`: Inspect/block tool calls before execution
- `AFTER_TOOL_CALL`: Audit/log tool results after execution
- `USER_MESSAGE_SEND`: Filter user input before sending
- `AGENT_STOP`: Add follow-up messages when agent would stop
- `TURN_END`: Post-turn processing

To disable hooks entirely:

```python
agent = Kodelet(config=KodeletConfig(no_hooks=True))
```

### Custom Tools via MCP

For custom tools, users should create MCP servers using the official `mcp` Python package. The SDK configures kodelet to connect to these servers. This approach:

1. **Leverages existing infrastructure** - No need to reinvent tool protocols
2. **Full Python support** - MCP servers can use any Python libraries
3. **Standard protocol** - Tools work with any MCP-compatible client

Example using the `mcp` package to create a custom tool server:

```python
# my_tools_server.py
from mcp.server import Server
from mcp.types import Tool, TextContent

server = Server("my-tools")

@server.tool()
async def calculator(expression: str) -> str:
    """Evaluate mathematical expressions"""
    return str(eval(expression, {"__builtins__": {}}, {}))

@server.tool()
async def weather(location: str, units: str = "celsius") -> str:
    """Get weather for a location"""
    return f"Weather in {location}: 22°{units[0].upper()}, sunny"

if __name__ == "__main__":
    import asyncio
    asyncio.run(server.run_stdio())
```

Then configure the SDK to use it:

```python
from kodelet import Kodelet
from kodelet.mcp import StdioServer

my_tools = StdioServer(
    name="my_tools",
    command="python",
    args=["my_tools_server.py"],
)

agent = Kodelet(mcp_servers=[my_tools])

async for event in agent.query("What's 2+2?"):
    pass
```

### MCP Configuration

```python
from kodelet import Kodelet
from kodelet.mcp import MCPServer, StdioServer, SSEServer

# Stdio-based MCP server
filesystem = StdioServer(
    name="fs",
    command="docker",
    args=["run", "-i", "--rm", "mcp/filesystem", "/"],
    tool_whitelist=["list_directory", "read_file"]
)

# SSE-based MCP server
time_server = SSEServer(
    name="time",
    base_url="http://localhost:8080",
    headers={"Authorization": "Bearer token"}
)

agent = Kodelet(
    mcp_servers=[filesystem, time_server]
)

async for event in agent.query("List files in /tmp"):
    pass
```

### Error Handling

```python
from kodelet import Kodelet
from kodelet.exceptions import (
    KodeletError,
    BinaryNotFoundError,
    ConfigurationError,
    ConversationNotFoundError,
    ToolExecutionError,
    HookBlockedError
)

agent = Kodelet()

try:
    async for event in agent.query("Do something"):
        pass
except BinaryNotFoundError:
    print("kodelet binary not found in PATH")
except ConfigurationError as e:
    print(f"Configuration error: {e}")
except HookBlockedError as e:
    print(f"Hook blocked operation: {e.reason}")
except KodeletError as e:
    print(f"Kodelet error: {e}")
```

## Implementation Design

### Client Implementation

```python
# src/kodelet/client.py
import asyncio
import json
import shutil
import tempfile
import os
from pathlib import Path
from typing import AsyncGenerator, Optional

from .config import KodeletConfig
from .events import Event, parse_event
from .conversation import ConversationManager
from .mcp import MCPServer
from .exceptions import BinaryNotFoundError, KodeletError

class Kodelet:
    """Main client for interacting with kodelet"""
    
    def __init__(
        self,
        config: Optional[KodeletConfig] = None,
        resume: Optional[str] = None,
        follow: bool = False,
        mcp_servers: Optional[list[MCPServer]] = None,
    ):
        self.config = config or KodeletConfig()
        self._resume = resume
        self._follow = follow
        self._conversation_id: Optional[str] = None
        self._mcp_servers = mcp_servers or []
        
        # Find kodelet binary
        self._binary = self._find_binary()
        
        # Conversation manager (lazy initialization)
        self._conversations: Optional[ConversationManager] = None
    
    def _find_binary(self) -> Path:
        """Find kodelet binary"""
        if self.config.kodelet_path:
            if self.config.kodelet_path.exists():
                return self.config.kodelet_path
            raise BinaryNotFoundError(f"Kodelet binary not found at {self.config.kodelet_path}")
        
        # Check PATH
        binary = shutil.which("kodelet")
        if binary:
            return Path(binary)
        
        raise BinaryNotFoundError("kodelet binary not found in PATH")
    
    @property
    def conversation_id(self) -> Optional[str]:
        """Current conversation ID"""
        return self._conversation_id
    
    @property
    def conversations(self) -> ConversationManager:
        """Access conversation management"""
        if self._conversations is None:
            self._conversations = ConversationManager(self._binary, self.config.cwd)
        return self._conversations
    
    def _build_command(self, query: str) -> list[str]:
        """Build kodelet command with all flags"""
        cmd = [str(self._binary), "run", "--headless"]
        
        if self.config.stream_deltas:
            cmd.append("--stream-deltas")
        
        # Provider and model
        cmd.extend(["--provider", self.config.provider])
        cmd.extend(["--model", self.config.model])
        cmd.extend(["--weak-model", self.config.weak_model])
        cmd.extend(["--max-tokens", str(self.config.max_tokens)])
        cmd.extend(["--weak-model-max-tokens", str(self.config.weak_model_max_tokens)])
        cmd.extend(["--thinking-budget-tokens", str(self.config.thinking_budget_tokens)])
        
        if self.config.provider == "openai":
            cmd.extend(["--reasoning-effort", self.config.reasoning_effort])
        
        # Tools
        if self.config.allowed_tools:
            cmd.extend(["--allowed-tools", ",".join(self.config.allowed_tools)])
        
        if self.config.allowed_commands:
            cmd.extend(["--allowed-commands", ",".join(self.config.allowed_commands)])
        
        # Execution options
        cmd.extend(["--max-turns", str(self.config.max_turns)])
        cmd.extend(["--compact-ratio", str(self.config.compact_ratio)])
        
        if self.config.disable_auto_compact:
            cmd.append("--disable-auto-compact")
        
        if self.config.include_history:
            cmd.append("--include-history")
        
        # Feature flags
        if self.config.no_skills:
            cmd.append("--no-skills")
        
        if self.config.no_hooks:
            cmd.append("--no-hooks")
        
        if self.config.no_mcp:
            cmd.append("--no-mcp")
        
        if self.config.no_save:
            cmd.append("--no-save")
        
        # Conversation management
        if self._resume:
            cmd.extend(["--resume", self._resume])
        elif self._follow:
            cmd.append("--follow")
        elif self._conversation_id:
            cmd.extend(["--resume", self._conversation_id])
        
        # Anthropic account
        if self.config.account:
            cmd.extend(["--account", self.config.account])
        
        # Query
        cmd.append(query)
        
        return cmd
    
    def _generate_mcp_config(self) -> str:
        """Generate YAML config for MCP servers"""
        if not self._mcp_servers:
            return ""
        
        lines = ["mcp:", "  servers:"]
        for server in self._mcp_servers:
            lines.append(f"    {server.name}:")
            lines.extend(server.to_yaml_lines(indent=6))
        return "\n".join(lines)
    
    async def query(self, message: str) -> AsyncGenerator[Event, None]:
        """Send a query and stream the response"""
        cmd = self._build_command(message)
        cwd = self.config.cwd or Path.cwd()
        env = os.environ.copy()
        
        # Create temp config file for MCP servers if needed
        config_file = None
        if self._mcp_servers:
            mcp_config = self._generate_mcp_config()
            config_file = tempfile.NamedTemporaryFile(
                mode='w', suffix='.yaml', delete=False
            )
            config_file.write(mcp_config)
            config_file.close()
            env["KODELET_CONFIG"] = config_file.name
        
        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=cwd,
                env=env,
            )
            
            try:
                async for line in process.stdout:
                    line = line.decode().strip()
                    if not line:
                        continue
                    
                    try:
                        data = json.loads(line)
                        event = parse_event(data)
                        
                        # Track conversation ID
                        if event.conversation_id and not self._conversation_id:
                            self._conversation_id = event.conversation_id
                        
                        yield event
                        
                    except json.JSONDecodeError:
                        continue
                
                await process.wait()
                
                if process.returncode != 0:
                    stderr = await process.stderr.read()
                    raise KodeletError(f"kodelet exited with code {process.returncode}: {stderr.decode()}")
                    
            except asyncio.CancelledError:
                process.terminate()
                await process.wait()
                raise
        finally:
            # Clean up temp config file
            if config_file:
                Path(config_file.name).unlink(missing_ok=True)
    
    async def run(self, message: str) -> str:
        """Send a query and return the complete response (convenience method)"""
        result = []
        async for event in self.query(message):
            if event.kind == "text":
                result.append(event.content)
        return "".join(result)
```

### Conversation Manager

```python
# src/kodelet/conversation.py
import asyncio
import json
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Optional

@dataclass
class ConversationMessage:
    role: str
    content: str

@dataclass
class ConversationSummary:
    id: str
    created_at: datetime
    updated_at: datetime
    message_count: int
    provider: str
    preview: str
    total_cost: float
    current_context: int
    max_context: int

@dataclass
class Conversation:
    id: str
    provider: str
    summary: str
    created_at: datetime
    updated_at: datetime
    messages: list[ConversationMessage]
    usage: dict

class ConversationManager:
    """Manage kodelet conversations"""
    
    def __init__(self, binary: Path, cwd: Optional[Path] = None):
        self._binary = binary
        self._cwd = cwd or Path.cwd()
    
    async def list(
        self,
        limit: int = 10,
        offset: int = 0,
        search: Optional[str] = None,
        provider: Optional[str] = None,
        start_date: Optional[str] = None,
        end_date: Optional[str] = None,
        sort_by: str = "updated_at",
        sort_order: str = "desc",
    ) -> list[ConversationSummary]:
        """List conversations"""
        cmd = [str(self._binary), "conversation", "list", "--json"]
        cmd.extend(["--limit", str(limit)])
        cmd.extend(["--offset", str(offset)])
        cmd.extend(["--sort-by", sort_by])
        cmd.extend(["--sort-order", sort_order])
        
        if search:
            cmd.extend(["--search", search])
        if provider:
            cmd.extend(["--provider", provider])
        if start_date:
            cmd.extend(["--start", start_date])
        if end_date:
            cmd.extend(["--end", end_date])
        
        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=self._cwd,
        )
        
        stdout, stderr = await process.communicate()
        
        if process.returncode != 0:
            raise KodeletError(f"Failed to list conversations: {stderr.decode()}")
        
        data = json.loads(stdout.decode())
        return [
            ConversationSummary(
                id=c["id"],
                created_at=datetime.fromisoformat(c["created_at"]),
                updated_at=datetime.fromisoformat(c["updated_at"]),
                message_count=c["message_count"],
                provider=c["provider"],
                preview=c["preview"],
                total_cost=c["total_cost"],
                current_context=c["current_context_window"],
                max_context=c["max_context_window"],
            )
            for c in data["conversations"]
        ]
    
    async def show(self, conversation_id: str) -> Conversation:
        """Show a specific conversation"""
        cmd = [
            str(self._binary), "conversation", "show",
            conversation_id, "--format", "json"
        ]
        
        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=self._cwd,
        )
        
        stdout, stderr = await process.communicate()
        
        if process.returncode != 0:
            raise ConversationNotFoundError(f"Conversation {conversation_id} not found")
        
        data = json.loads(stdout.decode())
        return Conversation(
            id=data["id"],
            provider=data["provider"],
            summary=data.get("summary", ""),
            created_at=datetime.fromisoformat(data["created_at"]),
            updated_at=datetime.fromisoformat(data["updated_at"]),
            messages=[
                ConversationMessage(role=m["role"], content=m["content"])
                for m in data.get("messages", [])
            ],
            usage=data.get("usage", {}),
        )
    
    async def delete(self, conversation_id: str, confirm: bool = True) -> None:
        """Delete a conversation"""
        cmd = [str(self._binary), "conversation", "delete", conversation_id]
        if not confirm:
            cmd.append("--no-confirm")
        
        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            stdin=asyncio.subprocess.PIPE,
            cwd=self._cwd,
        )
        
        # Auto-confirm if confirm=True (send 'y')
        if confirm:
            stdout, stderr = await process.communicate(input=b"y\n")
        else:
            stdout, stderr = await process.communicate()
        
        if process.returncode != 0:
            raise KodeletError(f"Failed to delete conversation: {stderr.decode()}")
    
    async def fork(self, conversation_id: Optional[str] = None) -> str:
        """Fork a conversation, returns new conversation ID"""
        cmd = [str(self._binary), "conversation", "fork"]
        if conversation_id:
            cmd.append(conversation_id)
        
        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=self._cwd,
        )
        
        stdout, stderr = await process.communicate()
        
        if process.returncode != 0:
            raise KodeletError(f"Failed to fork conversation: {stderr.decode()}")
        
        # Parse the new conversation ID from output
        output = stdout.decode()
        # Expected: "Conversation forked successfully. New ID: xxx"
        for line in output.split("\n"):
            if "New ID:" in line:
                return line.split("New ID:")[1].strip()
        
        raise KodeletError("Could not parse new conversation ID from fork output")
```

## Testing Strategy

### Unit Tests

```python
# tests/test_client.py
import pytest
from unittest.mock import AsyncMock, patch
from kodelet import Kodelet, KodeletConfig

@pytest.mark.asyncio
async def test_simple_query():
    """Test basic query functionality"""
    with patch("kodelet.client.asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 0
        mock_process.stdout.__aiter__.return_value = iter([
            b'{"kind":"text-delta","delta":"Hello","conversation_id":"test-123","role":"assistant"}\n',
            b'{"kind":"text","content":"Hello World","conversation_id":"test-123","role":"assistant"}\n',
        ])
        mock_process.stderr.read = AsyncMock(return_value=b"")
        mock_exec.return_value = mock_process
        
        agent = Kodelet()
        events = []
        async for event in agent.query("test"):
            events.append(event)
        
        assert len(events) == 2
        assert events[0].kind == "text-delta"
        assert events[1].kind == "text"
        assert agent.conversation_id == "test-123"

@pytest.mark.asyncio
async def test_config_to_command():
    """Test configuration translates to correct CLI flags"""
    config = KodeletConfig(
        provider="openai",
        model="gpt-4.1",
        max_tokens=4096,
        allowed_tools=["bash", "file_read"],
    )
    
    agent = Kodelet(config=config)
    cmd = agent._build_command("test query")
    
    assert "--provider" in cmd
    assert "openai" in cmd
    assert "--model" in cmd
    assert "gpt-4.1" in cmd
    assert "--allowed-tools" in cmd
    assert "bash,file_read" in cmd
```

### Integration Tests

```python
# tests/test_integration.py
import pytest
from kodelet import Kodelet

@pytest.mark.integration
@pytest.mark.asyncio
async def test_real_query():
    """Integration test with actual kodelet binary"""
    agent = Kodelet()
    
    events = []
    async for event in agent.query("What is 2+2? Answer in one word."):
        events.append(event)
    
    # Should have at least one text event
    text_events = [e for e in events if e.kind == "text"]
    assert len(text_events) > 0
    
    # Answer should contain "4" or "four"
    full_text = "".join(e.content for e in text_events)
    assert "4" in full_text.lower() or "four" in full_text.lower()
```

## Package Configuration

```toml
# pyproject.toml
[project]
name = "kodelet-sdk"
version = "0.1.0"
description = "Python SDK for Kodelet - AI-assisted software engineering CLI"
readme = "README.md"
license = { text = "MIT" }
requires-python = ">=3.12"
authors = [
    { name = "Kodelet Contributors" }
]
keywords = ["kodelet", "ai", "llm", "cli", "sdk"]
classifiers = [
    "Development Status :: 3 - Alpha",
    "Intended Audience :: Developers",
    "License :: OSI Approved :: MIT License",
    "Programming Language :: Python :: 3",
    "Programming Language :: Python :: 3.12",
    "Programming Language :: Python :: 3.13",
    "Topic :: Software Development :: Libraries :: Python Modules",
    "Typing :: Typed",
]

dependencies = []

[project.optional-dependencies]
dev = [
    "pytest>=8.0",
    "pytest-asyncio>=0.23",
    "pytest-cov>=4.0",
    "mypy>=1.8",
    "ruff>=0.3",
]

[project.urls]
Homepage = "https://github.com/jingkaihe/kodelet"
Documentation = "https://github.com/jingkaihe/kodelet/tree/main/sdk/python"
Repository = "https://github.com/jingkaihe/kodelet"

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["src/kodelet"]

[tool.pytest.ini_options]
asyncio_mode = "auto"
testpaths = ["tests"]
markers = [
    "integration: marks tests as integration tests (deselect with '-m \"not integration\"')",
]

[tool.mypy]
python_version = "3.12"
strict = true

[tool.ruff]
target-version = "py312"
line-length = 100

[tool.ruff.lint]
select = ["E", "F", "I", "N", "W", "UP", "B", "C4", "SIM"]
```

## Implementation Phases

### Phase 1: Core Client (Week 1)
- [ ] Create project structure with uv
- [ ] Implement `KodeletConfig` dataclass
- [ ] Implement event types and `parse_event()` function
- [ ] Implement `Kodelet` client class with `query()` method
- [ ] Add exception types
- [ ] Write unit tests

### Phase 2: Conversation Management (Week 1-2)
- [ ] Implement `ConversationManager`
- [ ] Add list, show, delete, fork operations
- [ ] Support resume and follow patterns
- [ ] Write tests

### Phase 3: MCP Support (Week 2)
- [ ] Implement `StdioServer` and `SSEServer` configuration classes
- [ ] Generate temporary config files for MCP servers
- [ ] Write tests

### Phase 4: Documentation and Examples (Week 2)
- [ ] Write comprehensive README
- [ ] Create example applications
- [ ] Add inline documentation

## Backward Compatibility

The SDK is designed to be forward-compatible with kodelet CLI changes:
1. **Version Detection**: SDK can check kodelet version and adjust behavior
2. **Flag Passthrough**: Unknown options can be passed through to CLI
3. **Graceful Degradation**: Missing features fall back to basic functionality

## Security Considerations

1. **No Credential Handling**: SDK relies on kodelet's credential management
2. **Process Isolation**: Each query runs in a separate process
3. **Working Directory**: Configurable `cwd` prevents unintended file access
4. **Tool Validation**: Custom tools are validated before registration

## Conclusion

The Python SDK provides a Pythonic interface to kodelet while:

1. **Leveraging Existing Infrastructure**: Uses the well-tested `--headless` mode
2. **Maintaining Feature Parity**: Supports all CLI options programmatically
3. **Enabling Advanced Use Cases**: Hooks, custom tools, and MCP integration
4. **Following Python Best Practices**: Async-first, type-safe, well-documented

The thin wrapper approach ensures the SDK stays in sync with kodelet development while providing a clean abstraction for Python applications.

## References

- [ADR 026: Headless Mode Partial Message Streaming](026-headless-partial-message-streaming.md)
- [ADR 021: Agent Lifecycle Hooks](021-agent-lifecycle-hooks.md)
- [ADR 020: Agentic Skills](020-agentic-skills.md)
- [docs/HOOKS.md](../docs/HOOKS.md)
- [docs/mcp.md](../docs/mcp.md)
- [examples/streamlit/main.py](../examples/streamlit/main.py)
