# IDE Integration for Kodelet

## Overview

Kodelet now supports deep IDE integration, enabling bidirectional context sharing between your IDE and Kodelet sessions. The first supported IDE is Neovim via the `kodelet.nvim` plugin.

## Architecture

The integration uses **file-based communication** for simplicity and reliability:

```
┌─────────────┐                          ┌──────────────┐
│   Neovim    │──── writes context ────▶ │  ~/.kodelet/ │
│  (Plugin)   │                          │     ide/     │
└─────────────┘                          └──────────────┘
                                                 │
                                                 ▼
                                         ┌──────────────┐
                                         │   Kodelet    │
                                         │   (reads &   │
                                         │   processes) │
                                         └──────────────┘
```

### Communication Protocol

**IDE Context**: `~/.kodelet/ide/context-{conversation_id}.json`

Contains:
- Open files with language info
- Code selections (visual mode)
- LSP diagnostics (errors, warnings, hints)
- Timestamp

**Lifecycle**:
1. IDE writes context file when attached to a conversation
2. Context updates automatically on buffer changes (debounced)
3. Kodelet reads context at the start of each LLM turn
4. Context is prepended to the system prompt
5. Kodelet clears context after processing

**Feedback**: Uses existing `kodelet feedback` CLI command

## Components

### Kodelet Side (Go)

#### `pkg/ide/store.go`
- `IDEStore`: Manages IDE context storage
- `WriteContext()`: Atomic file write using `lockedfile`
- `ReadContext()`: Read context for a conversation
- `ClearContext()`: Remove context after processing
- `HasContext()`: Check if context exists

#### `pkg/ide/formatter.go`
- `FormatContextPrompt()`: Converts IDE context to LLM prompt format
- Groups diagnostics by severity (errors, warnings, others)
- Formats file paths, selections, and diagnostic messages

#### Integration Points
- **Anthropic** (`pkg/llm/anthropic/anthropic.go`): Prepends IDE context to system prompt blocks
- **OpenAI** (`pkg/llm/openai/openai.go`): Prepends IDE context to system message
- **Google** (`pkg/llm/google/google.go`): Prepends IDE context to system prompt string

### Neovim Side (Lua)

Located in `kodelet.nvim/` directory.

#### `lua/kodelet/writer.lua`
- Gathers IDE context (open files, diagnostics)
- Writes context to JSON file
- Handles selections and context clearing

#### `lua/kodelet/context.lua`
- Sets up autocmds for buffer change tracking
- Debounced context updates (200ms)
- Visual selection extraction

#### `lua/kodelet/commands.lua`
- `:KodeletAttach` - Attach to conversation with tab completion
- `:KodeletAttachSelect` - Interactive conversation picker
- `:KodeletFeedback` - Send feedback messages
- `:KodeletSendSelection` - Send visual selection
- `:KodeletStatus` - Show connection status
- `:KodeletClearContext` - Clear context manually
- `:KodeletDetach` - Detach from conversation

#### `lua/kodelet/init.lua`
- Plugin entry point
- Auto-attach support via `KODELET_CONVERSATION_ID` env var

## Usage Example

### Terminal 1: Start Kodelet
```bash
kodelet chat
# Conversation ID: 20241201T120000-a1b2c3d4e5f67890
```

### Terminal 2: Neovim
```vim
" Attach to conversation
:KodeletAttach <Tab>
" Select: 20241201T120000-a1b2c3d4e5f67890    Refactor authentication module

" Context is now shared automatically:
" - Open files
" - LSP diagnostics
" - Updates on buffer changes

" Send a message
:KodeletFeedback Please refactor the login function to use proper error handling

" Send visual selection (in visual mode)
:'<,'>KodeletSendSelection
```

### Terminal 1: Kodelet Response
Kodelet receives the context and processes it:
```
[INFO] processing IDE context
  open_files_count: 3
  has_selection: true
  diagnostics_count: 2
```

The system prompt includes:
```
## Currently Open Files in IDE
- /path/to/auth.go (go)
- /path/to/login.go (go)
- /path/to/user.go (go)

## Currently Selected Code in IDE
File: /path/to/login.go (lines 45-60)
```
func Login(username, password string) error {
    // current implementation
}
```

## IDE Diagnostics

### Errors
- auth.go:23:5 - [gopls/UndeclaredName] undefined variable 'user'

### Warnings
- login.go:50:10 - [gopls/UnusedVar] unused variable 'token'
```

## Benefits

1. **Context Awareness**: Kodelet sees what you're working on
2. **Diagnostics Integration**: Share compiler/linter errors directly
3. **Precise Discussions**: Send exact code selections
4. **Seamless Workflow**: Auto-updates, no manual copy-paste
5. **LSP Integration**: Leverages your existing LSP setup

## Installation

See `kodelet.nvim/README.md` for detailed installation instructions for Neovim.

## Future Enhancements

### Phase 2: Enhanced Features
- Telescope integration for conversation browsing
- Better visual feedback in Neovim
- `--ide` flag for better UX (display conversation ID prominently)

### Phase 3: Bidirectional Communication (Socket-based)
- Real-time context updates without file polling
- Kodelet → IDE commands:
  - Apply code edits
  - Jump to file/line
  - Highlight code
- Live diagnostics streaming
- Code actions and refactoring requests

### Phase 4: Other IDEs
- VS Code extension
- JetBrains plugin
- Emacs integration

## Security Considerations

- Context files use standard file permissions (0644)
- File paths are not sanitized (trust localhost)
- Atomic file operations prevent race conditions
- No sensitive data in context (only file paths and diagnostics)

## Testing

Run the IDE integration tests:
```bash
go test ./pkg/ide/... -v
```

Test coverage:
- Context write/read/clear operations
- File locking and atomic writes
- Context formatting and prompt generation
- Diagnostics grouping and formatting
- Selection extraction

## Troubleshooting

### Context not updating
```bash
# Check context file exists
ls -la ~/.kodelet/ide/
cat ~/.kodelet/ide/context-*.json | jq .
```

### Kodelet not receiving context
```bash
# Verify conversation ID matches
kodelet conversation list

# Check logs
kodelet chat --log-level debug
```

### Neovim plugin not loading
```vim
:lua print(vim.inspect(package.loaded['kodelet']))
:lua require('kodelet').setup()
```

## Contributing

Contributions welcome! Areas for improvement:
- Additional IDE integrations
- Enhanced context filtering
- Performance optimizations
- Better error handling

## Related Files

- `pkg/ide/store.go` - IDE context storage
- `pkg/ide/formatter.go` - Context formatting
- `pkg/llm/anthropic/anthropic.go` - Anthropic integration
- `pkg/llm/openai/openai.go` - OpenAI integration
- `pkg/llm/google/google.go` - Google integration
- `kodelet.nvim/` - Neovim plugin
