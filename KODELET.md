# Kodelet Documentation

## Project Overview
Kodelet is a lightweight CLI tool that helps with software engineering tasks. It supports both Anthropic Claude and OpenAI APIs to process user queries and execute various tools.

## Project Structure
```
├── .github/             # GitHub configuration
├── .dockerignore        # Docker ignore file
├── .gitignore           # Git ignore file
├── adrs/                # Architecture Decision Records
├── bin/                 # Compiled binaries
├── cmd/                 # Application entry point
│   └── kodelet/         # Main application command
├── config.sample.yaml   # Sample configuration file
├── Dockerfile           # Docker configuration
├── docs/                # Documentation files
├── go.mod               # Go module file
├── go.sum               # Go dependencies checksum
├── install.sh           # Installation script
├── KODELET.md           # Project documentation
├── LICENSE              # License file
├── Makefile             # Build automation
├── pkg/                 # Core packages
│   ├── conversations/   # Conversation storage and management
│   ├── llm/             # LLM client for AI interactions
│   │   ├── anthropic/   # Anthropic Claude API client
│   │   └── openai/      # OpenAI API client
│   ├── sysprompt/       # System prompt configuration
│   │   └── templates/   # Prompt templates
│   │       └── components/ # Template components
│   │           └── examples/ # Template for the prompt examples
│   ├── telemetry/       # Telemetry components
│   ├── tools/           # Tool implementations
│   ├── tui/             # Terminal UI components
│   ├── types/           # Common types
│   │   ├── llm/         # LLM related types
│   │   └── tools/       # Tool related types
│   ├── utils/           # Utility functions
│   └── version/         # Version information
├── README.md            # Project overview
├── RELEASE.md           # Release notes
└── VERSION.txt          # Version information file
```

The codebase follows a modular structure with separation of concerns between LLM communication, tools, UI, and core functionality.

## Tech Stack
- **Go 1.24.2** - Programming language
- **Anthropic SDK** - For Claude AI integration (v0.2.0-beta.3)
- **OpenAI SDK** - For GPT models integration
- **Charm libraries** - TUI components
- **Cobra & Viper** - CLI commands and configuration
- **Docker** - For containerization

## Key Commands

### CLI Commands

#### One-shot Mode
```bash
# Basic one-shot query
kodelet run "your query"

# One-shot query with conversation persistence
kodelet run "your query"                     # saved automatically
kodelet run --resume CONVERSATION_ID "more"  # continue a conversation
kodelet run --no-save "temporary query"      # don't save the conversation
```

#### Interactive Chat Mode
```bash
kodelet chat
kodelet chat --plain
```

#### Conversation Management
```bash
kodelet conversation list
kodelet conversation list --search "term" --sort-by "updated" --sort-order "desc"
kodelet conversation show <conversation-id>
kodelet conversation show <conversation-id> --format [text|json|raw]
kodelet conversation delete <conversation-id>
kodelet conversation delete --no-confirm <conversation-id>
```

#### Watch Mode
```bash
kodelet watch [--include "*.go"] [--ignore ".git,node_modules"] [--verbosity level] [--debounce ms]
```

### Development Commands
```bash
make build          # Build the application
make cross-build    # Build for multiple platforms
make docker-build   # Build Docker image
make test           # Run tests
make format         # Format code
make lint           # Lint code
make release        # Create a release
make help           # Display help
```

## Configuration

1. **Environment Variables**:
   ```bash
   # LLM configuration - Anthropic
   export ANTHROPIC_API_KEY="sk-ant-api..."
   export KODELET_PROVIDER="anthropic"  # Optional, detected from model name
   export KODELET_MODEL="claude-3-7-sonnet-latest"
   export KODELET_MAX_TOKENS="8192"
   
   # LLM configuration - OpenAI
   export OPENAI_API_KEY="sk-..."
   export KODELET_PROVIDER="openai"
   export KODELET_MODEL="gpt-4.1"
   export KODELET_MAX_TOKENS="8192"
   export KODELET_REASONING_EFFORT="medium"  # low, medium, high
   ```

2. **Configuration File** (`config.yaml`):
   ```yaml
   # Anthropic configuration
   provider: "anthropic"
   model: "claude-3-7-sonnet-latest"
   max_tokens: 8192
   weak_model: "claude-3-5-haiku-latest"
   weak_model_max_tokens: 8192
   
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

3. **Command Line Flags**:
   ```bash
   # Anthropic example
   kodelet run --provider "anthropic" --model "claude-3-opus-20240229" --max-tokens 4096 --weak-model-max-tokens 2048 "query"
   
   # OpenAI example
   kodelet run --provider "openai" --model "gpt-4.1" --max-tokens 4096 --reasoning-effort "high" "query"
   ```

## LLM Architecture

Kodelet uses a `Thread` abstraction for all interactions with LLM providers (Anthropic Claude and OpenAI):
- Maintains message history and state
- Handles tool execution and responses
- Uses a handler-based pattern for processing responses
- Supports provider-specific features (thinking for Claude, reasoning effort for OpenAI)

The architecture provides a unified approach for both interactive and one-shot uses with token usage tracking for all API calls across different providers.
