---
name: kodelet
description: Kodelet CLI usage guide, commands, configuration, and workflows. Use when users ask about kodelet features, commands, configuration options, or how to accomplish tasks with kodelet.
---

# Kodelet - LLM-Friendly Guide

> **Kodelet** is a lightweight agentic SWE Agent that runs as an interactive CLI tool. It performs software engineering and production operations tasks using AI assistance.

## Quick Start

### Installation

```bash
# Using install script
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash

# Update to latest version
kodelet update
```

## Core Usage Modes

### One-shot Mode
Execute single commands quickly:

```bash
# Basic query
kodelet run "your query"

# Continue most recent conversation
kodelet run --follow "continue the task"
kodelet run -f "quick follow-up"

# Resume specific conversation
kodelet run --resume CONVERSATION_ID "more questions"

# Don't save conversation
kodelet run --no-save "temporary query"

# Output only the final result (no intermediate output or usage stats)
kodelet run --result-only "what is 2+2"

# Disable all tools (for simple query-response usage)
kodelet run --no-tools "what is the capital of France?"
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

## Key Features

### Agent Context Files
Kodelet automatically loads project-specific context from `AGENTS.md` in your current directory. This file should contain:

- Project overview and structure
- Tech stack and dependencies
- Engineering principles and coding style
- Key commands (build, test, lint, deploy)
- Configuration and environment setup
- Testing conventions
- Error handling patterns

Bootstrap your context file:
```bash
kodelet run -r init
```

### Fragments/Recipes System
Reusable prompt templates with variable substitution, bash command execution, and tool/command restrictions.

**Built-in recipes:**
```bash
kodelet recipe list              # List all available recipes
kodelet recipe show init         # View recipe content

# Repository initialization
kodelet run -r init             # Bootstrap AGENTS.md with project context

# Custom tool creation
kodelet run -r custom-tool --arg task="fetch weather data"
kodelet run -r custom-tool --arg task="validate JSON" --arg global=true
```

**Recipe capabilities:**
- Variable substitution: `{{.variable_name}}`
- Bash command execution: `{{bash "git" "branch" "--show-current"}}`
- Default values: Define fallbacks in recipe frontmatter
- Tool restrictions: Limit available tools with `allowed_tools`
- Command restrictions: Limit bash commands with `allowed_commands`

**Custom recipes:**
Create templates in `./recipes/` or `~/.kodelet/recipes/`:

```markdown
---
name: My Custom Recipe
description: Brief description
defaults:
  project: "default-value"
allowed_tools:
  - file_read
  - grep_tool
  - bash
allowed_commands:
  - "git *"
  - "cat *"
---

## Context:
Current branch: {{bash "git" "branch" "--show-current"}}
Project: {{.project}}

## Task:
Please analyze {{.focus_area}}
```

Usage:
```bash
kodelet run -r my-recipe --arg project="Kodelet" --arg focus_area="security"
kodelet run -r my-recipe "additional context or instructions"
```

### Agentic Skills
Model-invoked capabilities that package domain expertise. Unlike fragments (user-invoked), skills are automatically invoked when relevant to your task.

**Skill locations:**
- `./.kodelet/skills/<name>/SKILL.md` - Repository-local (higher precedence)
- `~/.kodelet/skills/<name>/SKILL.md` - User-global

**Managing skills:**
```bash
# Add skills from GitHub repository
kodelet skill add orgname/skills                        # Add all skills
kodelet skill add orgname/skills --dir skills/my-skill  # Add specific skill
kodelet skill add orgname/skills@v0.1.0                 # From specific version
kodelet skill add orgname/skills -g                     # To global directory

# List installed skills
kodelet skill list

# Remove skills
kodelet skill remove skill-name                         # From local directory
kodelet skill remove skill-name -g                      # From global directory
```

**Creating a skill:**
```markdown
---
name: my-skill
description: Brief description for model decision-making
---

