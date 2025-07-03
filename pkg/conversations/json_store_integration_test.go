package conversations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestJSONStore_StructuredToolResults(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "kodelet-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a store
	store, err := NewJSONConversationStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a conversation record with structured tool results
	record := NewConversationRecord("test-conversation")
	record.ToolResults = map[string]tools.StructuredToolResult{
		"call_1": {
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: tools.FileReadMetadata{
				FilePath:  "/test/file.go",
				Lines:     []string{"package main", "func main() {}"},
				Language:  "go",
				Truncated: false,
			},
		},
		"call_2": {
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: tools.BashMetadata{
				Command:       "go test ./...",
				ExitCode:      0,
				Output:        "ok\tgithub.com/test\t0.005s",
				ExecutionTime: 5 * time.Second,
				WorkingDir:    "/test",
			},
		},
	}

	// Save the record
	err = store.Save(record)
	if err != nil {
		t.Fatalf("Failed to save record: %v", err)
	}

	// Load it back
	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatalf("Failed to load record: %v", err)
	}

	// Verify the tool results were preserved
	if len(loaded.ToolResults) != len(record.ToolResults) {
		t.Errorf("Expected %d tool results, got %d",
			len(record.ToolResults), len(loaded.ToolResults))
	}

	// Check specific results
	for key, original := range record.ToolResults {
		loaded, exists := loaded.ToolResults[key]
		if !exists {
			t.Errorf("Missing tool result for key %s", key)
			continue
		}

		if loaded.ToolName != original.ToolName {
			t.Errorf("Tool name mismatch for %s: got %s, want %s",
				key, loaded.ToolName, original.ToolName)
		}

		if loaded.Success != original.Success {
			t.Errorf("Success mismatch for %s", key)
		}

		if loaded.Metadata == nil {
			t.Errorf("Metadata is nil for %s", key)
		} else if loaded.Metadata.ToolType() != original.Metadata.ToolType() {
			t.Errorf("Metadata type mismatch for %s: got %s, want %s",
				key, loaded.Metadata.ToolType(), original.Metadata.ToolType())
		}
	}

	// Verify the actual JSON file exists and check its structure
	jsonPath := filepath.Join(tempDir, record.ID+".json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("Failed to read JSON file: %v", err)
	}

	// The JSON should contain metadataType fields
	if !strings.Contains(string(data), `"metadataType"`) {
		t.Errorf("JSON should contain metadataType field")
	}
}
