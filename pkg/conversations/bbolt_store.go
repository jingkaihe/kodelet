package conversations

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.etcd.io/bbolt"
)

// BBoltConversationStore implements ConversationStore using BoltDB
// Uses operation-scoped database access for multi-process safety
type BBoltConversationStore struct {
	dbPath string
}

// withDB executes an operation with a temporary database connection
// This ensures minimal lock duration and allows multiple processes to access concurrently
func (s *BBoltConversationStore) withDB(operation func(*bbolt.DB) error) error {
	db, err := bbolt.Open(s.dbPath, 0600, &bbolt.Options{
		Timeout: 2 * time.Second, // Reasonable timeout for lock acquisition
	})
	if err != nil {
		return errors.Wrap(err, "failed to open database")
	}
	defer db.Close() // Always close after operation

	return operation(db)
}

// ensureBuckets creates required buckets if they don't exist
func (s *BBoltConversationStore) ensureBuckets(db *bbolt.DB) error {
	return db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte("conversations")); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte("summaries")); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte("search_index")); err != nil {
			return err
		}
		return nil
	})
}

// NewBBoltConversationStore creates a new BBolt-based conversation store
func NewBBoltConversationStore(ctx context.Context, dbPath string) (*BBoltConversationStore, error) {
	// Create directory if needed
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, errors.Wrap(err, "failed to create database directory")
	}

	store := &BBoltConversationStore{
		dbPath: dbPath,
	}

	// Initialize database and create buckets on first access
	err := store.withDB(func(db *bbolt.DB) error {
		return store.ensureBuckets(db)
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize database")
	}

	return store, nil
}

// Save stores a conversation record using the triple storage pattern
func (s *BBoltConversationStore) Save(record ConversationRecord) error {
	return s.withDB(func(db *bbolt.DB) error {
		return db.Update(func(tx *bbolt.Tx) error {
			// 1. Save full record
			conversationsBucket := tx.Bucket([]byte("conversations"))
			recordData, err := json.Marshal(record)
			if err != nil {
				return errors.Wrap(err, "failed to marshal conversation record")
			}

			// 2. Save summary for efficient listing
			summariesBucket := tx.Bucket([]byte("summaries"))
			summary := record.ToSummary()
			summaryData, err := json.Marshal(summary)
			if err != nil {
				return errors.Wrap(err, "failed to marshal conversation summary")
			}

			// 3. Save search index fields (no JSON, raw strings)
			searchBucket := tx.Bucket([]byte("search_index"))

			// Atomic writes to all three buckets
			if err := conversationsBucket.Put([]byte(record.ID), recordData); err != nil {
				return errors.Wrap(err, "failed to save conversation record")
			}
			if err := summariesBucket.Put([]byte("conv:"+record.ID), summaryData); err != nil {
				return errors.Wrap(err, "failed to save conversation summary")
			}
			if err := searchBucket.Put([]byte("msg:"+record.ID), []byte(summary.FirstMessage)); err != nil {
				return errors.Wrap(err, "failed to save search index for message")
			}
			if err := searchBucket.Put([]byte("sum:"+record.ID), []byte(summary.Summary)); err != nil {
				return errors.Wrap(err, "failed to save search index for summary")
			}

			return nil
		})
	})
}

// Load retrieves a conversation record by ID
func (s *BBoltConversationStore) Load(id string) (ConversationRecord, error) {
	var record ConversationRecord
	err := s.withDB(func(db *bbolt.DB) error {
		return db.View(func(tx *bbolt.Tx) error {
			bucket := tx.Bucket([]byte("conversations"))
			data := bucket.Get([]byte(id))
			if data == nil {
				return errors.Errorf("conversation not found: %s", id)
			}
			return json.Unmarshal(data, &record)
		})
	})
	return record, err
}