# My Skill

## Instructions
Step-by-step guidance for the agent...
```

**Configuration:**
```yaml
skills:
  enabled: true      # Default: true
  allowed:           # Empty = all skills enabled
    - pdf
    - xlsx
```

**Disabling skills:**
```bash
kodelet run --no-skills "query"
```

### Agent Lifecycle Hooks
Hooks allow external scripts to observe and control agent behavior for audit logging, security controls, and monitoring.

**Hook locations:**
- `./.kodelet/hooks/` - Repository-local (higher precedence)
- `~/.kodelet/hooks/` - User-global

**Hook protocol:**
1. `./hook hook` - Discovery: returns the event type string
2. `echo '<json_payload>' | ./hook run` - Execution: receives JSON payload via stdin, returns JSON result

**Payload Types (TypeScript):**
```typescript
type HookType = "before_tool_call" | "after_tool_call" | "user_message_send" | "agent_stop";
type InvokedBy = "main" | "subagent";

interface BasePayload {
  event: HookType;
  conv_id: string;
  cwd: string;
  invoked_by: InvokedBy;
}

// before_tool_call: Can block or modify tool input
interface BeforeToolCallPayload extends BasePayload {
  event: "before_tool_call";
  tool_name: string;
  tool_input: Record<string, unknown>;
  tool_user_id: string;
}
interface BeforeToolCallResult {
  blocked: boolean;
  reason?: string;
  input?: Record<string, unknown>;
}

// after_tool_call: Can modify tool output
interface AfterToolCallPayload extends BasePayload {
  event: "after_tool_call";
  tool_name: string;
  tool_input: Record<string, unknown>;
  tool_output: { toolName: string; success: boolean; error?: string; timestamp: string };
  tool_user_id: string;
}
interface AfterToolCallResult {
  output?: { toolName: string; success: boolean; error?: string; timestamp: string };
}

// user_message_send: Can block message
interface UserMessageSendPayload extends BasePayload {
  event: "user_message_send";
  message: string;
}
interface UserMessageSendResult {
  blocked: boolean;
  reason?: string;
}

// agent_stop: Can return follow-up messages
interface AgentStopPayload extends BasePayload {
  event: "agent_stop";
  messages: Array<{ role: "user" | "assistant"; content: string }>;
}
interface AgentStopResult {
  follow_up_messages?: string[];
}
```

**Example hook (only acts for main agent):**
```bash
#!/bin/bash
if [ "$1" == "hook" ]; then echo "agent_stop"; exit 0; fi
if [ "$1" == "run" ]; then
    payload=$(cat)
    invoked_by=$(echo "$payload" | jq -r '.invoked_by')
    if [ "$invoked_by" != "main" ]; then exit 0; fi
    if [ -f "./cleanup-needed.txt" ]; then
        echo '{"follow_up_messages":["Please clean up temporary files."]}'
    fi
    exit 0
fi
```

**Disabling hooks:**
```bash
kodelet run --no-hooks "query"
```

### Git Integration

**AI Commit Messages:**
```bash
git add .
# Recommended: fast, non-interactive, no conversation saved
kodelet commit --short --no-confirm --no-save

# Other options
kodelet commit                  # Interactive with confirmation
kodelet commit --short          # Shorter message with confirmation
kodelet commit --no-confirm     # Skip confirmation only
```

**Pull Requests:**
```bash
kodelet pr                      # Create PR with AI-generated description
kodelet pr --target main        # Specify target branch
kodelet pr --draft              # Create draft PR
```

**Issue Resolution:**
```bash
kodelet issue-resolve --issue-url https://github.com/owner/repo/issues/123
```

**PR Response:**
```bash
kodelet pr-respond --pr-url https://github.com/owner/repo/pull/456
kodelet pr-respond --pr-url URL --review-id 123456
```

### GitHub Actions Background Agent
Automated agent that responds to `@kodelet` mentions:

```bash
kodelet gha-agent-onboard       # Complete setup (GitHub app + secrets + workflow)
```

After setup, team members can mention `@kodelet` in issues and PRs for automated assistance.

### Image Input Support
Vision-enabled analysis with multi-modal models:

```bash
# Single image
kodelet run --image /path/to/screenshot.png "What's wrong with this UI?"

