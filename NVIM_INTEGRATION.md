# Neovim Integration for Kodelet

## Executive Summary

This document outlines the design and implementation strategy for `kodelet.nvim`, a Neovim plugin that provides deep integration between Neovim and Kodelet. The plugin will enable bidirectional communication, allowing Neovim to share context (open files, selected code, LSP diagnostics) with Kodelet and send messages similar to the existing feedback mechanism.

## Architecture Overview

### Core Components

1. **Kodelet Side (IDE-neutral)**
   - New `--ide` flag for `kodelet run` and `kodelet chat` commands
   - File-based IDE context storage (similar to feedback mechanism)
   - IDE context reader that merges with conversation context
   - Extended feedback mechanism for IDE messages

2. **Neovim Plugin (kodelet.nvim)**
   - Lua-based plugin using Neovim's built-in APIs
   - File-based context writer
   - Buffer change detection and notification
   - Visual selection tracking
   - Command interface for user interactions

### Communication Protocol

The integration will use **file-based communication** (similar to the existing feedback mechanism) for simplicity and reliability:

```
Neovim writes to files → Kodelet reads on each turn
~/.kodelet/ide/context-{conversation_id}.json  (IDE context: open files, selection)
~/.kodelet/feedback/feedback-{conversation_id}.json (Messages - existing feedback)
```

**Why File-based vs Sockets?**

File-based communication is preferred for the MVP because:
- **Simpler**: Reuses proven patterns from feedback mechanism
- **Reliable**: No connection management, works across restarts
- **Sufficient**: We don't need real-time bidirectional communication initially
- **Debuggable**: Can inspect JSON files directly
- **Consistent**: Matches existing kodelet architecture

**Future Enhancement**: Sockets can be added later for bidirectional features like:
- Real-time code navigation (kodelet → IDE)
- Apply code edits directly
- Live diagnostics streaming

## Implementation Design

### 1. Kodelet Side Implementation

#### Command Modifications

##### `cmd/kodelet/run.go` and `cmd/kodelet/chat.go`
```go
type RunConfig struct {
    // ... existing fields ...
    IDE bool  // Enable IDE integration mode
}

// Add flag registration
runCmd.Flags().BoolVar(&config.IDE, "ide", false, "Enable IDE integration")
```

#### New IDE Package: `pkg/ide/`

