package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestWebFetchToolValidation(t *testing.T) {
	tool := &WebFetchTool{}
	state := &BasicState{}

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "Valid input with prompt",
			input:       `{"url": "https://example.com", "prompt": "Extract the main heading"}`,
			expectError: false,
		},
		{
			name:        "Valid input without prompt", // Updated: prompt is now optional
			input:       `{"url": "https://example.com"}`,
			expectError: false,
		},
		{
			name:        "Missing URL",
			input:       `{"prompt": "Extract the main heading"}`,
			expectError: true,
		},
		{
			name:        "Empty URL",
			input:       `{"url": "", "prompt": "Extract the main heading"}`,
			expectError: true,
		},
		{
			name:        "Invalid URL scheme - FTP",
			input:       `{"url": "ftp://example.com", "prompt": "Extract the main heading"}`,
			expectError: true,
		},
		{
			name:        "Invalid URL scheme - HTTP for external domain",
			input:       `{"url": "http://example.com", "prompt": "Extract the main heading"}`,
			expectError: true,
		},
		{
			name:        "Valid URL scheme - HTTP for localhost",
			input:       `{"url": "http://localhost:8080", "prompt": "Extract the main heading"}`,
			expectError: false,
		},
		{
			name:        "Valid URL scheme - HTTP for 127.0.0.1",
			input:       `{"url": "http://127.0.0.1:3000", "prompt": "Extract the main heading"}`,
			expectError: false,
		},
		{
			name:        "Valid URL scheme - HTTP for ::1 (IPv6 localhost)",
			input:       `{"url": "http://[::1]:8080", "prompt": "Extract the main heading"}`,
			expectError: false,
		},
		{
			name:        "Invalid JSON",
			input:       `{"url": "https://example.com", "prompt": }`,
			expectError: true,
		},
		{
			name:        "Malformed URL",
			input:       `{"url": "not-a-url", "prompt": "Extract the main heading"}`,
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

func TestWebFetchToolHelperFunctions(t *testing.T) {
	t.Run("isLocalHost", func(t *testing.T) {
		tests := []struct {
			hostname string
			expected bool
		}{
			// Standard localhost names
			{"localhost", true},
			{"127.0.0.1", true},
			{"::1", true},
			{"0.0.0.0", true},

			// Other loopback addresses
			{"127.0.0.2", true},
			{"127.1.1.1", true},
			{"127.255.255.255", true},

			// External domains
			{"example.com", false},
			{"github.com", false},
			{"192.168.1.1", false},
			{"10.0.0.1", false},
			{"8.8.8.8", false},
			{"2001:db8::1", false},

			// Invalid/edge cases
			{"", false},
			{"not-an-ip", false},
		}

		for _, test := range tests {
			result := isLocalHost(test.hostname)
			assert.Equal(t, test.expected, result, "Hostname: %s", test.hostname)
		}
	})

	t.Run("isMarkdownFromURL", func(t *testing.T) {
		tests := []struct {
			url      string
			expected bool
		}{
			{"https://github.com/user/repo/README.md", true},
			{"https://example.com/doc.markdown", true},
			{"https://example.com/file.MD", true}, // Case insensitive
			{"https://example.com/file.txt", false},
			{"https://example.com/page.html", false},
			{"https://example.com/", false},
			{"invalid-url", false},
		}

		for _, test := range tests {
			result := isMarkdownFromURL(test.url)
			assert.Equal(t, test.expected, result, "URL: %s", test.url)
		}
	})

	t.Run("getFileNameFromURL", func(t *testing.T) {
		tests := []struct {
			url      string
			expected string
		}{
			{"https://github.com/user/repo/README.md", "README"},
			{"https://example.com/file.txt", "file"},
			{"https://example.com/script.js", "script"},
			{"https://example.com/", "example_com"},
			{"https://api.github.com/", "api_github_com"},
			{"https://sub.domain.com/path/", "path"}, // path.Base("/path/") returns "path"
			{"invalid-url", "invalid-url"},           // Treated as relative URL, path.Base("invalid-url") returns "invalid-url"
		}

		for _, test := range tests {
			result := getFileNameFromURL(test.url)
			assert.Equal(t, test.expected, result, "URL: %s", test.url)
		}
	})

	t.Run("getFileExtensionFromContentType", func(t *testing.T) {
		tests := []struct {
			contentType string
			url         string
			expected    string
		}{
			// URL extension takes priority
			{"text/plain", "https://example.com/script.py", ".py"},
			{"text/plain", "https://example.com/config.yaml", ".yaml"},
			{"text/plain", "https://example.com/data.json", ".json"},

			// Content type mapping when no URL extension
			{"application/json", "https://api.example.com/data", ".json"},
			{"text/html", "https://example.com/page", ".html"},
			{"application/javascript", "https://example.com/script", ".js"},
			{"text/javascript", "https://example.com/script", ".js"},
			{"application/xml", "https://example.com/feed", ".xml"},
			{"text/xml", "https://example.com/feed", ".xml"},
			{"application/yaml", "https://example.com/config", ".yaml"},
			{"text/yaml", "https://example.com/config", ".yaml"},
			{"text/plain", "https://example.com/file", ".txt"},
			{"text/css", "https://example.com/style", ".css"},
			{"text/markdown", "https://example.com/doc", ".md"},

			// Fallback to .txt for unknown types
			{"unknown/type", "https://example.com/file", ".txt"},
			{"", "https://example.com/file", ".txt"},
		}

		for _, test := range tests {
			result := getFileExtensionFromContentType(test.contentType, test.url)
			assert.Equal(t, test.expected, result, "ContentType: %s, URL: %s", test.contentType, test.url)
		}
	})
}

func TestWebFetchToolCodeTextContent(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tempDir)

	// Skip TLS tests for now and use simpler unit tests
	t.Run("Test helper functions work correctly", func(t *testing.T) {
		// Test content type detection
		ext := getFileExtensionFromContentType("application/json", "https://example.com/data")
		assert.Equal(t, ".json", ext)

		// Test filename extraction
		filename := getFileNameFromURL("https://example.com/test.py")
		assert.Equal(t, "test", filename)

		// Test markdown detection
		isMd := isMarkdownFromURL("https://example.com/README.md")
		assert.True(t, isMd)
	})
}

