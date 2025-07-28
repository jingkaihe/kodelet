package tools

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStructuredToolResult_JSONMarshaling(t *testing.T) {
	tests := []struct {
		name   string
		result StructuredToolResult
	}{
		{
			name: "FileReadMetadata with value type",
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
			name: "FileReadMetadata with pointer type",
			result: StructuredToolResult{
				ToolName:  "file_read",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &FileReadMetadata{
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
			name: "BackgroundBashMetadata",
			result: StructuredToolResult{
				ToolName:  "bash_background",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &BackgroundBashMetadata{
					Command:   "python -m http.server 8000",
					PID:       12345,
					LogPath:   "/tmp/.kodelet/12345/out.log",
					StartTime: time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name: "GrepMetadata",
			result: StructuredToolResult{
				ToolName:  "grep_tool",
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
			name: "WebFetchMetadata with content",
			result: StructuredToolResult{
				ToolName:  "web_fetch",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &WebFetchMetadata{
					URL:           "https://example.com",
					ContentType:   "text/html",
					Size:          1024,
					SavedPath:     "/tmp/example.html",
					Prompt:        "Extract main content",
					ProcessedType: "saved",
					Content:       "<html>Example content</html>",
				},
			},
		},
		{
			name: "TodoMetadata",
			result: StructuredToolResult{
				ToolName:  "todo_read",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: TodoMetadata{
					Action: "read",
					TodoList: []TodoItem{
						{ID: "1", Content: "Task 1", Status: "pending", Priority: "high"},
						{ID: "2", Content: "Task 2", Status: "completed", Priority: "medium"},
					},
					Statistics: TodoStats{
						Total:      2,
						Completed:  1,
						InProgress: 0,
						Pending:    1,
					},
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
			require.NoError(t, err, "Failed to marshal")

			t.Logf("Marshaled JSON: %s", string(data))

			// Verify metadataType field is included
			var jsonMap map[string]interface{}
			json.Unmarshal(data, &jsonMap)
			if tt.result.Metadata != nil {
				_, hasType := jsonMap["metadataType"]
				assert.True(t, hasType, "Expected metadataType field in JSON")
			}

			// Unmarshal back
			var unmarshaled StructuredToolResult
			err = json.Unmarshal(data, &unmarshaled)
			require.NoError(t, err, "Failed to unmarshal")

			// Compare basic fields
			assert.Equal(t, tt.result.ToolName, unmarshaled.ToolName, "ToolName mismatch")
			assert.Equal(t, tt.result.Success, unmarshaled.Success, "Success mismatch")
			assert.Equal(t, tt.result.Error, unmarshaled.Error, "Error mismatch")

			// Compare metadata
			if tt.result.Metadata == nil {
				assert.Nil(t, unmarshaled.Metadata, "Expected nil metadata")
			} else {
				assert.NotNil(t, unmarshaled.Metadata, "Expected metadata")
				// Check that ToolType matches
				assert.Equal(t, tt.result.Metadata.ToolType(), unmarshaled.Metadata.ToolType(), "Metadata type mismatch")

				// IMPORTANT: After unmarshaling, metadata is always a value type, not a pointer
				metaType := reflect.TypeOf(unmarshaled.Metadata)
				assert.NotEqual(t, reflect.Ptr, metaType.Kind(), "Expected value type after unmarshal, got pointer type: %T", unmarshaled.Metadata)

				// Log the actual type for debugging
				t.Logf("Unmarshaled metadata type: %T", unmarshaled.Metadata)
			}
		})
	}
}

func TestStructuredToolResult_TypeAssertions(t *testing.T) {
	// Test that type assertions work correctly for both pointer and value types
	tests := []struct {
		name            string
		metadata        ToolMetadata
		expectedType    string
		shouldBeValue   bool
		shouldBePointer bool
	}{
		{
			name:            "FileReadMetadata value",
			metadata:        FileReadMetadata{FilePath: "/test.go"},
			expectedType:    "file_read",
			shouldBeValue:   true,
			shouldBePointer: false,
		},
		{
			name:            "FileReadMetadata pointer",
			metadata:        &FileReadMetadata{FilePath: "/test.go"},
			expectedType:    "file_read",
			shouldBeValue:   false,
			shouldBePointer: true,
		},
		{
			name:            "WebFetchMetadata value",
			metadata:        WebFetchMetadata{URL: "https://example.com", Content: "test"},
			expectedType:    "web_fetch",
			shouldBeValue:   true,
			shouldBePointer: false,
		},
		{
			name:            "WebFetchMetadata pointer",
			metadata:        &WebFetchMetadata{URL: "https://example.com", Content: "test"},
			expectedType:    "web_fetch",
			shouldBeValue:   false,
			shouldBePointer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check ToolType
			assert.Equal(t, tt.expectedType, tt.metadata.ToolType(), "ToolType mismatch")

			// Test value type assertion
			switch tt.expectedType {
			case "file_read":
				_, ok := tt.metadata.(FileReadMetadata)
				assert.Equal(t, tt.shouldBeValue, ok, "Value type assertion mismatch")
				_, ok = tt.metadata.(*FileReadMetadata)
				assert.Equal(t, tt.shouldBePointer, ok, "Pointer type assertion mismatch")
			case "web_fetch":
				_, ok := tt.metadata.(WebFetchMetadata)
				assert.Equal(t, tt.shouldBeValue, ok, "Value type assertion mismatch")
				_, ok = tt.metadata.(*WebFetchMetadata)
				assert.Equal(t, tt.shouldBePointer, ok, "Pointer type assertion mismatch")
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
	require.NoError(t, err, "Failed to unmarshal old format")

	// Should successfully unmarshal basic fields
	assert.Equal(t, "file_read", result.ToolName, "Expected tool name 'file_read'")
	assert.True(t, result.Success, "Expected success to be true")

	// Metadata will be nil since we can't determine the type
	assert.Nil(t, result.Metadata, "Expected nil metadata for old format")
}

func TestStructuredToolResult_ComplexMetadata(t *testing.T) {
	// Test complex metadata types that have nested structures
	tests := []struct {
		name   string
		result StructuredToolResult
	}{
		{
			name: "MCPToolMetadata with content array",
			result: StructuredToolResult{
				ToolName:  "mcp_definition",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &MCPToolMetadata{
					MCPToolName: "definition",
					ServerName:  "language-server",
					Parameters:  map[string]any{"symbol": "TestFunction"},
					Content: []MCPContent{
						{Type: "text", Text: "function definition here"},
						{Type: "code", Text: "func TestFunction() {}", MimeType: "text/x-go"},
					},
					ContentText:   "function definition here\nfunc TestFunction() {}",
					ExecutionTime: 50 * time.Millisecond,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := json.Marshal(tt.result)
			require.NoError(t, err, "Failed to marshal")

			t.Logf("Marshaled JSON: %s", string(data))

			// Unmarshal back
			var unmarshaled StructuredToolResult
			err = json.Unmarshal(data, &unmarshaled)
			require.NoError(t, err, "Failed to unmarshal")

			// Verify the metadata type
			require.NotNil(t, unmarshaled.Metadata, "Expected metadata")
			assert.Equal(t, tt.result.Metadata.ToolType(), unmarshaled.Metadata.ToolType(), "Metadata type mismatch")
		})
	}
}

func TestConversationRecord_JSONRoundTrip(t *testing.T) {
	// Test that a map of StructuredToolResult can be marshaled and unmarshaled
	toolResults := map[string]StructuredToolResult{
		"call_1": {
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &FileReadMetadata{
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
		"call_3": {
			ToolName:  "web_fetch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &WebFetchMetadata{
				URL:           "https://example.com",
				SavedPath:     "/tmp/page.html",
				Content:       "Page content here",
				ProcessedType: "saved",
			},
		},
	}

	// Marshal the map
	data, err := json.Marshal(toolResults)
	require.NoError(t, err, "Failed to marshal tool results map")

	t.Logf("Marshaled map JSON: %s", string(data))

	// Unmarshal back
	var unmarshaled map[string]StructuredToolResult
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err, "Failed to unmarshal tool results map")

	// Verify the results
	assert.Equal(t, len(toolResults), len(unmarshaled), "Expected same number of results")

	for key, original := range toolResults {
		result, exists := unmarshaled[key]
		require.True(t, exists, "Missing result for key %s", key)

		assert.Equal(t, original.ToolName, result.ToolName, "Tool name mismatch for %s", key)
		assert.Equal(t, original.Success, result.Success, "Success mismatch for %s", key)
		assert.NotNil(t, result.Metadata, "Expected metadata for %s", key)
		assert.Equal(t, original.Metadata.ToolType(), result.Metadata.ToolType(), "Metadata type mismatch for %s", key)
		// Log the actual type after unmarshal
		t.Logf("Result %s metadata type after unmarshal: %T", key, result.Metadata)
	}
}

func TestStructuredToolResult_RawJSONStrings(t *testing.T) {
	// Test unmarshaling from raw JSON strings that would be stored in files
	tests := []struct {
		name     string
		jsonStr  string
		expected ToolMetadata
		wantErr  bool
	}{
		{
			name: "WebFetch with all fields",
			jsonStr: `{
				"toolName": "web_fetch",
				"success": true,
				"timestamp": "2023-01-01T00:00:00Z",
				"metadataType": "web_fetch",
				"metadata": {
					"url": "https://example.com",
					"contentType": "text/html",
					"size": 1024,
					"savedPath": "/tmp/example.html",
					"prompt": "Extract content",
					"processedType": "saved",
					"content": "This is the fetched content"
				}
			}`,
			expected: WebFetchMetadata{
				URL:           "https://example.com",
				ContentType:   "text/html",
				Size:          1024,
				SavedPath:     "/tmp/example.html",
				Prompt:        "Extract content",
				ProcessedType: "saved",
				Content:       "This is the fetched content",
			},
			wantErr: false,
		},
		{
			name: "FileRead with lines",
			jsonStr: `{
				"toolName": "file_read",
				"success": true,
				"timestamp": "2023-01-01T00:00:00Z",
				"metadataType": "file_read",
				"metadata": {
					"filePath": "/src/main.go",
					"offset": 10,
					"lines": ["package main", "", "import \"fmt\""],
					"language": "go",
					"truncated": false
				}
			}`,
			expected: FileReadMetadata{
				FilePath:  "/src/main.go",
				Offset:    10,
				Lines:     []string{"package main", "", "import \"fmt\""},
				Language:  "go",
				Truncated: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result StructuredToolResult
			err := json.Unmarshal([]byte(tt.jsonStr), &result)

			if tt.wantErr {
				assert.Error(t, err, "Expected error but got none")
				return
			}
			assert.NoError(t, err, "Unexpected error during unmarshal")

			if err == nil && result.Metadata != nil {
				// Verify the metadata matches expected
				assert.Equal(t, tt.expected.ToolType(), result.Metadata.ToolType(), "ToolType mismatch")

				// Check it's a value type (not pointer) after unmarshal
				assert.NotEqual(t, reflect.Ptr, reflect.TypeOf(result.Metadata).Kind(), "Expected value type after unmarshal, got pointer")
			}
		})
	}
}

// ExtractMetadata tests (moved from renderers package)

func TestExtractMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata ToolMetadata
		target   interface{}
		want     bool
		validate func(t *testing.T, target interface{})
	}{
		{
			name:     "nil metadata returns false",
			metadata: nil,
			target:   &FileReadMetadata{},
			want:     false,
		},
		{
			name:     "nil target returns false",
			metadata: FileReadMetadata{FilePath: "/test.go"},
			target:   nil,
			want:     false,
		},
		{
			name:     "non-pointer target returns false",
			metadata: FileReadMetadata{FilePath: "/test.go"},
			target:   FileReadMetadata{},
			want:     false,
		},
		{
			name: "value type to matching pointer target",
			metadata: FileReadMetadata{
				FilePath: "/test.go",
				Lines:    []string{"line1", "line2"},
				Language: "go",
			},
			target: &FileReadMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*FileReadMetadata)
				assert.Equal(t, "/test.go", result.FilePath, "FilePath mismatch")
				assert.Equal(t, 2, len(result.Lines), "Lines length mismatch")
				assert.Equal(t, "go", result.Language, "Language mismatch")
			},
		},
		{
			name: "pointer type to matching pointer target",
			metadata: &WebFetchMetadata{
				URL:     "https://example.com",
				Content: "test content",
				Size:    100,
			},
			target: &WebFetchMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*WebFetchMetadata)
				assert.Equal(t, "https://example.com", result.URL, "URL mismatch")
				assert.Equal(t, "test content", result.Content, "Content mismatch")
				assert.Equal(t, int64(100), result.Size, "Size mismatch")
			},
		},
		{
			name:     "mismatched types returns false",
			metadata: FileReadMetadata{FilePath: "/test.go"},
			target:   &BashMetadata{},
			want:     false,
		},
		{
			name: "metadata with slices and maps",
			metadata: &MCPToolMetadata{
				MCPToolName: "test_tool",
				ServerName:  "test_server",
				Parameters: map[string]any{
					"key1": "value1",
					"key2": 42,
				},
				Content: []MCPContent{
					{Type: "text", Text: "content1"},
					{Type: "code", Text: "content2"},
				},
			},
			target: &MCPToolMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*MCPToolMetadata)
				assert.Equal(t, "test_tool", result.MCPToolName, "MCPToolName mismatch")
				assert.Equal(t, 2, len(result.Parameters), "Parameters length mismatch")
				assert.Equal(t, "value1", result.Parameters["key1"], "Parameters[key1] mismatch")
				assert.Equal(t, 2, len(result.Content), "Content length mismatch")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMetadata(tt.metadata, tt.target)
			assert.Equal(t, tt.want, got, "ExtractMetadata() return value mismatch")
			if got && tt.validate != nil {
				tt.validate(t, tt.target)
			}
		})
	}
}

func TestExtractMetadata_AllTypes(t *testing.T) {
	// Test that all metadata types work with the generic function
	metadataTypes := []struct {
		name     string
		metadata ToolMetadata
		target   interface{}
	}{
		{"FileReadMetadata", FileReadMetadata{FilePath: "/test"}, &FileReadMetadata{}},
		{"FileWriteMetadata", FileWriteMetadata{FilePath: "/test"}, &FileWriteMetadata{}},
		{"FileEditMetadata", FileEditMetadata{FilePath: "/test"}, &FileEditMetadata{}},

		{"BashMetadata", BashMetadata{Command: "test"}, &BashMetadata{}},
		{"BackgroundBashMetadata", BackgroundBashMetadata{Command: "test", PID: 1234, LogPath: "/tmp/log.txt"}, &BackgroundBashMetadata{}},
		{"GrepMetadata", GrepMetadata{Pattern: "test"}, &GrepMetadata{}},
		{"GlobMetadata", GlobMetadata{Pattern: "*.go"}, &GlobMetadata{}},
		{"TodoMetadata", TodoMetadata{Action: "read"}, &TodoMetadata{}},
		{"ThinkingMetadata", ThinkingMetadata{Thought: "test"}, &ThinkingMetadata{}},
		{"SubAgentMetadata", SubAgentMetadata{Question: "test"}, &SubAgentMetadata{}},
		{"ImageRecognitionMetadata", ImageRecognitionMetadata{ImagePath: "/test.png"}, &ImageRecognitionMetadata{}},
		{"WebFetchMetadata", WebFetchMetadata{URL: "https://test"}, &WebFetchMetadata{}},
		{"ViewBackgroundProcessesMetadata", ViewBackgroundProcessesMetadata{Count: 1}, &ViewBackgroundProcessesMetadata{}},
		{"MCPToolMetadata", MCPToolMetadata{MCPToolName: "test"}, &MCPToolMetadata{}},
	}

	for _, tt := range metadataTypes {
		t.Run(tt.name, func(t *testing.T) {
			// Test with value type
			assert.True(t, ExtractMetadata(tt.metadata, tt.target), "ExtractMetadata() failed for value type %s", tt.name)

			// Reset target for pointer test
			tt.target = reflect.New(reflect.TypeOf(tt.metadata)).Interface()

			// Test with pointer type - create a pointer to the metadata
			metadataValue := reflect.ValueOf(tt.metadata)
			metadataPtr := reflect.New(metadataValue.Type())
			metadataPtr.Elem().Set(metadataValue)

			assert.True(t, ExtractMetadata(metadataPtr.Interface().(ToolMetadata), tt.target), "ExtractMetadata() failed for pointer type %s", tt.name)
		})
	}
}
