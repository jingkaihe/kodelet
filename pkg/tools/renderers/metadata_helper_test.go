package renderers

import (
	"reflect"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestExtractMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata tools.ToolMetadata
		target   interface{}
		want     bool
		validate func(t *testing.T, target interface{})
	}{
		{
			name:     "nil metadata returns false",
			metadata: nil,
			target:   &tools.FileReadMetadata{},
			want:     false,
		},
		{
			name:     "nil target returns false",
			metadata: tools.FileReadMetadata{FilePath: "/test.go"},
			target:   nil,
			want:     false,
		},
		{
			name:     "non-pointer target returns false",
			metadata: tools.FileReadMetadata{FilePath: "/test.go"},
			target:   tools.FileReadMetadata{},
			want:     false,
		},
		{
			name: "value type to matching pointer target",
			metadata: tools.FileReadMetadata{
				FilePath: "/test.go",
				Lines:    []string{"line1", "line2"},
				Language: "go",
			},
			target: &tools.FileReadMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*tools.FileReadMetadata)
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
			metadata: &tools.WebFetchMetadata{
				URL:     "https://example.com",
				Content: "test content",
				Size:    100,
			},
			target: &tools.WebFetchMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*tools.WebFetchMetadata)
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
			metadata: tools.FileReadMetadata{FilePath: "/test.go"},
			target:   &tools.BashMetadata{},
			want:     false,
		},
		{
			name: "complex nested metadata",
			metadata: tools.BatchMetadata{
				Description:  "batch test",
				SuccessCount: 2,
				FailureCount: 0,
				SubResults: []tools.StructuredToolResult{
					{
						ToolName: "bash",
						Success:  true,
						Metadata: tools.BashMetadata{Command: "echo test"},
					},
				},
			},
			target: &tools.BatchMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*tools.BatchMetadata)
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
			metadata: &tools.MCPToolMetadata{
				MCPToolName: "test_tool",
				ServerName:  "test_server",
				Parameters: map[string]any{
					"key1": "value1",
					"key2": 42,
				},
				Content: []tools.MCPContent{
					{Type: "text", Text: "content1"},
					{Type: "code", Text: "content2"},
				},
			},
			target: &tools.MCPToolMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*tools.MCPToolMetadata)
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
			metadata: tools.BrowserNavigateMetadata{
				URL:      "https://example.com",
				FinalURL: "https://example.com/home",
				Title:    "Example",
				LoadTime: 100 * time.Millisecond,
			},
			target: &tools.BrowserNavigateMetadata{},
			want:   true,
			validate: func(t *testing.T, target interface{}) {
				result := target.(*tools.BrowserNavigateMetadata)
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
			got := extractMetadata(tt.metadata, tt.target)
			if got != tt.want {
				t.Errorf("extractMetadata() = %v, want %v", got, tt.want)
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
		metadata tools.ToolMetadata
		target   interface{}
	}{
		{"FileReadMetadata", tools.FileReadMetadata{FilePath: "/test"}, &tools.FileReadMetadata{}},
		{"FileWriteMetadata", tools.FileWriteMetadata{FilePath: "/test"}, &tools.FileWriteMetadata{}},
		{"FileEditMetadata", tools.FileEditMetadata{FilePath: "/test"}, &tools.FileEditMetadata{}},
		{"FileMultiEditMetadata", tools.FileMultiEditMetadata{FilePath: "/test"}, &tools.FileMultiEditMetadata{}},
		{"BashMetadata", tools.BashMetadata{Command: "test"}, &tools.BashMetadata{}},
		{"GrepMetadata", tools.GrepMetadata{Pattern: "test"}, &tools.GrepMetadata{}},
		{"GlobMetadata", tools.GlobMetadata{Pattern: "*.go"}, &tools.GlobMetadata{}},
		{"TodoMetadata", tools.TodoMetadata{Action: "read"}, &tools.TodoMetadata{}},
		{"ThinkingMetadata", tools.ThinkingMetadata{Thought: "test"}, &tools.ThinkingMetadata{}},
		{"BatchMetadata", tools.BatchMetadata{Description: "test"}, &tools.BatchMetadata{}},
		{"SubAgentMetadata", tools.SubAgentMetadata{Question: "test"}, &tools.SubAgentMetadata{}},
		{"ImageRecognitionMetadata", tools.ImageRecognitionMetadata{ImagePath: "/test.png"}, &tools.ImageRecognitionMetadata{}},
		{"WebFetchMetadata", tools.WebFetchMetadata{URL: "https://test"}, &tools.WebFetchMetadata{}},
		{"ViewBackgroundProcessesMetadata", tools.ViewBackgroundProcessesMetadata{Count: 1}, &tools.ViewBackgroundProcessesMetadata{}},
		{"MCPToolMetadata", tools.MCPToolMetadata{MCPToolName: "test"}, &tools.MCPToolMetadata{}},
		{"BrowserNavigateMetadata", tools.BrowserNavigateMetadata{URL: "https://test"}, &tools.BrowserNavigateMetadata{}},
		{"BrowserClickMetadata", tools.BrowserClickMetadata{ElementID: 123, ElementFound: true}, &tools.BrowserClickMetadata{}},
		{"BrowserTypeMetadata", tools.BrowserTypeMetadata{ElementID: 456, Text: "hello"}, &tools.BrowserTypeMetadata{}},
		{"BrowserScreenshotMetadata", tools.BrowserScreenshotMetadata{OutputPath: "/test.png", Width: 800, Height: 600}, &tools.BrowserScreenshotMetadata{}},
		{"BrowserGetPageMetadata", tools.BrowserGetPageMetadata{URL: "https://test"}, &tools.BrowserGetPageMetadata{}},
		{"BrowserWaitForMetadata", tools.BrowserWaitForMetadata{Condition: "visible", Selector: "#test"}, &tools.BrowserWaitForMetadata{}},
	}

	for _, tt := range metadataTypes {
		t.Run(tt.name, func(t *testing.T) {
			// Test with value type
			if !extractMetadata(tt.metadata, tt.target) {
				t.Errorf("extractMetadata() failed for value type %s", tt.name)
			}

			// Reset target for pointer test
			tt.target = reflect.New(reflect.TypeOf(tt.metadata)).Interface()

			// Test with pointer type - create a pointer to the metadata
			metadataValue := reflect.ValueOf(tt.metadata)
			metadataPtr := reflect.New(metadataValue.Type())
			metadataPtr.Elem().Set(metadataValue)

			if !extractMetadata(metadataPtr.Interface().(tools.ToolMetadata), tt.target) {
				t.Errorf("extractMetadata() failed for pointer type %s", tt.name)
			}
		})
	}
}

func BenchmarkExtractMetadata(b *testing.B) {
	metadata := tools.FileReadMetadata{
		FilePath:  "/test/file.go",
		Lines:     []string{"line1", "line2", "line3", "line4", "line5"},
		Language:  "go",
		Offset:    0,
		Truncated: false,
	}
	target := &tools.FileReadMetadata{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractMetadata(metadata, target)
	}
}
