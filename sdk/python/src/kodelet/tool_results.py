"""Structured tool result types for Kodelet SDK.

These dataclasses represent the structured metadata returned by kodelet's builtin tools.
Use `ToolResultEvent.decode_result()` to parse tool results into typed objects.

Example:
    ```python
    async for event in agent.query("list files"):
        if isinstance(event, ToolResultEvent):
            result = event.decode_result()
            if isinstance(result, BashResult):
                print(f"Command: {result.command}")
                print(f"Exit code: {result.exit_code}")
    ```
"""

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any


@dataclass
class Edit:
    """A single text replacement in a file edit operation."""

    start_line: int
    end_line: int
    old_content: str
    new_content: str


@dataclass
class SearchMatch:
    """A single match in a search result."""

    line_number: int
    content: str
    match_start: int
    match_end: int
    is_context: bool = False


@dataclass
class SearchResult:
    """Search results for a single file."""

    file_path: str
    matches: list[SearchMatch] = field(default_factory=list)
    language: str = ""


@dataclass
class FileInfo:
    """Information about a matched file."""

    path: str
    size: int
    mod_time: datetime | None
    type: str  # "file" or "directory"
    language: str = ""


@dataclass
class TodoItem:
    """A single todo list item."""

    id: str
    content: str
    status: str
    priority: str
    created_at: datetime | None = None
    updated_at: datetime | None = None


@dataclass
class TodoStats:
    """Statistics about the todo list."""

    total: int = 0
    completed: int = 0
    in_progress: int = 0
    pending: int = 0


@dataclass
class ImageDimensions:
    """Dimensions of an image."""

    width: int
    height: int


@dataclass
class BackgroundProcessInfo:
    """Information about a single background process."""

    pid: int
    command: str
    log_path: str
    start_time: datetime | None
    status: str  # "running" or "stopped"


@dataclass
class MCPContent:
    """A content block returned by an MCP tool."""

    type: str
    text: str = ""
    data: str = ""
    mime_type: str = ""
    uri: str = ""
    metadata: dict[str, Any] = field(default_factory=dict)


# Tool result dataclasses


@dataclass
class BashResult:
    """Result of a bash command execution."""

    command: str
    exit_code: int
    output: str
    execution_time_ns: int
    working_dir: str = ""

    @property
    def execution_time_seconds(self) -> float:
        """Execution time in seconds."""
        return self.execution_time_ns / 1_000_000_000


@dataclass
class BackgroundBashResult:
    """Result of a background bash command execution."""

    command: str
    pid: int
    log_path: str
    start_time: datetime | None


@dataclass
class FileReadResult:
    """Result of a file read operation."""

    file_path: str
    offset: int
    line_limit: int
    lines: list[str] = field(default_factory=list)
    language: str = ""
    truncated: bool = False
    remaining_lines: int = 0


@dataclass
class FileWriteResult:
    """Result of a file write operation."""

    file_path: str
    content: str
    size: int
    language: str = ""


@dataclass
class FileEditResult:
    """Result of a file edit operation."""

    file_path: str
    edits: list[Edit] = field(default_factory=list)
    language: str = ""
    replace_all: bool = False
    replaced_count: int = 0


@dataclass
class GrepResult:
    """Result of a grep search operation."""

    pattern: str
    results: list[SearchResult] = field(default_factory=list)
    path: str = ""
    include: str = ""
    truncated: bool = False


@dataclass
class GlobResult:
    """Result of a glob pattern match operation."""

    pattern: str
    files: list[FileInfo] = field(default_factory=list)
    path: str = ""
    truncated: bool = False


@dataclass
class TodoResult:
    """Result of a todo list operation."""

    action: str  # "read" or "write"
    todo_list: list[TodoItem] = field(default_factory=list)
    statistics: TodoStats | None = None


@dataclass
class ImageRecognitionResult:
    """Result of an image recognition operation."""

    image_path: str
    image_type: str  # "local" or "remote"
    prompt: str
    analysis: str
    image_size: ImageDimensions | None = None


@dataclass
class SubAgentResult:
    """Result of a sub-agent invocation."""

    question: str
    response: str


@dataclass
class WebFetchResult:
    """Result of a web fetch operation."""

    url: str
    content_type: str
    size: int
    content: str
    processed_type: str  # "saved", "markdown", "ai_extracted"
    saved_path: str = ""
    prompt: str = ""


@dataclass
class ViewBackgroundProcessesResult:
    """Result of viewing background processes."""

    processes: list[BackgroundProcessInfo] = field(default_factory=list)
    count: int = 0


@dataclass
class CodeExecutionResult:
    """Result of a code execution operation."""

    code: str
    output: str
    runtime: str


@dataclass
class SkillResult:
    """Result of a skill invocation."""

    skill_name: str
    directory: str


@dataclass
class MCPToolResult:
    """Result of an MCP tool execution."""

    mcp_tool_name: str
    content_text: str
    content: list[MCPContent] = field(default_factory=list)
    server_name: str = ""
    parameters: dict[str, Any] = field(default_factory=dict)
    execution_time_ns: int = 0

    @property
    def execution_time_seconds(self) -> float:
        """Execution time in seconds."""
        return self.execution_time_ns / 1_000_000_000


