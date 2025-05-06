# Kodelet

Kodelet is a lightweight CLI tool that helps with site reliability and platform engineering tasks.


## Development

### Prerequisites

- Go 1.24 or higher

### Running locally

You can use Kodelet in two ways:

#### Run Command (One-shot)

```bash
go build -o ./bin/kodelet ./cmd/kodelet/
./bin/kodelet run "your query"

#### Interactive Chat Mode

```bash
go run cmd/kodelet/main.go chat
```
Or using Make:
```bash
make chat
```

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

# Run with Docker
make docker-run query="your query"

# Display help information
make help
```
