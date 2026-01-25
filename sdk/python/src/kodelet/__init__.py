"""Kodelet SDK - Python SDK for Kodelet AI-assisted software engineering CLI.

This package provides a Python interface for interacting with the kodelet CLI,
enabling programmatic access to AI-assisted software engineering capabilities.

Example:
    ```python
    import asyncio
    from kodelet import Kodelet, KodeletConfig

    async def main():
        # Simple usage with defaults
        agent = Kodelet()

        async for event in agent.query("Hello World"):
            if event.kind == "text-delta":
                print(event.delta, end="", flush=True)
        print()

        # Custom configuration
        config = KodeletConfig(
            provider="anthropic",
            model="claude-opus-4-5-20251101",
            max_tokens=16000,
        )
        agent = Kodelet(config=config)

        response = await agent.run("Write a fibonacci function")
        print(response)

    asyncio.run(main())
    ```
"""

from .client import Kodelet
from .config import KodeletConfig
from .conversation import (
    Conversation,
    ConversationManager,
    ConversationMessage,
    ConversationSummary,
)
from .events import (
    ContentEndEvent,
    Event,
    TextDeltaEvent,
    TextEvent,
    ThinkingDeltaEvent,
    ThinkingEndEvent,
    ThinkingEvent,
    ThinkingStartEvent,
    ToolResultEvent,
    ToolUseEvent,
    parse_event,
)
from .exceptions import (
    BinaryNotFoundError,
    ConfigurationError,
    ConversationNotFoundError,
    KodeletError,
    QueryError,
)
from .hooks import Hook, HookDefinition, HookFunc, HookManager, HookType
from .mcp import MCPServer, SSEServer, StdioServer
from .tool_results import (
    BackgroundBashResult,
    BackgroundProcessInfo,
    BashResult,
    BlockedResult,
    CodeExecutionResult,
    CustomToolResult,
    Edit,
    FileEditResult,
    FileInfo,
    FileReadResult,
    FileWriteResult,
    GlobResult,
    GrepResult,
    ImageDimensions,
    ImageRecognitionResult,
    MCPContent,
    MCPToolResult,
    SearchMatch,
    SearchResult,
    SkillResult,
    SubAgentResult,
    TodoItem,
    TodoResult,
    TodoStats,
    ToolResult,
    UnknownToolResult,
    ViewBackgroundProcessesResult,
    WebFetchResult,
    decode_tool_result,
)

__version__ = "0.1.0"

__all__ = [
    # Main client
    "Kodelet",
    "KodeletConfig",
    # Events
    "Event",
    "TextDeltaEvent",
    "TextEvent",
    "ThinkingStartEvent",
    "ThinkingDeltaEvent",
    "ThinkingEndEvent",
    "ThinkingEvent",
    "ToolUseEvent",
    "ToolResultEvent",
    "ContentEndEvent",
    "parse_event",
    # Conversations
    "ConversationManager",
    "Conversation",
    "ConversationSummary",
    "ConversationMessage",
    # Hooks
    "Hook",
    "HookType",
    "HookFunc",
    "HookDefinition",
    "HookManager",
    # MCP
    "MCPServer",
    "StdioServer",
    "SSEServer",
    # Exceptions
    "KodeletError",
    "BinaryNotFoundError",
    "ConfigurationError",
    "ConversationNotFoundError",
    "QueryError",
    # Tool Results
    "ToolResult",
    "BashResult",
    "BackgroundBashResult",
    "FileReadResult",
    "FileWriteResult",
    "FileEditResult",
    "GrepResult",
    "GlobResult",
    "TodoResult",
    "ImageRecognitionResult",
    "SubAgentResult",
    "WebFetchResult",
    "ViewBackgroundProcessesResult",
    "CodeExecutionResult",
    "SkillResult",
    "MCPToolResult",
    "CustomToolResult",
    "BlockedResult",
    "UnknownToolResult",
    "decode_tool_result",
    # Tool Result helpers
    "Edit",
    "SearchMatch",
    "SearchResult",
    "FileInfo",
    "TodoItem",
    "TodoStats",
    "ImageDimensions",
    "BackgroundProcessInfo",
    "MCPContent",
]
