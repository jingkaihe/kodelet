# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with site reliability and platform engineering tasks. It uses the Anthropic Claude API to process user queries and execute various tools.

## Project Structure
```
├── .github/             # GitHub configuration
│   └── workflows/       # GitHub Actions workflows
├── bin/                 # Compiled binaries
├── cmd/
│   └── kodelet/         # Application entry point
└── pkg/
    ├── state/           # State management for the application
    ├── sysprompt/       # System prompt configuration and templates
    ├── tools/           # Tool implementations (bash, file operations, etc.)
    └── utils/           # Utility functions and helpers
```

The codebase follows a modular structure with clear separation of concerns:
- Core application logic in the `cmd/kodelet` directory with separate files for different execution modes
- State management interfaces and implementations in the `pkg/state` package
- System prompt configuration in the `pkg/sysprompt` package
- Tools for executing various operations in the `pkg/tools` package (bash, file operations, code search, todo management, etc.)
- Common utilities and helper functions in the `pkg/utils` package

## Tech Stack
- **Go 1.24+** - Programming language
- **Anthropic SDK** - For Claude AI integration
- **Docker** - For containerization

## Key Commands

### Building & Running

#### Build the application
```bash
make build
```

#### Build the Docker image
```bash
make docker-build
```

#### Run with Docker
```bash
make docker-run query="your query"
```

#### Run locally (one-shot mode)
```bash
go run cmd/kodelet/main.go run "your query"
```

#### Run locally (interactive chat mode)
```bash
go run cmd/kodelet/main.go chat
```

### Development

#### Run tests
```bash
make test
```

#### Format code
```bash
make format
```

#### Lint code
```bash
make lint
```

#### Display help information
```bash
make help
```

## Coding Conventions
- Use Go's standard formatting rules (enforced by `go fmt`)
- Follow standard Go error handling patterns
- Tools implement the `Tool` interface defined in pkg/tools/tools.go
- State is managed through the `State` interface in pkg/state/state.go
- Function and variable names use camelCase
- Type names use PascalCase

## Configuration
- Requires an Anthropic API key for Claude integration
- Uses Claude Sonnet 3.5 model by default

## Important Notes
- The bash tool has security restrictions to prevent dangerous commands
- The file_read tool has a maximum output limit of 100KB
- The kodelet.md file (this file) is used for project context
