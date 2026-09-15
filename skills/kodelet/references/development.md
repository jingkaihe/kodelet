# Developing Kodelet

Use this reference when changing Kodelet itself. Repository-wide conventions live in `AGENTS.md`; paths and commands below are relative to the repository root.

## Code map

- `cmd/kodelet/`: Cobra/Viper CLI commands.
- `pkg/controlplane/`, `pkg/auth/`, `pkg/conversations/`: daemon API, runner coordination, authentication, and SQLite history.
- `pkg/llm/`, `pkg/tools/`, `pkg/browser/`, `pkg/artifacts/`: provider threads, tool execution, runner-owned browsers, and persisted images.
- `pkg/fragments/`, `pkg/skills/`, `pkg/plugins/`: recipes, skills, and plugins.
- `pkg/webui/`: HTTP handler and embedded React/TypeScript SPA; frontend source is in `frontend/`.
- `skills/kodelet/`, `recipes/`, `docs/`: built-in guidance, examples, prompt templates, and documentation.

Toolchain versions live in `mise.toml`; dependencies live in `go.mod` and `pkg/webui/frontend/package.json` rather than being repeated here.

## Build and verification

Run commands from the repository root:

| Command | Purpose |
| --- | --- |
| `mise run build` | Build the CLI with regenerated, embedded frontend assets. |
| `mise run build-dev` | Build the CLI without regenerating the frontend. |
| `mise run code-generation` | Run `go generate ./pkg/webui` to install/build the frontend. |
| `mise run format` | Format Go with gofumpt. |
| `mise run e2e-test-docker` | Run Docker acceptance tests. |
| `mise run frontend-icons` | Regenerate PNG icons from `logo.svg`; requires uv and Cairo. |

Standard lint/test tasks are listed in `AGENTS.md`. For a focused Go check, use `mise exec -- go test ./pkg/<package>`.

Live Anthropic tests are opt-in and incur API charges. Set `ANTHROPIC_API_KEY`, then explicitly enable them:

```bash
KODELET_ANTHROPIC_INTEGRATION_TESTS=1 mise exec -- go test -count=1 ./pkg/llm ./pkg/llm/anthropic
```

## Architecture notes

- CLI/TUI/ACP execution uses the daemon; runners own workspace access and tools. Configure models and credentials on the daemon and restart it after changing defaults. Repository `kodelet-config.yaml` only configures runner workspace settings and cannot widen host permissions. Model `profiles` and runner `environment_profiles` are separate; see [configuration](configuration.md).
- LLM providers use the `Thread` abstraction for history, tool execution, response handlers, reasoning features, and token tracking.
- `pkg/binaries/` manages checksum-verified ripgrep/fd downloads in `~/.kodelet/bin/`. Packaged Linux builds prefer bundled binaries in `/usr/libexec/kodelet/` before managed or system binaries.

## Detailed docs

In the repository, consult `docs/MANUAL.md` and `config.sample.yaml` for CLI/configuration, `docs/SKILLS.md` for skills, `docs/FRAGMENTS.md` for recipes, `docs/extension-design.md` for extensions, and `docs/mcp.md` for MCP.
