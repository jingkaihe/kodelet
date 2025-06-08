# Development Guide

This guide is for developers who want to contribute to Kodelet or build it from source.

For user documentation, see the [User Manual](MANUAL.md).

## Prerequisites

- Go 1.24 or higher
- Make (for using Makefile commands)
- Git

## Setting Up Development Environment

1. Clone the repository:
   ```bash
   git clone https://github.com/jingkaihe/kodelet.git
   cd kodelet
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Set up your API keys for testing:
   ```bash
   export ANTHROPIC_API_KEY="sk-ant-api..."
   # or
   export OPENAI_API_KEY="sk-..."
   ```

## Building from Source

### Local Build

```bash
make build
```

This creates the binary in `./bin/kodelet`.

### Cross-Platform Build

```bash
make cross-build
```

This builds binaries for multiple platforms in the `./bin/` directory.

### Docker Build

```bash
make docker-build
```

## Development Commands

### Testing

```bash
# Run all tests
make test

# Run tests with coverage
go test -v -cover ./pkg/... ./cmd/...

# Run tests for a specific package
go test -v ./pkg/llm/...

# Acceptance tests
make e2e-test-docker
```

### Code Quality

```bash
# Run linter
make lint

# Format code
make format

# Check for formatting issues
gofmt -d .
```

### Local Development

1. Build the development version:
   ```bash
   make build
   ```

2. Test your changes:
   ```bash
   ./bin/kodelet run "test query"
   ```

3. Run specific functionality:
   ```bash
   ./bin/kodelet chat
   ./bin/kodelet watch
   ```

## Project Structure

```
├── cmd/kodelet/         # Application entry point
├── pkg/                 # Core packages
│   ├── conversations/   # Conversation storage and management
│   ├── llm/             # LLM client for AI interactions
│   │   ├── anthropic/   # Anthropic Claude API client
│   │   └── openai/      # OpenAI API client
│   ├── logger/          # Context-aware structured logging
│   ├── sysprompt/       # System prompt configuration
│   ├── telemetry/       # Telemetry components
│   ├── tools/           # Tool implementations
│   ├── tui/             # Terminal UI components
│   ├── types/           # Common types
│   └── utils/           # Utility functions
├── docs/                # Documentation
├── .github/             # GitHub configuration
└── Makefile             # Build automation
```

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make your changes
4. Run tests: `make test`
5. Run linter: `make lint`
6. Commit your changes: `git commit -am 'Add some feature'`
7. Push to the branch: `git push origin feature/my-feature`
8. Submit a pull request

## Release Process

1. Update version in `VERSION.txt`
2. Update `RELEASE.md` with changelog
3. Create release using make:
   ```bash
   make release
   ```

## Available Make Commands

For a complete list of available commands:

```bash
make help
```

Common commands:
- `make build` - Build the application
- `make test` - Run tests
- `make lint` - Run linter
- `make format` - Format code
- `make docker-build` - Build Docker image
- `make cross-build` - Build for multiple platforms
- `make release` - Create a release
