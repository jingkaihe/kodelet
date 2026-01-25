"""Structured tool result types for Kodelet SDK.

This module provides typed dataclasses for decoding structured tool results returned
by kodelet's builtin tools. The kodelet CLI serializes tool execution metadata as JSON
which can be parsed into these typed objects for easier programmatic access.

Tool results are returned as `ToolResultEvent` instances during streaming. Use the
`decode_result()` method to parse the JSON string into a typed dataclass.

Supported Tool Types:
    - BashResult: Shell command execution
    - BackgroundBashResult: Background process spawning
    - FileReadResult: File content reading with line numbers
    - FileWriteResult: File creation/overwrite
    - FileEditResult: In-place file modification with diffs
    - GrepResult: Regex pattern search across files
    - GlobResult: File pattern matching
    - TodoResult: Task list management
    - ImageRecognitionResult: Image analysis with vision models
    - SubAgentResult: Delegated sub-agent queries
    - WebFetchResult: HTTP content fetching
    - ViewBackgroundProcessesResult: Background process listing
    - CodeExecutionResult: TypeScript/JavaScript code execution via MCP
    - SkillResult: Skill invocation
    - MCPToolResult: Generic MCP tool execution
    - CustomToolResult: Custom tool execution
    - BlockedResult: Blocked tool due to security hooks
    - UnknownToolResult: Fallback for unrecognized tool types

Example:
    ```python
    async for event in client.query("list files in current directory"):
        if isinstance(event, ToolResultEvent):
            result = event.decode_result()
            match result:
                case BashResult():
                    print(f"Command: {result.command}")
                    print(f"Exit code: {result.exit_code}")
                    print(f"Output: {result.output}")
                case GlobResult():
                    for f in result.files:
                        print(f"{f.path} ({f.size} bytes)")
                case _:
                    print(f"Other tool: {type(result).__name__}")
    ```

Note:
    The JSON structure matches the Go `StructuredToolResult` type from
    `pkg/types/tools/structured_result.go`. Field names are converted from
    camelCase (JSON) to snake_case (Python).
"""

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any


@dataclass
class Edit:
    """A single text replacement in a file edit operation.

    Attributes:
        start_line: The 1-indexed starting line number of the edit.
        end_line: The 1-indexed ending line number of the edit (inclusive).
        old_content: The original text that was replaced.
        new_content: The new text that replaced the original.
    """

    start_line: int
    end_line: int
    old_content: str
    new_content: str


@dataclass
class SearchMatch:
    """A single match in a search result.

    Attributes:
        line_number: The 1-indexed line number where the match was found.
        content: The full line content containing the match.
        match_start: Character offset where the match begins in content.
        match_end: Character offset where the match ends in content.
        is_context: True if this is a context line (not the actual match).
    """

    line_number: int
    content: str
    match_start: int
    match_end: int
    is_context: bool = False


@dataclass
class SearchResult:
    """Search results for a single file.

    Attributes:
        file_path: Absolute or relative path to the file.
        matches: List of individual matches found in the file.
        language: Detected programming language (e.g., "python", "go").
    """

    file_path: str
    matches: list[SearchMatch] = field(default_factory=list)
    language: str = ""


@dataclass
class FileInfo:
    """Information about a matched file or directory.

    Attributes:
        path: Absolute or relative path to the file/directory.
        size: Size in bytes (0 for directories).
        mod_time: Last modification timestamp, or None if unavailable.
        type: Either "file" or "directory".
        language: Detected programming language for files.
    """

    path: str
    size: int
    mod_time: datetime | None
    type: str
    language: str = ""


@dataclass
class TodoItem:
    """A single todo list item.

    Attributes:
        id: Unique identifier for the todo item.
        content: The task description text.
        status: One of "pending", "in_progress", "completed", or "canceled".
        priority: One of "low", "medium", or "high".
        created_at: When the item was created.
        updated_at: When the item was last modified.
    """

    id: str
    content: str
    status: str
    priority: str
    created_at: datetime | None = None
    updated_at: datetime | None = None


@dataclass
class TodoStats:
    """Statistics about the todo list.

    Attributes:
        total: Total number of todo items.
        completed: Number of items with status "completed".
        in_progress: Number of items with status "in_progress".
        pending: Number of items with status "pending".
    """

    total: int = 0
    completed: int = 0
    in_progress: int = 0
    pending: int = 0


@dataclass
class ImageDimensions:
    """Dimensions of an image in pixels.

    Attributes:
        width: Image width in pixels.
        height: Image height in pixels.
    """

    width: int
    height: int


