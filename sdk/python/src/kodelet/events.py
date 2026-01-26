"""Streaming event types for Kodelet SDK.

These correspond to the JSON events emitted by `kodelet run --headless --stream-deltas`.
"""

import json
from dataclasses import dataclass
from typing import Any

from .tool_results import ToolResult, UnknownToolResult, decode_tool_result


@dataclass
class Event:
    """Base class for all streaming events."""

    kind: str
    conversation_id: str
    role: str = "assistant"
    turn: int = 0  # Assistant turn number (1-indexed)
    raw: dict[str, Any] | None = None


@dataclass
class TextDeltaEvent(Event):
    """Partial text content (streaming)."""

    delta: str = ""


@dataclass
class TextEvent(Event):
    """Complete text block."""

    content: str = ""


@dataclass
class ThinkingStartEvent(Event):
    """Thinking block begins."""

    pass


@dataclass
class ThinkingDeltaEvent(Event):
    """Partial thinking content (streaming)."""

    delta: str = ""


@dataclass
class ThinkingEndEvent(Event):
    """Thinking block ends."""

    pass


@dataclass
class ThinkingEvent(Event):
    """Complete thinking block."""

    content: str = ""


@dataclass
class ToolUseEvent(Event):
    """Tool invocation."""

    tool_name: str = ""
    tool_call_id: str = ""
    input: str = ""  # JSON string


@dataclass
class ToolResultEvent(Event):
    """Tool execution result."""

    tool_name: str = ""
    tool_call_id: str = ""
    result: str = ""

    def decode_result(self) -> ToolResult:
        """Decode the tool result into a typed dataclass.

        Attempts to parse the result as JSON and decode it into the appropriate
        tool result type. If the result is not valid JSON or is in an unexpected
        format, returns an UnknownToolResult.

        Returns:
            A typed tool result dataclass (BashResult, FileReadResult, etc.)

        Example:
            ```python
            async for event in agent.query("list files"):
                if isinstance(event, ToolResultEvent):
                    result = event.decode_result()
                    if isinstance(result, BashResult):
                        print(f"Exit code: {result.exit_code}")
            ```
        """
        try:
            data = json.loads(self.result)
            if isinstance(data, dict) and "toolName" in data:
                return decode_tool_result(data)
        except (json.JSONDecodeError, TypeError, KeyError):
            pass

        return UnknownToolResult(
            tool_name=self.tool_name,
            success=True,
            error="",
            raw_metadata={},
        )


@dataclass
class ContentEndEvent(Event):
    """Content block ends."""

    pass


def parse_event(data: dict[str, Any]) -> Event:
    """Parse a JSON event into the appropriate Event type.

    Args:
        data: Raw JSON data from kodelet headless output

    Returns:
        Typed Event instance
    """
    kind = data.get("kind", "")
    conversation_id = data.get("conversation_id", "")
    role = data.get("role", "assistant")
    turn = data.get("turn", 0)

    base_kwargs = {
        "kind": kind,
        "conversation_id": conversation_id,
        "role": role,
        "turn": turn,
        "raw": data,
    }

    match kind:
        case "text-delta":
            return TextDeltaEvent(**base_kwargs, delta=data.get("delta", ""))

        case "text":
            return TextEvent(**base_kwargs, content=data.get("content", ""))

        case "thinking-start":
            return ThinkingStartEvent(**base_kwargs)

        case "thinking-delta":
            return ThinkingDeltaEvent(**base_kwargs, delta=data.get("delta", ""))

        case "thinking-end":
            return ThinkingEndEvent(**base_kwargs)

        case "thinking":
            return ThinkingEvent(**base_kwargs, content=data.get("content", ""))

        case "tool-use":
            return ToolUseEvent(
                **base_kwargs,
                tool_name=data.get("tool_name", ""),
                tool_call_id=data.get("tool_call_id", ""),
                input=data.get("input", ""),
            )

        case "tool-result":
            return ToolResultEvent(
                **base_kwargs,
                tool_name=data.get("tool_name", ""),
                tool_call_id=data.get("tool_call_id", ""),
                result=data.get("result", ""),
            )

        case "content-end":
            return ContentEndEvent(**base_kwargs)

        case _:
            return Event(**base_kwargs)
