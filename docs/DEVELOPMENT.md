# Development Guide

## Prerequisites

- Go 1.24 or higher

## Running locally

You can use Kodelet in a few ways:

### Run Command (One-shot)

```bash
# Basic one-shot query
kodelet run "your idea"

# With conversation features
kodelet run "your idea"                      # saved as a conversation
kodelet run --resume CONVERSATION_ID "more"  # continue a conversation
kodelet run --no-save "temporary query"      # don't save as a conversation
```

### Generate Git Commit

```bash
kodelet commit
```

This command analyzes your staged changes (git diff --cached) and uses AI to generate a meaningful commit message following conventional commits format. You must stage your changes using `git add` before running this command.

Options:
- `--no-sign`: Disable commit signing (commits are signed by default)
- `--template` or `-t`: Use a template for the commit message
- `--short`: Generate a short commit message with just a description, no bullet points
- `--no-confirm`: Skip confirmation prompt and create commit automatically

### Interactive Chat Mode

The chat mode allows you to interact with Kodelet in a TUI.

```bash
kodelet chat
```

### Watch Mode

The watch mode allows you to monitor file changes and automatically process files with special "@kodelet" comments.

```bash
kodelet watch
```
Options:
- `--ignore` or `-i`: Directories to ignore (default: `.git,node_modules`)
- `--include` or `-p`: File pattern to include (e.g., `*.go`, `*.{js,ts}`)
- `--verbosity` or `-v`: Verbosity level (`quiet`, `normal`, `verbose`)
- `--debounce` or `-d`: Debounce time in milliseconds (default: 500)
- `--auto-completion-model`: Model to use for auto-completion requests

Make sure your Anthropic API key is set in your environment:

```bash
export ANTHROPIC_API_KEY="sk-ant-api..."
```

### Configuration

Kodelet uses Viper for configuration management. You can configure Kodelet in several ways:

1. **Environment Variables** - All environment variables should be prefixed with `KODELET_`:
   ```bash
   export ANTHROPIC_API_KEY="sk-ant-api..."
   export KODELET_MODEL="claude-sonnet-4-0"
   export KODELET_MAX_TOKENS="8192"
   export KODELET_WEAK_MODEL_MAX_TOKENS="8192"
   export KODELET_THINKING_BUDGET_TOKENS="4048"
   ```

2. **Configuration File** - Kodelet looks for a configuration file named `config.yaml` in:
   - Current directory
   - `$HOME/.kodelet/` directory

Example `config.yaml`:
```yaml
# Anthropic model to use
model: "claude-sonnet-4-0"

# Maximum tokens for responses
max_tokens: 8192

# Weak model to use for less complex tasks
weak_model: "claude-3-5-haiku-latest"

# Maximum tokens for weak model responses
weak_model_max_tokens: 8192
```

3. **Command Line Flags**:
   ```bash
   kodelet run --model "claude-3-opus-20240229" --max-tokens 4096 --weak-model-max-tokens 2048 "query"
   ```

## Available Make Commands

Kodelet provides several make commands to simplify development and usage:

```bash
# Build the application
make build

# Run tests
make test

# Run linter
make lint

# Format code
make format

# Build Docker image
make docker-build

# Display help information
make help
```
