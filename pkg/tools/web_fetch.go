package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/google/shlex"
	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// WebFetchToolResult represents the result of fetching content from a web URL
type WebFetchToolResult struct {
	url      string
	prompt   string
	result   string
	err      string
	filePath string // For saved files
}

// GetResult returns the fetched content
func (r *WebFetchToolResult) GetResult() string {
	return r.result
}

// GetError returns the error message
func (r *WebFetchToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *WebFetchToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *WebFetchToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
}

// WebFetchTool implements the web_fetch tool for retrieving and processing web content.
type WebFetchTool struct {
	domainFilter *osutil.DomainFilter
}

// NewWebFetchTool creates a new WebFetchTool with optional domain filtering
func NewWebFetchTool(allowedDomainsFile string) *WebFetchTool {
	var domainFilter *osutil.DomainFilter
	if allowedDomainsFile != "" {
		domainFilter = osutil.NewDomainFilter(allowedDomainsFile)
	}
	return &WebFetchTool{
		domainFilter: domainFilter,
	}
}

// isLocalHost checks if the given hostname/IP is a localhost or internal address
func isLocalHost(hostname string) bool {
	// Check for common localhost names
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" || hostname == "0.0.0.0" {
		return true
	}

	// Check for other loopback addresses (127.0.0.0/8)
	if ip := net.ParseIP(hostname); ip != nil {
		return ip.IsLoopback()
	}

	return false
}

// WebFetchInput defines the input parameters for the web_fetch tool.
type WebFetchInput struct {
	URL    string `json:"url" jsonschema:"description=The URL to fetch content from"`
	Prompt string `json:"prompt,omitempty" jsonschema:"description=Information to extract from HTML/Markdown content (optional)"`
}

// Name returns the name of the tool.
func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

// GenerateSchema generates the JSON schema for the tool's input parameters.
func (t *WebFetchTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[WebFetchInput]()
}

// Description returns the description of the tool.
func (t *WebFetchTool) Description() string {
	return `Fetch content from a public URL.

# Input
- url: required URL to fetch
- prompt: optional instruction for extracting info from HTML/Markdown pages

# Rules
- Use HTTPS for external domains.
- HTTP is allowed only for localhost/internal addresses.
- Redirects are followed only within the same domain (max 10).
- Binary content types (zip/pdf/image/audio/video/octet-stream) are rejected.

# Behavior
- Code/text/JSON/XML/etc:
  - Save to ~/.kodelet/web-archives/{domain}/{filename}.{ext}
  - Return content with line numbers (truncated if output is too large)
- HTML/Markdown without prompt:
  - Return full page content as Markdown (HTML is converted)
- HTML/Markdown with prompt:
  - Run AI extraction against page content and return only the extracted result

# Prompt guidance
Use prompt when:
- You need specific facts/sections from an HTML/Markdown page
- The page is large and you do not want full-page output

Examples:
- url: https://docs.example.com/api-reference
  prompt: List all endpoints with HTTP methods
- url: https://company.example.com/changelog
  prompt: Summarize breaking changes in the latest release

Do not use prompt when:
- You want raw file contents with line numbers
- You want full-page output

Examples:
- url: https://raw.githubusercontent.com/user/repo/main/config.yaml
- url: https://example.com/data.json

# Notes
- Only public URLs are supported (no auth/session handling).
- Prompt is ignored for non-HTML/Markdown responses (code/text/JSON/XML).
`
}

// ValidateInput validates the input parameters for the tool.
func (t *WebFetchTool) ValidateInput(_ tooltypes.State, parameters string) error {
	input := &WebFetchInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.URL == "" {
		return errors.New("url is required")
	}

	// Validate URL format and scheme
	parsedURL, err := url.Parse(input.URL)
	if err != nil {
		return errors.Wrap(err, "invalid URL")
	}

	// Allow HTTP for localhost/internal addresses, require HTTPS for external domains
	if parsedURL.Scheme != "https" && (parsedURL.Scheme != "http" || !isLocalHost(parsedURL.Hostname())) {
		return errors.New("only HTTPS scheme is supported for external domains, HTTP is allowed for localhost/internal addresses")
	}

	// Check domain filtering if configured
	if t.domainFilter != nil {
		allowed, err := t.domainFilter.IsAllowed(input.URL)
		if err != nil {
			return errors.Wrap(err, "failed to validate domain")
		}
		if !allowed {
			return errors.Errorf("domain %s is not in the allowed domains list", parsedURL.Hostname())
		}
	}

	// Prompt is now optional, no validation needed
	return nil
}

