# Kodelet

## 0.2.23.beta (2026-02-19)

- OpenAI Responses now persists conversation state on each exchange (including error paths), with regression tests to prevent persistence regressions.
- Patched frontend high-severity transitive vulnerabilities by updating the lockfile to use `sucrase@3.35.1` (removing the vulnerable `glob`/`minimatch` chain).
- Todo tools are now disabled by default for the main agent, and can be explicitly re-enabled with `--enable-todos` (or `enable_todos: true`).

## 0.2.22.beta (2026-02-17)

### Features

Added `Claude Sonnet 4.6`
 
## 0.2.21.beta (2026-02-14)

### Features

- **HTTPS-only image validation**: All LLM providers now reject plain HTTP image URLs for security

### Bug Fixes

- **Persistence cleanup on load**: Fixed orphaned trailing tool calls left in conversation history after loading persisted OpenAI/OpenAI Responses conversations
- **Google tool result lookup**: Structured tool results now resolve by call ID first (falling back to tool name), fixing incorrect rendering for repeated tool calls

### Internal Changes

- Centralized shared LLM logic (hook triggers, tool execution, auto-compact, image handling, utility prompts, context swap, background process restore) into `pkg/llm/base/`, removing duplicated code across all three providers
- Removed separate OpenAI system prompt template (`openai_system.tmpl`) — all providers now use the unified `system.tmpl`

## 0.2.20.beta (2026-02-13)

### Breaking Changes

**Codex preset system prompt behavior changed**: The Codex preset no longer uses a separate embedded Codex-specific system prompt. It now uses the standard OpenAI system prompt pipeline.

### Features

- **New Codex models**: Added `gpt-5.3-codex` and `gpt-5.3-codex-spark`
- **Updated default Codex model**: Default model changed from `gpt-5.1-codex-max` to `gpt-5.3-codex`
- **Codex status output refresh**: `kodelet codex status` now shows the latest Codex model list and ordering

### Bug Fixes

- **Web UI OpenAI Responses rendering**: Conversation view now correctly renders OpenAI Responses tool-call messages (including reasoning/tool-call flow)

### Internal Changes

- Removed legacy embedded Codex prompt assets and related prompt-loading/caching logic
- Simplified sysprompt renderer/provider branching around Codex preset handling
- Updated tests for Codex model/pricing coverage and OpenAI Responses web message conversion

## 0.2.19.beta (2026-02-12)

### Features

**`--disable-subagent` flag**: New flag to completely disable the subagent tool and remove all subagent-related guidance from the system prompt. Available on `run`, `acp`, and as a global persistent flag. Also configurable via `disable_subagent: true` in config.

```bash
kodelet run --disable-subagent "query"
```

### Internal Changes

- Disabled `todo_read`/`todo_write` tools for subagents to reduce unnecessary tool overhead
- Refactored ACP session `Manager` to use a `ManagerConfig` struct instead of positional parameters

## 0.2.18.beta (2026-02-07)

**Configurable max-turns limit**: Added `--max-turns` flag to control the maximum number of agentic interaction turns within a single message call. Set to `0` (now the default, previously `50`) for unlimited turns, or specify a positive integer to cap iterations. Available on both `run` and `acp` commands.

## 0.2.17.beta (2026-02-05)

### Features

- **Anthropic SDK v1.21.0**: Upgraded from v1.19.0, bringing support for new model identifiers
- **Claude Opus 4.6 support**: Added `claude-opus-4-6` as a new model with pricing, thinking model registration, and updated the `opus-46` alias (replaces `opus-45`)
- **Removed Claude 3.5 Haiku pricing**: The `claude-3-5-haiku` pricing entry and fallback lookup have been removed. Use `claude-haiku-4-5-20251001` instead.

## 0.2.16.beta (2026-02-04)

### Bug Fixes

- Fixed missing git SHA in version output by making build variables (VERSION, GIT_COMMIT, BUILD_TIME) overridable via environment variables

## 0.2.15.beta (2026-02-04)

### Features

**Subagent working directory support**: The subagent tool now accepts a `cwd` parameter to specify a working directory for the spawned subagent:

**Workflow profile support**: Workflow fragments can now specify a `profile` in their frontmatter, which overrides any `--profile` in `subagent_args`:

```yaml
---
name: Cheap Workflow
workflow: true
profile: cheap
---
```

## 0.2.14.beta (2026-02-04)

### Features

**Database management commands**: New `kodelet db` command for managing database migrations:

```bash
kodelet db status              # Show migration status with applied/pending indicators
kodelet db rollback            # Rollback the last migration (with confirmation)
kodelet db rollback --no-confirm  # Rollback without confirmation
```

**Streamlit ACP chatbot example**: Added a full-featured Streamlit chatbot at `examples/streamlit-acp/` demonstrating ACP integration with conversation history, thinking visualization, tool call inspection, and image support. Run directly with:

```bash
uv run https://raw.githubusercontent.com/jingkaihe/kodelet/refs/heads/main/examples/streamlit-acp/main.py
```

### Bug Fixes

- Fixed assistant message finalization checks to include tools in completion detection
- Increased ACP buffer limit from default to 50MB to handle large conversation histories

### Internal Changes

- **Unified database migrations**: Migrated to Rails-style timestamp versioning (YYYYMMDDHHmmss) with centralized migration runner in `pkg/db/`
- **ACP session storage**: Migrated from JSONL files to SQLite for improved reliability and thread safety
- **Migrations run at startup**: Database migrations now execute once at CLI startup instead of per-component initialization
- Added process cleanup helper for background process acceptance tests

## 0.2.12.beta (2026-02-03)

### Internal Changes

- Simplified Docker cross-build to use mise instead of nvm for consistent toolchain management
- Reduced Dockerfile complexity by ~60% (from 59 to 25 lines)
- Added `-s -w` ldflags to strip debug symbols, producing smaller release binaries
- Removed hardcoded NODE_VERSION/NPM_VERSION build args (now managed by mise.toml)

## 0.2.11.beta (2026-02-03)

### Features

**Plugin hooks support**: Hooks can now be distributed via the plugin system alongside skills and recipes:

```bash
kodelet plugin add user/repo   # Now installs skills, recipes, AND hooks
kodelet plugin list            # Shows hook counts per plugin
```

Plugin hooks are discovered from `<plugin>/hooks/` directories and prefixed with `org/repo/` (e.g., `jingkaihe/hooks/audit-logger`).

**Recipe-aware hooks**: All hook payloads now include `recipe_name` field, enabling hooks to filter or behave differently based on the active recipe:

```bash
# Hook payload now includes:
{
  "event": "turn_end",
  "recipe_name": "code-review",  // Present when invoked via -r flag
  ...
}
```

Hooks can check this field to act only for specific recipes (see `.kodelet/hooks/intro-logger` for an example).

### Internal Changes

- Hook discovery now scans four locations in precedence order: repo-local standalone → repo-local plugins → global standalone → global plugins
- Added `GetHookByName()` and `AllHooks()` methods to HookManager
- Condensed AGENTS.md documentation for improved readability
- Added ADR 029 documenting plugin hooks design

## 0.2.10.beta (2026-02-03)

### Breaking Changes

**`kodelet feedback` renamed to `kodelet steer`**: The feedback command has been renamed to better reflect its purpose of steering autonomous conversations:

```bash
# Old (no longer supported)
kodelet feedback --follow "focus on error handling"
kodelet feedback --conversation-id ID "message"

# New
kodelet steer --follow "focus on error handling"
kodelet steer --conversation-id ID "message"
```

`kodelet ralph` command removed: The ralph autonomous development loop has been removed. Use the `jingkaihe/skills` plugin instead.
Removed built-in `code/architect`, `code/explorer`, and `code/reviewer` recipes (now available as skills via `jingkaihe/skills` plugin).

## 0.2.9.beta (2026-02-03)

### Breaking Changes

**`kodelet skill` commands replaced by `kodelet plugin`**: The `kodelet skill add/list/remove` commands have been removed and replaced with a unified plugin system. Migrate your workflows:

