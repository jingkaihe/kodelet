# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It supports Anthropic Claude, OpenAI, and Google GenAI APIs to process user queries and execute various tools through an agentic workflow.

## Project Structure
```
├── .github/             # GitHub configuration
│   └── workflows/       # CI/CD workflows (release, tests, Docker builds)
├── adrs/                # Architecture Decision Records (18 ADRs documenting key decisions)
├── bin/                 # Compiled binaries
├── cmd/                 # Application entry point
│   └── kodelet/         # Main application command (30 command files)
├── config.sample.yaml   # Sample configuration file
├── docs/                # Documentation files
├── Dockerfile           # Docker runtime image configuration
├── Dockerfile.cross-build # Docker cross-compilation environment
├── .dockerignore        # Docker ignore patterns
├── install.sh           # Installation script
├── AGENTS.md            # Project documentation (this file)
├── kodelet-config.yaml  # Repository-specific MCP tools configuration
├── LICENSE              # License file
├── mise.toml            # Tool management and task automation
├── pkg/                 # Core packages
│   ├── auth/            # Authentication and login management
│   ├── binaries/        # External binary management (ripgrep, etc.)
│   ├── conversations/   # Conversation storage and management
│   │   ├── sqlite/      # SQLite storage implementation (pure Go)
│   │   ├── service.go   # Main conversation service
│   │   ├── factory.go   # Store factory for different backends
│   │   └── store.go     # Store interface definitions
│   ├── feedback/        # Feedback system for autonomous conversations
│   ├── fragments/       # Fragment/recipe template system
│   │   └── recipes/     # Built-in recipe templates
│   │       └── github/  # GitHub-specific recipes
│   ├── skills/          # Agentic skills system (model-invoked capabilities)
│   ├── github/          # GitHub Actions templates and utilities
│   │   └── templates/   # GitHub workflow templates
│   ├── ide/             # IDE integration tools (for kodelet-tools)
│   ├── llm/             # LLM client for AI interactions
│   │   ├── anthropic/   # Anthropic Claude API client
│   │   ├── google/      # Google GenAI API client (Gemini & Vertex AI)
│   │   ├── openai/      # OpenAI API client
│   │   │   └── preset/  # OpenAI model presets
│   │   │       ├── openai/  # OpenAI model presets
│   │   │       └── xai/     # X.AI (Grok) model presets
│   │   └── prompts/     # Common LLM prompts
│   ├── llmstxt/         # LLM-friendly documentation (llms.txt)
│   ├── logger/          # Context-aware structured logging
│   ├── osutil/          # OS utilities and process management
│   ├── presenter/       # User-facing output and formatting
│   ├── sysprompt/       # System prompt configuration
│   │   └── templates/   # Prompt templates
│   │       └── components/ # Template components
│   │           └── examples/ # Example components
│   ├── telemetry/       # Telemetry and tracing components (OpenTelemetry)
│   ├── tools/           # Tool implementations (31 tool files)
│   │   └── renderers/   # Tool output renderers
│   ├── tui/             # Terminal UI components (Bubble Tea)
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
- **Anthropic SDK v1.13.0** - For Claude AI integration
- **OpenAI SDK v1.41.2** - For GPT and compatible models (including X.AI Grok)
- **Google GenAI SDK v1.25.0** - For Google Gemini and Vertex AI integration
- **MCP v0.29.0** - Model Context Protocol for AI tool integration
- **SQLite (modernc.org/sqlite)** - Pure Go database implementation
- **Logrus** - Structured logging library
- **Charm libraries** - TUI components (Bubble Tea, Lipgloss, Bubbles)
- **Cobra & Viper** - CLI commands and configuration
- **React 18 & TypeScript** - Web UI frontend
- **Vite** - Frontend build tool
- **Vitest** - Frontend testing framework
- **Tailwind CSS & DaisyUI** - Frontend styling
- **OpenTelemetry** - Distributed tracing and telemetry
- **Docker** - For containerization
- **mise** - Tool version management and task automation

## Build System & Tool Management

The project uses [mise](https://mise.jdx.dev/) for tool version management and task automation. This ensures consistent tool versions across all development environments.

All development commands use `mise run <task>`. The `mise.toml` file defines all available tasks and manages tool versions automatically.

## Frontend Bundling

The web UI is a React/TypeScript SPA built with Vite and embedded directly into the Go binary:

**Frontend Stack**: React 18, TypeScript, Tailwind CSS, DaisyUI, React Router, Vite, Vitest

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

1. **Always run linting**: Make sure you run `mise run lint` after you finish any work to ensure code quality and consistency. The lint command runs go vet, golangci-lint (with staticcheck, unparam, ineffassign, nilnil, unconvert, misspell, revive) and standalone staticcheck with all checks enabled for comprehensive analysis. For frontend changes, also run `mise run eslint` to check TypeScript/React code quality.
2. **Write comprehensive tests**: Always write tests for new features you add, and regression tests for changes you make to existing functionality. Use testify for Go tests and Vitest for frontend tests.
3. **Document CLI changes**: Always document when you have changed the CLI interface to maintain clear usage documentation.

## Testing

```bash
mise run test                # Run all Go tests
mise run e2e-test-docker     # Run acceptance tests in Docker
go test ./pkg/...            # Run tests for a specific package
go test -v -cover ./pkg/... ./cmd/...  # Run tests with coverage

