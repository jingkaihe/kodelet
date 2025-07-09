# ADR 016: BoltDB Conversation Storage

## Status
Implemented

## Context
The current conversation persistence implementation uses JSON files stored in `~/.kodelet/conversations/`. While this works well for basic use cases, it has some limitations:

1. **File system overhead**: Each conversation is stored as a separate file, which can lead to filesystem clutter with many conversations
2. **Complex caching logic**: The JSON store implements an in-memory cache with file watching to improve performance
3. **Limited querying capabilities**: Searching and filtering conversations requires loading and parsing multiple files
4. **Potential race conditions**: Despite atomic writes, multiple kodelet instances could potentially cause issues

BoltDB (bbolt) is a pure Go key/value database that provides:
- Single file storage with efficient B+tree structure
- ACID transactions with full consistency guarantees
- Built-in support for buckets (namespacing)
- No external dependencies (pure Go)
- Excellent performance for read-heavy workloads
- Simple API that fits well with our key-value storage pattern

## Decision
We will implement a BoltDB-based conversation store and make it the default storage backend, while maintaining the JSON store as an alternative option.

**Multi-Process Support**: To address BoltDB's exclusive file locking limitation, we implemented an **operation-scoped database access pattern** where the database connection is opened, used, and closed for each individual operation rather than maintaining a persistent connection. This approach enables natural multi-process concurrency by minimizing lock duration to just the operation time (milliseconds).

### Implementation Details

1. **Storage location**:
   - Default: `$HOME/.kodelet/storage.db`
   - Database file created with 0600 permissions for security
   - Automatic directory creation if parent directories don't exist

2. **Optimized bucket structure** (hybrid approach for search performance):
   ```
   storage.db
   ├── conversations/          # Main bucket for full conversation records
   │   ├── 20240708T150405-abc123 → ConversationRecord{...}
   │   └── 20240708T160235-def456 → ConversationRecord{...}
   ├── summaries/             # Full summaries for efficient listing
   │   ├── conv:20240708T150405-abc123 → ConversationSummary{...}
   │   └── conv:20240708T160235-def456 → ConversationSummary{...}
   └── search_index/          # Search-optimized fields (no JSON unmarshaling)
       ├── msg:20240708T150405-abc123 → "How to implement..."
       ├── msg:20240708T160235-def456 → "Debug the error..."
       ├── sum:20240708T150405-abc123 → "Discussion about implementation..."
       └── sum:20240708T160235-def456 → "Troubleshooting session..."
   ```

   **Performance benefits:**
   - **List operations:** Read `conv:` prefixed keys, unmarshal once (same as current)
   - **Search operations:** Iterate `msg:` and `sum:` keys, no unmarshaling (**40x faster**)
   - **Storage overhead:** +97% but provides significant search performance gains
   - **Memory efficiency:** No temporary object allocation during search

3. **Key design**:
   - Conversation IDs use timestamp format: `20060102T150405-randomhex`
   - Natural lexicographic sorting by creation time
   - Efficient range queries using BoltDB's ordered key iteration
   - 64-bit timestamp prefix enables efficient date-based filtering

4. **Data format**:
   - Continue using JSON serialization within BoltDB values
   - Two-tier storage: full records + lightweight summaries
   - Maintains compatibility with existing conversation structures
   - Enables efficient list operations without loading full conversation data

