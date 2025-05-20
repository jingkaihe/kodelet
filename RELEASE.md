# Kodelet

## 0.0.21.alpha (2025-05-20)

### New Features

- **MCP Integration**: Added support for the Model Context Protocol (MCP) which allows Kodelet to connect to external tools and services
  - New MCP server configuration options in `config.yaml`
  - Support for both stdio and SSE transport modes
  - Tool whitelisting for granular control over what tools are allowed to avoid prompt bloat.

- **File Access Tracking**: Added file last access tracking to conversation persistence
  - Improves context management for files accessed during conversations
  - Enables better persistence of file interactions

### Configuration

Added new configuration section for MCP in `config.yaml`:

```yaml
mcp:
  servers:
    fs:
      command: "npx"  # Command to execute for stdio server
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/files"]
      tool_white_list: ["list_directory"]  # Optional tool white list
```

### Dependencies

- Added MCP Go client (`github.com/mark3labs/mcp-go v0.29.0`)
- Added `github.com/hashicorp/go-multierror v1.1.1` for error handling

### Improvements

- **Code Cleanup**: Removed unused code

## 0.0.19.alpha (2025-05-19)

### Improvements

- **Enhanced Grep Tool**:
  - Improved file pattern matching to support both base name and relative path matches
  - Files now match if either their relative path or base name matches the include pattern
  - Example: `*.go` will now match both `foo.go` and `pkg/foo/bar.go`

## 0.0.18.alpha (2025-05-19)

### New Features

- **Configurable Weak Model Tokens**: Added support for configuring maximum token output for weak models
  - Added `weak_model_max_tokens` configuration option (default: 8192)
  - Added `--weak-model-max-tokens` command line flag
  - Added corresponding environment variable `KODELET_WEAK_MODEL_MAX_TOKENS`
- **Enhanced Model Selection**: Improved model selection logic to use appropriate token limits based on model type

### Improvements

- **Configuration Wizard**: Updated initialization wizard to configure weak model token limits
- **Documentation Updates**: Enhanced configuration examples in KODELET.md and DEVELOPMENT.md

## 0.0.17.alpha (2025-05-19)

### New Features

- **Thinking Tokens Support**: Added support for handling Anthropic thinking events
  - Integrated with Anthropic API to capture model thinking process
  - Added thinking tokens configuration to Kodelet LLM configuration
- **Improved Conversation Management**: Completely redesigned conversation commands
  - Added dedicated `kodelet conversation` namespace for managing saved chats
  - Implemented advanced filtering and sorting options
  - Added multiple output formats (text, JSON, raw) for viewing conversations
  - Simplified resuming conversations in both chat and one-shot modes
- **Enhanced One-shot Experience**: Improved `run` command capabilities
  - Added support for piped input from other commands
  - Implemented conversation persistence for one-shot queries
  - Added ability to resume conversations with `--resume` flag

## 0.0.16.alpha (2025-05-18)

### New Features

- **File Multi-Edit Tool**: Added new `file_multi_edit` tool to support editing multiple occurrences of text in a file
  - Allows efficient modification of repeated patterns in large files
  - Provides clear reporting on number of replacements made
  - Includes validation to prevent unintended mass replacements

### Improvements

- **Enhanced Grep Tool**:
  - Upgraded pattern matching with doublestar library for more powerful glob support
  - Improved file path handling to use absolute paths by default
  - Better documentation with detailed examples for pattern parameter
- Fixed trailing newlines in multiple system prompt files
- Code formatting and style improvements

## 0.0.15.alpha (2025-05-17)

### New Features

- **Web Fetch Tool**: Added new `web_fetch` tool for retrieving and processing content from websites
  - Securely fetch content from HTTPS URLs with same-domain redirect protection
  - Convert HTML to Markdown for better readability in CLI context
  - Extract specific information using AI processing
  - Perfect for retrieving documentation, API specifications, and other web content

### Dependencies

- Added `github.com/JohannesKaufmann/html-to-markdown` for HTML to Markdown conversion

## 0.0.14.alpha (2025-05-16)

### New Features

- **Interactive Setup Wizard**: Added a new `kodelet init` command that provides an interactive setup experience for first-time users
  - Guides users through configuring their Anthropic API key
  - Automatically detects shell type (bash, zsh, fish) and offers to add the API key to the appropriate profile
  - Configures model preferences with sensible defaults
  - Creates the required configuration files and directories

### Improvements

- **Enhanced Installation Script**: Updated the `install.sh` script to:
  - Automatically detect shell type and add Kodelet to PATH
  - Launch the new init wizard after installation when no API key is detected
  - Provide better guidance for different shell environments

### Bug Fixes

- Fixed debug output in subagent prompt generation (removed unintended print statement)

### Dependencies

- Added `golang.org/x/term` package for secure password input
- Updated `golang.org/x/sys` from v0.32.0 to v0.33.0

## 0.0.12.alpha (2025-05-16)

### System Prompt Refactoring

- **Complete Template Overhaul**: Refactored system prompt generation with a modular, template-driven design
  - Implemented new renderer with embedded filesystem for template storage
  - Created component-based template system with reusable sections
  - Added support for conditional template rendering based on feature configuration
- **Improved Configuration**: Added PromptConfig system for fine-grained control of enabled features
- **Enhanced Testing**: Added comprehensive test suite for template rendering and system prompt generation
- **Code Organization**: Moved constant definitions to dedicated constants.go file

