# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It supports both Anthropic Claude and OpenAI APIs to process user queries and execute various tools.

## Project Structure
```
├── .github/             # GitHub configuration
│   └── workflows/       # GitHub Actions workflows (4 workflow files)
├── adrs/                # Architecture Decision Records (13 ADRs)
├── bin/                 # Compiled binaries
├── cmd/                 # Application entry point
│   └── kodelet/         # Main application command (15+ command files)
├── config.sample.yaml   # Sample configuration file
├── docs/                # Documentation files
├── Dockerfile           # Docker configuration
├── install.sh           # Installation script
├── KODELET.md           # Project documentation (this file)
├── kodelet-config.yaml  # Repository-specific configuration
├── LICENSE              # License file
├── Makefile             # Build automation
├── pkg/                 # Core packages
│   ├── conversations/   # Conversation storage and management
│   ├── github/          # GitHub Actions templates and utilities
│   ├── llm/             # LLM client for AI interactions
│   │   ├── anthropic/   # Anthropic Claude API client
│   │   └── openai/      # OpenAI API client
│   ├── logger/          # Context-aware structured logging
│   ├── sysprompt/       # System prompt configuration
│   │   └── templates/   # Prompt templates
│   │       └── components/ # Template components
│   ├── telemetry/       # Telemetry and tracing components
│   ├── tools/           # Tool implementations (20+ tools)
│   │   └── browser/     # Browser automation tools package
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

## Logging
Always use the logger package with context:
```go
import "github.com/jingkaihe/kodelet/pkg/logger"

// Good
logger.G(ctx).WithField("user_id", userID).Info("Processing request")

// Bad - never use fmt.Printf or log.Printf
fmt.Printf("Processing request for %s", userID)
```

### Error Handling
```go
// Return errors with context
if err != nil {
    return fmt.Errorf("failed to process: %w", err)
}
```

## Code Intelligence & MCP Language Server Tools

Kodelet integrates with MCP (Model Context Protocol) Language Server to provide advanced code intelligence capabilities. **ALWAYS prioritize these MCP tools over basic text search (grep/find) for code navigation and understanding.**

### Core MCP Tools

#### Symbol Navigation
- **`mcp_definition`**: Get complete source code definition of any symbol (functions, types, constants, methods)
- **`mcp_references`**: Find all usages and references of a symbol throughout the entire codebase
- **`mcp_hover`**: Get type information, documentation, and context for symbols at specific positions

#### Code Modification
- **`mcp_rename_symbol`**: Safely rename symbols and update all references across the codebase
- **`mcp_edit_file`**: Apply multiple precise text edits to files with line-based targeting
- **`mcp_diagnostics`**: Get compiler/linter diagnostics and errors for specific files

### When to Use MCP Tools vs Basic Search

**Use MCP tools for:**
- Finding function/type definitions: `mcp_definition` instead of `grep_tool`
- Understanding symbol usage: `mcp_references` instead of `grep_tool`
- Refactoring code: `mcp_rename_symbol` instead of manual find/replace
- Getting type information: `mcp_hover` instead of guessing from context
- Checking code health: `mcp_diagnostics` instead of running linters manually
- Precise code edits: `mcp_edit_file` instead of `file_edit` for multiple changes

**Use basic search tools only for:**
- Searching for string literals, comments, or documentation
- Finding configuration patterns or non-code content
- Exploratory searches where you don't know exact symbol names

### Best Practices

1. **Start with MCP**: Always try MCP tools first for code-related queries
2. **Symbol-aware navigation**: Use `mcp_definition` and `mcp_references` to understand code relationships
3. **Safe refactoring**: Use `mcp_rename_symbol` for renaming to ensure all references are updated
4. **Diagnostic-driven fixes**: Use `mcp_diagnostics` to identify and prioritize code issues
5. **Precise editing**: Use `mcp_edit_file` for making multiple related changes in a single operation

### Example Workflows

```bash
# Understanding a function
1. mcp_definition "FunctionName"           # Get implementation
2. mcp_references "FunctionName"           # Find all usages
3. mcp_hover at usage locations            # Understand context

# Refactoring workflow
1. mcp_diagnostics for target files        # Check current issues
2. mcp_rename_symbol for safe renames      # Update symbols
3. mcp_edit_file for implementation changes # Apply changes
4. mcp_diagnostics again                   # Verify fixes

# Code review workflow
1. mcp_diagnostics on changed files        # Check for issues
2. mcp_references for modified symbols     # Understand impact
3. mcp_hover for type verification         # Ensure correctness
```

This approach provides language-aware code intelligence that understands Go syntax, semantics, and project structure, making code navigation and modification significantly more reliable than text-based search.
