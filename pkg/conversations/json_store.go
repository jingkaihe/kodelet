package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jingkaihe/kodelet/pkg/logger"
)

// JSONConversationStore implements the ConversationStore interface using JSON files
// with in-memory caching and file watching for performance optimization
type JSONConversationStore struct {
	basePath string

	// In-memory caches for conversation data
	summariesCache map[string]ConversationSummary
	recordsCache   map[string]ConversationRecord // Full records cache for faster Load operations
	cacheRWMutex   sync.RWMutex

	// File watcher
	watcher *fsnotify.Watcher

	// Shutdown context and cancel
	ctx        context.Context
	cancel     context.CancelFunc
	shutdownWg sync.WaitGroup
}

// NewJSONConversationStore creates a new JSON file-based conversation store with file watching
func NewJSONConversationStore(ctx context.Context, basePath string) (*JSONConversationStore, error) {
	// Create the directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create conversations directory: %w", err)
	}

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Create context for graceful shutdown that respects the parent context
	storeCtx, cancel := context.WithCancel(ctx)

	store := &JSONConversationStore{
		basePath:       basePath,
		summariesCache: make(map[string]ConversationSummary),
		recordsCache:   make(map[string]ConversationRecord),
		watcher:        watcher,
		ctx:            storeCtx,
		cancel:         cancel,
	}

	// Initial load of all conversations into cache
	if err := store.loadAllConversations(); err != nil {
		store.Close()
		return nil, fmt.Errorf("failed to load initial conversations: %w", err)
	}

	// Start watching the directory
	if err := store.watcher.Add(basePath); err != nil {
		store.Close()
		return nil, fmt.Errorf("failed to watch conversations directory: %w", err)
	}

	// Start the file watcher goroutine
	store.shutdownWg.Add(1)
	go store.watchFileChanges()

	return store, nil
}

// loadAllConversations loads all conversations from disk into the in-memory cache
func (s *JSONConversationStore) loadAllConversations() error {
	s.cacheRWMutex.Lock()
	defer s.cacheRWMutex.Unlock()

	// Clear existing caches
	s.summariesCache = make(map[string]ConversationSummary)
	s.recordsCache = make(map[string]ConversationRecord)

	// Find all JSON files in the directory
	err := filepath.WalkDir(s.basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-JSON files
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		// Skip temporary files
		if strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}

		// Load the conversation record
		if err := s.loadConversationIntoCache(path); err != nil {
			logger.G(s.ctx).WithError(err).WithField("path", path).Warn("Failed to load conversation into cache")
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk conversations directory: %w", err)
	}

	logger.G(s.ctx).WithField("count", len(s.summariesCache)).Debug("Loaded conversations into cache")
	return nil
}

// loadConversationIntoCache loads a single conversation file into the cache
func (s *JSONConversationStore) loadConversationIntoCache(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read conversation file: %w", err)
	}

	var record ConversationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("failed to unmarshal conversation record: %w", err)
	}

	// Add to both caches
	s.summariesCache[record.ID] = record.ToSummary()
	s.recordsCache[record.ID] = record
	return nil
}

// watchFileChanges monitors the conversations directory for file changes
func (s *JSONConversationStore) watchFileChanges() {
	defer s.shutdownWg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return

		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}

			// Only process JSON files, ignore temporary files
			if !strings.HasSuffix(event.Name, ".json") || strings.HasSuffix(event.Name, ".tmp") {
				continue
			}

			// Extract conversation ID from filename
			filename := filepath.Base(event.Name)
			conversationID := strings.TrimSuffix(filename, ".json")

			switch {
			case event.Op&fsnotify.Create == fsnotify.Create:
				s.handleFileCreated(event.Name, conversationID)
			case event.Op&fsnotify.Write == fsnotify.Write:
				s.handleFileModified(event.Name, conversationID)
			case event.Op&fsnotify.Remove == fsnotify.Remove:
				s.handleFileDeleted(conversationID)
			}

		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			logger.G(s.ctx).WithError(err).Error("File watcher error")
		}
	}
}