```bash
# Old (no longer supported)
kodelet skill add user/repo
kodelet skill list
kodelet skill remove my-skill

# New
kodelet plugin add user/repo
kodelet plugin list
kodelet plugin remove user/repo
```

**Recipe directory moved**: Repo-local recipes have moved from `./recipes/` to `./.kodelet/recipes/`. Move your existing recipes to the new location.

### Features

**Unified plugin system**: A new `kodelet plugin` command manages both skills and recipes from GitHub repositories:

```bash
kodelet plugin add user/repo              # Install skills and recipes from repo
kodelet plugin add user/repo@v1.0.0       # Install specific version
kodelet plugin add user/repo -g           # Install globally
kodelet plugin list                       # List all installed plugins
kodelet plugin show user/repo             # Show plugin details
kodelet plugin remove user/repo           # Remove a plugin
```

Plugins use `org@repo` directory naming to avoid collisions. Skills and recipes from plugins are prefixed with `org/repo/` (e.g., `jingkaihe/skills/pdf`).

**JSON output for plugin commands**: Use `--json` flag for machine-readable output with skill/recipe descriptions:

```bash
kodelet plugin list --json
kodelet plugin show user/repo --json
```

### Internal Changes

- New `pkg/plugins` package for unified plugin discovery, installation, and removal
- Skills and recipes now support plugin-based discovery with proper precedence
- Removed `docs/mcp.md` and `docs/tools.md` documentation files

## 0.2.8.beta (2026-02-03)

### Features

**New code recipes**: Added three new built-in recipes for code analysis and review workflows:

- **`code/architect`** - Analyzes codebase patterns and designs architectural solutions with ADR-style implementation blueprints
- **`code/explorer`** - Explores and explains how a codebase works, creating guided learning journeys with file references
- **`code/reviewer`** - Performs comprehensive code reviews with configurable scope (staged, working directory, or branch comparison)

Usage examples:
```bash
kodelet run -r code/reviewer --arg scope=staged        # Review staged changes
kodelet run -r code/explorer --arg focus="auth flow"   # Understand authentication
kodelet run -r code/architect --arg focus="add caching" # Design a caching layer
```

## 0.2.7.beta (2026-02-02)

### Features

**ACP session persistence and replay**: ACP sessions are now persisted to JSONL files (`~/.kodelet/acp/sessions/`) and automatically replayed when a session is loaded. This enables IDE integrations to restore conversation history across restarts. Consecutive text chunks are merged during storage for efficient replay.

**Conversation cleanup integration**: Deleting a conversation via `kodelet conversation delete` now also removes the associated ACP session file if it exists.

### Internal Changes

- Improved error handling in ACP session storage

## 0.2.6.beta (2026-02-02)

### Breaking Changes

**Fragment metadata `defaults` replaced by `arguments`**: The `defaults` field in fragment/recipe YAML frontmatter has been replaced with a more expressive `arguments` structure. Existing recipes using `defaults` must be migrated:

```yaml
# Old format (no longer supported)
defaults:
  target: "main"
  draft: "false"

# New format
arguments:
  target:
    description: Target branch to merge into
    default: "main"
  draft:
    description: Whether to create as a draft pull request
    default: "false"
```

### Features

**Subagent workflow support**: The subagent tool can now execute workflows (recipes/fragments) directly, enabling the model to delegate specialized tasks like PR creation or issue resolution:

```json
{"workflow": "github/pr", "args": {"target": "develop", "draft": "true"}}
```

The `question` parameter is now optional when a workflow is specified.

**Structured fragment arguments**: Fragment/recipe metadata now supports argument descriptions and defaults in a structured format:

```yaml
arguments:
  target:
    description: Target branch to merge into
    default: "main"
```

The `kodelet recipe show` command now displays argument descriptions alongside defaults.

**`--no-workflows` flag**: Added flag to disable subagent workflows for security or debugging:

```bash
kodelet run --no-workflows "query"    # Disable workflows for run command
kodelet acp --no-workflows            # Disable workflows for ACP mode
```

## 0.2.5.beta (2026-02-01)

### Breaking Changes

**Subagent configuration simplified**: The nested `subagent:` configuration block has been replaced with a simpler `subagent_args` string. This is a breaking change - users with existing `subagent:` configs must migrate:

```yaml
# Old (no longer supported)
subagent:
  provider: openai
  model: gpt-4.1
  reasoning_effort: high

# New - create a profile and reference it
profiles:
  openai-subagent:
    provider: openai
    model: gpt-4.1
    reasoning_effort: high

subagent_args: "--profile openai-subagent"
```

Common patterns:
- `subagent_args: "--use-weak-model"` - Use weak model (same provider)
- `subagent_args: "--profile cheap"` - Use a different profile

### Features

**`--no-tools` flag**: New flag to disable all tools for simple query-response usage:

```bash
kodelet run --no-tools "What is the capital of France?"
```

### Internal Changes

- Subagent, image recognition, and web fetch tools now use shell-out pattern via `kodelet run --as-subagent` (ADR 027)
- Removed `NewSubAgent()` method from all LLM providers
- Removed separate subagent prompt template (`pkg/sysprompt/templates/subagent.tmpl`) - subagent now uses main system prompt with `isSubagent` feature flag
- Removed Docker build workflow and Dockerfile
- CI workflows now use PVC-based tool caching instead of mise cache

## 0.2.4.beta (2026-01-28)

* Remove default and constraint annotations from JSON schema for tool params
* Updated copyright year: Updated copyright year to 2026 in LICENSE and fixed formatting in README.md.

## 0.2.3.beta (2026-01-25)

### Features

**Grep tool output size controls**: The grep tool now prevents context overflow with three new safeguards:

- **Output size limit (50KB)**: Results are automatically truncated when total output exceeds 50KB
- **Line length limit (300 chars)**: Individual lines longer than 300 characters are truncated with `... [truncated]` indicator
- **Configurable `max_results` parameter**: Limit the number of files returned (1-100, default 100) to reduce output size when searching large codebases

## 0.2.2.beta (2026-01-25)

### Features

**Skill add `--force` flag**: Added `--force` (`-f`) flag to `kodelet skill add` command to overwrite existing skills during installation:

```bash
kodelet skill add orgname/skills --force  # Overwrite existing skills
```

Previously, attempting to install a skill that already exists would skip it with a warning. Now you can use `--force` to remove and reinstall skills, useful for updating skills to newer versions.

## 0.2.1.beta (2026-01-24)

### Features

**ACP auto-compact configuration**: Added CLI flags to control automatic context compaction in ACP sessions:

```bash
kodelet acp --compact-ratio 0.8          # Trigger compaction at 80% context usage (default)
kodelet acp --disable-auto-compact       # Disable auto-compaction entirely
```

**Streamlit chatbot example**: Added a new example at `examples/streamlit/` demonstrating how to build a chat interface using kodelet's CLI with real-time streaming. Features include conversation persistence, thinking visualization, and tool call inspection.

### Bug Fixes

**Web UI dark mode fix**: Fixed code blocks appearing black on Linux when system appearance is set to dark mode. The web UI now enforces light color scheme regardless of system preferences.

## 0.2.0.beta (2026-01-23)

### Features

**Partial message streaming in headless mode**: Added `--stream-deltas` flag that enables real-time token streaming in headless mode. Text and thinking content are now output as they're generated, enabling ChatGPT-style streaming experiences in third-party UIs:

```bash
kodelet run --headless --stream-deltas "explain recursion"
```

Delta events (`text-delta`, `thinking-delta`, `thinking-start`, `thinking-end`, `content-end`) are interleaved with complete message events, allowing clients to show progressive output while still receiving full content for persistence.

**Enhanced `conversation show` command**: The command now displays conversation metadata and usage statistics by default:

```bash
kodelet conversation show <id>              # Shows header + messages (new default)
kodelet conversation show <id> --no-header  # Messages only (previous behavior)
kodelet conversation show <id> --stats-only # Header/stats only, no messages
```

