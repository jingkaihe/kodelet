"""Simple functional API for kodelet operations."""

import json
import subprocess
import time
from dataclasses import dataclass
from typing import Optional, Iterator, Dict, Any, List
import requests

# Global configuration
KODELET_BINARY = "kodelet"
API_HOST = "localhost"
API_PORT = 8080
BASE_URL = f"http://{API_HOST}:{API_PORT}"

# Global process reference for kodelet serve
_serve_process = None


@dataclass
class StreamEntry:
    """Represents a stream entry from kodelet headless/stream output."""
    kind: str  # "text" | "tool-use" | "tool-result" | "thinking"
    content: Optional[str] = None
    tool_name: Optional[str] = None
    input: Optional[str] = None
    result: Optional[str] = None
    role: Optional[str] = None  # "user" | "assistant" | "system"
    tool_call_id: Optional[str] = None
    conversation_id: Optional[str] = None


@dataclass
class KodeletConversation:
    """Represents a kodelet conversation."""
    id: str
    created_at: str
    updated_at: str
    provider: str
    message_count: int
    summary: Optional[str] = None
    messages: Optional[List[Dict[str, Any]]] = None


def run_headless_query(query: str, conversation_id: Optional[str] = None,
                      images: Optional[List[str]] = None) -> Iterator[StreamEntry]:
    """
    Run a kodelet query using headless mode for both new and resumed conversations.

    Args:
        query: The query to send to kodelet
        conversation_id: Optional conversation ID to resume
        images: Optional list of image paths

    Yields:
        StreamEntry objects as they arrive from the stream
    """
    cmd = [KODELET_BINARY, "run", "--headless"]

    # Add resume flag if conversation ID provided
    if conversation_id:
        cmd.extend(["--resume", conversation_id])

    # Add images if provided
    if images:
        for image in images:
            cmd.extend(["--image", image])

    # Add the query
    cmd.append(query)

    try:
        # Start kodelet in headless mode
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,  # Line buffered
            universal_newlines=True
        )

        # Read lines as they come in
        while True:
            line = process.stdout.readline()
            if not line:
                # Check if process has ended
                if process.poll() is not None:
                    break
                continue

            line = line.strip()
            if not line:
                continue

            try:
                # Parse JSON stream entry
                stream_data = json.loads(line)
                entry = StreamEntry(**stream_data)
                yield entry
            except json.JSONDecodeError as e:
                print(f"Failed to parse JSON: {line} - Error: {e}")
                continue
            except Exception as e:
                print(f"Error processing stream entry: {e}")
                continue

        # Wait for process to complete
        return_code = process.wait()
        if return_code != 0:
            stderr_output = process.stderr.read()
            if stderr_output.strip():
                yield StreamEntry(
                    kind="text",
                    role="assistant",
                    content=f"Error: {stderr_output.strip()}"
                )

    except Exception as e:
        yield StreamEntry(
            kind="text",
            role="assistant",
            content=f"Error running query: {str(e)}"
        )


def stream_conversation(conversation_id: str, include_history: bool = False) -> Iterator[StreamEntry]:
    """
    Stream updates from an existing conversation.

    Args:
        conversation_id: The conversation ID to stream
        include_history: Whether to include historical messages

    Yields:
        StreamEntry objects from the conversation stream
    """
    cmd = [KODELET_BINARY, "conversation", "stream", conversation_id]

    if include_history:
        cmd.append("--include-history")

    try:
        # Start streaming
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,  # Line buffered
            universal_newlines=True
        )

        # Read lines as they come in
        timeout_counter = 0
        max_timeout = 30  # seconds

        while timeout_counter < max_timeout:
            line = process.stdout.readline()

            if not line:
                # Check if process has ended
                if process.poll() is not None:
                    break
                # No data, wait a bit
                time.sleep(1)
                timeout_counter += 1
                continue

            # Reset timeout when we get data
            timeout_counter = 0

            line = line.strip()
            if not line:
                continue

            try:
                # Parse JSON stream entry
                stream_data = json.loads(line)
                entry = StreamEntry(**stream_data)
                yield entry
            except json.JSONDecodeError as e:
                print(f"Failed to parse JSON: {line} - Error: {e}")
                continue
            except Exception as e:
                print(f"Error processing stream entry: {e}")
                continue

        # Cleanup
        process.terminate()
        process.wait()

    except Exception as e:
        yield StreamEntry(
            kind="text",
            role="assistant",
            content=f"Error streaming conversation: {str(e)}"
        )


def start_serve() -> bool:
    """Start kodelet serve in background if not already running."""
    global _serve_process

    if _serve_process is not None:
        return True

    # Check if already running
    if is_serve_running():
        return True

    try:
        # Start kodelet serve in background
        _serve_process = subprocess.Popen(
            [KODELET_BINARY, "serve", "--host", API_HOST, "--port", str(API_PORT)],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )

        # Wait a bit for server to start
        time.sleep(2)

        # Verify it's running
        return is_serve_running()

    except Exception as e:
        print(f"Failed to start kodelet serve: {e}")
        return False


def stop_serve():
    """Stop the kodelet serve process."""
    global _serve_process
    if _serve_process:
        _serve_process.terminate()
        _serve_process.wait()
        _serve_process = None


def is_serve_running() -> bool:
    """Check if kodelet serve is running."""
    try:
        response = requests.get(f"{BASE_URL}/api/conversations", timeout=1)
        return response.status_code == 200
    except Exception:
        return False


def get_conversations(limit: int = 50, offset: int = 0,
                     search: Optional[str] = None) -> List[KodeletConversation]:
    """Get list of conversations from the API."""
    if not is_serve_running():
        return []

    try:
        params = {"limit": limit, "offset": offset}
        if search:
            params["search"] = search

        response = requests.get(f"{BASE_URL}/api/conversations", params=params, timeout=3)
        response.raise_for_status()

        data = response.json()
        conversations = []

        for conv_data in data.get("conversations", []):
            conv = KodeletConversation(
                id=conv_data["id"],
                created_at=conv_data["createdAt"],
                updated_at=conv_data["updatedAt"],
                provider=conv_data["provider"],
                message_count=conv_data["messageCount"],
                summary=conv_data.get("summary")
            )
            conversations.append(conv)

        return conversations

    except Exception as e:
        print(f"Error fetching conversations: {e}")
        return []


def get_conversation(conversation_id: str) -> Optional[KodeletConversation]:
    """Get detailed conversation with messages."""
    if not is_serve_running():
        return None

    try:
        response = requests.get(f"{BASE_URL}/api/conversations/{conversation_id}", timeout=3)
        response.raise_for_status()

        data = response.json()

        # Parse messages
        messages = []
        for msg_data in data.get("messages", []):
            message = {
                "role": msg_data["role"],
                "content": msg_data["content"],
                "tool_calls": msg_data.get("toolCalls", []),
                "thinking_text": msg_data.get("thinkingText")
            }
            messages.append(message)

        conv = KodeletConversation(
            id=data["id"],
            created_at=data["createdAt"],
            updated_at=data["updatedAt"],
            provider=data["provider"],
            message_count=data["messageCount"],
            summary=data.get("summary"),
            messages=messages
        )

        return conv

    except Exception as e:
        print(f"Error fetching conversation {conversation_id}: {e}")
        return None


def delete_conversation(conversation_id: str) -> bool:
    """Delete a conversation."""
    if not is_serve_running():
        return False

    try:
        response = requests.delete(f"{BASE_URL}/api/conversations/{conversation_id}")
        return response.status_code == 204
    except Exception as e:
        print(f"Error deleting conversation {conversation_id}: {e}")
        return False
