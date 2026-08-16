// Package sqlite provides SQLite-specific implementation for conversation storage.
// It implements the ConversationStore interface using SQLite database with
// optimized WAL mode configuration, schema migrations, and efficient querying.
package sqlite

import (
	"context"
	"database/sql"
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

// NewStore creates a new SQLite-based conversation store.
// Note: Migrations should be run via db.RunMigrations() at CLI startup before calling this.
func NewStore(ctx context.Context, dbPath string) (*Store, error) {
	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	return &Store{
		dbPath: dbPath,
		db:     sqlDB,
	}, nil
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
			id, cwd, raw_messages, provider, usage,
			summary, created_at, updated_at, metadata, tool_results
		) VALUES (
			:id, :cwd, :raw_messages, :provider, :usage,
			:summary, :created_at, :updated_at, :metadata, :tool_results
		)
		ON CONFLICT(id) DO UPDATE SET
			cwd = excluded.cwd,
			raw_messages = excluded.raw_messages,
			provider = excluded.provider,
			usage = excluded.usage,
			summary = excluded.summary,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata,
			tool_results = excluded.tool_results
	`
	_, err = tx.NamedExecContext(ctx, conversationQuery, dbRecord)
	if err != nil {
		return errors.Wrap(err, "failed to save conversation record")
	}

	// Insert or update conversation summary with UPSERT to preserve created_at
	summaryQuery := `
		INSERT INTO conversation_summaries (
			id, cwd, message_count, first_message, summary, provider, metadata, usage, created_at, updated_at
		) VALUES (
			:id, :cwd, :message_count, :first_message, :summary, :provider, :metadata, :usage, :created_at, :updated_at
		)
		ON CONFLICT(id) DO UPDATE SET
			cwd = excluded.cwd,
			message_count = excluded.message_count,
			first_message = excluded.first_message,
			summary = excluded.summary,
			provider = excluded.provider,
			metadata = excluded.metadata,
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

	query := `SELECT id, cwd, raw_messages, provider, usage,
		summary, created_at, updated_at, metadata, tool_results
		FROM conversations WHERE id = ?`
	err := s.db.GetContext(ctx, &dbRecord, query, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return conversations.ConversationRecord{}, errors.Wrapf(conversations.ErrConversationNotFound, "%s", id)
		}
		return conversations.ConversationRecord{}, errors.Wrap(err, "failed to load conversation record")
	}

	record := dbRecord.ToConversationRecord()
	var affinity struct {
		RunnerID           string `db:"runner_id"`
		EnvironmentProfile string `db:"environment_profile"`
	}
	err = s.db.GetContext(ctx, &affinity, `
		SELECT runner_id, environment_profile
		FROM conversation_runner_affinity
		WHERE conversation_id = ?
	`, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return conversations.ConversationRecord{}, errors.Wrap(err, "failed to load conversation runner affinity")
	}
	if err == nil {
		if record.Metadata == nil {
			record.Metadata = make(map[string]any)
		}
		record.Metadata[conversations.RunnerIDMetadataKey] = affinity.RunnerID
		record.Metadata[conversations.RunnerEnvironmentProfileMetadataKey] = affinity.EnvironmentProfile
	}
	return record, nil
}

// Delete removes a conversation and its associated data
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM runner_runs WHERE conversation_id = ?", id)
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation runner run history")
	}

	// Runner affinity may exist before the first conversation record is saved, so it is
	// explicitly released only when central conversation deletion commits.
	_, err = tx.ExecContext(ctx, "DELETE FROM conversation_runner_affinity WHERE conversation_id = ?", id)
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation runner affinity")
	}

	// Delete from both conversation tables.
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

	if searchTerm := strings.TrimSpace(options.SearchTerm); searchTerm != "" {
		searchPattern := "%" + strings.ToLower(searchTerm) + "%"
		conditions = append(conditions, "(LOWER(id) LIKE :search_term OR LOWER(cwd) LIKE :search_term OR LOWER(first_message) LIKE :search_term OR LOWER(summary) LIKE :search_term)")
		args["search_term"] = searchPattern
	}

	if options.Provider != "" {
		conditions = append(conditions, "provider = :provider")
		args["provider"] = options.Provider
	}

	if options.CWD = strings.TrimSpace(options.CWD); options.CWD != "" {
		conditions = append(conditions, "cwd = :cwd")
		args["cwd"] = options.CWD
	}

	if options.RunnerID != "" {
		conditions = append(conditions, `EXISTS (
			SELECT 1 FROM conversation_runner_affinity affinity
			WHERE affinity.conversation_id = conversation_summaries.id
				AND affinity.runner_id = :runner_id
		)`)
		args["runner_id"] = options.RunnerID
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
	baseQuery := `SELECT id, cwd, message_count, first_message, summary, provider,
		metadata, usage, created_at, updated_at FROM conversation_summaries`
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
	if s == nil || s.db == nil {
		return nil
	}

	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
