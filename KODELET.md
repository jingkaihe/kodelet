# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It uses the Anthropic Claude API to process user queries and execute various tools.

## Project Structure
```
├── .github/             # GitHub configuration
│   └── workflows/       # GitHub Actions workflows
├── adrs/                # Architecture Decision Records
├── bin/                 # Compiled binaries
├── cmd/
│   └── kodelet/         # Application entry point
├── docs/                # Documentation files
│   └── DEVELOPMENT.md   # Development guidelines and information
└── pkg/
    ├── llm/             # LLM client for AI interactions
    │   ├── anthropic/   # Anthropic-specific client implementation
    │   └── types/       # Common types and interfaces for LLM clients
    ├── state/           # State management for the application
    ├── sysprompt/       # System prompt configuration and templates
    ├── tools/           # Tool implementations (bash, file operations, etc.)
    ├── tui/             # Terminal User Interface components
    ├── utils/           # Utility functions and helpers
    └── version/         # Version information
```

The codebase follows a modular structure with clear separation of concerns:
- Core application logic in the `cmd/kodelet` directory with separate files for different execution modes
- State management interfaces and implementations in the `pkg/state` package
- System prompt configuration in the `pkg/sysprompt` package
- LLM communication is handled in the `pkg/llm` package with a conversational Thread abstraction
  - The `pkg/llm/anthropic` package contains the Anthropic-specific client implementation
  - The `pkg/llm/types` package defines common interfaces and types used across LLM implementations
- Tools for executing various operations in the `pkg/tools` package (bash, file operations, code search, todo management, etc.)
- Terminal user interface components in the `pkg/tui` package for interactive chat mode
- Common utilities and helper functions in the `pkg/utils` package
- Version information in the `pkg/version` package

## Tech Stack
- **Go 1.24.2** - Programming language
- **Anthropic SDK** - For Claude AI integration (anthropics/anthropic-sdk-go v0.2.0-beta.3)
- **Charm libraries** - TUI components (bubbletea, bubbles, lipgloss)
- **Cobra & Viper** - CLI commands and configuration
- **Docker** - For containerization

## Key Commands

### CLI Commands

#### One-shot Mode (Run)
Execute a single query and exit:
```bash
kodelet run "your query"
```

#### Interactive Chat Mode
Start an interactive chat session with a modern TUI interface:
```bash
kodelet chat
```

Start with plain command-line interface instead of TUI:
```bash
kodelet chat --plain
```

List all saved conversations:
```bash
kodelet chat list
```

Delete a specific conversation:
```bash
kodelet chat delete <conversation-id>
```

#### Version Information
Display version information in JSON format:
```bash
kodelet version
```

#### Watch Mode
Continuously monitor file changes and provide AI assistance:
```bash
kodelet watch
```

Watch specific file types only:
```bash
kodelet watch --include "*.go"
```

Available options:
- `--ignore` or `-i`: Directories to ignore (default: `.git,node_modules`)
- `--include` or `-p`: File pattern to include (e.g., `*.go`, `*.{js,ts}`)
- `--verbosity` or `-v`: Verbosity level (`quiet`, `normal`, `verbose`)
- `--debounce` or `-d`: Debounce time in milliseconds (default: 500)

### Building & Running

#### Build the application
```bash
make build
```

#### Build for multiple platforms
```bash
make cross-build
```

#### Build the Docker image
```bash
make docker-build
```

#### Run locally (one-shot mode)
```bash
make build
./bin/kodelet run "your query"
```

#### Run locally (interactive chat mode)
```bash
make build
./bin/kodelet chat
```

### Development

#### Run tests
```bash
make test
```

#### Format code
```bash
make format
```

#### Lint code
```bash
make lint
```

#### Create a release
```bash
make release
```

#### Display help information
```bash
make help
```

## Coding Conventions
- Use Go's standard formatting rules (enforced by `go fmt`)
- Follow standard Go error handling patterns
- Tools implement the `Tool` interface defined in pkg/tools/tools.go
- State is managed through the `State` interface in pkg/state/state.go
- Function and variable names use camelCase
- Type names use PascalCase
- Always run `make format && make lint` after finishing code changes to ensure code style compliance

## Configuration
Kodelet uses Viper for configuration management. You can configure Kodelet in several ways:

1. **Environment Variables** - All environment variables should be prefixed with `KODELET_`:
   ```bash
   export ANTHROPIC_API_KEY="sk-ant-api..."
   export KODELET_MODEL="claude-3-7-sonnet-latest"
   export KODELET_MAX_TOKENS="8192"
   ```

2. **Configuration File** - Kodelet looks for a configuration file named `config.yaml` in:
   - Current directory
   - `$HOME/.kodelet/` directory

Example `config.yaml`:
```yaml
# Anthropic model to use
model: "claude-3-7-sonnet-latest"

# Maximum tokens for responses
max_tokens: 8192
```

3. **Command Line Flags** - Override configuration options via command line flags:
   ```bash
   kodelet run --model "claude-3-opus-20240229" --max-tokens 4096 "your query"
   ```

## LLM Architecture

Kodelet uses a `Thread` abstraction for all interactions with the Anthropic Claude API:

### Thread
The `Thread` type in `pkg/llm/thread.go` represents a conversation thread with Claude:
- Maintains message history and state
- Handles tool execution and responses
- Uses a handler-based pattern for processing responses

### MessageHandler Interface
The `MessageHandler` interface defines how message events are processed:
```go
type MessageHandler interface {
    HandleText(text string)
    HandleToolUse(toolName string, input string)
    HandleToolResult(toolName string, result string)
    HandleDone()
}
```

### Handler Implementations
- `ConsoleMessageHandler`: Prints messages directly to the console
- `ChannelMessageHandler`: Sends messages through a channel for the TUI
- `StringCollectorHandler`: Collects text responses into a string

This architecture provides a unified approach for both interactive and one-shot uses.

### Token Usage Tracking
Kodelet tracks token usage from all LLM API calls, including:
- Regular input tokens
- Output tokens
- Cache write input tokens
- Cache read input tokens
- Total tokens

All commands (chat, run, commit) display detailed token usage statistics upon completion, helping users monitor API usage and cost.
