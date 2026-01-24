"""Tests for MCP configuration."""

from kodelet.mcp import SSEServer, StdioServer


def test_stdio_server_basic():
    server = StdioServer(
        name="test",
        command="echo",
        args=["hello"],
    )

    assert server.name == "test"
    assert server.command == "echo"
    assert server.args == ["hello"]


def test_stdio_server_to_yaml():
    server = StdioServer(
        name="fs",
        command="docker",
        args=["run", "-i", "mcp/filesystem"],
        tool_whitelist=["list_directory"],
    )

    lines = server.to_yaml_lines(indent=0)

    assert 'command: "docker"' in lines
    assert 'args: ["run", "-i", "mcp/filesystem"]' in lines
    assert 'tool_white_list: ["list_directory"]' in lines


def test_stdio_server_to_yaml_with_indent():
    server = StdioServer(name="test", command="echo")

    lines = server.to_yaml_lines(indent=4)

    assert lines[0].startswith("    ")


def test_sse_server_basic():
    server = SSEServer(
        name="api",
        base_url="http://localhost:8080",
        headers={"Authorization": "Bearer token"},
    )

    assert server.name == "api"
    assert server.base_url == "http://localhost:8080"
    assert server.headers == {"Authorization": "Bearer token"}


def test_sse_server_to_yaml():
    server = SSEServer(
        name="api",
        base_url="http://localhost:8080",
        headers={"Authorization": "Bearer token", "X-Custom": "value"},
        tool_whitelist=["tool1", "tool2"],
    )

    lines = server.to_yaml_lines(indent=0)
    yaml_str = "\n".join(lines)

    assert 'base_url: "http://localhost:8080"' in yaml_str
    assert "headers:" in yaml_str
    assert 'Authorization: "Bearer token"' in yaml_str
    assert 'tool_white_list: ["tool1", "tool2"]' in yaml_str
