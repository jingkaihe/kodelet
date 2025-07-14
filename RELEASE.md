# Kodelet

## 0.0.65.alpha (2025-07-14)

### GitHub Copilot Integration

- **Copilot Authentication**: Added `kodelet copilot-login` and `kodelet copilot-logout` commands for GitHub Copilot integration
  - OAuth-based authentication flow for accessing GitHub Copilot services
  - Seamless integration with OpenAI client for Copilot-powered requests
  - Streamlined login process with improved user experience
- **Provider Terminology**: Refactored terminology from "ModelType" to "Provider" throughout the codebase for clarity
  - Updated conversation management, database schema, and web UI to use consistent "Provider" naming
  - Enhanced provider filtering and breakdown in conversation and usage commands
- **Database Optimizations**: Improved SQL query performance with explicit column selection and schema cleanup

## 0.0.64.alpha (2025-07-12)

### OpenAI-Compatible Provider Support

- **Provider Presets**: Added configuration-based preset system for popular OpenAI-compatible providers
  - Built-in `xai` preset with complete xAI Grok model configuration including pricing and reasoning categorization
  - Configurable via `openai.preset` in configuration files for seamless provider switching
- **Custom Provider Configuration**: Enhanced OpenAI client to support custom base URLs and model configurations
  - `OPENAI_API_BASE` environment variable support for alternative API endpoints
  - Auto-population of non-reasoning models from pricing configuration to reduce duplication
- **Backward Compatibility**: Maintains full compatibility with existing OpenAI configurations while enabling third-party providers

## 0.0.63.alpha (2025-07-12)

### Model Aliases Support

- **Configuration-based Model Aliases**: Added support for custom model aliases in configuration files
  to allow memorable names for frequently used models (e.g., `claude-sonnet-4-20250514` -> `sonnet-4`, `claude-4-opus-20250514` -> `opus-4`).
- **Streaming Improvements**: Better error handling and conversation saving for streaming message responses

## 0.0.62.alpha (2025-07-11)

### Conversation management improvements

- **Improved default sorting**: Changed default conversation sort order to `updated_at` for better user experience
- **Code organization**: Simplified SQLite conversation store naming and removed unused management methods
- **Documentation**: Updated project structure documentation with current file counts

## 0.0.61.alpha (2025-07-11)

### Introduce `sqlite` as the default conversation store

We use the `modernc.org/sqlite` for the sqlite implementation, which is a pure Go SQLite implementation that ensures compatibility across different platforms without requiring any C dependencies. This replaces the previous `bbolt` based store as bolt does not native support multiple-process access.

## 0.0.60.alpha (2025-07-10)

### refactor: replace `t.Error` with `t.Fatal` in tests with testify `assert` and `require`.

### PR response improvements

Now kodelet can not only make commit based on the PR comment, but also doing code review and answer questions based on user's comment.

## 0.0.59.alpha (2025-07-09)

### refactor: replace fmt.Errorf with pkg/errors for consistent error wrapping


## 0.0.58.alpha (2025-07-08)

### Support boltdb as the conversation store

### Support conversation import and export feature for sharing and backup

Added `kodelet conversation import|export|edit` commands for importing, exporting and editing conversations. Here are some examples of how to use it:

```bash
kodelet conversation export <conversation-id> $PATH # export conversation to a local file
kodelet conversation export --gist <conversation-id> # export conversation to a private gist
kodelet conversation export --public-gist <conversation-id> # export conversation to a public gist
kodelet conversation import $PATH # import conversation from a local file
kodelet conversation import https://example.com/conversation.json # import conversation from a URL
kodelet conversation edit <conversation-id> # edit conversation in a text editor
```

## 0.0.57.alpha (2025-07-08)

### Subagent Tool Simplification

- **Removed Model Strength Parameter**: Simplified subagent tool interface by removing the `model_strength` parameter. Going forward subagent will use the default model for tasks.

### Automatic Compacting

Add context auto-compact support to allow kodelet to run for a long period of time without hitting the context window limit.

## 0.0.56.alpha (2025-07-06)

### Test Improvements

Generally improve ROI of the tests by either removing the low value tests or improving the test coverage.

### Conversation WebUI and API Improvements

* Drastically improve conversation loading performance in web UI by implementing in-memory caching and file watching.
* Fixed the pagination issue in web UI.

### Others

