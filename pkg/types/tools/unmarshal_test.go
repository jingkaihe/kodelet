package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				require.IsType(t, FileReadMetadata{}, result.Metadata)
				meta := result.Metadata.(FileReadMetadata)
				assert.Equal(t, "/test.go", meta.FilePath)
				assert.Len(t, meta.Lines, 2)
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
				require.IsType(t, BashMetadata{}, result.Metadata)
				meta := result.Metadata.(BashMetadata)
				assert.Equal(t, "echo test", meta.Command)
				assert.Equal(t, 0, meta.ExitCode)
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
				assert.Nil(t, result.Metadata)
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var result StructuredToolResult
			err := json.Unmarshal([]byte(tc.json), &result)
			require.NoError(t, err)
			tc.validate(t, result)
		})
	}
}

func TestMetadataTypeRegistry_Completeness(t *testing.T) {
	// Test that the registry contains all expected metadata types
	expectedTypes := []string{
		"file_read", "file_write", "file_edit",
		"grep_tool", "glob_tool", "bash", "bash_background", "mcp_tool", "todo",
		"thinking", "image_recognition", "subagent",
		"web_fetch", "view_background_processes", "custom_tool",
	}

	for _, typeName := range expectedTypes {
		assert.Contains(t, metadataTypeRegistry, typeName)
	}

	// Verify registry size matches expected
	assert.Equal(t, len(expectedTypes), len(metadataTypeRegistry))
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
