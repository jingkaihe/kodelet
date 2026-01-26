#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = [
#     "streamlit>=1.45.0",
# ]
# [tool.uv.sources]
# kodelet = { path = "../../sdk/python" }
# ///
"""
Kodelet Streamlit Chatbot

A chatbot interface that uses the kodelet Python SDK for streaming responses
with rich tool result visualizations.

Usage:
    uv run streamlit run main.py
"""

import asyncio
import difflib
import html
import json
import os
import sys
from datetime import datetime
from pathlib import Path

os.environ["STREAMLIT_THEME_BASE"] = "light"

# Add SDK to path for development
sys.path.insert(0, str(__file__.rsplit("/", 3)[0] + "/sdk/python/src"))

import streamlit as st
from kodelet import Kodelet, KodeletConfig
from kodelet.conversation import ConversationManager
from kodelet.exceptions import BinaryNotFoundError
from kodelet.tool_results import (
    BackgroundBashResult,
    BashResult,
    BlockedResult,
    CodeExecutionResult,
    CustomToolResult,
    FileEditResult,
    FileReadResult,
    FileWriteResult,
    GlobResult,
    GrepResult,
    ImageRecognitionResult,
    MCPToolResult,
    SkillResult,
    SubAgentResult,
    TodoResult,
    UnknownToolResult,
    ViewBackgroundProcessesResult,
    WebFetchResult,
    decode_tool_result,
)

# Language to Streamlit code language mapping
LANGUAGE_MAP = {
    "python": "python",
    "javascript": "javascript",
    "typescript": "typescript",
    "go": "go",
    "rust": "rust",
    "java": "java",
    "c": "c",
    "cpp": "cpp",
    "csharp": "csharp",
    "ruby": "ruby",
    "php": "php",
    "swift": "swift",
    "kotlin": "kotlin",
    "scala": "scala",
    "shell": "bash",
    "bash": "bash",
    "sql": "sql",
    "html": "html",
    "css": "css",
    "json": "json",
    "yaml": "yaml",
    "xml": "xml",
    "markdown": "markdown",
    "dockerfile": "dockerfile",
    "makefile": "makefile",
    "toml": "toml",
}

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

/* Tool Result Cards */
.tool-card {
    background-color: white;
    border: 1px solid var(--kodelet-light-gray);
    border-radius: 6px;
    padding: 12px 16px;
    margin: 8px 0;
    border-left: 3px solid var(--kodelet-mid-gray);
}

.tool-card.bash { border-left-color: var(--kodelet-green); }
.tool-card.file { border-left-color: var(--kodelet-blue); }
.tool-card.search { border-left-color: var(--kodelet-orange); }
.tool-card.error { border-left-color: #c44; }

.tool-header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
    font-family: 'Poppins', Arial, sans-serif;
    font-size: 0.9rem;
    font-weight: 500;
    color: var(--kodelet-dark);
}

.tool-meta {
    margin-left: auto;
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.75rem;
    color: var(--kodelet-mid-gray);
}

.exit-badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 4px;
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.75rem;
    font-weight: 600;
}

.exit-badge.success {
    background-color: rgba(120, 140, 93, 0.15);
    color: var(--kodelet-green);
}

.exit-badge.error {
    background-color: rgba(204, 68, 68, 0.15);
    color: #c44;
}

.file-path {
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.85rem;
    color: var(--kodelet-blue);
    background-color: rgba(106, 155, 204, 0.1);
    padding: 4px 8px;
    border-radius: 4px;
    display: inline-block;
    margin-top: 4px;
}

.terminal-output {
    background-color: var(--kodelet-dark);
    color: var(--kodelet-light);
    border-radius: 4px;
    padding: 12px;
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.85rem;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 300px;
    overflow-y: auto;
    margin-top: 8px;
}

.match-item {
    display: flex;
    gap: 12px;
    padding: 6px 0;
    border-bottom: 1px solid var(--kodelet-light-gray);
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.85rem;
}

.match-item:last-child {
    border-bottom: none;
}

.line-num {
    color: var(--kodelet-mid-gray);
    min-width: 40px;
    text-align: right;
}

