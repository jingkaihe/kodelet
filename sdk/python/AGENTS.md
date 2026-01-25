# AGENTS.md - Kodelet Python SDK

## Project Overview

Python SDK for [Kodelet](https://github.com/jingkaihe/kodelet) - an AI-assisted software engineering CLI. This SDK wraps the `kodelet` binary and provides an async Python interface for programmatic access.

**Key Features**: Async streaming via generators, conversation management, lifecycle hooks (Python DSL), MCP server integration.

## Project Structure

```
sdk/python/
├── src/kodelet/           # Main package
│   ├── __init__.py        # Public API exports
│   ├── client.py          # Main Kodelet client
│   ├── config.py          # KodeletConfig dataclass
│   ├── events.py          # Streaming event types
│   ├── conversation.py    # Conversation management
│   ├── hooks.py           # Hook decorator and manager
│   ├── mcp.py             # MCP server configs (Stdio/SSE)
│   └── exceptions.py      # Custom exceptions
├── tests/                 # pytest test suite
├── examples/              # Usage examples
└── pyproject.toml         # Project config (hatch build)
```

## Tech Stack

| Component | Tool/Version |
|-----------|--------------|
| Python | 3.12+ |
| Package Manager | uv |
| Build System | hatchling |
| Testing | pytest, pytest-asyncio |
| Type Checking | mypy (strict mode) |
| Linting | ruff |
| Dependencies | Zero runtime deps (stdlib only) |

## Key Commands

```bash
# Install with dev dependencies
uv sync --all-extras

# Run unit tests (default - excludes integration)
uv run pytest

# Run tests with coverage
uv run pytest --cov=kodelet

# Run integration tests only (requires kodelet binary + API keys)
uv run pytest -m integration

# Run all tests (unit + integration)
uv run pytest -m ""

# Type checking (strict mode)
uv run mypy src/kodelet

# Linting
uv run ruff check src/kodelet

# Format code
uv run ruff format src/kodelet
```

## Testing Conventions

- **Unit tests**: Mock external dependencies, run by default
- **Integration tests**: Marked with `@pytest.mark.integration`, require `kodelet` binary and API keys
- **Async tests**: Use `pytest-asyncio` with auto mode (`asyncio_mode = "auto"`)
- **Test files**: `tests/test_<module>.py` naming pattern

## Code Conventions

### Type Annotations
- **Strict mypy**: All code must pass `mypy --strict`
- **Modern syntax**: Use `X | None` instead of `Optional[X]`, `list[str]` instead of `List[str]`
- **Type aliases**: Define at module level (e.g., `HookFunc = Callable[[dict[str, Any]], dict[str, Any]]`)

### Data Structures
- **Prefer dataclasses** for data containers (`@dataclass`)
- **Use `field(default_factory=list)`** for mutable defaults
- **Abstract base classes** for interfaces (`ABC`, `@abstractmethod`)

### Async Patterns
- **Async generators** for streaming: `async def query(...) -> AsyncGenerator[Event, None]`
- **asyncio.create_subprocess_exec** for CLI wrapper
- **try/finally** for cleanup in async context

### Docstrings
Use Google-style docstrings with Args, Returns, Raises, Example:
```python
def example(param: str) -> int:
    """Short description.

    Longer description if needed.

    Args:
        param: Description of parameter

    Returns:
        Description of return value

    Raises:
        ValueError: When param is invalid

    Example:
        ```python
        result = example("test")
        ```
    """
```

### Naming
- **snake_case**: Functions, methods, variables
- **PascalCase**: Classes, type aliases
- **UPPER_CASE**: Module-level constants
- **Private**: Prefix with `_` (e.g., `_HOOK_PREFIX`)

### Import Organization
1. Standard library imports
2. Third-party imports (none in this project)
3. Local package imports (relative: `from .module import ...`)

### Pattern Matching
Use `match`/`case` for event type dispatch:
```python
match event.kind:
    case "text-delta":
        print(event.delta)
    case "tool-use":
        print(f"Using: {event.tool_name}")
```

### Error Handling
- **Custom exceptions** inherit from `KodeletError`
- **Specific exceptions** for specific error cases (`BinaryNotFoundError`, `ConversationNotFoundError`)
- **contextlib.suppress** for ignoring expected exceptions

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     User Application                         │
└──────────────────────────┬──────────────────────────────────┘
                           │
┌──────────────────────────▼──────────────────────────────────┐
│                    Kodelet (client.py)                       │
│  - Wraps kodelet binary                                      │
│  - Manages subprocess lifecycle                              │
│  - Streams JSON events from --headless mode                  │
└──────────────────────────┬──────────────────────────────────┘
        │                  │                    │
┌───────▼───────┐  ┌───────▼───────┐  ┌────────▼────────┐
│ HookManager   │  │ MCPServer     │  │ Conversation    │
│ (hooks.py)    │  │ (mcp.py)      │  │ Manager         │
│               │  │               │  │ (conversation.py)│
│ Generates     │  │ StdioServer   │  │                 │
│ hook scripts  │  │ SSEServer     │  │ list/show/      │
│ at runtime    │  │ → YAML config │  │ delete/fork     │
└───────────────┘  └───────────────┘  └─────────────────┘
        │
┌───────▼────────────────────────────────────────────────────┐
│                    kodelet binary                           │
│                    (subprocess)                             │
│    kodelet run --headless --stream-deltas "query"          │
└────────────────────────────────────────────────────────────┘
```

## ruff Configuration

```toml
[tool.ruff]
target-version = "py312"
line-length = 100

[tool.ruff.lint]
select = ["E", "F", "I", "N", "W", "UP", "B", "C4", "SIM"]
```

Key rules:
- **E/W**: pycodestyle errors/warnings
- **F**: pyflakes
- **I**: isort (import sorting)
- **N**: pep8-naming
- **UP**: pyupgrade (modern Python syntax)
- **B**: flake8-bugbear
- **C4**: flake8-comprehensions
- **SIM**: flake8-simplify

## Adding New Features

1. **New event type**: Add dataclass to `events.py`, update `parse_event()`, export in `__init__.py`
2. **New config option**: Add field to `KodeletConfig`, handle in `_build_command()`
3. **New hook type**: Add to `HookType` enum in `hooks.py`
4. **New exception**: Subclass `KodeletError` in `exceptions.py`

## SDK Binary Protocol

The SDK communicates with `kodelet run --headless --stream-deltas` which outputs newline-delimited JSON:
```json
{"kind": "text-delta", "delta": "Hello", "conversation_id": "..."}
{"kind": "tool-use", "tool_name": "Bash", "input": "{...}"}
{"kind": "tool-result", "result": "..."}
```

Hooks are generated as Python scripts in `.kodelet/hooks/` that implement:
- `./hook hook` → prints hook type
- `./hook run` → reads JSON from stdin, prints JSON result
