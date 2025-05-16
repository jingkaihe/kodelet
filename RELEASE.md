# Kodelet

## 0.0.10.alpha (2025-05-16)

### Self-Update Command

- **New Command**: Added `kodelet update` for easy version management
  - Download and install the latest Kodelet version with a single command
  - Support for installing specific versions with `--version` flag
  - Auto-detection of platform (OS and architecture)
  - Automatic handling of permission requirements
- **Improved User Experience**: No need to manually download and install new versions
- **Version Management**: Updated README with instructions for updating

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