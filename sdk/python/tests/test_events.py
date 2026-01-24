"""Tests for event parsing."""

from kodelet.events import (
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


def test_parse_text_delta_event():
    data = {
        "kind": "text-delta",
        "delta": "Hello",
        "conversation_id": "conv-123",
        "role": "assistant",
    }
    event = parse_event(data)

    assert isinstance(event, TextDeltaEvent)
    assert event.kind == "text-delta"
    assert event.delta == "Hello"
    assert event.conversation_id == "conv-123"
    assert event.role == "assistant"


def test_parse_text_event():
    data = {
        "kind": "text",
        "content": "Hello World",
        "conversation_id": "conv-123",
        "role": "assistant",
    }
    event = parse_event(data)

    assert isinstance(event, TextEvent)
    assert event.kind == "text"
    assert event.content == "Hello World"


def test_parse_thinking_events():
    # Thinking start
    event = parse_event({"kind": "thinking-start", "conversation_id": "c1", "role": "assistant"})
    assert isinstance(event, ThinkingStartEvent)

    # Thinking delta
    data = {
        "kind": "thinking-delta",
        "delta": "Let me think...",
        "conversation_id": "c1",
        "role": "assistant",
    }
    event = parse_event(data)
    assert isinstance(event, ThinkingDeltaEvent)
    assert event.delta == "Let me think..."

    # Thinking end
    event = parse_event({"kind": "thinking-end", "conversation_id": "c1", "role": "assistant"})
    assert isinstance(event, ThinkingEndEvent)

    # Complete thinking
    data = {
        "kind": "thinking",
        "content": "Full thought",
        "conversation_id": "c1",
        "role": "assistant",
    }
    event = parse_event(data)
    assert isinstance(event, ThinkingEvent)
    assert event.content == "Full thought"


def test_parse_tool_use_event():
    data = {
        "kind": "tool-use",
        "tool_name": "bash",
        "tool_call_id": "tc-123",
        "input": '{"command": "ls -la"}',
        "conversation_id": "conv-123",
        "role": "assistant",
    }
    event = parse_event(data)

    assert isinstance(event, ToolUseEvent)
    assert event.tool_name == "bash"
    assert event.tool_call_id == "tc-123"
    assert event.input == '{"command": "ls -la"}'


def test_parse_tool_result_event():
    data = {
        "kind": "tool-result",
        "tool_name": "bash",
        "tool_call_id": "tc-123",
        "result": "file1.txt\nfile2.txt",
        "conversation_id": "conv-123",
        "role": "assistant",
    }
    event = parse_event(data)

    assert isinstance(event, ToolResultEvent)
    assert event.tool_name == "bash"
    assert event.result == "file1.txt\nfile2.txt"


def test_parse_content_end_event():
    data = {"kind": "content-end", "conversation_id": "conv-123", "role": "assistant"}
    event = parse_event(data)

    assert isinstance(event, ContentEndEvent)


def test_parse_unknown_event():
    data = {"kind": "unknown-event", "conversation_id": "conv-123", "role": "assistant"}
    event = parse_event(data)

    assert isinstance(event, Event)
    assert event.kind == "unknown-event"


def test_event_preserves_raw_data():
    data = {
        "kind": "text",
        "content": "Hello",
        "conversation_id": "c1",
        "role": "assistant",
        "extra_field": "extra_value",
    }
    event = parse_event(data)

    assert event.raw == data
    assert event.raw["extra_field"] == "extra_value"