@dataclass
class CustomToolResult:
    """Result of a custom tool execution."""

    output: str
    execution_time_ns: int = 0

    @property
    def execution_time_seconds(self) -> float:
        """Execution time in seconds."""
        return self.execution_time_ns / 1_000_000_000


@dataclass
class BlockedResult:
    """Result when a tool was blocked by a security hook."""

    tool_name: str
    reason: str


@dataclass
class UnknownToolResult:
    """Result for unknown or unrecognized tool types."""

    tool_name: str
    success: bool
    error: str = ""
    raw_metadata: dict[str, Any] = field(default_factory=dict)


# Type alias for all tool result types
ToolResult = (
    BashResult
    | BackgroundBashResult
    | FileReadResult
    | FileWriteResult
    | FileEditResult
    | GrepResult
    | GlobResult
    | TodoResult
    | ImageRecognitionResult
    | SubAgentResult
    | WebFetchResult
    | ViewBackgroundProcessesResult
    | CodeExecutionResult
    | SkillResult
    | MCPToolResult
    | CustomToolResult
    | BlockedResult
    | UnknownToolResult
)


def _parse_datetime(value: str | None) -> datetime | None:
    """Parse an ISO datetime string, handling various formats."""
    if not value:
        return None
    try:
        # Handle Go's RFC3339 format with Z suffix
        if value.endswith("Z"):
            value = value[:-1] + "+00:00"
        return datetime.fromisoformat(value)
    except (ValueError, TypeError):
        return None


def _parse_edit(data: dict[str, Any]) -> Edit:
    """Parse an Edit from JSON data."""
    return Edit(
        start_line=data.get("startLine", 0),
        end_line=data.get("endLine", 0),
        old_content=data.get("oldContent", ""),
        new_content=data.get("newContent", ""),
    )


def _parse_search_match(data: dict[str, Any]) -> SearchMatch:
    """Parse a SearchMatch from JSON data."""
    return SearchMatch(
        line_number=data.get("lineNumber", 0),
        content=data.get("content", ""),
        match_start=data.get("matchStart", 0),
        match_end=data.get("matchEnd", 0),
        is_context=data.get("isContext", False),
    )


def _parse_search_result(data: dict[str, Any]) -> SearchResult:
    """Parse a SearchResult from JSON data."""
    return SearchResult(
        file_path=data.get("filePath", ""),
        language=data.get("language", ""),
        matches=[_parse_search_match(m) for m in data.get("matches", [])],
    )


def _parse_file_info(data: dict[str, Any]) -> FileInfo:
    """Parse a FileInfo from JSON data."""
    return FileInfo(
        path=data.get("path", ""),
        size=data.get("size", 0),
        mod_time=_parse_datetime(data.get("modTime")),
        type=data.get("type", ""),
        language=data.get("language", ""),
    )


def _parse_todo_item(data: dict[str, Any]) -> TodoItem:
    """Parse a TodoItem from JSON data."""
    return TodoItem(
        id=data.get("id", ""),
        content=data.get("content", ""),
        status=data.get("status", ""),
        priority=data.get("priority", ""),
        created_at=_parse_datetime(data.get("createdAt")),
        updated_at=_parse_datetime(data.get("updatedAt")),
    )


def _parse_todo_stats(data: dict[str, Any] | None) -> TodoStats | None:
    """Parse TodoStats from JSON data."""
    if not data:
        return None
    return TodoStats(
        total=data.get("total", 0),
        completed=data.get("completed", 0),
        in_progress=data.get("inProgress", 0),
        pending=data.get("pending", 0),
    )


def _parse_image_dimensions(data: dict[str, Any] | None) -> ImageDimensions | None:
    """Parse ImageDimensions from JSON data."""
    if not data:
        return None
    return ImageDimensions(
        width=data.get("width", 0),
        height=data.get("height", 0),
    )


def _parse_background_process_info(data: dict[str, Any]) -> BackgroundProcessInfo:
    """Parse a BackgroundProcessInfo from JSON data."""
    return BackgroundProcessInfo(
        pid=data.get("pid", 0),
        command=data.get("command", ""),
        log_path=data.get("logPath", ""),
        start_time=_parse_datetime(data.get("startTime")),
        status=data.get("status", ""),
    )


def _parse_mcp_content(data: dict[str, Any]) -> MCPContent:
    """Parse an MCPContent from JSON data."""
    return MCPContent(
        type=data.get("type", ""),
        text=data.get("text", ""),
        data=data.get("data", ""),
        mime_type=data.get("mimeType", ""),
        uri=data.get("uri", ""),
        metadata=data.get("metadata", {}),
    )


