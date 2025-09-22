# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It supports both Anthropic Claude and OpenAI APIs to process user queries and execute various tools.

## Project Structure
```
├── .github/             # GitHub configuration
│   └── workflows/       # GitHub Actions workflows (5 workflow files)
├── adrs/                # Architecture Decision Records (18 ADRs)
├── bin/                 # Compiled binaries
├── cmd/                 # Application entry point
│   └── kodelet/         # Main application command (28 command files)
├── config.sample.yaml   # Sample configuration file
├── docs/                # Documentation files
├── Dockerfile           # Docker configuration
├── Dockerfile.cross-build # Docker cross-compilation configuration
├── .dockerignore        # Docker ignore patterns
├── install.sh           # Installation script
├── AGENTS.md            # Project documentation (this file)
├── kodelet-config.yaml  # Repository-specific configuration
├── LICENSE              # License file
├── mise.toml            # Tool management and task automation
├── pkg/                 # Core packages
│   ├── auth/            # Authentication and login management
│   ├── conversations/   # Conversation storage and management
│   │   ├── sqlite/      # SQLite storage implementation
│   │   ├── service.go   # Main conversation service
│   │   ├── factory.go   # Store factory for different backends
│   │   └── store.go     # Store interface definitions
│   ├── feedback/        # Feedback system for autonomous conversations
│   ├── fragments/       # Fragment/recipe template system
│   ├── github/          # GitHub Actions templates and utilities
│   ├── llm/             # LLM client for AI interactions
│   │   ├── anthropic/   # Anthropic Claude API client
│   │   ├── openai/      # OpenAI API client
│   │   │   └── preset/  # OpenAI model presets
│   │   │       ├── grok/    # Grok model presets
│   │   │       └── openai/  # OpenAI model presets
│   │   └── prompts/     # Common LLM prompts
│   ├── logger/          # Context-aware structured logging
│   ├── presenter/       # User-facing output and formatting
│   ├── sysprompt/       # System prompt configuration
│   │   └── templates/   # Prompt templates
│   │       └── components/ # Template components
│   │           └── examples/ # Example components
│   ├── telemetry/       # Telemetry and tracing components
│   ├── tools/           # Tool implementations (29 tool files)
│   │   └── renderers/   # Tool output renderers
│   ├── tui/             # Terminal UI components
│   ├── types/           # Common types
│   │   ├── conversations/ # Conversation related types
│   │   ├── llm/         # LLM related types
│   │   └── tools/       # Tool related types
│   ├── usage/           # Usage tracking and statistics
│   ├── utils/           # Utility functions
│   ├── version/         # Version information
│   └── webui/           # Web UI server and React frontend
│       ├── frontend/    # React/TypeScript SPA with Vite build
│       └── dist/        # Built frontend assets (embedded in binary)
├── README.md            # Project overview
├── recipes/             # Sample fragment/recipe templates
├── RELEASE.md           # Release notes
├── scripts/             # Build and utility scripts
├── tests/               # Test files
│   └── acceptance/      # Acceptance tests
└── VERSION.txt          # Version information file
```

The codebase follows a modular structure with separation of concerns between LLM communication, tools, UI, and core functionality.

## Tech Stack
- **Go 1.25.1** - Programming language
- **Node.js 22.17.0** - Frontend development and build tooling
- **Anthropic SDK v1.7.0** - For Claude AI integration
- **OpenAI SDK v1.40.0** - For GPT and compatible models
- **MCP v0.29.0** - Language server protocol for AI tool integration
- **SQLite** - Pure Go database implementation
- **Logrus** - Structured logging library
- **Charm libraries** - TUI components
- **Cobra & Viper** - CLI commands and configuration
- **React & TypeScript** - Web UI frontend
- **Docker** - For containerization
- **mise** - Tool version management and task automation

## Build System & Tool Management

