"""Tests for tool result decoding."""

import json
from datetime import datetime, timezone

import pytest

from kodelet.events import ToolResultEvent
from kodelet.tool_results import (
    BackgroundBashResult,
    BackgroundProcessInfo,
    BashResult,
    BlockedResult,
    CodeExecutionResult,
    CustomToolResult,
    Edit,
    FileEditResult,
    FileInfo,
    FileReadResult,
    FileWriteResult,
    GlobResult,
    GrepResult,
    ImageDimensions,
    ImageRecognitionResult,
    MCPContent,
    MCPToolResult,
    SearchMatch,
    SearchResult,
    SkillResult,
    SubAgentResult,
    TodoItem,
    TodoResult,
    TodoStats,
    UnknownToolResult,
    ViewBackgroundProcessesResult,
    WebFetchResult,
    decode_tool_result,
)


class TestDecodeToolResult:
    """Tests for decode_tool_result function."""

    def test_decode_bash_result(self) -> None:
        """Test decoding a bash tool result."""
        data = {
            "toolName": "bash",
            "success": True,
            "metadataType": "bash",
            "metadata": {
                "command": "ls -la",
                "exitCode": 0,
                "output": "total 0\n",
                "executionTime": 1500000000,
                "workingDir": "/home/user",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, BashResult)
        assert result.command == "ls -la"
        assert result.exit_code == 0
        assert result.output == "total 0\n"
        assert result.execution_time_ns == 1500000000
        assert result.execution_time_seconds == 1.5
        assert result.working_dir == "/home/user"

    def test_decode_background_bash_result(self) -> None:
        """Test decoding a background bash tool result."""
        data = {
            "toolName": "bash_background",
            "success": True,
            "metadataType": "bash_background",
            "metadata": {
                "command": "python server.py",
                "pid": 12345,
                "logPath": "/home/user/.kodelet/bgpids/12345/out.log",
                "startTime": "2024-01-01T12:00:00Z",
            },
            "timestamp": "2024-01-01T12:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, BackgroundBashResult)
        assert result.command == "python server.py"
        assert result.pid == 12345
        assert result.log_path == "/home/user/.kodelet/bgpids/12345/out.log"
        assert result.start_time == datetime(2024, 1, 1, 12, 0, 0, tzinfo=timezone.utc)

    def test_decode_file_read_result(self) -> None:
        """Test decoding a file read tool result."""
        data = {
            "toolName": "file_read",
            "success": True,
            "metadataType": "file_read",
            "metadata": {
                "filePath": "/home/user/test.py",
                "offset": 1,
                "lineLimit": 100,
                "lines": ["def hello():", "    print('hello')"],
                "language": "python",
                "truncated": False,
                "remainingLines": 0,
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, FileReadResult)
        assert result.file_path == "/home/user/test.py"
        assert result.offset == 1
        assert result.line_limit == 100
        assert result.lines == ["def hello():", "    print('hello')"]
        assert result.language == "python"
        assert result.truncated is False
        assert result.remaining_lines == 0

    def test_decode_file_write_result(self) -> None:
        """Test decoding a file write tool result."""
        data = {
            "toolName": "file_write",
            "success": True,
            "metadataType": "file_write",
            "metadata": {
                "filePath": "/home/user/new.py",
                "content": "print('hello')",
                "size": 14,
                "language": "python",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, FileWriteResult)
        assert result.file_path == "/home/user/new.py"
        assert result.content == "print('hello')"
        assert result.size == 14
        assert result.language == "python"

    def test_decode_file_edit_result(self) -> None:
        """Test decoding a file edit tool result."""
        data = {
            "toolName": "file_edit",
            "success": True,
            "metadataType": "file_edit",
            "metadata": {
                "filePath": "/home/user/test.py",
                "edits": [
                    {
                        "startLine": 1,
                        "endLine": 2,
                        "oldContent": "def foo():\n    pass",
                        "newContent": "def bar():\n    return 42",
                    }
                ],
                "language": "python",
                "replaceAll": False,
                "replacedCount": 1,
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, FileEditResult)
        assert result.file_path == "/home/user/test.py"
        assert len(result.edits) == 1
        edit = result.edits[0]
        assert isinstance(edit, Edit)
        assert edit.start_line == 1
        assert edit.end_line == 2
        assert edit.old_content == "def foo():\n    pass"
        assert edit.new_content == "def bar():\n    return 42"
        assert result.language == "python"
        assert result.replace_all is False
        assert result.replaced_count == 1

    def test_decode_grep_result(self) -> None:
        """Test decoding a grep tool result."""
        data = {
            "toolName": "grep_tool",
            "success": True,
            "metadataType": "grep_tool",
            "metadata": {
                "pattern": "def test",
                "path": "/home/user/project",
                "include": "*.py",
                "results": [
                    {
                        "filePath": "/home/user/project/test.py",
                        "language": "python",
                        "matches": [
                            {
                                "lineNumber": 10,
                                "content": "def test_foo():",
                                "matchStart": 0,
                                "matchEnd": 8,
                                "isContext": False,
                            }
                        ],
                    }
                ],
                "truncated": False,
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, GrepResult)
        assert result.pattern == "def test"
        assert result.path == "/home/user/project"
        assert result.include == "*.py"
        assert len(result.results) == 1
        search_result = result.results[0]
        assert isinstance(search_result, SearchResult)
        assert search_result.file_path == "/home/user/project/test.py"
        assert len(search_result.matches) == 1
        match = search_result.matches[0]
        assert isinstance(match, SearchMatch)
        assert match.line_number == 10
        assert match.content == "def test_foo():"
        assert match.match_start == 0
        assert match.match_end == 8
        assert match.is_context is False

    def test_decode_glob_result(self) -> None:
        """Test decoding a glob tool result."""
        data = {
            "toolName": "glob_tool",
            "success": True,
            "metadataType": "glob_tool",
            "metadata": {
                "pattern": "*.py",
                "path": "/home/user/project",
                "files": [
                    {
                        "path": "/home/user/project/main.py",
                        "size": 1024,
                        "modTime": "2024-01-01T00:00:00Z",
                        "type": "file",
                        "language": "python",
                    }
                ],
                "truncated": False,
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, GlobResult)
        assert result.pattern == "*.py"
        assert result.path == "/home/user/project"
        assert len(result.files) == 1
        file_info = result.files[0]
        assert isinstance(file_info, FileInfo)
        assert file_info.path == "/home/user/project/main.py"
        assert file_info.size == 1024
        assert file_info.type == "file"
        assert file_info.language == "python"

    def test_decode_todo_result(self) -> None:
        """Test decoding a todo tool result."""
        data = {
            "toolName": "todo",
            "success": True,
            "metadataType": "todo",
            "metadata": {
                "action": "write",
                "todoList": [
                    {
                        "id": "1",
                        "content": "Write tests",
                        "status": "in_progress",
                        "priority": "high",
                        "createdAt": "2024-01-01T00:00:00Z",
                        "updatedAt": "2024-01-01T01:00:00Z",
                    }
                ],
                "statistics": {
                    "total": 3,
                    "completed": 1,
                    "inProgress": 1,
                    "pending": 1,
                },
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, TodoResult)
        assert result.action == "write"
        assert len(result.todo_list) == 1
        todo = result.todo_list[0]
        assert isinstance(todo, TodoItem)
        assert todo.id == "1"
        assert todo.content == "Write tests"
        assert todo.status == "in_progress"
        assert todo.priority == "high"
        assert result.statistics is not None
        assert isinstance(result.statistics, TodoStats)
        assert result.statistics.total == 3
        assert result.statistics.completed == 1
        assert result.statistics.in_progress == 1
        assert result.statistics.pending == 1

    def test_decode_image_recognition_result(self) -> None:
        """Test decoding an image recognition result."""
        data = {
            "toolName": "image_recognition",
            "success": True,
            "metadataType": "image_recognition",
            "metadata": {
                "imagePath": "/home/user/image.png",
                "imageType": "local",
                "prompt": "Describe this image",
                "analysis": "The image shows a landscape.",
                "imageSize": {"width": 800, "height": 600},
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, ImageRecognitionResult)
        assert result.image_path == "/home/user/image.png"
        assert result.image_type == "local"
        assert result.prompt == "Describe this image"
        assert result.analysis == "The image shows a landscape."
        assert result.image_size is not None
        assert isinstance(result.image_size, ImageDimensions)
        assert result.image_size.width == 800
        assert result.image_size.height == 600

    def test_decode_subagent_result(self) -> None:
        """Test decoding a subagent result."""
        data = {
            "toolName": "subagent",
            "success": True,
            "metadataType": "subagent",
            "metadata": {
                "question": "What is the architecture?",
                "response": "The system uses a microservices architecture.",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, SubAgentResult)
        assert result.question == "What is the architecture?"
        assert result.response == "The system uses a microservices architecture."

    def test_decode_web_fetch_result(self) -> None:
        """Test decoding a web fetch result."""
        data = {
            "toolName": "web_fetch",
            "success": True,
            "metadataType": "web_fetch",
            "metadata": {
                "url": "https://example.com/api",
                "contentType": "application/json",
                "size": 256,
                "content": '{"data": "test"}',
                "processedType": "saved",
                "savedPath": "/home/user/.kodelet/web-archives/example.com/api.json",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, WebFetchResult)
        assert result.url == "https://example.com/api"
        assert result.content_type == "application/json"
        assert result.size == 256
        assert result.content == '{"data": "test"}'
        assert result.processed_type == "saved"
        assert (
            result.saved_path == "/home/user/.kodelet/web-archives/example.com/api.json"
        )

    def test_decode_view_background_processes_result(self) -> None:
        """Test decoding a view background processes result."""
        data = {
            "toolName": "view_background_processes",
            "success": True,
            "metadataType": "view_background_processes",
            "metadata": {
                "processes": [
                    {
                        "pid": 12345,
                        "command": "python server.py",
                        "logPath": "/home/user/.kodelet/bgpids/12345/out.log",
                        "startTime": "2024-01-01T12:00:00Z",
                        "status": "running",
                    }
                ],
                "count": 1,
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, ViewBackgroundProcessesResult)
        assert result.count == 1
        assert len(result.processes) == 1
        proc = result.processes[0]
        assert isinstance(proc, BackgroundProcessInfo)
        assert proc.pid == 12345
        assert proc.command == "python server.py"
        assert proc.status == "running"

    def test_decode_code_execution_result(self) -> None:
        """Test decoding a code execution result."""
        data = {
            "toolName": "code_execution",
            "success": True,
            "metadataType": "code_execution",
            "metadata": {
                "code": "console.log('hello')",
                "output": "hello\n",
                "runtime": "node",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, CodeExecutionResult)
        assert result.code == "console.log('hello')"
        assert result.output == "hello\n"
        assert result.runtime == "node"

    def test_decode_skill_result(self) -> None:
        """Test decoding a skill result."""
        data = {
            "toolName": "skill",
            "success": True,
            "metadataType": "skill",
            "metadata": {
                "skillName": "kodelet",
                "directory": "/home/user/.kodelet/skills/kodelet",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, SkillResult)
        assert result.skill_name == "kodelet"
        assert result.directory == "/home/user/.kodelet/skills/kodelet"

    def test_decode_mcp_tool_result(self) -> None:
        """Test decoding an MCP tool result."""
        data = {
            "toolName": "mcp_tool",
            "success": True,
            "metadataType": "mcp_tool",
            "metadata": {
                "mcpToolName": "query_database",
                "serverName": "database-server",
                "parameters": {"query": "SELECT * FROM users"},
                "content": [
                    {
                        "type": "text",
                        "text": "Found 10 users",
                    }
                ],
                "contentText": "Found 10 users",
                "executionTime": 500000000,
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, MCPToolResult)
        assert result.mcp_tool_name == "query_database"
        assert result.server_name == "database-server"
        assert result.parameters == {"query": "SELECT * FROM users"}
        assert result.content_text == "Found 10 users"
        assert result.execution_time_ns == 500000000
        assert result.execution_time_seconds == 0.5
        assert len(result.content) == 1
        content = result.content[0]
        assert isinstance(content, MCPContent)
        assert content.type == "text"
        assert content.text == "Found 10 users"

    def test_decode_custom_tool_result(self) -> None:
        """Test decoding a custom tool result."""
        data = {
            "toolName": "custom_tool",
            "success": True,
            "metadataType": "custom_tool",
            "metadata": {
                "output": "Custom tool output",
                "executionTime": 100000000,
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, CustomToolResult)
        assert result.output == "Custom tool output"
        assert result.execution_time_ns == 100000000
        assert result.execution_time_seconds == 0.1

    def test_decode_blocked_result(self) -> None:
        """Test decoding a blocked tool result."""
        data = {
            "toolName": "bash",
            "success": False,
            "error": "blocked by hook",
            "metadataType": "blocked",
            "metadata": {
                "tool_name": "bash",
                "reason": "Command not allowed",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, BlockedResult)
        assert result.tool_name == "bash"
        assert result.reason == "Command not allowed"

    def test_decode_unknown_result(self) -> None:
        """Test decoding an unknown tool result."""
        data = {
            "toolName": "future_tool",
            "success": True,
            "metadataType": "future_tool",
            "metadata": {"someKey": "someValue"},
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, UnknownToolResult)
        assert result.tool_name == "future_tool"
        assert result.success is True
        assert result.raw_metadata == {"someKey": "someValue"}

    def test_decode_missing_metadata(self) -> None:
        """Test decoding when metadata is missing."""
        data = {
            "toolName": "bash",
            "success": True,
            "metadataType": "bash",
            "timestamp": "2024-01-01T00:00:00Z",
        }

        result = decode_tool_result(data)
        assert isinstance(result, BashResult)
        assert result.command == ""
        assert result.exit_code == 0


class TestToolResultEventDecode:
    """Tests for ToolResultEvent.decode_result method."""

    def test_decode_valid_json(self) -> None:
        """Test decoding valid JSON tool result."""
        data = {
            "toolName": "bash",
            "success": True,
            "metadataType": "bash",
            "metadata": {
                "command": "echo hello",
                "exitCode": 0,
                "output": "hello\n",
                "executionTime": 50000000,
                "workingDir": "/home/user",
            },
            "timestamp": "2024-01-01T00:00:00Z",
        }

        event = ToolResultEvent(
            kind="tool-result",
            conversation_id="test-123",
            tool_name="bash",
            tool_call_id="call-456",
            result=json.dumps(data),
        )

        result = event.decode_result()
        assert isinstance(result, BashResult)
        assert result.command == "echo hello"
        assert result.exit_code == 0

    def test_decode_invalid_json(self) -> None:
        """Test decoding invalid JSON returns UnknownToolResult."""
        event = ToolResultEvent(
            kind="tool-result",
            conversation_id="test-123",
            tool_name="bash",
            tool_call_id="call-456",
            result="not valid json",
        )

        result = event.decode_result()
        assert isinstance(result, UnknownToolResult)
        assert result.tool_name == "bash"

    def test_decode_non_structured_result(self) -> None:
        """Test decoding non-structured result returns UnknownToolResult."""
        event = ToolResultEvent(
            kind="tool-result",
            conversation_id="test-123",
            tool_name="bash",
            tool_call_id="call-456",
            result='{"someOther": "format"}',
        )

        result = event.decode_result()
        assert isinstance(result, UnknownToolResult)
        assert result.tool_name == "bash"

    def test_decode_empty_result(self) -> None:
        """Test decoding empty result returns UnknownToolResult."""
        event = ToolResultEvent(
            kind="tool-result",
            conversation_id="test-123",
            tool_name="bash",
            tool_call_id="call-456",
            result="",
        )

        result = event.decode_result()
        assert isinstance(result, UnknownToolResult)
