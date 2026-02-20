#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "streamlit>=1.45.0",
#     "agent-client-protocol>=0.7.0",
# ]
# ///
"""
Kodelet Streamlit Chatbot (ACP)

A chatbot interface that communicates with kodelet via the Agent Client Protocol (ACP).

Usage:
    uv run main.py
"""

import asyncio
import base64
import json
import os
import subprocess
import sys
from datetime import datetime
from shutil import which
from typing import Any, cast

import streamlit as st_module  # type: ignore[import-untyped]
from acp import (  # type: ignore[import-not-found]
    PROTOCOL_VERSION,
    Client,
    RequestError,
    image_block,
    spawn_agent_process,
    text_block,
)
from acp.schema import (  # type: ignore[import-not-found]
    AgentMessageChunk,
    AgentThoughtChunk,
    CreateTerminalResponse,
    EnvVariable,
    KillTerminalCommandResponse,
    PermissionOption,
    ReadTextFileResponse,
    ReleaseTerminalResponse,
    RequestPermissionResponse,
    TerminalOutputResponse,
    TextContentBlock,
    ToolCall,
    ToolCallProgress,
    ToolCallStart,
    WaitForTerminalExitResponse,
    WriteTextFileResponse,
)

os.environ["STREAMLIT_THEME_BASE"] = "light"
st: Any = cast(Any, st_module)

CUSTOM_CSS = """
<style>
@import url('https://fonts.googleapis.com/css2?family=Lora:wght@400;500;600&family=Poppins:wght@400;500;600;700&display=swap');

:root {
    --kodelet-dark: #141413;
    --kodelet-light: #faf9f5;
    --kodelet-mid-gray: #b0aea5;
    --kodelet-light-gray: #e8e6dc;
    --kodelet-orange: #d97757;
}

.stApp { background-color: var(--kodelet-light); }
h1, h2, h3 { font-family: 'Poppins', Arial, sans-serif !important; color: var(--kodelet-dark) !important; }
.stMarkdown p, .stMarkdown li { font-family: 'Lora', Georgia, serif; }

[data-testid="stChatMessage"] {
    background-color: white;
    border: 1px solid var(--kodelet-light-gray);
    border-radius: 8px;
    padding: 1rem !important;
}
[data-testid="stChatMessage"] * { border-color: transparent !important; }
[data-testid="stChatMessage"] [data-testid="stExpander"] {
    border-color: var(--kodelet-light-gray) !important;
    border-radius: 6px !important;
}

code, pre { font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace !important; }

.stButton > button {
    background-color: var(--kodelet-orange) !important;
    color: white !important;
    border: none !important;
    font-weight: 500 !important;
}
.stButton > button:hover { background-color: #c4644a !important; }

[data-testid="stSidebar"] { background-color: var(--kodelet-light-gray) !important; }
.sidebar-header {
    color: var(--kodelet-dark);
    font-family: 'Poppins', Arial, sans-serif;
    font-weight: 600;
    border-bottom: 2px solid var(--kodelet-orange);
    padding-bottom: 8px;
    margin-bottom: 16px;
}

.chat-list { list-style: none; padding: 0; margin: 0; }
.chat-item {
    padding: 10px 12px;
    margin: 4px 0;
    border-radius: 6px;
    cursor: pointer;
    transition: background 0.15s ease;
    border-left: 3px solid transparent;
}
.chat-item:hover { background: rgba(217, 119, 87, 0.1); }
.chat-item.active {
    background: rgba(217, 119, 87, 0.15);
    border-left-color: var(--kodelet-orange);
}
.chat-item a {
    text-decoration: none;
    color: inherit;
    display: block;
}
.chat-preview {
    font-family: 'Lora', Georgia, serif;
    font-size: 0.9rem;
    color: var(--kodelet-dark);
    line-height: 1.4;
    margin-bottom: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.chat-date {
    font-size: 0.75rem;
    color: var(--kodelet-mid-gray);
}
</style>
"""