5. **Query implementation strategies**:

   **a) List operations** (using `conv:` prefixed keys):
   ```go
   // Efficient listing using summaries bucket with conv: prefix
   summariesBucket := tx.Bucket([]byte("summaries"))
   cursor := summariesBucket.Cursor()
   prefix := []byte("conv:")

   for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {
       var summary ConversationSummary
       if err := json.Unmarshal(v, &summary); err != nil {
           continue
       }
       summaries = append(summaries, summary)
   }
   ```

   **b) Date-based filtering** (with `conv:` prefix):
   ```go
   // Leverage timestamp-based keys for efficient date ranges
   summariesBucket := tx.Bucket([]byte("summaries"))
   cursor := summariesBucket.Cursor()

   // Build prefixed keys for date range
   startKey := []byte("conv:" + startDate.Format("20060102T150405"))
   endKey := []byte("conv:" + endDate.Format("20060102T150405"))

   for k, v := cursor.Seek(startKey); k != nil && bytes.Compare(k, endKey) < 0; k, v = cursor.Next() {
       if !bytes.HasPrefix(k, []byte("conv:")) {
           continue
       }
       // Process conversations within date range
       var summary ConversationSummary
       json.Unmarshal(v, &summary)
       summaries = append(summaries, summary)
   }
   ```

   **c) Sorting and pagination**:
   ```go
   // Forward iteration for ascending order
   cursor := bucket.Cursor()
   for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
       // Process in chronological order
   }

   // Reverse iteration for descending order (most recent first)
   cursor := bucket.Cursor()
   for k, v := cursor.Last(); k != nil; k, v = cursor.Prev() {
       // Process in reverse chronological order
   }
   ```

   **d) Optimized search implementation** (40x faster):
   ```go
   // Ultra-fast search using search_index bucket (no JSON unmarshaling)
   func (s *BBoltConversationStore) searchConversations(searchTerm string) []string {
       var matchingIDs []string
       searchTermLower := strings.ToLower(searchTerm)

       searchBucket := tx.Bucket([]byte("search_index"))
       cursor := searchBucket.Cursor()

       // Search in first messages (msg: prefix)
       msgPrefix := []byte("msg:")
       for k, v := cursor.Seek(msgPrefix); k != nil && bytes.HasPrefix(k, msgPrefix); k, v = cursor.Next() {
           if strings.Contains(strings.ToLower(string(v)), searchTermLower) {
               // Extract conversation ID from key: msg:20240708T150405-abc123
               conversationID := string(k[4:]) // Remove "msg:" prefix
               matchingIDs = append(matchingIDs, conversationID)
           }
       }

       // Search in summaries (sum: prefix)
       sumPrefix := []byte("sum:")
       for k, v := cursor.Seek(sumPrefix); k != nil && bytes.HasPrefix(k, sumPrefix); k, v = cursor.Next() {
           if strings.Contains(strings.ToLower(string(v)), searchTermLower) {
               conversationID := string(k[4:]) // Remove "sum:" prefix
               matchingIDs = append(matchingIDs, conversationID)
           }
       }

       // Deduplicate IDs and return
       return deduplicate(matchingIDs)
   }

   // Then retrieve full summaries for matching IDs
   func (s *BBoltConversationStore) getSummariesByIDs(tx *bbolt.Tx, ids []string) []ConversationSummary {
       var summaries []ConversationSummary
       summariesBucket := tx.Bucket([]byte("summaries"))

       for _, id := range ids {
           key := []byte("conv:" + id)
           if data := summariesBucket.Get(key); data != nil {
               var summary ConversationSummary
               if err := json.Unmarshal(data, &summary); err == nil {
                   summaries = append(summaries, summary)
               }
           }
       }

       return summaries
   }
   ```

6. **Performance optimizations**:
   - **Dual storage**: Full records + summaries for different access patterns
   - **Cursor-based iteration**: Efficient pagination without loading all data
   - **Key prefix scanning**: Fast date-range queries using timestamp-based keys
   - **Lazy loading**: Load full conversation data only when needed
   - **Read-only transactions**: Multiple concurrent readers for better performance

7. **Store implementation**:
   - Create `bbolt_store.go` implementing the `ConversationStore` interface
   - Use read-only transactions for queries to maximize concurrency
   - Use read-write transactions only for mutations (Save/Delete)
   - Implement proper error handling and resource cleanup
   - Add connection pooling and transaction management

8. **Configuration**:
   - Update default store type from "json" to "bbolt"
   - Support `KODELET_CONVERSATION_STORE_TYPE` environment variable
   - Add "bbolt" as a valid store type in the factory
   - Configurable database options (timeout, sync mode, etc.)

## Consequences

### Positive
- **Single file storage**: All conversations in one database file, easier to manage and backup
- **Better performance**: B+tree structure provides O(log n) lookups without needing in-memory caching
- **Built-in consistency**: ACID transactions eliminate potential race conditions
- **Simpler implementation**: No need for file watching or complex caching logic
- **Efficient queries**: Can iterate through conversations without loading all files
- **Reduced filesystem pressure**: One file instead of potentially thousands
- **Pure Go**: No CGO dependencies unlike SQLite

### Negative
- **Additional dependency**: Adds `go.etcd.io/bbolt` as a project dependency
- **Migration required**: Existing users will need to migrate from JSON files
- **Less human-readable**: Cannot directly view/edit conversations as text files
- **Database corruption risk**: Single file corruption could affect all conversations (mitigated by backups)

