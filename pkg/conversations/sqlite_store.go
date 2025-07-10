package conversations

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	_ "modernc.org/sqlite"
)

// SQLiteConversationStore implements ConversationStore using SQLite database
type SQLiteConversationStore struct {
	dbPath string
	db     *sql.DB
}

// NewSQLiteConversationStore creates a new SQLite-based conversation store
func NewSQLiteConversationStore(ctx context.Context, dbPath string) (*SQLiteConversationStore, error) {
	// Create directory if needed
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, errors.Wrap(err, "failed to create database directory")
	}

	// Open SQLite database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open database")
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, errors.Wrap(err, "failed to ping database")
	}

	// Configure database for optimal WAL mode performance
	if err := configureDatabase(ctx, db); err != nil {
		db.Close()
		return nil, errors.Wrap(err, "failed to configure database")
	}

	store := &SQLiteConversationStore{
		dbPath: dbPath,
		db:     db,
	}

	// Initialize schema and run migrations
	if err := store.initializeSchema(); err != nil {
		db.Close()
		return nil, errors.Wrap(err, "failed to initialize schema")
	}

	return store, nil
}

// configureDatabase sets up SQLite pragmas for optimal WAL mode performance
func configureDatabase(ctx context.Context, db *sql.DB) error {
	// Configure database for optimal WAL mode performance
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=1000", 
		"PRAGMA temp_store=memory",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}

	for _, pragma := range pragmas {
		pragmaCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := db.ExecContext(pragmaCtx, pragma)
		cancel()
		if err != nil {
			return errors.Wrapf(err, "failed to execute pragma: %s", pragma)
		}
	}

	// Verify WAL mode is enabled
	var journalMode string
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := db.QueryRowContext(queryCtx, "PRAGMA journal_mode").Scan(&journalMode)
	cancel()
	if err != nil {
		return errors.Wrap(err, "failed to query journal mode")
	}

	if strings.ToLower(journalMode) != "wal" {
		return errors.Errorf("WAL mode not enabled. Current mode: %s", journalMode)
	}

	return nil
}

// verifyDatabaseConfiguration checks if the database is properly configured
func verifyDatabaseConfiguration(db *sql.DB) error {
	// Check journal mode
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return errors.Wrap(err, "failed to query journal mode")
	}
	if strings.ToLower(journalMode) != "wal" {
		return errors.Errorf("expected WAL mode, got %s", journalMode)
	}

	// Check synchronous mode
	var synchronous string
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		return errors.Wrap(err, "failed to query synchronous mode")
	}
	if synchronous != "1" { // NORMAL = 1
		return errors.Errorf("expected NORMAL synchronous mode, got %s", synchronous)
	}

	// Check foreign keys
	var foreignKeys string
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return errors.Wrap(err, "failed to query foreign keys")
	}
	if foreignKeys != "1" { // ON = 1
		return errors.Errorf("expected foreign keys ON, got %s", foreignKeys)
	}

	return nil
}

// initializeSchema creates the database schema and runs migrations
func (s *SQLiteConversationStore) initializeSchema() error {
	// Run migrations
	if err := s.runMigrations(); err != nil {
		return errors.Wrap(err, "failed to run migrations")
	}

	return nil
}

