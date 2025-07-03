package tools

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalJSON_SimplifiedRegistry(t *testing.T) {
	// Test that all metadata types in the registry can be properly unmarshaled
	testCases := map[string]struct {
		json     string
		validate func(t *testing.T, result StructuredToolResult)
	}{
		"file_read": {
			json: `{
				"toolName": "file_read",
				"success": true,
				"timestamp": "2023-01-01T00:00:00Z",
				"metadataType": "file_read",
				"metadata": {
					"filePath": "/test.go",
					"lines": ["line1", "line2"],
					"language": "go",
					"offset": 10,
					"truncated": false
				}
			}`,
			validate: func(t *testing.T, result StructuredToolResult) {
				meta, ok := result.Metadata.(FileReadMetadata)
				if !ok {
					t.Fatalf("Expected FileReadMetadata, got %T", result.Metadata)
				}
				if meta.FilePath != "/test.go" {
					t.Errorf("FilePath = %v, want /test.go", meta.FilePath)
				}
				if len(meta.Lines) != 2 {
					t.Errorf("Lines length = %v, want 2", len(meta.Lines))
				}
			},
		},
		"bash": {
			json: `{
				"toolName": "bash",
				"success": true,
				"timestamp": "2023-01-01T00:00:00Z",
				"metadataType": "bash",
				"metadata": {
					"command": "echo test",
					"exitCode": 0,
					"output": "test",
					"executionTime": 100000000,
					"workingDir": "/home"
				}
			}`,
			validate: func(t *testing.T, result StructuredToolResult) {
				meta, ok := result.Metadata.(BashMetadata)
				if !ok {
					t.Fatalf("Expected BashMetadata, got %T", result.Metadata)
				}
				if meta.Command != "echo test" {
					t.Errorf("Command = %v, want echo test", meta.Command)
				}
				if meta.ExitCode != 0 {
					t.Errorf("ExitCode = %v, want 0", meta.ExitCode)
				}
			},
		},
		"unknown_type": {
			json: `{
				"toolName": "unknown",
				"success": true,
				"timestamp": "2023-01-01T00:00:00Z",
				"metadataType": "future_type",
				"metadata": {
					"someField": "value"
				}
			}`,
			validate: func(t *testing.T, result StructuredToolResult) {
				if result.Metadata != nil {
					t.Errorf("Expected nil metadata for unknown type, got %T", result.Metadata)
				}
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var result StructuredToolResult
			err := json.Unmarshal([]byte(tc.json), &result)
			if err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}
			tc.validate(t, result)
		})
	}
}

func TestMetadataTypeRegistry_Completeness(t *testing.T) {
	// Test that the registry contains all expected metadata types
	expectedTypes := []string{
		"file_read", "file_write", "file_edit", "file_multi_edit",
		"grep_tool", "glob_tool", "bash", "mcp_tool", "todo",
		"thinking", "batch", "browser_navigate", "browser_click",
		"browser_get_page", "browser_screenshot", "browser_type",
		"browser_wait_for", "image_recognition", "subagent",
		"web_fetch", "view_background_processes",
	}

	for _, typeName := range expectedTypes {
		if _, exists := metadataTypeRegistry[typeName]; !exists {
			t.Errorf("Missing metadata type in registry: %s", typeName)
		}
	}

	// Verify registry size matches expected
	if len(metadataTypeRegistry) != len(expectedTypes) {
		t.Errorf("Registry size mismatch: got %d, want %d",
			len(metadataTypeRegistry), len(expectedTypes))
	}
}

func BenchmarkUnmarshalJSON_Original(b *testing.B) {
	// This would benchmark the original implementation if it existed
	// For now, we'll benchmark the current implementation
	data := []byte(`{
		"toolName": "file_read",
		"success": true,
		"timestamp": "2023-01-01T00:00:00Z",
		"metadataType": "file_read",
		"metadata": {
			"filePath": "/test.go",
			"lines": ["line1", "line2", "line3"],
			"language": "go"
		}
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var result StructuredToolResult
		_ = json.Unmarshal(data, &result)
	}
}