@dataclass
class BackgroundProcessInfo:
    """Information about a single background process.

    Attributes:
        pid: Process ID of the background process.
        command: The command that was executed.
        log_path: Path to the log file capturing stdout/stderr.
        start_time: When the process was started.
        status: Either "running" or "stopped".
    """

    pid: int
    command: str
    log_path: str
    start_time: datetime | None
    status: str


@dataclass
class MCPContent:
    """A content block returned by an MCP tool.

    MCP tools can return multiple content blocks of different types.

    Attributes:
        type: Content type (e.g., "text", "image", "resource").
        text: Text content for text-type blocks.
        data: Base64-encoded data for binary content.
        mime_type: MIME type of the content.
        uri: Resource URI for resource-type blocks.
        metadata: Additional metadata from the MCP server.
    """

    type: str
    text: str = ""
    data: str = ""
    mime_type: str = ""
    uri: str = ""
    metadata: dict[str, Any] = field(default_factory=dict)


# Tool result dataclasses


@dataclass
class BashResult:
    """Result of a bash command execution.

    Represents the output of running a shell command via the Bash tool.

    Attributes:
        command: The shell command that was executed.
        exit_code: The command's exit code (0 indicates success).
        output: Combined stdout and stderr output from the command.
        execution_time_ns: Execution time in nanoseconds.
        working_dir: Directory where the command was executed.

    Example:
        ```python
        if isinstance(result, BashResult):
            if result.exit_code == 0:
                print(f"Success: {result.output}")
            else:
                print(f"Failed with exit code {result.exit_code}")
            print(f"Took {result.execution_time_seconds:.2f}s")
        ```
    """

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
    """Result of spawning a background bash process.

    Background processes run independently and their output is captured to a log file.

    Attributes:
        command: The shell command that was started.
        pid: Process ID of the spawned background process.
        log_path: Path to the log file capturing stdout/stderr.
        start_time: When the process was started.
    """

    command: str
    pid: int
    log_path: str
    start_time: datetime | None


@dataclass
class FileReadResult:
    """Result of a file read operation.

    Contains the file content with optional line-based pagination.

    Attributes:
        file_path: Absolute path to the file that was read.
        offset: The 1-indexed line number where reading started.
        line_limit: Maximum number of lines that were requested.
        lines: The actual lines read from the file.
        language: Detected programming language (e.g., "python", "go").
        truncated: True if the file was too large and reading was truncated.
        remaining_lines: Number of lines not read due to truncation.
    """

    file_path: str
    offset: int
    line_limit: int
    lines: list[str] = field(default_factory=list)
    language: str = ""
    truncated: bool = False
    remaining_lines: int = 0


@dataclass
class FileWriteResult:
    """Result of a file write operation.

    Indicates that a file was created or overwritten successfully.

    Attributes:
        file_path: Absolute path to the file that was written.
        content: The content that was written to the file.
        size: Size of the written content in bytes.
        language: Detected programming language based on file extension.
    """

    file_path: str
    content: str
    size: int
    language: str = ""


@dataclass
class FileEditResult:
    """Result of an in-place file edit operation.

    Contains the diffs showing what was changed in the file.

    Attributes:
        file_path: Absolute path to the file that was edited.
        edits: List of Edit objects describing each change made.
        language: Detected programming language.
        replace_all: True if all occurrences were replaced.
        replaced_count: Number of replacements made (when replace_all=True).
    """

    file_path: str
    edits: list[Edit] = field(default_factory=list)
    language: str = ""
    replace_all: bool = False
    replaced_count: int = 0


@dataclass
class GrepResult:
    """Result of a regex search operation.

    Contains matches found across multiple files.

    Attributes:
        pattern: The regex pattern that was searched for.
        results: List of SearchResult objects, one per file with matches.
        path: The directory or file path that was searched.
        include: Glob pattern used to filter files (e.g., "*.py").
        truncated: True if results were truncated due to too many matches.
    """

    pattern: str
    results: list[SearchResult] = field(default_factory=list)
    path: str = ""
    include: str = ""
    truncated: bool = False


@dataclass
class GlobResult:
    """Result of a file glob pattern match operation.

    Lists files and directories matching a glob pattern.

    Attributes:
        pattern: The glob pattern that was matched (e.g., "**/*.py").
        files: List of FileInfo objects for matching files/directories.
        path: The base directory where the search was performed.
        truncated: True if results were truncated due to too many matches.
    """

    pattern: str
    files: list[FileInfo] = field(default_factory=list)
    path: str = ""
    truncated: bool = False