// Save stores a conversation record using atomic transactions
func (s *SQLiteConversationStore) Save(record ConversationRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	// Convert complex types to JSON for storage
	rawMessagesStr := string(record.RawMessages)
	
	fileLastAccessJSON, err := json.Marshal(record.FileLastAccess)
	if err != nil {
		return errors.Wrap(err, "failed to marshal file last access")
	}

	usageJSON, err := json.Marshal(record.Usage)
	if err != nil {
		return errors.Wrap(err, "failed to marshal usage")
	}

	metadataJSON, err := json.Marshal(record.Metadata)
	if err != nil {
		return errors.Wrap(err, "failed to marshal metadata")
	}

	toolResultsJSON, err := json.Marshal(record.ToolResults)
	if err != nil {
		return errors.Wrap(err, "failed to marshal tool results")
	}

	// Insert or update conversation record
	query := `
		INSERT OR REPLACE INTO conversations (
			id, raw_messages, model_type, file_last_access, usage, 
			summary, created_at, updated_at, metadata, tool_results
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = tx.Exec(query, record.ID, rawMessagesStr, record.ModelType, string(fileLastAccessJSON), 
		string(usageJSON), record.Summary, record.CreatedAt.Format(time.RFC3339Nano), 
		record.UpdatedAt.Format(time.RFC3339Nano), string(metadataJSON), string(toolResultsJSON))
	if err != nil {
		return errors.Wrap(err, "failed to save conversation record")
	}

	// Create summary for denormalized table
	summary := record.ToSummary()
	summaryUsageJSON, err := json.Marshal(summary.Usage)
	if err != nil {
		return errors.Wrap(err, "failed to marshal summary usage")
	}

	// Insert or update conversation summary
	summaryQuery := `
		INSERT OR REPLACE INTO conversation_summaries (
			id, message_count, first_message, summary, usage, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err = tx.Exec(summaryQuery, summary.ID, summary.MessageCount, summary.FirstMessage, 
		summary.Summary, string(summaryUsageJSON), summary.CreatedAt.Format(time.RFC3339Nano), 
		summary.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return errors.Wrap(err, "failed to save conversation summary")
	}

	return tx.Commit()
}

// Load retrieves a conversation record by ID
func (s *SQLiteConversationStore) Load(id string) (ConversationRecord, error) {
	var record ConversationRecord
	var rawMessages, fileLastAccess, usage, metadata, toolResults string
	var createdAt, updatedAt string

	query := `
		SELECT id, raw_messages, model_type, file_last_access, usage, 
			   summary, created_at, updated_at, metadata, tool_results
		FROM conversations 
		WHERE id = ?
	`

	row := s.db.QueryRow(query, id)
	err := row.Scan(&record.ID, &rawMessages, &record.ModelType, &fileLastAccess, 
		&usage, &record.Summary, &createdAt, &updatedAt, &metadata, &toolResults)
	if err != nil {
		if err == sql.ErrNoRows {
			return record, errors.Errorf("conversation not found: %s", id)
		}
		return record, errors.Wrap(err, "failed to load conversation record")
	}

	// Parse JSON fields
	record.RawMessages = json.RawMessage(rawMessages)

	if err := json.Unmarshal([]byte(fileLastAccess), &record.FileLastAccess); err != nil {
		return record, errors.Wrap(err, "failed to unmarshal file last access")
	}

	if err := json.Unmarshal([]byte(usage), &record.Usage); err != nil {
		return record, errors.Wrap(err, "failed to unmarshal usage")
	}

	if err := json.Unmarshal([]byte(metadata), &record.Metadata); err != nil {
		return record, errors.Wrap(err, "failed to unmarshal metadata")
	}

	if err := json.Unmarshal([]byte(toolResults), &record.ToolResults); err != nil {
		return record, errors.Wrap(err, "failed to unmarshal tool results")
	}

	// Parse timestamps
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return record, errors.Wrap(err, "failed to parse created at timestamp")
	}

	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return record, errors.Wrap(err, "failed to parse updated at timestamp")
	}

	return record, nil
}