### Neutral
- **Similar query limitations**: BoltDB is still a key-value store, not a full SQL database
- **Debugging**: Requires bbolt CLI tool or custom tooling to inspect database contents

## Implementation Plan

### Phase 1: Core BBolt Store Implementation

1. **Add bbolt dependency**:
   ```bash
   go get go.etcd.io/bbolt
   ```

2. **Create bbolt store implementation** (`pkg/conversations/bbolt_store.go`):
   ```go
   type BBoltConversationStore struct {
       db             *bbolt.DB
       dbPath         string
       ctx            context.Context
       cancel         context.CancelFunc
   }

   func NewBBoltConversationStore(ctx context.Context, dbPath string) (*BBoltConversationStore, error) {
       // Create directory if needed
       dir := filepath.Dir(dbPath)
       if err := os.MkdirAll(dir, 0755); err != nil {
           return nil, fmt.Errorf("failed to create database directory: %w", err)
       }

       // Open database with appropriate options
       db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{
           Timeout: 1 * time.Second,
       })
       if err != nil {
           return nil, fmt.Errorf("failed to open database: %w", err)
       }

       // Create required buckets
       err = db.Update(func(tx *bbolt.Tx) error {
           if _, err := tx.CreateBucketIfNotExists([]byte("conversations")); err != nil {
               return err
           }
           if _, err := tx.CreateBucketIfNotExists([]byte("summaries")); err != nil {
               return err
           }
           if _, err := tx.CreateBucketIfNotExists([]byte("search_index")); err != nil {
               return err
           }
           return nil
       })
       if err != nil {
           db.Close()
           return nil, fmt.Errorf("failed to create buckets: %w", err)
       }

       storeCtx, cancel := context.WithCancel(ctx)
       return &BBoltConversationStore{
           db:     db,
           dbPath: dbPath,
           ctx:    storeCtx,
           cancel: cancel,
       }, nil
   }
   ```

3. **Implement core CRUD operations**:
   ```go
   // Save - optimized triple storage pattern
   func (s *BBoltConversationStore) Save(record ConversationRecord) error {
       return s.db.Update(func(tx *bbolt.Tx) error {
           // 1. Save full record
           conversationsBucket := tx.Bucket([]byte("conversations"))
           recordData, err := json.Marshal(record)
           if err != nil {
               return err
           }

           // 2. Save summary for efficient listing
           summariesBucket := tx.Bucket([]byte("summaries"))
           summary := record.ToSummary()
           summaryData, err := json.Marshal(summary)
           if err != nil {
               return err
           }

           // 3. Save search index fields (no JSON, raw strings)
           searchBucket := tx.Bucket([]byte("search_index"))

           // Atomic writes to all three buckets
           if err := conversationsBucket.Put([]byte(record.ID), recordData); err != nil {
               return err
           }
           if err := summariesBucket.Put([]byte("conv:"+record.ID), summaryData); err != nil {
               return err
           }
           if err := searchBucket.Put([]byte("msg:"+record.ID), []byte(summary.FirstMessage)); err != nil {
               return err
           }
           return searchBucket.Put([]byte("sum:"+record.ID), []byte(summary.Summary))
       })
   }

   // Load - single bucket read
   func (s *BBoltConversationStore) Load(id string) (ConversationRecord, error) {
       var record ConversationRecord
       err := s.db.View(func(tx *bbolt.Tx) error {
           bucket := tx.Bucket([]byte("conversations"))
           data := bucket.Get([]byte(id))
           if data == nil {
               return fmt.Errorf("conversation not found: %s", id)
           }
           return json.Unmarshal(data, &record)
       })
       return record, err
   }
   ```

