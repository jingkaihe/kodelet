// Package sqlite provides SQLite-specific implementation for conversation storage.
// It implements the ConversationStore interface using SQLite database with
// optimized WAL mode configuration, schema migrations, and efficient querying.
package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/types/conversations"
)

// Store implements ConversationStore using SQLite database
type Store struct {
	dbPath string
	db     *sqlx.DB
}

// NewStore creates a new SQLite-based conversation store
func NewStore(ctx context.Context, dbPath string) (*Store, error) {
	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	store := &Store{
		dbPath: dbPath,
		db:     sqlDB,
	}

	if err := store.initializeSchema(); err != nil {
		sqlDB.Close()
		return nil, errors.Wrap(err, "failed to initialize schema")
	}

	return store, nil
}

// initializeSchema creates the database schema and runs migrations
func (s *Store) initializeSchema() error {
	// Run migrations
	if err := s.runMigrations(); err != nil {
		return errors.Wrap(err, "failed to run migrations")
	}

	return nil
}

// Save persists a conversation record to the database using UPSERT to preserve created_at timestamps
func (s *Store) Save(ctx context.Context, record conversations.ConversationRecord) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	// Ensure UpdatedAt is set to current time for saves
	record.UpdatedAt = time.Now()

	// Convert to database models
	dbRecord := fromConversationRecord(record)
	dbSummary := fromConversationSummary(record.ToSummary())

	// Insert or update conversation record with UPSERT to preserve created_at
	conversationQuery := `
		INSERT INTO conversations (
			id, raw_messages, provider, file_last_access, usage,
			summary, created_at, updated_at, metadata, tool_results, background_processes
		) VALUES (
			:id, :raw_messages, :provider, :file_last_access, :usage,
			:summary, :created_at, :updated_at, :metadata, :tool_results, :background_processes
		)
		ON CONFLICT(id) DO UPDATE SET
			raw_messages = excluded.raw_messages,
			provider = excluded.provider,
			file_last_access = excluded.file_last_access,
			usage = excluded.usage,
			summary = excluded.summary,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata,
			tool_results = excluded.tool_results,
			background_processes = excluded.background_processes
	`
	_, err = tx.NamedExecContext(ctx, conversationQuery, dbRecord)
	if err != nil {
		return errors.Wrap(err, "failed to save conversation record")
	}

	// Insert or update conversation summary with UPSERT to preserve created_at
	summaryQuery := `
		INSERT INTO conversation_summaries (
			id, message_count, first_message, summary, provider, usage, created_at, updated_at
		) VALUES (
			:id, :message_count, :first_message, :summary, :provider, :usage, :created_at, :updated_at
		)
		ON CONFLICT(id) DO UPDATE SET
			message_count = excluded.message_count,
			first_message = excluded.first_message,
			summary = excluded.summary,
			provider = excluded.provider,
			usage = excluded.usage,
			updated_at = excluded.updated_at
	`
	_, err = tx.NamedExecContext(ctx, summaryQuery, dbSummary)
	if err != nil {
		return errors.Wrap(err, "failed to save conversation summary")
	}

	return tx.Commit()
}

// Load retrieves a conversation record by ID
func (s *Store) Load(ctx context.Context, id string) (conversations.ConversationRecord, error) {
	var dbRecord dbConversationRecord

	query := `SELECT id, raw_messages, provider, file_last_access, usage,
		summary, created_at, updated_at, metadata, tool_results, background_processes
		FROM conversations WHERE id = ?`
	err := s.db.GetContext(ctx, &dbRecord, query, id)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return conversations.ConversationRecord{}, errors.Errorf("conversation not found: %s", id)
		}
		return conversations.ConversationRecord{}, errors.Wrap(err, "failed to load conversation record")
	}

	return dbRecord.ToConversationRecord(), nil
}

// Delete removes a conversation and its associated data
func (s *Store) Delete(ctx context.Context, id string) error {
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
func (s *Store) Query(ctx context.Context, options conversations.QueryOptions) (conversations.QueryResult, error) {
	// Build WHERE conditions
	conditions := []string{}
	args := map[string]any{}

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

	if options.Provider != "" {
		conditions = append(conditions, "provider = :provider")
		args["provider"] = options.Provider
	}

	// Build ORDER BY clause
	sortBy := "updated_at"
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
	baseQuery := `SELECT id, message_count, first_message, summary, provider,
		usage, created_at, updated_at FROM conversation_summaries`
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
		return conversations.QueryResult{}, errors.Wrap(err, "failed to build named query")
	}

	finalQuery = s.db.Rebind(finalQuery)
	err = s.db.SelectContext(ctx, &dbSummaries, finalQuery, argsSlice...)
	if err != nil {
		return conversations.QueryResult{}, errors.Wrap(err, "failed to execute query")
	}

	// Convert to domain models
	summaries := make([]conversations.ConversationSummary, len(dbSummaries))
	for i, dbSummary := range dbSummaries {
		summaries[i] = dbSummary.ToConversationSummary()
	}

	// Get total count (without pagination)
	countQuery := "SELECT COUNT(*) FROM conversation_summaries"
	if len(conditions) > 0 {
		countQuery += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Remove pagination args for count query
	countArgs := make(map[string]any)
	for k, v := range args {
		if k != "limit" && k != "offset" {
			countArgs[k] = v
		}
	}

	var total int
	finalCountQuery, countArgsSlice, err := sqlx.Named(countQuery, countArgs)
	if err != nil {
		return conversations.QueryResult{}, errors.Wrap(err, "failed to build named count query")
	}

	finalCountQuery = s.db.Rebind(finalCountQuery)
	err = s.db.GetContext(ctx, &total, finalCountQuery, countArgsSlice...)
	if err != nil {
		return conversations.QueryResult{}, errors.Wrap(err, "failed to get total count")
	}

	return conversations.QueryResult{
		ConversationSummaries: summaries,
		Total:                 total,
		QueryOptions:          options,
	}, nil
}

// Close closes the database connection
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
