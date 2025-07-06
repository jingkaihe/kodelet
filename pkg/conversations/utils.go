package conversations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// GenerateID creates a unique identifier for a conversation
func GenerateID() string {
	// Create a timestamp prefix for the ID
	timestamp := time.Now().UTC().Format("20060102T150405")

	// Add some randomness (8 random bytes = 16 hex chars)
	b := make([]byte, 8)
	rand.Read(b)
	randomHex := hex.EncodeToString(b)

	return timestamp + "-" + randomHex
}

// GetDefaultBasePath returns the default path for storing conversations
func GetDefaultBasePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Create the base cache directory structure
	basePath := filepath.Join(homeDir, ".cache", "kodelet", "conversations")

	// Make sure the directory exists
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return "", err
	}

	return basePath, nil
}

// GetMostRecentConversationID returns the ID of the most recent conversation
func GetMostRecentConversationID(ctx context.Context) (string, error) {
	store, err := GetConversationStore(ctx)
	if err != nil {
		return "", err
	}
	defer store.Close()

	// Query for the most recent conversation
	options := QueryOptions{
		Limit:     1,
		Offset:    0,
		SortBy:    "updated_at",
		SortOrder: "desc",
	}

	conversations, err := store.Query(options)
	if err != nil {
		return "", err
	}

	if len(conversations) == 0 {
		return "", errors.New("no conversations found")
	}

	return conversations[0].ID, nil
}