##### `pkg/ide/store.go`
```go
package ide

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"
    
    "github.com/pkg/errors"
    "github.com/rogpeppe/go-internal/lockedfile"
)

// IDEStore manages IDE context storage (similar to FeedbackStore)
type IDEStore struct {
    ideDir string
    mu     sync.RWMutex
}

// IDEContext represents the current IDE state
type IDEContext struct {
    OpenFiles   []FileInfo       `json:"open_files"`
    Selection   *SelectionInfo   `json:"selection,omitempty"`
    Diagnostics []DiagnosticInfo `json:"diagnostics,omitempty"`
    UpdatedAt   time.Time        `json:"updated_at"`
}

type FileInfo struct {
    Path     string `json:"path"`
    Language string `json:"language,omitempty"`
}

type SelectionInfo struct {
    FilePath  string `json:"file_path"`
    StartLine int    `json:"start_line"`
    EndLine   int    `json:"end_line"`
    Content   string `json:"content"`
}

type DiagnosticInfo struct {
    FilePath string `json:"file_path"`
    Line     int    `json:"line"`
    Column   int    `json:"column,omitempty"`
    Severity string `json:"severity"` // "error", "warning", "info", "hint"
    Message  string `json:"message"`
    Source   string `json:"source,omitempty"` // e.g., "eslint", "gopls", "rust-analyzer"
    Code     string `json:"code,omitempty"`   // e.g., "unused-var", "E0308"
}

func NewIDEStore() (*IDEStore, error) {
    homeDir, err := os.UserHomeDir()
    if err != nil {
        return nil, errors.Wrap(err, "failed to get user home directory")
    }

    ideDir := filepath.Join(homeDir, ".kodelet", "ide")

    if err := os.MkdirAll(ideDir, 0755); err != nil {
        return nil, errors.Wrap(err, "failed to create ide directory")
    }

    return &IDEStore{
        ideDir: ideDir,
    }, nil
}

func (s *IDEStore) getContextPath(conversationID string) string {
    return filepath.Join(s.ideDir, fmt.Sprintf("context-%s.json", conversationID))
}

// WriteContext writes IDE context using atomic file operations
func (s *IDEStore) WriteContext(conversationID string, context *IDEContext) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    filePath := s.getContextPath(conversationID)
    context.UpdatedAt = time.Now()

    data, err := json.MarshalIndent(context, "", "  ")
    if err != nil {
        return errors.Wrap(err, "failed to marshal IDE context")
    }

    // Use lockedfile for atomic write
    if err := lockedfile.Write(filePath, bytes.NewReader(data), 0644); err != nil {
        return errors.Wrap(err, "failed to write IDE context file")
    }

    return nil
}

// ReadContext reads IDE context if available
func (s *IDEStore) ReadContext(conversationID string) (*IDEContext, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    filePath := s.getContextPath(conversationID)

    if _, err := os.Stat(filePath); os.IsNotExist(err) {
        return nil, nil // No context available
    }

    data, err := lockedfile.Read(filePath)
    if err != nil {
        return nil, errors.Wrap(err, "failed to read IDE context file")
    }

    var context IDEContext
    if err := json.Unmarshal(data, &context); err != nil {
        return nil, errors.Wrap(err, "failed to unmarshal IDE context")
    }

    return &context, nil
}

// ClearContext removes IDE context file
func (s *IDEStore) ClearContext(conversationID string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    filePath := s.getContextPath(conversationID)
    
    if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
        return errors.Wrap(err, "failed to remove IDE context file")
    }
    return nil
}

// HasContext checks if IDE context exists
func (s *IDEStore) HasContext(conversationID string) bool {
    filePath := s.getContextPath(conversationID)
    
    if info, err := os.Stat(filePath); err == nil && info.Size() > 0 {
        return true
    }
    
    return false
}
```

##### `pkg/ide/formatter.go`
```go
package ide

import (
    "fmt"
    "strings"
)

// FormatContextPrompt converts IDE context into a prompt string
func FormatContextPrompt(context *IDEContext) string {
    if context == nil {
        return ""
    }
    
    var prompt strings.Builder
    
    if len(context.OpenFiles) > 0 {
        prompt.WriteString("\n## Currently Open Files in IDE\n")
        for _, file := range context.OpenFiles {
            if file.Language != "" {
                prompt.WriteString(fmt.Sprintf("- %s (%s)\n", file.Path, file.Language))
            } else {
                prompt.WriteString(fmt.Sprintf("- %s\n", file.Path))
            }
        }
    }
    
    if context.Selection != nil {
        prompt.WriteString("\n## Currently Selected Code in IDE\n")
        prompt.WriteString(fmt.Sprintf("File: %s (lines %d-%d)\n```\n%s\n```\n",
            context.Selection.FilePath, 
            context.Selection.StartLine,
            context.Selection.EndLine,
            context.Selection.Content))
    }
    
    if len(context.Diagnostics) > 0 {
        prompt.WriteString("\n## IDE Diagnostics\n")
        
        // Group by severity: errors, warnings, others
        errors := []DiagnosticInfo{}
        warnings := []DiagnosticInfo{}
        others := []DiagnosticInfo{}
        
        for _, diag := range context.Diagnostics {
            switch diag.Severity {
            case "error":
                errors = append(errors, diag)
            case "warning":
                warnings = append(warnings, diag)
            default:
                others = append(others, diag)
            }
        }
        
        if len(errors) > 0 {
            prompt.WriteString("\n### Errors\n")
            for _, diag := range errors {
                // Format: file:line:col - [source/code] message
                prompt.WriteString(fmt.Sprintf("- %s:%d:%d - [%s/%s] %s\n",
                    filepath.Base(diag.FilePath), diag.Line, diag.Column,
                    diag.Source, diag.Code, diag.Message))
            }
        }
        
        if len(warnings) > 0 {
            prompt.WriteString("\n### Warnings\n")
            for _, diag := range warnings {
                prompt.WriteString(fmt.Sprintf("- %s:%d:%d - [%s/%s] %s\n",
                    filepath.Base(diag.FilePath), diag.Line, diag.Column,
                    diag.Source, diag.Code, diag.Message))
            }
        }
    }
    
    return prompt.String()
}
```

