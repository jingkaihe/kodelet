"""Integration tests for Kodelet SDK.

These tests require:
1. kodelet binary installed and in PATH
2. Valid API keys configured (ANTHROPIC_API_KEY or OPENAI_API_KEY)

Run with: uv run pytest tests/test_integration.py -v -m integration
"""

import shutil

import pytest

from kodelet import Kodelet, KodeletConfig
from kodelet.events import TextDeltaEvent, TextEvent

pytestmark = pytest.mark.integration


def has_kodelet() -> bool:
    """Check if kodelet is available."""
    return shutil.which("kodelet") is not None


@pytest.mark.skipif(not has_kodelet(), reason="kodelet binary not found in PATH")
@pytest.mark.asyncio
async def test_simple_query():
    """Test basic query functionality with real kodelet."""
    config = KodeletConfig(
        no_skills=True,
        no_hooks=True,
        no_mcp=True,
        no_save=True,  # Don't save conversation
        max_turns=1,
    )
    agent = Kodelet(config=config)

    events = []
    async for event in agent.query("What is 2+2? Answer with just the number."):
        events.append(event)

    # Should have received events
    assert len(events) > 0

    # Should have text events
    text_events = [e for e in events if isinstance(e, (TextDeltaEvent, TextEvent))]
    assert len(text_events) > 0

    # Should have a conversation ID
    assert agent.conversation_id is not None


@pytest.mark.skipif(not has_kodelet(), reason="kodelet binary not found in PATH")
@pytest.mark.asyncio
async def test_run_method():
    """Test the convenience run method."""
    config = KodeletConfig(
        no_skills=True,
        no_hooks=True,
        no_mcp=True,
        no_save=True,
        max_turns=1,
    )
    agent = Kodelet(config=config)

    result = await agent.run("What is the capital of France? Answer with just the city name.")

    assert isinstance(result, str)
    assert len(result) > 0
    # The answer should contain "Paris"
    assert "paris" in result.lower() or "Paris" in result


@pytest.mark.skipif(not has_kodelet(), reason="kodelet binary not found in PATH")
@pytest.mark.asyncio
async def test_conversation_list():
    """Test listing conversations."""
    agent = Kodelet()

    # This should not raise, even if there are no conversations
    conversations = await agent.conversations.list(limit=5)

    assert isinstance(conversations, list)
