"""Tests for conversation management."""

import json
from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest

from kodelet.conversation import (
    Conversation,
    ConversationManager,
    ConversationMessage,
    ConversationSummary,
)
from kodelet.exceptions import ConversationNotFoundError, KodeletError


@pytest.fixture
def manager() -> ConversationManager:
    return ConversationManager(Path("/usr/bin/kodelet"))


@pytest.mark.asyncio
async def test_list_conversations(manager: ConversationManager):
    mock_output = json.dumps({
        "conversations": [
            {
                "id": "conv-001",
                "created_at": "2024-01-01T12:00:00Z",
                "updated_at": "2024-01-01T13:00:00Z",
                "message_count": 5,
                "provider": "anthropic",
                "preview": "Hello world...",
                "total_cost": 0.01,
                "current_context_window": 1000,
                "max_context_window": 100000,
            },
            {
                "id": "conv-002",
                "created_at": "2024-01-02T12:00:00Z",
                "updated_at": "2024-01-02T13:00:00Z",
                "message_count": 10,
                "provider": "openai",
                "preview": "Another conversation...",
                "total_cost": 0.05,
                "current_context_window": 2000,
                "max_context_window": 128000,
            },
        ]
    })

    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 0
        mock_process.communicate = AsyncMock(return_value=(mock_output.encode(), b""))
        mock_exec.return_value = mock_process

        conversations = await manager.list(limit=10)

        assert len(conversations) == 2
        assert conversations[0].id == "conv-001"
        assert conversations[0].provider == "anthropic"
        assert conversations[0].message_count == 5
        assert conversations[1].id == "conv-002"
        assert conversations[1].provider == "openai"


@pytest.mark.asyncio
async def test_list_conversations_with_search(manager: ConversationManager):
    mock_output = json.dumps({"conversations": []})

    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 0
        mock_process.communicate = AsyncMock(return_value=(mock_output.encode(), b""))
        mock_exec.return_value = mock_process

        await manager.list(search="test", provider="anthropic", limit=5)

        # Verify command was called with search parameters
        call_args = mock_exec.call_args[0]
        assert "--search" in call_args
        assert "test" in call_args
        assert "--provider" in call_args
        assert "anthropic" in call_args


@pytest.mark.asyncio
async def test_show_conversation(manager: ConversationManager):
    mock_output = json.dumps({
        "id": "conv-001",
        "provider": "anthropic",
        "summary": "A test conversation",
        "created_at": "2024-01-01T12:00:00Z",
        "updated_at": "2024-01-01T13:00:00Z",
        "messages": [
            {"role": "user", "content": "Hello"},
            {"role": "assistant", "content": "Hi there!"},
        ],
        "usage": {"input_tokens": 100, "output_tokens": 50},
    })

    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 0
        mock_process.communicate = AsyncMock(return_value=(mock_output.encode(), b""))
        mock_exec.return_value = mock_process

        conv = await manager.show("conv-001")

        assert isinstance(conv, Conversation)
        assert conv.id == "conv-001"
        assert conv.provider == "anthropic"
        assert conv.summary == "A test conversation"
        assert len(conv.messages) == 2
        assert conv.messages[0].role == "user"
        assert conv.messages[0].content == "Hello"
        assert conv.messages[1].role == "assistant"


@pytest.mark.asyncio
async def test_show_conversation_not_found(manager: ConversationManager):
    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 1
        mock_process.communicate = AsyncMock(return_value=(b"", b"Not found"))
        mock_exec.return_value = mock_process

        with pytest.raises(ConversationNotFoundError):
            await manager.show("nonexistent")


@pytest.mark.asyncio
async def test_delete_conversation(manager: ConversationManager):
    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 0
        mock_process.communicate = AsyncMock(return_value=(b"Deleted", b""))
        mock_exec.return_value = mock_process

        # Should not raise
        await manager.delete("conv-001")

        # Verify --no-confirm was passed (always used)
        call_args = mock_exec.call_args[0]
        assert "--no-confirm" in call_args


@pytest.mark.asyncio
async def test_delete_conversation_error(manager: ConversationManager):
    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 1
        mock_process.communicate = AsyncMock(return_value=(b"", b"Error deleting"))
        mock_exec.return_value = mock_process

        with pytest.raises(KodeletError, match="Failed to delete"):
            await manager.delete("conv-001")


@pytest.mark.asyncio
async def test_fork_conversation(manager: ConversationManager):
    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 0
        mock_process.communicate = AsyncMock(
            return_value=(b"Conversation forked successfully. New ID: conv-002", b"")
        )
        mock_exec.return_value = mock_process

        new_id = await manager.fork("conv-001")

        assert new_id == "conv-002"


@pytest.mark.asyncio
async def test_fork_conversation_error(manager: ConversationManager):
    with patch("asyncio.create_subprocess_exec") as mock_exec:
        mock_process = AsyncMock()
        mock_process.returncode = 1
        mock_process.communicate = AsyncMock(return_value=(b"", b"Fork failed"))
        mock_exec.return_value = mock_process

        with pytest.raises(KodeletError, match="Failed to fork"):
            await manager.fork("conv-001")


def test_conversation_summary_dataclass():
    from datetime import datetime

    summary = ConversationSummary(
        id="conv-001",
        created_at=datetime(2024, 1, 1, 12, 0, 0),
        updated_at=datetime(2024, 1, 1, 13, 0, 0),
        message_count=5,
        provider="anthropic",
        preview="Hello...",
        total_cost=0.01,
        current_context=1000,
        max_context=100000,
    )

    assert summary.id == "conv-001"
    assert summary.message_count == 5


def test_conversation_message_dataclass():
    msg = ConversationMessage(role="user", content="Hello")

    assert msg.role == "user"
    assert msg.content == "Hello"
