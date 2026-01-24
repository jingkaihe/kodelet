"""Main Kodelet client implementation."""

import asyncio
import contextlib
import json
import os
import shutil
import tempfile
from collections.abc import AsyncGenerator
from pathlib import Path

from .config import KodeletConfig
from .conversation import ConversationManager
from .events import Event, parse_event
from .exceptions import BinaryNotFoundError, KodeletError
from .hooks import HookFunc, HookManager
from .mcp import MCPServer

# Prefix for generated hook scripts to avoid conflicts
_HOOK_PREFIX = "_pysdk_"


class Kodelet:
    """Main client for interacting with kodelet.

    The Kodelet client wraps the kodelet CLI binary and provides an async
    Python interface for sending queries and managing conversations.

    Example:
        ```python
        from kodelet import Kodelet

        agent = Kodelet()

        async for event in agent.query("Hello World"):
            if event.kind == "text-delta":
                print(event.delta, end="", flush=True)
        ```

    With hooks:
        ```python
        from kodelet import Kodelet
        from kodelet.hooks import Hook, HookType

        @Hook(HookType.BEFORE_TOOL_CALL)
        def security_check(payload: dict) -> dict:
            if "rm -rf" in payload.get("tool_input", {}).get("command", ""):
                return {"blocked": True, "reason": "Dangerous command"}
            return {"blocked": False}

        agent = Kodelet(hooks=[security_check])
        ```
    """

    def __init__(
        self,
        config: KodeletConfig | None = None,
        resume: str | None = None,
        follow: bool = False,
        mcp_servers: list[MCPServer] | None = None,
        hooks: list[HookFunc] | None = None,
    ):
        """Initialize the Kodelet client.

        Args:
            config: Configuration options (uses defaults if not specified)
            resume: Resume a specific conversation by ID
            follow: Resume the most recent conversation
            mcp_servers: MCP servers to configure
            hooks: List of functions decorated with @Hook for lifecycle hooks
        """
        self.config = config or KodeletConfig()
        self._resume = resume
        self._follow = follow
        self._conversation_id: str | None = None
        self._mcp_servers = mcp_servers or []
        self._hook_manager = HookManager(hooks)

        # Find kodelet binary
        self._binary = self._find_binary()

        # Conversation manager (lazy initialization)
        self._conversations: ConversationManager | None = None

    def _find_binary(self) -> Path:
        """Find the kodelet binary."""
        if self.config.kodelet_path:
            if self.config.kodelet_path.exists():
                return self.config.kodelet_path
            raise BinaryNotFoundError(
                f"Kodelet binary not found at {self.config.kodelet_path}"
            )

        # Check PATH
        binary = shutil.which("kodelet")
        if binary:
            return Path(binary)

        raise BinaryNotFoundError(
            "kodelet binary not found in PATH. "
            "Please install kodelet or specify kodelet_path in config."
        )

    @property
    def conversation_id(self) -> str | None:
        """Current conversation ID."""
        return self._conversation_id

    @property
    def conversations(self) -> ConversationManager:
        """Access conversation management."""
        if self._conversations is None:
            self._conversations = ConversationManager(self._binary, self.config.cwd)
        return self._conversations

    def _build_command(self, query: str) -> list[str]:
        """Build the kodelet command with all flags."""
        cmd = [str(self._binary), "run", "--headless"]

        if self.config.stream_deltas:
            cmd.append("--stream-deltas")

        # Provider and model
        cmd.extend(["--provider", self.config.provider])
        cmd.extend(["--model", self.config.model])
        cmd.extend(["--weak-model", self.config.weak_model])
        cmd.extend(["--max-tokens", str(self.config.max_tokens)])
        cmd.extend(["--weak-model-max-tokens", str(self.config.weak_model_max_tokens)])
        cmd.extend(
            ["--thinking-budget-tokens", str(self.config.thinking_budget_tokens)]
        )

        if self.config.provider == "openai":
            cmd.extend(["--reasoning-effort", self.config.reasoning_effort])

        # Tools
        if self.config.allowed_tools:
            cmd.extend(["--allowed-tools", ",".join(self.config.allowed_tools)])

        if self.config.allowed_commands:
            cmd.extend(["--allowed-commands", ",".join(self.config.allowed_commands)])

        # Execution options
        cmd.extend(["--max-turns", str(self.config.max_turns)])
        cmd.extend(["--compact-ratio", str(self.config.compact_ratio)])

        if self.config.disable_auto_compact:
            cmd.append("--disable-auto-compact")

        if self.config.include_history:
            cmd.append("--include-history")

        # Feature flags
        if self.config.no_skills:
            cmd.append("--no-skills")

        if self.config.no_hooks:
            cmd.append("--no-hooks")

        if self.config.no_mcp:
            cmd.append("--no-mcp")

        if self.config.no_save:
            cmd.append("--no-save")

        # Images
        for image in self.config.images:
            cmd.extend(["--image", image])

        # Conversation management
        if self._resume:
            cmd.extend(["--resume", self._resume])
        elif self._follow:
            cmd.append("--follow")
        elif self._conversation_id:
            cmd.extend(["--resume", self._conversation_id])

        # Anthropic account
        if self.config.account:
            cmd.extend(["--account", self.config.account])

        # Query
        cmd.append(query)

        return cmd

    def _generate_mcp_config(self) -> str:
        """Generate YAML config for MCP servers."""
        if not self._mcp_servers:
            return ""

        lines = ["mcp:", "  servers:"]
        for server in self._mcp_servers:
            lines.append(f"    {server.name}:")
            lines.extend(server.to_yaml_lines(indent=6))
        return "\n".join(lines)

    def _setup_hooks(self, cwd: Path) -> list[Path]:
        """Set up hook scripts in the working directory.

        Args:
            cwd: The working directory for kodelet

        Returns:
            List of created hook script paths (for cleanup)
        """
        if not self._hook_manager.hooks:
            return []

        hooks_dir = cwd / ".kodelet" / "hooks"
        hooks_dir.mkdir(parents=True, exist_ok=True)

        created_scripts: list[Path] = []

        for name, defn in self._hook_manager.hooks.items():
            # Use prefix to avoid conflicts with existing hooks
            script_name = f"{_HOOK_PREFIX}{name}"
            script_path = hooks_dir / script_name
            script_content = self._hook_manager._generate_hook_script(defn)
            script_path.write_text(script_content)
            script_path.chmod(0o755)
            created_scripts.append(script_path)

        return created_scripts

    def _cleanup_hooks(self, scripts: list[Path]) -> None:
        """Clean up generated hook scripts.

        Args:
            scripts: List of script paths to remove
        """
        for script_path in scripts:
            with contextlib.suppress(OSError):
                script_path.unlink(missing_ok=True)

    async def query(self, message: str) -> AsyncGenerator[Event, None]:
        """Send a query and stream the response.

        This is an async generator that yields events as they are received
        from kodelet. The events include text deltas, thinking blocks,
        tool uses, and tool results.

        Args:
            message: The query to send to kodelet

        Yields:
            Event objects representing the streaming response

        Raises:
            KodeletError: If the query fails

        Example:
            ```python
            async for event in agent.query("Write hello world"):
                match event.kind:
                    case "text-delta":
                        print(event.delta, end="")
                    case "tool-use":
                        print(f"Using tool: {event.tool_name}")
            ```
        """
        cmd = self._build_command(message)
        cwd = self.config.cwd or Path.cwd()
        env = os.environ.copy()

        # Create temp config file for MCP servers if needed
        config_file_path: Path | None = None
        if self._mcp_servers:
            mcp_config = self._generate_mcp_config()
            fd, config_file_name = tempfile.mkstemp(suffix=".yaml", text=True)
            config_file_path = Path(config_file_name)
            with os.fdopen(fd, "w") as f:
                f.write(mcp_config)
            env["KODELET_CONFIG"] = str(config_file_path)

        # Set up hook scripts
        hook_scripts = self._setup_hooks(cwd)

        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=cwd,
                env=env,
            )

            try:
                assert process.stdout is not None
                async for line in process.stdout:
                    line_str = line.decode().strip()
                    if not line_str:
                        continue

                    try:
                        data = json.loads(line_str)
                        event = parse_event(data)

                        # Track conversation ID
                        if event.conversation_id and not self._conversation_id:
                            self._conversation_id = event.conversation_id

                        yield event

                    except json.JSONDecodeError:
                        continue

                await process.wait()

                if process.returncode != 0:
                    assert process.stderr is not None
                    stderr = await process.stderr.read()
                    raise KodeletError(
                        f"kodelet exited with code {process.returncode}: {stderr.decode()}"
                    )

            except asyncio.CancelledError:
                process.terminate()
                await process.wait()
                raise
        finally:
            # Clean up temp config file
            if config_file_path:
                config_file_path.unlink(missing_ok=True)

            # Clean up hook scripts
            self._cleanup_hooks(hook_scripts)

    async def run(self, message: str) -> str:
        """Send a query and return the complete response.

        This is a convenience method that collects all text events and
        returns the complete response as a string.

        Args:
            message: The query to send to kodelet

        Returns:
            The complete text response

        Example:
            ```python
            response = await agent.run("What is 2+2?")
            print(response)
            ```
        """
        result: list[str] = []
        async for event in self.query(message):
            if event.kind == "text":
                from .events import TextEvent

                assert isinstance(event, TextEvent)
                result.append(event.content)
        return "".join(result)