# Frontend testing
mise run frontend-test              # Run frontend tests
mise run frontend-test-watch        # Run frontend tests in watch mode
mise run frontend-test-ui           # Run frontend tests with interactive UI
mise run frontend-test-coverage     # Run frontend tests with coverage
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
kodelet serve [--host HOST] [--port PORT]  # Web UI server (default: localhost:8080)

# Fragment/Recipe system (see docs/FRAGMENTS.md)
kodelet run -r init                    # Bootstrap AGENTS.md for repository
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

# Anthropic multi-account management
kodelet anthropic login --alias work   # Login with account alias
kodelet anthropic logout               # Logout from Anthropic
kodelet anthropic accounts list        # List all accounts (default marked with *)
kodelet anthropic accounts default     # Show current default account
kodelet anthropic accounts default <alias>  # Set default account
kodelet anthropic accounts rename <old> <new>  # Rename an account alias
kodelet anthropic accounts remove <alias>   # Remove an account
kodelet run --account work "query"     # Use specific account for run
kodelet chat --account work            # Use specific account for chat

# PR management
kodelet pr-respond --pr-url URL                           # Respond to latest @kodelet mention
kodelet pr-respond --pr-url URL --review-id ID    # Respond to review comment
kodelet pr-respond --pr-url URL --issue-comment-id ID     # Respond to issue comment

# Ralph - Autonomous development loop (see docs/RALPH.md)
kodelet ralph                          # Run autonomous feature development loop
kodelet ralph init                     # Initialize PRD from project analysis
kodelet ralph --prd features.json      # Use custom PRD file
kodelet ralph --iterations 50          # Run for more iterations

# Image support
kodelet run --image path.png "query"   # Single/multiple images
kodelet run --image file1.png --image file2.png "compare these"

# LLM-friendly documentation
kodelet llms.txt                       # Display LLM-friendly usage guide
# Web endpoint: http://localhost:8080/llms.txt (when running kodelet serve)

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

# Google GenAI (choose one authentication method)
export GOOGLE_API_KEY="your-api-key"      # For Gemini API
# OR for Vertex AI:
export GOOGLE_CLOUD_PROJECT="your-project"
export GOOGLE_CLOUD_LOCATION="us-central1"
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account.json"
```

**Common Environment Variables**:
```bash
export KODELET_PROVIDER="anthropic|openai|google"
export KODELET_MODEL="claude-sonnet-4-5-20250929|claude-opus-4-5-20251101|gpt-4.1|grok-3|gemini-2.5-pro"
export KODELET_MAX_TOKENS="8192"
export KODELET_LOG_LEVEL="info"
```

For complete configuration options including tracing, model settings, and environment variable overrides, see [`config.sample.yaml`](./config.sample.yaml).

## LLM Architecture

Kodelet uses a `Thread` abstraction for all interactions with LLM providers (Anthropic Claude, OpenAI, and Google GenAI):
- Maintains message history and state
- Handles tool execution and responses
- Uses a handler-based pattern for processing responses
- Supports provider-specific features (extended thinking for Claude, reasoning effort for OpenAI, thinking for Google)
- Token usage tracking for all API calls across different providers

The architecture provides a unified approach for both interactive and one-shot uses with comprehensive observability through OpenTelemetry tracing.

### Base Thread Package

The `pkg/llm/base` package provides shared functionality for all LLM provider implementations using Go's struct embedding pattern. Provider-specific Thread structs (Anthropic, OpenAI, Google) embed `*base.Thread` to inherit common behavior:

- **Shared fields**: Config, State, Usage, ConversationID, Store, ToolResults, HookTrigger, SubagentContextFactory
- **Shared methods**: GetState/SetState, GetConfig, GetUsage, EnablePersistence, ShouldAutoCompact, CreateMessageSpan/FinalizeMessageSpan
- **Shared constants**: MaxImageFileSize (5MB), MaxImageCount (10)

This composition-based approach reduces code duplication (~300 lines per provider) while preserving provider-specific behaviors. See `pkg/llm/base/doc.go` for comprehensive documentation and ADR 023 for the architectural decision record.

## Error Handling

**Always prefer pkg/errors over fmt.Errorf** for error wrapping:

```go
import "github.com/pkg/errors"

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
logger.G(ctx).WithError(err).Error("Failed to process request")

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
- Colors auto-detect terminal/CI with `KODELET_COLOR` override (`always`, `never`, `auto`)
- Quiet mode support via `presenter.SetQuiet(true)`

