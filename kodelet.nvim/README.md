# kodelet.nvim

A Neovim plugin for deep integration with [Kodelet](https://github.com/jingkaihe/kodelet) - enabling context sharing, code selections, LSP diagnostics, and feedback messaging between Neovim and Kodelet sessions.

## Features

- 🔗 **Easy Attachment**: Tab completion to browse and attach to Kodelet conversations
- 📁 **Context Sharing**: Automatically share open files and project structure
- 🐛 **Diagnostics Integration**: Share LSP diagnostics (errors, warnings, hints)
- 📝 **Code Selection**: Send specific code snippets for focused discussion
- 💬 **Feedback System**: Send messages to running Kodelet sessions
- 🔄 **Auto-Updates**: Context updates automatically on buffer changes

## Prerequisites

- Neovim ≥ 0.8.0
- [Kodelet](https://github.com/jingkaihe/kodelet) installed and available in PATH
- LSP configured (optional, for diagnostics integration)

## Installation

### Using [lazy.nvim](https://github.com/folke/lazy.nvim)

```lua
{
  "jingkaihe/kodelet.nvim",
  event = "VeryLazy",
  config = function()
    require("kodelet").setup()
  end,
}
```

### With keymappings:

```lua
{
  "jingkaihe/kodelet.nvim",
  event = "VeryLazy",
  keys = {
    { "<leader>ka", "<cmd>KodeletAttach<cr>", desc = "Kodelet: Attach to conversation" },
    { "<leader>ks", "<cmd>KodeletAttachSelect<cr>", desc = "Kodelet: Attach with picker" },
    { "<leader>kf", "<cmd>KodeletFeedback<cr>", desc = "Kodelet: Send feedback" },
    { "<leader>kd", "<cmd>KodeletDetach<cr>", desc = "Kodelet: Detach" },
    { "<leader>kc", "<cmd>KodeletClearContext<cr>", desc = "Kodelet: Clear context" },
    { "<leader>kt", "<cmd>KodeletStatus<cr>", desc = "Kodelet: Show status" },
    { "<leader>ks", ":'<,'>KodeletSendSelection<cr>", mode = "v", desc = "Kodelet: Send selection" },
  },
  config = function()
    require("kodelet").setup()
  end,
}
```

### Using [packer.nvim](https://github.com/wbthomason/packer.nvim)

```lua
use {
  'jingkaihe/kodelet.nvim',
  config = function()
    require('kodelet').setup()
  end
}
```

### Using [vim-plug](https://github.com/junegunn/vim-plug)

```vim
Plug 'jingkaihe/kodelet.nvim'

lua << EOF
require('kodelet').setup()
EOF
```

## Usage

### Basic Workflow

1. **Start Kodelet in terminal**:
   ```bash
   kodelet chat
   ```

2. **In Neovim, attach to the conversation**:
   ```vim
   :KodeletAttach <Tab>
   " Tab shows: "20241201T120000-a1b2c3d4e5f67890    Refactor authentication module"
   
   " Or attach to most recent:
   :KodeletAttach
   
   " Or use interactive picker:
   :KodeletAttachSelect
   ```

3. **Context is automatically shared**:
   - Open files are tracked
   - LSP diagnostics are included
   - Updates on buffer changes (debounced)

4. **Send messages**:
   ```vim
   :KodeletFeedback Please refactor this function
   ```

5. **Send code selections** (in visual mode):
   ```vim
   :'<,'>KodeletSendSelection
   ```

### Commands

- `:KodeletAttach [conversation-id]` - Attach to Kodelet conversation (tab completion available)
- `:KodeletAttachSelect` - Interactive conversation picker
- `:KodeletFeedback [message]` - Send feedback/message to Kodelet
- `:KodeletSendSelection` - Send visual selection to Kodelet (visual mode)
- `:KodeletStatus` - Show current connection status
- `:KodeletClearContext` - Manually clear context
- `:KodeletDetach` - Detach from conversation

### Environment Variable Auto-Attach

Set `KODELET_CONVERSATION_ID` to auto-attach on Neovim startup:

```bash
export KODELET_CONVERSATION_ID=20241201T120000-a1b2c3d4e5f67890
nvim
```

## How It Works

The plugin uses file-based communication with Kodelet:

- **IDE Context**: `~/.kodelet/ide/context-{conversation_id}.json`
  - Contains open files, selections, and diagnostics
  - Updated automatically on buffer changes
  - Read and cleared by Kodelet on each turn

- **Feedback**: Uses Kodelet's `kodelet feedback` CLI command
  - Messages are appended to feedback queue
  - Processed by Kodelet on next turn

## Configuration

Currently the plugin works out-of-the-box with sensible defaults. Future versions may add configuration options for:

- Debounce delay for context updates
- Which diagnostics to include
- Custom context filters

## Development

For local development:

```lua
{
  dir = vim.fn.expand("~/path/to/kodelet.nvim"),
  name = "kodelet-dev",
  config = function()
    require("kodelet").setup()
  end,
}
```

Quick reload during development:

```vim
:lua package.loaded['kodelet'] = nil
:lua package.loaded['kodelet.writer'] = nil
:lua package.loaded['kodelet.context'] = nil
:lua package.loaded['kodelet.commands'] = nil
:lua require("kodelet").setup()
```

## Troubleshooting

**Plugin not loading:**
```vim
:lua print(vim.inspect(package.loaded['kodelet']))
```

**Context file not created:**
```bash
ls -la ~/.kodelet/ide/
cat ~/.kodelet/ide/context-*.json | jq .
```

**Kodelet not receiving context:**
- Ensure Kodelet CLI is in PATH: `which kodelet`
- Check conversation list works: `kodelet conversation list --json`
- Verify context file exists after attaching

## Contributing

Contributions welcome! Please open issues or PRs on the [main Kodelet repository](https://github.com/jingkaihe/kodelet).

## License

MIT License - see [LICENSE](../LICENSE) file for details.