4. **Implement efficient query operations**:
   ```go
   // Query - optimized for different access patterns
   func (s *BBoltConversationStore) Query(options QueryOptions) (QueryResult, error) {
       var summaries []ConversationSummary

       err := s.db.View(func(tx *bbolt.Tx) error {
           bucket := tx.Bucket([]byte("summaries"))
           cursor := bucket.Cursor()

           // Handle date filtering with key prefixes
           var startKey, endKey []byte
           if options.StartDate != nil {
               startKey = []byte(options.StartDate.Format("20060102T150405"))
           }
           if options.EndDate != nil {
               endKey = []byte(options.EndDate.Format("20060102T150405"))
           }

           // Iterate based on sort order
           var iterate func() ([]byte, []byte)
           if options.SortOrder == "asc" {
               k, v := cursor.First()
               if startKey != nil {
                   k, v = cursor.Seek(startKey)
               }
               iterate = func() ([]byte, []byte) {
                   defer func() { k, v = cursor.Next() }()
                   return k, v
               }
           } else {
               k, v := cursor.Last()
               if endKey != nil {
                   k, v = cursor.Seek(endKey)
                   if k != nil && bytes.Compare(k, endKey) >= 0 {
                       k, v = cursor.Prev()
                   }
               }
               iterate = func() ([]byte, []byte) {
                   defer func() { k, v = cursor.Prev() }()
                   return k, v
               }
           }

           // Process results with pagination
           offset := 0
           for k, v := iterate(); k != nil; {
               if endKey != nil && bytes.Compare(k, endKey) >= 0 {
                   break
               }

               var summary ConversationSummary
               if err := json.Unmarshal(v, &summary); err != nil {
                   continue
               }

               // Apply search filter
               if options.SearchTerm != "" {
                   if !s.matchesSearch(summary, options.SearchTerm, tx) {
                       continue
                   }
               }

               // Apply pagination
               if offset < options.Offset {
                   offset++
                   continue
               }

               if options.Limit > 0 && len(summaries) >= options.Limit {
                   break
               }

               summaries = append(summaries, summary)
           }

           return nil
       })

       return QueryResult{
           ConversationSummaries: summaries,
           Total:                 len(summaries),
           QueryOptions:          options,
       }, err
   }
   ```

### Phase 2: Factory and Configuration Updates

5. **Update factory** to support bbolt:
   ```go
   case "bbolt":
       dbPath := filepath.Join(config.BasePath, "storage.db")
       return NewBBoltConversationStore(ctx, dbPath)
   ```

6. **Update default configuration**:
   ```go
   return &Config{
       StoreType: "bbolt", // Changed from "json"
       BasePath:  basePath,
   }, nil
   ```

### Phase 3: Testing and Validation

7. **Comprehensive test suite**:
   - Unit tests for all ConversationStore interface methods
   - Concurrent access tests using goroutines
   - Performance benchmarks comparing JSON vs BoltDB
   - Data integrity tests with transaction rollbacks
   - Edge case testing (empty database, corrupted data)
   - Memory usage profiling

8. **Integration testing**:
   - Test with existing CLI commands
   - Verify compatibility with conversation management features
   - Test database recovery and corruption handling

### Phase 4: Migration and Deployment

9. **Migration tool implementation**:
   ```go
   // pkg/conversations/migrate.go
   func MigrateJSONToBBolt(ctx context.Context, jsonPath, dbPath string) error {
       // Read all JSON files
       // Create new BBolt store
       // Migrate conversations preserving all data
       // Validate migration completeness
   }
   ```

10. **Deployment strategy**:
    - Feature flag for gradual rollout
    - Automatic migration prompt on first run
    - Fallback to JSON store if migration fails
    - Documentation for manual migration process

## Migration Strategy

For existing users:
1. Kodelet will detect existing JSON conversations on first run
2. Offer to migrate conversations to the new bbolt format
3. Keep JSON files as backup until user confirms successful migration
4. Provide `kodelet conversation migrate` command for manual migration

## Performance Considerations

### Detailed Performance Analysis

**Current JSON Store:**
- **List:** Load all files + JSON unmarshal = ~5-10ms for 1000 conversations
- **Search:** Unmarshal all summaries + string search = ~2-3ms for 1000 conversations
- **Save:** File write + fsync = ~1-2ms per conversation

**Optimized BBolt Store:**
- **List:** Cursor iteration + JSON unmarshal = ~1-2ms for 1000 conversations (**3-5x faster**)
- **Search:** Raw string comparison, no unmarshaling = ~0.05ms for 1000 conversations (**40-60x faster**)
- **Save:** Single transaction, 3 bucket writes = ~0.5ms per conversation (**2-4x faster**)
- **Load:** Single B+tree lookup = ~0.01ms per conversation (**10-20x faster**)

### Storage Efficiency Trade-offs

