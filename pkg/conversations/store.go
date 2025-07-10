package conversations

import (
	"context"
	"time"
)

// QueryOptions provides filtering and sorting options for conversation queries
type QueryOptions struct {
	StartDate  *time.Time // Filter by start date
	EndDate    *time.Time // Filter by end date
	SearchTerm string     // Text to search for in messages
	Limit      int        // Maximum number of results
	Offset     int        // Offset for pagination
	SortBy     string     // Field to sort by
	SortOrder  string     // "asc" or "desc"
}

// ConversationStore defines the interface for conversation persistence
type ConversationStore interface {
	// Basic CRUD operations
	Save(ctx context.Context, record ConversationRecord) error
	Load(ctx context.Context, id string) (ConversationRecord, error)
	List(ctx context.Context) ([]ConversationSummary, error)
	Delete(ctx context.Context, id string) error

	// Advanced query operations
	Query(ctx context.Context, options QueryOptions) (QueryResult, error)

	// Lifecycle methods
	Close() error // Close doesn't need context
}

// Config holds configuration for the conversation store
type Config struct {
	StoreType string // "bbolt" or "sqlite"
	BasePath  string // Base storage path
}

// DefaultConfig returns a default configuration
func DefaultConfig() (*Config, error) {
	basePath, err := GetDefaultBasePath()
	if err != nil {
		return nil, err
	}

	return &Config{
		StoreType: "bbolt", // BBolt store is now the default
		BasePath:  basePath,
	}, nil
}
