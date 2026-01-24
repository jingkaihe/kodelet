"""MCP (Model Context Protocol) server configuration."""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field


class MCPServer(ABC):
    """Base class for MCP server configurations."""

    name: str

    @abstractmethod
    def to_yaml_lines(self, indent: int = 0) -> list[str]:
        """Generate YAML configuration lines for this server.

        Args:
            indent: Number of spaces to indent each line

        Returns:
            List of YAML configuration lines
        """
        ...


@dataclass
class StdioServer(MCPServer):
    """Stdio-based MCP server configuration.

    This runs an executable that communicates via stdin/stdout.

    Example:
        ```python
        server = StdioServer(
            name="filesystem",
            command="docker",
            args=["run", "-i", "--rm", "mcp/filesystem", "/"],
            tool_whitelist=["list_directory", "read_file"],
        )
        ```
    """

    name: str
    command: str
    args: list[str] = field(default_factory=list)
    tool_whitelist: list[str] = field(default_factory=list)

    def to_yaml_lines(self, indent: int = 0) -> list[str]:
        """Generate YAML configuration lines."""
        prefix = " " * indent
        lines = [
            f'{prefix}command: "{self.command}"',
        ]

        if self.args:
            args_str = ", ".join(f'"{arg}"' for arg in self.args)
            lines.append(f"{prefix}args: [{args_str}]")

        if self.tool_whitelist:
            whitelist_str = ", ".join(f'"{t}"' for t in self.tool_whitelist)
            lines.append(f"{prefix}tool_white_list: [{whitelist_str}]")

        return lines


@dataclass
class SSEServer(MCPServer):
    """SSE (Server-Sent Events) based MCP server configuration.

    This connects to an HTTP server that implements the MCP protocol via SSE.

    Example:
        ```python
        server = SSEServer(
            name="my_service",
            base_url="http://localhost:8080",
            headers={"Authorization": "Bearer token"},
        )
        ```
    """

    name: str
    base_url: str
    headers: dict[str, str] = field(default_factory=dict)
    tool_whitelist: list[str] = field(default_factory=list)

    def to_yaml_lines(self, indent: int = 0) -> list[str]:
        """Generate YAML configuration lines."""
        prefix = " " * indent
        lines = [
            f'{prefix}base_url: "{self.base_url}"',
        ]

        if self.headers:
            lines.append(f"{prefix}headers:")
            for key, value in self.headers.items():
                lines.append(f'{prefix}  {key}: "{value}"')

        if self.tool_whitelist:
            whitelist_str = ", ".join(f'"{t}"' for t in self.tool_whitelist)
            lines.append(f"{prefix}tool_white_list: [{whitelist_str}]")

        return lines
