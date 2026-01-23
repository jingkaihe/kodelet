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

You can resume a conversation in one-shot mode:

```bash
# Resume in one-shot mode
kodelet run --resume <conversation-id> "new message"
```

## Storage

Conversation data is stored locally using SQLite by default.

## Disabling Persistence

You can disable conversation persistence for any session:

```bash
kodelet run --no-save "query"
```