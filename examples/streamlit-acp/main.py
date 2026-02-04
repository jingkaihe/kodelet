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
import json
import os
import sys
from dataclasses import dataclass, field
from datetime import datetime
from shutil import which
from typing import Any

os.environ["STREAMLIT_THEME_BASE"] = "light"

import streamlit as st
from acp import (
    PROTOCOL_VERSION,
    Client,
    RequestError,
    spawn_agent_process,
    text_block,
)
from acp.schema import (
    AgentMessageChunk,
    AgentPlanUpdate,
    AgentThoughtChunk,
    AvailableCommandsUpdate,
    CreateTerminalResponse,
    CurrentModeUpdate,
    EnvVariable,
    KillTerminalCommandResponse,
    PermissionOption,
    ReadTextFileResponse,
    ReleaseTerminalResponse,
    RequestPermissionResponse,
    SessionInfoUpdate,
    TerminalOutputResponse,
    TextContentBlock,
    ToolCall,
    ToolCallProgress,
    ToolCallStart,
    UserMessageChunk,
    WaitForTerminalExitResponse,
    WriteTextFileResponse,
)

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
</style>
"""


def find_kodelet_binary() -> str:
    """Find the kodelet binary in PATH."""
    if path := which("kodelet"):
        return path
    st.error("Could not find `kodelet` in PATH. Please install it first.")
    st.stop()


@dataclass
class ResponseState:
    """State for tracking the current response."""

    thinking: str = ""
    tool_calls: list[dict] = field(default_factory=list)
    message: str = ""
    session_id: str | None = None

    # Mode: "off" (ignore), "history" (record to session_state), "live" (render to UI)
    mode: str = "off"

    # For history reconstruction - accumulates current assistant message
    _current_assistant: dict = field(
        default_factory=lambda: {"role": "assistant", "content": "", "thinking": "", "tools": []}
    )

    # UI placeholders - created lazily in live mode
    thinking_placeholder: Any = None
    tools_placeholder: Any = None
    message_placeholder: Any = None
    container: Any = None


class StreamlitACPClient(Client):
    """ACP Client that routes updates based on mode: history vs live."""

    def __init__(self, state: ResponseState):
        self.state = state

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

    async def session_update(
        self,
        session_id: str,
        update: UserMessageChunk | AgentMessageChunk | AgentThoughtChunk | ToolCallStart | ToolCallProgress | AgentPlanUpdate | AvailableCommandsUpdate | CurrentModeUpdate | SessionInfoUpdate,
        **kwargs: Any,
    ) -> None:
        if self.state.mode == "off":
            return
        if self.state.mode == "history":
            self._record_history(update)
            return
        # Live mode - render to UI
        self._render_live(update)

    def _record_history(self, update):
        """Record events during load_session to st.session_state.messages."""
        if isinstance(update, UserMessageChunk):
            # New user message = new turn, save previous assistant if any
            if self.state._current_assistant["content"] or self.state._current_assistant["thinking"]:
                st.session_state.messages.append(self.state._current_assistant)
                self.state._current_assistant = {"role": "assistant", "content": "", "thinking": "", "tools": []}
            content = update.content
            if isinstance(content, TextContentBlock):
                st.session_state.messages.append({"role": "user", "content": content.text})

        elif isinstance(update, AgentThoughtChunk):
            content = update.content
            if isinstance(content, TextContentBlock):
                self.state._current_assistant["thinking"] += content.text

        elif isinstance(update, AgentMessageChunk):
            content = update.content
            if isinstance(content, TextContentBlock):
                self.state._current_assistant["content"] += content.text

        elif isinstance(update, ToolCallStart):
            self.state._current_assistant["tools"].append({
                "id": update.tool_call_id,
                "title": update.title,
                "status": "completed",
                "output": update.raw_output,
            })

        elif isinstance(update, ToolCallProgress):
            for tc in self.state._current_assistant["tools"]:
                if tc["id"] == update.tool_call_id and update.raw_output:
                    tc["output"] = update.raw_output
                    break

    def _render_live(self, update):
        """Render updates to UI placeholders during prompt."""
        if isinstance(update, AgentThoughtChunk):
            content = update.content
            if isinstance(content, TextContentBlock):
                self.state.thinking += content.text
                self._render_thinking()

        elif isinstance(update, AgentMessageChunk):
            content = update.content
            if isinstance(content, TextContentBlock):
                self.state.message += content.text
                self._render_message()

        elif isinstance(update, ToolCallStart):
            self.state.tool_calls.append({
                "id": update.tool_call_id,
                "title": update.title,
                "status": update.status,
                "kind": update.kind,
                "input": update.raw_input,
                "output": update.raw_output,
            })
            self._render_tools()

        elif isinstance(update, ToolCallProgress):
            for tc in self.state.tool_calls:
                if tc["id"] == update.tool_call_id:
                    if update.status:
                        tc["status"] = update.status
                    if update.raw_output:
                        tc["output"] = update.raw_output
                    break
            self._render_tools()

    def _render_thinking(self):
        if self.state.thinking_placeholder is None:
            self.state.thinking_placeholder = self.state.container.empty()
        with self.state.thinking_placeholder.container():
            with st.expander("Thinking...", expanded=True):
                st.text(self.state.thinking)

    def _render_tools(self):
        if self.state.tools_placeholder is None:
            self.state.tools_placeholder = self.state.container.empty()
        with self.state.tools_placeholder.container():
            with st.expander(f"Tools ({len(self.state.tool_calls)})", expanded=False):
                for i, tc in enumerate(self.state.tool_calls):
                    status_icon = "⏳" if tc.get("status") == "running" else "✓" if tc.get("status") == "completed" else "•"
                    st.write(f"**{i + 1}. {status_icon} {tc['title']}**")
                    if tc.get("input"):
                        try:
                            st.code(json.dumps(tc["input"], indent=2), language="json")
                        except (TypeError, ValueError):
                            st.code(str(tc["input"]))
                    if tc.get("output"):
                        st.caption("Result:")
                        output = tc["output"]
                        st.code(json.dumps(output, indent=2) if isinstance(output, dict) else str(output))

    def _render_message(self):
        if self.state.message_placeholder is None:
            self.state.message_placeholder = self.state.container.empty()
        self.state.message_placeholder.markdown(self.state.message)


def _finalize_assistant(state: ResponseState):
    """Append any pending assistant message to session state."""
    if state._current_assistant["content"] or state._current_assistant["thinking"]:
        st.session_state.messages.append(state._current_assistant)
        state._current_assistant = {"role": "assistant", "content": "", "thinking": "", "tools": []}


async def load_session_history(session_id: str) -> bool:
    """Load session history on page load. Returns True if successful."""
    kodelet_path = find_kodelet_binary()
    state = ResponseState(mode="history")
    client = StreamlitACPClient(state)

    try:
        async with spawn_agent_process(client, kodelet_path, "acp") as (conn, _):
            await conn.initialize(protocol_version=PROTOCOL_VERSION, client_capabilities=None)
            resp = await conn.load_session(session_id=session_id, cwd=os.getcwd(), mcp_servers=[])
            if resp is None:
                return False
            _finalize_assistant(state)
            return True
    except Exception:
        return False


async def run_acp_prompt(query: str, container: Any, session_id: str | None = None) -> ResponseState:
    """Run a prompt via ACP and stream results."""
    kodelet_path = find_kodelet_binary()
    state = ResponseState(container=container)
    client = StreamlitACPClient(state)

    try:
        async with spawn_agent_process(client, kodelet_path, "acp") as (conn, _):
            await conn.initialize(protocol_version=PROTOCOL_VERSION, client_capabilities=None)

            if session_id:
                state.mode = "history"
                resp = await conn.load_session(session_id=session_id, cwd=os.getcwd(), mcp_servers=[])
                state.session_id = session_id if resp else None
                if resp is None:
                    session = await conn.new_session(cwd=os.getcwd(), mcp_servers=[])
                    state.session_id = session.session_id
            else:
                session = await conn.new_session(cwd=os.getcwd(), mcp_servers=[])
                state.session_id = session.session_id

            _finalize_assistant(state)
            state.mode = "live"
            await conn.prompt(session_id=state.session_id, prompt=[text_block(query)])

    except Exception as e:
        st.error(f"ACP error: {e}")

    return state


def render_history_message(msg: dict):
    """Render a historical assistant message."""
    if msg.get("thinking"):
        with st.expander("Thinking", expanded=False):
            st.text(msg["thinking"])
    if msg.get("tools"):
        with st.expander(f"Tools ({len(msg['tools'])})", expanded=False):
            for i, tc in enumerate(msg["tools"]):
                st.write(f"**{i + 1}. ✓ {tc['title']}**")
                if tc.get("output"):
                    output = tc["output"]
                    st.code(json.dumps(output, indent=2) if isinstance(output, dict) else str(output))
    st.markdown(msg["content"])


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
    if st.session_state.session_id and not url_session_id:
        st.query_params["c"] = st.session_state.session_id

    # Load history on page refresh with ?c=session_id
    if st.session_state.session_id and not st.session_state.messages:
        with st.spinner("Loading conversation history..."):
            if not asyncio.run(load_session_history(st.session_state.session_id)):
                st.session_state.session_id = None
                st.query_params.clear()

    # Greeting
    hour = datetime.now().hour
    greeting = "Good Morning" if hour < 12 else "Good Afternoon" if hour < 18 else "Good Evening"
    st.title(greeting)

    # Render message history
    for msg in st.session_state.messages:
        with st.chat_message(msg["role"]):
            if msg["role"] == "assistant":
                render_history_message(msg)
            else:
                st.markdown(msg["content"])

    # Handle new input
    if prompt := st.chat_input("Ask kodelet anything..."):
        with st.chat_message("user"):
            st.markdown(prompt)

        with st.chat_message("assistant"):
            container = st.container()
            state = asyncio.run(run_acp_prompt(prompt, container, st.session_state.session_id))

        if state.session_id:
            st.session_state.session_id = state.session_id
            st.query_params["c"] = state.session_id

        st.session_state.messages.append({"role": "user", "content": prompt})
        st.session_state.messages.append({
            "role": "assistant",
            "content": state.message or "No response received.",
            "thinking": state.thinking,
            "tools": state.tool_calls,
        })
        st.rerun()

    # Sidebar
    with st.sidebar:
        st.markdown('<div class="sidebar-header">About</div>', unsafe_allow_html=True)
        st.markdown("""
A Streamlit interface for [kodelet](https://github.com/jingkaihe/kodelet)
using the **Agent Client Protocol (ACP)**.

**Features**
- Session continuity via `?c=session_id`
- Real-time streaming
- Thinking visualization
- Tool call inspection
        """)

        if st.button("New Chat"):
            st.session_state.messages = []
            st.session_state.session_id = None
            st.query_params.clear()
            st.rerun()

        st.divider()
        if st.session_state.session_id:
            st.caption(f"Session: `{st.session_state.session_id[:8]}...`")
        st.caption(f"Binary: `{find_kodelet_binary()}`")


if __name__ == "__main__":
    from streamlit.web import cli as stcli

    if st.runtime.exists():
        main()
    else:
        sys.argv = ["streamlit", "run", __file__, "--server.headless", "true"]
        sys.exit(stcli.main())