.match-text {
    color: var(--kodelet-dark);
    flex: 1;
}

.match-highlight {
    background-color: rgba(217, 119, 87, 0.25);
    color: var(--kodelet-orange);
    padding: 0 2px;
    border-radius: 2px;
}

.file-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    background-color: var(--kodelet-light);
    border-radius: 4px;
    margin: 4px 0;
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.85rem;
}

.file-item-name {
    flex: 1;
    color: var(--kodelet-dark);
}

.file-item-meta {
    color: var(--kodelet-mid-gray);
    font-size: 0.75rem;
}

.todo-item {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 8px 12px;
    background-color: var(--kodelet-light);
    border-radius: 4px;
    margin: 4px 0;
}

.todo-content {
    flex: 1;
    font-family: 'Lora', Georgia, serif;
    color: var(--kodelet-dark);
}

.todo-priority {
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.7rem;
    padding: 2px 6px;
    border-radius: 3px;
    text-transform: uppercase;
}

.todo-priority.high {
    background-color: rgba(204, 68, 68, 0.15);
    color: #c44;
}

.todo-priority.medium {
    background-color: rgba(217, 119, 87, 0.15);
    color: var(--kodelet-orange);
}

.todo-priority.low {
    background-color: rgba(120, 140, 93, 0.15);
    color: var(--kodelet-green);
}

.stats-row {
    display: flex;
    gap: 16px;
    margin: 8px 0;
}

.stat-box {
    text-align: center;
    padding: 8px 16px;
    background-color: var(--kodelet-light);
    border-radius: 4px;
}

.stat-value {
    font-family: 'Poppins', Arial, sans-serif;
    font-size: 1.25rem;
    font-weight: 600;
    color: var(--kodelet-orange);
}

.stat-label {
    font-family: 'Monaco', 'Menlo', monospace;
    font-size: 0.7rem;
    color: var(--kodelet-mid-gray);
    text-transform: uppercase;
}

.blocked-banner {
    background-color: rgba(204, 68, 68, 0.1);
    border: 1px solid #c44;
    border-radius: 6px;
    padding: 12px 16px;
    display: flex;
    align-items: center;
    gap: 12px;
    color: #c44;
    font-family: 'Poppins', Arial, sans-serif;
}
</style>
"""


def format_file_size(size: int) -> str:
    """Format file size in human readable format."""
    for unit in ["B", "KB", "MB", "GB"]:
        if size < 1024:
            return f"{size:.1f} {unit}" if unit != "B" else f"{size} {unit}"
        size /= 1024
    return f"{size:.1f} TB"


def get_file_icon(file_type: str, language: str = "") -> str:
    """Get appropriate icon for file type."""
    if file_type == "directory":
        return "[dir]"
    return "[file]"


def render_bash_result(result: BashResult) -> None:
    """Render bash command result."""
    exit_class = "success" if result.exit_code == 0 else "error"

    st.markdown(
        f"""
        <div class="tool-card bash">
            <div class="tool-header">
                <span>Terminal</span>
                <span class="exit-badge {exit_class}">Exit {result.exit_code}</span>
                <span class="tool-meta">{result.execution_time_seconds:.2f}s</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    st.code(f"$ {result.command}", language="bash")

    if result.output.strip():
        st.markdown(
            f'<div class="terminal-output">{html.escape(result.output)}</div>',
            unsafe_allow_html=True,
        )


