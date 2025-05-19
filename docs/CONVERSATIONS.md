# Conversation Management

Kodelet provides dedicated commands for managing saved conversations from both chat sessions and one-shot `run` commands.

## Commands

### Listing Conversations

```bash
# Basic listing (newest first)
kodelet conversation list

# List with filtering by search term
kodelet conversation list --search "keyword"

# List with date filtering
kodelet conversation list --start "2025-05-01" --end "2025-05-19"

# Pagination control
kodelet conversation list --limit 10 --offset 0

# Sort options
kodelet conversation list --sort-by "updated" --sort-order "desc"  # default
kodelet conversation list --sort-by "created" --sort-order "asc"
kodelet conversation list --sort-by "messages" --sort-order "desc"

# Output as JSON
kodelet conversation list --json
```

### Showing Conversations

```bash
# Show a conversation in text format (default)
kodelet conversation show <conversation-id>

# Show in different formats
kodelet conversation show <conversation-id> --format text  # Default format with user/assistant labels
kodelet conversation show <conversation-id> --format json  # Structured JSON output
kodelet conversation show <conversation-id> --format raw   # Raw message format as stored
```

### Deleting Conversations

```bash
# Delete with confirmation prompt
kodelet conversation delete <conversation-id>

# Delete without confirmation
kodelet conversation delete --no-confirm <conversation-id>
```

## Resuming Conversations

You can resume a conversation either in interactive chat mode or one-shot mode:

```bash
# Resume in chat mode
kodelet chat --resume <conversation-id>

# Resume in one-shot mode
kodelet run --resume <conversation-id> "new message"
```

## Storage

Conversation data is stored locally and can be configured to use either JSON files (default) or SQLite:

```bash
# Specify storage type
kodelet chat --storage sqlite
```

## Disabling Persistence

You can disable conversation persistence for any session:

```bash
kodelet chat --no-save
kodelet run --no-save "query"
```