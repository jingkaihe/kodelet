"""Hook support for Kodelet SDK.

This module provides a Python DSL for defining lifecycle hooks that integrate
with kodelet's binary hook protocol.

Example:
    ```python
    from kodelet import Kodelet
    from kodelet.hooks import Hook, HookType

    @Hook(HookType.BEFORE_TOOL_CALL)
    def security_guardrail(payload: dict) -> dict:
        if payload.get("tool_name") == "bash":
            command = payload.get("tool_input", {}).get("command", "")
            if "rm -rf" in command:
                return {"blocked": True, "reason": "Dangerous command blocked"}
        return {"blocked": False}

    @Hook(HookType.AFTER_TOOL_CALL)
    def audit_logger(payload: dict) -> dict:
        print(f"Tool called: {payload.get('tool_name')}")
        return {}

    agent = Kodelet(hooks=[security_guardrail, audit_logger])
    ```
"""

import inspect
import textwrap
from collections.abc import Callable
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Any

# Type alias for hook functions
HookFunc = Callable[[dict[str, Any]], dict[str, Any]]


class HookType(Enum):
    """Types of lifecycle hooks supported by kodelet."""

    BEFORE_TOOL_CALL = "before_tool_call"
    AFTER_TOOL_CALL = "after_tool_call"
    USER_MESSAGE_SEND = "user_message_send"
    AGENT_STOP = "agent_stop"
    TURN_END = "turn_end"


@dataclass
class HookDefinition:
    """Metadata for a registered hook."""

    name: str
    hook_type: HookType
    func: HookFunc


def Hook(hook_type: HookType, name: str | None = None) -> Callable[[HookFunc], HookFunc]:  # noqa: N802
    """Decorator to register a function as a hook.

    Args:
        hook_type: The type of hook (when it should be triggered)
        name: Optional name for the hook (defaults to function name)

    Returns:
        Decorated function with hook metadata attached

    Example:
        ```python
        @Hook(HookType.BEFORE_TOOL_CALL)
        def my_hook(payload: dict) -> dict:
            # Process payload and return result
            return {"blocked": False}
        ```
    """

    def decorator(func: HookFunc) -> HookFunc:
        hook_name = name or func.__name__
        func._hook_definition = HookDefinition(  # type: ignore[attr-defined]
            name=hook_name,
            hook_type=hook_type,
            func=func,
        )
        return func

    return decorator


class HookManager:
    """Manages Python hooks and generates executable scripts for kodelet."""

    def __init__(self, hooks: list[HookFunc] | None = None):
        """Initialize the hook manager.

        Args:
            hooks: List of functions decorated with @Hook
        """
        self.hooks: dict[str, HookDefinition] = {}
        for hook in hooks or []:
            if hasattr(hook, "_hook_definition"):
                defn = hook._hook_definition
                self.hooks[defn.name] = defn

    def generate_hook_scripts(self, hooks_dir: Path) -> None:
        """Generate executable Python scripts for all hooks.

        Args:
            hooks_dir: Directory to write hook scripts to
        """
        hooks_dir.mkdir(parents=True, exist_ok=True)

        for name, defn in self.hooks.items():
            script_path = hooks_dir / name
            script_content = self._generate_hook_script(defn)
            script_path.write_text(script_content)
            script_path.chmod(0o755)

    def _generate_hook_script(self, defn: HookDefinition) -> str:
        """Generate a standalone Python script for a hook.

        The generated script implements the kodelet binary hook protocol:
        - `./hook hook` returns the hook type
        - `./hook run` receives JSON payload via stdin.

        Args:
            defn: The hook definition

        Returns:
            Python script content as a string
        """
        # Get the function source code
        func_source = inspect.getsource(defn.func)

        # Remove the decorator line(s)
        lines = func_source.split("\n")
        filtered_lines = []
        skip_next = False
        for line in lines:
            stripped = line.strip()
            if stripped.startswith("@Hook") or stripped.startswith("@kodelet"):
                skip_next = stripped.endswith("\\")
                continue
            if skip_next:
                skip_next = stripped.endswith("\\")
                continue
            filtered_lines.append(line)

        func_source = "\n".join(filtered_lines)

        # Dedent the function source
        func_source = textwrap.dedent(func_source)

        script = f'''#!/usr/bin/env python3
"""Auto-generated hook: {defn.name}

Hook type: {defn.hook_type.value}
"""

import json
import sys

# Hook implementation
{func_source}

def main():
    if len(sys.argv) < 2:
        print("Usage: hook hook|run", file=sys.stderr)
        sys.exit(1)

    command = sys.argv[1]

    if command == "hook":
        # Return the hook type
        print("{defn.hook_type.value}")
        sys.exit(0)

    elif command == "run":
        # Read payload from stdin
        try:
            payload = json.load(sys.stdin)
        except json.JSONDecodeError as e:
            print(json.dumps({{"error": f"Invalid JSON: {{e}}"}}))
            sys.exit(1)

        # Execute the hook function
        try:
            result = {defn.func.__name__}(payload)
            if result is None:
                result = {{}}
            print(json.dumps(result))
        except Exception as e:
            print(json.dumps({{"error": str(e)}}))
            sys.exit(1)

        sys.exit(0)

    else:
        print(f"Unknown command: {{command}}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
'''
        return script
