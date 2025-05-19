# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It uses the Anthropic Claude API to process user queries and execute various tools.

## Project Structure
```
├── .github/             # GitHub configuration
├── adrs/                # Architecture Decision Records
├── bin/                 # Compiled binaries
├── cmd/                 # Application entry point
│   └── kodelet/         # Main application command
├── docs/                # Documentation files
└── pkg/                 # Core packages
    ├── conversations/   # Conversation storage and management
    ├── llm/             # LLM client for AI interactions
    │   └── anthropic/   # Anthropic Claude API client
    ├── sysprompt/       # System prompt configuration
    │   └── templates/   # Prompt templates
    │       └── components/ # Template components
    │           └── examples/ # Template for the prompt examples
    ├── telemetry/       # Telemetry components
    ├── tools/           # Tool implementations
    ├── tui/             # Terminal UI components
    ├── types/           # Common types
    │   ├── llm/         # LLM related types
    │   └── tools/       # Tool related types
    ├── utils/           # Utility functions
    └── version/         # Version information
```

The codebase follows a modular structure with separation of concerns between LLM communication, tools, UI, and core functionality.

## Tech Stack
- **Go 1.24.2** - Programming language
- **Anthropic SDK** - For Claude AI integration (v0.2.0-beta.3)
- **Charm libraries** - TUI components
- **Cobra & Viper** - CLI commands and configuration
- **Docker** - For containerization

## Key Commands

### CLI Commands

#### One-shot Mode
```bash
# Basic one-shot query
kodelet run "your query"

# One-shot query with conversation persistence
kodelet run "your query"                     # saved automatically
kodelet run --resume CONVERSATION_ID "more"  # continue a conversation
kodelet run --no-save "temporary query"      # don't save the conversation
```

#### Interactive Chat Mode
```bash
kodelet chat
kodelet chat --plain
kodelet chat list
kodelet chat delete <conversation-id>
```

#### Watch Mode
```bash
kodelet watch [--include "*.go"] [--ignore ".git,node_modules"] [--verbosity level] [--debounce ms]
```

### Development Commands
```bash
make build          # Build the application
make cross-build    # Build for multiple platforms
make docker-build   # Build Docker image
make test           # Run tests
make format         # Format code
make lint           # Lint code
make release        # Create a release
make help           # Display help
```

## Configuration

1. **Environment Variables**:
   ```bash
   export ANTHROPIC_API_KEY="sk-ant-api..."
   export KODELET_MODEL="claude-3-7-sonnet-latest"
   export KODELET_MAX_TOKENS="8192"
   ```

2. **Configuration File** (`config.yaml`):
   ```yaml
   model: "claude-3-7-sonnet-latest"
   max_tokens: 8192
   ```

3. **Command Line Flags**:
   ```bash
   kodelet run --model "claude-3-opus-20240229" --max-tokens 4096 "query"
   ```

## LLM Architecture

Kodelet uses a `Thread` abstraction for all interactions with the Anthropic Claude API:
- Maintains message history and state
- Handles tool execution and responses
- Uses a handler-based pattern for processing responses

The architecture provides a unified approach for both interactive and one-shot uses with token usage tracking for all API calls.
