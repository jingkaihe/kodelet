package tools

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
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
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			t.Logf("Marshaled JSON: %s", string(data))

			// Verify metadataType field is included
			var jsonMap map[string]interface{}
			json.Unmarshal(data, &jsonMap)
			if tt.result.Metadata != nil {
				if _, hasType := jsonMap["metadataType"]; !hasType {
					t.Error("Expected metadataType field in JSON")
				}
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
				} else {
					// Check that ToolType matches
					if unmarshaled.Metadata.ToolType() != tt.result.Metadata.ToolType() {
						t.Errorf("Metadata type mismatch: got %s, want %s",
							unmarshaled.Metadata.ToolType(), tt.result.Metadata.ToolType())
					}

					// IMPORTANT: After unmarshaling, metadata is always a value type, not a pointer
					metaType := reflect.TypeOf(unmarshaled.Metadata)
					if metaType.Kind() == reflect.Ptr {
						t.Errorf("Expected value type after unmarshal, got pointer type: %T", unmarshaled.Metadata)
					}

					// Log the actual type for debugging
					t.Logf("Unmarshaled metadata type: %T", unmarshaled.Metadata)
				}
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
			if tt.metadata.ToolType() != tt.expectedType {
				t.Errorf("ToolType mismatch: got %s, want %s", tt.metadata.ToolType(), tt.expectedType)
			}

			// Test value type assertion
			switch tt.expectedType {
			case "file_read":
				_, ok := tt.metadata.(FileReadMetadata)
				if ok != tt.shouldBeValue {
					t.Errorf("Value type assertion mismatch: got %v, want %v", ok, tt.shouldBeValue)
				}
				_, ok = tt.metadata.(*FileReadMetadata)
				if ok != tt.shouldBePointer {
					t.Errorf("Pointer type assertion mismatch: got %v, want %v", ok, tt.shouldBePointer)
				}
			case "web_fetch":
				_, ok := tt.metadata.(WebFetchMetadata)
				if ok != tt.shouldBeValue {
					t.Errorf("Value type assertion mismatch: got %v, want %v", ok, tt.shouldBeValue)
				}
				_, ok = tt.metadata.(*WebFetchMetadata)
				if ok != tt.shouldBePointer {
					t.Errorf("Pointer type assertion mismatch: got %v, want %v", ok, tt.shouldBePointer)
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

func TestStructuredToolResult_ComplexMetadata(t *testing.T) {
	// Test complex metadata types that have nested structures
	tests := []struct {
		name   string
		result StructuredToolResult
	}{
		{
			name: "BatchMetadata with nested results",
			result: StructuredToolResult{
				ToolName:  "batch",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &BatchMetadata{
					Description:   "Running multiple commands",
					SuccessCount:  2,
					FailureCount:  0,
					ExecutionTime: 500 * time.Millisecond,
					SubResults: []StructuredToolResult{
						{
							ToolName:  "bash",
							Success:   true,
							Timestamp: time.Now(),
							Metadata: BashMetadata{
								Command:  "echo 'test'",
								ExitCode: 0,
								Output:   "test",
							},
						},
						{
							ToolName:  "file_read",
							Success:   true,
							Timestamp: time.Now(),
							Metadata: FileReadMetadata{
								FilePath: "/test.txt",
								Lines:    []string{"content"},
							},
						},
					},
				},
			},
		},
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
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			t.Logf("Marshaled JSON: %s", string(data))

			// Unmarshal back
			var unmarshaled StructuredToolResult
			err = json.Unmarshal(data, &unmarshaled)
			if err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			// Verify the metadata type
			if unmarshaled.Metadata == nil {
				t.Fatal("Expected metadata, got nil")
			}
			if unmarshaled.Metadata.ToolType() != tt.result.Metadata.ToolType() {
				t.Errorf("Metadata type mismatch: got %s, want %s",
					unmarshaled.Metadata.ToolType(), tt.result.Metadata.ToolType())
			}

			// For BatchMetadata, verify nested results
			if tt.result.ToolName == "batch" {
				batchMeta, ok := unmarshaled.Metadata.(BatchMetadata)
				if !ok {
					t.Fatalf("Failed to assert BatchMetadata type, got %T", unmarshaled.Metadata)
				}
				if len(batchMeta.SubResults) != 2 {
					t.Errorf("Expected 2 sub-results, got %d", len(batchMeta.SubResults))
				}
				// Check that nested metadata also unmarshal correctly
				for i, subResult := range batchMeta.SubResults {
					if subResult.Metadata == nil {
						t.Errorf("Sub-result %d has nil metadata", i)
					} else {
						t.Logf("Sub-result %d metadata type: %T", i, subResult.Metadata)
					}
				}
			}
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
	if err != nil {
		t.Fatalf("Failed to marshal tool results map: %v", err)
	}

	t.Logf("Marshaled map JSON: %s", string(data))

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
		} else {
			if result.Metadata.ToolType() != original.Metadata.ToolType() {
				t.Errorf("Metadata type mismatch for %s: got %s, want %s",
					key, result.Metadata.ToolType(), original.Metadata.ToolType())
			}
			// Log the actual type after unmarshal
			t.Logf("Result %s metadata type after unmarshal: %T", key, result.Metadata)
		}
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

			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil && result.Metadata != nil {
				// Verify the metadata matches expected
				if result.Metadata.ToolType() != tt.expected.ToolType() {
					t.Errorf("ToolType mismatch: got %s, want %s",
						result.Metadata.ToolType(), tt.expected.ToolType())
				}

				// Check it's a value type (not pointer) after unmarshal
				if reflect.TypeOf(result.Metadata).Kind() == reflect.Ptr {
					t.Errorf("Expected value type after unmarshal, got pointer: %T", result.Metadata)
				}
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
				if result.FilePath != "/test.go" {
					t.Errorf("FilePath = %v, want /test.go", result.FilePath)
				}
				if len(result.Lines) != 2 {
					t.Errorf("Lines length = %v, want 2", len(result.Lines))
				}
				if result.Language != "go" {
					t.Errorf("Language = %v, want go", result.Language)
				}
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
				if result.URL != "https://example.com" {
					t.Errorf("URL = %v, want https://example.com", result.URL)
				}
				if result.Content != "test content" {
					t.Errorf("Content = %v, want test content", result.Content)
				}
				if result.Size != 100 {
					t.Errorf("Size = %v, want 100", result.Size)
				}
			},
		},
		{
			name:     "mismatched types returns false",
			metadata: FileReadMetadata{FilePath: "/test.go"},
			target:   &BashMetadata{},
			want:     false,
		},
		{
			name: "complex nested metadata",
			metadata: BatchMetadata{
				Description:  "batch test",
				SuccessCount: 2,
				FailureCount: 0,
				SubResults: []StructuredToolResult{
					{
						ToolName: "bash",
						Success:  true,
						Metadata: BashMetadata{Command: "echo test"},
					},
				},
			},
			target: &BatchMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*BatchMetadata)
				if result.Description != "batch test" {
					t.Errorf("Description = %v, want batch test", result.Description)
				}
				if result.SuccessCount != 2 {
					t.Errorf("SuccessCount = %v, want 2", result.SuccessCount)
				}
				if len(result.SubResults) != 1 {
					t.Errorf("SubResults length = %v, want 1", len(result.SubResults))
				}
			},
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
				if result.MCPToolName != "test_tool" {
					t.Errorf("MCPToolName = %v, want test_tool", result.MCPToolName)
				}
				if len(result.Parameters) != 2 {
					t.Errorf("Parameters length = %v, want 2", len(result.Parameters))
				}
				if result.Parameters["key1"] != "value1" {
					t.Errorf("Parameters[key1] = %v, want value1", result.Parameters["key1"])
				}
				if len(result.Content) != 2 {
					t.Errorf("Content length = %v, want 2", len(result.Content))
				}
			},
		},
		{
			name: "all browser metadata types",
			metadata: BrowserNavigateMetadata{
				URL:      "https://example.com",
				FinalURL: "https://example.com/home",
				Title:    "Example",
				LoadTime: 100 * time.Millisecond,
			},
			target: &BrowserNavigateMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*BrowserNavigateMetadata)
				if result.URL != "https://example.com" {
					t.Errorf("URL = %v, want https://example.com", result.URL)
				}
				if result.Title != "Example" {
					t.Errorf("Title = %v, want Example", result.Title)
				}
				if result.LoadTime != 100*time.Millisecond {
					t.Errorf("LoadTime = %v, want 100ms", result.LoadTime)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMetadata(tt.metadata, tt.target)
			if got != tt.want {
				t.Errorf("ExtractMetadata() = %v, want %v", got, tt.want)
			}
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
		{"FileMultiEditMetadata", FileMultiEditMetadata{FilePath: "/test"}, &FileMultiEditMetadata{}},
		{"BashMetadata", BashMetadata{Command: "test"}, &BashMetadata{}},
		{"BackgroundBashMetadata", BackgroundBashMetadata{Command: "test", PID: 1234, LogPath: "/tmp/log.txt"}, &BackgroundBashMetadata{}},
		{"GrepMetadata", GrepMetadata{Pattern: "test"}, &GrepMetadata{}},
		{"GlobMetadata", GlobMetadata{Pattern: "*.go"}, &GlobMetadata{}},
		{"TodoMetadata", TodoMetadata{Action: "read"}, &TodoMetadata{}},
		{"ThinkingMetadata", ThinkingMetadata{Thought: "test"}, &ThinkingMetadata{}},
		{"BatchMetadata", BatchMetadata{Description: "test"}, &BatchMetadata{}},
		{"SubAgentMetadata", SubAgentMetadata{Question: "test"}, &SubAgentMetadata{}},
		{"ImageRecognitionMetadata", ImageRecognitionMetadata{ImagePath: "/test.png"}, &ImageRecognitionMetadata{}},
		{"WebFetchMetadata", WebFetchMetadata{URL: "https://test"}, &WebFetchMetadata{}},
		{"ViewBackgroundProcessesMetadata", ViewBackgroundProcessesMetadata{Count: 1}, &ViewBackgroundProcessesMetadata{}},
		{"MCPToolMetadata", MCPToolMetadata{MCPToolName: "test"}, &MCPToolMetadata{}},
		{"BrowserNavigateMetadata", BrowserNavigateMetadata{URL: "https://test"}, &BrowserNavigateMetadata{}},
		{"BrowserClickMetadata", BrowserClickMetadata{ElementID: 123, ElementFound: true}, &BrowserClickMetadata{}},
		{"BrowserTypeMetadata", BrowserTypeMetadata{ElementID: 456, Text: "hello"}, &BrowserTypeMetadata{}},
		{"BrowserScreenshotMetadata", BrowserScreenshotMetadata{OutputPath: "/test.png", Width: 800, Height: 600}, &BrowserScreenshotMetadata{}},
		{"BrowserGetPageMetadata", BrowserGetPageMetadata{URL: "https://test"}, &BrowserGetPageMetadata{}},
		{"BrowserWaitForMetadata", BrowserWaitForMetadata{Condition: "visible", Selector: "#test"}, &BrowserWaitForMetadata{}},
	}

	for _, tt := range metadataTypes {
		t.Run(tt.name, func(t *testing.T) {
			// Test with value type
			if !ExtractMetadata(tt.metadata, tt.target) {
				t.Errorf("ExtractMetadata() failed for value type %s", tt.name)
			}

			// Reset target for pointer test
			tt.target = reflect.New(reflect.TypeOf(tt.metadata)).Interface()

			// Test with pointer type - create a pointer to the metadata
			metadataValue := reflect.ValueOf(tt.metadata)
			metadataPtr := reflect.New(metadataValue.Type())
			metadataPtr.Elem().Set(metadataValue)

			if !ExtractMetadata(metadataPtr.Interface().(ToolMetadata), tt.target) {
				t.Errorf("ExtractMetadata() failed for pointer type %s", tt.name)
			}
		})
	}
}
