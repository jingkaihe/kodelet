package tools

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStructuredToolResult_JSONMarshaling(t *testing.T) {
	tests := []struct {
		name   string
		result StructuredToolResult
	}{
		{
			name: "FileReadMetadata",
			result: StructuredToolResult{
				ToolName:  "file_read",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: FileReadMetadata{
					FilePath:  "/test/file.go",
					Offset:    0,
					Lines:     []string{"package main", "import \"fmt\""},
					Language:  "go",
					Truncated: false,
				},
			},
		},
		{
			name: "BashMetadata",
			result: StructuredToolResult{
				ToolName:  "bash",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: BashMetadata{
					Command:       "ls -la",
					ExitCode:      0,
					Output:        "total 8\ndrwxr-xr-x 2 user user 4096 Jan 1 00:00 .",
					ExecutionTime: 100 * time.Millisecond,
					WorkingDir:    "/home/user",
				},
			},
		},
		{
			name: "GrepMetadata",
			result: StructuredToolResult{
				ToolName:  "grep",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: GrepMetadata{
					Pattern: "func.*Test",
					Path:    "/src",
					Include: "*.go",
					Results: []SearchResult{
						{
							FilePath: "test.go",
							Language: "go",
							Matches: []SearchMatch{
								{
									LineNumber: 10,
									Content:    "func TestExample(t *testing.T) {",
									MatchStart: 0,
									MatchEnd:   16,
								},
							},
						},
					},
					Truncated: false,
				},
			},
		},
		{
			name: "NoMetadata",
			result: StructuredToolResult{
				ToolName:  "unknown",
				Success:   false,
				Error:     "something went wrong",
				Timestamp: time.Now(),
				Metadata:  nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := json.Marshal(tt.result)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			// Unmarshal back
			var unmarshaled StructuredToolResult
			err = json.Unmarshal(data, &unmarshaled)
			if err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			// Compare basic fields
			if unmarshaled.ToolName != tt.result.ToolName {
				t.Errorf("ToolName mismatch: got %s, want %s", unmarshaled.ToolName, tt.result.ToolName)
			}
			if unmarshaled.Success != tt.result.Success {
				t.Errorf("Success mismatch: got %v, want %v", unmarshaled.Success, tt.result.Success)
			}
			if unmarshaled.Error != tt.result.Error {
				t.Errorf("Error mismatch: got %s, want %s", unmarshaled.Error, tt.result.Error)
			}

			// Compare metadata
			if tt.result.Metadata == nil {
				if unmarshaled.Metadata != nil {
					t.Errorf("Expected nil metadata, got %v", unmarshaled.Metadata)
				}
			} else {
				if unmarshaled.Metadata == nil {
					t.Errorf("Expected metadata, got nil")
				} else if unmarshaled.Metadata.ToolType() != tt.result.Metadata.ToolType() {
					t.Errorf("Metadata type mismatch: got %s, want %s",
						unmarshaled.Metadata.ToolType(), tt.result.Metadata.ToolType())
				}
			}
		})
	}
}

func TestStructuredToolResult_BackwardCompatibility(t *testing.T) {
	// Test unmarshaling old format without metadataType field
	oldFormat := `{
		"toolName": "file_read",
		"success": true,
		"timestamp": "2023-01-01T00:00:00Z",
		"metadata": {
			"filePath": "/test.go",
			"lines": ["package main"],
			"language": "go"
		}
	}`

	var result StructuredToolResult
	err := json.Unmarshal([]byte(oldFormat), &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal old format: %v", err)
	}

	// Should successfully unmarshal basic fields
	if result.ToolName != "file_read" {
		t.Errorf("Expected tool name 'file_read', got %s", result.ToolName)
	}
	if !result.Success {
		t.Errorf("Expected success to be true")
	}

	// Metadata will be nil since we can't determine the type
	if result.Metadata != nil {
		t.Errorf("Expected nil metadata for old format, got %v", result.Metadata)
	}
}

func TestConversationRecord_JSONRoundTrip(t *testing.T) {
	// Test that a map of StructuredToolResult can be marshaled and unmarshaled
	toolResults := map[string]StructuredToolResult{
		"call_1": {
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: FileReadMetadata{
				FilePath: "test.go",
				Lines:    []string{"package main"},
				Language: "go",
			},
		},
		"call_2": {
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: BashMetadata{
				Command:  "go test",
				ExitCode: 0,
				Output:   "PASS",
			},
		},
	}

	// Marshal the map
	data, err := json.Marshal(toolResults)
	if err != nil {
		t.Fatalf("Failed to marshal tool results map: %v", err)
	}

	// Unmarshal back
	var unmarshaled map[string]StructuredToolResult
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal tool results map: %v", err)
	}

	// Verify the results
	if len(unmarshaled) != len(toolResults) {
		t.Errorf("Expected %d results, got %d", len(toolResults), len(unmarshaled))
	}

	for key, original := range toolResults {
		result, exists := unmarshaled[key]
		if !exists {
			t.Errorf("Missing result for key %s", key)
			continue
		}

		if result.ToolName != original.ToolName {
			t.Errorf("Tool name mismatch for %s: got %s, want %s",
				key, result.ToolName, original.ToolName)
		}
		if result.Success != original.Success {
			t.Errorf("Success mismatch for %s", key)
		}
		if result.Metadata == nil {
			t.Errorf("Expected metadata for %s, got nil", key)
		} else if result.Metadata.ToolType() != original.Metadata.ToolType() {
			t.Errorf("Metadata type mismatch for %s: got %s, want %s",
				key, result.Metadata.ToolType(), original.Metadata.ToolType())
		}
	}
}
