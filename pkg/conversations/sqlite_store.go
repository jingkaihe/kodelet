package conversations

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
	_ "modernc.org/sqlite"
)

// SQLiteConversationStore implements ConversationStore using SQLite database
type SQLiteConversationStore struct {
	dbPath string
	db     *sqlx.DB
}

// NewSQLiteConversationStore creates a new SQLite-based conversation store
func NewSQLiteConversationStore(ctx context.Context, dbPath string) (*SQLiteConversationStore, error) {
	// Create directory if needed
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, errors.Wrap(err, "failed to create database directory")
	}

	// Open SQLite database
	db, err := sqlx.Open("sqlite", dbPath)
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
func configureDatabase(ctx context.Context, db *sqlx.DB) error {
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
		_, err := db.ExecContext(ctx, pragma)
		if err != nil {
			return errors.Wrapf(err, "failed to execute pragma: %s", pragma)
		}
	}
	db.SetMaxIdleConns(1)
	db.SetMaxOpenConns(1)
	// Verify WAL mode is enabled
	var journalMode string
	err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		return errors.Wrap(err, "failed to query journal mode")
	}

	if strings.ToLower(journalMode) != "wal" {
		return errors.Errorf("WAL mode not enabled. Current mode: %s", journalMode)
	}

	return nil
}

// verifyDatabaseConfiguration checks if the database is properly configured
func verifyDatabaseConfiguration(db *sqlx.DB) error {
	// Check journal mode
	var journalMode string
	if err := db.Get(&journalMode, "PRAGMA journal_mode"); err != nil {
		return errors.Wrap(err, "failed to query journal mode")
	}
	if strings.ToLower(journalMode) != "wal" {
		return errors.Errorf("expected WAL mode, got %s", journalMode)
	}

	// Check synchronous mode
	var synchronous string
	if err := db.Get(&synchronous, "PRAGMA synchronous"); err != nil {
		return errors.Wrap(err, "failed to query synchronous mode")
	}
	if synchronous != "1" { // NORMAL = 1
		return errors.Errorf("expected NORMAL synchronous mode, got %s", synchronous)
	}

	// Check foreign keys
	var foreignKeys string
	if err := db.Get(&foreignKeys, "PRAGMA foreign_keys"); err != nil {
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

func (s *SQLiteConversationStore) Save(ctx context.Context, record ConversationRecord) error {

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	// Convert to database models
	dbRecord := FromConversationRecord(record)
	dbSummary := FromConversationSummary(record.ToSummary())

	// Insert or update conversation record
	conversationQuery := `
		INSERT OR REPLACE INTO conversations (
			id, raw_messages, model_type, file_last_access, usage,
			summary, created_at, updated_at, metadata, tool_results
		) VALUES (
			:id, :raw_messages, :model_type, :file_last_access, :usage,
			:summary, :created_at, :updated_at, :metadata, :tool_results
		)
	`
	_, err = tx.NamedExecContext(ctx, conversationQuery, dbRecord)
	if err != nil {
		return errors.Wrap(err, "failed to save conversation record")
	}

	// Insert or update conversation summary
	summaryQuery := `
		INSERT OR REPLACE INTO conversation_summaries (
			id, message_count, first_message, summary, usage, created_at, updated_at
		) VALUES (
			:id, :message_count, :first_message, :summary, :usage, :created_at, :updated_at
		)
	`
	_, err = tx.NamedExecContext(ctx, summaryQuery, dbSummary)
	if err != nil {
		return errors.Wrap(err, "failed to save conversation summary")
	}

	return tx.Commit()
}

// Load retrieves a conversation record by ID
func (s *SQLiteConversationStore) Load(ctx context.Context, id string) (ConversationRecord, error) {

	var dbRecord dbConversationRecord

	query := "SELECT * FROM conversations WHERE id = ?"
	err := s.db.GetContext(ctx, &dbRecord, query, id)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return ConversationRecord{}, errors.Errorf("conversation not found: %s", id)
		}
		return ConversationRecord{}, errors.Wrap(err, "failed to load conversation record")
	}

	return dbRecord.ToConversationRecord(), nil
}

