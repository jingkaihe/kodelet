"""Conversation management for Kodelet SDK."""

import asyncio
import json
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

from .exceptions import ConversationNotFoundError, KodeletError


@dataclass
class ConversationMessage:
    """A message in a conversation."""

    role: str
    content: str


@dataclass
class ConversationSummary:
    """Summary information about a conversation."""

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
    """Full conversation details."""

    id: str
    provider: str
    summary: str
    created_at: datetime
    updated_at: datetime
    messages: list[ConversationMessage]
    usage: dict[str, Any]


class ConversationManager:
    """Manage kodelet conversations.

    Provides methods to list, show, delete, and fork conversations.
    """

    def __init__(self, binary: Path, cwd: Path | None = None):
        """Initialize the conversation manager.

        Args:
            binary: Path to the kodelet binary
            cwd: Working directory for commands
        """
        self._binary = binary
        self._cwd = cwd or Path.cwd()

    async def list(
        self,
        limit: int = 10,
        offset: int = 0,
        search: str | None = None,
        provider: str | None = None,
        start_date: str | None = None,
        end_date: str | None = None,
        sort_by: str = "updated_at",
        sort_order: str = "desc",
    ) -> list[ConversationSummary]:
        """List conversations.

        Args:
            limit: Maximum number of conversations to return
            offset: Offset for pagination
            search: Search term to filter conversations
            provider: Filter by provider (anthropic, openai, google)
            start_date: Filter conversations after this date (YYYY-MM-DD)
            end_date: Filter conversations before this date (YYYY-MM-DD)
            sort_by: Field to sort by (updated_at, created_at, messages)
            sort_order: Sort order (asc, desc)

        Returns:
            List of conversation summaries
        """
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
                created_at=datetime.fromisoformat(c["created_at"].replace("Z", "+00:00")),
                updated_at=datetime.fromisoformat(c["updated_at"].replace("Z", "+00:00")),
                message_count=c["message_count"],
                provider=c["provider"],
                preview=c["preview"],
                total_cost=c["total_cost"],
                current_context=c["current_context_window"],
                max_context=c["max_context_window"],
            )
            for c in data.get("conversations", [])
        ]

    async def show(self, conversation_id: str) -> Conversation:
        """Show a specific conversation.

        Args:
            conversation_id: The conversation ID

        Returns:
            Full conversation details

        Raises:
            ConversationNotFoundError: If the conversation does not exist
        """
        cmd = [
            str(self._binary),
            "conversation",
            "show",
            conversation_id,
            "--format",
            "json",
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
            created_at=datetime.fromisoformat(data["created_at"].replace("Z", "+00:00")),
            updated_at=datetime.fromisoformat(data["updated_at"].replace("Z", "+00:00")),
            messages=[
                ConversationMessage(role=m["role"], content=m["content"])
                for m in data.get("messages", [])
            ],
            usage=data.get("usage", {}),
        )

    async def delete(self, conversation_id: str) -> None:
        """Delete a conversation.

        Args:
            conversation_id: The conversation ID to delete

        Raises:
            KodeletError: If deletion fails
        """
        cmd = [
            str(self._binary),
            "conversation",
            "delete",
            conversation_id,
            "--no-confirm",
        ]

        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=self._cwd,
        )

        _, stderr = await process.communicate()

        if process.returncode != 0:
            raise KodeletError(f"Failed to delete conversation: {stderr.decode()}")

    async def fork(self, conversation_id: str | None = None) -> str:
        """Fork a conversation, creating a copy with reset usage statistics.

        Args:
            conversation_id: The conversation to fork (uses most recent if not specified)

        Returns:
            The new conversation ID

        Raises:
            KodeletError: If forking fails
        """
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
        for line in output.split("\n"):
            if "New ID:" in line:
                return line.split("New ID:")[1].strip()

        raise KodeletError("Could not parse new conversation ID from fork output")
