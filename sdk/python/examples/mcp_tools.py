#!/usr/bin/env python3
"""MCP tools example for Kodelet SDK.

This example demonstrates how to use MCP servers with the Kodelet SDK.

Prerequisites:
    1. Install the mcp package: pip install mcp
    2. The calculator_server.py in this directory

Usage:
    python mcp_tools.py
"""

import asyncio
import sys
from pathlib import Path

from kodelet import Kodelet, KodeletConfig
from kodelet.mcp import StdioServer


async def main():
    # Get the path to the calculator server
    examples_dir = Path(__file__).parent
    calculator_server_path = examples_dir / "calculator_server.py"

    if not calculator_server_path.exists():
        print(f"Error: {calculator_server_path} not found")
        sys.exit(1)

    # Configure the MCP calculator server
    calculator = StdioServer(
        name="calculator",
        command=sys.executable,  # Use current Python interpreter
        args=[str(calculator_server_path)],
    )

    print("MCP Calculator Example")
    print("=" * 50)
    print(f"Using calculator server: {calculator_server_path}")
    print()

    # Create agent with MCP server
    config = KodeletConfig(
        no_skills=True,
        no_hooks=True,
    )
    agent = Kodelet(config=config, mcp_servers=[calculator])

    # Ask a question that requires calculation
    query = "What is the square root of 144 plus 5 times 3? Use the calculate tool."
    print(f"Query: {query}\n")

    async for event in agent.query(query):
        match event.kind:
            case "text-delta":
                print(event.delta, end="", flush=True)
            case "thinking-start":
                print("\n[Thinking...]", flush=True)
            case "thinking-end":
                print("[Done thinking]", flush=True)
            case "tool-use":
                print(f"\n[Using tool: {event.tool_name}]", flush=True)
            case "tool-result":
                print(f"[Tool result: {event.result}]", flush=True)

    print(f"\n\nConversation ID: {agent.conversation_id}")


if __name__ == "__main__":
    asyncio.run(main())