The project uses [mise](https://mise.jdx.dev/) for tool version management and task automation. This ensures consistent tool versions across all development environments.

All development commands use `mise run <task>`. The `mise.toml` file defines all available tasks and manages tool versions automatically.

## Frontend Bundling

The web UI is a React/TypeScript SPA built with Vite and embedded directly into the Go binary:

**Frontend Stack**: React 18, TypeScript, Tailwind CSS, DaisyUI, React Router, Vite
**Build Process**:
- `go generate ./pkg/webui` triggers `npm install && npm run build` in frontend directory
- Vite builds optimized assets to `pkg/webui/dist/` directory
- Go's `//go:embed dist/*` directive embeds all built assets into the binary at compile time
- Web server serves embedded React SPA with `/api/*` endpoints for conversation management

**Development**: Use `mise run build-dev` to skip frontend build for faster Go-only builds.

The embedded approach eliminates external dependencies and ensures the web UI is always available with the binary.

For detailed frontend development workflow and commands, see [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## Engineering Principles

All development work must follow these core principles:

1. **Always run linting**: Make sure you run `mise run lint` after you finish any work to ensure code quality and consistency. The lint command runs go vet, golangci-lint and staticcheck with all checks enabled for comprehensive analysis. For frontend changes, also run `mise run eslint` to check TypeScript/React code quality.
2. **Write comprehensive tests**: Always write tests for new features you add, and regression tests for changes you make to existing functionality.
3. **Document CLI changes**: Always document when you have changed the CLI interface to maintain clear usage documentation.

## Testing

```bash
mise run test # Run all tests
mise run e2e-test-docker # Run acceptance tests in Docker
go test ./pkg/... # Run tests for a specific package
go test -v -cover ./pkg/... ./cmd/... # Run tests with coverage

# Frontend testing
mise run frontend-test # Run frontend tests
mise run frontend-test-watch # Run frontend tests in watch mode
mise run frontend-test-ui # Run frontend tests with UI
mise run frontend-test-coverage # Run frontend tests with coverage
```

### Testing Conventions

**Prefer testify assert and require over t.Errorf and t.Fatalf**:

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// Good - use testify for assertions
assert.Equal(t, expected, actual)
assert.NoError(t, err)
require.NotNil(t, result) // require stops execution on failure

// Bad - avoid t.Errorf and t.Fatalf
if expected != actual {
    t.Errorf("expected %v, got %v", expected, actual)
}
if err != nil {
    t.Fatalf("unexpected error: %v", err)
}
```

## Key Commands

For comprehensive usage documentation and examples, see [./docs/MANUAL.md](./docs/MANUAL.md).

```bash
# Core commands
kodelet run "query"                    # One-shot execution
kodelet chat                           # Interactive mode
kodelet watch                          # File watcher
kodelet serve [--host HOST] [--port PORT] # Web UI server (default: localhost:8080)

# Fragment/Receipt system (see docs/FRAGMENTS.md)
kodelet run -r fragment-name           # Use fragment/recipe template
kodelet run -r fragment --arg key=value  # Fragment with arguments
kodelet run -r fragment "additional instructions"  # Fragment with extra context

# Conversation management
kodelet conversation list|show|delete  # Manage conversations
kodelet conversation edit [--editor editor] [--edit-args "args"] ID  # Edit conversation JSON
kodelet run --resume ID "more"         # Continue specific conversation
kodelet run --follow "continue"        # Continue most recent conversation
kodelet chat --follow                  # Resume most recent in chat mode

# Feedback system
kodelet feedback --conversation-id ID "message"  # Send feedback to specific conversation
kodelet feedback --follow "message"             # Send feedback to most recent conversation

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
mise run build|test|lint|format|release    # Standard dev commands
mise run build-dev                          # Fast build without frontend assets
mise run dev-server                         # Start development server with auto-reload
mise run cross-build                        # Cross-compile for multiple platforms
mise run cross-build-docker                 # Cross-compile using Docker (recommended)
mise run eslint                            # Run frontend linting
mise run eslint-fix                        # Run frontend linting with auto-fix

# Dependency management (handled automatically by mise)
mise install                            # Install all tools and dependencies
mise run install                        # Install Go modules and npm dependencies
mise run install-npm                    # Install npm dependencies for frontend only

# For detailed build instructions and release process, see docs/DEVELOPMENT.md
```

## Configuration

Kodelet uses a layered configuration approach with environment variables, global config (`~/.kodelet/config.yaml`), and repository-specific config (`kodelet-config.yaml`).

**Required API Keys**:
```bash
export ANTHROPIC_API_KEY="sk-ant-api..."  # For Claude models
export OPENAI_API_KEY="sk-..."            # For OpenAI models (also for compatible APIs)
export OPENAI_API_BASE="https://api.x.ai/v1"  # Optional: Custom API endpoint for OpenAI-compatible providers
```

**Common Environment Variables**:
```bash
export KODELET_PROVIDER="anthropic|openai"
export KODELET_MODEL="claude-sonnet-4-20250514|gpt-4.1|grok-3"
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

## Error Handling

**Always prefer pkg/errors over fmt.Errorf** for error wrapping:

```go
// Good - pkg/errors provides stack traces and better error context
return errors.Wrap(err, "failed to validate config")
return errors.Wrapf(err, "failed to process file %s", filename)
return errors.New("configuration is invalid")

// Bad - fmt.Errorf loses stack trace information
return fmt.Errorf("failed to validate config: %w", err)
return fmt.Errorf("failed to process file %s: %w", filename, err)
```

## Logging & CLI Output

### Structured Logging
Always use the logger package with context for diagnostics:
```go
import "github.com/jingkaihe/kodelet/pkg/logger"

// Good - structured logging for diagnostics
logger.G(ctx).WithField("command", "commit").Info("Starting operation")

// Bad - never use fmt.Printf or log.Printf for diagnostics
fmt.Printf("Processing request for %s", userID)
```

### CLI Output
Use the presenter package for all user-facing output:
```go
import "github.com/jingkaihe/kodelet/pkg/presenter"

// User feedback
presenter.Success("Operation completed")    // Green ✓
presenter.Error(err, "Failed to commit")   // Red [ERROR]
presenter.Warning("No changes detected")   // Yellow ⚠
presenter.Info("Processing files...")      // Plain text
presenter.Section("Results")              // Bold header
presenter.Stats(usageStats)               // Formatted stats
```

**Key principles:**
- Logger = diagnostics (debug, structured data)
- Presenter = user interaction (progress, results, errors)
- Colors auto-detect terminal/CI with `KODELET_COLOR` override
- Quiet mode support via `presenter.SetQuiet(true)`

### CLI Error Handling
Handle errors with both user feedback and logging:
```go
if err != nil {
    presenter.Error(err, "Operation failed")           // For user
    return errors.Wrap(err, "failed to process")
}
```