// handleFileCreated handles file creation events
func (s *JSONConversationStore) handleFileCreated(filePath, conversationID string) {
	s.cacheRWMutex.Lock()
	defer s.cacheRWMutex.Unlock()

	if err := s.loadConversationIntoCache(filePath); err != nil {
		logger.G(s.ctx).WithError(err).WithField("path", filePath).Warn("Failed to load created conversation into cache")
	} else {
		logger.G(s.ctx).WithField("id", conversationID).Debug("Added conversation to cache")
	}
}

// handleFileModified handles file modification events
func (s *JSONConversationStore) handleFileModified(filePath, conversationID string) {
	s.cacheRWMutex.Lock()
	defer s.cacheRWMutex.Unlock()

	if err := s.loadConversationIntoCache(filePath); err != nil {
		logger.G(s.ctx).WithError(err).WithField("path", filePath).Warn("Failed to reload modified conversation into cache")
	} else {
		logger.G(s.ctx).WithField("id", conversationID).Debug("Updated conversation in cache")
	}
}

// handleFileDeleted handles file deletion events
func (s *JSONConversationStore) handleFileDeleted(conversationID string) {
	s.cacheRWMutex.Lock()
	defer s.cacheRWMutex.Unlock()

	delete(s.summariesCache, conversationID)
	delete(s.recordsCache, conversationID)
	logger.G(s.ctx).WithField("id", conversationID).Debug("Removed conversation from cache")
}

// Save persists a conversation to a JSON file
func (s *JSONConversationStore) Save(record ConversationRecord) error {
	// Ensure the record has an ID
	if record.ID == "" {
		record.ID = GenerateID()
	}

	// Update the timestamp
	record.UpdatedAt = time.Now()

	// Convert to JSON
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation record: %w", err)
	}

	// Write to a temporary file first (atomic write)
	filePath := filepath.Join(s.basePath, record.ID+".json")
	tempPath := filePath + ".tmp"

	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary conversation file: %w", err)
	}

	// Rename to final file (this is atomic on most systems)
	if err := os.Rename(tempPath, filePath); err != nil {
		// Clean up the temp file if rename fails
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename temporary conversation file: %w", err)
	}

	// Immediately update the cache to ensure consistency for tests and immediate queries
	s.cacheRWMutex.Lock()
	s.summariesCache[record.ID] = record.ToSummary()
	s.recordsCache[record.ID] = record
	s.cacheRWMutex.Unlock()

	// Note: The file watcher will also update the cache when the file is created/modified
	return nil
}

// Load retrieves a conversation from its JSON file or cache
func (s *JSONConversationStore) Load(id string) (ConversationRecord, error) {
	// First try to get from cache
	s.cacheRWMutex.RLock()
	if record, found := s.recordsCache[id]; found {
		s.cacheRWMutex.RUnlock()
		return record, nil
	}
	s.cacheRWMutex.RUnlock()

	// If not in cache, load from disk
	filePath := filepath.Join(s.basePath, id+".json")

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return ConversationRecord{}, fmt.Errorf("conversation not found: %s", id)
		}
		return ConversationRecord{}, fmt.Errorf("failed to read conversation file: %w", err)
	}

	var record ConversationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ConversationRecord{}, fmt.Errorf("failed to unmarshal conversation record: %w", err)
	}

	// Update cache with the loaded record
	s.cacheRWMutex.Lock()
	s.summariesCache[record.ID] = record.ToSummary()
	s.recordsCache[record.ID] = record
	s.cacheRWMutex.Unlock()

	return record, nil
}

// List returns summaries of all stored conversations using the in-memory cache
func (s *JSONConversationStore) List() ([]ConversationSummary, error) {
	return s.Query(QueryOptions{})
}

