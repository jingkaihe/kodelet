# Kodelet

Kodelet is a lightweight CLI tool that helps with site reliability and platform engineering tasks.

## Key Features

- **Interactive Chat**: Get AI assistance for SRE and platform engineering tasks through a modern TUI
- **One-shot Queries**: Run single queries for quick answers without starting a chat session
- **General Purpose Coding**: Simple write code with Kodelet
- **AI-Powered Git Commits**: Generate meaningful, conventional commits automatically from your staged changes
- **Watch Mode**: Monitor file changes and automatically process files with AI assistance using "@kodelet" comments


## Installation

```bash
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash
```

## Development

### Prerequisites

- Go 1.24 or higher

### Running locally

You can use Kodelet in two ways:

#### Run Command (One-shot)

```bash
make build
./bin/kodelet run "your query"
```

#### Generate Git Commit

```bash
./bin/kodelet commit
```

This command analyzes your staged changes (git diff --cached) and uses AI to generate a meaningful commit message following conventional commits format. You must stage your changes using `git add` before running this command.

Options:
- `--no-sign`: Disable commit signing (commits are signed by default)
- `--template` or `-t`: Use a template for the commit message

#### Interactive Chat Mode

```bash
./bin/kodelet chat
```
Or using Make:
```bash
make chat
```

#### Watch Mode

```bash
./bin/kodelet watch
```

Monitor file changes and automatically process files with special "@kodelet" comments. Options:
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
   export KODELET_MODEL="claude-3-7-sonnet-latest"
   export KODELET_MAX_TOKENS="2048"
   ```

2. **Configuration File** - Kodelet looks for a configuration file named `config.yaml` in:
   - Current directory
   - `$HOME/.kodelet/` directory

Example `config.yaml`:
```yaml
# Anthropic model to use
model: "claude-3-7-sonnet-latest"

# Maximum tokens for responses
max_tokens: 8192
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

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
Copyright (c) 2025 Jingkai He.
