# Kodelet User Manual

Kodelet is a lightweight agentic SWE Agent that runs as an interactive CLI tool in your terminal. It is capable of performing software engineering and production operating tasks.

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
kodelet run --no-save "temporary query"      # don't save the conversation
```

### Interactive Chat Mode

For extended conversations and complex tasks:

```bash
kodelet chat
kodelet chat --plain
```

### Watch Mode

Monitor file changes and automatically process files with special "@kodelet" comments:

```bash
kodelet watch [--include "*.go"] [--ignore ".git,node_modules"] [--verbosity level] [--debounce ms]
```

Options:
- `--ignore` or `-i`: Directories to ignore (default: `.git,node_modules`)
- `--include` or `-p`: File pattern to include (e.g., `*.go`, `*.{js,ts}`)
- `--verbosity` or `-v`: Verbosity level (`quiet`, `normal`, `verbose`)
- `--debounce` or `-d`: Debounce time in milliseconds (default: 500)

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

### Conversation Management

Manage your conversation history:

```bash
# List conversations
kodelet conversation list
kodelet conversation list --search "term" --sort-by "updated" --sort-order "desc"

# View conversation details
kodelet conversation show <conversation-id>
kodelet conversation show <conversation-id> --format [text|json|raw]

# Delete conversations
kodelet conversation delete <conversation-id>
kodelet conversation delete --no-confirm <conversation-id>
```

## Shell Completion

Kodelet provides shell completion support for bash, zsh, fish, and PowerShell. This enables tab completion for commands and flags, making the CLI experience more efficient.

### Setup Instructions

**Bash:**

To load completions in your current shell session:
```bash
source <(kodelet completion bash)
```

To load completions for every new session:

On Linux:
```bash
kodelet completion bash > /etc/bash_completion.d/kodelet
```

On macOS:
```bash
kodelet completion bash > $(brew --prefix)/etc/bash_completion.d/kodelet
```

**Zsh:**

If shell completion is not already enabled in your environment, you will need to enable it first:
```bash
echo "autoload -U compinit; compinit" >> ~/.zshrc
```

To load completions in your current shell session:
```bash
source <(kodelet completion zsh)
```

To load completions for every new session:

On Linux:
```bash
kodelet completion zsh > "${fpath[1]}/_kodelet"
```

On macOS:
```bash
kodelet completion zsh > $(brew --prefix)/share/zsh/site-functions/_kodelet
```

**Fish:**

To load completions in your current shell session:
```bash
kodelet completion fish | source
```

To load completions for every new session:
```bash
kodelet completion fish > ~/.config/fish/completions/kodelet.fish
```

**PowerShell:**

To load completions in your current shell session:
```powershell
kodelet completion powershell | Out-String | Invoke-Expression
```

To load completions for every new session, add the output of the above command to your PowerShell profile.

### Additional Options

All completion commands support these additional flags:
- `--no-descriptions`: Disable completion descriptions for a cleaner experience

Example:
```bash
kodelet completion bash --no-descriptions > /etc/bash_completion.d/kodelet
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
export KODELET_MODEL="claude-sonnet-4-0"
export KODELET_MAX_TOKENS="8192"
export KODELET_CACHE_EVERY="5"  # Cache messages every N interactions (0 to disable)

# LLM configuration - OpenAI
export OPENAI_API_KEY="sk-..."
export KODELET_PROVIDER="openai"
export KODELET_MODEL="gpt-4.1"
export KODELET_MAX_TOKENS="8192"
export KODELET_REASONING_EFFORT="medium"  # low, medium, high
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
model: "claude-sonnet-4-0"
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
model: "claude-sonnet-4-0"
max_tokens: 8192
weak_model: "claude-3-5-haiku-latest"
weak_model_max_tokens: 8192
cache_every: 10  # Cache messages every N interactions (0 to disable)

# Alternative OpenAI configuration
# provider: "openai"
# model: "gpt-4.1"
# max_tokens: 8192
# weak_model: "gpt-4.1-mini"
# weak_model_max_tokens: 4096
# reasoning_effort: "medium"
# weak_reasoning_effort: "low"

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
kodelet run --provider "anthropic" --model "claude-3-opus-20240229" --max-tokens 4096 --weak-model-max-tokens 2048 --cache-every 3 "query"

# OpenAI example
kodelet run --provider "openai" --model "gpt-4.1" --max-tokens 4096 --reasoning-effort "high" "query"
```

## LLM Providers

### Anthropic Claude

Kodelet supports various Anthropic Claude models:
- `claude-sonnet-4-0` (recommended for standard tasks)
- `claude-3-5-haiku-latest` (recommended for lightweight tasks)
- `claude-3-opus-20240229`

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

## Key Features

- **Intelligent Engineering Assistant**: Automates software engineering tasks and production operations with agentic capabilities.
- **Interactive Architecture Design**: Collaboratively design and refine system architectures through natural dialogue.
- **Continuous Code Intelligence**: Analyzes, understands, and improves your codebase while answering technical questions in context.
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

For more help, check the project repository: https://github.com/jingkaihe/kodelet