Output formats:
- `--format text` (default): Human-readable output with header and messages
- `--format json`: Structured JSON with id, provider, summary, timestamps, usage, and messages
- `--format raw`: Full `ConversationRecord` dump as JSON (includes rawMessages, toolResults, metadata)

**History-only streaming for conversations**: Added `--history-only` flag to `conversation stream` that outputs historical conversation data and exits immediately without live streaming:

```bash
kodelet conversation stream <id> --history-only  # Output history and exit
```

This is mutually exclusive with `--include-history` (which shows history then continues streaming).

## 0.1.50.beta (2026-01-23)

### Features

**Web UI visual refresh**: Redesigned conversation explorer with a refined visual language featuring custom color palette, editorial typography (Poppins headings, Lora body text), and subtle micro-interactions. Cards now feature gradient accent bars and refined shadows.

**Streamlined tool renderers**: Tool results now display with a compact, information-dense layout using new StatusBadge components. Long content (diffs, file contents, terminal output) is collapsed by default with "Show" buttons for on-demand expansion, reducing visual noise while preserving access to full details.

**Improved message styling**: User and assistant messages have distinct visual treatments with colored left borders and subtle gradient backgrounds for better conversation flow visualization.

## 0.1.49.beta (2026-01-23)

### Breaking Changes

**Removed `kodelet chat` command**: The interactive chat mode has been removed in favor of ACP (Agent Client Protocol) based interaction. Users should now use `toad acp 'kodelet acp'` for interactive sessions. The TUI implementation and all Charm library dependencies (Bubble Tea, Lipgloss, Bubbles) have been removed.

**Removed `/llms.txt` web endpoint**: The llms.txt content is no longer served as a web endpoint. This functionality has been migrated to the built-in kodelet skill.

### Features

**Built-in kodelet skill**: Migrated llms.txt documentation to a built-in skill at `skills/kodelet/SKILL.md`. The skill provides comprehensive CLI usage guide including commands, configuration, and workflows. The model will automatically discover and use this skill when users ask about kodelet features.

**Symlink support for skills**: Skills directories now properly support symbolic links. The skill discovery system follows symlinks to skill directories while gracefully handling broken symlinks and symlinks to files.

**Updated model configurations**: Added support for newer GPT models including `gpt-5.2-codex` and `gpt-5.1-codex-mini` in default configuration profiles. Updated OpenAI profile to use `use_responses_api: true` and `reasoning_effort: high`.

## 0.1.48.beta (2026-01-23)

### Internal Changes

**Simplified LLM usage logging**: Streamlined turn completion logs to include only essential fields (model, input/output tokens, total cost, max context window) and reduced decimal precision from 4 to 3 places for cleaner output.

## 0.1.47.beta (2026-01-22)

### Features

**ACP code block output**: Grep and glob tool results in ACP now render inside code blocks for better formatting preservation.
**ACP title generation improvements**: Tool titles now display full file paths instead of basenames and no longer truncate long commands.

### Internal Changes

**Go 1.25.6**: Upgraded Go from 1.25.1 to 1.25.6.
**npm 11.8.0**: Upgraded npm from 11.6.2 to 11.8.0.
**lodash 4.17.23**: Upgraded lodash from 4.17.21 to 4.17.23.

## 0.1.46.beta (2026-01-22)

**Removed `cache_every` configuration**: The `--cache-every` CLI flag and `cache_every` config option have been removed. Prompt caching for Anthropic now happens automatically on every exchange when enabled, eliminating the need for manual tuning.
**Simplified prompt caching**: Anthropic prompt caching now consistently caches the last content block before each API call, improving cache hit rates and reducing configuration complexity.

## 0.1.45.beta (2026-01-20)

### Breaking Changes

**Removed KODELET.md fallback**: Kodelet no longer automatically loads `KODELET.md` files. If you have existing `KODELET.md` files, rename them to `AGENTS.md`:

```bash
mv KODELET.md AGENTS.md
```

### Features

**Configurable context file patterns**: You can now configure which files are loaded as context for the agent. Configure via:
- CLI flag: `--context-patterns "AGENTS.md,README.md"`
- Config file: `context.patterns: ["AGENTS.md", "README.md"]`
- Environment variable: `KODELET_CONTEXT_PATTERNS="AGENTS.md,README.md"`

Files are searched in order; the first match wins per directory. Default remains `["AGENTS.md"]`.

## 0.1.44.beta (2026-01-16)

### Features

**Manual context compaction**: Added the `compact` recipe (and `/compact` slash command) to summarize and swap conversation history for long sessions.

**Recipe hook handlers**: Recipes can now declare `turn_end` hooks with built-in handlers like `swap_context` to mutate thread state after responses.

### Bug Fixes

**OpenAI reasoning stream sequencing**: Thinking blocks now close before text output in streaming chat completions.

**Result-only output cleanup**: Result-only mode no longer echoes the original user query.

**Responses compaction persistence**: Stored responses now keep compaction items so context resumes correctly.

## 0.1.43.beta (2026-01-15)

### Bug fixes

**Subagent usage aggregation**: Subagent current context window is not longer captured in parent thread usage statistics.

### Internal Changes

**Simplified subagent thread creation**: Refactored Anthropic and OpenAI providers to use the shared `base.NewThread` constructor for subagent creation, reducing code duplication.

## 0.1.42.beta (2026-01-14)

### Bug Fixes

**Thinking block termination**: Streaming output now ends thinking blocks separately from content across Anthropic/OpenAI/Responses streams to keep reasoning separators aligned.

### Internal Changes

**OpenAI reasoning summaries**: Responses threads now default to `auto` reasoning summaries.

## 0.1.41.beta (2026-01-11)

### Features

**Codex OAuth CLI**: New `kodelet codex login/logout/status` commands handle ChatGPT-backed Codex OAuth, start a local callback server, and store credentials in `~/.kodelet/codex-credentials.json` for use with `openai.preset: codex`.

**Codex preset for Responses API**: Added Codex preset that auto-uses the OpenAI Responses API, embeds the Codex system prompt, and sends required ChatGPT headers for GPT-5.x Codex models.

### Bug Fixes

**Reasoning stream completion**: Thinking blocks now end when reasoning summaries finish so streaming output stays aligned.

**Reasoning effort selection**: Weak models now downgrade to medium reasoning effort to avoid over-aggressive settings.


## 0.1.40.beta (2026-01-10)

### Breaking Changes

**Removed `thinking` tool**: The thinking tool, renderer, and metadata were removed from the default toolset. Workflows using `thinking` must migrate to other tools or rely on model-native reasoning.

### Features

**OpenAI Responses API (opt-in)**: New `openai.use_responses_api` config flag and `KODELET_OPENAI_USE_RESPONSES_API` env var enable the official Responses API with richer streaming events, `previous_response_id` multi-turn state, prompt caching controls, and conversation persistence. Added `openai-responses` profile and conversation streaming parser.

**New OpenAI model presets**: Added `gpt-5.2`, `gpt-5.2-pro`, and the `gpt-5.1-codex` family with pricing updates. Image inputs now support data URLs (including ACP) and local files for OpenAI requests.

**Improved OpenAI streaming and reasoning controls**: Dynamic model detection, config-driven reasoning effort, and a factory that routes between Chat Completions and Responses APIs for better streaming coverage.

### Bug Fixes

**Conversation stability**: Fixed OpenAI compaction flow and LLM summaries, improved handling of pending items and invalid `previous_response_id`, and enforced detailed reasoning summaries for dynamic models.

### Internal Changes

- Documented Responses API architecture in ADR 024.
- Updated PR recipe wording for initial tool calls.
- Added OpenAI thread factory, streamer parser, and extensive unit/integration coverage for Responses API paths.

## 0.1.37.beta (2026-01-09)

### Internal Changes

**Refactored Anthropic Tool Name Handling**: Simplified how tool names are transformed for Anthropic subscription accounts.