// List returns all conversation summaries sorted by creation time (newest first)
func (s *SQLiteConversationStore) List(ctx context.Context) ([]ConversationSummary, error) {

	var dbSummaries []dbConversationSummary

	query := "SELECT * FROM conversation_summaries ORDER BY created_at DESC"
	err := s.db.SelectContext(ctx, &dbSummaries, query)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query conversation summaries")
	}

	// Convert to domain models
	summaries := make([]ConversationSummary, len(dbSummaries))
	for i, dbSummary := range dbSummaries {
		summaries[i] = dbSummary.ToConversationSummary()
	}

	return summaries, nil
}

// Delete removes a conversation and its associated data
func (s *SQLiteConversationStore) Delete(ctx context.Context, id string) error {

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	// Delete from both tables
	_, err = tx.ExecContext(ctx, "DELETE FROM conversations WHERE id = ?", id)
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation record")
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM conversation_summaries WHERE id = ?", id)
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation summary")
	}

	return tx.Commit()
}

// Query performs advanced queries with filtering, sorting, and pagination
func (s *SQLiteConversationStore) Query(ctx context.Context, options QueryOptions) (QueryResult, error) {

	// Build WHERE conditions
	conditions := []string{}
	args := map[string]interface{}{}

	if options.StartDate != nil {
		conditions = append(conditions, "created_at >= :start_date")
		args["start_date"] = *options.StartDate
	}

	if options.EndDate != nil {
		conditions = append(conditions, "created_at <= :end_date")
		args["end_date"] = *options.EndDate
	}

	if options.SearchTerm != "" {
		searchPattern := "%" + strings.ToLower(options.SearchTerm) + "%"
		conditions = append(conditions, "(LOWER(first_message) LIKE :search_term OR LOWER(summary) LIKE :search_term)")
		args["search_term"] = searchPattern
	}

	// Build ORDER BY clause
	sortBy := "created_at"
	switch options.SortBy {
	case "createdAt":
		sortBy = "created_at"
	case "updatedAt":
		sortBy = "updated_at"
	case "messageCount":
		sortBy = "message_count"
	}

	sortOrder := "DESC"
	if options.SortOrder == "asc" {
		sortOrder = "ASC"
	}

	// Build main query
	baseQuery := "SELECT * FROM conversation_summaries"
	if len(conditions) > 0 {
		baseQuery += " WHERE " + strings.Join(conditions, " AND ")
	}
	baseQuery += " ORDER BY " + sortBy + " " + sortOrder

	// Add pagination
	if options.Limit > 0 {
		baseQuery += " LIMIT :limit"
		args["limit"] = options.Limit

		if options.Offset > 0 {
			baseQuery += " OFFSET :offset"
			args["offset"] = options.Offset
		}
	}

	// Execute main query
	var dbSummaries []dbConversationSummary
	finalQuery, argsSlice, err := sqlx.Named(baseQuery, args)
	if err != nil {
		return QueryResult{}, errors.Wrap(err, "failed to build named query")
	}

	finalQuery = s.db.Rebind(finalQuery)
	err = s.db.SelectContext(ctx, &dbSummaries, finalQuery, argsSlice...)
	if err != nil {
		return QueryResult{}, errors.Wrap(err, "failed to execute query")
	}

	// Convert to domain models
	summaries := make([]ConversationSummary, len(dbSummaries))
	for i, dbSummary := range dbSummaries {
		summaries[i] = dbSummary.ToConversationSummary()
	}

	// Get total count (without pagination)
	countQuery := "SELECT COUNT(*) FROM conversation_summaries"
	if len(conditions) > 0 {
		countQuery += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Remove pagination args for count query
	countArgs := make(map[string]interface{})
	for k, v := range args {
		if k != "limit" && k != "offset" {
			countArgs[k] = v
		}
	}

	var total int
	finalCountQuery, countArgsSlice, err := sqlx.Named(countQuery, countArgs)
	if err != nil {
		return QueryResult{}, errors.Wrap(err, "failed to build named count query")
	}

	finalCountQuery = s.db.Rebind(finalCountQuery)
	err = s.db.GetContext(ctx, &total, finalCountQuery, countArgsSlice...)
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
