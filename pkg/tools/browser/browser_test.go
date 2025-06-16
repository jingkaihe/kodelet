package browser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestNavigateToolValidation(t *testing.T) {
	tool := NavigateTool{}
	state := &mockState{}

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "valid input",
			input:       `{"url": "https://example.com", "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "missing url",
			input:       `{"timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "invalid url format",
			input:       `{"url": "not-a-url", "timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "negative timeout",
			input:       `{"url": "https://example.com", "timeout": -1}`,
			expectError: true,
		},
		{
			name:        "malformed json",
			input:       `{"url": "https://example.com"`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(state, tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestClickToolValidation(t *testing.T) {
	tool := ClickTool{}
	state := &mockState{}

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "valid input",
			input:       `{"selector": "button.submit", "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "missing selector",
			input:       `{"timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "empty selector",
			input:       `{"selector": "", "timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "negative timeout",
			input:       `{"selector": "button", "timeout": -1}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(state, tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTypeToolValidation(t *testing.T) {
	tool := TypeTool{}
	state := &mockState{}

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "valid input",
			input:       `{"selector": "input[name='email']", "text": "test@example.com", "clear": true, "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "missing selector",
			input:       `{"text": "test@example.com", "timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "missing text",
			input:       `{"selector": "input", "timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "empty text",
			input:       `{"selector": "input", "text": "", "timeout": 5000}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(state, tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWaitForToolValidation(t *testing.T) {
	tool := WaitForTool{}
	state := &mockState{}

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "valid page_load",
			input:       `{"condition": "page_load", "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "valid element_visible with selector",
			input:       `{"condition": "element_visible", "selector": ".content", "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "element_visible without selector",
			input:       `{"condition": "element_visible", "timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "invalid condition",
			input:       `{"condition": "unknown_condition", "timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "missing condition",
			input:       `{"timeout": 5000}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(state, tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractTextToolValidation(t *testing.T) {
	tool := ExtractTextTool{}
	state := &mockState{}

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "valid single element",
			input:       `{"selector": "h1", "multiple": false, "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "valid multiple elements",
			input:       `{"selector": ".item", "multiple": true, "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "missing selector",
			input:       `{"multiple": false, "timeout": 5000}`,
			expectError: true,
		},
		{
			name:        "empty selector",
			input:       `{"selector": "", "multiple": false, "timeout": 5000}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(state, tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestScreenshotToolValidation(t *testing.T) {
	tool := ScreenshotTool{}
	state := &mockState{}

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "valid png",
			input:       `{"full_page": true, "format": "png"}`,
			expectError: false,
		},
		{
			name:        "valid jpeg",
			input:       `{"full_page": false, "format": "jpeg"}`,
			expectError: false,
		},
		{
			name:        "invalid format",
			input:       `{"full_page": true, "format": "gif"}`,
			expectError: true,
		},
		{
			name:        "empty input (uses defaults)",
			input:       `{}`,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(state, tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestToolMetadata(t *testing.T) {
	tests := []struct {
		name     string
		tool     interface{ Name() string }
		expected string
	}{
		{"navigate tool", &NavigateTool{}, "browser_navigate"},
		{"get_page tool", &GetPageTool{}, "browser_get_page"},
		{"click tool", &ClickTool{}, "browser_click"},
		{"type tool", &TypeTool{}, "browser_type"},
		{"wait_for tool", &WaitForTool{}, "browser_wait_for"},
		{"extract_text tool", &ExtractTextTool{}, "browser_extract_text"},
		{"screenshot tool", &ScreenshotTool{}, "browser_screenshot"},
		{"go_back tool", &GoBackTool{}, "browser_go_back"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.tool.Name())
		})
	}
}

func TestToolSchemaGeneration(t *testing.T) {
	tests := []struct {
		name string
		tool tools.Tool
	}{
		{"browser_navigate", &NavigateTool{}},
		{"browser_get_page", &GetPageTool{}},
		{"browser_click", &ClickTool{}},
		{"browser_type", &TypeTool{}},
		{"browser_wait_for", &WaitForTool{}},
		{"browser_extract_text", &ExtractTextTool{}},
		{"browser_screenshot", &ScreenshotTool{}},
		{"browser_go_back", &GoBackTool{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := tt.tool.GenerateSchema()
			assert.NotNil(t, schema)
		})
	}
}

func TestToolResultInterfaces(t *testing.T) {
	tests := []struct {
		name   string
		result interface {
			AssistantFacing() string
			UserFacing() string
			IsError() bool
			GetError() string
			GetResult() string
		}
	}{
		{"navigate result", NavigateResult{Success: true, URL: "https://example.com", Title: "Test"}},
		{"get_page result", GetPageResult{Success: true, HTML: "<html></html>", URL: "https://example.com"}},
		{"click result", ClickResult{Success: true, ElementFound: true}},
		{"type result", TypeResult{Success: true, ElementFound: true}},
		{"wait_for result", WaitForResult{Success: true, ConditionMet: true}},
		{"extract_text result", ExtractTextResult{Success: true, Text: "extracted text", Count: 1}},
		{"screenshot result", ScreenshotResult{Success: true, OutputPath: "/path/to/screenshot.png"}},
		{"go_back result", GoBackResult{Success: true, URL: "https://example.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that all methods return non-empty strings for successful results
			assert.NotEmpty(t, tt.result.AssistantFacing())
			assert.NotEmpty(t, tt.result.UserFacing())
			assert.False(t, tt.result.IsError())
			assert.Empty(t, tt.result.GetError())
			assert.NotEmpty(t, tt.result.GetResult())
		})
	}
}

func TestSimplifyHTML(t *testing.T) {
	html := `<html>
		<head>
			<script>console.log('test');</script>
			<style>body { color: red; }</style>
		</head>
		<body class="main-body" style="color: blue;">
			<div data-id="123" aria-label="content">
				<p>This is content</p>
			</div>
		</body>
	</html>`

	simplified, truncated := SimplifyHTML(html, 1000)

	// Should remove script and style tags
	assert.NotContains(t, simplified, "console.log")
	assert.NotContains(t, simplified, "color: red")

	// Should contain actual content
	assert.Contains(t, simplified, "This is content")

	// Should not be truncated for small content
	assert.False(t, truncated)

	// Test truncation
	simplified, truncated = SimplifyHTML(html, 50)
	assert.True(t, truncated)
	assert.LessOrEqual(t, len(simplified), 50)
}

func TestTracingKVs(t *testing.T) {
	tests := []struct {
		name string
		tool interface {
			TracingKVs(string) ([]attribute.KeyValue, error)
		}
		parameters string
	}{
		{
			"navigate tool",
			&NavigateTool{},
			`{"url": "https://example.com", "timeout": 5000}`,
		},
		{
			"click tool",
			&ClickTool{},
			`{"selector": "button", "timeout": 5000}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kvs, err := tt.tool.TracingKVs(tt.parameters)
			assert.NoError(t, err)
			assert.NotEmpty(t, kvs)
		})
	}
}

// mockState implements tools.State for testing
type mockState struct{}

func (m *mockState) SetFileLastAccessed(path string, lastAccessed time.Time) error {
	return nil
}
func (m *mockState) GetFileLastAccessed(path string) (time.Time, error) {
	return time.Time{}, nil
}
func (m *mockState) ClearFileLastAccessed(path string) error                    { return nil }
func (m *mockState) TodoFilePath() (string, error)                              { return "", nil }
func (m *mockState) SetTodoFilePath(path string)                                {}
func (m *mockState) SetFileLastAccess(fileLastAccess map[string]time.Time)      {}
func (m *mockState) FileLastAccess() map[string]time.Time                       { return nil }
func (m *mockState) BasicTools() []tools.Tool                                   { return nil }
func (m *mockState) MCPTools() []tools.Tool                                     { return nil }
func (m *mockState) Tools() []tools.Tool                                        { return nil }
func (m *mockState) AddBackgroundProcess(process tools.BackgroundProcess) error { return nil }
func (m *mockState) GetBackgroundProcesses() []tools.BackgroundProcess          { return nil }
func (m *mockState) RemoveBackgroundProcess(pid int) error                      { return nil }
func (m *mockState) GetBrowserManager() tools.BrowserManager                    { return nil }
func (m *mockState) SetBrowserManager(manager tools.BrowserManager)             {}
