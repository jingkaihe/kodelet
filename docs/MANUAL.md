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
  - [Git Integration](#git-integration)
  - [GitHub Actions Background Agent](#github-actions-background-agent)
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
- [Custom Tools](#custom-tools)
  - [Creating Custom Tools](#creating-custom-tools)
  - [Tool Protocol](#tool-protocol)
  - [Directory Structure](#directory-structure)
  - [Configuration](#custom-tools-configuration)
  - [Examples](#custom-tools-examples)
  - [Generate Custom Tool](#generate-custom-tool)
- [Agentic Skills](#agentic-skills)
  - [How Skills Work](#how-skills-work)
  - [Creating Skills](#creating-skills)
  - [Skills Configuration](#skills-configuration)
  - [Disabling Skills](#disabling-skills)
- [Agent Lifecycle Hooks](#agent-lifecycle-hooks)
  - [Hook Types](#hook-types)
  - [Creating Hooks](#creating-hooks)
  - [Hook Protocol](#hook-protocol)
  - [Disabling Hooks](#disabling-hooks)
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
```

### Prerequisites

For running locally or building from source:
- Go 1.24 or higher

## Updating

To update Kodelet to the latest version:

```bash
kodelet update
```

To install a specific version:

```bash
kodelet update --version 0.0.24.alpha
```

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

# Headless mode for programmatic use
kodelet run --headless "your query"          # outputs structured JSON stream
kodelet run --headless --include-history "query"  # include historical data in stream
```

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

### Git Integration

Generate meaningful commit messages using AI:

```bash
kodelet commit
```

This command analyzes your staged changes (`git diff --cached`) and uses AI to generate a meaningful commit message following conventional commits format. You must stage your changes using `git add` before running this command.

Options:
- `--no-sign`: Disable commit signing (commits are signed by default)
- `--template` or `-t`: Use a template for the commit message
- `--short`: Generate a short commit message
- `--no-confirm`: Skip confirmation prompt

Create pull requests:

```bash
kodelet pr
```

Resolve GitHub issues automatically:

```bash
kodelet issue-resolve --issue-url https://github.com/owner/repo/issues/123
```

This command analyzes the issue, creates an appropriate branch, works on the issue resolution, and automatically creates a pull request with updates back to the original issue. Currently supports GitHub issues only.

Respond to specific pull request comments:

```bash
kodelet pr-respond --pr-url https://github.com/owner/repo/pull/456
kodelet pr-respond --pr-url https://github.com/owner/repo/pull/456 --review-id 123456
kodelet pr-respond --pr-url https://github.com/owner/repo/pull/456 --issue-comment-id 789012
```

This command focuses on addressing a specific comment or review feedback within a PR. You must provide either `--review-id` for review comments or `--issue-comment-id` for issue comments. Currently supports GitHub PRs only.

### GitHub Actions Background Agent

Set up an automated background agent that responds to `@kodelet` mentions in your GitHub repository:

```bash
kodelet gha-agent-onboard
```

This command automates the complete setup process for a GitHub Actions-based background agent:

1. **GitHub App Installation**: Opens the GitHub app installation page in your browser
2. **Secret Configuration**: Checks and sets up the `ANTHROPIC_API_KEY` repository secret
3. **Workflow Creation**: Creates a new git branch with the Kodelet workflow file (`.github/workflows/kodelet.yaml`)
4. **Pull Request**: Automatically commits changes and creates a pull request for review

**Prerequisites:**
- Must be run from within a git repository
- GitHub CLI (`gh`) must be installed and authenticated
- Repository owner/admin permissions to install GitHub apps and manage secrets

**Supported Triggers:**
- Issue comments containing `@kodelet`
- New issues with `@kodelet` in the body
- Pull request review comments with `@kodelet`
- Pull request reviews containing `@kodelet`

**Security Features:**
- Only responds to repository owners, members, and collaborators
- Uses repository secrets for secure API key management
- Runs with minimal required permissions (read-only access)

**Configuration Options:**
```bash
kodelet gha-agent-onboard --github-app "kodelet" --auth-gateway-endpoint "https://gha-auth-gateway.kodelet.com/api/github"
```

After the pull request is merged, team members can mention `@kodelet` in issues and pull requests to get automated assistance with development tasks.

### Image Input Support

Kodelet supports image inputs for vision-enabled models (currently Anthropic Claude models only). You can provide images through local file paths or HTTPS URLs.

```bash
# Single image analysis
kodelet run --image /path/to/screenshot.png "What's wrong with this UI?"

# Multiple images (local and remote)
kodelet run --image ./diagram.png --image https://example.com/mockup.jpg "Compare these designs"

# Architecture diagram analysis
kodelet run --image ./architecture.png "Review this system architecture and suggest improvements"
```

**Supported Features:**
- **Local Images**: JPEG, PNG, GIF, and WebP formats
- **Remote Images**: HTTPS URLs only (for security)
- **Multiple Images**: Up to 10 images per message
- **Size Limits**: Maximum 5MB per image file
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

As conversations grow longer, they may approach the context window limit. Kodelet automatically compacts context when utilization exceeds a threshold (default 80%), but you can also manually compact the context using the built-in `compact` recipe:

```bash
# Manually compact the current conversation
kodelet run -r compact --follow

# Continue working with the compacted context
kodelet run --follow "now implement the next feature"
```

The `compact` recipe generates a comprehensive summary of the conversation history, then replaces the conversation with that summary. This preserves all essential context while significantly reducing token usage.

**When to use manual compaction:**
- Before starting a new phase of work on a long-running task
- When you notice responses slowing down due to large context
- To create a checkpoint before major changes

**Creating custom compaction recipes:**

You can create custom compact recipes with different summarization strategies. See [Fragments/Recipes Documentation](./FRAGMENTS.md#recipe-hooks) for details.

### Conversation Management

Manage your conversation history:

```bash
# List conversations
kodelet conversation list
kodelet conversation list --search "term" --sort-by "updated" --sort-order "desc"

# View conversation details
kodelet conversation show <conversation-id>
kodelet conversation show <conversation-id> --format [text|json|raw]

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

Kodelet automatically detects and loads `AGENTS.md` context files from the current working directory and accessed subdirectories.

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
export KODELET_MODEL="claude-sonnet-4-5-20250929"
export KODELET_MAX_TOKENS="8192"
export KODELET_CACHE_EVERY="5"  # Cache messages every N interactions (0 to disable)

# LLM configuration - OpenAI
export OPENAI_API_KEY="sk-..."
export KODELET_PROVIDER="openai"
export KODELET_MODEL="gpt-4.1"
export KODELET_MAX_TOKENS="8192"
export KODELET_REASONING_EFFORT="medium"  # low, medium, high

# Profile configuration
export KODELET_PROFILE="premium"  # Use a specific profile

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
model: "claude-sonnet-4-5-20250929"
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
model: "claude-sonnet-4-5-20250929"
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

# MCP configuration
mcp:
  servers:
    fs:
      command: "npx" # Command to execute for stdio server
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/files"]
      tool_white_list: ["list_directory"] # Optional tool white list
   some_sse_server:   # sse config
      base_url: "http://localhost:8000" # Base URL for SSE server
      headers: # Headers for HTTP requests
        Authorization: "Bearer token"
      tool_white_list: ["tool1", "tool2"] # Optional tool white list
```

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

# Profile override for single command
kodelet run --profile premium "explain this architecture"
```

## Configuration Profiles

Kodelet includes a comprehensive profile system that allows you to define and switch between named configurations for different use cases. This eliminates the need to manually edit configuration files when experimenting with different model setups.

### Profile Definition

Profiles are defined in your configuration files using the `profiles` section. Each profile can override any configuration setting:

```yaml

# Default profile
model: "claude-sonnet-4-5-20250929"
weak_model: "claude-haiku-4-5-20251001"
max_tokens: 16000
weak_model_max_tokens: 8192
thinking_budget_tokens: 8000

# Active profile selection
profile: "premium"  # Optional: specify the active profile

# Profile definitions
profiles:
  premium:
    weak_model: "sonnet-45" # alias to "claude-sonnet-4-5-20250929"
    max_tokens: 16000
    weak_model_max_tokens: 8192
    thinking_budget_tokens: 8000

  openai:
    provider: "openai"
    use_copilot: true
    model: "gpt-4.1"
    weak_model: "gpt-4.1-mini"
    max_tokens: 16000
    reasoning_effort: "medium"

  xai:
    provider: "openai"
    model: "grok-3"
    weak_model: "grok-3-mini"
    max_tokens: 16000
    reasoning_effort: "none"
    openai:
      preset: "xai"

  mix-n-match:
    # Main agent uses Claude
    provider: "anthropic"
    model: "claude-sonnet-4-5-20250929"
    weak_model: "claude-haiku-4-5-20251001"
    max_tokens: 16000
    # Subagent uses OpenAI profile for cross-provider support
    subagent_args: "--profile openai-subagent"

  # Subagent profile for mix-n-match (cross-provider example)
  openai-subagent:
    provider: "openai"
    model: "o3"
    reasoning_effort: "high"
    allowed_tools: ["file_read", "glob_tool", "grep_tool", "thinking"]

# Model aliases work across all profiles
aliases:
    haiku-45: claude-haiku-4-5-20251001
    opus-46: claude-opus-4-6
    sonnet-45: claude-sonnet-4-5-20250929
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
kodelet profile show premium
kodelet profile show default  # Shows base configuration
```

**Switch to a different profile:**
```bash
kodelet profile use premium       # Switch in repository config (./kodelet-config.yaml)
kodelet profile use openai -g     # Switch in global config (~/.kodelet/config.yaml)
kodelet profile use default       # Use base configuration without any profile
```

**Practical workflow examples:**
```bash
# Check what's currently active, then switch to a different profile
kodelet profile current
kodelet profile use development

# Use a premium profile globally across all projects
kodelet profile use premium -g

# Show the merged configuration for a specific profile
kodelet profile show mix-n-match

# Return to base configuration (no profile)
kodelet profile use default
```

### Profile Usage

**Temporary profile override for single commands:**
```bash
# Use a specific profile for a single command without changing config
kodelet run --profile premium "explain this architecture"
kodelet commit --profile premium
kodelet run --profile openai "what does this function do?"

# Use base configuration without any profile
kodelet run --profile default "use base configuration"
```

**Environment variable override:**
```bash
export KODELET_PROFILE="premium"
kodelet run "this will use the premium profile"
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

## LLM Providers

### Anthropic Claude

Kodelet supports various Anthropic Claude models:
- `claude-sonnet-4-5-20250929` (recommended for standard tasks)
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
- Reasoning effort control (low, medium, high)
- Function calling capabilities
- Vision support (planned)

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

## Custom Tools

Kodelet supports custom executable tools that extend its capabilities beyond the built-in tool set. Custom tools are standalone executables (scripts or binaries) that implement a simple two-command protocol and can be written in any programming language.

### Creating Custom Tools

Custom tools are executable files that respond to two commands:
- `<tool> description` - Returns a JSON schema describing the tool
- `<tool> run` - Executes the tool with JSON input from stdin

**Basic Requirements:**
1. **Executable**: The file must have execute permissions (`chmod +x`)
2. **Two Commands**: Must support `description` and `run` commands
3. **JSON Protocol**: Uses JSON for both schema definition and input/output

### Tool Protocol

**Description Command:**
```bash
./my-tool description
```

Must return a JSON object with this structure:
```json
{
  "name": "my_tool",
  "description": "Brief description of what this tool does",
  "input_schema": {
    "type": "object",
    "properties": {
      "param1": {
        "type": "string",
        "description": "Description of parameter"
      },
      "param2": {
        "type": "integer",
        "description": "Another parameter"
      }
    },
    "required": ["param1"]
  }
}
```

**Run Command:**
```bash
echo '{"param1": "value", "param2": 42}' | ./my-tool run
```

The tool receives JSON input via stdin and can:
- **Success**: Write output to stdout and exit with code 0
- **Error**: Write error message to stderr and exit with non-zero code
- **JSON Error**: Write `{"error": "message"}` to stdout for structured errors

### Directory Structure

Custom tools are discovered from two directories:

**Global Tools**: `~/.kodelet/tools/`
- Available across all projects
- Good for general-purpose utilities

**Local Tools**: `./.kodelet/tools/`
- Project-specific tools
- Override global tools with the same name
- Should be committed to your repository

### Custom Tools Configuration

Configure custom tools behavior in your `config.yaml` or `kodelet-config.yaml`:

```yaml
custom_tools:
  # Enable/disable custom tools (default: true)
  enabled: true

  # Global tools directory (default: ~/.kodelet/tools)
  global_dir: "~/.kodelet/tools"

  # Local tools directory (default: ./.kodelet/tools)
  local_dir: "./.kodelet/tools"

  # Execution timeout (default: 30s)
  timeout: 30s

  # Maximum output size (default: 100KB)
  max_output_size: 102400

  # Tool whitelist - only specified tools will be loaded (empty means load all tools)
  # When the whitelist is empty, all discovered custom tools will be available
  # When specified, only tools with these exact names will be loaded
  tool_white_list:
    - "my-custom-tool"
    - "database-backup"
    - "deploy-script"
```

**Tool Whitelisting:**
The `tool_white_list` configuration allows you to control which custom tools are loaded and available for use. When the whitelist is empty or not specified, all discovered custom tools in the configured directories will be available. When you specify tool names in the whitelist, only those exact tools will be loaded, providing granular control over which tools are accessible in your environment.

**Command Line Override:**
```bash
# Temporary disable custom tools
kodelet run --config custom_tools.enabled=false "query"
```

### Custom Tools Examples

**1. Simple Hello Tool (Bash):**

```bash
#!/bin/bash
# File: ~/.kodelet/tools/hello

case "$1" in
  "description")
    cat <<EOF
{
  "name": "hello",
  "description": "Say hello to a person",
  "input_schema": {
    "type": "object",
    "properties": {
      "name": {
        "type": "string",
        "description": "The name of the person"
      },
      "age": {
        "type": "integer",
        "description": "The age of the person (optional)"
      }
    },
    "required": ["name"]
  }
}
EOF
    ;;
  "run")
    # Read JSON from stdin
    input=$(cat)
    name=$(echo "$input" | jq -r '.name')
    age=$(echo "$input" | jq -r '.age // empty')

    if [ -n "$age" ]; then
      echo "Hello, $name! You are $age years old."
    else
      echo "Hello, $name!"
    fi
    ;;
  *)
    echo "Usage: hello [description|run]" >&2
    exit 1
    ;;
esac
```

**2. Git Info Tool (Advanced Bash):**

```bash
#!/bin/bash
# File: ./.kodelet/tools/git_info

case "$1" in
  "description")
    cat <<EOF
{
  "name": "git_info",
  "description": "Get current git repository information",
  "input_schema": {
    "type": "object",
    "properties": {}
  }
}
EOF
    ;;
  "run")
    if ! git rev-parse --git-dir >/dev/null 2>&1; then
      echo '{"error": "Not in a git repository"}'
      exit 0
    fi

    branch=$(git branch --show-current)
    commit=$(git rev-parse HEAD)
    uncommitted=$(git status --porcelain | wc -l)

    cat <<EOF
{
  "branch": "$branch",
  "commit": "$commit",
  "uncommitted_changes": $uncommitted
}
EOF
    ;;
esac
```

**3. Python Tool Example:**

```python
#!/usr/bin/env python3
# File: ~/.kodelet/tools/analyze_logs

import json
import sys
import os

def description():
    return {
        "name": "analyze_logs",
        "description": "Analyze log files for errors and patterns",
        "input_schema": {
            "type": "object",
            "properties": {
                "file_path": {
                    "type": "string",
                    "description": "Path to the log file"
                },
                "pattern": {
                    "type": "string",
                    "description": "Pattern to search for (optional)"
                }
            },
            "required": ["file_path"]
        }
    }

def run():
    try:
        input_data = json.load(sys.stdin)
        file_path = input_data['file_path']
        pattern = input_data.get('pattern', 'ERROR')

        if not os.path.exists(file_path):
            print(json.dumps({"error": f"File not found: {file_path}"}))
            return

        with open(file_path, 'r') as f:
            lines = f.readlines()

        matches = [line.strip() for line in lines if pattern in line]

        result = {
            "total_lines": len(lines),
            "matches": len(matches),
            "pattern": pattern,
            "sample_matches": matches[:10]  # First 10 matches
        }

        print(json.dumps(result, indent=2))

    except Exception as e:
        print(json.dumps({"error": str(e)}))

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: analyze_logs [description|run]", file=sys.stderr)
        sys.exit(1)

    command = sys.argv[1]
    if command == "description":
        print(json.dumps(description(), indent=2))
    elif command == "run":
        run()
    else:
        print(f"Unknown command: {command}", file=sys.stderr)
        sys.exit(1)
```

**Usage in Kodelet:**

Once tools are created and made executable, Kodelet automatically discovers them and makes them available:

```bash
# Kodelet will find and use your custom tools
kodelet run "Say hello to Alice who is 25 years old"
# Uses: custom_tool_hello

kodelet run "What's the current git status of this repo?"
# Uses: custom_tool_git_info

kodelet run "Analyze the server logs for any ERROR patterns"
# Uses: custom_tool_analyze_logs
```

**Best Practices:**

1. **Error Handling**: Always handle errors gracefully and provide helpful error messages
2. **Input Validation**: Validate required parameters and provide clear error messages
3. **Documentation**: Write clear descriptions and parameter documentation
4. **Testing**: Test both `description` and `run` commands manually before use
5. **Permissions**: Ensure tools have proper execute permissions (`chmod +x`)
6. **Dependencies**: Document any external dependencies (jq, python, etc.)
7. **Security**: Be careful with user input, especially when executing system commands

### Generate Custom Tool

Kodelet includes a built-in `custom-tool` recipe that automatically generates custom tool templates based on your task description. This is the fastest way to create new tools with proper structure and best practices.

**Generate a Custom Tool:**

```bash
# Generate a weather tool without API key requirement (saved locally)
kodelet run -r custom-tool --arg task="implement a tool to fetch the weather based on the location, ideally without requiring api key"

# Generate a global tool available across all projects
kodelet run -r custom-tool --arg task="format and validate JSON" --arg global=true
```

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
├── reference.md      (optional)
├── examples.md       (optional)
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

## Examples
Concrete usage examples...
```

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
# Add all skills from a GitHub repository
kodelet skill add orgname/skills

# Add a specific skill from a repository
kodelet skill add orgname/skills --dir skills/specific-skill

# Add skills from a specific version/branch/tag
kodelet skill add orgname/skills@v0.1.0
kodelet skill add orgname/skills@main

# Add skills to global directory (~/.kodelet/skills)
kodelet skill add orgname/skills -g

# List all installed skills
kodelet skill list

# Remove a skill from local directory
kodelet skill remove skill-name

# Remove a skill from global directory
kodelet skill remove skill-name -g
```

**Requirements:**
- GitHub CLI (`gh`) must be installed and authenticated
- Run `gh auth login` if not already authenticated

### Disabling Skills

To run without skills for a single session:

```bash
kodelet run --no-skills "your query"
```

For detailed skill creation guide, see [docs/SKILLS.md](SKILLS.md).

## Agent Lifecycle Hooks

Agent Lifecycle Hooks allow external scripts to observe and control agent behavior. Hooks are language-agnostic executables that receive JSON payloads and can block operations, modify inputs/outputs, or trigger follow-up actions.

**Use Cases:**
- **Audit logging**: Record all tool invocations and user interactions
- **Security controls**: Block potentially harmful tool calls or inputs
- **Monitoring & alerting**: Send notifications to external systems
- **Compliance**: Enforce organizational policies on agent behavior

### Hook Types

| Hook Type | Trigger Point | Can Block | Can Modify |
|-----------|--------------|-----------|------------|
| `before_tool_call` | Before tool execution | Yes | Tool input |
| `after_tool_call` | After tool execution | No | Tool output |
| `user_message_send` | When user sends message | Yes | N/A |
| `agent_stop` | When agent would stop | No | Follow-up messages |

### Creating Hooks

Hooks are executable files discovered from four directories (in precedence order):

**Hook Locations:**
- `.kodelet/hooks/` - Repository-local standalone (highest precedence)
- `.kodelet/plugins/<org@repo>/hooks/` - Repository-local plugin hooks
- `~/.kodelet/hooks/` - User-global standalone
- `~/.kodelet/plugins/<org@repo>/hooks/` - User-global plugin hooks (lowest precedence)

Plugin hooks are prefixed with `org/repo/` (e.g., `jingkaihe/hooks/audit-logger`).

**Example Security Hook (Bash):**

```bash
#!/bin/bash
# File: .kodelet/hooks/security_guardrail

case "$1" in
  "hook")
    echo "before_tool_call"
    ;;
  "run")
    # Read JSON payload from stdin
    payload=$(cat)
    tool_name=$(echo "$payload" | jq -r '.tool_name')

    # Block dangerous commands
    if [ "$tool_name" == "bash" ]; then
      command=$(echo "$payload" | jq -r '.tool_input.command // ""')
      if [[ "$command" == *"rm -rf"* ]]; then
        echo '{"blocked":true,"reason":"rm -rf commands are not allowed"}'
        exit 0
      fi
    fi

    echo '{"blocked":false}'
    ;;
  *)
    exit 1
    ;;
esac
```

**Example Audit Logger (Bash):**

```bash
#!/bin/bash
# File: ~/.kodelet/hooks/audit_logger

if [ "$1" == "hook" ]; then
    echo "after_tool_call"
    exit 0
fi

if [ "$1" == "run" ]; then
    payload=$(cat)
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) | $payload" >> ~/.kodelet/audit.log
    # Empty output = no modification
    exit 0
fi

exit 1
```

### Hook Protocol

Hooks implement a simple two-command protocol:

**Discovery Command:**
```bash
./my_hook hook
# Output: hook type (e.g., "before_tool_call")
```

**Execution Command:**
```bash
echo '{"event":"before_tool_call","tool_name":"bash",...}' | ./my_hook run
# Output: JSON result (or empty for no action)
```

**Payload Structure (before_tool_call):**
```json
{
  "event": "before_tool_call",
  "conv_id": "conversation-id",
  "tool_name": "bash",
  "tool_input": {"command": "ls -la"},
  "tool_user_id": "tool-call-id",
  "cwd": "/current/working/dir",
  "invoked_by": "main"
}
```

**Result Structure (before_tool_call):**
```json
{
  "blocked": false,
  "reason": "optional reason if blocked",
  "input": {"modified": "input"}
}
```

**Hook Behavior:**
- Non-zero exit codes indicate hook failure (logged but doesn't halt agent)
- Empty stdout with exit code 0 means "no action" (not blocked, no modification)
- 30-second timeout prevents hung hooks from blocking the agent
- Deny-fast semantics: if any hook blocks, execution stops immediately

### Disabling Hooks

To run without hooks for a single session:

```bash
kodelet run --no-hooks "your query"
```

Hooks are automatically disabled for `kodelet commit` and `kodelet pr` commands.

For detailed hook creation guide including payload structures, example implementations, and debugging tips, see [HOOKS.md](HOOKS.md).

## Key Features

- **Intelligent Engineering Assistant**: Automates software engineering tasks and production operations with agentic capabilities.
- **Interactive Architecture Design**: Collaboratively design and refine system architectures through natural dialogue.
- **Continuous Code Intelligence**: Analyzes, understands, and improves your codebase while answering technical questions in context.
- **Agent Context Files**: Automatic loading of project-specific context from `AGENTS.md` files for enhanced project understanding.
- **Custom Tools**: Extend Kodelet with your own executable tools written in any programming language using a simple JSON protocol.
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
