# Kodelet configuration

Kodelet uses layered configuration:

1. Environment variables.
2. Global config: `~/.kodelet/config.yaml`.
3. Repository config: `./kodelet-config.yaml`.

Repository config overrides global config. See `config.sample.yaml` in the repo for the complete schema.

## Provider setup

### Anthropic Claude

```bash
# OAuth-style login flow, no API key env var needed
kodelet anthropic-login

# Or use an API key
export ANTHROPIC_API_KEY="sk-ant-api..."

kodelet run --provider anthropic "query"
```

Common model aliases in examples include `sonnet-46`, `haiku-45`, and `opus-48`. Check current config/source for the latest alias mapping.

### OpenAI

```bash
export OPENAI_API_KEY="sk-..."
kodelet run --provider openai --model gpt-5 "query"
```

OpenAI supports reasoning effort values such as `none`, `minimal`, `low`, `medium`, `high`, and `xhigh` when supported by the selected model/API mode.

## Example config

```yaml
aliases:
  haiku-45: claude-haiku-4-5-20251001
  opus-48: claude-opus-4-8
  sonnet-46: claude-sonnet-4-6

profile: default
provider: anthropic
model: sonnet-46
weak_model: haiku-45
max_tokens: 16000
reasoning_effort: medium
anthropic:
  # Optional: force adaptive-thinking request plumbing for custom Anthropic model IDs.
  # adaptive_thinking: true
conversation_summary_mode: llm

profiles:
  openai:
    provider: openai
    model: gpt-5
    weak_model: gpt-5
    reasoning_effort: medium
    tool_mode: patch
    enable_fs_search_tools: false

```

Profiles are useful for switching model/provider/tool-mode combinations. Note that profile switching may be constrained by provider compatibility in a given command flow.

## Skills config

```yaml
skills:
  enabled: true
  allowed:
    - pdf
    - xlsx
```

Disable for one run:

```bash
kodelet run --no-skills "query"
kodelet acp --no-skills
```

## Extension config

See `references/extensions.md` for the full extension model. Minimal config:

```yaml
extensions:
  enabled: true
  global_dir: ~/.kodelet/extensions
  local_dir: ./.kodelet/extensions
  max_output_size: 102400
```

Disable for one run:

```bash
kodelet run --no-extensions "query"
kodelet acp --no-extensions
```

## Command and tool restrictions

Restrict bash commands:

```yaml
allowed_commands:
  - "ls *"
  - "pwd"
  - "git status"
  - "npm *"
```

Or with an environment variable:

```bash
export KODELET_ALLOWED_COMMANDS="ls *,pwd,git status"
```

Set the maximum timeout the bash tool can request. Default is `120s`:

```yaml
bash:
  timeout: 5m
```

Or:

```bash
export KODELET_BASH_TIMEOUT=5m
```

Restrict model tools for a run:

```bash
kodelet run --allowed-tools "file_read,grep_tool,bash" "analyze code"
```

## Conversation summaries

By default, Kodelet can use the weak model for persisted conversation titles. To use the first user message instead:

```bash
kodelet run --conversation-summary-mode first_message "query"
```

Config/env equivalents:

```yaml
conversation_summary_mode: first_message
```

```bash
export KODELET_CONVERSATION_SUMMARY_MODE=first_message
```