### CLI Error Handling
Handle errors with user feedback:
```go
if err != nil {
    presenter.Error(err, "Operation failed")
    return errors.Wrap(err, "failed to process")
}
```

## Agentic Skills

Kodelet supports model-invoked skills that package domain expertise into discoverable capabilities:

- **Location**: `.kodelet/skills/<name>/SKILL.md` (repo) or `~/.kodelet/skills/<name>/SKILL.md` (global)
- **Invocation**: Automatic - model decides when skills are relevant to the task
- **Configuration**: `skills.enabled` and `skills.allowed` in config
- **CLI**: `--no-skills` flag to disable skills for a session

Skills differ from fragments/recipes: skills are model-invoked (automatic), while fragments are user-invoked (explicit).

See [docs/SKILLS.md](docs/SKILLS.md) for creating custom skills.

## Agent Lifecycle Hooks

Kodelet supports lifecycle hooks that allow external scripts to observe and control agent behavior:

- **Location**: `.kodelet/hooks/` (repo) or `~/.kodelet/hooks/` (global)
- **Hook types**: `before_tool_call`, `after_tool_call`, `user_message_send`, `agent_stop`
- **Protocol**: Executables responding to `hook` (type) and `run` (execution) commands
- **CLI**: `--no-hooks` flag to disable hooks for a session

Hooks differ from skills: hooks intercept agent operations (automatic), while skills provide domain expertise (model-invoked).

See [docs/HOOKS.md](docs/HOOKS.md) for creating custom hooks.

## External Binary Management

Kodelet manages external binary dependencies (like ripgrep and fd) through the `pkg/binaries` package:

- **Location**: Binaries are installed to `~/.kodelet/bin/`
- **Version tracking**: Each binary has a `.version` file to track installed versions
- **Automatic download**: On startup, kodelet downloads required binaries from GitHub releases
- **Checksum verification**: All downloads are verified using SHA256 checksums
- **Fallback behavior**: If download fails (no network, firewall), falls back to system-installed binaries

### Currently Managed Binaries

| Binary | Version | Platforms | Usage |
|--------|---------|-----------|-------|
| ripgrep (`rg`) | 15.1.0 | darwin/linux/windows (amd64, arm64) | `grep_tool` for code search |
| fd (`fd`) | 10.3.0 | darwin/linux/windows (amd64, arm64) | `glob_tool` for file finding |

### Fallback Behavior

1. Check if managed binary exists with correct version in `~/.kodelet/bin/`
2. If not, attempt to download from GitHub releases
3. If download fails, fall back to system-installed binary (e.g., `rg` in PATH)
4. If neither available, the dependent tool reports an error

This ensures kodelet works in:
- **Normal environments**: Downloads and uses managed binary
- **Air-gapped/corporate networks**: Uses pre-installed system binary
- **CI environments**: Works with pre-installed tools

## Resources

- **Documentation**: See `docs/` directory for comprehensive guides
- **ADRs**: See `adrs/` for architectural decision records
- **User Manual**: [docs/MANUAL.md](docs/MANUAL.md) for complete CLI reference
- **Development Guide**: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for setup and workflows
- **Fragments Guide**: [docs/FRAGMENTS.md](docs/FRAGMENTS.md) for template system
- **Skills Guide**: [docs/SKILLS.md](docs/SKILLS.md) for agentic skills system
- **Hooks Guide**: [docs/HOOKS.md](docs/HOOKS.md) for agent lifecycle hooks system
- **MCP Tools**: [docs/mcp.md](docs/mcp.md) for Model Context Protocol integration
- **ACP Integration**: [docs/ACP.md](docs/ACP.md) for Agent Client Protocol IDE integration