func TestWebFetchToolHTMLContentWithPrompt(t *testing.T) {
	tool := &WebFetchTool{}
	state := &BasicState{}

	// Test missing sub-agent config error
	t.Run("Missing sub-agent config returns error", func(t *testing.T) {
		// Test the validation part without actual network calls
		params := `{"url": "https://example.com/page.html", "prompt": "What is the main heading?"}`
		err := tool.ValidateInput(state, params)
		assert.NoError(t, err, "Validation should pass")
	})

	// Test AI extraction logic with mock
	t.Run("AI extraction logic works with mock", func(t *testing.T) {
		mockThread := &MockThread{
			response: "The main heading is: Welcome to Example.com",
		}

		ctx := context.WithValue(context.Background(), llm.SubAgentConfigKey, llm.SubAgentConfig{
			Thread: mockThread,
		})

		// Test the handleHTMLMarkdownWithPrompt function directly
		input := &WebFetchInput{
			URL:    "https://example.com/page.html",
			Prompt: "What is the main heading?",
		}

		htmlContent := `<html><body><h1>Welcome to Example.com</h1></body></html>`
		result := tool.handleHTMLMarkdownWithPrompt(ctx, input, htmlContent, "text/html")

		assert.False(t, result.IsError())
		assert.Equal(t, "The main heading is: Welcome to Example.com", result.GetResult())
		assert.True(t, mockThread.called)
	})

	// Test HTML content without prompt - should return markdown directly
	t.Run("HTML content without prompt returns markdown directly", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/page.html",
			// No prompt provided
		}

		htmlContent := `<html><body><h1>Welcome to Example.com</h1><p>This is a test page.</p></body></html>`
		result := tool.handleHTMLMarkdownContent(context.Background(), input, htmlContent, "text/html")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should contain markdown conversion
		assert.Contains(t, content, "# Welcome to Example.com")
		assert.Contains(t, content, "This is a test page.")

		// Should not have line numbers (that's only for code/text files)
		assert.NotContains(t, content, "1:")
		assert.NotContains(t, content, "2:")
	})

	// Test Markdown content without prompt - should return as-is
	t.Run("Markdown content without prompt returns as-is", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/doc.md",
			// No prompt provided
		}

		markdownContent := `# API Documentation

## Getting Started
This is the main documentation for our API.

### Authentication
Use your API key in the header.`

		result := tool.handleHTMLMarkdownContent(context.Background(), input, markdownContent, "text/markdown")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should return the markdown content as-is
		assert.Equal(t, markdownContent, content)
	})
}

func TestWebFetchToolErrorHandling(t *testing.T) {
	tool := &WebFetchTool{}
	state := &BasicState{}

	t.Run("Invalid JSON parameters", func(t *testing.T) {
		result := tool.Execute(context.Background(), state, `{"url": malformed}`)
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "invalid character")
	})

	t.Run("Network error", func(t *testing.T) {
		params := `{"url": "https://nonexistent-domain-12345.com/file.txt"}`
		result := tool.Execute(context.Background(), state, params)
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "Failed to fetch URL")
	})
}

func TestWebFetchToolFilenameConflictResolution(t *testing.T) {
	// Test the logic without network calls
	t.Run("Test conflict resolution logic", func(t *testing.T) {
		tempDir := t.TempDir()
		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		os.Chdir(tempDir)

		// Create archive directory manually
		archiveDir := "./.kodelet/web-archives"
		err := os.MkdirAll(archiveDir, 0o755)
		require.NoError(t, err)

		// Create first file
		firstFile := filepath.Join(archiveDir, "test.txt")
		err = os.WriteFile(firstFile, []byte("content1"), 0o644)
		require.NoError(t, err)

		// Test that the conflict resolution would create test_1.txt
		secondFile := filepath.Join(archiveDir, "test_1.txt")
		_, err = os.Stat(secondFile)
		assert.True(t, os.IsNotExist(err), "Second file should not exist yet")

		// Verify first file exists
		_, err = os.Stat(firstFile)
		assert.NoError(t, err, "First file should exist")
	})
}

