# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It supports both Anthropic Claude and OpenAI APIs to process user queries and execute various tools.

## Project Structure
```
├── .github/             # GitHub configuration
├── .dockerignore        # Docker ignore file
├── .gitignore           # Git ignore file
├── adrs/                # Architecture Decision Records
├── bin/                 # Compiled binaries
├── cmd/                 # Application entry point
│   └── kodelet/         # Main application command
├── config.sample.yaml   # Sample configuration file
├── Dockerfile           # Docker configuration
├── docs/                # Documentation files
├── go.mod               # Go module file
├── go.sum               # Go dependencies checksum
├── install.sh           # Installation script
├── KODELET.md           # Project documentation
├── LICENSE              # License file
├── Makefile             # Build automation
├── pkg/                 # Core packages
│   ├── conversations/   # Conversation storage and management
│   ├── llm/             # LLM client for AI interactions
│   │   ├── anthropic/   # Anthropic Claude API client
│   │   └── openai/      # OpenAI API client
│   ├── logger/          # Context-aware structured logging
│   ├── sysprompt/       # System prompt configuration
│   │   └── templates/   # Prompt templates
│   │       └── components/ # Template components
│   │           └── examples/ # Template for the prompt examples
│   ├── telemetry/       # Telemetry components
│   ├── tools/           # Tool implementations
│   ├── tui/             # Terminal UI components
│   ├── types/           # Common types
│   │   ├── llm/         # LLM related types
│   │   └── tools/       # Tool related types
│   ├── utils/           # Utility functions
│   └── version/         # Version information
├── README.md            # Project overview
├── RELEASE.md           # Release notes
├── tests/               # Test files
│   └── acceptance/      # Acceptance tests
└── VERSION.txt          # Version information file
```

The codebase follows a modular structure with separation of concerns between LLM communication, tools, UI, and core functionality.

## Tech Stack
- **Go 1.24.2** - Programming language
- **Anthropic SDK** - For Claude AI integration (v0.2.0-beta.3)
- **OpenAI SDK** - For GPT models integration
- **Logrus** - Structured logging library
- **Charm libraries** - TUI components
- **Cobra & Viper** - CLI commands and configuration
- **Docker** - For containerization

## Engineering Principles

All development work must follow these core principles:

1. **Always run linting**: Make sure you run `make lint` after you finish any work to ensure code quality and consistency.
2. **Write comprehensive tests**: Always write tests for new features you add, and regression tests for changes you make to existing functionality.
3. **Document CLI changes**: Always document when you have changed the CLI interface to maintain clear usage documentation.

## Testing

```bash
make test # Run all tests
make e2e-test-docker # Run acceptance tests in Docker
go test ./pkg/... # Run tests for a specific package
go test -v -cover ./pkg/... ./cmd/... # Run tests with coverage
```

## Key Commands

For comprehensive usage documentation and examples, see [./docs/MANUAL.md](./docs/MANUAL.md).

```bash
# Core commands
kodelet run "query"                    # One-shot execution
kodelet chat                           # Interactive mode
kodelet watch                          # File watcher

# Conversation management
kodelet conversation list|show|delete  # Manage conversations
kodelet run --resume ID "more"         # Continue specific conversation
kodelet run --follow "continue"        # Continue most recent conversation
kodelet chat --follow                  # Resume most recent in chat mode

# Git integration
kodelet commit [--no-confirm|--short]  # AI commit messages
kodelet pr [--target main]             # Generate PRs
kodelet issue-resolve --issue-url URL        # Resolve GitHub issues

# PR management
kodelet pr-respond --pr-url URL                           # Respond to latest @kodelet mention
kodelet pr-respond --pr-url URL --review-id ID    # Respond to review comment
kodelet pr-respond --pr-url URL --issue-comment-id ID     # Respond to issue comment

# Image support
kodelet run --image path.png "query"   # Single/multiple images
kodelet run --image file1.png --image file2.png "compare these"

# Development
make build|test|lint|format|release    # Standard dev commands
```

## Configuration

Kodelet uses a layered configuration approach with environment variables, global config (`~/.kodelet/config.yaml`), and repository-specific config (`kodelet-config.yaml`).

**Required API Keys**:
```bash
export ANTHROPIC_API_KEY="sk-ant-api..."  # For Claude models
export OPENAI_API_KEY="sk-..."            # For OpenAI models
```

**Common Environment Variables**:
```bash
export KODELET_PROVIDER="anthropic|openai"
export KODELET_MODEL="claude-sonnet-4-0|gpt-4.1"
export KODELET_MAX_TOKENS="8192"
export KODELET_LOG_LEVEL="info"
```

For complete configuration options including tracing, model settings, and environment variable overrides, see [`config.sample.yaml`](./config.sample.yaml).

## LLM Architecture

Kodelet uses a `Thread` abstraction for all interactions with LLM providers (Anthropic Claude and OpenAI):
- Maintains message history and state
- Handles tool execution and responses
- Uses a handler-based pattern for processing responses
- Supports provider-specific features (thinking for Claude, reasoning effort for OpenAI)

The architecture provides a unified approach for both interactive and one-shot uses with token usage tracking for all API calls across different providers.

## Logger Package

Context-aware structured logging using [logrus](https://github.com/sirupsen/logrus) with automatic context propagation.

### Key APIs
- **`logger.G(ctx)`**: Get logger from context (ALWAYS use this)
- **`logger.WithLogger(ctx, logger)`**: Store logger in context
- **`log.WithField(key, value)`**: Add contextual field to logger

### Usage
```go
// Basic usage
log := logger.G(ctx)
log.Info("Processing request")

// Add context fields
log = log.WithField("request_id", id)
ctx = logger.WithLogger(ctx, log)

// always use structured logging
// GOOD:
log.WithField("request_id", id).Info("Processing request")
// BAD
log.Info("Processing request %s", id)
```