@dataclass
class TodoResult:
    """Result of a todo list operation.

    The todo tool is used for task tracking during agent execution.

    Attributes:
        action: Either "read" (list todos) or "write" (create/update todos).
        todo_list: Current list of TodoItem objects.
        statistics: Summary statistics about the todo list.
    """

    action: str
    todo_list: list[TodoItem] = field(default_factory=list)
    statistics: TodoStats | None = None


@dataclass
class ImageRecognitionResult:
    """Result of an image analysis operation using vision models.

    Attributes:
        image_path: Path or URL to the analyzed image.
        image_type: Either "local" for file paths or "remote" for URLs.
        prompt: The question/instruction given for image analysis.
        analysis: The model's analysis/description of the image.
        image_size: Dimensions of the image, if available.
    """

    image_path: str
    image_type: str
    prompt: str
    analysis: str
    image_size: ImageDimensions | None = None


@dataclass
class SubAgentResult:
    """Result of delegating a query to a sub-agent.

    Sub-agents are used for complex, multi-step code searches and analysis.

    Attributes:
        question: The question that was delegated to the sub-agent.
        response: The sub-agent's response text.
    """

    question: str
    response: str


@dataclass
class WebFetchResult:
    """Result of fetching content from a URL.

    Supports HTML-to-markdown conversion and AI-based content extraction.

    Attributes:
        url: The URL that was fetched.
        content_type: HTTP Content-Type header value.
        size: Size of the fetched content in bytes.
        content: The fetched content (may be converted to markdown).
        processed_type: How the content was processed: "saved" (raw),
            "markdown" (converted), or "ai_extracted" (AI summarized).
        saved_path: Local path if content was saved to disk.
        prompt: AI extraction prompt if processed_type is "ai_extracted".
    """

    url: str
    content_type: str
    size: int
    content: str
    processed_type: str
    saved_path: str = ""
    prompt: str = ""


@dataclass
class ViewBackgroundProcessesResult:
    """Result of listing background processes.

    Attributes:
        processes: List of BackgroundProcessInfo for each tracked process.
        count: Total number of background processes.
    """

    processes: list[BackgroundProcessInfo] = field(default_factory=list)
    count: int = 0


@dataclass
class CodeExecutionResult:
    """Result of executing TypeScript/JavaScript code via MCP.

    Used for running code that interacts with MCP tools.

    Attributes:
        code: The TypeScript/JavaScript code that was executed.
        output: The console output from the execution.
        runtime: The runtime used (e.g., "tsx", "node").
    """

    code: str
    output: str
    runtime: str


@dataclass
class SkillResult:
    """Result of invoking an agentic skill.

    Skills provide domain-specific expertise that the model can invoke.

    Attributes:
        skill_name: Name of the skill that was invoked.
        directory: Path to the skill's directory containing SKILL.md.
    """

    skill_name: str
    directory: str


@dataclass
class MCPToolResult:
    """Result of executing an MCP (Model Context Protocol) tool.

    MCP tools are external capabilities provided by MCP servers.

    Attributes:
        mcp_tool_name: Name of the MCP tool that was called.
        content_text: Combined text content from all content blocks.
        content: List of MCPContent blocks returned by the tool.
        server_name: Name of the MCP server that provided the tool.
        parameters: The parameters passed to the tool.
        execution_time_ns: Execution time in nanoseconds.

    Example:
        ```python
        if isinstance(result, MCPToolResult):
            print(f"MCP tool: {result.mcp_tool_name}")
            print(f"Server: {result.server_name}")
            print(f"Took {result.execution_time_seconds:.2f}s")
        ```
    """

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
    """Result of executing a custom tool.

    Custom tools are user-defined tools registered in configuration.

    Attributes:
        output: The output produced by the custom tool.
        execution_time_ns: Execution time in nanoseconds.
    """

    output: str
    execution_time_ns: int = 0

    @property
    def execution_time_seconds(self) -> float:
        """Execution time in seconds."""
        return self.execution_time_ns / 1_000_000_000


@dataclass
class BlockedResult:
    """Result when a tool execution was blocked by a security hook.

    Agent lifecycle hooks can intercept and block tool calls for security.

    Attributes:
        tool_name: Name of the tool that was blocked.
        reason: Explanation of why the tool was blocked.
    """

    tool_name: str
    reason: str


@dataclass
class UnknownToolResult:
    """Fallback result for unrecognized tool types.

    Used when the metadata type doesn't match any known tool.

    Attributes:
        tool_name: Name of the tool as reported in the result.
        success: Whether the tool execution was marked as successful.
        error: Error message if the tool failed.
        raw_metadata: The unparsed metadata dictionary for inspection.
    """

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
