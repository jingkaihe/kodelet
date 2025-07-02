package conversations

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// JSONConversationStore implements the ConversationStore interface using JSON files
type JSONConversationStore struct {
	basePath string
}

// NewJSONConversationStore creates a new JSON file-based conversation store
func NewJSONConversationStore(basePath string) (*JSONConversationStore, error) {
	// Create the directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create conversations directory: %w", err)
	}

	return &JSONConversationStore{
		basePath: basePath,
	}, nil
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

	return nil
}

// Load retrieves a conversation from its JSON file
func (s *JSONConversationStore) Load(id string) (ConversationRecord, error) {
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

	return record, nil
}

// List returns summaries of all stored conversations
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

	return nil
}

// Query searches for conversations matching the given criteria
func (s *JSONConversationStore) Query(options QueryOptions) ([]ConversationSummary, error) {
	var summaries []ConversationSummary

	// Find all JSON files in the directory
	err := filepath.WalkDir(s.basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-JSON files
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		// Load the conversation record
		data, err := os.ReadFile(path)
		if err != nil {
			// Log error but continue with other files
			fmt.Fprintf(os.Stderr, "Error reading file %s: %v\n", path, err)
			return nil
		}

		var record ConversationRecord
		if err := json.Unmarshal(data, &record); err != nil {
			// Log error but continue with other files
			fmt.Fprintf(os.Stderr, "Error parsing file %s: %v\n", path, err)
			return nil
		}

		// Apply date filters if specified
		if options.StartDate != nil && record.UpdatedAt.Before(*options.StartDate) {
			return nil
		}
		if options.EndDate != nil && record.UpdatedAt.After(*options.EndDate) {
			return nil
		}

		// Apply text search if specified
		if options.SearchTerm != "" {
			found := false
			// Search in summary
			if strings.Contains(strings.ToLower(record.Summary), strings.ToLower(options.SearchTerm)) {
				found = true
			}

			// FirstUserPrompt has been removed, so skip this check

			// Search in raw messages
			if !found && len(record.RawMessages) > 0 {
				if strings.Contains(strings.ToLower(string(record.RawMessages)), strings.ToLower(options.SearchTerm)) {
					found = true
				}
			}

			// Skip if not found
			if !found {
				return nil
			}
		}

		// Add to result list
		summaries = append(summaries, record.ToSummary())
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to query conversations: %w", err)
	}

	// Sort results
	sort.Slice(summaries, func(i, j int) bool {
		// Default sort is by updated time
		if options.SortBy == "" || options.SortBy == "updated" {
			if options.SortOrder == "asc" {
				return summaries[i].UpdatedAt.Before(summaries[j].UpdatedAt)
			}
			return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
		}

		// Sort by created time
		if options.SortBy == "created" {
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

// AddToolExecution adds a tool execution to a conversation
func (s *JSONConversationStore) AddToolExecution(conversationID, toolName, input, userFacing string, messageIndex int) error {
	// Load the existing conversation
	record, err := s.Load(conversationID)
	if err != nil {
		return fmt.Errorf("failed to load conversation for tool execution: %w", err)
	}

	// Add the tool execution
	record.AddToolExecution(toolName, input, userFacing, messageIndex)

	// Save the updated record
	return s.Save(record)
}

// Close cleans up any resources
func (s *JSONConversationStore) Close() error {
	// No resources to clean up for the JSON file store
	return nil
}
