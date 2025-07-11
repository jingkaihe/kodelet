package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"go.etcd.io/bbolt"

	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/types/conversations"
)

func main() {
	if err := runMigration(); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Migration completed successfully!")
}

func runMigration() error {
	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(err, "failed to get home directory")
	}

	// Define paths
	bboltPath := filepath.Join(homeDir, ".cache", "kodelet", "conversations", "storage.db")
	sqlitePath := filepath.Join(homeDir, ".kodelet", "storage.db")

	fmt.Printf("Migrating from BBolt: %s\n", bboltPath)
	fmt.Printf("To SQLite: %s\n", sqlitePath)

	// Check if bbolt database exists
	if _, err := os.Stat(bboltPath); os.IsNotExist(err) {
		return errors.Errorf("BBolt database not found at %s", bboltPath)
	}

	// Check if SQLite database already exists
	if _, err := os.Stat(sqlitePath); err == nil {
		return errors.Errorf("SQLite database already exists at %s. Please remove it first or backup your data", sqlitePath)
	}

	// Read conversations from BBolt
	conversations, err := readConversationsFromBBolt(bboltPath)
	if err != nil {
		return errors.Wrap(err, "failed to read conversations from BBolt")
	}

	fmt.Printf("Found %d conversations in BBolt database\n", len(conversations))

	if len(conversations) == 0 {
		fmt.Println("No conversations found, creating empty SQLite database")
	}

	// Create SQLite database and migrate data
	if err := writeConversationsToSQLite(sqlitePath, conversations); err != nil {
		return errors.Wrap(err, "failed to write conversations to SQLite")
	}

	return nil
}

func readConversationsFromBBolt(dbPath string) ([]conversations.ConversationRecord, error) {
	// Open BBolt database
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{
		ReadOnly: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to open BBolt database")
	}
	defer db.Close()

	var records []conversations.ConversationRecord

	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("conversations"))
		if bucket == nil {
			fmt.Println("No conversations bucket found in BBolt database")
			return nil
		}

		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var record conversations.ConversationRecord
			if err := json.Unmarshal(v, &record); err != nil {
				fmt.Printf("Warning: Failed to unmarshal conversation %s: %v\n", string(k), err)
				continue
			}
			records = append(records, record)
		}

		return nil
	})

	if err != nil {
		return nil, errors.Wrap(err, "failed to read from BBolt database")
	}

	return records, nil
}

func writeConversationsToSQLite(dbPath string, records []conversations.ConversationRecord) error {
	ctx := context.Background()

	// Create SQLite store (this will create the database and run migrations)
	store, err := sqlite.NewSQLiteConversationStore(ctx, dbPath)
	if err != nil {
		return errors.Wrap(err, "failed to create SQLite store")
	}
	defer store.Close()

	// Save all conversations
	for i, record := range records {
		if err := store.Save(ctx, record); err != nil {
			return errors.Wrapf(err, "failed to save conversation %s (record %d)", record.ID, i+1)
		}
		
		// Print progress for large migrations
		if (i+1)%10 == 0 || i+1 == len(records) {
			fmt.Printf("Migrated %d/%d conversations\n", i+1, len(records))
		}
	}

	return nil
}