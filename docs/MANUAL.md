# Kodelet User Manual

Kodelet is a lightweight agentic SWE Agent that runs as an interactive CLI tool in your terminal. It is capable of performing software engineering and production operating tasks.

## Table of Contents

- [Installation](#installation)
  - [Using Install Script](#using-install-script)
  - [Prerequisites](#prerequisites)
- [Updating](#updating)
- [Usage Modes](#usage-modes)
  - [One-shot Mode](#one-shot-mode)
  - [Interactive Chat Mode (ACP)](#interactive-chat-mode-acp)
  - [Web UI Server](#web-ui-server)
  - [Git Integration](#git-integration)
  - [Image Input Support](#image-input-support)
  - [Conversation Continuation](#conversation-continuation)
  - [Context Compaction](#context-compaction)
  - [Conversation Management](#conversation-management)
- [Streaming and Programmatic Access](#streaming-and-programmatic-access)
  - [Headless Mode](#headless-mode)
  - [Partial Message Streaming](#partial-message-streaming)
  - [Conversation Stream Command](#conversation-stream-command)
  - [StreamEntry JSON Schema](#streamentry-json-schema)
  - [Example Stream Output](#example-stream-output)
  - [Processing Stream Output](#processing-stream-output)
- [Agent Context Files](#agent-context-files)
  - [Creating Context Files](#creating-context-files)
  - [Context File Priority](#context-file-priority)
  - [Best Practices](#best-practices)
- [Shell Completion](#shell-completion)
  - [Setup Instructions](#setup-instructions)
  - [Additional Options](#additional-options)
- [Configuration](#configuration)
  - [Environment Variables](#environment-variables)
  - [Configuration File](#configuration-file)
  - [Command Line Flags](#command-line-flags)
- [Configuration Profiles](#configuration-profiles)
  - [Profile Definition](#profile-definition)
  - [Profile Management Commands](#profile-management-commands)
  - [Profile Usage](#profile-usage)
  - [Profile Precedence and Merging](#profile-precedence-and-merging)
  - [Special "Default" Profile](#special-default-profile)
- [Security Configuration](#security-configuration)
  - [Bash Command Restrictions](#bash-command-restrictions)
- [LLM Providers](#llm-providers)
  - [Anthropic Claude](#anthropic-claude)
  - [OpenAI](#openai)
- [Anthropic Multi-Account Authentication](#anthropic-multi-account-authentication)
  - [Logging In with Multiple Accounts](#logging-in-with-multiple-accounts)
  - [Managing Accounts](#managing-accounts)
  - [Using Accounts at Runtime](#using-accounts-at-runtime)
  - [Account Status](#account-status)
- [Extensions](#extensions)
  - [TypeScript Agent SDK](#typescript-agent-sdk)
  - [Creating TypeScript Extensions](#creating-typescript-extensions)
  - [Requesting User Input from Extensions](#requesting-user-input-from-extensions)
  - [Extension Discovery](#extension-discovery)
  - [Extension Commands and Dynamic Recipes](#extension-commands-and-dynamic-recipes)
  - [Extension Events](#extension-events)
  - [Extensions Configuration](#extensions-configuration)
- [Agentic Skills](#agentic-skills)
  - [How Skills Work](#how-skills-work)
  - [Creating Skills](#creating-skills)
  - [Skills Configuration](#skills-configuration)
  - [Disabling Skills](#disabling-skills)
- [Key Features](#key-features)
- [Security & Limitations](#security--limitations)
  - [Image Input Security](#image-input-security)
  - [General Security](#general-security)
- [Troubleshooting](#troubleshooting)
  - [Common Issues](#common-issues)

## Installation

### Using Install Script

```bash
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash

# Force standalone binary install
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash -s -- --binary
```

By default the install script uses package-based installation: Homebrew on macOS and `.deb`/`.rpm` packages on Linux.

### Prerequisites

For running locally or building from source:
- Go 1.24 or higher

## Usage Modes

Kodelet supports several usage modes depending on your needs.

### One-shot Mode

Perfect for quick queries and automation:

```bash
# Basic one-shot query
kodelet run "your query"

# One-shot query with conversation persistence
kodelet run "your query"                     # saved automatically
kodelet run --resume CONVERSATION_ID "more"  # continue a conversation
kodelet run --follow "continue most recent"  # continue the most recent conversation
kodelet run -f "quick follow-up"             # short form
kodelet run --no-save "temporary query"      # don't save the conversation

# Output only the final result (suppresses intermediate output and usage stats)
kodelet run --result-only "what is 2+2"      # outputs just: 4

# Disable all tools (for simple query-response usage)
kodelet run --no-tools "what is the capital of France?"

# Enable filesystem search tools (glob_tool and grep_tool) instead of fd/rg via bash
kodelet run --enable-fs-search-tools "find references to SessionManager"

# Set a persistent thread goal for goal-directed work
kodelet run "/goal finish the migration and verify tests pass"

# Headless mode for programmatic use
kodelet run --headless "your query"          # outputs structured JSON stream
kodelet run --headless --include-history "query"  # include historical data in stream
```

### Thread Goals

Use `/goal <objective>` in CLI, ACP, or the Web UI to set an active goal for the current thread. While the goal is active, Kodelet keeps future turns focused on that objective, including after conversation resume or compaction. The agent marks the goal complete when it is done, or blocked if it cannot make meaningful progress without user input.

### Terminal Chat TUI

For a minimal terminal UI, use `kodelet chat`:

```bash
kodelet chat                         # start a new TUI conversation
kodelet chat --follow                # continue the most recent conversation
kodelet chat -f                      # short form
kodelet chat --resume CONVERSATION_ID  # resume a specific conversation
kodelet chat -r CONVERSATION_ID        # short form
kodelet chat --theme tokyo-night        # switch from the default catppuccin-mocha theme
kodelet chat --no-tools              # chat without tools
kodelet chat --no-extensions         # disable extensions
```

The TUI uses `catppuccin-mocha` by default. Use `--theme` to switch to another
available theme such as `tokyo-night`. It streams assistant responses, collapses
thinking and tool details by default, and lets you toggle details with `ctrl+o`
or by clicking the detail header. It uses the same chat runner as the Web UI, so
conversations are persisted and can be resumed by ID. While the assistant is
working, the composer stays editable; press `Enter` to queue the typed text as
steering for the active conversation. Kodelet applies queued steering on the next
model API call.

### Interactive Chat Mode (ACP)

For extended conversations and complex tasks, use the Agent Client Protocol (ACP) with a compatible client like `toad`:

```bash
toad acp 'kodelet acp'             # Start interactive chat via ACP
```

The ACP mode provides a rich interactive experience with features like:
- Real-time streaming responses
- Tool execution visualization
- Conversation persistence
- Multi-turn conversations

### Web UI Server

Start the browser-based chat UI with:

```bash
kodelet serve
```

By default, `kodelet serve` generates a random access token and prints a URL like
`http://localhost:8080?token=...`. Opening that URL stores the token in an
HTTP-only cookie for subsequent same-browser requests. You can also supply a
stable token explicitly:

```bash
kodelet serve --auth-token "your-secret-token"
```

Explicit tokens may contain only letters, numbers, and URL-safe punctuation
(`-._~`) so they can be stored safely in the browser auth cookie.

Same-origin Web UI requests do not require CORS. Browser requests from loopback
origins are allowed by default for local development. To allow additional
browser origins, pass `--cors-origins` with a comma-separated list:

```bash
kodelet serve --cors-origins https://app.example.com,https://admin.example.com
```

For trusted local-only use, disable the web UI token gate with:

```bash
kodelet serve --skip-auth
```

### Git Integration

Generate meaningful commit messages using AI:

```bash
kodelet commit
```

This command analyzes your staged changes (`git diff --cached`) and uses AI to generate a meaningful commit message following conventional commits format. You must stage your changes using `git add` before running this command.

Options:
- `--no-sign`: Disable commit signing (commits are signed by default)
- `--template` or `-t`: Use a template for the commit message
- `--short`: Generate a short commit message (enabled by default)
- `--prefix`: Prefix the generated commit message, such as `TICKET-123`
- `--no-confirm`: Skip confirmation prompt
- `--save`: Enable conversation persistence (disabled by default for commits)

Create pull requests:

```bash
kodelet pr
```

### Image Input Support

Kodelet supports image inputs for vision-enabled models (currently Anthropic Claude models only). You can provide images through local file paths or HTTPS URLs.

```bash
# Single image analysis
kodelet run --image /path/to/screenshot.png "What's wrong with this UI?"

# Multiple images (local and remote)
kodelet run --image ./diagram.png --image https://example.com/mockup.jpg "Compare these designs"

# Architecture diagram analysis
kodelet run --image ./architecture.png "Review this system architecture and suggest improvements"

# Steer a running conversation with image context
kodelet steer --conversation-id 20231201T120000-a1b2c3d4e5f67890 --image ./screenshot.png "Use this screenshot as context"
```

**Supported Features:**
- **Local Images**: JPEG, PNG, GIF, and WebP formats
- **Remote Images**: HTTPS URLs only (for security)
- **Multiple Images**: Up to 10 images per message
- **Size Limits**: Maximum 5MB per image file
- **Steering**: `kodelet steer --image/-I` supports text + images; image-only steering is not supported.
- **Provider Support**: Anthropic Claude models (OpenAI support planned)

### Conversation Continuation

Continue previous conversations seamlessly:

```bash
# Continue the most recent conversation
kodelet run --follow "continue working on the feature"
kodelet run -f "what's the status?"

# Continue a specific conversation by ID
kodelet run --resume CONVERSATION_ID "more questions"
```

**Note**: The `--follow` and `--resume` flags cannot be used together. If no conversations exist when using `--follow`, a new conversation will be started with a warning message.

### Context Compaction

As conversations grow longer, they may approach the context window limit. Kodelet automatically compacts context when utilization exceeds a configured threshold (default 80%). Compaction generates a comprehensive summary of the conversation history and replaces the active context with that summary, preserving essential details while reducing token usage.

```bash
# Use the default threshold
kodelet run --follow "continue working on the feature"

# Override the threshold for this invocation
kodelet --compact-ratio 0.9 run --follow "continue working on the feature"
```

Auto-compaction uses the shared `compact_ratio` configuration in CLI, ACP, and web UI server modes. Configure it via `--compact-ratio`, `compact_ratio` in config, or `KODELET_COMPACT_RATIO` in the environment. The ratio must be greater than `0.0` and less than or equal to `1.0`. Manual context compaction recipes are no longer supported.

### Conversation Management

Manage your conversation history:

```bash
# List conversations
kodelet conversation list
kodelet conversation list --search "term" --sort-by "updated" --sort-order "desc"

# View conversation details
kodelet conversation show <conversation-id>
kodelet conversation show <conversation-id> --format [text|markdown|json|raw]

# Stream conversation updates in real-time
kodelet conversation stream <conversation-id>
kodelet conversation stream <conversation-id> --include-history

# Delete conversations
kodelet conversation delete <conversation-id>
kodelet conversation delete --no-confirm <conversation-id>
```

### Database Management

Manage the kodelet database and migrations:

```bash
# Show migration status
kodelet db status

# Rollback the last migration (with confirmation prompt)
kodelet db rollback

# Rollback without confirmation (use with caution)
kodelet db rollback --no-confirm
```

## Streaming and Programmatic Access

Kodelet provides structured JSON streaming capabilities for programmatic integration, enabling you to build custom UIs, monitoring tools, and automation pipelines.

### Headless Mode

The `--headless` flag transforms `kodelet run` into a JSON streaming service, outputting structured data instead of formatted console text:

```bash
# Stream JSON output for a new query
kodelet run --headless "analyze this codebase"

# Include historical conversation data in the stream
kodelet run --headless --include-history "continue the analysis"

# Continue a specific conversation in headless mode
kodelet run --headless --resume CONVERSATION_ID "more questions"
```

**Use Cases:**
- CI/CD pipeline integration
- Custom web interfaces
- Monitoring and logging systems

### Partial Message Streaming

The `--stream-deltas` flag enables real-time token streaming in headless mode, outputting text as it's generated rather than waiting for complete messages. This creates a more responsive user experience similar to ChatGPT or Claude.io:

```bash
# Stream partial text deltas with headless output
kodelet run --headless --stream-deltas "explain how TCP works"

# Show only text deltas (real-time text streaming)
kodelet run --headless --stream-deltas "write a poem" | \
    jq -r 'select(.kind == "text-delta") | .delta' | tr -d '\n'

# Show thinking in real-time
kodelet run --headless --stream-deltas "solve this puzzle" | \
    jq -r 'select(.kind == "thinking-delta") | .delta' | tr -d '\n'
```

**Delta Event Types:**

| Kind | Description | Fields |
|------|-------------|--------|
| `text-delta` | Partial text content | `delta`, `conversation_id`, `role` |
| `thinking-delta` | Partial thinking content | `delta`, `conversation_id`, `role` |
| `thinking-start` | Thinking block begins | `conversation_id`, `role` |
| `thinking-end` | Thinking block ends | `conversation_id`, `role` |
| `content-end` | Content block ends | `conversation_id`, `role` |

**Example Output:**

```jsonl
{"kind":"thinking-start","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-delta","delta":"Let me","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-delta","delta":" analyze this","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-end","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":"The","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":" answer","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":" is 42.","conversation_id":"abc123","role":"assistant"}
{"kind":"content-end","conversation_id":"abc123","role":"assistant"}
{"kind":"text","content":"The answer is 42.","conversation_id":"abc123","role":"assistant"}
```

Note: Complete messages (`text`, `thinking`, `tool-use`, `tool-result`) are still emitted after their delta streams, ensuring clients that ignore deltas still receive full content.

**Third-Party UI Integration (Python):**

```python
import subprocess
import json

process = subprocess.Popen(
    ["kodelet", "run", "--headless", "--stream-deltas", "explain recursion"],
    stdout=subprocess.PIPE,
    text=True
)

current_text = ""
for line in process.stdout:
    event = json.loads(line)
    
    if event["kind"] == "text-delta":
        current_text += event["delta"]
        update_ui(current_text)
    elif event["kind"] == "thinking-start":
        show_thinking_indicator()
    elif event["kind"] == "thinking-delta":
        update_thinking_panel(event["delta"])
    elif event["kind"] == "thinking-end":
        hide_thinking_indicator()
    elif event["kind"] == "tool-use":
        show_tool_execution(event["tool_name"], event["input"])
```

### Conversation Stream Command

Stream real-time updates from any conversation:

```bash
# Stream live updates from a conversation (like tail -f)
kodelet conversation stream CONVERSATION_ID

# Include historical data before streaming new entries
kodelet conversation stream CONVERSATION_ID --include-history
```

This command is useful for:
- Monitoring ongoing conversations
- Building real-time dashboards
- Debugging conversation flow
- Creating custom conversation viewers

### StreamEntry JSON Schema

Both headless mode and conversation streaming output data using the `StreamEntry` format. Each line is a complete JSON object representing one conversation event:

```typescript
interface StreamEntry {
  kind: "text" | "tool-use" | "tool-result" | "thinking";  // Type of entry
  content?: string;         // Text content (for text and thinking entries)
  tool_name?: string;       // Name of the tool (for tool-use and tool-result)
  input?: string;          // JSON input for tool-use
  result?: string;         // Tool execution result
  role: "user" | "assistant" | "system";  // Message role
  tool_call_id?: string;   // Unique ID to match tool calls with results
  conversation_id?: string; // ID of the conversation
}
```

### Example Stream Output

```json
{"kind":"text","role":"user","content":"What files are in this directory?","conversation_id":"conv_123"}
{"kind":"thinking","role":"assistant","content":"The user wants to see the files...","conversation_id":"conv_123"}
{"kind":"tool-use","tool_name":"bash","input":"{\"command\":\"ls -la\"}","tool_call_id":"call_456","role":"assistant","conversation_id":"conv_123"}
{"kind":"tool-result","tool_name":"bash","result":"total 24\ndrwxr-xr-x  3 user user 4096 ...\n","tool_call_id":"call_456","role":"assistant","conversation_id":"conv_123"}
{"kind":"text","role":"assistant","content":"Here are the files in the current directory...","conversation_id":"conv_123"}
```

### Processing Stream Output

**Using jq for filtering:**

```bash
# Extract only text messages
kodelet run --headless "query" | jq -r 'select(.kind == "text") | .content'

# Monitor tool usage
kodelet conversation stream ID | jq 'select(.kind == "tool-use") | {tool: .tool_name, input: .input}'

# Get assistant responses only
kodelet run --headless "query" | jq -r 'select(.role == "assistant" and .kind == "text") | .content'
```

## Agent Context Files

Agent context files provide project-specific information to Kodelet, enabling it to better understand your codebase, conventions, and workflows. These files are automatically loaded and made available to the AI assistant when working in your project directory.

### Creating Context Files

To bootstrap the context file simply use the following command:

```bash
cat <<EOF | kodelet run
Please analyse this repository and create an AGENTS.md file that will serve as context for future Kodelet sessions. Include:

1. **Project Overview** - Brief description of what this project does
2. **Project Structure** - Key directories and their purposes
3. **Tech Stack** - Languages, frameworks, and major dependencies
4. **Engineering Principles** - Code style, testing approach, and development workflow
5. **Key Commands** - Common commands for building, testing, linting, running, and deploying
6. **Configuration** - Environment variables, config files, and setup requirements
7. **Testing** - How to run tests and testing conventions
8. **Error Handling** - Project-specific error handling patterns
9. **Development Workflow** - How to contribute, PR process, and release process

Focus on information that would be repeatedly useful for an AI assistant working on this codebase. Include specific commands, file paths, and conventions that are unique to this project.
EOF
```

Make sure that you sanity check the generated `AGENTS.md` file, and update it as necessary. The context file often have great influnce on quality of the results produced by Kodelet. It is recommended to create a context file for each project you work on, and keep it up to date as the project evolves.

### Context File Priority

Kodelet automatically detects and loads matching context files from the current working directory and the global `~/.kodelet` directory.

You can configure custom context file patterns via:
- CLI flag: `--context-patterns "AGENTS.md,README.md"`
- Config file: `context.patterns: ["AGENTS.md", "README.md"]`
- Environment variable: `KODELET_CONTEXT_PATTERNS="AGENTS.md,README.md"`

Files are searched in order; the first match wins per directory.

**Migration from KODELET.md:**

If you have an existing `KODELET.md` file from an older version of Kodelet, rename it to `AGENTS.md`:

```bash
mv KODELET.md AGENTS.md
```

### Best Practices

**What to include in your context file:**

1. **Project Overview** - Brief description of what this project does
2. **Project Structure** - Key directories and their purposes
3. **Tech Stack** - Languages, frameworks, and major dependencies
4. **Engineering Principles** - Code style, testing approach, and development workflow
5. **Key Commands** - Common commands for building, testing, linting, running, and deploying
6. **Configuration** - Environment variables, config files, and setup requirements
7. **Testing** - How to run tests and testing conventions
8. **Error Handling** - Project-specific error handling patterns
9. **Development Workflow** - How to contribute, PR process, and release process

**Keep it up to date:**

- Update context files when architecture changes
- Add new commands as they become part of regular workflow
- Include lessons learned and common pitfalls
- Review and refresh content during major project milestones

**Note**: Context files are loaded automatically - no special commands needed. Kodelet will inform you which context file is being used when debug logging is enabled (`KODELET_LOG_LEVEL=debug`).

## Shell Completion

Kodelet provides shell completion support for bash, zsh, and fish. This enables tab completion for commands and flags, making the CLI experience more efficient.

### Setup Instructions

**Bash:**

To load completions for every new session, add the following to your `~/.bashrc`:
```bash
echo 'source <(kodelet completion bash)' >> ~/.bashrc
```

**Zsh:**

If shell completion is not already enabled in your environment, you will need to enable it first:
```bash
echo "autoload -U compinit; compinit" >> ~/.zshrc
```

To load completions for every new session, add the following to your `~/.zshrc`:
```bash
echo 'source <(kodelet completion zsh)' >> ~/.zshrc
```

**Fish:**

To load completions for every new session:
```bash
kodelet completion fish > ~/.config/fish/completions/kodelet.fish
```

### Additional Options

All completion commands support these additional flags:
- `--no-descriptions`: Disable completion descriptions for a cleaner experience

Example:
```bash
echo 'source <(kodelet completion bash --no-descriptions)' >> ~/.bashrc
```

After setting up completion, you will need to start a new shell session for the changes to take effect.

## Configuration

Kodelet supports multiple configuration methods with the following precedence (highest to lowest):

1. Command line flags
2. Environment variables
3. Configuration file
4. Defaults

### Environment Variables

All environment variables should be prefixed with `KODELET_`:

```bash
# Logging configuration
export KODELET_LOG_LEVEL="info"  # panic, fatal, error, warn, info, debug, trace

# LLM configuration - Anthropic
export ANTHROPIC_API_KEY="sk-ant-api..."
export KODELET_PROVIDER="anthropic"  # Optional, detected from model name
export KODELET_MODEL="claude-sonnet-4-6"
export KODELET_MAX_TOKENS="8192"
export KODELET_CACHE_EVERY="5"  # Cache messages every N interactions (0 to disable)

# LLM configuration - OpenAI
export OPENAI_API_KEY="sk-..."
export KODELET_PROVIDER="openai"
export KODELET_MODEL="gpt-4.1"
export KODELET_MAX_TOKENS="8192"
export KODELET_REASONING_EFFORT="medium"  # OpenAI: none|minimal|low|medium|high|xhigh; Anthropic adaptive thinking: none|low|medium|high|xhigh|max

# Profile configuration
export KODELET_PROFILE="anthropic"  # Use a specific profile

# Command restriction configuration
export KODELET_ALLOWED_COMMANDS="ls *,pwd,echo *,git status"  # Comma-separated allowed command patterns
```

### Configuration File

Kodelet uses a **layered configuration approach** where settings are applied in the following order:

1. **Defaults**: Built-in default values
2. **Global Config**: `config.yaml` in `$HOME/.kodelet/` directory
3. **Repository Config**: `kodelet-config.yaml` in the current directory (overrides global)

**Repository-level Configuration**

Use `kodelet-config.yaml` in your project root for project-specific settings. This file will **merge with and override** your global configuration, so you only need to specify the settings that differ from your global defaults.

```yaml
# Global config (~/.kodelet/config.yaml)
provider: "anthropic"
model: "claude-sonnet-4-6"
max_tokens: 8192
log_level: "info"
```

```yaml
# Repository config (kodelet-config.yaml) - only override what's different
provider: "openai"
model: gpt-4.1
```

```bash
# Result: using provider=openai, model=gpt-4.1, max_tokens=8192, log_level=info
kodelet run "analyze this codebase"
```

**Benefits of layered configuration:**
- **Minimal repo configs**: Only specify what's different from your global settings
- **Team consistency**: Share project-specific settings while preserving individual global preferences
- **Inheritance**: Automatically inherit global settings like API keys, logging preferences, etc.

Example `config.yaml`:

```yaml
# Logging configuration
log_level: "info"  # panic, fatal, error, warn, info, debug, trace

# Anthropic configuration
provider: "anthropic"
model: "claude-sonnet-4-6"
max_tokens: 8192
weak_model: "claude-haiku-4-5-20251001"
weak_model_max_tokens: 8192

# Alternative OpenAI configuration
# provider: "openai"
# model: "gpt-4.1"
# max_tokens: 8192
# weak_model: "gpt-4.1-mini"
# weak_model_max_tokens: 4096
# reasoning_effort: "medium"
# weak_reasoning_effort: "low"
# Anthropic adaptive-thinking models also use reasoning_effort via output_config.effort.

# Security configuration
allowed_commands: []  # Empty means use default banned commands
# allowed_commands:   # Example: restrict bash tool to specific commands
#   - "ls *"
#   - "pwd"
#   - "echo *"
#   - "cat *"
#   - "grep *"
#   - "find *"
#   - "npm *"
#   - "yarn *"
#   - "git status"
#   - "git log *"

bash:
  # Maximum execution timeout for bash tool calls (default: 120s)
  timeout: 120s

# Tool behavior configuration
# Tool interaction mode
# - full: standard tool access
# - patch: use apply_patch plus search/navigation tools instead of direct file reads/writes
tool_mode: full

# Conversation summary behavior
# - llm: generate a short summary with the weak model
# - first_message: use the first user message directly
conversation_summary_mode: llm
```

MCP servers are configured separately from `config.yaml`. The SDK MCP extension reads the standard `mcpServers` JSON shape from `./mcp.json` and `~/.kodelet/mcp.json`; repository config overrides global config by server name.

Example `mcp.json`:

```json
{
  "mcpServers": {
    "fs": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/files"],
      "tool_white_list": ["list_directory"]
    },
    "some_http_server": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": {
        "Authorization": "Bearer token"
      },
      "tool_white_list": ["tool1", "tool2"]
    },
    "some_sse_server": {
      "type": "sse",
      "url": "http://localhost:8000/sse",
      "headers": {
        "Authorization": "Bearer token"
      },
      "tool_white_list": ["tool1", "tool2"]
    }
  }
}
```

The standard MCP JSON fields are `command`, `args`, and `env` for stdio servers. Kodelet also accepts `url`, `type` (`http` or `sse`), `headers`, and `tool_white_list` for remote servers/tool filtering. The current SDK MCP extension supports static headers for remote authentication; it does not currently implement an interactive OAuth flow.

### Command Line Flags

Override configuration for specific commands:

```bash
# Log level example
kodelet run --log-level debug "query"

# Anthropic example
kodelet run --provider "anthropic" --model "claude-opus-4-1-20250805" --max-tokens 4096 --weak-model-max-tokens 2048 "query"

# OpenAI example
kodelet run --provider "openai" --model "gpt-4.1" --max-tokens 4096 --reasoning-effort "high" "query"

# Command restriction example
kodelet run --allowed-commands "ls *,pwd,echo *" "query"

# Enable filesystem search tools (`glob_tool` and `grep_tool`)
kodelet run --enable-fs-search-tools "query"

# Use the first user message for persisted conversation summaries
kodelet run --conversation-summary-mode first_message "query"

# Use a custom system prompt template
kodelet run --sysprompt ./sysprompt.tmpl "query"

# Pass custom template args (repeatable)
kodelet run --sysprompt ./sysprompt.tmpl --sysprompt-arg project=kodelet --sysprompt-arg env=dev "query"

# Profile override for single command
kodelet run --profile anthropic "explain this architecture"
```

## Configuration Profiles

Kodelet includes a comprehensive profile system that allows you to define and switch between named configurations for different use cases. This eliminates the need to manually edit configuration files when experimenting with different model setups.

### Profile Definition

Profiles are defined in your configuration files using the `profiles` section. Each profile can override any configuration setting:

```yaml

# Default profile
model: "claude-sonnet-4-6"
weak_model: "claude-haiku-4-5-20251001"
max_tokens: 16000
weak_model_max_tokens: 8192
# On adaptive Claude models, reasoning_effort controls adaptive thinking.
# thinking_budget_tokens is only used on older Claude models that still use manual thinking.
reasoning_effort: "medium"

# Anthropic-compatible platforms can force adaptive-thinking plumbing for the configured
# non-standard model IDs that Kodelet does not know about yet.
anthropic:
  # platform: copilot
  # adaptive_thinking: true

# Active profile selection
profile: "anthropic"  # Optional: specify the active profile

# Profile definitions
profiles:
  anthropic:
    model: "opus-48" # alias to "claude-opus-4-8"
    weak_model: "sonnet-46" # alias to "claude-sonnet-4-6"
    max_tokens: 64000
    weak_model_max_tokens: 8192
    reasoning_effort: "max"

  openai:
    provider: "openai"
    model: "gpt-4.1"
    weak_model: "gpt-4.1-mini"
    max_tokens: 16000
    reasoning_effort: "medium"
    tool_mode: "patch"
    enable_fs_search_tools: false
    enable_search: true
    openai:
      platform: copilot

# Model aliases work across all profiles
aliases:
    fable-5: claude-fable-5
    haiku-45: claude-haiku-4-5-20251001
    opus-48: claude-opus-4-8
    sonnet-46: claude-sonnet-4-6
```

### Profile Management Commands

**View current active profile:**
```bash
kodelet profile current
```

**List all available profiles:**
```bash
kodelet profile list
```

**Show detailed configuration for a profile:**
```bash
kodelet profile show anthropic
kodelet profile show default  # Shows base configuration
```

**Switch to a different profile:**
```bash
kodelet profile use anthropic    # Switch in repository config (./kodelet-config.yaml)
kodelet profile use openai -g     # Switch in global config (~/.kodelet/config.yaml)
kodelet profile use default       # Use base configuration without any profile
```

**Practical workflow examples:**
```bash
# Check what's currently active, then switch to a different profile
kodelet profile current
kodelet profile use development

# Use the anthropic profile globally across all projects
kodelet profile use anthropic -g

# Show the merged configuration for a specific profile
kodelet profile show mix-n-match

# Return to base configuration (no profile)
kodelet profile use default
```

### Profile Usage

**Temporary profile override for single commands:**
```bash
# Use a specific profile for a single command without changing config
kodelet run --profile anthropic "explain this architecture"
kodelet commit --profile anthropic
kodelet run --profile openai "what does this function do?"

# Use base configuration without any profile
kodelet run --profile default "use base configuration"
```

**Environment variable override:**
```bash
export KODELET_PROFILE="anthropic"
kodelet run "this will use the anthropic profile"
```

### Profile Precedence and Merging

Profiles follow a hierarchical approach with intelligent merging:

**Profile Selection Priority:**
1. Command-line `--profile` flag (highest)
2. `KODELET_PROFILE` environment variable
3. `profile` field in repository config (`kodelet-config.yaml`)
4. `profile` field in global config (`~/.kodelet/config.yaml`) (lowest)

**Profile Definition Priority:**
- Repository profiles override global profiles with the same name
- All profiles from both global and repository configs are available
- Profile settings override base configuration
- Undefined fields in profiles inherit from base configuration

**Configuration Priority (overall):**
1. Command-line flags (highest)
2. Active profile settings
3. Repository configuration base settings
4. Global configuration base settings
5. Default values (lowest)

### Special "Default" Profile

The `"default"` profile is a special reserved name that means "use base configuration without any profile":

```bash
# These are equivalent - both use base configuration
kodelet profile use default
kodelet run --profile default "query"
```

You cannot define a profile named "default" in your configuration files - it's reserved for this special purpose.

## Security Configuration

Kodelet includes security features to control command execution and protect your system from potentially harmful operations.

### Bash Command Restrictions

The `allowed_commands` configuration option provides fine-grained control over which bash commands Kodelet can execute. This is particularly useful in automated environments or when working with untrusted queries.

**Pattern Matching:**

The allowed commands support glob patterns for flexible matching:

- `ls` - Exact match for the `ls` command only
- `ls *` - Allows `ls` with any arguments (e.g., `ls -la`, `ls /home`)
- `git status` - Exact match for `git status`
- `git log *` - Allows `git log` with any arguments
- `npm *` - Allows any npm command

**Configuration Examples:**

Environment variable:
```bash
export KODELET_ALLOWED_COMMANDS="ls *,pwd,echo *,git status,git log *"
```

Configuration file:
```yaml
allowed_commands:
  - "ls *"
  - "pwd"
  - "echo *"
  - "cat *"
  - "grep *"
  - "find *"
  - "npm *"
  - "yarn *"
  - "git status"
  - "git log *"
```

Command line:
```bash
kodelet run --allowed-commands "ls *,pwd,echo *" "analyze this directory"
```

**Usage Notes:**

- If the command appears in the default banned commands list, it will be rejected even if it matches the allowed commands pattern
- Commands are validated before execution, and non-matching commands are rejected with an error
- Patterns are matched against the entire command string, not just the command name
- Use specific patterns rather than overly broad wildcards for better security

### Bash Tool Timeout

The `bash.timeout` configuration option controls the maximum timeout the agent can request for a bash command. It defaults to `120s` and accepts Go-style duration strings such as `120s`, `2m`, or `5m`.

Configuration file:
```yaml
bash:
  timeout: 5m
```

Environment variable:
```bash
export KODELET_BASH_TIMEOUT=5m
```

## LLM Providers

### Anthropic Claude

Kodelet supports various Anthropic Claude models:
- `claude-fable-5` (most capable widely released model for demanding reasoning and long-horizon agentic work)
- `claude-sonnet-4-6` (recommended for standard tasks)
- `claude-haiku-4-5-20251001` (recommended for lightweight tasks)
- `claude-opus-4-5-20251101` (most intelligent model for building agents and coding)
- `claude-opus-4-1-20250805` (high-end model for complex tasks)

Features:
- Vision capabilities for image analysis
- Message caching for improved performance
- Thinking mode for complex reasoning

### OpenAI

Kodelet supports OpenAI models:
- `gpt-4.1` (latest GPT-4 model)
- `gpt-4.1-mini` (lightweight variant)

Features:
- Reasoning effort control (none, minimal, low, medium, high, xhigh)
- Function calling capabilities
- Vision support (planned)

## OpenAI Codex Authentication

Kodelet supports ChatGPT-backed Codex authentication for `openai.platform: codex`.

### Login

```bash
# Browser redirect flow
kodelet codex login

# Device code flow for remote/headless machines
kodelet codex login --device-auth
```

Both flows save credentials to `~/.kodelet/codex-credentials.json`.

### Check Status

```bash
kodelet codex status
```

With ChatGPT OAuth credentials, this also shows the live Codex usage snapshot,
including rolling windows and workspace credits.

### Configure Codex

```yaml
provider: openai
openai:
  platform: codex
  api_mode: responses
  service_tier: fast
  websocket_mode: true
```

`openai.service_tier` is optional. Kodelet accepts OpenAI's native values
`auto`, `default`, `flex`, `priority`, and `scale`, plus Codex's
user-facing `fast` alias. When you set `fast`, Kodelet sends
`service_tier: priority` to the upstream API.

`openai.websocket_mode` controls the Responses API WebSocket transport. It
defaults to `true` for supported OpenAI and Codex Responses API endpoints to
reduce end-to-end latency. Kodelet still sends `store: false` and replays local
conversation state. If WebSocket setup or streaming fails while this is enabled,
the request fails instead of silently retrying over HTTP.

You can force HTTP streaming with:

```yaml
openai:
  websocket_mode: false
```

## OpenAI Native Web Search

When you use the OpenAI Responses API against the real OpenAI platform,
Kodelet can expose OpenAI's native `web_search` tool in addition to the existing
`web_fetch` tool.

- `web_search` is for open-ended discovery and current information.
- `web_fetch` is still available for deterministic fetching/extraction from a known URL.

Native OpenAI search is enabled by default and can be controlled with:

```bash
kodelet run --enable-openai-search "what changed in postgres 18 this week?"
```

Or in config:

```yaml
openai:
  enable_search: true
```

Kodelet only enables this built-in tool when all of the following are true:

- provider is `openai`
- API mode resolves to `responses`
- platform resolves to the real OpenAI platform
- GitHub Copilot mode is not being used
- no custom non-OpenAI base URL is configured

## Anthropic Multi-Account Authentication

Kodelet supports multiple Anthropic subscription accounts, allowing you to manage different accounts (e.g., work and personal) and switch between them at runtime.

### Logging In with Multiple Accounts

```bash
# Login with an alias
kodelet anthropic login --alias work

# Login without alias (uses email prefix as alias)
kodelet anthropic login
```

The first account logged in automatically becomes the default account.

### Managing Accounts

**List all accounts:**
```bash
kodelet anthropic accounts list
```

Output shows all logged-in accounts with their status:
```
ALIAS      EMAIL                    STATUS
*work      user@company.com         valid
personal   user@personal.com        needs refresh
```

The asterisk (*) indicates the default account.

**Set default account:**
```bash
# Show current default
kodelet anthropic accounts default

# Set a new default
kodelet anthropic accounts default personal
```

**Remove an account:**
```bash
kodelet anthropic accounts remove work
```

If you remove the default account, another account will automatically become the new default (if available).

### Using Accounts at Runtime

Specify which account to use with the `--account` flag:

```bash
# Use a specific account for one-shot queries
kodelet run --account work "analyze this code"
kodelet run --account personal "help with my side project"
```

Without the `--account` flag, Kodelet uses the default account.

### Account Status

The `kodelet anthropic accounts list` command shows token status:
- **valid**: Token is valid and ready to use
- **needs refresh**: Token will be refreshed on next use
- **expired**: Token has expired and needs re-authentication

If a token is expired, run `kodelet anthropic login --alias <alias>` to re-authenticate.

## Extensions

Extensions are Kodelet's unified external extensibility primitive. They replace the old executable custom-tool and lifecycle-hook systems with one long-running subprocess that can register model tools, prompt commands, dynamic recipes, and lifecycle event handlers.

Extensions communicate with Kodelet over stdio JSON-RPC using `Content-Length` framing. `stdout` is reserved for protocol messages and `stderr` is used for logs.

### TypeScript Agent SDK

The `kodelet` TypeScript package can also launch and drive agent sessions from Node/TypeScript. It speaks to `kodelet acp` over stdio JSON-RPC, so it preserves normal profile resolution, conversation persistence, built-in tools, skills, and extension behavior.

```typescript
import { Client } from "kodelet";

const client = new Client();
const session = await client.createSession();
const response = await session.runAndWait({ message: "what is the meaning of life" });

console.log(response.content);
await client.close();
```

Create streaming sessions with an inline or named profile, and optionally pass in-process extension definitions. Inline extensions are exposed to Kodelet through a temporary JSON-RPC bridge for that session. The bridge uses a Unix domain socket (or Windows named pipe) by default; set `extensionTransport: "tcp"` to use an ephemeral loopback TCP port instead.

```typescript
import { Client, Profile, defineExtension, z } from "kodelet";

const workspace = defineExtension((ext) => {
  ext.setMetadata({ name: "workspace", version: "0.1.0" });
  ext.registerTool({
    name: "ask_user_question",
    description: "Ask the user to choose one option.",
    inputSchema: z.object({ question: z.string(), options: z.array(z.string()).min(2).max(5) }),
    async execute(input, ctx) {
      const choice = await ctx.ui.select({ title: input.question, options: input.options });
      return choice ? `User selected: ${choice}` : "User dismissed the question.";
    },
  });
});

const profile = new Profile({
  provider: "openai",
  model: "gpt-5.5",
  reasoning_effort: "xhigh",
  tool_mode: "patch",
  openai: { api_mode: "responses", platform: "codex", service_tier: "fast" },
});

const client = new Client();
const session = await client.createSession({
  profile,
  extensions: [workspace],
  streaming: true,
  // Optional: use loopback TCP instead of the default socket/named-pipe bridge.
  extensionTransport: "tcp",
});

session.on("assistant.message_delta", (event) => process.stdout.write(event.data.deltaContent));
session.on("tool.call", (event) => console.error(event.data.toolName, event.data.input));

const response = await session.runAndWait({ message: "help me choose an approach" });
console.log("\nfinal:", response.content);
await client.close();
```

### Creating TypeScript Extensions

The TypeScript SDK is exposed as `kodelet` and re-exports Zod as `z` so extension authors can define schemas and handlers from one package:

```typescript
import { z, defineExtension } from "kodelet";

const WeatherInput = z.object({
  location: z.string().describe("Location to fetch weather for"),
});

export default defineExtension((ext) => {
  ext.setMetadata({ name: "weather", version: "0.1.0" });

  ext.registerTool({
    name: "get_weather",
    description: "Get the current weather for a location",
    inputSchema: WeatherInput,
    timeoutInSec: 600,
    async execute(input, ctx) {
      ctx.log.info(`Fetching weather for ${input.location}`);
      return {
        content: `Weather for ${input.location}: 18°C, cloudy`,
        data: { location: input.location, temperatureC: 18 },
      };
    },
  });

  ext.on("tool.call", { timeoutInSec: 5 }, async (event) => {
    if (event.tool.name === "bash" && JSON.stringify(event.tool.input).includes("rm -rf /")) {
      return { block: { reason: "Refusing dangerous shell command" } };
    }
  });
});
```

A typical extension directory contains a package, compiled JavaScript, and an executable wrapper:

```text
.kodelet/extensions/weather/
  package.json
  src/index.ts
  dist/index.js
  kodelet-extension-weather
```

Example wrapper:

```bash
#!/usr/bin/env bash
exec kodelet-extension-node ./dist/index.js
```

### Requesting User Input from Extensions

Tool, command, and event handlers can ask the active Kodelet UI for user-facing prompts with `ctx.ui.input(...)`, `ctx.ui.confirm(...)`, `ctx.ui.select(...)`, and `ctx.ui.notify(...)`. Kodelet routes prompts to the interactive CLI terminal or to the Web UI conversation stream. In headless/result-only runs, or when no interactive UI is attached, the SDK resolves inputs/selects as `undefined` and confirmations as `false`.

```typescript
ext.registerTool({
  name: "ask_user_question",
  description: "Ask the user to choose between concrete options",
  inputSchema: z.object({
    question: z.string(),
    options: z.array(z.string()).min(2).max(5),
  }),
  async execute(input, ctx) {
    const choices = input.options.map((option, index) => `${index + 1}. ${option}`).join("\n");
    const answer = await ctx.ui.input({
      title: input.question,
      helpText: `${choices}\n\nType the number of your choice`,
      submitButtonText: "Select",
      required: true,
    });

    if (!answer) {
      return "User dismissed the question without choosing.";
    }

    const index = parseInt(answer.trim(), 10) - 1;
    if (index >= 0 && index < input.options.length) {
      return `User selected option ${index + 1}: ${input.options[index]}`;
    }
    return `User responded with: ${answer}`;
  },
});
```

Input prompts support `title`, `helpText`, `message`, `placeholder`, `defaultValue`, custom submit/cancel labels, `required`, and `secret` for password-style Web UI input. Extension authors should treat a missing return value as dismissal or unavailable UI and continue gracefully.

Other UI helpers use separate host RPC methods and separate Web UI stream events, so clients can handle each concern independently:

| SDK helper | Host RPC method | Web UI stream event | Result |
|---|---|---|---|
| `ctx.ui.input(request)` | `kodelet.ui.input` | `ui-input-request` with `ui_input` | `string \| undefined` |
| `ctx.ui.confirm(request)` | `kodelet.ui.confirm` | `ui-confirm-request` with `ui_confirm` | `boolean` |
| `ctx.ui.select(request)` | `kodelet.ui.select` | `ui-select-request` with `ui_select` | `string \| undefined` |
| `ctx.ui.notify(messageOrRequest)` | `kodelet.ui.notify` | `ui-notification` with `ui_notify` | `void` |

Examples:

```typescript
const confirmed = await ctx.ui.confirm({
  title: `Allow ${event.tool.name}?`,
  message: "A tool call incoming",
  confirmButtonText: "Allow",
});

const choice = await ctx.ui.select({
  title: "What is your favourite food?",
  message: "Choose what you like.",
  options: ["Pasta", "Pizza", "Focaccia"],
});

await ctx.ui.notify({
  title: "Kitchen sink",
  message: `Kitchen sink session.start for ${event.conversation_id}.`,
});
```

### Extension Discovery

Kodelet discovers executable files named `kodelet-extension-*` in these locations, in precedence order:

1. `./.kodelet/extensions`
2. `./.kodelet/plugins/<org@repo>/extensions`
3. `~/.kodelet/extensions`
4. `~/.kodelet/plugins/<org@repo>/extensions`

Within each extension root, Kodelet loads either direct or nested executables:

```text
<extension-root>/kodelet-extension-xxx
<extension-root>/*/kodelet-extension-xxx
```

The executable filename must be `kodelet-extension-xxx`. Kodelet derives the extension ID/name as `xxx` for a direct executable, or as the parent directory name for a nested executable. Plugin extension IDs are addressed as `org@repo/extension`. Standalone extensions are matched by directory or executable path in allow/deny config.

Inspect discovered extensions with:

```bash
kodelet extension list
kodelet extension list --json
kodelet extension inspect weather
kodelet extension inspect org@repo/weather --json
```

### Extension Commands and Dynamic Recipes

Extensions can register prompt-level commands. Commands are checked before the LLM sees a user prompt. A command result controls what Kodelet does next:

| Action | Meaning | LLM input? |
|---|---|---:|
| `pass` | Decline handling and continue normal routing | No |
| `respond` | Display a direct terminal/Web UI response | No |
| `runAgent` | Replace the prompt and run the normal agent flow | Yes |

Direct-response example:

```typescript
const OpenInput = z.object({ path: z.string().optional() });

ext.registerCommand({
  name: "open",
  aliases: ["/open"],
  description: "Open a project-relative path in the configured editor",
  inputSchema: OpenInput,
  timeoutInSec: 60,
  async execute(input, ctx) {
    const target = ctx.path.resolveWorkspacePath(input.path ?? ".");
    if (!(await ctx.fs.exists(target))) {
      return { action: "respond", response: `Cannot open ${ctx.path.relativeToWorkspace(target)}.` };
    }

    const editor = ctx.env.get("EDITOR") ?? "code";
    await ctx.process.spawn(editor, [target], { detach: true });
    return { action: "respond", response: `Opened ${ctx.path.relativeToWorkspace(target)} in ${editor}.` };
  },
});
```

Recipe-like command example:

```typescript
ext.registerCommand({
  name: "review",
  aliases: ["/review"],
  description: "Run an extension-provided code review recipe",
  kind: "recipe",
  inputSchema: z.object({ target: z.string().default("HEAD") }),
  async execute(input) {
    return {
      action: "runAgent",
      recipeName: "review",
      prompt: `Review ${input.target}. Focus on correctness, simplicity, and tests.`,
    };
  },
});
```

Recipe-like commands appear in `kodelet recipe list` and can be invoked through recipe UX such as `kodelet run -r review --arg target=main`. They can also be invoked directly as slash commands, for example `/review target=main`.

### Extension Events

Extensions subscribe to dot-separated lifecycle events with `ext.on(...)`. Mutating/blocking events run sequentially by priority, then discovery order, then registration order. The first blocking handler stops the operation.

| Event | When | Can block? | Can mutate? |
|---|---|---:|---:|
| `session.start` | Extension runtime starts | No | No |
| `resources.discover` | Extension resources are finalized | No | Resource registrations |
| `user.message` | User prompt received | Yes | Message |
| `agent.init` | System prompt built before first model request | No | System prompt / tool list |
| `agent.start` | Agent loop starts | No | No |
| `turn.start` | Before a model turn | No | No |
| `tool.call` | Before a tool runs | Yes | Tool input |
| `tool.result` | After a tool runs | No | Tool result |
| `turn.end` | After one assistant turn completes | No | No |
| `agent.end` | Agent loop completes | No | Follow-up messages |
| `session.end` | Extension runtime shuts down | No | No |

Migration map from removed hooks:

| Removed hook | Extension event |
|---|---|
| `before_tool_call` | `tool.call` |
| `after_tool_call` | `tool.result` |
| `user_message_send` | `user.message` |
| `agent_stop` | `agent.end` |
| `turn_end` | `turn.end` |

### Extensions Configuration

Configure extensions in `~/.kodelet/config.yaml` or repository-level `kodelet-config.yaml`:

```yaml
extensions:
  enabled: true
  global_dir: ~/.kodelet/extensions
  local_dir: ./.kodelet/extensions
  max_output_size: 102400

  allow:
    - org@repo/security
    - ./.kodelet/extensions/weather

  deny:
    - org@repo/experimental
    - /absolute/path/to/kodelet-extension-experimental

  tools:
    get_weather:
      enabled: true

```

Extension subprocesses inherit Kodelet's environment. Start Kodelet with any environment variables required by extension tools.

Timeouts are controlled by SDK-declared `timeoutInSec`. Extension events use SDK `timeoutInSec` or the built-in `30s` default, extension tools use SDK `timeoutInSec` or the built-in `10m` default, and extension commands use SDK `timeoutInSec` or no timeout.

Use `kodelet run --no-extensions "query"` or `extensions.enabled: false` to disable extension loading.

## Agentic Skills

Agentic Skills are model-invoked capabilities that package domain expertise into discoverable units. Unlike fragments/recipes (which require explicit user invocation), skills are automatically invoked by Kodelet when it determines they are relevant to your task.

### How Skills Work

1. **Discovery**: At startup, Kodelet discovers skills from configured directories
2. **Description**: Each skill has a name and description that help Kodelet decide when to use it
3. **Invocation**: When a task matches a skill's domain, Kodelet automatically invokes it
4. **Context Loading**: The skill's instructions become available to guide Kodelet's work

### Creating Skills

Skills are directories containing a `SKILL.md` file with YAML frontmatter:

**Directory Structure:**
```
~/.kodelet/skills/my-skill/
├── SKILL.md          (required)
├── references/       (optional detailed docs)
│   └── api.md
├── examples/         (optional samples)
│   └── sample.txt
└── scripts/
    └── helper.py     (optional)
```

**SKILL.md Format:**
```markdown
---
name: my-skill
description: Brief description of what this skill does and when to use it
---

# My Skill

## Instructions
Step-by-step guidance for the agent...

## References
Read `references/api.md` only for API-specific tasks.
```

Keep `SKILL.md` compact. Supporting files are available from the skill directory and can be inspected on demand, which avoids loading rarely needed details into context every time the skill is invoked.

**Skill Locations:**
- `./.kodelet/skills/<skill_name>/` - Repository-local (higher precedence)
- `~/.kodelet/skills/<skill_name>/` - User-global

Repository-local skills take precedence over user-global skills with the same name.

### Skills Configuration

Configure skills in `config.yaml` or `kodelet-config.yaml`:

```yaml
skills:
  # Enable/disable skills globally (default: true)
  enabled: true

  # Allowlist of skill names (empty = all discovered skills enabled)
  allowed:
    - pdf
    - xlsx
    - kubernetes
```

### Managing Skills

Kodelet provides commands to manage skills from GitHub repositories:

```bash
# Install all skills and recipes from a GitHub repository
kodelet plugin add orgname/skills

# Install to the global plugin directory
kodelet plugin add orgname/skills -g

# List installed plugins and the skills they provide
kodelet plugin list

# Show details for one installed plugin
kodelet plugin show orgname/skills
```

Plugins can also provide extension executables under `extensions/`. Use the normal plugin commands to install and inspect them:

```bash
kodelet plugin add orgname/extensions
kodelet plugin list
kodelet plugin show orgname/extensions
```

Extension-provided tools, commands, and dynamic recipes are loaded through the extension runtime when extensions are enabled.

### Disabling Skills

To run without skills for a single session:

```bash
kodelet run --no-skills "your query"
```

### Enabling Filesystem Search Tools

The built-in filesystem search tools (`glob_tool` and `grep_tool`) are disabled by default; the system prompt instructs the agent to use `fd` and `rg` via the `bash` tool for filesystem search tasks instead. To enable them:

```bash
kodelet run --enable-fs-search-tools "your query"
```

This can also be set via configuration file (`enable_fs_search_tools: true`) or environment variable (`KODELET_ENABLE_FS_SEARCH_TOOLS=true`).

### Conversation Summary Mode

To use the first user message instead of weak-model summary generation for persisted conversation titles:

```bash
kodelet run --conversation-summary-mode first_message "your query"
```

This can also be set via configuration file (`conversation_summary_mode: first_message`) or environment variable (`KODELET_CONVERSATION_SUMMARY_MODE=first_message`). The default is `llm`. This only affects short persisted conversation summaries/titles, not context compaction.

### Context Compaction Ratio

Kodelet automatically compacts conversation context when context-window utilization reaches the configured ratio:

```bash
kodelet --compact-ratio 0.9 run "your query"
```

This can also be set via configuration file (`compact_ratio: 0.9`) or environment variable (`KODELET_COMPACT_RATIO=0.9`). The default is `0.8`; values must be greater than `0.0` and less than or equal to `1.0`.

### Custom System Prompt Template

You can provide a custom system prompt template via CLI or configuration:

```bash
kodelet run --sysprompt ./sysprompt.tmpl "your query"
```

```yaml
sysprompt: "~/.kodelet/sysprompt.tmpl"
sysprompt_args:
  project: "kodelet"
  env: "dev"
```

Custom templates use Go template syntax, can access `.Args.<key>`, and can reuse built-in sections:

```gotemplate
You are a focused coding assistant.

Project: {{default .Args.project "unknown"}}
Branch: {{bash "git" "branch" "--show-current"}}

{{include "templates/sections/behavior.tmpl" .}}
{{include "templates/sections/tooling.tmpl" .}}
{{include "templates/sections/context_runtime.tmpl" .}}
```

If loading or parsing a custom template fails, Kodelet logs a warning and falls back to the default system prompt.

For detailed skill creation guide, see [docs/SKILLS.md](SKILLS.md).

## Key Features

- **Intelligent Engineering Assistant**: Automates software engineering tasks and production operations with agentic capabilities.
- **Interactive Architecture Design**: Collaboratively design and refine system architectures through natural dialogue.
- **Continuous Code Intelligence**: Analyzes, understands, and improves your codebase while answering technical questions in context.
- **Agent Context Files**: Automatic loading of project-specific context from `AGENTS.md` files for enhanced project understanding.
- **Extensions**: Extend Kodelet with long-running tools, prompt commands, dynamic recipes, and lifecycle event handlers over stdio JSON-RPC.
- **Vision Capabilities**: Support for image inputs including screenshots, diagrams, and mockups (Anthropic Claude models).
- **Multiple LLM Providers**: Supports both Anthropic Claude and OpenAI models, giving you flexibility in choosing the best model for your needs.

## Security & Limitations

### Image Input Security
- Only HTTPS URLs are accepted for remote images (no HTTP)
- File size limited to 5MB per image
- Maximum 10 images per message
- Supported formats: JPEG, PNG, GIF, WebP only

### General Security
- API keys are stored in environment variables or configuration files
- No sensitive data is logged by default
- All external connections use secure protocols
- Bash command execution can be restricted using `allowed_commands` configuration (see [Security Configuration](#security-configuration))
- Default banned commands list prevents execution of potentially dangerous commands like `vim`, `less`, `more`, and `cd`

## Troubleshooting

### Common Issues

1. **API Key Not Found**
   - Ensure your API key is set in environment variables or configuration file
   - Check that the variable name matches the expected format (e.g., `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`)

2. **Model Not Available**
   - Verify the model name is correct and available for your API key
   - Check if you have access to the specific model in your account

3. **Configuration Not Loading**
   - Ensure the configuration file is in the correct location
   - Verify YAML syntax is correct
   - Check file permissions

4. **Vision Features Not Working**
   - Ensure you're using an Anthropic Claude model
   - Check image format and size limitations
   - Verify image URLs are accessible (HTTPS only)

5. **Command Execution Blocked**
   - Check if the command is in the banned commands list (default behavior)
   - If using `allowed_commands`, ensure the command matches one of the allowed patterns
   - Verify glob patterns are correctly formatted (e.g., `ls *` not `ls*`)
   - Use `--allowed-commands` flag to override configuration for testing

6. **Context Files Not Loading**
   - Ensure the context file (`AGENTS.md`) is in the current working directory
   - Verify file permissions are readable
   - Use `KODELET_LOG_LEVEL=debug` to see which context file is being loaded
   - Check file syntax if content seems to be ignored

For more help, check the project repository: https://github.com/jingkaihe/kodelet
