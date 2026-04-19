package conversations

import (
	"context"
	"os"
	"path/filepath"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
)

// NewConversationStore creates the appropriate ConversationStore implementation
// based on the provided configuration
func NewConversationStore(ctx context.Context, config *Config) (ConversationStore, error) {
	if config == nil {
		// Use default config if none provided
		var err error
		config, err = DefaultConfig()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create default config")
		}
	}

	dbPath := filepath.Join(config.BasePath, "storage.db")

	switch config.StoreType {
	case "sqlite":
		return newSQLiteConversationStore(ctx, dbPath)
	default:
		// Default to SQLite store
		return newSQLiteConversationStore(ctx, dbPath)
	}
}

func newSQLiteConversationStore(ctx context.Context, dbPath string) (ConversationStore, error) {
	store, err := sqlite.NewStore(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// GetConversationStore is a convenience function that creates a store
// with default configuration
func GetConversationStore(ctx context.Context) (ConversationStore, error) {
	config, err := DefaultConfig()
	if err != nil {
		return nil, err
	}

	// Check for environment variable override
	if storeType := os.Getenv("KODELET_CONVERSATION_STORE_TYPE"); storeType != "" {
		config.StoreType = storeType
	}

	return NewConversationStore(ctx, config)
}
