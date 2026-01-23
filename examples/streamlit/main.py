#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "streamlit>=1.45.0",
# ]
# ///
"""
Kodelet Streamlit Chatbot

A chatbot interface that replicates kodelet's interactive functionality
by shelling out to the kodelet CLI with streaming output.

Usage:
    uv run streamlit run main.py
"""

import json
import subprocess
from datetime import datetime
from pathlib import Path

import streamlit as st

KODELET_BIN = Path(__file__).parent.parent.parent / "bin" / "kodelet"

CUSTOM_CSS = """
<style>
@import url('https://fonts.googleapis.com/css2?family=Lora:wght@400;500;600&family=Poppins:wght@400;500;600;700&display=swap');

:root {
    --kodelet-dark: #141413;
    --kodelet-light: #faf9f5;
    --kodelet-mid-gray: #b0aea5;
    --kodelet-light-gray: #e8e6dc;
    --kodelet-orange: #d97757;
    --kodelet-blue: #6a9bcc;
    --kodelet-green: #788c5d;
}

.stApp {
    background-color: var(--kodelet-light);
}

h1, h2, h3 {
    font-family: 'Poppins', Arial, sans-serif !important;
    color: var(--kodelet-dark) !important;
}

.stMarkdown p, .stMarkdown li {
    font-family: 'Lora', Georgia, serif;
}

[data-testid="stChatMessage"] {
    background-color: white;
    border: 1px solid var(--kodelet-light-gray);
    border-radius: 8px;
    padding: 1rem !important;
}

[data-testid="stChatMessage"] * {
    border-color: transparent !important;
}

[data-testid="stChatMessage"] [data-testid="stExpander"] {
    border-color: var(--kodelet-light-gray) !important;
    border-radius: 6px !important;
}

code, pre {
    font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace !important;
}

.stButton > button {
    background-color: var(--kodelet-orange) !important;
    color: white !important;
    border: none !important;
    font-weight: 500 !important;
}

.stButton > button:hover {
    background-color: #c4644a !important;
}

[data-testid="stSidebar"] {
    background-color: var(--kodelet-light-gray) !important;
}

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
    """Find the kodelet binary, checking multiple locations."""
    if KODELET_BIN.exists():
        return str(KODELET_BIN)

    from shutil import which

    system_kodelet = which("kodelet")
    if system_kodelet:
        return system_kodelet

    st.error(
        f"Could not find kodelet binary. Tried:\n"
        f"- {KODELET_BIN}\n"
        f"- System PATH\n\n"
        f"Please build kodelet with `mise run build` or install it."
    )
    st.stop()


def load_conversation_history(conversation_id: str) -> list:
    """Load conversation history from kodelet using streaming format."""
    messages = []
    try:
        kodelet_path = find_kodelet_binary()
        result = subprocess.run(
            [kodelet_path, "conversation", "stream", conversation_id, "--history-only"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0:
            entries = []
            for line in result.stdout.strip().split("\n"):
                if not line:
                    continue
                try:
                    entries.append(json.loads(line))
                except json.JSONDecodeError:
                    continue
            
            current_role = None
            current_msg = None
            
            for entry in entries:
                kind = entry.get("kind", "")
                role = entry.get("role", "")
                
                # tool-result has role "user" but belongs to the assistant turn
                if kind == "tool-result":
                    if current_msg and current_msg["tools"]:
                        tool_call_id = entry.get("tool_call_id", "")
                        for tc in current_msg["tools"]:
                            if tc.get("call_id") == tool_call_id:
                                tc["result"] = entry.get("result", "")
                                break
                    continue
                
                if role != current_role:
                    if current_msg:
                        messages.append(current_msg)
                    current_msg = {
                        "role": role,
                        "content": "",
                        "thinking": "",
                        "tools": [],
                    }
                    current_role = role
                
                if kind == "text":
                    current_msg["content"] += entry.get("content", "")
                elif kind == "thinking":
                    current_msg["thinking"] += entry.get("content", "")
                elif kind == "tool-use":
                    current_msg["tools"].append({
                        "name": entry.get("tool_name", "unknown"),
                        "input": entry.get("input", "{}"),
                        "call_id": entry.get("tool_call_id", ""),
                    })
            
            if current_msg:
                messages.append(current_msg)
    except Exception:
        pass
    return messages


def get_conversation_summary(conversation_id: str) -> str:
    """Fetch conversation summary from kodelet."""
    try:
        kodelet_path = find_kodelet_binary()
        result = subprocess.run(
            [kodelet_path, "conversation", "show", conversation_id, "--format", "json", "--stats-only"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode == 0:
            data = json.loads(result.stdout)
            return data.get("summary", "")
    except Exception:
        pass
    return ""


def stream_kodelet_response(query: str, placeholder, conversation_id: str = None):
    """Stream kodelet response and update the placeholder in real-time."""
    kodelet_path = find_kodelet_binary()

    cmd = [kodelet_path, "run", "--headless", "--stream-deltas"]
    if conversation_id:
        cmd.extend(["--resume", conversation_id])
    cmd.append(query)

    full_text = ""
    thinking_text = ""
    in_thinking = False
    tool_calls = []
    conv_id = conversation_id

    try:
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )

        for line in process.stdout:
            line = line.strip()
            if not line:
                continue

            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue

            kind = event.get("kind", "")

            if not conv_id and "conversation_id" in event:
                conv_id = event["conversation_id"]

            if kind == "thinking-start":
                in_thinking = True
                thinking_text = ""
            elif kind == "thinking-delta":
                thinking_text += event.get("delta", "")
                _render_response(
                    placeholder, full_text, thinking_text, in_thinking, tool_calls
                )
            elif kind == "thinking-end":
                in_thinking = False
                _render_response(
                    placeholder, full_text, thinking_text, in_thinking, tool_calls
                )
            elif kind == "text-delta":
                full_text += event.get("delta", "")
                _render_response(
                    placeholder, full_text, thinking_text, in_thinking, tool_calls
                )
            elif kind == "tool-use":
                tool_name = event.get("tool_name", "unknown")
                tool_input = event.get("input", "{}")
                tool_call_id = event.get("tool_call_id", "")
                tool_calls.append({
                    "name": tool_name,
                    "input": tool_input,
                    "call_id": tool_call_id,
                })
                _render_response(
                    placeholder, full_text, thinking_text, in_thinking, tool_calls
                )
            elif kind == "tool-result":
                tool_call_id = event.get("tool_call_id", "")
                result = event.get("result", "")
                for tc in reversed(tool_calls):
                    if tc.get("call_id") == tool_call_id and "result" not in tc:
                        tc["result"] = result
                        break
                _render_response(
                    placeholder, full_text, thinking_text, in_thinking, tool_calls
                )

        process.wait()

        if process.returncode != 0:
            stderr = process.stderr.read()
            if stderr:
                st.error(f"Kodelet error: {stderr}")

    except FileNotFoundError:
        st.error(f"Could not execute kodelet at: {kodelet_path}")
    except Exception as e:
        st.error(f"Error running kodelet: {e}")

    return full_text, thinking_text, tool_calls, conv_id


def _render_response(
    placeholder, text: str, thinking: str, in_thinking: bool, tools: list
):
    """Render the current response state to the placeholder."""
    with placeholder.container():
        if thinking:
            label = "Thinking..." if in_thinking else "Thinking"
            with st.expander(label, expanded=in_thinking):
                st.text(thinking)

        if tools:
            with st.expander(f"Tools ({len(tools)})", expanded=False):
                for i, tc in enumerate(tools):
                    st.write(f"**{i+1}. {tc['name']}**")
                    try:
                        input_data = json.loads(tc["input"])
                        st.code(json.dumps(input_data, indent=2), language="json")
                    except json.JSONDecodeError:
                        st.code(tc["input"])
                    if "result" in tc:
                        st.caption("Result:")
                        st.code(tc["result"])

        if text:
            st.markdown(text)
        elif not thinking and not tools:
            st.empty()


def main():
    st.set_page_config(
        page_title="Kodelet Chat",
        page_icon="K",
        layout="wide",
    )

    st.markdown(CUSTOM_CSS, unsafe_allow_html=True)

    if "messages" not in st.session_state:
        st.session_state.messages = []
    if "conversation_id" not in st.session_state:
        st.session_state.conversation_id = None

    url_conv_id = st.query_params.get("c")
    if url_conv_id and st.session_state.conversation_id != url_conv_id:
        st.session_state.conversation_id = url_conv_id
        st.session_state.messages = load_conversation_history(url_conv_id)

    if st.session_state.conversation_id and not url_conv_id:
        st.query_params["c"] = st.session_state.conversation_id

    summary = ""
    if st.session_state.conversation_id:
        summary = get_conversation_summary(st.session_state.conversation_id)

    if summary:
        st.header(summary)
    else:
        hour = datetime.now().hour
        if hour < 12:
            greeting = "Good Morning"
        elif hour < 18:
            greeting = "Good Afternoon"
        else:
            greeting = "Good Evening"
        st.title(greeting)

    for msg in st.session_state.messages:
        with st.chat_message(msg["role"]):
            if msg["role"] == "assistant":
                if msg.get("thinking"):
                    with st.expander("Thinking", expanded=False):
                        st.text(msg["thinking"])
                if msg.get("tools"):
                    with st.expander(f"Tools ({len(msg['tools'])})", expanded=False):
                        for i, tc in enumerate(msg["tools"]):
                            st.write(f"**{i+1}. {tc['name']}**")
                            if "result" in tc:
                                st.code(tc["result"])
            st.markdown(msg["content"])

    if prompt := st.chat_input("Ask kodelet anything..."):
        st.session_state.messages.append({"role": "user", "content": prompt})

        with st.chat_message("user"):
            st.markdown(prompt)

        with st.chat_message("assistant"):
            placeholder = st.empty()
            text, thinking, tools, conv_id = stream_kodelet_response(
                prompt, placeholder, st.session_state.conversation_id
            )

            _render_response(placeholder, text, thinking, False, tools)

            if conv_id:
                st.session_state.conversation_id = conv_id
                st.query_params["c"] = conv_id

            st.session_state.messages.append(
                {
                    "role": "assistant",
                    "content": text or "No response received.",
                    "thinking": thinking,
                    "tools": tools,
                }
            )

    with st.sidebar:
        st.markdown('<div class="sidebar-header">About</div>', unsafe_allow_html=True)
        st.markdown(
            """
            A Streamlit interface for [kodelet](https://github.com/jingkaihe/kodelet).

            Follow-up messages continue the same conversation context.

            **Features**
            - Real-time streaming output
            - Conversation continuity
            - Thinking visualization
            - Tool call inspection
            """
        )

        if st.button("New Chat"):
            st.session_state.messages = []
            st.session_state.conversation_id = None
            st.query_params.clear()
            st.rerun()

        st.divider()
        if st.session_state.conversation_id:
            st.caption(f"ID: `{st.session_state.conversation_id}`")
        st.caption(f"Binary: `{find_kodelet_binary()}`")


if __name__ == "__main__":
    from streamlit.web import cli as stcli
    import sys
    if st.runtime.exists():
        main()
    else:
        sys.argv = ["streamlit", "run", __file__, "--server.headless", "true"]
        sys.exit(stcli.main())