#### Integration Points

When kodelet processes a conversation turn, it should:

1. **Read IDE Context** (in LLM thread processing):
```go
// In pkg/llm/anthropic/anthropic.go, openai/openai.go, google/google.go
// Before sending messages to LLM

ideStore, _ := ide.NewIDEStore()
ideContext, _ := ideStore.ReadContext(conversationID)

if ideContext != nil {
    // Prepend IDE context to system prompt or first user message
    contextPrompt := ide.FormatContextPrompt(ideContext)
    // Add to conversation context
}
```

2. **Clear IDE Context After Processing**:
```go
// After successful LLM response
if ideContext != nil {
    ideStore.ClearContext(conversationID)
}
```

3. **No Changes Needed to Feedback System** - The neovim plugin will write directly to the existing feedback file using `kodelet feedback -f` or by writing to `~/.kodelet/feedback/feedback-{conversationID}.json` directly

### 2. Neovim Plugin Implementation

#### Plugin Structure
```
kodelet.nvim/
├── lua/
│   └── kodelet/
│       ├── init.lua          # Main plugin entry
│       ├── writer.lua         # File-based context writer
│       ├── context.lua        # Context gathering and autocmds
│       ├── commands.lua       # User commands
│       └── config.lua         # Configuration (optional)
├── plugin/
│   └── kodelet.vim           # Vim script entry point (optional)
└── README.md
```

#### Core Implementation

##### `lua/kodelet/writer.lua`
```lua
local M = {}

M.conversation_id = nil
M.ide_dir = vim.fn.expand("~/.kodelet/ide")

function M.set_conversation_id(conv_id)
    M.conversation_id = conv_id
    -- Ensure IDE directory exists
    vim.fn.mkdir(M.ide_dir, "p")
end

function M.get_context_path()
    if not M.conversation_id then
        return nil
    end
    return M.ide_dir .. "/context-" .. M.conversation_id .. ".json"
end

-- Gather current IDE context
function M.gather_context(include_diagnostics)
    local open_files = {}
    local buffers = vim.api.nvim_list_bufs()
    
    for _, buf in ipairs(buffers) do
        if vim.api.nvim_buf_is_loaded(buf) and vim.bo[buf].buflisted then
            local filepath = vim.api.nvim_buf_get_name(buf)
            if filepath ~= "" and vim.fn.filereadable(filepath) == 1 then
                table.insert(open_files, {
                    path = filepath,
                    language = vim.bo[buf].filetype
                })
            end
        end
    end
    
    local context = {
        open_files = open_files,
        updated_at = os.date("!%Y-%m-%dT%H:%M:%SZ")
    }
    
    -- Optionally include diagnostics
    if include_diagnostics then
        context.diagnostics = M.gather_diagnostics()
    end
    
    return context
end

-- Gather diagnostics from all open buffers
function M.gather_diagnostics()
    local diagnostics = {}
    local buffers = vim.api.nvim_list_bufs()
    
    for _, buf in ipairs(buffers) do
        if vim.api.nvim_buf_is_loaded(buf) then
            local filepath = vim.api.nvim_buf_get_name(buf)
            if filepath ~= "" then
                local buf_diagnostics = vim.diagnostic.get(buf)
                
                for _, diag in ipairs(buf_diagnostics) do
                    local severity_map = {
                        [vim.diagnostic.severity.ERROR] = "error",
                        [vim.diagnostic.severity.WARN] = "warning",
                        [vim.diagnostic.severity.INFO] = "info",
                        [vim.diagnostic.severity.HINT] = "hint"
                    }
                    
                    table.insert(diagnostics, {
                        file_path = filepath,
                        line = diag.lnum + 1,  -- Convert 0-indexed to 1-indexed
                        column = diag.col + 1,
                        severity = severity_map[diag.severity] or "info",
                        message = diag.message,
                        source = diag.source or "",
                        code = diag.code or ""
                    })
                end
            end
        end
    end
    
    return diagnostics
end

-- Write IDE context to file (always includes diagnostics)
function M.write_context()
    if not M.conversation_id then
        vim.notify("Not attached to Kodelet session", vim.log.levels.WARN)
        return false
    end
    
    local context = M.gather_context(true)  -- Always include diagnostics
    local context_path = M.get_context_path()
    
    local json_str = vim.fn.json_encode(context)
    local success = vim.fn.writefile({json_str}, context_path)
    
    if success == 0 then
        return true
    else
        vim.notify("Failed to write IDE context", vim.log.levels.ERROR)
        return false
    end
end

-- Update context with selection (always includes diagnostics)
function M.write_context_with_selection(selection_info)
    if not M.conversation_id then
        vim.notify("Not attached to Kodelet session", vim.log.levels.WARN)
        return false
    end
    
    local context = M.gather_context(true)  -- Include diagnostics
    context.selection = selection_info
    
    local context_path = M.get_context_path()
    local json_str = vim.fn.json_encode(context)
    local success = vim.fn.writefile({json_str}, context_path)
    
    return success == 0
end

-- Clear IDE context file
function M.clear_context()
    if not M.conversation_id then
        return false
    end
    
    local context_path = M.get_context_path()
    if vim.fn.filereadable(context_path) == 1 then
        vim.fn.delete(context_path)
        return true
    end
    return false
end

return M
```

