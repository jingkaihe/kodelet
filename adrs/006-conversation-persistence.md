# ADR 006: Conversation Persistence

## Status
Proposed

## Context
Currently, Kodelet's chat mode (`kodelet chat`) does not persist conversation history between sessions. When a user exits the application, all conversation context and history is lost. This limits the usefulness of the tool for longer-term projects and requires users to manually track and restate context for related queries across multiple sessions.

Users have expressed interest in being able to:
- Resume previous conversations
- Review past interactions
- Manage conversation history

Additionally, persisting conversations would allow for future features like conversation search, analysis of common patterns, and potentially training custom assistants based on historical interactions.

## Decision
We will implement conversation persistence with a storage-agnostic approach that initially uses JSON files but can be extended to support SQLite in the future:

1. Store conversation data in the user's cache directory:
   - Default location: `~/.cache/kodelet/conversations/`
   - Initial format: JSON files with pattern `{conversation_id}.json`
   - Future option: SQLite database at `~/.cache/kodelet/conversations.db`

2. Define a `ConversationRecord` structure in the `pkg/conversations` package:
   ```go
   // pkg/conversations/conversation.go
   type ConversationRecord struct {
     ID        string                 `json:"id"`
     Messages  []types.Message        `json:"messages"`
     Usage     types.Usage            `json:"usage"`
     Summary   string                 `json:"summary,omitempty"`
     CreatedAt time.Time              `json:"createdAt"`
     UpdatedAt time.Time              `json:"updatedAt"`
     Metadata  map[string]interface{} `json:"metadata,omitempty"`
   }
   ```

3. Implement persistence logic:
   - Save conversation after each message exchange
   - Use atomic file operations to prevent corruption
   - Include conversation metadata (timestamp, model used, etc.)

4. Add CLI commands for conversation management:
   - `kodelet chat --resume <conversation_id>`: Resume a specific conversation
   - `kodelet chat --list`: List available conversations with a brief summary
   - `kodelet chat --delete <conversation_id>`: Delete a specific conversation
   - `kodelet chat --storage <json|sqlite>`: Specify storage backend (optional, defaults to "json")

5. Enhance the TUI to show the current conversation ID and persistence status

## Consequences

### Positive
- Users can continue conversations across multiple sessions
- Historical context is preserved, improving continuity
- Enables future features like conversation search and analysis
- Improves user experience with long-running projects
- Provides conversation management capabilities

### Negative
- Increases storage requirements on the client machine
- Adds complexity to the codebase
- Potential privacy concerns with storing conversation data
- Need for data migration strategy if conversation format changes

### Neutral
- May require adjustments to the existing Thread abstraction
- Requires consideration of data retention policies

## Implementation Plan

1. Create a new package `pkg/conversations` with a storage-agnostic design:
   ```
   pkg/
    └── conversations/
        ├── conversation.go      # ConversationRecord and related types
        ├── store.go             # ConversationStore interface
        ├── json_store.go        # JSON file implementation
        ├── sqlite_store.go      # Future SQLite implementation (placeholder)
        └── factory.go           # Factory for creating store implementations
   ```

2. Design a flexible `ConversationStore` interface that accommodates both storage options:
   ```go
   // pkg/conversations/store.go
   type ConversationStore interface {
     // Basic CRUD operations
     Save(record ConversationRecord) error
     Load(id string) (ConversationRecord, error)
     List() ([]ConversationSummary, error)
     Delete(id string) error
     
     // Advanced query operations (for future expansion)
     Query(options QueryOptions) ([]ConversationSummary, error)
     
     // Lifecycle methods
     Close() error
   }
   
   // Query options to support future advanced filtering
   type QueryOptions struct {
     StartDate   *time.Time
     EndDate     *time.Time
     SearchTerm  string
     Limit       int
     Offset      int
     SortBy      string
     SortOrder   string
   }
   ```

3. Implement the JSON file-based store first:
   ```go
   // pkg/conversations/json_store.go
   type JSONConversationStore struct {
     basePath string
   }
   
   func NewJSONConversationStore(basePath string) (*JSONConversationStore, error) {
     // Create directory if it doesn't exist
     // Return store instance
   }
   
   // Implement ConversationStore interface methods
   ```

4. Create a factory function for store creation:
   ```go
   // pkg/conversations/factory.go
   func NewConversationStore(config Config) (ConversationStore, error) {
     switch config.StoreType {
     case "json":
       return NewJSONConversationStore(config.BasePath)
     case "sqlite":
       return nil, errors.New("SQLite store not yet implemented")
     default:
       return NewJSONConversationStore(config.BasePath)
     }
   }
   ```

5. Modify the Thread implementation to persist conversations:
   - Create a new conversation ID on thread initialization if not provided
   - Save conversation state after each message exchange using the ConversationStore
   - Load conversation state when resuming

3. Implement CLI command handlers:
   - Add `--resume` flag to the chat command
   - Implement the `--list` command with tabular output
   - Add the `--delete` command with confirmation

4. Update the TUI to show conversation persistence status:
   - Display conversation ID in the status bar
   - Show save status indicators

5. Add unit and integration tests for the new `pkg/conversations` package:
   - Test filesystem operations with mock filesystem
   - Test serialization/deserialization of conversation records
   - Test integration with Thread implementation
   - Test CLI command handlers

## Storage Options Comparison

### JSON Files (Proposed)

#### Pros
- Simple implementation with no external dependencies
- Native Go serialization/deserialization
- Easy to read/debug manually
- Simple backup/migration strategy
- Fits with the lightweight nature of Kodelet
- Straightforward file handling for CRUD operations

#### Cons
- Limited querying capabilities
- Less robust for concurrent access
- No built-in indexing
- Potentially less efficient for large datasets
- Need to implement search manually

### SQLite Database (Alternative)

#### Pros
- SQL querying capabilities (filtering, sorting, advanced search)
- ACID compliance (atomic, consistent, isolated, durable)
- Better performance for larger datasets
- Built-in support for concurrent access
- Single file database rather than multiple files
- Support for full-text search and indexing

#### Cons
- Additional dependency for the project
- Slightly more complex implementation
- More overhead for simple use cases
- Requires managing database connections
- Requires SQLite libraries/CGO

## Recommendation

For the initial implementation, **JSON files** are recommended because:
1. They align with Kodelet's lightweight, dependency-minimal philosophy
2. The expected data volume for most users is relatively small
3. Simple implementation allows for faster delivery
4. No additional dependencies required

However, the architecture should be designed with a storage interface that would allow migrating to SQLite in the future if advanced querying and scaling become necessary.

## Other Alternatives Considered

1. **Server-side persistence**:
   - Store conversations on a server instead of locally
   - Rejected due to privacy concerns and added complexity of server management

2. **Conversation summarization**:
   - Automatically generate summaries of conversations
   - Partially accepted as an optional feature, not required for initial implementation

3. **No persistence**:
   - Continue with the current ephemeral conversation model
   - Rejected because persistence offers significant user experience improvements