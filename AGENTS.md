# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It supports Anthropic Claude, OpenAI, and Google GenAI APIs to process user queries and execute various tools through an agentic workflow.

## Project Structure
```
cmd/kodelet/     # CLI commands
pkg/             # Core packages
  ├── auth/      # Authentication
  ├── binaries/  # External binary management (ripgrep, fd)
  ├── conversations/  # Conversation storage (SQLite)
  ├── fragments/ # Fragment/recipe templates
  ├── llm/       # LLM clients (anthropic/, google/, openai/)
  ├── plugins/   # Unified plugin system
  ├── skills/    # Agentic skills system
  ├── tools/     # Tool implementations
  ├── webui/     # Web UI (React/TypeScript SPA embedded in binary)
  └── ...        # logger/, presenter/, sysprompt/, telemetry/, types/, utils/
docs/            # Documentation (MANUAL.md, DEVELOPMENT.md, SKILLS.md, etc.)
adrs/            # Architecture Decision Records
skills/          # Built-in skills
recipes/         # Sample fragment/recipe templates
```

## Tech Stack
**Backend**: Go 1.25.6, Cobra/Viper (CLI), Logrus (logging), SQLite (modernc.org/sqlite), OpenTelemetry
**Frontend**: React 18, TypeScript, Vite, Vitest, Tailwind CSS, DaisyUI
**LLM SDKs**: Anthropic v1.13.0, OpenAI v1.41.2, Google GenAI v1.25.0, MCP v0.29.0
**Tools**: mise (task runner), Docker

## Build System
All commands use `mise run <task>`. Frontend is embedded via `go generate ./pkg/webui`.

## Engineering Principles
1. **Always run linting**: `mise run lint` (Go), `mise run eslint` (frontend)
2. **Write tests**: Use testify for Go, Vitest for frontend
3. **Document CLI changes**: Update docs when CLI interface changes

## Testing
```bash
mise run test                    # All Go tests
mise run e2e-test-docker         # Acceptance tests in Docker
mise run frontend-test           # Frontend tests
```

**Use testify** for assertions (`assert.Equal`, `require.NotNil`) over `t.Errorf`/`t.Fatalf`.

## Key Commands
```bash
# Core
kodelet run "query"              # One-shot execution
kodelet serve                    # Web UI server (localhost:8080)
kodelet run -r recipe-name       # Use recipe template
kodelet run --follow "continue"  # Continue recent conversation

# Git integration
kodelet commit                   # AI commit messages
kodelet pr [--target main]       # Generate PRs
kodelet issue-resolve --issue-url URL

# Development
mise run build|test|lint|format  # Standard commands
mise run build-dev               # Fast build (skip frontend)
```

See [docs/MANUAL.md](docs/MANUAL.md) for complete reference.

## Configuration
Layered: env vars → global (`~/.kodelet/config.yaml`) → repo (`kodelet-config.yaml`)

```bash
# Required API keys
export ANTHROPIC_API_KEY="sk-ant-api..."
export OPENAI_API_KEY="sk-..."
export GOOGLE_API_KEY="your-api-key"  # or GOOGLE_CLOUD_PROJECT for Vertex AI

# Common settings
export KODELET_PROVIDER="anthropic|openai|google"
export KODELET_MODEL="claude-sonnet-4-6|gpt-4.1|gemini-2.5-pro"
```

See [`config.sample.yaml`](./config.sample.yaml) for all options.

## LLM Architecture
Uses `Thread` abstraction for all LLM providers: message history, tool execution, handler-based responses, provider-specific features (extended thinking, reasoning effort), and token tracking.

## Error Handling
**Use pkg/errors** over fmt.Errorf for stack traces:
```go
return errors.Wrap(err, "failed to validate config")
return errors.Wrapf(err, "failed to process file %s", filename)
```

## Logging & CLI Output
```go
// Diagnostics - use logger package
logger.G(ctx).WithField("key", "value").Info("message")

// User-facing - use presenter package
presenter.Success("Done")  // Green ✓
presenter.Error(err, "Failed")  // Red [ERROR]
presenter.Warning("Caution")  // Yellow ⚠
```

**Logger** = diagnostics/debug. **Presenter** = user interaction.

## Agentic Skills
Model-invoked capabilities at `.kodelet/skills/<name>/SKILL.md` (repo), `~/.kodelet/skills/<name>/SKILL.md` (global), or via plugins.

- **Automatic invocation**: Model decides when relevant
- **Disable**: `--no-skills` flag or `skills.enabled: false` in config
- **Built-in**: `kodelet` skill (CLI usage guide)

See [docs/SKILLS.md](docs/SKILLS.md).

## Plugin System
```bash
kodelet plugin add user/repo      # Install locally
kodelet plugin add user/repo -g   # Install globally
kodelet plugin list               # List all plugins
kodelet plugin remove user/repo   # Remove plugin
```

Plugins stored as `org@repo` format. See [ADR 028](adrs/028-unified-plugin-system.md).

## Agent Lifecycle Hooks
Scripts at `.kodelet/hooks/` or `~/.kodelet/hooks/` that intercept agent operations.

- **Types**: `before_tool_call`, `after_tool_call`, `user_message_send`, `agent_stop`
- **Disable**: `--no-hooks` flag

See [docs/HOOKS.md](docs/HOOKS.md).

## External Binary Management
Managed binaries in `~/.kodelet/bin/`: ripgrep (15.1.0), fd (10.3.0). Auto-downloaded with checksum verification; falls back to system binaries if unavailable.

## Resources
- [docs/MANUAL.md](docs/MANUAL.md) - CLI reference
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) - Development guide
- [docs/SKILLS.md](docs/SKILLS.md) - Skills system
- [docs/HOOKS.md](docs/HOOKS.md) - Hooks system
- [docs/FRAGMENTS.md](docs/FRAGMENTS.md) - Template system
- [docs/mcp.md](docs/mcp.md) - MCP integration
- [adrs/](adrs/) - Architecture decisions
