package conversations

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db"
)

// NewConversationStore returns a conversation store backed by database. The
// caller owns database; closing the store does not close it.
func NewConversationStore(database *db.DB) ConversationStore {
	return sqlite.NewStore(database)
}

// GetConversationStore opens a conversation store with its own handle on the
// default database, which closing the store releases.
func GetConversationStore(ctx context.Context) (ConversationStore, error) {
	config, err := DefaultConfig()
	if err != nil {
		return nil, err
	}

	// Check for environment variable override
	if storeType := os.Getenv("KODELET_CONVERSATION_STORE_TYPE"); storeType != "" {
		config.StoreType = storeType
	}

	store, err := sqlite.OpenStore(ctx, filepath.Join(config.BasePath, "storage.db"))
	if err != nil {
		return nil, err
	}
	return store, nil
}
