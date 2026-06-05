---
name: kodelet
description: Kodelet CLI usage guide, commands, configuration, extensions, recipes, skills, and workflows. Use when users ask about kodelet features, commands, configuration options, or how to accomplish tasks with kodelet.
---

# Kodelet Guide

Kodelet is a lightweight agentic software-engineering CLI. This skill gives a quick orientation; deeper topics live in focused references.

## Reference map

For details, choose the reference that matches the task:

- `references/quick-start.md` — installation, core `kodelet run` usage, Web UI, ACP/IDE mode, image input, git commands, completions, common workflows.
- `references/conversations-streaming.md` — conversation list/show/stream/fork commands, headless JSON output, `--stream-deltas`, Streamlit examples.
- `references/configuration.md` — config files, profiles, model/provider setup, security restrictions, MCP, bash timeout.
- `references/recipes.md` — `AGENTS.md`, fragments/recipes, and subagent workflow recipes.
- `references/skills.md` — agentic skills, skill layout, configuration, and skill plugins.
- `references/extensions.md` — extension runtime, discovery, configuration, plugins, and operational behavior.
- `references/sdk.md` — TypeScript SDK agent sessions, extension authoring API, tools, commands/dynamic recipes, lifecycle events, UI helpers, and examples.

Examples now live with the skill under `examples/`:

- `examples/streamlit/` — Streamlit chatbot around `kodelet run --headless --stream-deltas`.
- `examples/streamlit-acp/` — Streamlit chatbot using Agent Client Protocol.
- `examples/extensions/workspace/` — TypeScript extension with an `ask_user_question` tool and bash approval policy.

When answering detailed or version-sensitive questions, prefer the current repository docs/source over memory. If the user asks to modify Kodelet itself, inspect the relevant code first and follow repository conventions from `AGENTS.md`.

## Essential commands

```bash
# One-shot agent run
kodelet run "your query"

# Continue the most recent conversation
kodelet run -f "continue the task" # same as --follow

# Resume a specific conversation
kodelet run --resume CONVERSATION_ID "more questions"

# Temporary or minimal runs
kodelet run --no-save "temporary query"
kodelet run --result-only "what is 2+2"
kodelet run --no-tools "what is the capital of France?"

# Interactive/IDE mode over Agent Client Protocol
kodelet acp

# Browser UI
kodelet serve
```

## Feature summary

- Context files: Kodelet automatically loads `AGENTS.md`; bootstrap one with `kodelet run -r init`.
- Recipes/fragments: User-invoked prompt templates in `./recipes/` or `~/.kodelet/recipes/`; list with `kodelet recipe list`, inspect with `kodelet recipe show <name>`, run with `kodelet run -r <name>`.
- Skills: Model-invoked domain guidance in `.kodelet/skills/<name>/SKILL.md`, plugins, or global skill dirs; disable with `--no-skills`.
- Workflows/subagents: Recipes with `workflow: true` can be delegated through the subagent tool; disable with `--no-workflows` or `--disable-subagent`.
- Extensions: Long-running subprocesses can register model tools, prompt commands/dynamic recipes, and lifecycle event handlers; inspect with `kodelet extension list` and disable with `--no-extensions`.
- Plugins: Install bundled skills, recipes, and extensions with `kodelet plugin add org/repo`; inspect with `kodelet plugin list` and `kodelet plugin show org/repo`.
- Conversations: Use `kodelet conversation list/show/stream/delete/fork` for persisted runs.
- Git helpers: `kodelet commit` generates commit messages; `kodelet pr` creates PRs.

## Quick decision guide

- User asks “how do I run/use/install Kodelet?” → use `references/quick-start.md`.
- User asks about config, providers, models, command/tool restrictions, MCP → use `references/configuration.md`.
- User asks about recipes, fragments, `AGENTS.md`, or subagent workflows → use `references/recipes.md`.
- User asks about agentic skills, skill layout, or skill plugins → use `references/skills.md`.
- User asks about extension discovery/runtime/config, plugins, or hooks/custom-tool replacement → use `references/extensions.md`.
- User asks about the TypeScript SDK, SDK agent sessions, SDK extension tools/commands/events, or SDK UI prompts → use `references/sdk.md`.
- User asks about history, JSON streams, programmatic usage, Streamlit examples → use `references/conversations-streaming.md`.