def find_kodelet_binary() -> str:
    """Find the kodelet binary in PATH."""
    if path := which("kodelet"):
        return path
    st.error("Could not find `kodelet` in PATH. Please install it first.")
    st.stop()
    raise SystemExit  # Unreachable, but satisfies type checker


def load_conversations(limit: int = 20) -> list[dict]:
    """Load recent conversations from kodelet."""
    try:
        result = subprocess.run(
            [find_kodelet_binary(), "conversation", "list", "--limit", str(limit), "--json"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode == 0:
            data = json.loads(result.stdout)
            return data.get("conversations", [])
    except (subprocess.TimeoutExpired, json.JSONDecodeError, FileNotFoundError):
        pass
    return []


class ACPClient(Client):
    """ACP Client that streams responses to a Streamlit placeholder."""

    def __init__(self):
        self.blocks: list[dict] = []
        self.placeholder: Any = None
        self.streaming = False
        self.tool_state: dict[str, dict[str, Any]] = {}

    def start_streaming(self):
        """Reset accumulators and enable streaming."""
        self.blocks = []
        self.tool_state = {}
        self.streaming = True

    def _find_tool_entry(self, tool_call_id: str) -> dict | None:
        for block in reversed(self.blocks):
            if block["type"] != "tools":
                continue
            for tc in block["items"]:
                if tc["id"] == tool_call_id:
                    return tc
        return None

    # Required ACP client methods (not implemented for this example)
    async def request_permission(self, options: list[PermissionOption], session_id: str, tool_call: ToolCall, **kwargs: Any) -> RequestPermissionResponse:
        raise RequestError.method_not_found("session/request_permission")

    async def write_text_file(self, content: str, path: str, session_id: str, **kwargs: Any) -> WriteTextFileResponse | None:
        raise RequestError.method_not_found("fs/write_text_file")

    async def read_text_file(self, path: str, session_id: str, limit: int | None = None, line: int | None = None, **kwargs: Any) -> ReadTextFileResponse:
        raise RequestError.method_not_found("fs/read_text_file")

    async def create_terminal(self, command: str, session_id: str, args: list[str] | None = None, cwd: str | None = None, env: list[EnvVariable] | None = None, output_byte_limit: int | None = None, **kwargs: Any) -> CreateTerminalResponse:
        raise RequestError.method_not_found("terminal/create")

    async def terminal_output(self, session_id: str, terminal_id: str, **kwargs: Any) -> TerminalOutputResponse:
        raise RequestError.method_not_found("terminal/output")

    async def release_terminal(self, session_id: str, terminal_id: str, **kwargs: Any) -> ReleaseTerminalResponse | None:
        raise RequestError.method_not_found("terminal/release")

    async def wait_for_terminal_exit(self, session_id: str, terminal_id: str, **kwargs: Any) -> WaitForTerminalExitResponse:
        raise RequestError.method_not_found("terminal/wait_for_exit")

    async def kill_terminal(self, session_id: str, terminal_id: str, **kwargs: Any) -> KillTerminalCommandResponse | None:
        raise RequestError.method_not_found("terminal/kill")

    async def ext_method(self, method: str, params: dict) -> dict:
        raise RequestError.method_not_found(method)

    async def ext_notification(self, method: str, params: dict) -> None:
        pass

    def on_connect(self, conn: Any) -> None:
        pass

    async def session_update(self, session_id: str, update: Any, **kwargs: Any) -> None:
        if not self.streaming:
            return

        if isinstance(update, AgentThoughtChunk) and isinstance(update.content, TextContentBlock):
            if self.blocks and self.blocks[-1]["type"] == "thinking":
                self.blocks[-1]["content"] += update.content.text
            else:
                self.blocks.append({"type": "thinking", "content": update.content.text})
            self._render()

        elif isinstance(update, AgentMessageChunk) and isinstance(update.content, TextContentBlock):
            if self.blocks and self.blocks[-1]["type"] == "message":
                self.blocks[-1]["content"] += update.content.text
            else:
                self.blocks.append({"type": "message", "content": update.content.text})
            self._render()

        elif isinstance(update, ToolCallStart):
            existing = self._find_tool_entry(update.tool_call_id)
            if existing:
                existing["title"] = update.title or existing.get("title")
                existing["status"] = update.status if update.status is not None else existing.get("status")
                if update.raw_output is not None:
                    existing["output"] = update.raw_output
                self.tool_state[update.tool_call_id] = dict(existing)
            else:
                tool_entry = {
                    "id": update.tool_call_id,
                    "title": update.title,
                    "status": update.status,
                    "output": update.raw_output,
                }
                self.tool_state[update.tool_call_id] = dict(tool_entry)
                self.blocks.append({"type": "tools", "items": [tool_entry]})
            self._render()

        elif isinstance(update, ToolCallProgress):
            previous = self.tool_state.get(update.tool_call_id, {"id": update.tool_call_id, "title": update.tool_call_id})
            tool_entry = {
                "id": update.tool_call_id,
                "title": getattr(update, "title", None) or previous.get("title") or update.tool_call_id,
                "status": update.status if getattr(update, "status", None) is not None else previous.get("status"),
                "output": update.raw_output if update.raw_output is not None else previous.get("output"),
            }
            self.tool_state[update.tool_call_id] = dict(tool_entry)
            existing = self._find_tool_entry(update.tool_call_id)
            if existing:
                existing.update(tool_entry)
            else:
                self.blocks.append({"type": "tools", "items": [tool_entry]})
            self._render()

    def _render(self):
        """Render current state to the placeholder, preserving turn order."""
        if not self.placeholder:
            return
        with self.placeholder.container():
            for i, block in enumerate(self.blocks):
                if block["type"] == "thinking":
                    has_later_content = any(b["type"] != "thinking" for b in self.blocks[i + 1:])
                    label = "Thinking" if has_later_content else "Thinking..."
                    with st.expander(label, expanded=not has_later_content):
                        st.markdown(block["content"])
                elif block["type"] == "tools":
                    items = block["items"]
                    with st.expander(f"Tools ({len(items)})", expanded=False):
                        for j, tc in enumerate(items):
                            if tc.get("status") in ("completed",):
                                icon = "✓"
                            elif tc.get("status") == "failed":
                                icon = "✗"
                            else:
                                icon = "⏳"
                            st.write(f"**{j + 1}. {icon} {tc['title']}**")
                            if tc.get("output"):
                                output = tc["output"]
                                st.code(json.dumps(output, indent=2) if isinstance(output, dict) else str(output))
                elif block["type"] == "message":
                    st.markdown(block["content"])

    def get_result(self) -> dict:
        """Return accumulated result as a message dict with ordered blocks."""
        return {"role": "assistant", "blocks": self.blocks}


# 50MB buffer limit for large conversation history
ACP_BUFFER_LIMIT = 50 * 1024 * 1024


def supports_load_session(init_response: Any) -> bool:
    agent_capabilities = getattr(init_response, "agent_capabilities", None)
    if agent_capabilities is None:
        agent_capabilities = getattr(init_response, "agentCapabilities", None)
    if agent_capabilities is None:
        return False
    if isinstance(agent_capabilities, dict):
        return bool(agent_capabilities.get("load_session") or agent_capabilities.get("loadSession"))
    return bool(
        getattr(agent_capabilities, "load_session", None)
        or getattr(agent_capabilities, "loadSession", None)
    )


async def run_acp_prompt(
    query: str,
    placeholder: Any,
    session_id: str | None = None,
    images: list[Any] | None = None,
) -> tuple[dict, str | None]:
    """Run a prompt via ACP and stream results. Returns (result_dict, session_id)."""
    kodelet_path = find_kodelet_binary()
    client = ACPClient()
    client.placeholder = placeholder
    result_session_id = session_id

    try:
        async with spawn_agent_process(client, kodelet_path, "acp", transport_kwargs={"limit": ACP_BUFFER_LIMIT}) as (conn, _):
            init_resp = await conn.initialize(protocol_version=PROTOCOL_VERSION, client_capabilities={})
            can_load_session = supports_load_session(init_resp)

            # Load or create session
            if session_id and can_load_session:
                try:
                    await conn.load_session(session_id=session_id, cwd=os.getcwd(), mcp_servers=[])
                    await asyncio.sleep(0)  # Yield to let history callbacks complete
                    result_session_id = session_id
                except Exception:
                    session = await conn.new_session(cwd=os.getcwd(), mcp_servers=[])
                    result_session_id = session.session_id
            else:
                session = await conn.new_session(cwd=os.getcwd(), mcp_servers=[])
                result_session_id = session.session_id

            # Build prompt and stream response
            client.start_streaming()
            prompt_blocks: list[Any] = []
            if images:
                for img in images:
                    img_data = base64.b64encode(img.read()).decode("utf-8")
                    prompt_blocks.append(image_block(img_data, img.type))
            if query:
                prompt_blocks.append(text_block(query))
            await conn.prompt(session_id=result_session_id, prompt=prompt_blocks)

    except Exception as e:
        st.error(f"ACP error: {e}")

    return client.get_result(), result_session_id


def render_assistant_message(msg: dict):
    """Render an assistant message (for history), preserving turn order."""
    if blocks := msg.get("blocks"):
        for block in blocks:
            if block["type"] == "thinking":
                with st.expander("Thinking", expanded=False):
                    st.markdown(block["content"])
            elif block["type"] == "tools":
                items = block["items"]
                with st.expander(f"Tools ({len(items)})", expanded=False):
                    for i, tc in enumerate(items):
                        title = tc.get("title") or tc.get("name", "Tool")
                        if tc.get("status") in ("completed",):
                            icon = "✓"
                        elif tc.get("status") == "failed":
                            icon = "✗"
                        else:
                            icon = "⏳"
                        st.write(f"**{i + 1}. {icon} {title}**")
                        if tc.get("output") or tc.get("result"):
                            output = tc.get("output") or tc.get("result")
                            st.code(json.dumps(output, indent=2) if isinstance(output, dict) else str(output))
            elif block["type"] == "message":
                st.markdown(block["content"])
        return
    # Legacy flat format fallback
    if msg.get("thinking"):
        with st.expander("Thinking", expanded=False):
            st.markdown(msg["thinking"])
    if msg.get("tools"):
        with st.expander(f"Tools ({len(msg['tools'])})", expanded=False):
            for i, tc in enumerate(msg["tools"]):
                title = tc.get("title") or tc.get("name", "Tool")
                st.write(f"**{i + 1}. ✓ {title}**")
                if tc.get("output") or tc.get("result"):
                    output = tc.get("output") or tc.get("result")
                    st.code(json.dumps(output, indent=2) if isinstance(output, dict) else str(output))
    st.markdown(msg.get("content", ""))


async def load_history_via_acp(session_id: str) -> list[dict]:
    """Load conversation history via ACP session/load."""
    kodelet_path = find_kodelet_binary()
    messages: list[dict] = []
    current_msg: dict | None = None
    current_tool_state: dict[str, dict[str, Any]] = {}

    class HistoryClient(Client):
        """Minimal client that records history from session/load."""

        async def request_permission(self, *args: Any, **kwargs: Any) -> RequestPermissionResponse:
            raise RequestError.method_not_found("session/request_permission")

        async def write_text_file(self, *args: Any, **kwargs: Any) -> WriteTextFileResponse | None:
            raise RequestError.method_not_found("fs/write_text_file")

        async def read_text_file(self, *args: Any, **kwargs: Any) -> ReadTextFileResponse:
            raise RequestError.method_not_found("fs/read_text_file")

        async def create_terminal(self, *args: Any, **kwargs: Any) -> CreateTerminalResponse:
            raise RequestError.method_not_found("terminal/create")

        async def terminal_output(self, *args: Any, **kwargs: Any) -> TerminalOutputResponse:
            raise RequestError.method_not_found("terminal/output")

        async def release_terminal(self, *args: Any, **kwargs: Any) -> ReleaseTerminalResponse | None:
            raise RequestError.method_not_found("terminal/release")

        async def wait_for_terminal_exit(self, *args: Any, **kwargs: Any) -> WaitForTerminalExitResponse:
            raise RequestError.method_not_found("terminal/wait_for_exit")

        async def kill_terminal(self, *args: Any, **kwargs: Any) -> KillTerminalCommandResponse | None:
            raise RequestError.method_not_found("terminal/kill")

        async def ext_method(self, method: str, params: dict) -> dict:
            raise RequestError.method_not_found(method)

        async def ext_notification(self, method: str, params: dict) -> None:
            pass

        def on_connect(self, conn: Any) -> None:
            pass

        async def session_update(self, session_id: str, update: Any, **kwargs: Any) -> None:
            nonlocal current_msg

            from acp.schema import UserMessageChunk  # type: ignore[import-not-found]

            # User message = new turn
            if isinstance(update, UserMessageChunk) and isinstance(update.content, TextContentBlock):
                if current_msg:
                    messages.append(current_msg)
                current_msg = {"role": "user", "content": update.content.text}
                current_tool_state = {}
                return

            # Agent content - ensure we have an assistant message with blocks
            if current_msg is None or current_msg["role"] != "assistant":
                if current_msg:
                    messages.append(current_msg)
                current_msg = {"role": "assistant", "blocks": []}

            blocks = current_msg["blocks"]

            if isinstance(update, AgentThoughtChunk) and isinstance(update.content, TextContentBlock):
                if blocks and blocks[-1]["type"] == "thinking":
                    blocks[-1]["content"] += update.content.text
                else:
                    blocks.append({"type": "thinking", "content": update.content.text})
            elif isinstance(update, AgentMessageChunk) and isinstance(update.content, TextContentBlock):
                if blocks and blocks[-1]["type"] == "message":
                    blocks[-1]["content"] += update.content.text
                else:
                    blocks.append({"type": "message", "content": update.content.text})
            elif isinstance(update, ToolCallStart):
                tool_entry = {
                    "id": update.tool_call_id,
                    "title": update.title,
                    "status": getattr(update, "status", None),
                    "output": update.raw_output,
                }
                current_tool_state[update.tool_call_id] = dict(tool_entry)
                existing = None
                for blk in reversed(blocks):
                    if blk["type"] != "tools":
                        continue
                    for tc in blk["items"]:
                        if tc["id"] == update.tool_call_id:
                            existing = tc
                            break
                    if existing:
                        break
                if existing:
                    existing.update(tool_entry)
                else:
                    blocks.append({"type": "tools", "items": [tool_entry]})
            elif isinstance(update, ToolCallProgress):
                previous = current_tool_state.get(update.tool_call_id, {"id": update.tool_call_id, "title": update.tool_call_id})
                tool_entry = {
                    "id": update.tool_call_id,
                    "title": getattr(update, "title", None) or previous.get("title") or update.tool_call_id,
                    "status": update.status if getattr(update, "status", None) is not None else previous.get("status"),
                    "output": update.raw_output if update.raw_output is not None else previous.get("output"),
                }
                current_tool_state[update.tool_call_id] = dict(tool_entry)
                existing = None
                for blk in reversed(blocks):
                    if blk["type"] != "tools":
                        continue
                    for tc in blk["items"]:
                        if tc["id"] == update.tool_call_id:
                            existing = tc
                            break
                    if existing:
                        break
                if existing:
                    existing.update(tool_entry)
                else:
                    blocks.append({"type": "tools", "items": [tool_entry]})

    try:
        async with spawn_agent_process(HistoryClient(), kodelet_path, "acp", transport_kwargs={"limit": ACP_BUFFER_LIMIT}) as (conn, _):
            init_resp = await conn.initialize(protocol_version=PROTOCOL_VERSION, client_capabilities={})
            if not supports_load_session(init_resp):
                return []
            await conn.load_session(session_id=session_id, cwd=os.getcwd(), mcp_servers=[])
            await asyncio.sleep(0)  # Yield to ensure all callbacks complete
            if current_msg:
                messages.append(current_msg)
    except Exception:
        return []

    return messages


def main():
    st.set_page_config(page_title="Kodelet Chat (ACP)", page_icon="K", layout="wide")
    st.markdown(CUSTOM_CSS, unsafe_allow_html=True)

    if "messages" not in st.session_state:
        st.session_state.messages = []
    if "session_id" not in st.session_state:
        st.session_state.session_id = None

    # Sync session_id with URL parameter ?c=...
    url_session_id = st.query_params.get("c")
    if url_session_id and st.session_state.session_id != url_session_id:
        st.session_state.session_id = url_session_id
        st.session_state.messages = asyncio.run(load_history_via_acp(url_session_id))
    if st.session_state.session_id and not url_session_id:
        st.query_params["c"] = st.session_state.session_id

    # Greeting
    hour = datetime.now().hour
    greeting = "Good Morning" if hour < 12 else "Good Afternoon" if hour < 18 else "Good Evening"
    st.title(greeting)

    # Render message history
    for msg in st.session_state.messages:
        with st.chat_message(msg["role"]):
            if msg["role"] == "assistant":
                render_assistant_message(msg)
            else:
                if msg.get("images"):
                    for img in msg["images"]:
                        st.image(base64.b64decode(img["data"]))
                if msg.get("content"):
                    st.markdown(msg["content"])

    # Handle new input
    if prompt := st.chat_input("Ask kodelet anything...", accept_file="multiple", file_type=["png", "jpg", "jpeg", "gif", "webp"]):
        text = prompt.text if hasattr(prompt, "text") else str(prompt)
        files = prompt.files if hasattr(prompt, "files") else []

        with st.chat_message("user"):
            for f in files:
                st.image(f)
            if text:
                st.markdown(text)

        with st.chat_message("assistant"):
            placeholder = st.empty()
            result, new_session_id = asyncio.run(run_acp_prompt(text, placeholder, st.session_state.session_id, images=files if files else None))

        if new_session_id:
            st.session_state.session_id = new_session_id
            st.query_params["c"] = new_session_id

        # Store messages in session state
        user_msg: dict[str, Any] = {"role": "user", "content": text}
        if files:
            user_msg["images"] = [{"data": base64.b64encode(f.getvalue()).decode("utf-8"), "type": f.type} for f in files]
        st.session_state.messages.append(user_msg)
        st.session_state.messages.append(result)

    # Sidebar
    with st.sidebar:
        if st.button("✨ New Chat", use_container_width=True):
            st.session_state.messages = []
            st.session_state.session_id = None
            st.query_params.clear()
            st.rerun()

        st.divider()
        st.markdown('<div class="sidebar-header">Recent Chats</div>', unsafe_allow_html=True)

        conversations = load_conversations(limit=15)
        chat_items = []
        for convo in conversations:
            cid = convo.get("id", "")
            preview = convo.get("preview", "No preview")[:50]
            if len(convo.get("preview", "")) > 50:
                preview += "..."
            date = convo.get("updated_at", "")[:10]
            is_active = "active" if cid == st.session_state.session_id else ""
            chat_items.append(
                f'<li class="chat-item {is_active}">'
                f'<a href="?c={cid}" target="_top">'
                f'<div class="chat-preview">{preview}</div>'
                f'<div class="chat-date">{date}</div>'
                f'</a></li>'
            )
        st.markdown(f'<ul class="chat-list">{"".join(chat_items)}</ul>', unsafe_allow_html=True)


if __name__ == "__main__":
    from streamlit.web import cli as stcli  # type: ignore[import-not-found]

    if st.runtime.exists():
        main()
    else:
        sys.argv = ["streamlit", "run", __file__, "--server.headless", "true"]
        sys.exit(stcli.main())