// List returns all conversation summaries
func (s *BBoltConversationStore) List() ([]ConversationSummary, error) {
	var summaries []ConversationSummary

	err := s.withDB(func(db *bbolt.DB) error {
		return db.View(func(tx *bbolt.Tx) error {
			bucket := tx.Bucket([]byte("summaries"))
			cursor := bucket.Cursor()
			prefix := []byte("conv:")

			for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {
				var summary ConversationSummary
				if err := json.Unmarshal(v, &summary); err != nil {
					continue // Skip corrupted entries
				}
				summaries = append(summaries, summary)
			}

			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	// Sort by creation time (newest first)
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})

	return summaries, nil
}

// Delete removes a conversation and its associated data
func (s *BBoltConversationStore) Delete(id string) error {
	return s.withDB(func(db *bbolt.DB) error {
		return db.Update(func(tx *bbolt.Tx) error {
			// Remove from all three buckets
			conversationsBucket := tx.Bucket([]byte("conversations"))
			summariesBucket := tx.Bucket([]byte("summaries"))
			searchBucket := tx.Bucket([]byte("search_index"))

			// Delete from conversations bucket
			if err := conversationsBucket.Delete([]byte(id)); err != nil {
				return errors.Wrap(err, "failed to delete conversation record")
			}

			// Delete from summaries bucket
			if err := summariesBucket.Delete([]byte("conv:" + id)); err != nil {
				return errors.Wrap(err, "failed to delete conversation summary")
			}

			// Delete from search index
			if err := searchBucket.Delete([]byte("msg:" + id)); err != nil {
				return errors.Wrap(err, "failed to delete search index for message")
			}
			if err := searchBucket.Delete([]byte("sum:" + id)); err != nil {
				return errors.Wrap(err, "failed to delete search index for summary")
			}

			return nil
		})
	})
}

// Query performs advanced queries with filtering, sorting, and pagination
func (s *BBoltConversationStore) Query(options QueryOptions) (QueryResult, error) {
	var allSummaries []ConversationSummary
	var filteredSummaries []ConversationSummary

	err := s.withDB(func(db *bbolt.DB) error {
		return db.View(func(tx *bbolt.Tx) error {
			// If we have a search term, use optimized search
			if options.SearchTerm != "" {
				matchingIDs := s.searchConversations(tx, options.SearchTerm)
				filteredSummaries = s.getSummariesByIDs(tx, matchingIDs)
			} else {
				// Load all summaries for filtering
				bucket := tx.Bucket([]byte("summaries"))
				cursor := bucket.Cursor()
				prefix := []byte("conv:")

				for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {
					var summary ConversationSummary
					if err := json.Unmarshal(v, &summary); err != nil {
						continue // Skip corrupted entries
					}
					allSummaries = append(allSummaries, summary)
				}
				filteredSummaries = allSummaries
			}

			return nil
		})
	})

	if err != nil {
		return QueryResult{}, err
	}

	// Apply date filtering
	if options.StartDate != nil || options.EndDate != nil {
		var dateFiltered []ConversationSummary
		for _, summary := range filteredSummaries {
			if options.StartDate != nil && summary.CreatedAt.Before(*options.StartDate) {
				continue
			}
			if options.EndDate != nil && summary.CreatedAt.After(*options.EndDate) {
				continue
			}
			dateFiltered = append(dateFiltered, summary)
		}
		filteredSummaries = dateFiltered
	}

	// Apply sorting
	s.applySorting(filteredSummaries, options.SortBy, options.SortOrder)

	// Get total count before pagination
	total := len(filteredSummaries)

	// Apply pagination
	if options.Offset > 0 {
		if options.Offset >= len(filteredSummaries) {
			filteredSummaries = []ConversationSummary{}
		} else {
			filteredSummaries = filteredSummaries[options.Offset:]
		}
	}

	if options.Limit > 0 && len(filteredSummaries) > options.Limit {
		filteredSummaries = filteredSummaries[:options.Limit]
	}

	return QueryResult{
		ConversationSummaries: filteredSummaries,
		Total:                 total,
		QueryOptions:          options,
	}, nil
}

// searchConversations performs optimized search using search_index bucket
func (s *BBoltConversationStore) searchConversations(tx *bbolt.Tx, searchTerm string) []string {
	var matchingIDs []string
	searchTermLower := strings.ToLower(searchTerm)
	seen := make(map[string]bool)

	searchBucket := tx.Bucket([]byte("search_index"))
	cursor := searchBucket.Cursor()

	// Search in first messages (msg: prefix)
	msgPrefix := []byte("msg:")
	for k, v := cursor.Seek(msgPrefix); k != nil && bytes.HasPrefix(k, msgPrefix); k, v = cursor.Next() {
		if strings.Contains(strings.ToLower(string(v)), searchTermLower) {
			// Extract conversation ID from key: msg:20240708T150405-abc123
			conversationID := string(k[4:]) // Remove "msg:" prefix
			if !seen[conversationID] {
				matchingIDs = append(matchingIDs, conversationID)
				seen[conversationID] = true
			}
		}
	}

	// Search in summaries (sum: prefix)
	sumPrefix := []byte("sum:")
	for k, v := cursor.Seek(sumPrefix); k != nil && bytes.HasPrefix(k, sumPrefix); k, v = cursor.Next() {
		if strings.Contains(strings.ToLower(string(v)), searchTermLower) {
			conversationID := string(k[4:]) // Remove "sum:" prefix
			if !seen[conversationID] {
				matchingIDs = append(matchingIDs, conversationID)
				seen[conversationID] = true
			}
		}
	}

	return matchingIDs
}

// getSummariesByIDs retrieves summaries for specific conversation IDs
func (s *BBoltConversationStore) getSummariesByIDs(tx *bbolt.Tx, ids []string) []ConversationSummary {
	var summaries []ConversationSummary
	summariesBucket := tx.Bucket([]byte("summaries"))

	for _, id := range ids {
		key := []byte("conv:" + id)
		if data := summariesBucket.Get(key); data != nil {
			var summary ConversationSummary
			if err := json.Unmarshal(data, &summary); err == nil {
				summaries = append(summaries, summary)
			}
		}
	}

	return summaries
}

// applySorting sorts summaries based on the specified criteria
func (s *BBoltConversationStore) applySorting(summaries []ConversationSummary, sortBy, sortOrder string) {
	if sortBy == "" {
		sortBy = "createdAt"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}

	sort.Slice(summaries, func(i, j int) bool {
		var less bool
		switch sortBy {
		case "createdAt":
			less = summaries[i].CreatedAt.Before(summaries[j].CreatedAt)
		case "updatedAt":
			less = summaries[i].UpdatedAt.Before(summaries[j].UpdatedAt)
		case "messageCount":
			less = summaries[i].MessageCount < summaries[j].MessageCount
		default:
			// Default to creation time
			less = summaries[i].CreatedAt.Before(summaries[j].CreatedAt)
		}

		if sortOrder == "desc" {
			return !less
		}
		return less
	})
}

// Close closes the database connection (no-op with operation-scoped access)
func (s *BBoltConversationStore) Close() error {
	// No persistent connection to close with operation-scoped access
	return nil
}