// Execute executes the web_fetch tool.
func (t *WebFetchTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &WebFetchInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    err.Error(),
		}
	}

	// 1. Fetch the content with a custom HTTP client that handles same-domain redirects
	content, contentType, err := fetchWithSameDomainRedirects(ctx, input.URL)
	if err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to fetch URL: %s", err),
		}
	}

	// 2. Determine content handling strategy
	isHTMLOrMarkdown := strings.Contains(contentType, "text/html") ||
		strings.Contains(contentType, "text/markdown") ||
		isMarkdownFromURL(input.URL)

	// Scenario 1: Code/Text content - save to file with line numbers
	if !isHTMLOrMarkdown {
		return t.handleCodeTextContent(ctx, input, content, contentType)
	}

	// Scenario 2: HTML/Markdown content
	if input.Prompt == "" {
		// No prompt: return converted markdown content directly
		return t.handleHTMLMarkdownContent(ctx, input, content, contentType)
	}
	// With prompt: use AI extraction
	return t.handleHTMLMarkdownWithPrompt(ctx, state, input, content, contentType)
}

// TracingKVs returns tracing key-value pairs for observability.
func (t *WebFetchTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &WebFetchInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("url", input.URL),
	}, nil
}

// isMarkdownFromURL checks if the URL ends with markdown file extensions
func isMarkdownFromURL(urlStr string) bool {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	ext := strings.ToLower(path.Ext(parsedURL.Path))
	return ext == ".md" || ext == ".markdown"
}

// getFileNameFromURL extracts a filename from the URL
func getFileNameFromURL(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "unnamed"
	}

	filename := path.Base(parsedURL.Path)
	if filename == "" || filename == "/" || filename == "." {
		// Use domain name if no filename
		filename = strings.ReplaceAll(parsedURL.Hostname(), ".", "_")
	}

	// Remove any file extension to add appropriate one later
	ext := path.Ext(filename)
	if ext != "" {
		filename = strings.TrimSuffix(filename, ext)
	}

	return filename
}

// getFileExtensionFromContentType determines file extension from content type
func getFileExtensionFromContentType(contentType, urlStr string) string {
	// Check URL first for explicit extension
	parsedURL, err := url.Parse(urlStr)
	if err == nil {
		ext := strings.ToLower(path.Ext(parsedURL.Path))
		if ext != "" {
			return ext
		}
	}

	// Fall back to content type mapping
	contentType = strings.ToLower(contentType)
	switch {
	case strings.Contains(contentType, "javascript"):
		return ".js"
	case strings.Contains(contentType, "json"):
		return ".json"
	case strings.Contains(contentType, "xml"):
		return ".xml"
	case strings.Contains(contentType, "yaml"):
		return ".yaml"
	case strings.Contains(contentType, "text/html"):
		return ".html"
	case strings.Contains(contentType, "text/markdown"):
		return ".md"
	case strings.Contains(contentType, "text/plain"):
		return ".txt"
	case strings.Contains(contentType, "text/css"):
		return ".css"
	default:
		return ".txt"
	}
}

// handleCodeTextContent saves content to file and returns with line numbers
func (t *WebFetchTool) handleCodeTextContent(_ context.Context, input *WebFetchInput, content, contentType string) tooltypes.ToolResult {
	// Parse URL to get domain name
	parsedURL, err := url.Parse(input.URL)
	if err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to parse URL: %s", err),
		}
	}

	// Get home directory for web archives
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to get home directory: %s", err),
		}
	}

	// Create domain-specific web-archives directory
	domainName := parsedURL.Hostname()
	archiveDir := filepath.Join(homeDir, ".kodelet", "web-archives", domainName)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to create archive directory: %s", err),
		}
	}

	// Generate filename
	baseFileName := getFileNameFromURL(input.URL)
	ext := getFileExtensionFromContentType(contentType, input.URL)
	fileName := baseFileName + ext
	filePath := filepath.Join(archiveDir, fileName)

	// Handle potential filename conflicts
	counter := 1
	for {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			break
		}
		filePath = fmt.Sprintf("%s_%d%s",
			filepath.Join(archiveDir, baseFileName), counter, ext)
		counter++
	}

	// Write content to file
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to save content to file: %s", err),
		}
	}

	// Prepare content with line numbers
	lines := strings.Split(content, "\n")
	contentBytes := len(content)

	var resultContent string
	if contentBytes <= MaxOutputBytes {
		// Return full content with line numbers
		resultContent = osutil.ContentWithLineNumber(lines, 1)
	} else {
		// Return truncated content with line numbers
		bytesRead := 0
		var truncatedLines []string
		for _, line := range lines {
			if bytesRead+len(line)+1 > MaxOutputBytes { // +1 for newline
				break
			}
			truncatedLines = append(truncatedLines, line)
			bytesRead += len(line) + 1
		}

		resultContent = osutil.ContentWithLineNumber(truncatedLines, 1)
		resultContent += fmt.Sprintf("\n\n[truncated due to max output bytes limit of %d, please read the full file at %s]",
			MaxOutputBytes, filePath)
	}

	return &WebFetchToolResult{
		url:      input.URL,
		prompt:   input.Prompt,
		result:   resultContent,
		filePath: filePath,
	}
}

// handleHTMLMarkdownContent processes HTML/Markdown content without AI extraction
// Returns the converted markdown content directly
func (t *WebFetchTool) handleHTMLMarkdownContent(ctx context.Context, input *WebFetchInput, content, contentType string) tooltypes.ToolResult {
	// Convert HTML to Markdown if needed
	var processedContent string
	if strings.Contains(contentType, "text/html") {
		processedContent = convertHTMLToMarkdown(ctx, content)
	} else {
		processedContent = content
	}

	return &WebFetchToolResult{
		url:    input.URL,
		prompt: input.Prompt,
		result: processedContent,
	}
}