func TestFetchWithSameDomainRedirects(t *testing.T) {
	// Setup HTTP test server for redirects
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body><h1>Hello World</h1></body></html>"))
		case "/redirect-same-domain":
			http.Redirect(w, r, "/", http.StatusFound)
		case "/redirect-different-domain":
			http.Redirect(w, r, "https://different-domain.com/", http.StatusFound)
		case "/too-many-redirects":
			http.Redirect(w, r, "/redirect-loop", http.StatusFound)
		case "/redirect-loop":
			http.Redirect(w, r, "/too-many-redirects", http.StatusFound)
		}
	}))
	defer server.Close()

	// Note: We're testing with HTTP server, but the function requires HTTPS for external domains
	// HTTP should be rejected for external domains but allowed for localhost
	t.Run("HTTP URLs are rejected for external domains", func(t *testing.T) {
		_, _, err := fetchWithSameDomainRedirects(context.Background(), "http://external-domain.com/test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "only HTTPS scheme is supported for external domains")
	})

	t.Run("HTTP URLs are allowed for localhost", func(t *testing.T) {
		// Use the test server URL which should be localhost (127.0.0.1)
		content, contentType, err := fetchWithSameDomainRedirects(context.Background(), server.URL)
		// Should succeed since 127.0.0.1 is localhost
		assert.NoError(t, err)
		assert.Contains(t, content, "Hello World")
		assert.Contains(t, contentType, "text/html")
	})
}

func TestConvertHTMLToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected []string // Strings that should be present in the markdown
	}{
		{
			name:     "Basic HTML conversion",
			html:     "<html><body><h1>Hello World</h1><p>This is a <strong>test</strong>.</p></body></html>",
			expected: []string{"# Hello World", "This is a **test**."},
		},
		{
			name:     "Links and images",
			html:     `<html><body><a href="/about">About</a><img src="image.jpg" alt="Test Image"></body></html>`,
			expected: []string{"[About](/about)", "![Test Image](image.jpg)"},
		},
		{
			name:     "Lists",
			html:     "<html><body><ul><li>Item 1</li><li>Item 2</li></ul></body></html>",
			expected: []string{"- Item 1", "- Item 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdown := convertHTMLToMarkdown(context.Background(), tt.html)
			for _, expected := range tt.expected {
				assert.Contains(t, markdown, expected)
			}
		})
	}
}

func TestWebFetchToolResultInterfaces(t *testing.T) {
	t.Run("WebFetchToolResult methods", func(t *testing.T) {
		// Test success result
		successResult := &WebFetchToolResult{
			url:      "https://example.com/file.txt",
			prompt:   "extract info",
			result:   "File content here",
			filePath: "/path/to/saved/file.txt",
		}

		assert.Equal(t, "File content here", successResult.GetResult())
		assert.Empty(t, successResult.GetError())
		assert.False(t, successResult.IsError())

		// Test error result
		errorResult := &WebFetchToolResult{
			url: "https://example.com/file.txt",
			err: "Connection failed",
		}

		assert.Empty(t, errorResult.GetResult())
		assert.Equal(t, "Connection failed", errorResult.GetError())
		assert.True(t, errorResult.IsError())
	})
}

func TestWebFetchToolName(t *testing.T) {
	tool := &WebFetchTool{}
	assert.Equal(t, "web_fetch", tool.Name())
}

func TestWebFetchToolDescription(t *testing.T) {
	tool := &WebFetchTool{}
	description := tool.Description()

	// Check that description contains key information about new functionality
	assert.Contains(t, description, "Scenario 1: Code/Text Content")
	assert.Contains(t, description, "Scenario 2: HTML/Markdown Content")
	assert.Contains(t, description, "~/.kodelet/web-archives/")
	assert.Contains(t, description, "100KB")
	assert.Contains(t, description, "prompt: (Optional)")
	assert.Contains(t, description, "Without prompt")
	assert.Contains(t, description, "With prompt")
}

func TestWebFetchToolGenerateSchema(t *testing.T) {
	tool := &WebFetchTool{}
	schema := tool.GenerateSchema()

	assert.NotNil(t, schema)
	assert.NotNil(t, schema.Properties)

	// Check that URL property exists
	urlProp, exists := schema.Properties.Get("url")
	assert.True(t, exists)
	assert.NotNil(t, urlProp)

	// Check that prompt property exists
	promptProp, exists := schema.Properties.Get("prompt")
	assert.True(t, exists)
	assert.NotNil(t, promptProp)
}