##### `lua/kodelet/context.lua`
```lua
local M = {}
local writer = require('kodelet.writer')

-- Track buffer changes and update context file
function M.setup_autocmds()
    local group = vim.api.nvim_create_augroup("KodeletContext", { clear = true })
    
    -- Update context when buffers change
    vim.api.nvim_create_autocmd({"BufEnter", "BufDelete", "BufWipeout"}, {
        group = group,
        callback = function()
            -- Debounce: only write after a short delay
            vim.defer_fn(function()
                writer.write_context()
            end, 200)
        end
    })
end

-- Send current visual selection to Kodelet
function M.send_selection()
    local start_pos = vim.fn.getpos("'<")
    local end_pos = vim.fn.getpos("'>")
    local filepath = vim.fn.expand("%:p")
    
    -- Get selected lines
    local lines = vim.api.nvim_buf_get_lines(
        0,
        start_pos[2] - 1,
        end_pos[2],
        false
    )
    
    local content = table.concat(lines, "\n")
    
    local selection_info = {
        file_path = filepath,
        start_line = start_pos[2],
        end_line = end_pos[2],
        content = content
    }
    
    -- Write context with selection
    if writer.write_context_with_selection(selection_info) then
        vim.notify("Selection added to Kodelet context", vim.log.levels.INFO)
    else
        vim.notify("Failed to add selection to context", vim.log.levels.ERROR)
    end
end

return M
```

