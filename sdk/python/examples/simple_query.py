#!/usr/bin/env python3
"""Simple query example for Kodelet SDK.

This example demonstrates basic usage of the Kodelet SDK to send a query
and stream the response.

Usage:
    python simple_query.py
    # or with uv:
    uv run simple_query.py
"""

import asyncio

from kodelet import Kodelet, KodeletConfig


async def main():
    # Simple usage with defaults
    agent = Kodelet()

    print("Sending query...")
    async for event in agent.query("What is 2+2? Answer briefly."):
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
                print("[Tool result received]", flush=True)

    print(f"\n\nConversation ID: {agent.conversation_id}")


async def with_custom_config():
    """Example with custom configuration."""
    config = KodeletConfig(
        provider="anthropic",
        model="claude-sonnet-4-5-20250929",
        max_tokens=4096,
        thinking_budget_tokens=2000,
        no_skills=True,  # Disable skills for faster response
    )

    agent = Kodelet(config=config)

    response = await agent.run("Write a haiku about coding.")
    print(response)


if __name__ == "__main__":
    asyncio.run(main())
    print("\n--- Custom config example ---\n")
    asyncio.run(with_custom_config())