- Changed from `oc_` prefix to capitalization-based naming (e.g., `file_read` → `File_read`)
- Moved `ToAnthropicTools` from shared package to Anthropic-specific implementation
- Renamed `stripToolNamePrefix` to `normalizeToolName` for clarity
- Added `capitalizeToolName` and `decapitalizeToolName` helper functions
- Improved test coverage with mock tool implementation

## 0.1.36.beta (2026-01-08)

### Internal Changes

**Unified LLM Thread Base Package**: Refactored all LLM provider implementations to share common functionality through a new `pkg/llm/base` package.

- Created shared `Thread` struct with common fields (Config, State, Usage, ConversationID, ToolResults, etc.)
- All providers (Anthropic, OpenAI, Google) now embed `*base.Thread` using Go's struct composition pattern
- Shared methods: `GetState`, `SetState`, `GetConfig`, `GetUsage`, `EnablePersistence`, `ShouldAutoCompact`, `CreateMessageSpan`, `FinalizeMessageSpan`
- Shared constants: `MaxImageFileSize` (5MB), `MaxImageCount` (10)
- Reduces ~300 lines of duplicated code per provider
- Added comprehensive unit tests for the base package
- See ADR 023 for architectural decision details

## 0.1.35.beta (2026-01-08)

### Features

**Anthropic Rate Limit Usage Display**: New `kodelet anthropic accounts usage` command to monitor API rate limits

- View current rate limit utilization for 5-hour and 7-day windows
- Shows status (allowed/limited), utilization percentage, and reset time
- Supports JSON output with `--json` flag for scripting and automation
- Works with any configured account alias (defaults to current account)

```bash
kodelet anthropic accounts usage           # Show usage for default account
kodelet anthropic accounts usage work      # Show usage for specific account
kodelet anthropic accounts usage --json    # Output in JSON format
```

## 0.1.34.beta (2026-01-08)

### Features

**Multi-Account Anthropic Authentication**: Manage multiple Anthropic subscription accounts for different contexts (work, personal, etc.)

- **Account Aliases**: Login with `--alias` flag to name accounts (e.g., `kodelet anthropic login --alias work`)
- **Account Management**: New `kodelet anthropic accounts` commands for listing, switching, and removing accounts
  - `accounts list` - Display all accounts with status (valid, needs refresh, expired)
  - `accounts default [alias]` - Show or set the default account
  - `accounts remove <alias>` - Remove an account
  - `accounts rename <old> <new>` - Rename an account alias
- **Runtime Selection**: Use `--account` flag with `run` and `chat` commands to select which account to use
- **Automatic Migration**: Existing single-account credentials are automatically migrated to the new multi-account format

### Breaking Changes

**Anthropic Command Restructure**: Authentication commands moved under unified `anthropic` parent command

- `kodelet anthropic-login` → `kodelet anthropic login`
- `kodelet anthropic-logout` → `kodelet anthropic logout`

### Internal Changes

- Comprehensive unit test coverage for multi-account credential storage
- Refactored credential storage from single-account to multi-account JSON format

## 0.1.33.beta (2026-01-06)

### Features

**Ralph - Autonomous Development Loop**: New `kodelet ralph` command for autonomous feature implementation

- Iteratively implements features from a PRD (Product Requirements Document)
- Tracks progress between iterations in a dedicated progress file
- Makes git commits after each completed feature
- Runs type checking and tests to verify each feature
- Configurable iteration count, completion signal, and file paths
- Graceful cancellation support (Ctrl+C stops after current iteration)

**PRD Generation**: New `kodelet ralph init` command to bootstrap a PRD

- Analyzes repository structure and generates a PRD JSON file
- Supports extra instructions to guide PRD generation (e.g., `kodelet ralph init "check ./design.md"`)
- Prioritizes discussion and design docs over repository analysis
- Also available as standalone recipe: `kodelet run -r ralph-init`

See [docs/RALPH.md](docs/RALPH.md) for complete documentation.
## 0.1.32.beta (2026-01-05)

### Features

**grep_tool now supports searching individual files**: The `path` parameter can now be a file or a directory

- When a file path is provided, searches that specific file for the pattern
- When a directory path is provided, searches all files recursively (existing behavior)
- The `include` glob pattern only applies when searching directories

**Improved Tool Input Validation**: `grep_tool` and `glob_tool` now validate the `path` parameter