##### `lua/kodelet/commands.lua`
```lua
local M = {}
local writer = require('kodelet.writer')

-- Fetch conversation list for completion
local function fetch_conversations()
    local handle = io.popen("kodelet conversation list --json 2>/dev/null")
    if not handle then
        return {}
    end
    
    local output = handle:read("*a")
    handle:close()
    
    if output == "" then
        return {}
    end
    
    local ok, conversations = pcall(vim.fn.json_decode, output)
    if not ok or type(conversations) ~= "table" then
        return {}
    end
    
    return conversations
end

-- Completion function for KodeletAttach
local function complete_conversation_id(arg_lead, cmd_line, cursor_pos)
    local conversations = fetch_conversations()
    local completions = {}
    
    for _, conv in ipairs(conversations) do
        if conv.id and conv.summary then
            -- Format: "ID    summary"
            local completion = string.format("%s\t%s", conv.id, conv.summary)
            if vim.startswith(conv.id, arg_lead) then
                table.insert(completions, completion)
            end
        elseif conv.id then
            if vim.startswith(conv.id, arg_lead) then
                table.insert(completions, conv.id)
            end
        end
    end
    
    return completions
end

function M.setup()
    -- Attach to Kodelet session with tab completion
    vim.api.nvim_create_user_command("KodeletAttach", function(args)
        local conversation_id = args.args
        
        if conversation_id == "" then
            -- Try to find the most recent conversation
            local conversations = fetch_conversations()
            
            if #conversations == 0 then
                vim.notify("No conversations found", vim.log.levels.WARN)
                return
            end
            
            -- Use most recent
            if conversations[1] and conversations[1].id then
                conversation_id = conversations[1].id
            else
                vim.notify("No conversation ID found", vim.log.levels.ERROR)
                return
            end
        else
            -- Extract just the ID if user selected with summary (ID\tsummary)
            local id_part = vim.split(conversation_id, "\t")[1]
            conversation_id = id_part
        end
        
        if conversation_id ~= "" then
            writer.set_conversation_id(conversation_id)
            writer.write_context()
            vim.notify("Attached to Kodelet session: " .. conversation_id, vim.log.levels.INFO)
        else
            vim.notify("No conversation ID provided or found", vim.log.levels.ERROR)
        end
    end, { 
        nargs = "?",
        complete = complete_conversation_id,
        desc = "Attach to a Kodelet conversation (tab to see list)"
    })
    
    -- Alternative: Use vim.ui.select for interactive picker
    vim.api.nvim_create_user_command("KodeletAttachSelect", function()
        local conversations = fetch_conversations()
        
        if #conversations == 0 then
            vim.notify("No conversations found", vim.log.levels.WARN)
            return
        end
        
        -- Format for display
        local items = {}
        local id_map = {}
        for i, conv in ipairs(conversations) do
            local label = conv.id
            if conv.summary then
                label = string.format("%s - %s", conv.id, conv.summary)
            end
            items[i] = label
            id_map[i] = conv.id
        end
        
        vim.ui.select(items, {
            prompt = "Select Kodelet conversation:",
            format_item = function(item)
                return item
            end,
        }, function(choice, idx)
            if idx then
                local conversation_id = id_map[idx]
                writer.set_conversation_id(conversation_id)
                writer.write_context()
                vim.notify("Attached to Kodelet session: " .. conversation_id, vim.log.levels.INFO)
            end
        end)
    end, { desc = "Attach to Kodelet conversation using picker" })
    
    -- Send feedback message to Kodelet using the CLI
    vim.api.nvim_create_user_command("KodeletFeedback", function(args)
        if not writer.conversation_id then
            vim.notify("Not attached to a Kodelet session. Use :KodeletAttach first", vim.log.levels.WARN)
            return
        end
        
        local message = args.args
        if message == "" then
            message = vim.fn.input("Feedback message: ")
        end
        
        if message ~= "" then
            -- Use kodelet feedback CLI
            local escaped_msg = message:gsub("'", "'\\''")
            local cmd = string.format("kodelet feedback --conversation-id %s '%s' 2>&1", 
                writer.conversation_id, escaped_msg)
            
            local handle = io.popen(cmd)
            if handle then
                local output = handle:read("*a")
                local success = handle:close()
                
                if success then
                    vim.notify("Feedback sent to Kodelet", vim.log.levels.INFO)
                else
                    vim.notify("Failed to send feedback: " .. output, vim.log.levels.ERROR)
                end
            end
        end
    end, { nargs = "*" })
    
    -- Send visual selection
    vim.api.nvim_create_user_command("KodeletSendSelection", function()
        if not writer.conversation_id then
            vim.notify("Not attached to a Kodelet session. Use :KodeletAttach first", vim.log.levels.WARN)
            return
        end
        require('kodelet.context').send_selection()
    end, { range = true })
    
    -- Detach from session
    vim.api.nvim_create_user_command("KodeletDetach", function()
        writer.set_conversation_id(nil)
        vim.notify("Detached from Kodelet session", vim.log.levels.INFO)
    end, {})
    
    -- Show status
    vim.api.nvim_create_user_command("KodeletStatus", function()
        if writer.conversation_id then
            vim.notify("Attached to Kodelet: " .. writer.conversation_id, vim.log.levels.INFO)
        else
            vim.notify("Not attached to Kodelet session", vim.log.levels.WARN)
        end
    end, {})
    
    -- Clear context manually
    vim.api.nvim_create_user_command("KodeletClearContext", function()
        if not writer.conversation_id then
            vim.notify("Not attached to a Kodelet session", vim.log.levels.WARN)
            return
        end
        
        if writer.clear_context() then
            vim.notify("Context cleared", vim.log.levels.INFO)
        else
            vim.notify("No context to clear", vim.log.levels.INFO)
        end
    end, {})
end

return M
```