**Storage overhead breakdown:**
- **JSON store:** 508 bytes per conversation
- **BBolt optimized:** 1004 bytes per conversation (+97% overhead)
- **Space vs Speed:** 2x storage for 40x search performance improvement

**Why the trade-off is worth it:**
- Storage is cheap (1GB can store ~1 million conversations)
- Search operations are frequent in user workflows
- List operations remain fast despite larger storage
- Modern SSDs make the additional I/O negligible

## Future Enhancements

The bbolt implementation opens possibilities for:
1. **Conversation indexing**: Secondary indices for faster queries
2. **Metadata buckets**: Separate buckets for tags, categories, etc.
3. **Compression**: Store compressed conversation data to save space
4. **Incremental backups**: Export only changed conversations

## Alternatives Considered

1. **SQLite**: More powerful querying but requires CGO
2. **BadgerDB**: Higher performance but larger dependency and more complex API
3. **Keep JSON only**: Simpler but doesn't address current limitations
4. **PostgreSQL/MySQL**: Overkill for a CLI tool, requires external service

BoltDB strikes the right balance between simplicity, performance, and features for Kodelet's needs.

## Multi-Process Access Solution

### Problem
BoltDB uses exclusive file locking, which prevents multiple processes from accessing the same database file simultaneously. This creates issues when users run multiple Kodelet CLI instances concurrently.

### Solution: Operation-Scoped Database Access
Instead of maintaining persistent database connections, we implemented an operation-scoped pattern:

```go
type BBoltConversationStore struct {
    dbPath string  // Only store path, not connection
}

func (s *BBoltConversationStore) withDB(operation func(*bbolt.DB) error) error {
    db, err := bbolt.Open(s.dbPath, 0600, &bbolt.Options{
        Timeout: 2 * time.Second,
    })
    if err != nil {
        return err
    }
    defer db.Close()  // Always close after operation

    return operation(db)
}

func (s *BBoltConversationStore) Save(record ConversationRecord) error {
    return s.withDB(func(db *bbolt.DB) error {
        return db.Update(func(tx *bbolt.Tx) error {
            // Save logic here
        })
    })
}
```

### Benefits
1. **Minimal Lock Duration**: Database only locked during actual operation (~1-10ms)
2. **Natural Concurrency**: Multiple CLI processes can interleave operations seamlessly
3. **Crash Resilience**: No stale locks if process crashes
4. **Simple Implementation**: No complex retry logic or coordination needed
5. **Performance**: Negligible overhead for opening/closing database connections

### Testing Results
Multi-process testing with 3 concurrent processes performing 5 operations each demonstrates:
- Zero lock conflicts or failures
- All 15 operations completed successfully
- Proper data consistency across all processes
- Natural interleaving of operations without coordination

## Migration Implementation

### Automatic Migration
- **Detection**: Automatically detects existing JSON conversations on first BBolt store access
- **User Prompt**: Prompts user for migration consent with clear information
- **Backup Creation**: Automatically creates timestamped backup of JSON files
- **Validation**: Performs comprehensive validation after migration
- **Fallback**: Gracefully handles migration failures without data loss

### Manual Migration Command
Users can manually trigger migration using the CLI command:

```bash
# Dry run to see what would be migrated
kodelet conversation migrate --dry-run --verbose

# Perform actual migration with backup
kodelet conversation migrate --verbose

# Force migration (overwrite existing)
kodelet conversation migrate --force --verbose

# Custom paths
kodelet conversation migrate \
  --json-path ~/old-conversations \
  --db-path ~/new-conversations.db \
  --backup-path ~/backup-conversations
```

### Migration Features
- **Batch Processing**: Loads all conversations at once and migrates them in a single BBolt transaction for maximum efficiency
- **Comprehensive validation**: Ensures data integrity by comparing JSON semantics (ignoring formatting)
- **Atomic operations**: Uses BBolt transactions for consistency
- **Progress reporting**: Detailed feedback during migration process
- **Error handling**: Continues migration on individual failures, reports all issues
- **Backup management**: Automatic backup creation with timestamp organization
- **Performance optimized**: Single database connection with bulk operations for improved speed

### Migration Safety
- **Non-destructive**: Original JSON files are preserved as backup
- **Validation**: Full data comparison between source and target
- **Rollback capability**: Users can revert to JSON store if needed
- **Error isolation**: Individual conversation failures don't affect overall migration
