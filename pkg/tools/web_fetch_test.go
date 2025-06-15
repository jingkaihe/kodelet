package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
			name:        "Valid input",
			input:       `{"url": "https://example.com", "prompt": "Extract the main heading"}`,
			expectError: false,
		},
		{
			name:        "Missing URL",
			input:       `{"prompt": "Extract the main heading"}`,
			expectError: true,
		},
		{
			name:        "Missing prompt",
			input:       `{"url": "https://example.com"}`,
			expectError: true,
		},
		{
			name:        "Invalid URL scheme",
			input:       `{"url": "ftp://example.com", "prompt": "Extract the main heading"}`,
			expectError: true,
		},
		{
			name:        "Invalid JSON",
			input:       `{"url": "https://example.com", "prompt": }`,
			expectError: true,
		},
		{
			name:        "Invalid URL",
			input:       `{"url": "http://example.com", "prompt": "Extract the main heading"}`,
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

func TestFetchWithSameDomainRedirects(t *testing.T) {
	// Setup HTTP test server for redirects
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body><h1>Hello World</h1></body></html>"))
		case "/redirect-same-domain":
			http.Redirect(w, r, "/", http.StatusFound)
		}
	}))
	defer server.Close()

	// Test successful fetch
	content, contentType, err := fetchWithSameDomainRedirects(context.Background(), server.URL)
	assert.NoError(t, err)
	assert.Contains(t, contentType, "text/html")
	assert.Contains(t, content, "Hello World")

	// Test successful redirect (same domain)
	content, contentType, err = fetchWithSameDomainRedirects(context.Background(), server.URL+"/redirect-same-domain")
	assert.NoError(t, err)
	assert.Contains(t, contentType, "text/html")
	assert.Contains(t, content, "Hello World")
}

func TestConvertHTMLToMarkdown(t *testing.T) {
	html := "<html><body><h1>Hello World</h1><p>This is a <strong>test</strong>.</p></body></html>"
	markdown := convertHTMLToMarkdown(context.Background(), html)
	assert.Contains(t, markdown, "# Hello World")
	assert.Contains(t, markdown, "This is a **test**.")
}

func TestWebFetchToolName(t *testing.T) {
	tool := &WebFetchTool{}
	assert.Equal(t, "web_fetch", tool.Name())
}

func TestWebFetchToolTracingKVs(t *testing.T) {
	tool := &WebFetchTool{}
	kvs, err := tool.TracingKVs(`{"url": "https://example.com", "prompt": "Extract the main heading"}`)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(kvs))
	// Skip the key check as it's an opaque type
	assert.Equal(t, "https://example.com", kvs[0].Value.AsString())
}
