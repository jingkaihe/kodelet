"""Streaming event types for Kodelet SDK.

These correspond to the JSON events emitted by `kodelet run --headless --stream-deltas`.
"""

from dataclasses import dataclass
from typing import Any


@dataclass
class Event:
    """Base class for all streaming events."""

    kind: str
    conversation_id: str
    role: str = "assistant"
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

    base_kwargs = {
        "kind": kind,
        "conversation_id": conversation_id,
        "role": role,
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