- `grep_tool`: validates path exists (file or directory)
- `glob_tool`: validates path is a directory (since globbing within a file doesn't make sense)
- Clear error messages for invalid paths caught early in validation phase

## 0.1.31.beta (2026-01-04)

### Features

**External Binary Management**: Kodelet now manages external binary dependencies (ripgrep, fd) automatically

- Binaries are downloaded from GitHub releases to `~/.kodelet/bin/`
- SHA256 checksum verification for security
- Automatic fallback to system-installed binaries if download fails (air-gapped/corporate networks)
- Runtime version detection replaces version files

**Enhanced grep_tool (ripgrep-powered)**:

- Migrated from pure Go regex to ripgrep for significantly improved search performance
- Added `ignore_case` option for case-insensitive search
- Added `fixed_strings` option to treat patterns as literal strings (useful for special characters)
- Added `surround_lines` parameter to show context lines around matches
- Match positions now exposed in structured results
- Respects `.gitignore` patterns by default

**Enhanced glob_tool (fd-powered)**:

- Replaced with fd-based implementation for faster file finding
- Added `ignore_gitignore` option (respects `.gitignore` by default)
- Hidden files/directories excluded by default for cleaner results

**Improved file_edit**:

- `replaceAll` operations no longer require prior file read, enabling declarative bulk replacements

**Graceful Process Termination**:

- Foreground bash commands now receive SIGTERM before SIGKILL on timeout
- 2-second grace period allows processes to flush buffers and cleanup
- Entire process group is terminated, including child processes

### Internal Changes

- Consolidated process group handling in `pkg/osutil` with platform-specific implementations
- Added comprehensive tests for process lifecycle management
- Updated frontend GrepRenderer to support context lines styling

## 0.1.29.beta (2026-01-01)

### Features

**Agent Client Protocol (ACP) Integration**: Added `kodelet acp` command for seamless IDE integration

- **IDE Embedding**: Run kodelet as a subprocess in ACP-compatible clients like Zed and JetBrains IDEs
- **JSON-RPC 2.0**: Communication over stdio with full session management, prompt handling, and streaming updates
- **Slash Commands**: Recipe/fragment invocation via slash commands (e.g., `/init`, `/custom-tool`) with argument support
- **Session Persistence**: ACP sessions integrate with existing conversation persistence for resumable workflows

**Data URL Image Support**: Added support for base64-encoded images across all LLM providers (Anthropic, OpenAI, Google)

**MCP Improvements**:
- `--no-mcp` flag to disable MCP tools for `run` and `chat` commands
- `mcp.enabled` config option to disable MCP globally
- Per-session MCP socket isolation for concurrent kodelet instances
- Configurable socket path override via `mcp.code_execution.socket_path`

### Breaking Changes

**Removed `kodelet watch` command**: The file watching feature has been removed. Use `kodelet acp` for IDE integration instead.
**Removed `--ide` flag**: The IDE integration mode flag has been replaced by the ACP protocol. Use `kodelet acp` for IDE integration.

**MCP Socket Path Change**: Socket path is now per-session (`mcp-{session-id}.sock`) to allow concurrent instances. Existing hardcoded socket paths in automation may need updates.

### Documentation

- Added comprehensive ACP documentation in `docs/ACP.md`
- Added ADR 022 documenting the ACP integration architecture

## 0.1.28.beta (2025-12-27)

### Features

**Skill Management Commands**: Added CLI commands for managing skills from GitHub repositories

- `kodelet skill add <repo>` - Install skills from GitHub (supports `@tag/branch` syntax and `--dir` for specific skills)
- `kodelet skill list` - List all installed skills with descriptions
- `kodelet skill remove <name>` - Remove installed skills
- Use `-g/--global` flag to manage global vs local skills

**Improved Code Execution Error Reporting**: Error output now includes stderr content for better debugging

### Breaking Changes

**Directory Structure Reorganization**: Several directories moved to consolidate state in home directory

- Custom tools local directory: `./kodelet-tools` → `./.kodelet/tools`
- Background process logs: `.kodelet/{PID}/out.log` → `~/.kodelet/bgpids/{PID}/out.log`
- Todo files: `.kodelet/kodelet-todos-*.json` → `~/.kodelet/todos/*.json`
- Web archives: `.kodelet/web-archives/` → `~/.kodelet/web-archives/`
- MCP socket: `.kodelet/mcp.sock` → `.kodelet/mcp/mcp.sock`

## 0.1.26.beta (2025-12-23)

### Features

**Weak Model Support**: Added `--use-weak-model` flag to `run` and `chat` commands

- Enables using a weaker/cheaper model for processing when full model capability isn't needed
- Available in both interactive chat and one-shot run modes

### Internal Changes

- Refactored chat function parameters into `ChatOpts` struct for cleaner API
- Consolidated TUI initialization with unified options pattern

## 0.1.25.beta (2025-12-23)

### Features

**Result-Only Output Mode**: Added `--result-only` flag to suppress intermediate output and usage statistics

- Only prints the final agent message when enabled
- Sets presenter to quiet mode and logger to error level
- Useful for scripting and programmatic use

### Documentation

- Updated CLI manual and LLM-friendly documentation with `--result-only` flag examples

## 0.1.24.beta (2025-12-18)

### Features

**Hook Disable Support**: Added ability to temporarily disable hooks by renaming with `.disable` suffix

- Hooks with filenames ending in `.disable` are skipped during discovery
- Allows quick enable/disable without deleting hook files

### Documentation

- Added comprehensive TypeScript interfaces for all hook payload structures
- Enhanced hook documentation with detailed examples and type definitions

### Internal Changes

- Hooks are now disabled during conversation summarization to prevent side effects
- Refactored context compacting to use `StringCollectorHandler` instead of extracting text from message history
- Simplified `ShortSummary` implementation using separate summary threads across all LLM providers (Anthropic, OpenAI, Google)

## 0.1.23.beta (2025-12-17)

### Features

**Agent Lifecycle Hooks**: Introduced extensibility mechanism for observing and controlling agent behavior

- **Hook Types**: Four hook types covering the agent lifecycle - `before_tool_call`, `after_tool_call`, `user_message_send`, and `agent_stop`
- **Security Controls**: Blocking hooks can prevent dangerous tool executions with deny-fast semantics
- **Input/Output Modification**: Hooks can modify tool inputs before execution and outputs after completion
- **Follow-up Messages**: `agent_stop` hooks can return follow-up messages to continue the conversation
- **Language-Agnostic**: Hooks are any executable files that implement a simple JSON-based protocol
- **Discovery Locations**: Hooks discovered from `.kodelet/hooks/` (repo-local) and `~/.kodelet/hooks/` (global)
- **CLI Flag**: `--no-hooks` flag to disable hooks for a session

### Internal Changes

- Hooks automatically disabled for `kodelet commit` and `kodelet pr` commands
- Added comprehensive hook documentation in `docs/HOOKS.md`
- Added ADR 021 documenting the hooks architecture

## 0.1.22.beta (2025-12-14)

### Features

**Agentic Skills System**: Introduced model-invoked capabilities that package domain expertise into discoverable units

- **Automatic Invocation**: Skills are automatically invoked by Kodelet when relevant to your task - no explicit user action required
- **Skill Discovery**: Skills are discovered from `.kodelet/skills/<name>/SKILL.md` in both repository-local and user-global directories
- **Configuration Options**: Control skills via `skills.enabled` and `skills.allowed` in config, or disable with `--no-skills` CLI flag
- **Supporting Files**: Skills can include reference docs, examples, scripts, and templates that Kodelet can read when needed
- **Web UI Support**: New SkillRenderer component displays loaded skills in the web interface

## 0.1.21.beta (2025-12-11)

### Features

**Parallel Tool Execution**: Tool calls are now executed concurrently for Anthropic Claude models

- **Faster Execution**: Multiple independent tool calls run in parallel, significantly reducing wait times
- **Real-time Results**: Tool results stream as they complete rather than waiting for all tools to finish
- **Improved Readability**: Tool use inputs are now pretty-printed with JSON formatting

### Bug Fixes

- **Concurrency Safety**: Added mutex protection for handler access during parallel tool execution
- **Output Visibility**: Fixed tool output display in parallel execution mode
- **Context Cancellation**: Proper cancellation handling prevents orphaned tool executions

## 0.1.20.beta (2025-12-11)

### Features

**OpenAI Streaming Support**: Added streaming support for OpenAI and compatible API responses

- **Live Output**: Text content now streams in real-time for OpenAI compatible models
- **Reasoning Support**: Streams reasoning content for models with extended thinking

## 0.1.19.beta (2025-12-10)

### Features

**Real-time Streaming**: Added streaming support for Anthropic Claude responses

- **Live Output**: Text and thinking content now streams in real-time as the LLM generates it
- **TUI Enhancement**: Interactive chat mode displays streaming responses character-by-character

## 0.1.18.beta (2025-11-24)

**Claude Opus 4.5 Support**: Added support for Anthropic's latest Claude Opus 4.5 model (`claude-opus-4-5-20251101`)

## 0.1.17.beta (2025-11-19)

Introduced mcp-as-code for the purpose of context preservation.

## 0.1.16.beta (2025-11-14)

### Internal Changes

**Prompt Example Simplification**: Streamlined ShortSummaryPrompt examples by removing intermediate analysis blocks for cleaner documentation


## 0.1.15.beta (2025-11-08)

### Features

**Improved PR Recipe**: Enhanced the built-in GitHub PR recipe with more reliable branch comparison

## 0.1.14.beta (2025-10-21)

### Internal Changes

**Frontend Build Tool Upgrade**: Updated Vite from v6.3.6 to v6.4.1 for improved build performance and security patches


## 0.1.12.beta (2025-10-18)

### Features

* Introduced [Haiku 4.5](https://www.anthropic.com/news/claude-haiku-4-5) from Anthropic and use it as the new 'weak' model with improved capabilities.

### Internal Changes

**Dependency Upgrades**: Updated key dependencies for improved stability and compatibility

- **Anthropic SDK**: Upgraded from v1.13.0 to v1.14.0 for latest API features and model support
- **npm**: Updated from 10.9.2 to 11.6.2 across Docker builds and development environment

## 0.1.11.beta (2025-10-11)

Improved `llms.txt` with better recipe system documentation

## 0.1.10.beta (2025-10-11)

### Features

**LLM-Friendly Documentation**: Added new `llms.txt` command for comprehensive LLM-optimized usage documentation

- **CLI Command**: `kodelet llms.txt` displays complete guide optimized for LLM consumption
- **Web Endpoint**: New `/llms.txt` endpoint serves documentation in markdown format with caching
- **Comprehensive Coverage**: Includes quick start, core usage modes, configuration, providers, advanced features, security, and troubleshooting
- **LLM Integration**: Designed for AI agents to quickly understand Kodelet's capabilities and usage patterns

### Bug Fixes

**OpenAI Configuration**: Updated default OpenAI model in setup configuration from `o3` to `gpt-5` for improved compatibility

## 0.1.9.beta (2025-10-07)

### Breaking Changes

**Command Rename**: The `kodelet init` command has been renamed to `kodelet setup` for better clarity

- **Migration**: Update scripts and documentation to use `kodelet setup` instead of `kodelet init`
- **Reason**: Avoids confusion with the new `init` recipe that bootstraps repository-specific AGENTS.md files

### Features

**Repository Initialization Recipe**: New built-in `init` recipe for bootstrapping workspace context. You can run `kodelet run -r init` to analyze your repository and create/enhance AGENTS.md

## 0.1.8.beta (2025-10-04)

### Features

**Fragment Default Values**: Added support for default values in fragments/recipes to reduce repetition

- **YAML Defaults**: Define default values in fragment frontmatter for common arguments
- **Template Defaults**: Use `{{default .variable "fallback"}}` function for optional values with inline fallbacks
- **Smart Merging**: User-provided arguments override defaults, maintaining backward compatibility

**Built-in Recipe Updates**: All built-in recipes now include sensible defaults (e.g., `github/pr` defaults to `target="main"` and `draft="false"`)

## 0.1.7.beta (2025-10-02)

### Internal Changes

**Code Quality and Documentation**: Major refactoring focused on code organization, documentation, and linting standards

- **Enhanced Linting**: Expanded `.golangci.yml` configuration with comprehensive linter suite including staticcheck, unparam, ineffassign, nilnil, and revive
- **Stricter Formatting**: Switched from `go fmt` to `gofumpt` for more consistent code formatting across the codebase

**Note**: This is a maintenance release with no user-facing changes. All changes are internal improvements to code quality and maintainability.

## 0.1.6.beta (2025-10-02)

### Features

**IDE Integration**: Deep IDE integration enabling bidirectional context sharing between Kodelet and your editor

- **Neovim Plugin Support**: Added `--ide` flag for `kodelet run` and `kodelet chat` commands to enable IDE integration mode with prominent conversation ID display
- **Context Sharing**: Automatic sharing of open files, code selections, and LSP diagnostics from IDE to Kodelet via file-based communication (`~/.kodelet/ide/context-{conversation_id}.json`)

**Usage Example**:
```bash
# Start Kodelet with IDE integration
kodelet chat --ide

# In Neovim, attach to the conversation
:KodeletAttach <conversation-id>
```

## 0.1.5.beta (2025-10-01)

### Features

**Enhanced Chat UI**:

- **Tokyo Night Theme**: Applied modern Tokyo Night color palette throughout the chat UI for better visual clarity and reduced eye strain
- **Version Banner**: Added version information to chat welcome screen for better visibility of current release
- **Model Info Display**: Status bar now shows active provider and model (e.g., "anthropic/claude-sonnet-4-5-20250929") for transparency
- **Improved Help Text**: Redesigned keyboard shortcuts and command help with better formatting and clearer organization

### Bug Fixes

- **Conversation ID Generation**: Fixed conversation ID generation logic to ensure unique IDs are created for all chat sessions
- **Log File Handling**: Corrected log file creation with proper octal permission notation (0o644, 0o755)

### Internal Changes

- **Code Organization**: Refactored TUI package into focused modules (`commands.go`, `messages.go`, `views.go`) for better maintainability
- **Test Coverage**: Added comprehensive test suites for command parsing, message formatting, and view rendering utilities
- **Code Cleanup**: Removed redundant code comments throughout TUI implementation

## 0.1.4.beta (2025-09-30)

### Features

**Enhanced Custom Tool Generation**: Improved custom tool creation with global and local scope support

- **Global Tools**: Added `--arg global=true` option to create tools available across all projects in `~/.kodelet/tools`
- **Recipe Rename**: Renamed `custom-tools` recipe to `custom-tool` (singular) for consistency

## 0.1.2.beta (2025-09-30)

### Features

**Conversation Fork Command**: Added ability to fork conversations for experimenting with different directions while preserving context

- **New Command**: `kodelet conversation fork [conversationID]` creates a copy of an existing conversation with reset usage statistics (tokens and costs)
- **Context Preservation**: Forked conversations retain all messages, tool results, file access history, and metadata from the source

**Enhanced Conversation Listing**: Improved `kodelet conversation list` output with usage and context information

- **Cost Display**: Shows total cost (input + output + caching) for each conversation in table format
- **Context Window Tracking**: Displays current/max context window usage (e.g., "50000/200000") to monitor conversation capacity
- **Cleaner Formatting**: Removed newlines from preview text to maintain clean table formatting

## 0.1.1.beta (2025-09-29)

### Features

**Claude Sonnet 4.5 Support**: Added support for Anthropic's latest Claude Sonnet 4.5 model (20250929) with enhanced capabilities

- **New Model Alias**: Added `sonnet-45` alias for `claude-sonnet-4-5-20250929` model
- **Updated Defaults**: Changed default model from Claude Sonnet 4 to Claude Sonnet 4.5 across all configurations and profiles
- **Pricing Integration**: Added pricing information for the new model with prompt caching support
- **Thinking Support**: Full support for extended thinking capabilities in Claude Sonnet 4.5

### Dependencies

- Upgraded `anthropic-sdk-go` from v1.7.0 to v1.13.0 for Claude Sonnet 4.5 support
- Upgraded `tidwall/match` from v1.1.1 to v1.2.0
- Moved `google.golang.org/genai` from indirect to direct dependency

### Internal Changes

- Updated all configuration samples, templates, and documentation to reference Claude Sonnet 4.5
- Updated GitHub Actions workflow templates with new default model
- Refreshed test suites to use Claude Sonnet 4.5 model identifier

## 0.1.0.beta (2025-09-26)

### Features

**Streamlined Multi-Provider Initialization**: Complete redesign of the `kodelet init` command with intelligent defaults and multi-provider support

- **Simplified Setup Process**: Replaced interactive wizard with automatic configuration using sensible defaults, eliminating complex user prompts
- **Multi-Provider Configuration**: Added comprehensive support for Anthropic Claude, OpenAI, Google GenAI, and xAI Grok models with provider-specific profiles
- **Configuration Profiles**: Pre-configured profiles (`default`, `hybrid`, `openai`, `premium`, `google`, `xai`) for different use cases and provider preferences
- **Intelligent API Key Detection**: Smart environment variable detection with clear messaging about API key requirements for each provider
- **Configuration Backup**: Added `--override` flag with automatic backup of existing configuration files to prevent accidental data loss
- **Enhanced Model Aliases**: Built-in model aliases (`sonnet-4`, `haiku-35`, `opus-41`, `gemini-pro`, `gemini-flash`) for easier model selection

## 0.0.100.alpha (2025-09-25)

### Features

**Enhanced Image Analysis**: Improved image analysis prompt structure and guidance for more accurate and actionable responses

## 0.0.99.alpha (2025-09-24)

### Features

**Google GenAI Integration**: Complete support for Google's Gemini and Vertex AI models as a third LLM provider option alongside Anthropic Claude and OpenAI
- **Dual Backend Support**: Seamless integration with both Gemini API (developer-focused) and Vertex AI (enterprise-grade) with automatic backend detection
- **Thinking Capability**: Native support for Google's thinking feature in compatible models (Gemini 2.5 Pro)
- **Tiered Pricing Model**: Intelligent cost calculation supporting Google's complex tiered pricing structure for accurate usage tracking
- **Model Selection**: Support for Gemini 2.5 Pro, Gemini 2.5 Flash, and Gemini 2.5 Flash Lite with appropriate default configurations
- **Thread Interface Compliance**: Full implementation of kodelet's Thread interface including conversation persistence, auto-compaction, and subagent creation


### CI/Build

- **GCP Integration**: Added Google Cloud Workload Identity authentication to test workflow

## 0.0.98.alpha (2025-09-23)

### Features

**Provider-Aware Prompt Rendering**: OpenAI provider now uses an embedded, OpenAI-optimized system and subagent prompt for better alignment and behavior.

### Dependencies

- Bumped `github.com/sashabaranov/go-openai` to v1.41.2.


### Internal Changes

- Removed redundant comments in sysprompt code and tests.
- Added provider selection tests for system/subagent prompt rendering.


## 0.0.97.alpha (2025-09-22)

### Internal Code Quality

**Precise Cost and Usage Reporting**: Enhanced cost calculation precision with standardized rounding to prevent floating-point inconsistencies

- **Improved Precision**: Added rounding function for cost and usage metrics to ensure consistent 4-decimal place precision across all financial calculations
- **Code Cleanup**: Removed unnecessary code comments throughout usage statistics package for improved maintainability
- **Enhanced Testing**: Updated test suite to validate precise rounding behavior for financial calculations

## 0.0.96.alpha (2025-09-18)

### Internal Code Quality

**Comprehensive Package Documentation**: Added standardized package-level documentation comments across all major packages for improved developer experience

- **Enhanced Linting**: Updated lint configuration to include staticcheck with comprehensive analysis including style and documentation rules
- **Code Quality Improvements**: Standardised variable naming conventions throughout the codebase

## 0.0.95.alpha (2025-09-16)

### Headless Mode

- **Headless Mode**: New `--headless` flag for `kodelet run` outputs structured JSON instead of console formatting, useful for automation and 3rd-party UI integration
- **Conversation Stream Command**: New `kodelet conversation stream` command enables streaming of both historical and live conversation updates with unified JSON format
- **Historical Streaming**: Use `--include-history` flag to replay existing conversation data before streaming new entries (similar to `tail -f`)

### Security Updates

**Frontend Security**: Updated Vite from 6.3.5 to 6.3.6 to address security vulnerabilities identified by Dependabot alerts


## 0.0.94.alpha (2025-09-05)

### Go Version Upgrade

* Updated Go runtime from 1.24.2 to 1.25.1
* Display the Go version in `kodelet version` command

## 0.0.93.alpha (2025-09-05)

### Hierarchical Context Discovery

**Hierarchical Context Discovery**: Improve context system enabling dynamic discovery and composition of context files from multiple sources

- **Multi-Source Context Discovery**: Automatically discovers context files from working directory, accessed file paths, and user home directory (`~/.kodelet/`)
- **Access-Based Context Intelligence**: Dynamically includes relevant context files based on actual file access patterns during conversation
- **Hierarchical Context Search**: Walks up directory tree to find nearest context files for accessed files, providing contextually relevant guidance

## 0.0.92.alpha (2025-09-04)

### Internal Changes

**Chat Interface Simplification**: Removed plain UI mode from chat command for streamlined user experience
**Code Cleanup**: Extensive removal of unnecessary code comments across CLI commands for improved code maintainability and readability

## 0.0.91.alpha (2025-09-04)

### Bug Fixes

**Commit Message Confirmation**: Fixed commit message editing workflow to properly use edited messages

### Conversation Enhancements

**Conversation List Default Limit**: Changed default limit for `kodelet conversation list` from 0 (unlimited) to 10 conversations for improved usability. Users can still set `--limit 0` to show all conversations.

## 0.0.90.alpha (2025-09-02)

### Custom Tools System

**Custom Tools System**: Revolutionary custom tool integration allowing users to extend Kodelet with executable tools written in any programming language

- **Universal Language Support**: Create custom tools using Python, Bash, or any executable language with simple two-command protocol (`description` and `run`)
- **Automatic Discovery**: Tools automatically discovered from global (`~/.kodelet/tools`) and local (`./kodelet-tools`) directories with local override support
- **JSON Protocol**: Simple JSON-based input/output protocol with schema validation and structured error handling
- **Built-in Tool Generator**: New `custom-tools` fragment recipe generates complete tool templates with best practices and proper structure

## 0.0.89.alpha (2025-09-01)

### Configuration Management Enhancement

**Configuration Profile System**: Added comprehensive profile system for streamlined model configuration switching and management

- **Profile Management Commands**: New `kodelet profile` command group with `current`, `list`, `show`, and `use` subcommands for easy profile management
- **Dynamic Profile Switching**: Switch between named configuration profiles instantly without manual config file editing (`kodelet profile use premium`)
- **Profile Override Support**: New `--profile` flag available on all commands for temporary profile override (`kodelet run --profile fast "query"`)
- **Hierarchical Configuration**: Profiles support both global (`~/.kodelet/config.yaml`) and repository (`kodelet-config.yaml`) scopes with intelligent merging
- **Built-in Default Profile**: Special "default" profile uses base configuration without any profile settings
- **Tabular Profile Display**: Enhanced `kodelet profile list` with clean tabular format showing profile scope and active status
- **Mix-and-Match Support**: Profiles can configure different providers for main agent and subagent (e.g., Claude for main, OpenAI o3 for reasoning tasks)
- **Comprehensive Documentation**: Added detailed Architecture Decision Record (ADR 017) documenting the profile system design and implementation

## 0.0.88.alpha (2025-09-01)

### Model Support Enhancement

**xAI Grok Preset Renaming**: Renamed Grok preset package to xai for improved branding consistency

**Updated xAI Grok Model Configurations**: Enhanced model support with latest xAI Grok configurations
- Added new `grok-code-fast-1` model optimized for code-related tasks
- Updated pricing information for improved cost accuracy and transparency

## 0.0.87.alpha (2025-08-29)

### Pull Request Workflow Enhancement

**Draft Pull Request Support**: Added `-d/--draft` flag to `kodelet pr` command for creating draft pull requests that are not ready for review

## 0.0.86.alpha (2025-08-27)

### Build System Modernization

**mise Migration**: Comprehensive migration from Make to mise for improved development workflow and tool management

**Unified Tool Management**: Replaced Makefile with `mise.toml` configuration that automatically manages Go, Node.js, npm, and development tool versions for consistent environments across all team members


## 0.0.85.alpha (2025-08-25)

### Version Information Enhancement

**Build Time Tracking**: Enhanced version information with build timestamp for improved debugging and binary identification

## 0.0.84.alpha (2025-08-20)

### Documentation Enhancement

**Context File Naming Consistency**: Renamed AGENT.md to AGENTS.md throughout the codebase for improved clarity and consistency

- **File Rename**: Updated context file name from `AGENT.md` to `AGENTS.md` to better reflect its purpose as agent context documentation
- **Comprehensive Updates**: Updated all references in documentation, constants, tests, and system prompts to use the new naming convention
- **Backward Compatibility**: Maintained existing fallback behavior where `KODELET.md` is used when `AGENTS.md` is not present


## 0.0.83.alpha (2025-08-18)

### Model Support Enhancement

**GPT-5 Model Series Support**: Added models and pricing support for the new GPT-5 model family

## 0.0.82.alpha (2025-08-16)

### Fragment Organization Enhancement

**Hierarchical Fragment System**: Enhanced fragment architecture with nested directory support for improved organization and discoverability

- **GitHub Fragment Reorganization**: Moved GitHub-related fragments (`issue-resolve`, `pr`, `pr-respond`) from root level to organized `github/` subdirectory for better categorization
- **Nested Directory Support**: Fragment system now supports hierarchical organization with subdirectory paths (e.g., `github/issue-resolve` instead of `issue-resolve`)
- **Improved Fragment ID Generation**: Enhanced path handling to support nested fragment references while maintaining backward compatibility
- **Automated Migration**: Built-in commands automatically updated to use new fragment paths without user intervention

## 0.0.81.alpha (2025-08-15)

### Fragment-Based Architecture Enhancement

**Built-in Recipe System**: Core commands now use unified fragment-based architecture with built-in recipes for improved consistency and maintainability

- **Fragment-Powered Commands**: Migrated `commit`, `pr`, and `issue-resolve` commands to use built-in fragment recipes, providing more consistent and template-driven workflows
- **Enhanced Template Support**: PR command now supports custom template files through fragment arguments while maintaining backward compatibility
- **Improved Error Handling**: Fragment bash command execution now returns actual command output instead of generic error messages, improving debugging experience

### Agent Context Files Enhancement

**AGENT.md Context File Migration**: Enhanced context file management with migration from KODELET.md to AGENT.md for improved project understanding

- **Context File Priority System**: Automatic detection with AGENT.md taking precedence over KODELET.md, providing clear migration path for existing projects
- **Enhanced Documentation**: Added extensive agent context files section to MANUAL.md with creation guidelines and usage examples

## 0.0.79.alpha (2025-08-06)

### File Search Performance Improvements

**Intelligent Directory Filtering**: Enhanced glob tool with smart high-volume directory exclusion for significantly improved performance

- **Performance Optimization**: Automatically excludes high-volume directories (`.git`, `node_modules`, `build`, `dist`, `.cache`, `vendor`, etc.) by default to prevent result flooding and improve search speed
- **Selective Access Control**: New `include_high_volume` parameter allows including excluded directories when specifically needed
- **Development-Friendly**: Preserves access to common development directories (`.github`, `.vscode`, etc.) while filtering out performance-heavy directories
- **Backward Compatibility**: Existing glob patterns work unchanged with improved performance characteristics
- **Comprehensive Coverage**: Supports major build systems and package managers (NPM, Python, Go, Rust, Terraform, etc.)


## 0.0.78.alpha (2025-08-06)

### Model Support Enhancement

**Claude Opus 4.1 Model Support**: Added support for the new Claude Opus 4.1 model.
**Anthropic SDK**: Updated `anthropic-sdk-go` from v1.4.0 to v1.7.0 for improved API compatibility and performance
**Remove Deprecated Opus 3 Model**: Removed Opus 3 model support from pricing and model mapping, as they will be deprecated by Anthropic.


## 0.0.77.alpha (2025-07-28)

**Browser Automation Removal**: Completely removed browser automation functionality from Kodelet in favour of Playwright MCP

## 0.0.76.alpha (2025-07-26)

### Recipe Management System

**New Recipe Command**: Added comprehensive `kodelet recipe` command for managing fragments/recipes with metadata support

- **Recipe Listing**: `kodelet recipe list` displays all available recipes with metadata including name, description, and optional file paths
- **Recipe Preview**: `kodelet recipe show <recipe>` renders recipe content with metadata display and template argument substitution
- **JSON Output Support**: `--json` flag for programmatic recipe information access
- **Metadata Integration**: YAML frontmatter support in recipe files for enhanced organization
  ```yaml
  ---
  name: Recipe Display Name
  description: Brief recipe description
  allowed_tools: ["bash", "file_read"]
  allowed_commands: ["git *", "echo *"]
  ---
  ```

### Allowed Tools for agent and subagents

* Added `allowed_tools` config for agents and subagents to restrict tool usage.
* Also allow `allowed_tools` and `allowed_commands` to be specified in the recipe metadata.

## 0.0.75.alpha (2025-07-25)

### Security

- **Dependency Updates**: Updated vulnerable dependencies to resolve Dependabot alerts
  - `golang.org/x/net`: Fixed XSS vulnerabilities and HTTP proxy bypass issues
  - `golang.org/x/oauth2`: Fixed input validation flaws
  - `github.com/go-viper/mapstructure/v2`: Fixed information leakage in logs

## 0.0.74.alpha (2025-07-24)

### Commit Command Enhancements

**Configurable Coauthor Attribution**: Added flexible coauthor configuration for commit messages

- **Global Configuration**: Control coauthor attribution through `config.yaml` settings
  ```yaml
  commit:
    coauthor:
      enabled: true                    # Enable/disable coauthor attribution (default: true)
      name: "Kodelet"                  # Coauthor name (default: "Kodelet")
      email: "noreply@kodelet.com"     # Coauthor email (default: "noreply@kodelet.com")
  ```
- **Per-Command Control**: New `--no-coauthor` flag to disable coauthor attribution for individual commits
- **Environment Variable Support**: Configure via `KODELET_COMMIT_COAUTHOR_*` environment variables
- **Backward Compatibility**: Existing behavior maintained with coauthor attribution enabled by default

## 0.0.73.alpha (2025-07-23)

### API Reliability Enhancements

**Configurable API Retry Mechanism**: Added comprehensive retry configuration for improved API call reliability

```yaml
# API Retry Configuration
retry:
  attempts: 3              # Maximum retry attempts (both providers)
  initial_delay: 1000      # Initial delay in ms (OpenAI only)
  max_delay: 10000         # Maximum delay in ms (OpenAI only)
  backoff_type: "exponential"  # Backoff strategy (OpenAI only)
```

## 0.0.72.alpha (2025-07-22)

### Fragments/Receipts System

- **Template-Based Prompt Management**: Added comprehensive fragments system for creating reusable prompt templates with variable substitution and bash command execution
- **Dynamic Content Generation**: Support for `{{bash "cmd" "arg1" "arg2"}}` syntax to execute shell commands and embed their output directly into prompts
- **Variable Substitution**: Use `{{.variable_name}}` syntax with `--arg key=value` parameter passing for customizable templates
- **Flexible Directory Structure**: Fragments discovered in `./recipes/` (repository-specific) and `~/.kodelet/recipes/` (user-global) with precedence support
- **CLI Integration**: New `-r` flag for fragment selection, `--arg` for parameter passing, and `--fragment-dirs` for custom fragment directories
- **Comprehensive Documentation**: Added detailed documentation in `docs/FRAGMENTS.md` with examples and best practices


## 0.0.71.alpha (2025-07-19)

### Tool System Improvements

- **Enhanced File Edit Tool**: Added support for replace-all functionality to efficiently update multiple occurrences of text patterns across files
- **Streamlined File Reading**: Improved file_read tool with line limits, truncation handling, and better navigation metadata for large files
- **Tool Simplification**: Removed redundant file_multi_edit and batch tools, consolidating functionality into core tools for better performance and maintainability
- **Enhanced Web UI**: Updated frontend components with improved file truncation display and navigation controls

## 0.0.70.alpha (2025-07-18)

### Cross-Provider Subagent Support

- **Provider Mix-and-Match**: Added support for using different LLM providers and models between main agent and subagents
  - Configure main agent with Claude while using GPT models for subagents for cost optimization
  - Mix providers based on task requirements (e.g., Claude for complex analysis, GPT for simple tasks)
  - Independent configuration of provider, model, max tokens, and provider-specific settings
- **Flexible Configuration**: New `subagent` configuration section in `config.yaml` with comprehensive options
  - Provider selection independent of main agent
  - Model-specific configurations for optimal performance
  - Provider-specific settings (reasoning_effort for OpenAI, thinking_budget for Anthropic)
  - Complete OpenAI configuration support for subagents when using different providers
- **Enhanced Testing**: Comprehensive test coverage for cross-provider functionality and improved CI security

## 0.0.69.alpha (2025-07-17)

### LLM Usage Logging Enhancements

- **Enhanced Usage Tracking**: Improved LLM usage logging with request-specific output token tracking for more granular cost analysis
- **Structured Logging**: Added comprehensive structured logging for LLM usage metrics with detailed diagnostics
- **Usage Logging Control**: Added option to disable usage logging for internal operations to reduce noise in weak model workflows
- **Testing Coverage**: Added comprehensive test suite for LLM usage logging functionality to ensure reliability

### OpenAI Provider Configuration

Allow custom OpenAI provider configuration via `openai.api_key_env_var` setting in `config.yaml` to specify which environment variable to use for OpenAI-compatible LLM providers. There are sane presets for popular providers:
* OpenAI: `OPENAI_API_KEY`
* xAI: `XAI_API_KEY`

## 0.0.68.alpha (2025-07-16)

### Feedback System

- **New Feedback Command**: Added `kodelet feedback` command for interactive conversation feedback during the middle of a kodelet run
  - Send feedback to specific conversations with `--conversation-id` flag
  - Use `--follow` flag to send feedback to most recent conversation

## 0.0.67.alpha (2025-07-15)

### Background Process Persistence

- **Conversation Storage Enhancement**: Added support for background processes in conversation persistence
  - Background processes now properly saved and restored across conversation sessions
  - Enhanced SQLite storage with background process metadata and state tracking
  - Improved conversation continuity for long-running tasks and background operations
- **Database Schema Updates**: Extended conversation models to include background process information
  - New migration system for seamless database schema upgrades
  - Enhanced process utilities for better background task management
  - Updated conversation types to support background process lifecycle

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