def render_background_bash_result(result: BackgroundBashResult) -> None:
    """Render background process result."""
    st.markdown(
        f"""
        <div class="tool-card bash">
            <div class="tool-header">
                <span>Background Process</span>
                <span class="tool-meta">PID: {result.pid}</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )
    st.code(f"$ {result.command}", language="bash")
    st.caption(f"Log: `{result.log_path}`")


def render_file_read_result(result: FileReadResult) -> None:
    """Render file read result with syntax highlighting."""
    lang = LANGUAGE_MAP.get(result.language, "text")
    truncated = " (truncated)" if result.truncated else ""
    icon = get_file_icon("file", result.language)

    st.markdown(
        f"""
        <div class="tool-card file">
            <div class="tool-header">
                <span>{icon} File Read{truncated}</span>
            </div>
            <div class="file-path">{result.file_path}</div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    if result.lines:
        content = "\n".join(result.lines)
        st.code(content, language=lang, line_numbers=True)

    if result.remaining_lines > 0:
        st.caption(f"{result.remaining_lines} more lines remaining")


def render_file_write_result(result: FileWriteResult) -> None:
    """Render file write result."""
    icon = get_file_icon("file", result.language)

    st.markdown(
        f"""
        <div class="tool-card file">
            <div class="tool-header">
                <span>{icon} File Written</span>
                <span class="tool-meta">{format_file_size(result.size)}</span>
            </div>
            <div class="file-path">{result.file_path}</div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    lang = LANGUAGE_MAP.get(result.language, "text")
    with st.expander("View Content", expanded=False):
        st.code(result.content, language=lang, line_numbers=True)


def render_file_edit_result(result: FileEditResult) -> None:
    """Render file edit result with unified diff view."""
    edit_count = len(result.edits)
    badge = (
        f"{result.replaced_count} replacements"
        if result.replace_all
        else f"{edit_count} edit(s)"
    )
    icon = get_file_icon("file", result.language)

    st.markdown(
        f"""
        <div class="tool-card file">
            <div class="tool-header">
                <span>{icon} File Edited</span>
                <span class="tool-meta">{badge}</span>
            </div>
            <div class="file-path">{result.file_path}</div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    for i, edit in enumerate(result.edits):
        with st.expander(
            f"Edit {i + 1}: Lines {edit.start_line}-{edit.end_line}", expanded=i == 0
        ):
            old_lines = edit.old_content.splitlines(keepends=True)
            new_lines = edit.new_content.splitlines(keepends=True)
            diff = difflib.unified_diff(
                old_lines,
                new_lines,
                fromfile="before",
                tofile="after",
                lineterm="",
            )
            diff_text = "".join(diff)
            if diff_text:
                st.code(diff_text, language="diff")
            else:
                st.caption("No changes")


def render_grep_result(result: GrepResult) -> None:
    """Render grep search results."""
    total_matches = sum(len(r.matches) for r in result.results)
    file_count = len(result.results)

    st.markdown(
        f"""
        <div class="tool-card search">
            <div class="tool-header">
                <span>Search Results</span>
                <span class="tool-meta">{total_matches} matches in {file_count} files</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    st.code(result.pattern, language="text")

    for file_result in result.results[:10]:
        with st.expander(
            f"{file_result.file_path} ({len(file_result.matches)} matches)"
        ):
            for match in file_result.matches[:20]:
                content = html.escape(match.content)
                if match.match_start < match.match_end:
                    before = html.escape(match.content[: match.match_start])
                    highlighted = html.escape(
                        match.content[match.match_start : match.match_end]
                    )
                    after = html.escape(match.content[match.match_end :])
                    content = f"{before}<span class='match-highlight'>{highlighted}</span>{after}"

                st.markdown(
                    f"""
                    <div class="match-item">
                        <span class="line-num">{match.line_number}</span>
                        <span class="match-text">{content}</span>
                    </div>
                    """,
                    unsafe_allow_html=True,
                )

    if result.truncated:
        st.caption("Results truncated")


def render_glob_result(result: GlobResult) -> None:
    """Render glob file list results."""
    st.markdown(
        f"""
        <div class="tool-card search">
            <div class="tool-header">
                <span>Files Found</span>
                <span class="tool-meta">{len(result.files)} items</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    st.code(result.pattern, language="text")

    for file_info in result.files[:50]:
        icon = get_file_icon(file_info.type, file_info.language)
        size = format_file_size(file_info.size) if file_info.type == "file" else ""
        st.markdown(
            f"""
            <div class="file-item">
                <span>{icon}</span>
                <span class="file-item-name">{file_info.path}</span>
                <span class="file-item-meta">{size}</span>
            </div>
            """,
            unsafe_allow_html=True,
        )

    if result.truncated:
        st.caption("Results truncated")


def render_todo_result(result: TodoResult) -> None:
    """Render todo list with status indicators."""
    st.markdown(
        f"""
        <div class="tool-card">
            <div class="tool-header">
                <span>Todo List</span>
                <span class="tool-meta">{result.action}</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    if result.statistics:
        stats = result.statistics
        st.markdown(
            f"""
            <div class="stats-row">
                <div class="stat-box">
                    <div class="stat-value">{stats.completed}</div>
                    <div class="stat-label">Completed</div>
                </div>
                <div class="stat-box">
                    <div class="stat-value">{stats.in_progress}</div>
                    <div class="stat-label">In Progress</div>
                </div>
                <div class="stat-box">
                    <div class="stat-value">{stats.pending}</div>
                    <div class="stat-label">Pending</div>
                </div>
            </div>
            """,
            unsafe_allow_html=True,
        )

    status_labels = {
        "completed": "[done]",
        "in_progress": "[...]",
        "pending": "[   ]",
        "canceled": "[x]",
    }

    for todo in result.todo_list:
        label = status_labels.get(todo.status, "[?]")
        priority_class = todo.priority.lower() if todo.priority else "low"
        st.markdown(
            f"""
            <div class="todo-item">
                <span class="tool-meta">{label}</span>
                <span class="todo-content">{html.escape(todo.content)}</span>
                <span class="todo-priority {priority_class}">{todo.priority}</span>
            </div>
            """,
            unsafe_allow_html=True,
        )


def render_web_fetch_result(result: WebFetchResult) -> None:
    """Render web fetch result."""
    st.markdown(
        f"""
        <div class="tool-card">
            <div class="tool-header">
                <span>Web Fetch</span>
                <span class="tool-meta">{format_file_size(result.size)}</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    st.markdown(f"**URL:** [{result.url}]({result.url})")
    st.caption(
        f"Content-Type: `{result.content_type}` | Mode: `{result.processed_type}`"
    )

    if result.content:
        with st.expander("View Content", expanded=False):
            if "json" in result.content_type:
                st.code(result.content[:5000], language="json")
            elif "html" in result.content_type or "markdown" in result.processed_type:
                st.markdown(result.content[:3000])
            else:
                st.code(result.content[:3000])


def render_image_recognition_result(result: ImageRecognitionResult) -> None:
    """Render image recognition result."""
    st.markdown(
        f"""
        <div class="tool-card">
            <div class="tool-header">
                <span>Image Analysis</span>
                <span class="tool-meta">{result.image_type}</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    st.caption(f"**Image:** `{result.image_path}`")
    if result.image_size:
        st.caption(
            f"**Dimensions:** {result.image_size.width} × {result.image_size.height}"
        )

    st.markdown(f"**Prompt:** {result.prompt}")
    st.markdown(result.analysis)


def render_subagent_result(result: SubAgentResult) -> None:
    """Render subagent result."""
    st.markdown(
        """
        <div class="tool-card">
            <div class="tool-header">
                <span>Sub-Agent</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    st.markdown(f"**Question:** {result.question}")
    with st.expander("Response", expanded=True):
        st.markdown(result.response)


def render_code_execution_result(result: CodeExecutionResult) -> None:
    """Render code execution result."""
    st.markdown(
        f"""
        <div class="tool-card bash">
            <div class="tool-header">
                <span>Code Execution</span>
                <span class="tool-meta">{result.runtime}</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    with st.expander("Code", expanded=False):
        st.code(result.code, language="typescript")

    if result.output:
        st.markdown(
            f'<div class="terminal-output">{html.escape(result.output)}</div>',
            unsafe_allow_html=True,
        )


def render_skill_result(result: SkillResult) -> None:
    """Render skill invocation result."""
    st.markdown(
        f"""
        <div class="tool-card">
            <div class="tool-header">
                <span>Skill: {result.skill_name}</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )
    st.caption(f"Directory: `{result.directory}`")


def render_mcp_tool_result(result: MCPToolResult) -> None:
    """Render MCP tool result."""
    st.markdown(
        f"""
        <div class="tool-card">
            <div class="tool-header">
                <span>MCP: {result.mcp_tool_name}</span>
                <span class="tool-meta">{result.execution_time_seconds:.2f}s</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    if result.server_name:
        st.caption(f"Server: `{result.server_name}`")

    if result.content_text:
        st.markdown(result.content_text)


def render_view_background_processes_result(
    result: ViewBackgroundProcessesResult,
) -> None:
    """Render background processes list."""
    st.markdown(
        f"""
        <div class="tool-card bash">
            <div class="tool-header">
                <span>Background Processes</span>
                <span class="tool-meta">{result.count} running</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )

    for proc in result.processes:
        status_label = "[running]" if proc.status == "running" else "[stopped]"
        st.markdown(
            f"""
            <div class="file-item">
                <span class="tool-meta">{status_label}</span>
                <span class="file-item-name"><code>{html.escape(proc.command)}</code></span>
                <span class="file-item-meta">PID: {proc.pid}</span>
            </div>
            """,
            unsafe_allow_html=True,
        )


def render_blocked_result(result: BlockedResult) -> None:
    """Render blocked tool result."""
    st.markdown(
        f"""
        <div class="blocked-banner">
            <div>
                <strong>Tool Blocked:</strong> {result.tool_name}<br/>
                <small>{html.escape(result.reason)}</small>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )


def render_custom_tool_result(result: CustomToolResult) -> None:
    """Render custom tool result."""
    st.markdown(
        f"""
        <div class="tool-card">
            <div class="tool-header">
                <span>Custom Tool</span>
                <span class="tool-meta">{result.execution_time_seconds:.2f}s</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )
    st.code(result.output)


def render_unknown_result(result: UnknownToolResult, raw_result: str) -> None:
    """Render unknown tool result."""
    st.markdown(
        f"""
        <div class="tool-card">
            <div class="tool-header">
                <span>{result.tool_name}</span>
            </div>
        </div>
        """,
        unsafe_allow_html=True,
    )
    st.code(raw_result)


def render_tool_result(tool_name: str, raw_result: str) -> None:
    """Decode and render a tool result with appropriate visualization."""
    try:
        data = json.loads(raw_result)
        if isinstance(data, dict) and "toolName" in data:
            decoded = decode_tool_result(data)

            match decoded:
                case BashResult():
                    render_bash_result(decoded)
                case BackgroundBashResult():
                    render_background_bash_result(decoded)
                case FileReadResult():
                    render_file_read_result(decoded)
                case FileWriteResult():
                    render_file_write_result(decoded)
                case FileEditResult():
                    render_file_edit_result(decoded)
                case GrepResult():
                    render_grep_result(decoded)
                case GlobResult():
                    render_glob_result(decoded)
                case TodoResult():
                    render_todo_result(decoded)
                case WebFetchResult():
                    render_web_fetch_result(decoded)
                case ImageRecognitionResult():
                    render_image_recognition_result(decoded)
                case SubAgentResult():
                    render_subagent_result(decoded)
                case CodeExecutionResult():
                    render_code_execution_result(decoded)
                case SkillResult():
                    render_skill_result(decoded)
                case MCPToolResult():
                    render_mcp_tool_result(decoded)
                case ViewBackgroundProcessesResult():
                    render_view_background_processes_result(decoded)
                case BlockedResult():
                    render_blocked_result(decoded)
                case CustomToolResult():
                    render_custom_tool_result(decoded)
                case UnknownToolResult():
                    render_unknown_result(decoded, raw_result)
                case _:
                    st.code(raw_result)
        else:
            st.code(raw_result)
    except (json.JSONDecodeError, TypeError):
        st.code(raw_result)


def _get_local_binary_path() -> Path | None:
    """Get path to local kodelet binary if it exists."""
    # Try ../../bin/kodelet relative to this script
    script_dir = Path(__file__).parent
    local_binary = script_dir / ".." / ".." / "bin" / "kodelet"
    if local_binary.exists():
        return local_binary.resolve()
    return None


def get_kodelet_client(conversation_id: str | None = None) -> Kodelet:
    """Create a Kodelet client, optionally resuming a conversation."""
    config = KodeletConfig(
        stream_deltas=True,
        kodelet_path=_get_local_binary_path(),
    )
    return Kodelet(config=config, resume=conversation_id)


def get_conversation_manager() -> ConversationManager:
    """Get a ConversationManager instance."""
    try:
        config = KodeletConfig(kodelet_path=_get_local_binary_path())
        client = Kodelet(config=config)
        return client.conversations
    except BinaryNotFoundError as e:
        st.error(str(e))
        st.stop()


def parse_history_events(events: list[dict]) -> list[dict]:
    """Parse streaming history events into message format for display.

    Groups events by role AND turn number, so multiple assistant turns
    become separate message blocks.
    """
    messages = []
    current_key = None  # (role, turn) tuple for grouping
    current_msg = None
    # Track tool results to match with tool calls
    tool_results = {}

    # First pass: collect all tool results by call_id
    for entry in events:
        if entry.get("kind") == "tool-result":
            call_id = entry.get("tool_call_id", "")
            if call_id:
                tool_results[call_id] = entry.get("result", "")

    # Second pass: build messages grouped by (role, turn)
    for entry in events:
        kind = entry.get("kind", "")
        role = entry.get("role", "")
        turn = entry.get("turn", 0)

        # Skip tool-result (handled separately)
        if kind == "tool-result":
            continue

        # For user messages, turn is 0 - use a unique key per user message
        if role == "user":
            # User messages don't have turns, group by role only
            # But start a new message for each user text
            if kind == "text":
                if current_msg:
                    messages.append(current_msg)
                current_msg = {
                    "role": role,
                    "content": entry.get("content", ""),
                    "thinking": "",
                    "tools": [],
                }
                current_key = (role, id(entry))  # Unique key per user message
            continue

        # For assistant messages, group by turn
        msg_key = (role, turn)

        if msg_key != current_key:
            if current_msg:
                messages.append(current_msg)
            current_msg = {
                "role": role,
                "content": "",
                "thinking": "",
                "tools": [],
            }
            current_key = msg_key

        if kind == "text":
            current_msg["content"] += entry.get("content", "")
        elif kind == "thinking":
            current_msg["thinking"] += entry.get("content", "")
        elif kind == "tool-use":
            call_id = entry.get("tool_call_id", "")
            # Avoid duplicate tool entries
            existing_ids = {tc.get("call_id") for tc in current_msg["tools"]}
            if call_id not in existing_ids:
                tool_entry = {
                    "name": entry.get("tool_name", "unknown"),
                    "input": entry.get("input", "{}"),
                    "call_id": call_id,
                }
                # Attach result if we have it
                if call_id in tool_results:
                    tool_entry["result"] = tool_results[call_id]
                current_msg["tools"].append(tool_entry)

    if current_msg:
        messages.append(current_msg)

    return messages


def load_conversation_history(conversation_id: str) -> list[dict]:
    """Load conversation history using the SDK."""
    try:
        manager = get_conversation_manager()
        events = asyncio.run(manager.stream_history(conversation_id))
        return parse_history_events(events)
    except Exception:
        return []


def get_conversation_summary(conversation_id: str) -> str:
    """Fetch conversation summary using the SDK."""
    try:
        manager = get_conversation_manager()
        return asyncio.run(manager.get_summary(conversation_id))
    except Exception:
        return ""


async def stream_response_async(
    query: str, placeholder, conversation_id: str | None = None
) -> tuple[str, str, list[dict], str | None]:
    """Stream kodelet response using the SDK.

    Tracks multiple turns using turn numbers from events.
    Each turn can have thinking, tool calls, and text.
    """
    client = get_kodelet_client(conversation_id)

    # Turns indexed by turn number (1-indexed from backend)
    turns: dict[int, dict] = {}
    current_turn_num = 0
    conv_id = conversation_id

    def get_or_create_turn(turn_num: int) -> dict:
        """Get or create a turn dict for the given turn number."""
        if turn_num not in turns:
            turns[turn_num] = {
                "thinking": "",
                "tools": [],
                "text": "",
                "in_thinking": False,
            }
        return turns[turn_num]

    def get_turns_list() -> list[dict]:
        """Get turns as a sorted list for rendering."""
        if not turns:
            return []
        return [turns[k] for k in sorted(turns.keys())]

    try:
        async for event in client.query(query):
            if not conv_id and event.conversation_id:
                conv_id = event.conversation_id

            # Get turn number from event (default to 1 if not set)
            event_turn = getattr(event, "turn", 0)
            turn_num = event_turn if event_turn > 0 else max(current_turn_num, 1)

            match event.kind:
                case "thinking-start":
                    turn = get_or_create_turn(turn_num)
                    turn["in_thinking"] = True
                    current_turn_num = turn_num
                case "thinking-delta":
                    turn = get_or_create_turn(turn_num)
                    turn["thinking"] += event.delta
                    _render_response(placeholder, get_turns_list(), current_turn_num)
                case "thinking-end":
                    turn = get_or_create_turn(turn_num)
                    turn["in_thinking"] = False
                    _render_response(placeholder, get_turns_list(), current_turn_num)
                case "text-delta":
                    turn = get_or_create_turn(turn_num)
                    turn["text"] += event.delta
                    _render_response(placeholder, get_turns_list(), current_turn_num)
                case "tool-use":
                    turn = get_or_create_turn(turn_num)
                    # Avoid duplicate tool entries
                    existing_ids = {tc.get("call_id") for tc in turn["tools"]}
                    if event.tool_call_id not in existing_ids:
                        turn["tools"].append(
                            {
                                "name": event.tool_name,
                                "input": event.input,
                                "call_id": event.tool_call_id,
                            }
                        )
                        _render_response(
                            placeholder, get_turns_list(), current_turn_num
                        )
                case "tool-result":
                    # Find tool by call_id across all turns and attach result
                    for t in turns.values():
                        for tc in t["tools"]:
                            if (
                                tc.get("call_id") == event.tool_call_id
                                and "result" not in tc
                            ):
                                tc["result"] = event.result
                                _render_response(
                                    placeholder, get_turns_list(), current_turn_num
                                )
                                break
                        else:
                            continue
                        break  # Exit outer loop when found

    except BinaryNotFoundError as e:
        st.error(str(e))
    except Exception as e:
        st.error(f"Error running kodelet: {e}")

    # Collect final results for return value compatibility
    all_tools = []
    all_thinking = ""
    all_text = ""
    for turn in get_turns_list():
        if turn["thinking"]:
            all_thinking += turn["thinking"] + "\n"
        all_tools.extend(turn["tools"])
        all_text += turn["text"]

    return all_text, all_thinking.strip(), all_tools, conv_id


def stream_kodelet_response(
    query: str, placeholder, conversation_id: str | None = None
) -> tuple[str, str, list[dict], str | None]:
    """Stream kodelet response (sync wrapper for async)."""
    return asyncio.run(stream_response_async(query, placeholder, conversation_id))


def _get_thinking_preview(thinking: str, max_len: int = 80) -> str:
    """Get a short preview of thinking content."""
    first_line = thinking.strip().split("\n")[0]
    if len(first_line) > max_len:
        return first_line[:max_len] + "..."
    return first_line


def _get_tools_summary(tools: list[dict]) -> str:
    """Get a summary of tool names used."""
    if not tools:
        return ""
    names = [tc.get("name", "unknown") for tc in tools]
    # Deduplicate while preserving order
    seen = set()
    unique_names = [n for n in names if not (n in seen or seen.add(n))]
    if len(unique_names) <= 3:
        return ", ".join(unique_names)
    return f"{', '.join(unique_names[:3])} +{len(unique_names) - 3} more"


def _get_tool_input_summary(
    tool_name: str, input_json: str, has_result: bool = False
) -> str | None:
    """Extract the most relevant info from a tool's input.

    Returns a concise summary string, None to show full JSON, or "" to skip input display.
    When has_result is True, returns "" for tools whose result already shows the key info.
    """
    try:
        data = json.loads(input_json)
    except json.JSONDecodeError:
        return None

    match tool_name:
        # These tools' results already display command/path/pattern - skip input
        case (
            "Bash"
            | "File_read"
            | "File_write"
            | "File_edit"
            | "Grep_tool"
            | "Glob_tool"
        ):
            return "" if has_result else None
        case "Subagent":
            # Result shows the response, but question is useful context
            question = data.get("question", "")
            if len(question) > 200:
                return question[:200] + "..."
            return question
        case "Web_fetch":
            return "" if has_result else None
        case "Image_recognition":
            return "" if has_result else None
        case "Todo_read":
            return "" if has_result else "(read current todos)"
        case "Todo_write":
            return "" if has_result else f"({len(data.get('todos', []))} items)"
        case "Skill":
            return "" if has_result else data.get("skill_name", "")
        case "View_background_processes":
            return "" if has_result else "(list processes)"
        case _:
            return None  # Show full JSON for unknown tools


def _render_turn(turn: dict, turn_idx: int | None = None):
    """Render a single turn's thinking, tools, and text."""
    suffix = f" (Turn {turn_idx})" if turn_idx is not None else ""

    # Render thinking with preview in header
    if turn["thinking"]:
        is_active = turn.get("in_thinking", False)
        if is_active:
            label = "Thinking..."
        else:
            preview = _get_thinking_preview(turn["thinking"])
            label = f"Thinking: {preview}"
        with st.expander(label + suffix, expanded=is_active):
            st.text(turn["thinking"])

    # Render tools with summary in header
    if turn["tools"]:
        summary = _get_tools_summary(turn["tools"])
        label = f"Tools: {summary}" if summary else f"Tools ({len(turn['tools'])})"
        with st.expander(label + suffix, expanded=False):
            for i, tc in enumerate(turn["tools"]):
                has_result = "result" in tc
                st.write(f"**{i + 1}. {tc['name']}**")
                # Show concise input summary when possible (skip if result shows same info)
                input_summary = _get_tool_input_summary(
                    tc["name"], tc.get("input", "{}"), has_result
                )
                if input_summary:  # Non-empty string
                    st.code(
                        input_summary,
                        language="bash" if tc["name"] == "Bash" else "text",
                    )
                elif input_summary is None:  # None = show full JSON
                    try:
                        input_data = json.loads(tc["input"])
                        st.code(json.dumps(input_data, indent=2), language="json")
                    except json.JSONDecodeError:
                        st.code(tc["input"])
                # Empty string = skip input display entirely
                if has_result:
                    render_tool_result(tc["name"], tc["result"])

    # Render text output inline (always visible)
    if turn["text"]:
        st.markdown(turn["text"])


def _render_response(placeholder, turns: list[dict], current_turn_num: int):
    """Render all turns to the placeholder."""
    with placeholder.container():
        if not turns:
            st.empty()
            return

        # Show turn numbers only if there are multiple turns
        show_turn_numbers = len(turns) > 1

        for i, turn in enumerate(turns):
            turn_idx = (i + 1) if show_turn_numbers else None
            _render_turn(turn, turn_idx)


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
                # Render as a turn for consistent UX
                turn_data = {
                    "thinking": msg.get("thinking", ""),
                    "tools": msg.get("tools", []),
                    "text": msg.get("content", ""),
                    "in_thinking": False,
                }
                _render_turn(turn_data)
            else:
                st.markdown(msg.get("content", ""))

    if prompt := st.chat_input("Ask kodelet anything..."):
        st.session_state.messages.append({"role": "user", "content": prompt})

        with st.chat_message("user"):
            st.markdown(prompt)

        with st.chat_message("assistant"):
            placeholder = st.empty()
            text, thinking, tools, conv_id = stream_kodelet_response(
                prompt, placeholder, st.session_state.conversation_id
            )

            # Final render is handled by streaming - just ensure we have content
            if not text and not thinking and not tools:
                placeholder.markdown("No response received.")

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
            - Rich tool result display
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
        st.caption("Using: kodelet Python SDK")


if __name__ == "__main__":
    from streamlit.web import cli as stcli

    if st.runtime.exists():
        main()
    else:
        sys.argv = ["streamlit", "run", __file__, "--server.headless", "true"]
        sys.exit(stcli.main())