// List returns all conversation summaries sorted by creation time (newest first)
func (s *SQLiteConversationStore) List() ([]ConversationSummary, error) {
	var summaries []ConversationSummary

	query := `
		SELECT id, message_count, first_message, summary, usage, created_at, updated_at
		FROM conversation_summaries
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query conversation summaries")
	}
	defer rows.Close()

	for rows.Next() {
		var summary ConversationSummary
		var usage, createdAt, updatedAt string

		err := rows.Scan(&summary.ID, &summary.MessageCount, &summary.FirstMessage, 
			&summary.Summary, &usage, &createdAt, &updatedAt)
		if err != nil {
			// Skip corrupted entries
			continue
		}

		// Parse JSON usage
		if err := json.Unmarshal([]byte(usage), &summary.Usage); err != nil {
			continue
		}

		// Parse timestamps
		summary.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			continue
		}

		summary.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			continue
		}

		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "error iterating rows")
	}

	return summaries, nil
}

// Delete removes a conversation and its associated data
func (s *SQLiteConversationStore) Delete(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	// Delete from conversations table
	_, err = tx.Exec("DELETE FROM conversations WHERE id = ?", id)
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation record")
	}

	// Delete from conversation_summaries table
	_, err = tx.Exec("DELETE FROM conversation_summaries WHERE id = ?", id)
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation summary")
	}

	return tx.Commit()
}

// Query performs advanced queries with filtering, sorting, and pagination
func (s *SQLiteConversationStore) Query(options QueryOptions) (QueryResult, error) {
	var args []interface{}
	var conditions []string

	// Build WHERE clause
	if options.StartDate != nil {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, options.StartDate.Format(time.RFC3339Nano))
	}

	if options.EndDate != nil {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, options.EndDate.Format(time.RFC3339Nano))
	}

	if options.SearchTerm != "" {
		searchPattern := "%" + strings.ToLower(options.SearchTerm) + "%"
		conditions = append(conditions, "(LOWER(first_message) LIKE ? OR LOWER(summary) LIKE ?)")
		args = append(args, searchPattern, searchPattern)
	}

	// Build ORDER BY clause
	sortBy := "created_at"
	if options.SortBy != "" {
		switch options.SortBy {
		case "createdAt":
			sortBy = "created_at"
		case "updatedAt":
			sortBy = "updated_at"
		case "messageCount":
			sortBy = "message_count"
		default:
			sortBy = "created_at"
		}
	}

	sortOrder := "DESC"
	if options.SortOrder == "asc" {
		sortOrder = "ASC"
	}

	// Build complete query
	query := `
		SELECT id, message_count, first_message, summary, usage, created_at, updated_at
		FROM conversation_summaries
	`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY " + sortBy + " " + sortOrder

	// Add LIMIT and OFFSET if specified
	if options.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, options.Limit)
		
		if options.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, options.Offset)
		}
	}

	// Execute query
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return QueryResult{}, errors.Wrap(err, "failed to execute query")
	}
	defer rows.Close()

	var summaries []ConversationSummary
	for rows.Next() {
		var summary ConversationSummary
		var usage, createdAt, updatedAt string

		err := rows.Scan(&summary.ID, &summary.MessageCount, &summary.FirstMessage, 
			&summary.Summary, &usage, &createdAt, &updatedAt)
		if err != nil {
			continue
		}

		// Parse JSON usage
		if err := json.Unmarshal([]byte(usage), &summary.Usage); err != nil {
			continue
		}

		// Parse timestamps
		summary.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			continue
		}

		summary.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			continue
		}

		summaries = append(summaries, summary)
	}

	// Get total count (without pagination)
	totalQuery := `SELECT COUNT(*) FROM conversation_summaries`
	var totalArgs []interface{}
	if len(conditions) > 0 {
		totalQuery += " WHERE " + strings.Join(conditions, " AND ")
		// Calculate how many args are for conditions (exclude LIMIT/OFFSET)
		conditionsArgsCount := len(args)
		if options.Limit > 0 {
			conditionsArgsCount--
			if options.Offset > 0 {
				conditionsArgsCount--
			}
		}
		totalArgs = args[:conditionsArgsCount]
	}

	var total int
	err = s.db.QueryRow(totalQuery, totalArgs...).Scan(&total)
	if err != nil {
		return QueryResult{}, errors.Wrap(err, "failed to get total count")
	}

	return QueryResult{
		ConversationSummaries: summaries,
		Total:                 total,
		QueryOptions:          options,
	}, nil
}

// Close closes the database connection
func (s *SQLiteConversationStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}