#!/usr/bin/env python3
"""MCP tools example for Kodelet SDK.

This example demonstrates how to configure MCP servers for custom tools.

Usage:
    python mcp_tools.py
"""

import asyncio

from kodelet.mcp import SSEServer, StdioServer


async def main():
    # Example: Configure a filesystem MCP server (Docker-based)
    # This assumes you have the mcp/filesystem Docker image
    filesystem_server = StdioServer(
        name="filesystem",
        command="docker",
        args=["run", "-i", "--rm", "-v", "/tmp:/workspace", "mcp/filesystem", "/workspace"],
        tool_whitelist=["list_directory", "read_file", "write_file"],
    )

    # Example: Configure an SSE-based MCP server
    # This is for HTTP-based MCP servers
    api_server = SSEServer(
        name="my_api",
        base_url="http://localhost:8080",
        headers={"Authorization": "Bearer your-token"},
        tool_whitelist=["get_data", "post_data"],
    )

    # Create agent with MCP servers
    # Note: This will fail if the MCP servers aren't actually running
    # This is just to demonstrate the configuration
    print("MCP Configuration Example")
    print("=" * 50)
    print("\nFilesystem Server config:")
    print("\n".join(f"  {line}" for line in filesystem_server.to_yaml_lines(indent=0)))
    print("\nAPI Server config:")
    print("\n".join(f"  {line}" for line in api_server.to_yaml_lines(indent=0)))

    # To actually use MCP servers, uncomment below:
    # agent = Kodelet(mcp_servers=[filesystem_server])
    # async for event in agent.query("List files in /workspace"):
    #     if event.kind == "text-delta":
    #         print(event.delta, end="", flush=True)


async def custom_mcp_server_example():
    """Example of creating a custom MCP server with the mcp Python package.

    First, create your MCP server (my_tools_server.py):

    ```python
    from mcp.server import Server

    server = Server("my-tools")

    @server.tool()
    async def calculator(expression: str) -> str:
        '''Evaluate mathematical expressions'''
        return str(eval(expression, {"__builtins__": {}}, {}))

    if __name__ == "__main__":
        import asyncio
        asyncio.run(server.run_stdio())
    ```

    Then use it with the SDK:
    """
    # Configure the MCP server
    my_tools = StdioServer(
        name="my_tools",
        command="python",
        args=["my_tools_server.py"],
    )

    # Print the configuration for the custom server
    print("\nCustom MCP Server Example")
    print("=" * 50)
    print("Custom server config:")
    print("\n".join(f"  {line}" for line in my_tools.to_yaml_lines(indent=0)))
    print("=" * 50)
    print("See the docstring for how to create a custom MCP server")
    print("using the 'mcp' Python package.")


if __name__ == "__main__":
    asyncio.run(main())
    asyncio.run(custom_mcp_server_example())
