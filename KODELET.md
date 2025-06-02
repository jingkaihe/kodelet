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

## Key Commands

```bash
# Core commands
kodelet run "query"                    # One-shot execution
kodelet chat                           # Interactive mode
kodelet watch                          # File watcher

# Conversation management
kodelet conversation list|show|delete  # Manage conversations
kodelet run --resume ID "more"         # Continue conversation

# Git integration
kodelet commit [--no-confirm|--short]  # AI commit messages
kodelet pr [--target main]             # Generate PRs
kodelet resolve --issue-url URL        # Resolve GitHub issues

# Image support (Claude only)
kodelet run --image path.png "query"   # Single/multiple images

# Development
make build|test|lint|format|release    # Standard dev commands
```

## Configuration

**Environment Variables**:
```bash
# API Keys
export ANTHROPIC_API_KEY="sk-ant-api..."  # Claude models
export OPENAI_API_KEY="sk-..."            # OpenAI models

# Core settings
export KODELET_PROVIDER="anthropic|openai"
export KODELET_MODEL="claude-sonnet-4-0|gpt-4.1"
export KODELET_MAX_TOKENS="8192"
export KODELET_LOG_LEVEL="info"
```

**Config File** (`config.yaml`):
```yaml
provider: "anthropic"
model: "claude-sonnet-4-0"
max_tokens: 8192
weak_model: "claude-3-5-haiku-latest"
log_level: "info"

# MCP servers (optional)
mcp:
  servers:
    fs:
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/path"]
```

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

## Image Input Support

Vision support for Claude models only. Supports local files (JPEG, PNG, GIF, WebP) and HTTPS URLs. Max 10 images, 5MB each.

```bash
kodelet run --image diagram.png "analyze this"
kodelet run --image file1.png --image file2.png "compare these"
```