def decode_tool_result(structured_data: dict[str, Any]) -> ToolResult:
    """Decode a structured tool result into a typed dataclass.

    Args:
        structured_data: The structured tool result JSON (from StructuredToolResult)

    Returns:
        A typed tool result dataclass

    Example:
        ```python
        import json
        data = json.loads(event.result)
        result = decode_tool_result(data)
        if isinstance(result, BashResult):
            print(f"Exit code: {result.exit_code}")
        ```
    """
    tool_name = structured_data.get("toolName", "")
    success = structured_data.get("success", False)
    error = structured_data.get("error", "")
    metadata_type = structured_data.get("metadataType", "")
    metadata = structured_data.get("metadata", {}) or {}

    match metadata_type:
        case "bash":
            return BashResult(
                command=metadata.get("command", ""),
                exit_code=metadata.get("exitCode", 0),
                output=metadata.get("output", ""),
                execution_time_ns=metadata.get("executionTime", 0),
                working_dir=metadata.get("workingDir", ""),
            )

        case "bash_background":
            return BackgroundBashResult(
                command=metadata.get("command", ""),
                pid=metadata.get("pid", 0),
                log_path=metadata.get("logPath", ""),
                start_time=_parse_datetime(metadata.get("startTime")),
            )

        case "file_read":
            return FileReadResult(
                file_path=metadata.get("filePath", ""),
                offset=metadata.get("offset", 0),
                line_limit=metadata.get("lineLimit", 0),
                lines=metadata.get("lines", []),
                language=metadata.get("language", ""),
                truncated=metadata.get("truncated", False),
                remaining_lines=metadata.get("remainingLines", 0),
            )

        case "file_write":
            return FileWriteResult(
                file_path=metadata.get("filePath", ""),
                content=metadata.get("content", ""),
                size=metadata.get("size", 0),
                language=metadata.get("language", ""),
            )

        case "file_edit":
            return FileEditResult(
                file_path=metadata.get("filePath", ""),
                edits=[_parse_edit(e) for e in metadata.get("edits", [])],
                language=metadata.get("language", ""),
                replace_all=metadata.get("replaceAll", False),
                replaced_count=metadata.get("replacedCount", 0),
            )

        case "grep_tool":
            return GrepResult(
                pattern=metadata.get("pattern", ""),
                path=metadata.get("path", ""),
                include=metadata.get("include", ""),
                results=[_parse_search_result(r) for r in metadata.get("results", [])],
                truncated=metadata.get("truncated", False),
            )

        case "glob_tool":
            return GlobResult(
                pattern=metadata.get("pattern", ""),
                path=metadata.get("path", ""),
                files=[_parse_file_info(f) for f in metadata.get("files", [])],
                truncated=metadata.get("truncated", False),
            )

        case "todo":
            return TodoResult(
                action=metadata.get("action", ""),
                todo_list=[_parse_todo_item(t) for t in metadata.get("todoList", [])],
                statistics=_parse_todo_stats(metadata.get("statistics")),
            )

        case "image_recognition":
            return ImageRecognitionResult(
                image_path=metadata.get("imagePath", ""),
                image_type=metadata.get("imageType", ""),
                prompt=metadata.get("prompt", ""),
                analysis=metadata.get("analysis", ""),
                image_size=_parse_image_dimensions(metadata.get("imageSize")),
            )

        case "subagent":
            return SubAgentResult(
                question=metadata.get("question", ""),
                response=metadata.get("response", ""),
            )

        case "web_fetch":
            return WebFetchResult(
                url=metadata.get("url", ""),
                content_type=metadata.get("contentType", ""),
                size=metadata.get("size", 0),
                content=metadata.get("content", ""),
                processed_type=metadata.get("processedType", ""),
                saved_path=metadata.get("savedPath", ""),
                prompt=metadata.get("prompt", ""),
            )

        case "view_background_processes":
            return ViewBackgroundProcessesResult(
                processes=[
                    _parse_background_process_info(p) for p in metadata.get("processes", [])
                ],
                count=metadata.get("count", 0),
            )

        case "code_execution":
            return CodeExecutionResult(
                code=metadata.get("code", ""),
                output=metadata.get("output", ""),
                runtime=metadata.get("runtime", ""),
            )

        case "skill":
            return SkillResult(
                skill_name=metadata.get("skillName", ""),
                directory=metadata.get("directory", ""),
            )

        case "mcp_tool":
            return MCPToolResult(
                mcp_tool_name=metadata.get("mcpToolName", ""),
                server_name=metadata.get("serverName", ""),
                parameters=metadata.get("parameters", {}),
                content=[_parse_mcp_content(c) for c in metadata.get("content", [])],
                content_text=metadata.get("contentText", ""),
                execution_time_ns=metadata.get("executionTime", 0),
            )

        case "custom_tool":
            return CustomToolResult(
                output=metadata.get("output", ""),
                execution_time_ns=metadata.get("executionTime", 0),
            )

        case "blocked":
            return BlockedResult(
                tool_name=metadata.get("tool_name", ""),
                reason=metadata.get("reason", ""),
            )

        case _:
            return UnknownToolResult(
                tool_name=tool_name,
                success=success,
                error=error,
                raw_metadata=metadata,
            )
