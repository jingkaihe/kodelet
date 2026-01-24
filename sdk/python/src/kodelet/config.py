"""Configuration management for Kodelet SDK."""

from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class KodeletConfig:
    """Configuration for Kodelet client.

    This maps to the CLI flags available in `kodelet run`.
    """

    # LLM Configuration
    provider: str = "anthropic"
    model: str = "claude-sonnet-4-5-20250929"
    weak_model: str = "claude-haiku-4-5-20251001"
    max_tokens: int = 8192
    weak_model_max_tokens: int = 8192
    thinking_budget_tokens: int = 4048
    reasoning_effort: str = "medium"  # For OpenAI models (low, medium, high)

    # Tool Configuration
    allowed_tools: list[str] = field(default_factory=list)
    allowed_commands: list[str] = field(default_factory=list)

    # Execution Configuration
    cwd: Path | None = None  # Working directory for the agent
    max_turns: int = 50
    compact_ratio: float = 0.8
    disable_auto_compact: bool = False

    # Output Configuration
    stream_deltas: bool = True
    include_history: bool = False

    # Feature Flags
    no_skills: bool = False
    no_hooks: bool = False
    no_mcp: bool = False
    no_save: bool = False

    # Binary Path
    kodelet_path: Path | None = None  # Auto-detect if not specified

    # Anthropic-specific
    account: str | None = None  # Anthropic account alias

    # Images
    images: list[str] = field(default_factory=list)