# Multiple images
kodelet run --image ./diagram.png --image ./mockup.jpg "Compare these designs"
```

**Supported:** JPEG, PNG, GIF, WebP | **Max:** 5MB per image, 10 images per message
**Provider Support:** All providers (Anthropic, OpenAI, Google) if the model supports multi-modal

### Custom Tools
Extend kodelet with executable tools in any language:

**Directory structure:**
- `~/.kodelet/tools/` - Global tools
- `./.kodelet/tools/` - Project-specific tools

**Generate custom tool:**
```bash
kodelet run -r custom-tool --arg task="fetch weather without API key"
kodelet run -r custom-tool --arg task="validate JSON" --arg global=true
```

**Tool protocol:**
```bash
./my-tool description  # Returns JSON schema
./my-tool run          # Executes with JSON input from stdin
```

### MCP Integration
Model Context Protocol for external integrations. Configure in `config.yaml`:

```yaml
mcp:
  servers:
    fs:
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/path"]
      tool_white_list: ["list_directory"]
```

## Conversation Management

```bash
# List conversations
kodelet conversation list
kodelet conversation list --search "keyword"

# View conversation
kodelet conversation show <id>
kodelet conversation show <id> --format json
kodelet conversation show <id> --stats-only        # Header/stats only, no messages
kodelet conversation show <id> --no-header         # Messages only, no header

# Stream conversation (real-time)
kodelet conversation stream <id>
kodelet conversation stream <id> --include-history  # Show history, then stream new
kodelet conversation stream <id> --history-only     # Show history and exit

# Delete conversation
kodelet conversation delete <id>

# Fork conversation (experimental branching)
# 1. Ensure clean git status
# 2. Fork the conversation to try different approaches
# 3. If it doesn't work: git reset --hard and continue with original
kodelet conversation fork <id>
```

**Output formats for `conversation show`:**

| Format | Description |
|--------|-------------|
| `--format text` | Human-readable output (default) |
| `--format json` | Structured JSON with id, provider, summary, usage, messages |
| `--format raw` | Full `ConversationRecord` dump as JSON (includes rawMessages, toolResults, metadata, etc.) |

**Flags:**
- `--stats-only`: Show header/stats without messages
- `--no-header`: Show messages only (for `text`/`json` formats)

## Configuration

Kodelet uses layered configuration:
1. Global: `~/.kodelet/config.yaml`
2. Repository: `./kodelet-config.yaml` (overrides global)

Example `config.yaml`:
```yaml
aliases:
    gemini-flash: gemini-2.5-flash
    gemini-pro: gemini-2.5-pro
    haiku-35: claude-3-5-haiku-20241022
    opus-45: claude-opus-4-5-20251101
    sonnet-45: claude-sonnet-4-5-20250929
max_tokens: 16000
model: sonnet-45
profile: default
thinking_budget_tokens: 8000
weak_model: haiku-35
weak_model_max_tokens: 8192
profiles:
    hybrid:
        max_tokens: 16000
        model: sonnet-45
        subagent_args: "--profile openai-subagent"
        thinking_budget_tokens: 8000
        weak_model: haiku-35
        weak_model_max_tokens: 8192
    openai-subagent:
        allowed_tools:
            - file_read
            - glob_tool
            - grep_tool
        model: gpt-5.2-codex
        openai:
            use_responses_api: true
        provider: openai
        reasoning_effort: high
    openai:
        max_tokens: 16000
        model: gpt-5
        provider: openai
        reasoning_effort: medium
        weak_model: gpt-5
    premium:
        max_tokens: 16000
        opus-45: claude-opus-4-5-20251101
        thinking_budget_tokens: 8000
        weak_model: sonnet-45
        weak_model_max_tokens: 8192
    google:
        max_tokens: 16000
        model: gemini-pro
        provider: google
        weak_model: gemini-flash
        weak_model_max_tokens: 8192
    xai:
        max_tokens: 16000
        model: grok-code-fast-1
        openai:
            preset: xai
        provider: openai
        reasoning_effort: none
        weak_model: grok-code-fast-1