// handleHTMLMarkdownWithPrompt processes HTML/Markdown content with AI extraction using shell-out pattern.
// This spawns a subagent process via `kodelet run --as-subagent` for content extraction.
// The full prompt (including content) is passed via stdin to avoid command-line argument length limits
// (especially on Windows ~32KB).
func (t *WebFetchTool) handleHTMLMarkdownWithPrompt(ctx context.Context, state tooltypes.State, input *WebFetchInput, content, contentType string) tooltypes.ToolResult {
	// Convert HTML to Markdown if needed
	var processedContent string
	if strings.Contains(contentType, "text/html") {
		processedContent = convertHTMLToMarkdown(ctx, content)
	} else {
		processedContent = content
	}

	// Get the current executable path
	exe, err := os.Executable()
	if err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to get executable path: %s", err),
		}
	}

	// Create a prompt for information extraction with content embedded
	// The full prompt is passed via stdin to avoid CLI argument length limits
	extractionPrompt := fmt.Sprintf(`
Here is the content from %s:

<content>
%s
</content>

Here is the instruction:
<instruction>
%s
</instruction>

Please extract the information from the content based on the instruction.
IMPORTANT: Make sure that you preserve all the links in the content including hyperlinks and images.
`,
		input.URL, processedContent, input.Prompt)

	// Build command arguments - use weak model and no tools for content extraction
	// Note: query is passed via stdin, not as an argument, to avoid CLI length limits
	args := []string{"run", "--result-only", "--as-subagent", "--use-weak-model", "--no-tools"}

	// Add subagent args from config if available
	if llmConfig, ok := state.GetLLMConfig().(llmtypes.Config); ok && llmConfig.SubagentArgs != "" {
		parsedArgs, err := shlex.Split(llmConfig.SubagentArgs)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("failed to parse subagent_args, ignoring")
		} else {
			args = append(args, parsedArgs...)
		}
	}

	// Execute the subagent with full prompt passed via stdin
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = strings.NewReader(extractionPrompt)

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &WebFetchToolResult{
				url:    input.URL,
				prompt: input.Prompt,
				err:    fmt.Sprintf("Failed to extract information: %s\nstderr: %s", err, string(exitErr.Stderr)),
			}
		}
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to extract information: %s", err),
		}
	}

	return &WebFetchToolResult{
		url:    input.URL,
		prompt: input.Prompt,
		result: strings.TrimSpace(string(output)),
	}
}

// fetchWithSameDomainRedirects fetches content from a URL and follows redirects
// only if they stay within the same domain.
func fetchWithSameDomainRedirects(ctx context.Context, urlStr string) (string, string, error) {
	// Parse the original URL to get the domain
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", "", errors.Wrap(err, "invalid URL")
	}

	// Allow HTTP for localhost/internal addresses, require HTTPS for external domains
	if parsedURL.Scheme != "https" && (parsedURL.Scheme != "http" || !isLocalHost(parsedURL.Hostname())) {
		return "", "", errors.New("only HTTPS scheme is supported for external domains, HTTP is allowed for localhost/internal addresses")
	}

	originalDomain := parsedURL.Hostname()

	// Create a custom HTTP client with a redirect policy
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Check if the redirect is to the same domain
			if req.URL.Hostname() != originalDomain {
				return errors.Errorf("redirect to different domain not allowed: %s -> %s",
					originalDomain, req.URL.Hostname())
			}

			// Limit the number of redirects
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}

			return nil
		},
	}

	// Create the request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", "", err
	}

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", errors.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/octet-stream") ||
		strings.Contains(contentType, "application/zip") ||
		strings.Contains(contentType, "application/pdf") ||
		strings.Contains(contentType, "image/") ||
		strings.Contains(contentType, "audio/") ||
		strings.Contains(contentType, "video/") {
		return "", "", errors.Errorf("unsupported content type: %s", contentType)
	}

	// Read the body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	return string(body), contentType, nil
}

// convertHTMLToMarkdown converts HTML content to Markdown.
func convertHTMLToMarkdown(ctx context.Context, htmlContent string) string {
	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(htmlContent)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to convert HTML to Markdown, returning raw HTML")
		return htmlContent
	}
	return markdown
}

// StructuredData returns structured metadata about the web fetch operation
func (r *WebFetchToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "web_fetch",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Determine processed type based on what happened
	processedType := "markdown" // Default for most web pages
	if r.filePath != "" {
		processedType = "saved"
	} else if r.prompt != "" {
		processedType = "ai_extracted"
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.WebFetchMetadata{
		URL:           r.url,
		ContentType:   "", // Not available in current structure
		Size:          int64(len(r.result)),
		SavedPath:     r.filePath,
		Prompt:        r.prompt,
		ProcessedType: processedType,
		Content:       r.result,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}