## 0.0.11.alpha (2025-05-16)

### Self-Update Command

- **New Command**: Added `kodelet update` for easy version management
  - Download and install the latest Kodelet version with a single command
  - Support for installing specific versions with `--version` flag
  - Auto-detection of platform (OS and architecture)
  - Automatic handling of permission requirements
- **Improved User Experience**: No need to manually download and install new versions
- **Version Management**: Updated README with instructions for updating

## 0.0.10.alpha (2025-05-16)

### Enhanced Subagent and Tool System

- **Improved Subagent Tool**: Completely redesigned subagent system prompt with better task delegation and consistent formatting
- **System Prompt Updates**: Modernized system prompts with consistent backtick formatting for tool references
- **New Glob Tool**: Added `glob_tool` for efficient file pattern matching with support for complex patterns
- **Enhanced Grep Tool**:
  - Added filtering to skip hidden files/directories
  - Implemented result sorting by modification time (newest first)
  - Added result truncation (100 files max) with clear notifications

### Bug Fixes

- Fixed file tracking in watch mode by properly setting file last accessed time

### Dependencies

- Added `github.com/bmatcuk/doublestar/v4 v4.8.1` for glob pattern matching support

## 0.0.9.alpha (2025-05-15)

### Package Structure Refactoring

- **Type Reorganization**: Moved types to more appropriate packages
  - Relocated LLM types from `pkg/llm/types` to `pkg/types/llm`
  - Moved tool interfaces from `pkg/tools` to `pkg/types/tools`
  - Integrated state management from `pkg/state` into `pkg/tools`
- **Improved Dependency Management**: Reduced circular dependencies and enhanced code modularity

### Batch Tool Implementation

- Added new `batch` tool for executing multiple independent tool calls in parallel
- Enhanced performance by reducing latency and context switching with parallel tool execution
- Implemented validation to prevent nested batch operations

### Other Improvements

- Enhanced error handling with `github.com/pkg/errors` for better error context and tracing
- Implemented more robust tool discovery and validation mechanisms
- Improved state management to support tool-specific configurations
- Code formatting and documentation updates

## 0.0.8.alpha1 (2025-05-14)

- Minior TUI message input fix

## 0.0.8.alpha (2025-05-14)

### SubAgent Tool Implementation

- Added new subagent tool functionality for delegating complex tasks
- Enhanced capabilities for semantic search and handling nuanced queries

### OpenTelemetry Tracing Implementation

- Added comprehensive OpenTelemetry tracing support for enhanced observability to support the subagent tool
- New `/pkg/telemetry` package with tracing initialization and helper functions
- Instrumented CLI commands, LLM interactions, and tool executions with tracing
- Added configuration options for enabling/disabling tracing and sampling strategies
- Created documentation in `docs/observability.md` explaining usage and configuration

### Thread Management Improvements

- Refactored thread architecture for better management of LLM interactions
- Improved token usage tracking and management
- Enhanced error handling and persistence functionality

### Chat UI Improvements

- Support multiline input with `Ctrl+S` to send the message

## 0.0.7.alpha (2025-05-13)

### Conversation Persistence

The main feature in this release is the addition of conversation persistence, allowing users to save, load, and manage chat conversations across sessions.

- **Conversation Management**: Save and load conversation history with persistent storage
- **Chat List Command**: Browse, filter, and sort saved conversations
- **Improved TUI**: Enhanced terminal UI with support for loading existing conversations
- **Weak Model Support**: Additional configuration options for message handling with less capable models

### Architectural Improvements

- Refactored LLM interfaces with better separation of concerns
- Enhanced token usage calculation and reporting
- Renamed legacy chat UI to "plain UI" with updated command structure

### Documentation

- Added detailed development guide at `docs/DEVELOPMENT.md`
- Created ADR for conversation persistence design decisions

## 0.0.6.alpha1 (2025-05-12)

- Added context window size tracking and cost calculation
- Separated the usage and cost stats into two lines in the TUI
- Bug fix: make sure that the watch command does not process binary files
- Nicer spinner for the TUI
## 0.0.6.alpha (2025-05-11)

- Added token usage and cost tracking for the LLM usage

## 0.0.5.alpha (2025-05-10)

- Added new LLM architecture with Thread abstraction that unifies all interactions with Claude API

## 0.0.4.alpha (2025-05-09)

- Added new `watch` command to monitor file changes and provide AI assistance, support for special `@kodelet` comments to trigger automatic code analysis and generation.
- Improved chat TUI with better text wrapping and no character limit
- Added `--short` flag to commit command for generating concise commit messages
- Fix the [cache control issue](https://github.com/anthropics/anthropic-sdk-go/issues/180) via explicitly setting `{"type": "ephemeral"}` for the system prompt.

## 0.0.3.alpha1 (2025-05-09)

- Reduce the log level of README.md and KODELET.md to `debug` to avoid cluttering the console output.

## 0.0.3.alpha (2025-05-09)

- Minor tweaks on the chat TUI (e.g. a rad ascii art and processing spinner)
- Added a new command `/help` to show the help message
- Added a new command `/clear` to clear the screen
- Added a new command `/bash` to execute the chat context

### Bug fixes

- Stream out the output from the llm whenever the it responds, instead of buffering it.
- Use `YYYY-MM-DD` in the system prompt instead of the time, so that we can have more efficient cache control for the purpose of cost optimisation.

## 0.0.2.alpha1

Initial release of the kodelet
