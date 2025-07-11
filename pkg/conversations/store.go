package conversations

import (
	"context"
	"errors"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
)

// ConversationStore defines the interface for conversation persistence
type ConversationStore interface {
	// Basic CRUD operations
	Save(ctx context.Context, record conversations.ConversationRecord) error
	Load(ctx context.Context, id string) (conversations.ConversationRecord, error)
	Delete(ctx context.Context, id string) error

	// Advanced query operations
	Query(ctx context.Context, options conversations.QueryOptions) (conversations.QueryResult, error)

	// Lifecycle methods
	Close() error // Close doesn't need context
}

// Config holds configuration for the conversation store
type Config struct {
	StoreType string // "sqlite"
	BasePath  string // Base storage path
}

// DefaultConfig returns a default configuration
func DefaultConfig() (*Config, error) {
	basePath, err := conversations.GetDefaultBasePath()
	if err != nil {
		return nil, err
	}

	return &Config{
		StoreType: "sqlite", // SQLite store is now the default
		BasePath:  basePath,
	}, nil
}

// GetMostRecentConversationID returns the ID of the most recent conversation
func GetMostRecentConversationID(ctx context.Context) (string, error) {
	store, err := GetConversationStore(ctx)
	if err != nil {
		return "", err
	}
	defer store.Close()

	// Query for the most recent conversation
	options := conversations.QueryOptions{
		Limit:     1,
		Offset:    0,
		SortBy:    "updated_at",
		SortOrder: "desc",
	}

	result, err := store.Query(ctx, options)
	if err != nil {
		return "", err
	}

	summaries := result.ConversationSummaries
	if len(summaries) == 0 {
		return "", errors.New("no conversations found")
	}

	return summaries[0].ID, nil
}