// Delete removes a conversation
func (s *JSONConversationStore) Delete(id string) error {
	filePath := filepath.Join(s.basePath, id+".json")

	err := os.Remove(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("conversation not found: %s", id)
		}
		return fmt.Errorf("failed to delete conversation file: %w", err)
	}

	// Immediately update the cache to ensure consistency for tests and immediate queries
	s.cacheRWMutex.Lock()
	delete(s.summariesCache, id)
	delete(s.recordsCache, id)
	s.cacheRWMutex.Unlock()

	// Note: The file watcher will also update the cache when the file is deleted
	return nil
}

// Query searches for conversations using the in-memory cache for performance
func (s *JSONConversationStore) Query(options QueryOptions) ([]ConversationSummary, error) {
	s.cacheRWMutex.RLock()
	defer s.cacheRWMutex.RUnlock()

	var summaries []ConversationSummary

	// Filter conversations from cache
	for _, summary := range s.summariesCache {
		// Apply date filters if specified
		if options.StartDate != nil && summary.UpdatedAt.Before(*options.StartDate) {
			continue
		}
		if options.EndDate != nil && summary.UpdatedAt.After(*options.EndDate) {
			continue
		}

		// Apply text search if specified
		if options.SearchTerm != "" {
			searchTerm := strings.ToLower(options.SearchTerm)
			found := false

			// Search in summary
			if strings.Contains(strings.ToLower(summary.Summary), searchTerm) {
				found = true
			}

			// Search in first message
			if !found && strings.Contains(strings.ToLower(summary.FirstMessage), searchTerm) {
				found = true
			}

			// For deeper text search, we need to load the full conversation
			if !found {
				if record, err := s.Load(summary.ID); err == nil {
					if strings.Contains(strings.ToLower(string(record.RawMessages)), searchTerm) {
						found = true
					}
				}
			}

			// Skip if not found
			if !found {
				continue
			}
		}

		// Add to result list
		summaries = append(summaries, summary)
	}

	// Sort results
	sort.Slice(summaries, func(i, j int) bool {
		// Default sort is by updated time
		if options.SortBy == "" || options.SortBy == "updated" || options.SortBy == "updated_at" {
			if options.SortOrder == "asc" {
				return summaries[i].UpdatedAt.Before(summaries[j].UpdatedAt)
			}
			return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
		}

		// Sort by created time
		if options.SortBy == "created" || options.SortBy == "created_at" {
			if options.SortOrder == "asc" {
				return summaries[i].CreatedAt.Before(summaries[j].CreatedAt)
			}
			return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
		}

		// Sort by message count
		if options.SortBy == "messages" {
			if options.SortOrder == "asc" {
				return summaries[i].MessageCount < summaries[j].MessageCount
			}
			return summaries[i].MessageCount > summaries[j].MessageCount
		}

		// Fallback to updated time
		if options.SortOrder == "asc" {
			return summaries[i].UpdatedAt.Before(summaries[j].UpdatedAt)
		}
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	// Apply limit and offset if specified
	if options.Limit > 0 || options.Offset > 0 {
		offset := options.Offset
		if offset > len(summaries) {
			offset = len(summaries)
		}

		limit := options.Limit
		if limit <= 0 || offset+limit > len(summaries) {
			limit = len(summaries) - offset
		}

		if offset > 0 || limit < len(summaries) {
			summaries = summaries[offset : offset+limit]
		}
	}

	return summaries, nil
}

// GetConversationSummary quickly retrieves a conversation summary from cache
func (s *JSONConversationStore) GetConversationSummary(id string) (ConversationSummary, bool) {
	s.cacheRWMutex.RLock()
	defer s.cacheRWMutex.RUnlock()

	summary, found := s.summariesCache[id]
	return summary, found
}

// Close cleans up resources and shuts down the file watcher
func (s *JSONConversationStore) Close() error {
	// Cancel the context to signal shutdown
	if s.cancel != nil {
		s.cancel()
	}

	// Close the file watcher
	if s.watcher != nil {
		if err := s.watcher.Close(); err != nil {
			// Use background context since store context is cancelled
			logger.G(context.Background()).WithError(err).Error("Failed to close file watcher")
		}
	}

	// Wait for all goroutines to finish
	s.shutdownWg.Wait()

	return nil
}
