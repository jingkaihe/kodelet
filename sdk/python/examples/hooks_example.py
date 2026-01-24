#!/usr/bin/env python3
"""Hooks example for Kodelet SDK.

This example demonstrates how to use Python hooks to intercept and control
agent behavior.

Usage:
    python hooks_example.py
"""

import asyncio

from kodelet import Kodelet
from kodelet.hooks import Hook, HookType


# Security hook: Block dangerous bash commands
@Hook(HookType.BEFORE_TOOL_CALL)
def security_guardrail(payload: dict) -> dict:
    """Block potentially dangerous commands."""
    if payload.get("tool_name") == "Bash":
        tool_input = payload.get("tool_input", {})
        command = tool_input.get("command", "")

        # List of dangerous patterns
        dangerous_patterns = [
            "rm -rf",
            "sudo",
            ":(){:|:&};:",  # Fork bomb
            "mkfs",
            "dd if=",
        ]

        for pattern in dangerous_patterns:
            if pattern in command:
                return {
                    "blocked": True,
                    "reason": f"Security policy: '{pattern}' commands are not allowed",
                }

    return {"blocked": False}


# Audit hook: Log all tool calls
@Hook(HookType.AFTER_TOOL_CALL)
def audit_logger(payload: dict) -> dict:
    """Log tool calls for auditing."""
    tool_name = payload.get("tool_name", "unknown")
    success = payload.get("tool_output", {}).get("success", False)

    print(f"[AUDIT] Tool: {tool_name}, Success: {success}")

    # Could write to a log file here
    # with open("audit.log", "a") as f:
    #     f.write(f"{datetime.now()}: {tool_name} - {success}\n")

    return {}


# Agent stop hook: Check for cleanup tasks
@Hook(HookType.AGENT_STOP)
def cleanup_checker(payload: dict) -> dict:
    """Check if any cleanup is needed before agent stops."""
    # Example: Check if a temp file exists that should be removed
    from pathlib import Path

    temp_file = Path("/tmp/kodelet_test_file.txt")
    if temp_file.exists():
        return {"follow_up_messages": ["Please remove /tmp/kodelet_test_file.txt"]}

    return {}


async def main():
    # Create agent with hooks
    agent = Kodelet(hooks=[security_guardrail, audit_logger, cleanup_checker])

    print("Testing hooks with a file operation...")
    print("-" * 50)

    async for event in agent.query("Create a file called test.txt with 'Hello World' in it"):
        if event.kind == "text-delta":
            print(event.delta, end="", flush=True)

    print("\n")
    print("-" * 50)
    print("Testing security hook with a blocked command...")
    print("-" * 50)

    # This should trigger the security hook
    async for event in agent.query("Run the command: rm -rf /tmp/test"):
        if event.kind == "text-delta":
            print(event.delta, end="", flush=True)

    print("\n")


if __name__ == "__main__":
    asyncio.run(main())