```

## LLM Providers

### Anthropic Claude (Recommended)
**Models:**
- `claude-sonnet-4-5-20250929` - Recommended for standard tasks
- `claude-3-5-haiku-20241022` - Lightweight tasks
- `claude-opus-4-1-20250805` - Complex reasoning

**Features:**
- Vision capabilities
- Message caching
- Extended thinking mode

**Setup:**
```bash
# Option 1: Using anthropic-login (no environment variable needed)
kodelet anthropic-login

# Option 2: Using environment variable
export ANTHROPIC_API_KEY="sk-ant-api..."

# Use the provider
kodelet run --provider anthropic "query"
```

### OpenAI
**Models:**
- `gpt-5` - Latest GPT model

**Features:**
- Reasoning effort control (low, medium, high)
- Function calling
- Vision capabilities (multi-modal models)

**Setup:**
```bash
export OPENAI_API_KEY="sk-..."
kodelet run --provider openai --model gpt-5 "query"
```

### Google Gemini
**Models:**
- `gemini-2.5-pro` - Advanced capabilities
- `gemini-2.5-flash` - Fast responses

**Features:**
- Vision capabilities (multi-modal models)
- Fast inference

**Setup:**
```bash
export GOOGLE_API_KEY="your-api-key"
kodelet run --provider google --model gemini-2.5-pro "query"
```

## Advanced Features

### Streaming and Programmatic Access
Headless mode outputs structured JSON stream:

```bash
# Stream JSON output
kodelet run --headless "analyze this codebase"
kodelet run --headless --include-history "continue analysis"

# Real-time conversation stream
kodelet conversation stream CONVERSATION_ID
kodelet conversation stream CONVERSATION_ID --include-history
```

**Partial Message Streaming (`--stream-deltas`):**
Enables real-time token streaming in headless mode, outputting text as it's generated:

```bash
# Stream partial text deltas
kodelet run --headless --stream-deltas "explain how TCP works"

# Show only text deltas in real-time
kodelet run --headless --stream-deltas "write a poem" | \
    jq -r 'select(.kind == "text-delta") | .delta' | tr -d '\n'

# Show thinking in real-time
kodelet run --headless --stream-deltas "solve this puzzle" | \
    jq -r 'select(.kind == "thinking-delta") | .delta' | tr -d '\n'
```

**Delta Event Types:**
| Kind | Description |
|------|-------------|
| `text-delta` | Partial text content chunk |
| `thinking-delta` | Partial thinking content chunk |
| `thinking-start` | Thinking block begins |
| `thinking-end` | Thinking block ends |
| `content-end` | Content block ends |

**Example Delta Output:**
```jsonl
{"kind":"thinking-start","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-delta","delta":"Let me analyze...","conversation_id":"abc123","role":"assistant"}
{"kind":"thinking-end","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":"The answer","conversation_id":"abc123","role":"assistant"}
{"kind":"text-delta","delta":" is 42.","conversation_id":"abc123","role":"assistant"}
{"kind":"content-end","conversation_id":"abc123","role":"assistant"}
{"kind":"text","content":"The answer is 42.","conversation_id":"abc123","role":"assistant"}
```

Note: Complete messages are still emitted after delta streams for clients that ignore deltas.

**StreamEntry JSON format (complete messages):**
```json
{"kind":"text","role":"user","content":"What files are here?","conversation_id":"conv_123"}
{"kind":"thinking","role":"assistant","content":"User wants to see files..."}
{"kind":"tool-use","tool_name":"bash","input":"{\"command\":\"ls\"}","tool_call_id":"call_456"}
{"kind":"tool-result","tool_name":"bash","result":"file1.txt\nfile2.go"}
{"kind":"text","role":"assistant","content":"Here are the files..."}
```

**Processing with jq:**
```bash
# Extract text only
kodelet run --headless "query" | jq -r 'select(.kind == "text") | .content'