##### `lua/kodelet/init.lua`
```lua
local M = {}

function M.setup(opts)
    opts = opts or {}
    
    -- Setup commands
    require('kodelet.commands').setup()
    
    -- Setup context tracking (only if attached to a session)
    require('kodelet.context').setup_autocmds()
    
    -- Auto-attach if KODELET_CONVERSATION_ID env var is set
    local env_conv_id = vim.env.KODELET_CONVERSATION_ID
    if env_conv_id and env_conv_id ~= "" then
        vim.defer_fn(function()
            local writer = require('kodelet.writer')
            writer.set_conversation_id(env_conv_id)
            writer.write_context()
            vim.notify("Auto-attached to Kodelet session: " .. env_conv_id, vim.log.levels.INFO)
        end, 100)
    end
end

return M
```

### 3. Usage Workflow

#### Basic Workflow (No --ide flag needed for MVP)

**Option 1: Start Kodelet in terminal, attach from Neovim**

```bash
# In terminal: Start kodelet
kodelet chat
# Note the conversation ID from the prompt or use :KodeletAttach to auto-detect
```

```vim
" In Neovim: Attach to the session by typing conversation ID
:KodeletAttach 20241201T120000-a1b2c3d4e5f67890

" Or use tab completion to browse conversations
:KodeletAttach <Tab>
" This shows: "20241201T120000-a1b2c3d4e5f67890    Refactor authentication module"

" Or attach to most recent conversation
:KodeletAttach

" Alternative: Use interactive picker (if you have telescope or fzf)
:KodeletAttachSelect

 Neovim now automatically updates open files context (with diagnostics)
" Send feedback/message
:KodeletFeedback Please refactor the function in utils.go

" Send visual selection (in visual mode)
:'<,'>KodeletSendSelection

" Check attachment status
:KodeletStatus

" Manually clear context (context auto-updates on buffer changes)
:KodeletClearContext

" Detach from session
:KodeletDetach
```

**Option 2: Start Kodelet with environment variable**

```bash
# Start kodelet and export conversation ID
export KODELET_CONVERSATION_ID=$(kodelet conversation list --limit 1 | head -1 | awk '{print $1}')
nvim

# Or start neovim in the kodelet process
KODELET_CONVERSATION_ID=20241201T120000-a1b2c3d4e5f67890 nvim
```

Neovim will auto-attach to the session on startup.

#### How It Works

1. **Tab Completion for Attach**: When you type `:KodeletAttach <Tab>`, the plugin fetches conversation list using `kodelet conversation list --json` and shows conversation IDs with their summaries for easy selection
2. **Context Sharing**: When you attach to a Kodelet session, the plugin automatically writes `~/.kodelet/ide/context-{conversation_id}.json` with your open files and LSP diagnostics
3. **Auto-Update**: Context file is updated automatically when you open/close buffers (with 200ms debounce), always including current diagnostics
4. **Selection Sharing**: Use `:KodeletSendSelection` to add selected code to the context
5. **Feedback Messages**: Use `:KodeletFeedback` to send messages (uses existing `kodelet feedback` CLI)
6. **Processing**: When Kodelet processes the next turn, it reads both the IDE context and feedback files, then clears them