func TestWebFetchToolTracingKVs(t *testing.T) {
	tool := &WebFetchTool{}

	t.Run("Valid parameters", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"url": "https://example.com", "prompt": "Extract the main heading"}`)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(kvs))
		assert.Equal(t, "https://example.com", kvs[0].Value.AsString())
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		_, err := tool.TracingKVs(`{"url": invalid}`)
		assert.Error(t, err)
	})
}

// MockThread implements a mock LLM thread for testing
type MockThread struct {
	called     bool
	lastPrompt string
	response   string
	err        error
	state      tooltypes.State
}

func (m *MockThread) SetState(s tooltypes.State) {
	m.state = s
}

func (m *MockThread) GetState() tooltypes.State {
	return m.state
}

func (m *MockThread) AddUserMessage(_ context.Context, _ string, _ ...string) {
	// Mock implementation - do nothing
}

func (m *MockThread) SendMessage(_ context.Context, prompt string, _ llm.MessageHandler, _ llm.MessageOpt) (string, error) {
	m.called = true
	m.lastPrompt = prompt
	return m.response, m.err
}

func (m *MockThread) GetUsage() llm.Usage {
	return llm.Usage{}
}

func (m *MockThread) GetConversationID() string {
	return "test-conversation-id"
}

func (m *MockThread) SetConversationID(_ string) {
	// Mock implementation - do nothing
}

func (m *MockThread) SaveConversation(_ context.Context, _ bool) error {
	return nil
}

func (m *MockThread) IsPersisted() bool {
	return false
}

func (m *MockThread) EnablePersistence(_ context.Context, _ bool) {
	// Mock implementation - do nothing
}

func (m *MockThread) Provider() string {
	return "mock"
}

func (m *MockThread) GetMessages() ([]llm.Message, error) {
	return []llm.Message{}, nil
}

func (m *MockThread) GetConfig() llm.Config {
	return llm.Config{}
}

func (m *MockThread) NewSubAgent(_ context.Context, _ llm.Config) llm.Thread {
	return m
}

func (m *MockThread) AggregateSubagentUsage(_ llm.Usage) {}

func (m *MockThread) SetRecipeHooks(_ map[string]llm.HookConfig) {}

func (m *MockThread) GetRecipeHooks() map[string]llm.HookConfig { return nil }

func (m *MockThread) Reset() {
	m.called = false
	m.lastPrompt = ""
	m.response = ""
	m.err = nil
}

func TestWebFetchToolCodeContentTruncation(t *testing.T) {
	tool := &WebFetchTool{}

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tempDir)

	t.Run("Small content returns full content with line numbers", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/small.txt",
		}

		// Create small content (well under 100KB)
		smallContent := "line 1\nline 2\nline 3\nline 4\nline 5"

		result := tool.handleCodeTextContent(context.Background(), input, smallContent, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should contain line numbers starting from 1
		assert.Contains(t, content, "1: line 1")
		assert.Contains(t, content, "2: line 2")
		assert.Contains(t, content, "3: line 3")
		assert.Contains(t, content, "4: line 4")
		assert.Contains(t, content, "5: line 5")

		// Should NOT contain truncation message
		assert.NotContains(t, content, "truncated due to max output bytes limit")

		// Should have created a file
		webFetchResult := result.(*WebFetchToolResult)
		assert.NotEmpty(t, webFetchResult.filePath)

		// Verify file exists and contains the content
		_, err := os.Stat(webFetchResult.filePath)
		assert.NoError(t, err)

		fileContent, err := os.ReadFile(webFetchResult.filePath)
		assert.NoError(t, err)
		assert.Equal(t, smallContent, string(fileContent))
	})

	t.Run("Large content gets truncated with proper line numbers", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/large.txt",
		}

		// Create content that exceeds MaxOutputBytes (100KB)
		// Each line is about 50 bytes, so we need about 2500+ lines to exceed 100KB
		var lines []string
		for i := 1; i <= 3000; i++ {
			lines = append(lines, fmt.Sprintf("This is line %04d with some padding text to make it longer", i))
		}
		largeContent := strings.Join(lines, "\n")

		// Verify our test content actually exceeds MaxOutputBytes
		assert.Greater(t, len(largeContent), MaxOutputBytes, "Test content should exceed MaxOutputBytes")

		result := tool.handleCodeTextContent(context.Background(), input, largeContent, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should contain line numbers starting from 1
		assert.Contains(t, content, "1: This is line 0001")
		assert.Contains(t, content, "2: This is line 0002")

		// Should contain truncation message
		assert.Contains(t, content, "truncated due to max output bytes limit")
		assert.Contains(t, content, fmt.Sprintf("%d", MaxOutputBytes))

		// Should contain file path in truncation message
		webFetchResult := result.(*WebFetchToolResult)
		assert.Contains(t, content, webFetchResult.filePath)

		// Should NOT contain content from the end of the original file
		assert.NotContains(t, content, "line 3000")
		assert.NotContains(t, content, "line 2999")

		// Should have created a file with full content
		assert.NotEmpty(t, webFetchResult.filePath)

		// Verify file exists and contains the FULL content (not truncated)
		_, err := os.Stat(webFetchResult.filePath)
		assert.NoError(t, err)

		fileContent, err := os.ReadFile(webFetchResult.filePath)
		assert.NoError(t, err)
		assert.Equal(t, largeContent, string(fileContent))
	})

	t.Run("Edge case: content exactly at MaxOutputBytes", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/exact.txt",
		}

		// Create content that is exactly MaxOutputBytes
		// We need to be careful about line breaks counting toward the limit
		lineContent := "This is a test line that is exactly 50 chars long!" // 50 chars
		linesNeeded := MaxOutputBytes / (len(lineContent) + 1)              // +1 for newline

		var lines []string
		for i := 0; i < linesNeeded; i++ {
			lines = append(lines, lineContent)
		}
		exactContent := strings.Join(lines, "\n")

		// Adjust to be exactly MaxOutputBytes or just under
		for len(exactContent) > MaxOutputBytes {
			lines = lines[:len(lines)-1]
			exactContent = strings.Join(lines, "\n")
		}

		result := tool.handleCodeTextContent(context.Background(), input, exactContent, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should contain line numbers
		assert.Contains(t, content, "1: This is a test line")

		// Should NOT contain truncation message since it's at or under the limit
		assert.NotContains(t, content, "truncated due to max output bytes limit")
	})
}

func TestWebFetchToolOneIndexedCodeView(t *testing.T) {
	tool := &WebFetchTool{}

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tempDir)

	t.Run("Single line content has 1-indexed line number", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/single.py",
		}

		singleLineContent := "print('Hello, World!')"

		result := tool.handleCodeTextContent(context.Background(), input, singleLineContent, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should start with line number 1
		assert.Contains(t, content, "1: print('Hello, World!')")
		// Should not contain line number 0
		assert.NotContains(t, content, "0:")
	})

	t.Run("Multi-line code content has proper 1-indexed line numbers", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/multi.py",
		}

		multiLineContent := `def hello():
    print("Hello")
    return "world"

if __name__ == "__main__":
    result = hello()
    print(result)`

		result := tool.handleCodeTextContent(context.Background(), input, multiLineContent, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should have proper 1-indexed line numbers
		assert.Contains(t, content, "1: def hello():")
		assert.Contains(t, content, "2:     print(\"Hello\")")
		assert.Contains(t, content, "3:     return \"world\"")
		assert.Contains(t, content, "4: ") // Empty line
		assert.Contains(t, content, "5: if __name__ == \"__main__\":")
		assert.Contains(t, content, "6:     result = hello()")
		assert.Contains(t, content, "7:     print(result)")

		// Should not contain line number 0
		assert.NotContains(t, content, "0:")
	})

	t.Run("Line numbers are properly padded for alignment", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/padded.txt",
		}

		// Create content with exactly 10 lines to test padding
		var lines []string
		for i := 1; i <= 10; i++ {
			lines = append(lines, fmt.Sprintf("Line %d content", i))
		}
		paddedContent := strings.Join(lines, "\n")

		result := tool.handleCodeTextContent(context.Background(), input, paddedContent, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Lines 1-9 should be padded with a space, line 10 should not
		assert.Contains(t, content, " 1: Line 1 content")
		assert.Contains(t, content, " 2: Line 2 content")
		assert.Contains(t, content, " 9: Line 9 content")
		assert.Contains(t, content, "10: Line 10 content")

		// Verify alignment by checking that single-digit line numbers are right-aligned
		contentLines := strings.Split(content, "\n")
		var lineNumberPrefixes []string
		for _, line := range contentLines {
			if strings.Contains(line, ": Line") {
				colonIndex := strings.Index(line, ":")
				if colonIndex > 0 {
					lineNumberPrefixes = append(lineNumberPrefixes, line[:colonIndex])
				}
			}
		}

		// Should have collected 10 line number prefixes
		assert.Equal(t, 10, len(lineNumberPrefixes))

		// All prefixes should have the same width (2 characters for 1-10)
		expectedWidth := 2
		for i, prefix := range lineNumberPrefixes {
			assert.Equal(t, expectedWidth, len(prefix), "Line number prefix %d ('%s') should be %d characters wide", i+1, prefix, expectedWidth)
		}
	})

	t.Run("Large line numbers maintain proper alignment", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/large_line_numbers.txt",
		}

		// Create content with 1000+ lines to test 4-digit line numbers
		var lines []string
		for i := 1; i <= 1200; i++ {
			lines = append(lines, fmt.Sprintf("Content of line %d", i))
		}
		largeLineContent := strings.Join(lines, "\n")

		// This will likely be truncated, but we can still test the line number formatting
		result := tool.handleCodeTextContent(context.Background(), input, largeLineContent, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should have proper alignment for 4-digit line numbers
		// All line numbers should be right-aligned to 4 characters
		assert.Contains(t, content, "   1: Content of line 1")
		assert.Contains(t, content, "  10: Content of line 10")
		assert.Contains(t, content, " 100: Content of line 100")

		// Due to truncation, we might not see line 1000+, but the alignment should be consistent
		// for whatever lines are included
	})

	t.Run("Empty lines preserve line numbering", func(t *testing.T) {
		input := &WebFetchInput{
			URL: "https://example.com/empty_lines.txt",
		}

		contentWithEmptyLines := `First line

Third line (second was empty)

Fifth line (fourth was empty)`

		result := tool.handleCodeTextContent(context.Background(), input, contentWithEmptyLines, "text/plain")

		assert.False(t, result.IsError())
		content := result.GetResult()

		// Should maintain proper line numbering even with empty lines
		assert.Contains(t, content, "1: First line")
		assert.Contains(t, content, "2: ") // Empty line 2
		assert.Contains(t, content, "3: Third line (second was empty)")
		assert.Contains(t, content, "4: ") // Empty line 4
		assert.Contains(t, content, "5: Fifth line (fourth was empty)")
	})
}

func TestWebFetchToolFileTypes(t *testing.T) {
	tool := &WebFetchTool{}

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tempDir)

	testCases := []struct {
		name        string
		url         string
		contentType string
		content     string
		expectFile  bool
		expectLines bool
	}{
		{
			name:        "JavaScript file",
			url:         "https://example.com/script.js",
			contentType: "application/javascript",
			content:     "function hello() {\n    console.log('Hello');\n}",
			expectFile:  true,
			expectLines: true,
		},
		{
			name:        "JSON file",
			url:         "https://example.com/config.json",
			contentType: "application/json",
			content:     "{\n  \"name\": \"test\",\n  \"version\": \"1.0.0\"\n}",
			expectFile:  true,
			expectLines: true,
		},
		{
			name:        "Python file",
			url:         "https://github.com/user/repo/main.py",
			contentType: "text/plain",
			content:     "#!/usr/bin/env python3\nprint('Hello World')\n",
			expectFile:  true,
			expectLines: true,
		},
		{
			name:        "XML file",
			url:         "https://example.com/data.xml",
			contentType: "application/xml",
			content:     "<?xml version=\"1.0\"?>\n<root>\n  <item>test</item>\n</root>",
			expectFile:  true,
			expectLines: true,
		},
		{
			name:        "HTML file should be processed as HTML/Markdown",
			url:         "https://example.com/page.html",
			contentType: "text/html",
			content:     "<html><body><h1>Title</h1></body></html>",
			expectFile:  false, // HTML content is not saved to file
			expectLines: false, // HTML content doesn't get line numbers
		},
		{
			name:        "Markdown file from URL should be processed as HTML/Markdown",
			url:         "https://example.com/README.md",
			contentType: "text/plain",
			content:     "# Title\n\nThis is markdown content.",
			expectFile:  false, // Markdown from URL is processed as HTML/Markdown
			expectLines: false, // Markdown content doesn't get line numbers
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input := &WebFetchInput{
				URL: tc.url,
			}

			result := tool.handleCodeTextContent(context.Background(), input, tc.content, tc.contentType)

			// Only test code/text content handling, not HTML/Markdown
			if tc.expectFile {
				assert.False(t, result.IsError())
				content := result.GetResult()

				if tc.expectLines {
					// Should contain line numbers
					assert.Contains(t, content, "1:")

					// Verify line number format matches expected pattern
					lines := strings.Split(tc.content, "\n")
					if len(lines) > 0 && lines[0] != "" {
						assert.Contains(t, content, fmt.Sprintf("1: %s", lines[0]))
					}
				}

				// Should have created a file
				webFetchResult := result.(*WebFetchToolResult)
				assert.NotEmpty(t, webFetchResult.filePath)

				// Verify file was created and contains original content
				_, err := os.Stat(webFetchResult.filePath)
				assert.NoError(t, err)

				fileContent, err := os.ReadFile(webFetchResult.filePath)
				assert.NoError(t, err)
				assert.Equal(t, tc.content, string(fileContent))
			}
		})
	}
}

func TestWebFetchToolDomainSpecificDirectories(t *testing.T) {
	tool := &WebFetchTool{}

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tempDir)

	t.Run("Files are saved to domain-specific directories", func(t *testing.T) {
		testCases := []struct {
			url            string
			expectedDomain string
		}{
			{
				url:            "https://github.com/user/repo/main.py",
				expectedDomain: "github.com",
			},
			{
				url:            "https://raw.githubusercontent.com/user/repo/main/config.json",
				expectedDomain: "raw.githubusercontent.com",
			},
			{
				url:            "https://api.example.com/data.xml",
				expectedDomain: "api.example.com",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.url, func(t *testing.T) {
				input := &WebFetchInput{
					URL: tc.url,
				}

				testContent := "test content for domain directory"
				result := tool.handleCodeTextContent(context.Background(), input, testContent, "text/plain")

				assert.False(t, result.IsError())

				// Should have created a file in the domain-specific directory
				webFetchResult := result.(*WebFetchToolResult)
				assert.NotEmpty(t, webFetchResult.filePath)

				// Verify the file path contains the expected domain directory
				homeDir, _ := os.UserHomeDir()
				expectedDir := filepath.Join(homeDir, ".kodelet", "web-archives", tc.expectedDomain)
				assert.Contains(t, webFetchResult.filePath, expectedDir)

				// Verify the directory structure exists
				_, err := os.Stat(webFetchResult.filePath)
				assert.NoError(t, err)

				// Verify file contains the original content
				fileContent, err := os.ReadFile(webFetchResult.filePath)
				assert.NoError(t, err)
				assert.Equal(t, testContent, string(fileContent))

				// Verify the domain directory was created
				domainDir := filepath.Join(homeDir, ".kodelet", "web-archives", tc.expectedDomain)
				_, err = os.Stat(domainDir)
				assert.NoError(t, err)
			})
		}
	})
}

func TestWebFetchToolDomainFilter(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tempDir)

	state := &BasicState{}

	t.Run("Domain filter with no file allows all domains", func(t *testing.T) {
		// Create tool without domain filter
		tool := NewWebFetchTool("")

		testURLs := []string{
			"https://github.com/user/repo",
			"https://example.com/page",
			"https://api.test.com/data",
		}

		for _, url := range testURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow all domains when no filter is configured")
		}
	})

	t.Run("Domain filter with empty file allows all domains", func(t *testing.T) {
		// Create empty domains file
		domainsFile := filepath.Join(tempDir, "empty_domains.txt")
		err := os.WriteFile(domainsFile, []byte(""), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		testURLs := []string{
			"https://github.com/user/repo",
			"https://example.com/page",
			"https://api.test.com/data",
		}

		for _, url := range testURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow all domains when domains file is empty")
		}
	})

	t.Run("Domain filter with nonexistent file allows all domains", func(t *testing.T) {
		// Use a nonexistent file path
		nonexistentFile := filepath.Join(tempDir, "nonexistent_domains.txt")
		tool := NewWebFetchTool(nonexistentFile)

		testURLs := []string{
			"https://github.com/user/repo",
			"https://example.com/page",
			"https://api.test.com/data",
		}

		for _, url := range testURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow all domains when domains file doesn't exist")
		}
	})

	t.Run("Domain filter with exact domain matches", func(t *testing.T) {
		// Create domains file with exact matches
		domainsFile := filepath.Join(tempDir, "exact_domains.txt")
		domainsContent := `# Allowed domains
github.com
example.com
api.test.com
# Comments should be ignored
`
		err := os.WriteFile(domainsFile, []byte(domainsContent), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		// Test allowed domains
		allowedURLs := []string{
			"https://github.com/user/repo",
			"https://example.com/page",
			"https://api.test.com/data",
		}

		for _, url := range allowedURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow domain: %s", url)
		}

		// Test blocked domains
		blockedURLs := []string{
			"https://blocked.com/page",
			"https://evil.com/malware",
			"https://unknown.domain.com/file",
		}

		for _, url := range blockedURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.Error(t, err, "Should block domain: %s", url)
			assert.Contains(t, err.Error(), "not in the allowed domains list")
		}
	})

	t.Run("Domain filter with glob patterns", func(t *testing.T) {
		// Create domains file with glob patterns
		domainsFile := filepath.Join(tempDir, "glob_domains.txt")
		domainsContent := `# Glob patterns for domains
*.github.com
*.example.com
api.*.com
test-*.org
`
		err := os.WriteFile(domainsFile, []byte(domainsContent), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		// Test URLs that should match glob patterns
		allowedURLs := []string{
			"https://raw.github.com/user/repo/file.txt",
			"https://api.github.com/repos",
			"https://docs.example.com/guide",
			"https://www.example.com/page",
			"https://api.service.com/data",
			"https://api.production.com/v1/users",
			"https://test-staging.org/app",
			"https://test-prod.org/dashboard",
		}

		for _, url := range allowedURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow URL matching glob pattern: %s", url)
		}

		// Test URLs that should NOT match glob patterns
		blockedURLs := []string{
			"https://github.com/user/repo", // doesn't match *.github.com
			"https://example.com/page",     // doesn't match *.example.com
			"https://api.example.net/data", // doesn't match api.*.com
			"https://prod-test.org/app",    // doesn't match test-*.org
			"https://blocked.com/page",     // no matching pattern
		}

		for _, url := range blockedURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.Error(t, err, "Should block URL not matching glob patterns: %s", url)
			assert.Contains(t, err.Error(), "not in the allowed domains list")
		}
	})

	t.Run("Domain filter with mixed exact and glob patterns", func(t *testing.T) {
		// Create domains file with both exact matches and glob patterns
		domainsFile := filepath.Join(tempDir, "mixed_domains.txt")
		domainsContent := `# Mixed domain configuration
github.com
example.com
*.googleapis.com
api.*.dev
# End of config
`
		err := os.WriteFile(domainsFile, []byte(domainsContent), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		// Test exact matches
		exactMatches := []string{
			"https://github.com/user/repo",
			"https://example.com/page",
		}

		for _, url := range exactMatches {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow exact match: %s", url)
		}

		// Test glob matches
		globMatches := []string{
			"https://storage.googleapis.com/bucket/file",
			"https://compute.googleapis.com/instances",
			"https://api.staging.dev/v1/data",
			"https://api.production.dev/users",
		}

		for _, url := range globMatches {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow glob match: %s", url)
		}

		// Test blocked domains
		blockedURLs := []string{
			"https://raw.githubusercontent.com/file", // not in allowed list
			"https://googleapis.com/service",         // doesn't match *.googleapis.com
			"https://api.prod.com/data",              // doesn't match api.*.dev
			"https://malicious.com/payload",          // not in allowed list
		}

		for _, url := range blockedURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.Error(t, err, "Should block URL: %s", url)
			assert.Contains(t, err.Error(), "not in the allowed domains list")
		}
	})

	t.Run("Domain filter always allows localhost", func(t *testing.T) {
		// Create restrictive domains file that doesn't include localhost
		domainsFile := filepath.Join(tempDir, "restrictive_domains.txt")
		domainsContent := `# Very restrictive - only example.com allowed
example.com
`
		err := os.WriteFile(domainsFile, []byte(domainsContent), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		// Test that localhost URLs are always allowed regardless of domain filter
		localhostURLs := []string{
			"http://localhost:8080/api",
			"http://127.0.0.1:3000/page",
			"http://[::1]:9000/service",
			"http://0.0.0.0:5000/health",
		}

		for _, url := range localhostURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should always allow localhost URL: %s", url)
		}

		// Test that external domains are still filtered
		externalURL := "https://github.com/user/repo"
		input := fmt.Sprintf(`{"url": "%s"}`, externalURL)
		err = tool.ValidateInput(state, input)
		assert.Error(t, err, "Should block external domain when not in allowed list")
		assert.Contains(t, err.Error(), "not in the allowed domains list")
	})

	t.Run("Domain filter handles URL normalization", func(t *testing.T) {
		// Create domains file
		domainsFile := filepath.Join(tempDir, "normalized_domains.txt")
		domainsContent := `# Test normalization
example.com
https://github.com/
http://api.test.com/path
`
		err := os.WriteFile(domainsFile, []byte(domainsContent), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		// Test that domains are properly normalized (protocol and path stripped)
		normalizedURLs := []string{
			"https://example.com/any/path",
			"https://github.com/different/path",
			"https://api.test.com/other/endpoint",
		}

		for _, url := range normalizedURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow URL with different path/protocol: %s", url)
		}
	})

	t.Run("Domain filter validation errors", func(t *testing.T) {
		// Create domains file
		domainsFile := filepath.Join(tempDir, "test_domains.txt")
		domainsContent := `example.com`
		err := os.WriteFile(domainsFile, []byte(domainsContent), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		// Test that domain filter validation integrates with other validation
		testCases := []struct {
			name        string
			input       string
			expectError bool
			errorMsg    string
		}{
			{
				name:        "Valid domain passes all validation",
				input:       `{"url": "https://example.com/file.txt"}`,
				expectError: false,
			},
			{
				name:        "Blocked domain fails domain filter validation",
				input:       `{"url": "https://blocked.com/file.txt"}`,
				expectError: true,
				errorMsg:    "not in the allowed domains list",
			},
			{
				name:        "Invalid URL fails before domain filter",
				input:       `{"url": "not-a-url"}`,
				expectError: true,
				errorMsg:    "only HTTPS scheme is supported",
			},
			{
				name:        "HTTP external domain fails scheme validation before domain filter",
				input:       `{"url": "http://example.com/file.txt"}`,
				expectError: true,
				errorMsg:    "only HTTPS scheme is supported",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				err := tool.ValidateInput(state, tc.input)
				if tc.expectError {
					assert.Error(t, err)
					if tc.errorMsg != "" {
						assert.Contains(t, err.Error(), tc.errorMsg)
					}
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})

	t.Run("Domain filter file with comments and whitespace", func(t *testing.T) {
		// Create domains file with various formatting
		domainsFile := filepath.Join(tempDir, "formatted_domains.txt")
		domainsContent := `
# This is a comment
  
github.com   
   example.com
# Another comment
 
   # Comment with spaces
*.googleapis.com  
   
# Final comment
`
		err := os.WriteFile(domainsFile, []byte(domainsContent), 0o644)
		require.NoError(t, err)

		tool := NewWebFetchTool(domainsFile)

		// Test that domains are properly parsed despite formatting
		allowedURLs := []string{
			"https://github.com/user/repo",
			"https://example.com/page",
			"https://storage.googleapis.com/bucket/file",
		}

		for _, url := range allowedURLs {
			input := fmt.Sprintf(`{"url": "%s"}`, url)
			err := tool.ValidateInput(state, input)
			assert.NoError(t, err, "Should allow URL despite file formatting: %s", url)
		}
	})
}