# Monitor tool usage
kodelet conversation stream ID | jq 'select(.kind == "tool-use") | .tool_name'
```

### Web UI
Browser-based interface:

```bash
kodelet serve                   # Start on localhost:8080
kodelet serve --host 0.0.0.0 --port 3000
```

Access at `http://localhost:8080` for conversation management and chat interface.

### IDE Integration (ACP)
Kodelet implements the Agent Client Protocol (ACP) for integration with compatible IDEs like Zed and JetBrains:

```bash
kodelet acp                     # Start ACP agent mode (stdin/stdout JSON-RPC)
```

**Zed configuration** (settings.json):
```json
{
  "agent": {
    "command": "kodelet",
    "args": ["acp"]
  }
}
```

**Features:**
- Session persistence (conversations can be resumed)
- Image input support
- Full access to kodelet's built-in tools
- Embedded file context support

For detailed protocol documentation, see [docs/ACP.md](https://github.com/jingkaihe/kodelet/blob/main/docs/ACP.md).

### Feedback System
Send feedback to conversations:

```bash
kodelet feedback --follow "great job, but please add tests"
kodelet feedback --conversation-id ID "needs improvement on error handling"
```

### Shell Completion
Tab completion for bash, zsh, fish:

```bash
# Bash
echo 'source <(kodelet completion bash)' >> ~/.bashrc

# Zsh
echo 'source <(kodelet completion zsh)' >> ~/.zshrc

# Fish
kodelet completion fish > ~/.config/fish/completions/kodelet.fish
```

## Security & Best Practices

### Command Restrictions
Control which bash commands kodelet can execute:

```yaml
# config.yaml
allowed_commands:
  - "ls *"
  - "pwd"
  - "git status"
  - "npm *"
```

Or via environment:
```bash
export KODELET_ALLOWED_COMMANDS="ls *,pwd,git status"
```

### Tool Restrictions
Limit available tools:

```bash
kodelet run --allowed-tools "file_read,grep_tool,bash" "analyze code"
```

### Best Practices
1. **Use context files** (`AGENTS.md`) for project-specific conventions
2. **Create fragments** for repetitive tasks
3. **Utilize profiles** for different model configurations (note: can only switch between profiles using the same provider)
4. **Enable conversation persistence** to build on previous work
5. **Use `--follow` flag** to continue conversations efficiently
6. **Restrict commands** in automated environments
7. **Review AI-generated code** before committing

## Common Workflows

### Code Review
```bash
git diff main | kodelet run "review these changes for issues"
```

### Bug Investigation
```bash
kodelet run "analyze error logs and suggest fixes"
kodelet run --follow "implement the suggested fix"
```

### Refactoring
```bash
kodelet run "refactor user authentication to use middleware pattern"
```

### Documentation
```bash
kodelet run "add comprehensive docstrings to all functions in this file"
```

### Testing
```bash
kodelet run "write unit tests for the payment processing module"
```

## Resources

- **Website:** https://kodelet.com
- **GitHub:** https://github.com/jingkaihe/kodelet
- **Documentation:** https://github.com/jingkaihe/kodelet/tree/main/docs

## Version Information

```bash
kodelet version                 # Show version and build info
```

---

**Note:** This guide focuses on end-user usage. For development information, build processes, and contribution guidelines, see the project's GitHub repository and development documentation.