## Implementation Phases

### Phase 1: Core File-Based Integration (MVP - Week 1)
- [ ] Create `pkg/ide/store.go` - IDE context storage (similar to feedback)
- [ ] Create `pkg/ide/formatter.go` - Format IDE context as prompt text
- [ ] Integrate IDE context reading in LLM thread processing
- [ ] Create neovim plugin structure and basic commands
- [ ] Implement file context gathering and writing
- [ ] Test basic workflow: attach, context update, feedback

### Phase 2: Enhanced Neovim Features (Week 2)
- [ ] Implement selection sharing (`:KodeletSendSelection`)
- [ ] Add automatic context updates on buffer changes
- [ ] Implement debouncing for performance
- [ ] Add `--ide` flag for better UX (display conv ID prominently)
- [ ] Create plugin documentation and installation guide
- [ ] Add telescope.nvim integration for conversation browsing

### Phase 3: Socket-Based Bidirectional Communication (Future)
- [ ] Implement Unix socket server in kodelet
- [ ] Add real-time context updates
- [ ] Implement kodelet → neovim commands (apply edits, jump to file)
- [ ] Add live diagnostics streaming
- [ ] Support code actions and refactoring requests

### Phase 4: Polish and Advanced Features (Future)
- [ ] LSP-like integration for code intelligence
- [ ] Inline code suggestions
- [ ] Multi-file refactoring support
- [ ] Session management UI

## Security Considerations

1. **File Permissions**: IDE context files should use appropriate permissions (0644)
2. **Path Sanitization**: File paths should be sanitized and validated before use
3. **Message Size Limits**: Context files should have reasonable size limits
4. **Atomic File Operations**: Use `lockedfile` package for thread-safe file operations

## Alternative Approaches Considered

### 1. Unix Domain Sockets (Originally Proposed)
**Pros**: Real-time bidirectional communication, low latency
**Cons**: Connection management complexity, requires socket lifecycle handling
**Decision**: Deferred to Phase 3 as it's not needed for MVP; file-based is sufficient

### 2. File-Based Communication (Chosen for MVP)
**Pros**: Simple, reliable, reuses existing patterns, no connection management
**Cons**: Not real-time, higher latency, requires file polling
**Decision**: **SELECTED** - Perfect for MVP, matches existing feedback architecture

### 3. HTTP/REST API
**Pros**: Language agnostic, easier debugging, well understood
**Cons**: Port management, overkill for local communication
**Decision**: Rejected as too complex for local IDE integration

### 4. Named Pipes (FIFOs)
**Pros**: Simple implementation, no network overhead
**Cons**: Unidirectional, buffer management issues, less portable
**Decision**: Rejected in favor of regular files with proven locking mechanism

## Testing Strategy

1. **Unit Tests**: Test message serialization, context gathering
2. **Integration Tests**: Test socket communication, message flow
3. **End-to-End Tests**: Full workflow testing with mock Kodelet server
4. **Manual Testing**: Real-world usage scenarios

## Conclusion

The proposed file-based architecture provides a simple, reliable foundation for IDE integration that:

1. **Reuses Proven Patterns**: Mirrors the existing feedback mechanism with atomic file operations
2. **Minimizes Complexity**: No connection management, socket lifecycle, or timing issues
3. **IDE-Neutral Design**: Kodelet side is generic; IDE-specific logic stays in plugins
4. **Incremental Enhancement**: Can add socket-based features later without breaking changes
5. **Immediate Value**: Provides context awareness and feedback integration from day one

The MVP focuses on the most valuable features (open files context, selection sharing, feedback messages) with the simplest implementation. Socket-based bidirectional communication can be added in Phase 3 when use cases requiring real-time interaction emerge (apply edits, code navigation requests, live diagnostics).

This approach follows the principle: **start simple, prove value, then enhance**.