* Support auto-reload of the Web UI via `make dev-server` in the dev mode using [air](https://github.com/air-verse/air).

## 0.0.55.alpha (2025-07-06)

### Conversation Web UI

Provide Web UI for conversations

## 0.0.54.alpha (2025-07-04)

### Store Structured Tool Result in Conversation

- **Structured Tool Results**: Complete architectural overhaul replacing string-based tool results with structured metadata storage
  - **Rich Metadata**: Tool results now capture structured data (file paths, line numbers, execution context, etc.) instead of plain strings
  - **Type-Safe Storage**: All tool outputs stored with type-safe metadata structures for better data integrity
  - **Improved Conversation Persistence**: Enhanced conversation storage with structured tool result metadata
  - **CLI Renderer System**: New renderer architecture generates CLI output from structured data at display time
  - **Web UI Foundation**: Structured data provides foundation for upcoming web UI conversation viewer

## 0.0.53.alpha (2025-07-03)

### Conversation Enhancement
- **Tool Results Display**: Tool execution results now properly display in conversation history and persist across sessions for both Anthropic and OpenAI

### SDLC Improvements
- **Linting Integration**: Added golangci-lint with comprehensive rules, Makefile targets, and CI integration
- **Code Cleanup**: Removed unused code and enhanced test coverage

### Usage Analytics Enhancement
- **Time Range Filtering**: Fixed `kodelet usage` command to properly filter conversations by `--since` and `--until` flags

## 0.0.52.alpha (2025-07-02)

### Security & Web Tools Enhancement
- **Domain Filtering for Web Tools**: Added configurable domain filtering system for web_fetch and browser tools
  - **Security Control**: New `allowed_domains_file` configuration option to restrict web tool access to specific domains
  - **Flexible Patterns**: Support for exact domain matches and glob patterns (e.g., `*.github.com`, `api.*.com`)
  - **Auto-Refresh**: Domain list refreshes every 30 seconds for dynamic control
  - **Localhost Bypass**: Localhost and internal addresses are always allowed regardless of domain filter
  - **Graceful Defaults**: When no domain file is configured, all domains are allowed for backward compatibility

### Anthropic Thinking Enhancements
**Interleaved Thinking**: Added support for interleaved thinking for Anthropic models that support it

Extended thinking with tool use in Claude 4 models supports [interleaved thinking](https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking#interleaved-thinking), which enables Claude to think between tool calls and make more sophisticated reasoning after receiving tool results.

## 0.0.51.alpha (2025-07-01)

### Web Fetch Tool Enhancement
- **Localhost HTTP Support**: Enhanced web_fetch tool to allow HTTP URLs for localhost and internal addresses (127.0.0.1, ::1, localhost, etc.) while maintaining HTTPS requirement for external domains

## 0.0.50.alpha (2025-06-30)

### Configuration Enhancements
- **Optimized Thread Management**: Subagent thread creation now reuses parent client and usage tracking for better resource efficiency
- **Configurable Anthropic Access**: Added support for configurable Anthropic API access mode to improve API interaction flexibility

## 0.0.49.alpha (2025-06-30)

### Usage Analytics Command
- **New `kodelet usage` Command**: Added comprehensive token usage and cost tracking functionality

## 0.0.48.alpha (2025-06-30)

### Enhanced CLI Output System
- **New Presenter Package**: Introduced dedicated presenter package for consistent CLI output with color support and context-aware formatting
- **Improved User Experience**: Better structured output with success/error/warning indicators and statistics reporting
- **Context-Aware Colors**: Automatic terminal detection with color override support

## 0.0.47.alpha (2025-06-21)

### Token Refresh Improvements
- Improved Anthropic token refresh logic to refresh 10 minutes before expiration instead of after expiration

### Web Fetch Tool Enhancements
- Allow web fetch tool to fetch files directly without prompt summarise. This is particularly useful for fetching source code where you just want to have the raw content.

## 0.0.46.alpha (2025-06-20)

### Anthropic Usage Enhancements
- System prompt includes official Anthropic branding when using subscription models

## 0.0.45.alpha (2025-06-20)

### Authentication & Model Access

**Anthropic OAuth Login**: Added `kodelet anthropic-login` command for accessing subscription-based models
- OAuth-based authentication flow with automatic browser opening
- Supports subscription models not available via standard API key
- Credentials saved to `~/.kodelet/anthropic-subscription.json`
- Cross-platform browser support (macOS, Linux, Windows)

## 0.0.44.alpha (2025-06-18)

### Security & Configuration

**Configurable Bash Commands**: Added `allowed_commands` configuration to restrict bash tool execution

## 0.0.43.alpha (2025-06-18)

### Browser Automation Tools

- **New Browser Tools**: Added `navigate`, `get_page`, `click`, `type`, `screenshot`, and `wait_for` tools

### Chat Experience

- **TUI Log Redirection**: Chat logs redirected to separate files for cleaner TUI interface

## 0.0.42.alpha (2025-06-15)

### Background Process Management

- **Background Process Execution**: Enhanced bash tool with background process support for long-running tasks
  - **Background Flag**: New `background=true` parameter runs commands in background with process tracking
  - **Process Monitoring**: Background processes write output to `.kodelet/{PID}/out.log` files
  - **Non-blocking Execution**: Bash tool returns immediately with PID and log file location for background tasks
- **View Background Processes Tool**: Added comprehensive background process management capabilities
  - **Process Tracking**: View all background processes with PID, status, start time, and command details
  - **Status Monitoring**: Track running/stopped status of background processes across sessions
  - **Log File Access**: Easy access to log file paths for debugging and monitoring background tasks

### Developer Experience Improvements

- **Enhanced File Reading**: Changed file_read tool line numbering from 0-indexed to 1-indexed for better readability and consistency with editors
- **Comprehensive Documentation**: Added detailed MCP language server tools documentation with code intelligence capabilities and best practices

## 0.0.41.alpha (2025-06-12)

### Message Cleanup

- **Orphaned Message Cleanup**: Added automatic cleanup for orphaned messages in both Anthropic and OpenAI threads
  - **Thread Integrity**: Ensures conversation threads maintain proper message structure and relationships
  - **Memory Optimization**: Removes orphaned messages that could accumulate during failed operations
  - **Cross-Provider Support**: Consistent cleanup behavior across both Anthropic and OpenAI implementations

## 0.0.40.alpha2 (2025-06-12)

### GitHub Actions Template System

- **New Template Rendering**: Added GitHub Actions workflow template system with Go templating support
  - **Template File Support**: New `pkg/github/templates.go` with embedded workflow templates

### PR Command Enhancements

- **MCP Tool Support**: Added support for MCP tools in the PR command

## 0.0.40.alpha1 (2025-06-11)

### MCP Configuration Enhancement

- **Environment Variable Interpolation**: Added support for environment variable interpolation in MCP server configuration
  - **Dollar Sign Syntax**: Use `$VAR_NAME` in MCP env configuration to reference environment variables
  - **Dynamic Configuration**: Enables secure handling of API keys and secrets in MCP server environments
  - **Backward Compatibility**: Existing configurations continue to work unchanged

## 0.0.40.alpha (2025-06-11)

### Issue Resolution Enhancements

- **Intelligent Type Detection**: Enhanced `kodelet issue-resolve` with automatic type detection and workflow selection
  - **Smart Analysis**: Automatically categorizes issues as bug fixes, feature requests, or documentation updates
  - **Workflow Optimization**: Tailors resolution approach based on detected issue type
  - **Improved Testing**: Expanded test coverage for issue type detection logic

## 0.0.39.alpha2 (2025-06-11)

**PR Respond Command**: Improved `kodelet pr-respond` to fetch PR basic info using `gh pr view --json title,author,body,comments`

So that we don't run into the issue of check status permission error.

## 0.0.39.alpha1 (2025-06-11)

Make sure the `kodelet gha-agent-onboard` update the dev env setup step before `git add` and `git commit`.

## 0.0.39.alpha (2025-06-11)

### GitHub Actions Background Agent

- **New `kodelet gha-agent-onboard` Command**: Added automated onboarding for GitHub Actions-based background agent
  - **One-Command Setup**: Automates GitHub app installation, secret configuration, and workflow creation
  - **Secure Integration**: Handles `ANTHROPIC_API_KEY` secret setup with validation
  - **Auto PR Creation**: Creates git branch, workflow file, and pull request automatically
  - **Branch Management**: Stores and restores original branch after onboarding
  - **URL Validation**: Comprehensive validation for GitHub app URLs

## 0.0.38.alpha (2025-06-09)

### End-to-End Testing Infrastructure

- **Comprehensive Acceptance Testing**: Added complete end-to-end testing suite to ensure reliability across different environments
  - **Docker-Based E2E Tests**: New `make e2e-test-docker` command runs tests in isolated container environment
  - **Core Functionality Tests**: Tests covering basic commands, conversation management, and file operations
  - **Conversation Tests**: Validation of chat persistence, resume functionality, and conversation lifecycle
  - **Version Compatibility**: Automated testing of version commands and update mechanisms
  - **GitHub Actions Integration**: Added `/e2e-test` comment trigger for PR testing with proper permissions

### Minor Improvements

**Updated Co-authorship Attribution**: Changed commit co-author email from `kodelet@tryopsmate.ai` to `noreply@kodelet.com`

## 0.0.37.alpha (2025-06-08)

### Issue Resolution Configuration Enhancement

- **Configurable Bot Mention**: Added `--bot-mention` flag to `resolve` and `issue-resolve` commands to customize bot mentions (defaults to `@kodelet`)

### PR Response Enhancement

- **Git Diff Context**: Enhanced `kodelet pr-respond` to include git diff in PR response data
  - **Better Code Context**: PR responses prompt now have access to actual code changes via `gh pr diff`, so that kodelet can make more informed responses by understanding what code was changed

## 0.0.36.alpha (2025-06-04)

### Conversation Continuity Enhancements

- **Follow Flag Implementation**: Added `--follow` / `-f` flag for seamless conversation continuation
  - **Run Command**: `kodelet run --follow "continue working"` resumes most recent conversation automatically
  - **Chat Command**: `kodelet chat --follow` enters interactive mode with most recent conversation loaded
  - **Smart Conflict Detection**: Prevents using `--follow` and `--resume` flags together with clear error messages
  - **Graceful Fallbacks**: When no conversations exist, starts new conversation with informative warning

## 0.0.35.alpha (2025-06-03)

### Enhanced PR Response System

- **Focused Comment Data Fetching**: Improved `kodelet pr-respond` with targeted comment analysis
  - **Smart Data Fetching**: When `--review-id` or `--issue-comment-id` is specified, fetches specific comment details and related discussions for focused responses
  - **Automatic @kodelet Detection**: When no comment-id provided, automatically finds latest @kodelet mention with contextual discussions
  - **Reduced Noise**: Removed redundant all-comments fetching, keeping only relevant focused sections
  - **Clean Repository Management**: Fixed accidental binary inclusion in commit history with proper cleanup

## 0.0.34.alpha (2025-06-02)

### Command Restructure

- **Renamed resolve command to issue-resolve**: Enhanced CLI clarity while maintaining full backward compatibility
  - Created dedicated `issue_resolve.go` file with complete implementation
  - Original `resolve` command acts as deprecated wrapper with migration notice
  - No breaking changes - existing scripts continue to work

### Configuration Enhancements

- **Layered Configuration System**: Implemented intelligent configuration merging with fallback behavior
  - **Global base**: Loads `~/.kodelet/config.yaml` as the foundation
  - **Repository override**: Merges `kodelet-config.yaml` on top, overriding only specified settings
  - **Minimal repo configs**: Only need to specify settings that differ from global defaults
  - **Automatic inheritance**: API keys, logging, and other global preferences are preserved
  - **Clear naming**: `kodelet-config.yaml` for repo-level, `config.yaml` for global only

```bash
# New recommended command
kodelet issue-resolve --issue-url https://github.com/owner/repo/issues/123

# Legacy command (still works, shows deprecation notice)
kodelet resolve --issue-url https://github.com/owner/repo/issues/123
```

## 0.0.33.alpha (2025-06-02)

### PR Comment Response System

- **New `kodelet pr-respond` Command**: Added intelligent PR comment response capability
  - **Focused Comment Handling**: Responds to specific PR comments with targeted code changes
  - **@kodelet Mention Detection**: Automatically finds latest @kodelet mentions when no comment ID specified
  - **Smart Comment Analysis**: Analyzes comment requests and implements precise changes without scope creep
  - **GitHub CLI Integration**: Uses `gh pr view` and comment APIs for seamless GitHub workflow integration
  - **Automatic Code Updates**: Makes targeted changes and commits them with `--no-confirm` flag
  - **Comment Reply System**: Responds to the original comment with summary of actions taken

### Enhanced GitHub Actions Integration

- **Comprehensive PR Review Support**: Updated `kodelet-background.yml` workflow for complete PR interaction
  - **Multi-Event Support**: Handles `pull_request_review_comment`, `pull_request_review`, and `issue_comment` events
  - **Context-Aware Processing**: Automatically detects whether comment is on PR or issue and routes appropriately
  - **Comment ID Tracking**: Passes specific comment IDs to `pr-respond` command for precise targeting
  - **Enhanced Error Handling**: Improved error reporting with detailed workflow logs and user-friendly messages
  - **Smart Event Routing**: Distinguishes between PR comments and issue comments for appropriate tool selection

### Logging Infrastructure Improvements

- **Configurable Log Format**: Added support for both JSON and text log formats
  - **New Configuration Options**: Added `log_format` config setting and corresponding environment variable
  - **Text Format Default**: Changed default from JSON to human-readable text format with full timestamps
  - **Backward Compatibility**: JSON format still available via configuration for structured logging needs
  - **Enhanced Readability**: Improved development experience with formatted text output

### Watch Mode Reliability

- **Improved Signal Handling**: Enhanced graceful shutdown in watch mode
  - **Context Management**: Better context propagation and cancellation handling
  - **Error Logging**: Fixed error logging in watch mode using `context.TODO()` when context is cancelled
  - **Signal Processing**: Improved handling of SIGINT and SIGTERM for clean shutdown

### Technical Improvements

- **Enhanced Prerequisites Validation**: All PR-related commands now validate git repository, GitHub CLI installation, and authentication
- **Robust Error Handling**: Comprehensive error checking with clear user guidance for missing dependencies
- **Configuration Management**: Added new configuration options with proper defaults and environment variable support
- **Code Quality**: Improved code organization and consistency across PR-related commands

### Usage Examples

```bash
# Respond to specific PR review comment
kodelet pr-respond --pr-url https://github.com/owner/repo/pull/123 --review-id 456789

# Respond to specific PR issue comment
kodelet pr-respond --pr-url https://github.com/owner/repo/pull/123 --issue-comment-id 789012

# Respond to latest @kodelet mention in PR
kodelet pr-respond --pr-url https://github.com/owner/repo/pull/123

# Configure text log format (default)
export KODELET_LOG_FORMAT="text"

# Configure JSON log format for structured logging
export KODELET_LOG_FORMAT="json"
```

## 0.0.32.alpha (2025-06-02)

### GitHub Issue Resolution

- **New `kodelet resolve` Command**: Added autonomous GitHub issue resolution capability
  - **Issue Analysis**: Automatically fetches and analyzes GitHub issues using `gh issue view`
  - **Smart Branch Creation**: Creates branches with naming pattern `kodelet/issue-{number}-{descriptive-name}`
  - **Autonomous Resolution**: Works through issue requirements step-by-step with todo tracking
  - **Automatic PR Creation**: Integrates with existing `kodelet pr` command to create pull requests
  - **Issue Commenting**: Automatically updates original issue with PR link and completion status
  - **Prerequisites Validation**: Ensures git repository, GitHub CLI installation, and authentication

### Enhanced Commit Command

- **Automatic Commit Generation**: Added `--no-confirm` flag for autonomous commit workflows
  - **Streamlined Automation**: Skip confirmation prompts when called from automated scripts
  - **Integration Ready**: Designed for use with `kodelet resolve` and CI/CD workflows
  - **Backward Compatibility**: Maintains existing confirmation behavior by default

### Documentation Improvements

- **Simplified KODELET.md**: Consolidated and streamlined key documentation sections
  - **Engineering Principles**: Added core development principles with linting, testing, and documentation requirements
  - **Streamlined Configuration**: Simplified configuration examples and removed redundant sections
  - **Focused Command Reference**: Concentrated on most commonly used commands and patterns
  - **Updated Architecture**: Refined LLM architecture documentation and logger usage examples

### Architecture Decision Record

- **ADR 013 Update**: Comprehensive revision of CLI background support approach
  - **Prompt-Based Orchestration**: Selected simpler prompt-based approach following `kodelet pr` pattern
  - **Implementation Strategy**: Detailed comparison of orchestration approaches with selected solution
  - **GitHub Actions Integration**: Defined workflow integration patterns for automated issue resolution

### Technical Improvements

- **MCP Tool Support**: Enhanced `kodelet resolve` with Model Context Protocol tool integration
- **Graceful Cancellation**: Added proper signal handling and context cancellation for long-running operations
- **Error Handling**: Comprehensive prerequisite validation with clear error messages and installation guidance
- **Test Coverage**: Added unit tests for issue resolution prompt generation and validation logic

### Usage Examples

```bash
# Resolve a GitHub issue autonomously
kodelet resolve --issue-url https://github.com/owner/repo/issues/123

# Create commits without confirmation (for automation)
kodelet commit --short --no-confirm

# Integration with existing PR workflow
kodelet pr  # Works seamlessly after kodelet resolve
```

### Integration Capabilities

- **GitHub Actions Ready**: Designed for automated issue resolution in CI/CD pipelines
- **Existing Tool Reuse**: Leverages all existing tools (grep, file operations, bash, etc.) through LLM orchestration
- **Conversation Persistence**: Maintains conversation history for debugging and analysis
- **Cost Tracking**: Provides detailed token usage and cost statistics for monitoring

## 0.0.31.alpha (2025-05-30)

### Conversation Context Management

- **Max-Turns Configuration**: Added configurable conversation turn limits to prevent excessive context growth
  - **CLI Flags**: New `--max-turns` flag for `chat` and `run` commands (default: 50 turns)
  - **Context Control**: Helps manage token usage and prevents runaway conversation loops
  - **Flexible Limits**: Set to 0 for unlimited turns, or negative values are treated as no limit

### LLM Caching Enhancements

- **Anthropic Message Caching**: Implemented configurable message caching for Anthropic threads
  - **Cache Configuration**: New `--cache-every` flag and `cache_every` config option (default: 10 interactions)
  - **Performance Optimization**: Reduces API costs by caching frequently accessed message history
  - **Anthropic-Specific**: Optimized for Anthropic's caching capabilities to improve response times

### Todo Management Improvements

- **Enhanced File Path Management**: Improved todo file organization and error handling
  - **Dedicated Directory**: Todo files now stored in `.kodelet/` directory for better organization
  - **Robust Error Handling**: Better error reporting when todo file paths cannot be determined
  - **Session-Based Storage**: Todo files remain session-specific with improved path resolution

### Technical Improvements

- **Debug Logging**: Added comprehensive debug logging for LLM turn limit checks and caching behavior
  - **Turn Tracking**: Better visibility into conversation turn counting for both Anthropic and OpenAI interactions
  - **Cache Debugging**: Detailed logging for message caching operations and decisions
- **Configuration Management**: Enhanced configuration handling for new caching and turn limit features
  - **Backward Compatibility**: All new features have sensible defaults and don't break existing configurations
  - **Provider-Specific**: Turn limits and caching options are intelligently applied based on LLM provider capabilities

### Bug Fixes

- **Todo Tool Reliability**: Fixed potential crashes when todo file paths cannot be determined
- **Configuration Loading**: Improved handling of missing or invalid configuration values for new features

## 0.0.30.alpha (2025-05-29)

### User Experience Improvements

- **Enhanced Tool Output Visibility**: Improved user-facing output for better transparency and debugging
  - **Bash Tool**: Command output and errors are now both shown to users, with errors appended after command output for better context
  - **Batch Tool**: All tool results are now displayed to users, including those that encounter errors, providing complete visibility into batch operations
  - **SubAgent Tool**: Simplified output handling to ensure consistent display of subagent results to users

## 0.0.29.alpha (2025-05-29)

### Major Architectural Improvements

- **Tool Result Interface Redesign**: Complete overhaul of tool execution and result handling
  - **Dual-Facing Results**: Implemented `ToolResult` interface with separate `UserFacing()` and `AssistantFacing()` methods for optimal output formatting
  - **Structured Tool Results**: Added dedicated result types for all tools (`GrepToolResult`, `FileMultiEditToolResult`, `GlobToolResult`, `SubAgentToolResult`, etc.)
  - **Enhanced Error Handling**: Improved error reporting and debugging capabilities across all tool operations
  - **Better User Experience**: User-facing results are optimized for readability while assistant-facing results provide structured data for LLM processing

### Context-Aware Logging Infrastructure

- **New Logger Package**: Implemented comprehensive context-aware structured logging using Logrus
  - **Context Propagation**: Automatic logger context propagation through `logger.G(ctx)` for consistent logging across the application
  - **Structured Fields**: Enhanced logging with contextual fields using `log.WithFields()` for better observability
  - **Configurable Log Levels**: Added support for configurable log levels across all application components

### Enhanced Tool Capabilities

- **File Multi-Edit Tool**: Enhanced with diff generation and detailed result reporting
  - Advanced result handling with before/after comparisons
  - Clear reporting of the number of replacements made
  - Improved validation to prevent unintended mass replacements

- **Grep Tool Improvements**: Enhanced search result handling and formatting
  - Structured result presentation with file paths, line numbers, and matched content
  - Better handling of large result sets with truncation notifications
  - Improved error reporting for invalid patterns or file access issues

- **Batch Tool Refinements**: Improved parallel tool execution with better result aggregation
  - Enhanced error handling for failed batch operations
  - Clearer result presentation for multiple tool executions
  - Better validation to prevent nested batch operations

### Technical Improvements

**Configuration Updates**: Enhanced logging configuration options
- Added log level configuration to sample config files
- Improved CLI flag handling for logging options
- Better integration with existing configuration management

### Developer Experience

- **Enhanced Documentation**: Updated KODELET.md with comprehensive logging usage examples
- **Improved Testing**: All tool result interfaces now have comprehensive test coverage
- **Better Error Messages**: More descriptive error messages throughout the application for easier debugging

## 0.0.28.alpha (2025-05-27)

### Major Refactoring

- **Command Configuration Redesign**: Comprehensive refactoring of CLI command flag handling and configuration management
  - **Type-Safe Configuration**: Introduced dedicated configuration structs for all commands (`CommitConfig`, `ConversationListConfig`, `ConversationDeleteConfig`, `ConversationShowConfig`, `PRConfig`, `RunConfig`, `UpdateConfig`, `WatchConfig`)
  - **Centralized Defaults**: Each command now has a `NewXConfig()` function that provides sensible default values
  - **Improved Flag Handling**: Replaced global variables with proper flag extraction functions that read values safely using Cobra's flag methods
  - **Enhanced Validation**: Added configuration validation with descriptive error messages for invalid inputs

### MCP Configuration Improvements

- **Robust Configuration Loading**: Improved MCP (Model Context Protocol) server configuration handling
  - **YAML-Based Loading**: Migrated from Viper's complex nested map handling to direct YAML parsing for better type safety
  - **Structured Configuration**: Enhanced `MCPConfig` and `MCPServerConfig` types with proper YAML tags
  - **Better Error Handling**: More descriptive error messages when MCP configuration fails to load
  - **Configuration File Safety**: Added proper file existence checks and graceful handling of missing config files

### Technical Improvements

- **Code Quality**: Eliminated global variables in CLI commands in favor of structured configuration patterns
- **Maintainability**: Each command now follows a consistent pattern: `NewXConfig()` → `getXConfigFromFlags()` → validation → execution
- **Type Safety**: Enhanced type safety across all command configurations with proper struct definitions
- **Testing Support**: Improved testability by removing global state dependencies

### Breaking Changes

- **Internal API Changes**: Command flag handling has been completely restructured (affects only internal APIs, not user-facing CLI)
- **Configuration Structure**: MCP configuration loading mechanism has changed (existing config files remain compatible)

### Dependencies

- **Added**: `gopkg.in/yaml.v2` for improved YAML configuration parsing
- **Updated**: Various dependency updates for better stability

### Bug Fixes

- **MCP Configuration**: Fixed issues with complex nested MCP server configurations not loading properly
- **Flag Validation**: Improved error handling for invalid command-line flag combinations
- **Configuration Loading**: Better handling of missing or malformed configuration files

## 0.0.26.alpha (2025-05-24)

### Major Features

- **Image Input Support**: Added comprehensive multimodal capabilities to Kodelet
  - **CLI Integration**: New `--image` flag supports multiple images per message via local files or HTTPS URLs
  - **Vision-Enabled Models**: Full support for Anthropic Claude models with vision capabilities
  - **Multiple Input Types**: Supports JPEG, PNG, GIF, and WebP formats with automatic validation
  - **Security First**: Only HTTPS URLs accepted for remote images, with 5MB file size limits
  - **Interactive Mode**: Added `/add-image` and `/remove-image` commands in chat mode
  - **Dual Provider Support**: Anthropic (full vision support) and OpenAI (graceful text-only fallback)

### New Tools

- **Image Recognition Tool**: Added dedicated `image_recognition` tool for vision-enabled AI analysis
  - Process images from local files or remote HTTPS URLs
  - Extract specific information from screenshots, diagrams, and mockups
  - Integrated with existing LLM workflow for seamless multimodal interactions
  - Support for architecture analysis, UI/UX feedback, and code review from screenshots

### Technical Improvements

- **Thread Interface Extension**: Updated `AddUserMessage` to support optional image inputs
  - Maintains backward compatibility with existing text-only workflows
  - Enhanced message options with `Images` field for multimodal content
- **Provider-Specific Implementation**:
  - **Anthropic**: Full vision support with base64 encoding and URL references
  - **OpenAI**: Graceful fallback with warning messages for unsupported vision features
- **Comprehensive Testing**: Added extensive test coverage for image processing and validation
- **Error Handling**: Robust validation for file formats, sizes, and accessibility

### Architecture Decision Record

- **ADR 011**: Documented complete design decisions for image input support
  - Security considerations and validation strategies
  - Multi-provider architecture approach
  - Implementation phases and future expansion plans

### Usage Examples

```bash
# Single image analysis
kodelet run --image /path/to/screenshot.png "What's wrong with this UI?"

# Multiple images comparison
kodelet run --image diagram.png --image https://example.com/mockup.jpg "Compare these designs"

# Architecture review
kodelet run --image ./architecture.png "Review this system architecture"
```

### Documentation Updates

- **Enhanced README**: Added vision capabilities to key features section
- **Updated KODELET.md**: Comprehensive documentation for image input usage
- **Security Guidelines**: Clear documentation of HTTPS-only policy and file size limits

## 0.0.25.alpha (2025-05-23)

### Major Updates

- **Claude Sonnet 4.0 Integration**: Upgraded default model from Claude 3.7 Sonnet to the new Claude Sonnet 4.0
  - Updated all configuration files, documentation, and code references
  - Changed default model constant from `ModelClaude3_7SonnetLatest` to `ModelClaudeSonnet4_0`
  - Enhanced performance and capabilities with the latest Claude model

- **Anthropic SDK Upgrade**: Major update to Anthropic SDK from v0.2.0-beta.3 to v1.2.0
  - **Breaking Changes**: Updated API interface to use stable SDK release
  - **Streaming Support**: Implemented streaming message responses for better user experience
  - **Improved Type Safety**: Updated all content block handling to use new API structure
  - **Enhanced Error Handling**: Better error reporting with streaming API
  - **Pricing Integration**: Added support for new Claude 4 Opus and Sonnet 4.0 pricing tiers

### Technical Improvements

- **Message Processing**: Refactored message handling to work with new SDK structure
  - Updated `OfRequestTextBlock` → `OfText`
  - Updated `OfRequestToolUseBlock` → `OfToolUse`
  - Updated `OfRequestToolResultBlock` → `OfToolResult`
  - Updated `OfRequestThinkingBlock` → `OfThinking`

- **Pricing Updates**: Added comprehensive pricing support for new Claude models
  - Claude Sonnet 4.0: $3/$15 per million tokens (input/output)
  - Claude 4 Opus: $15/$75 per million tokens (input/output)
  - Maintained backward compatibility with legacy model pricing

- **Configuration Updates**: Updated all default configurations across the codebase
  - Environment variable examples now use `claude-sonnet-4-0`
  - Sample configuration files updated with new model names
  - Command-line help text reflects new default models

### Documentation

- **Updated Examples**: All documentation examples now use Claude Sonnet 4.0 as the default
- **Migration Guide**: Configuration files and environment variables automatically use new model names
- **Pricing Documentation**: Updated cost calculations to reflect new model pricing

### Backward Compatibility

- Existing configurations will continue to work
- Legacy model names are still supported
- Automatic model detection and pricing fallback for unsupported models

## 0.0.24.alpha (2025-05-22)

### New Features
- **OpenAI LLM Integration**: Added provider support, model classification, pricing API integration, and pricing updates.
- **Dynamic Message Extraction**: Upgraded thread retrieval to extract structured messages and choose providers dynamically.

### Refactoring
- **Anthropic Deserialization**: Simplified ExtractMessages with a new DeserializeMessages function.
- **Message Modeling**: Modularized and centralized message model handling across TUI and core packages.

## 0.0.23.alpha (2025-05-21)

### New Features
- **Pull Request Command**: Added new `kodelet pr` command to generate AI-powered pull requests
  - Automatically analyzes git diffs to create meaningful PR titles and descriptions
  - Integrates with GitHub CLI for seamless PR creation
  - Supports custom PR templates via `--template-file` flag
  - Provides detailed analysis of changes for better PR quality

## 0.0.22.alpha (2025-05-20)

### Features
- **Conversation Management**: Improved conversation persistence and concurrency safety
- **Thread Context**: Added context cancellation and signal handling for graceful shutdown

### Refactoring
- Extracted tracing and message exchange logic into separate methods in Anthropic client

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
