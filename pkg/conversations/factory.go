package conversations

import (
	"errors"
	"fmt"
	"os"
)

// NewConversationStore creates the appropriate ConversationStore implementation
// based on the provided configuration
func NewConversationStore(config *Config) (ConversationStore, error) {
	if config == nil {
		// Use default config if none provided
		var err error
		config, err = DefaultConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
	}

	switch config.StoreType {
	case "json":
		return NewJSONConversationStore(config.BasePath)
	case "sqlite":
		return nil, errors.New("SQLite store not yet implemented")
	default:
		// Default to JSON store with watcher for better performance
		return NewJSONConversationStore(config.BasePath)
	}
}

// GetConversationStore is a convenience function that creates a store
// with default configuration
func GetConversationStore() (ConversationStore, error) {
	config, err := DefaultConfig()
	if err != nil {
		return nil, err
	}

	// Check for environment variable override
	if storeType := os.Getenv("KODELET_CONVERSATION_STORE_TYPE"); storeType != "" {
		config.StoreType = storeType
	}

	return NewConversationStore(config)
}
