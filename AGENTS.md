# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It supports Anthropic Claude and OpenAI APIs to process user queries and execute various tools through an agentic workflow.

## Project Structure
```
cmd/kodelet/     # CLI commands
pkg/             # Core packages
  ├── auth/      # Authentication
  ├── binaries/  # External binary management (ripgrep, fd)
  ├── controlplane/  # Central HTTP API, auth, chat, and runner coordination
  ├── conversations/  # Conversation storage (SQLite)
  ├── delegation/ # Scoped child execution authority and presets
  ├── fragments/ # Fragment/recipe templates
  ├── llm/       # LLM clients (anthropic/, openai/)
  ├── plugins/   # Unified plugin system
  ├── skills/    # Agentic skills system
  ├── tools/     # Tool implementations
  ├── webui/     # Embedded React/TypeScript SPA and HTTP handler
  └── ...        # logger/, presenter/, sysprompt/, telemetry/, types/, utils/
docs/            # Documentation (MANUAL.md, SKILLS.md, design docs, etc.)
skills/          # Built-in skills
  └── kodelet/   # Kodelet skill docs, references, and examples
recipes/         # Sample fragment/recipe templates
```

## Tech Stack
**Backend**: Go 1.26.5, Cobra/Viper (CLI), Logrus (logging), SQLite (modernc.org/sqlite), OpenTelemetry
**Frontend**: React 19, TypeScript, Vite, Vitest, Tailwind CSS, DaisyUI
**LLM SDKs**: Anthropic v1.13.0, OpenAI v1.41.2, MCP v0.29.0
**Tools**: mise (task runner), Docker

## Build System
All commands use `mise run <task>`. Frontend is embedded via `go generate ./pkg/webui`.

## Engineering Principles
1. **Always run linting**: `mise run lint` (Go), `mise run frontend-lint` (frontend)
2. **Write tests**: Use testify for Go, Vitest for frontend
3. **Document CLI changes**: Update docs when CLI interface changes
4. **Do not hard-wrap Markdown prose**: Keep each prose paragraph on a single source line
5. **Do not implement token environment or argv scrubbing**: The agent and its tools share the trusted process environment and can inspect it, so selective scrubbing is not a meaningful security boundary.
6. **Supported targets are Linux and macOS on amd64 and arm64**: Do not add Windows or other platform-specific implementations unless explicitly requested.
7. **Use sentence case in the Web UI**: Avoid all-caps interface copy and CSS `text-transform: uppercase`; reserve uppercase for codes and established acronyms.

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
kodelet serve                    # Required daemon plus embedded runner (localhost:8080)
kodelet run -r recipe-name       # Use recipe template
kodelet run --follow --cwd "$PWD" "continue"  # Continue scoped daemon history

# Git integration
kodelet commit                   # AI commit messages
kodelet pr [--target main]       # Generate PRs

# Development
mise run build|test|lint|format  # Standard commands
mise run build-dev               # Fast build (skip frontend)
```

See [docs/MANUAL.md](docs/MANUAL.md) for complete reference.

## Configuration
Trusted process defaults come from the user configuration (`~/.kodelet/config.yaml`), an explicit configuration file, environment and flags. Repository `kodelet-config.yaml` is loaded only by the runner for the execution CWD and only for permitted workspace settings; it cannot configure daemon models, credentials or endpoints. Daemon model profiles and runner environment profiles are separate namespaces. Ordinary CLI/TUI/ACP commands use the daemon and never initialize a local provider or conversation database; explicitly retained library-local ACP APIs are not a client fallback.

Embedded runner environment preference precedence (lowest to highest) is daemon base/defaults → selected daemon model profile's environment subset → explicit `serve.runner_settings` → permitted per-CWD repository settings → selected trusted environment profile → request narrowing. Explicit runner settings override preferences such as tool mode, prompt, filesystem search, context, and bash, and may narrow permissions. Selected daemon model/base `allowed_tools`, `allowed_commands`, `skills.enabled: false`, and `extensions.enabled: false` remain mandatory ceilings as on normal remote runs: intersect, deny wins, never last-write-wins. Runner settings and environment profiles cannot relax these ceilings; the embedded loader applies `inherited.EnvironmentOptions` before discovery to match actual runs. Repository settings cannot widen other effective trusted constraints (`skills.allowed`, extension allow/deny and per-tool settings, `allowed_domains_file`) or define either kind of profile.

`ProfileConfigLoader` resolves model profile identifiers against locally pinned environment projections (`default` uses base, blank uses the daemon's active default, named selects it). Saved conversations whose named model profile was removed retain their snapshotted model identity and fall back to base environment, not the active named default; unknown names still fail for new conversations. Model/provider configuration and credentials are not passed in runner settings. Both embedded and standalone runners resolve trusted `environment_profiles`; standalone preferences remain runner-owned while daemon restrictions still apply. Trusted embedded defaults/profile definitions are pinned at startup, require a `kodelet serve` restart to change, and are isolated from per-run and concurrent settings mutations. See [Workspace-bound Runners](docs/MANUAL.md#workspace-bound-runners) for the inherited fields and full contract.

```bash
# Provider API keys belong to the daemon environment
export ANTHROPIC_API_KEY="sk-ant-api..."
export OPENAI_API_KEY="sk-..."

# Common settings
export KODELET_PROVIDER="anthropic|openai"
export KODELET_MODEL="claude-sonnet-4-6|gpt-4.1"
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

Plugins stored as `org@repo` format.

## Extensions
Long-running executable extensions at `.kodelet/extensions/` or `~/.kodelet/extensions/` can register model tools, prompt commands, dynamic recipes, and lifecycle event handlers over stdio JSON-RPC.

- **Discovery**: executables named `kodelet-extension-*` directly under an extension root or one level below it
- **Events**: `user.message`, `agent.init`, `agent.start`, `turn.start`, `tool.call`, `tool.result`, `turn.end`, `agent.end`, plus session lifecycle events
- **Disable**: `--no-extensions` flag or `extensions.enabled: false` in config

See [docs/extension-design.md](docs/extension-design.md).

Discovery helpers:
```bash
kodelet extension list
kodelet extension inspect <name-or-id-or-path>
```

## External Binary Management
Managed binaries in `~/.kodelet/bin/`: ripgrep (15.2.0), fd (10.3.0). Auto-downloaded with checksum verification for standalone installs; packaged Linux builds bundle them in `/usr/libexec/kodelet/` and resolution prefers that location before falling back to managed or system binaries.

## Resources
- [docs/MANUAL.md](docs/MANUAL.md) - CLI reference
- [docs/SKILLS.md](docs/SKILLS.md) - Skills system
- [docs/extension-design.md](docs/extension-design.md) - Extension system design
- [docs/FRAGMENTS.md](docs/FRAGMENTS.md) - Template system
- [docs/mcp.md](docs/mcp.md) - MCP integration
