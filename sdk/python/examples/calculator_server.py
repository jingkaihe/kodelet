#!/usr/bin/env python3
"""Simple MCP calculator server.

This is a minimal MCP server that provides a calculator tool.

Requirements:
    pip install mcp

Usage:
    python calculator_server.py
"""

import asyncio
import math

from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import TextContent, Tool

server = Server("calculator")


@server.list_tools()
async def list_tools() -> list[Tool]:
    """List available tools."""
    return [
        Tool(
            name="calculate",
            description=(
                "Evaluate a mathematical expression. "
                "Supports +, -, *, /, **, sqrt, sin, cos, tan, log, pi, e."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "expression": {
                        "type": "string",
                        "description": (
                            "The mathematical expression to evaluate, "
                            "e.g., '2 + 2' or 'sqrt(16)'"
                        ),
                    }
                },
                "required": ["expression"],
            },
        )
    ]


@server.call_tool()
async def call_tool(name: str, arguments: dict) -> list[TextContent]:
    """Handle tool calls."""
    if name != "calculate":
        raise ValueError(f"Unknown tool: {name}")

    expression = arguments.get("expression", "")

    # Safe evaluation with limited builtins
    safe_dict = {
        "sqrt": math.sqrt,
        "sin": math.sin,
        "cos": math.cos,
        "tan": math.tan,
        "log": math.log,
        "log10": math.log10,
        "exp": math.exp,
        "abs": abs,
        "round": round,
        "pi": math.pi,
        "e": math.e,
    }

    try:
        result = eval(expression, {"__builtins__": {}}, safe_dict)
        return [TextContent(type="text", text=str(result))]
    except Exception as e:
        return [TextContent(type="text", text=f"Error: {e}")]


async def main():
    async with stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, server.create_initialization_options())


if __name__ == "__main__":
    asyncio.run(main())
