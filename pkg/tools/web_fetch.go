package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
)

type WebFetchToolResult struct {
	url      string
	prompt   string
	result   string
	err      string
	filePath string // For saved files
}

func (r *WebFetchToolResult) GetResult() string {
	return r.result
}

func (r *WebFetchToolResult) GetError() string {
	return r.err
}

func (r *WebFetchToolResult) IsError() bool {
	return r.err != ""
}

func (r *WebFetchToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
}

func (r *WebFetchToolResult) UserFacing() string {
	if r.IsError() {
		return r.GetError()
	}
	if r.filePath != "" {
		return fmt.Sprintf("Web Fetch: %s\nSaved to: %s\n%s", r.url, r.filePath, r.result)
	}
	if r.prompt != "" {
		return fmt.Sprintf("Web Fetch: %s\nPrompt: %s\n%s", r.url, r.prompt, r.result)
	}
	return fmt.Sprintf("Web Fetch: %s\n%s", r.url, r.result)
}

// WebFetchTool implements the web_fetch tool for retrieving and processing web content.
type WebFetchTool struct {
	domainFilter *utils.DomainFilter
}

// NewWebFetchTool creates a new WebFetchTool with optional domain filtering
func NewWebFetchTool(allowedDomainsFile string) *WebFetchTool {
	var domainFilter *utils.DomainFilter
	if allowedDomainsFile != "" {
		domainFilter = utils.NewDomainFilter(allowedDomainsFile)
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
	return `Fetches content from a web URL with intelligent content handling.

## Input
- url: The URL to fetch content from. HTTPS URLs are required for external domains. HTTP URLs are allowed for localhost/internal addresses (127.0.0.1, ::1, localhost, etc.).
- prompt: (Optional) Only provided if you want to extract specific information from HTML/Markdown content using AI instead of getting the whole content

## Behavior
**Scenario 1: Code/Text Content**
- If the URL contains code, plain text, JSON, XML, or other structured text:
  - Saves the content to ./.kodelet/web-archives/{domain_name}/{filename}.{ext}
  - Returns the full content with line numbers if under 100KB
  - Returns first 100KB with line numbers and truncation notice if over 100KB
  - No AI processing is applied

**Scenario 2: HTML/Markdown Content**
- If the URL contains HTML or Markdown:
  - **Without prompt**: Converts HTML to Markdown and returns the converted content directly
  - **With prompt**: Uses AI to extract specific information based on the prompt
  - Does not save the content to file in either case

## Common Use Cases
* Fetching code files from GitHub or other repositories (saved with line numbers)
* Downloading configuration files or documentation (saved with line numbers)
* Converting HTML pages to readable Markdown format (returned directly)
* Extracting specific information from HTML documentation using AI prompts
* Analyzing structured data files like JSON or XML (saved with line numbers)

## Important Notes
1. Only public URLs that don't require authentication can be accessed
2. For security reasons, redirects are only followed within the same domain
3. Code/text files are saved to ./.kodelet/web-archives/{domain_name}/ directory for future reference
4. HTML/Markdown content is returned directly (with optional AI processing)
5. Prompt parameter only affects HTML/Markdown content behavior

## Examples

<good-example>
url: https://raw.githubusercontent.com/user/repo/main/config.yaml
<reasoning>
Fetches a code/text file from GitHub and saves it with line numbers for future reference.
</reasoning>
</good-example>

<good-example>
url: https://docs.example.com/api-reference
<reasoning>
Converts HTML documentation page to readable Markdown format and returns it directly.
</reasoning>
</good-example>

<good-example>
url: https://docs.example.com/api-reference
prompt: Extract all API endpoints and their HTTP methods
<reasoning>
Uses AI to extract specific information from HTML documentation based on the provided prompt.
</reasoning>
</good-example>

<bad-example>
url: http://external-site.com/file.txt
<reasoning>
Only HTTPS URLs are supported for external domains for security reasons.
</reasoning>
</bad-example>

<good-example>
url: http://localhost:8080/api/data
<reasoning>
HTTP URLs are allowed for localhost/internal addresses for development convenience.
</reasoning>
</good-example>

<bad-example>
url: https://example.com/download.zip
<reasoning>
Binary files like ZIP archives are not supported. Only text-based content can be fetched.
</reasoning>
</bad-example>

<bad-example>
url: https://private-api.com/data
<reasoning>
URLs requiring authentication cannot be accessed as only public URLs are supported.
</reasoning>
</bad-example>
`
}

// ValidateInput validates the input parameters for the tool.
func (t *WebFetchTool) ValidateInput(state tooltypes.State, parameters string) error {
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
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Allow HTTP for localhost/internal addresses, require HTTPS for external domains
	if parsedURL.Scheme == "https" {
		// HTTPS is always allowed
	} else if parsedURL.Scheme == "http" && isLocalHost(parsedURL.Hostname()) {
		// HTTP is allowed for localhost/internal addresses
	} else {
		return errors.New("only HTTPS scheme is supported for external domains, HTTP is allowed for localhost/internal addresses")
	}

	// Check domain filtering if configured
	if t.domainFilter != nil {
		allowed, err := t.domainFilter.IsAllowed(input.URL)
		if err != nil {
			return fmt.Errorf("failed to validate domain: %w", err)
		}
		if !allowed {
			return fmt.Errorf("domain %s is not in the allowed domains list", parsedURL.Hostname())
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
	isHtmlOrMarkdown := strings.Contains(contentType, "text/html") ||
		strings.Contains(contentType, "text/markdown") ||
		isMarkdownFromURL(input.URL)

	// Scenario 1: Code/Text content - save to file with line numbers
	if !isHtmlOrMarkdown {
		return t.handleCodeTextContent(ctx, input, content, contentType)
	}

	// Scenario 2: HTML/Markdown content
	if input.Prompt == "" {
		// No prompt: return converted markdown content directly
		return t.handleHtmlMarkdownContent(ctx, input, content, contentType)
	} else {
		// With prompt: use AI extraction
		return t.handleHtmlMarkdownWithPrompt(ctx, input, content, contentType)
	}
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

	// Create domain-specific web-archives directory
	domainName := parsedURL.Hostname()
	archiveDir := filepath.Join("./.kodelet/web-archives", domainName)
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
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
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
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
		resultContent = utils.ContentWithLineNumber(lines, 1)
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

		resultContent = utils.ContentWithLineNumber(truncatedLines, 1)
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

// handleHtmlMarkdownContent processes HTML/Markdown content without AI extraction
// Returns the converted markdown content directly
func (t *WebFetchTool) handleHtmlMarkdownContent(ctx context.Context, input *WebFetchInput, content, contentType string) tooltypes.ToolResult {
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

// handleHtmlMarkdownWithPrompt processes HTML/Markdown content with AI extraction
func (t *WebFetchTool) handleHtmlMarkdownWithPrompt(ctx context.Context, input *WebFetchInput, content, contentType string) tooltypes.ToolResult {
	// Convert HTML to Markdown if needed
	var processedContent string
	if strings.Contains(contentType, "text/html") {
		processedContent = convertHTMLToMarkdown(ctx, content)
	} else {
		processedContent = content
	}

	// Use AI to extract the requested information
	subAgentConfig, ok := ctx.Value(llm.SubAgentConfig{}).(llm.SubAgentConfig)
	if !ok {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    "sub-agent config not found in context",
		}
	}

	// Create a prompt for information extraction
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

	// Use weak model for extraction
	extractedInfo, err := subAgentConfig.Thread.SendMessage(ctx,
		extractionPrompt,
		&llm.ConsoleMessageHandler{
			Silent: true,
		},
		llm.MessageOpt{
			UseWeakModel: true,
			PromptCache:  false,
			NoToolUse:    true,
		},
	)

	if err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to extract information: %s", err),
		}
	}

	return &WebFetchToolResult{
		url:    input.URL,
		prompt: input.Prompt,
		result: extractedInfo,
	}
}

// fetchWithSameDomainRedirects fetches content from a URL and follows redirects
// only if they stay within the same domain.
func fetchWithSameDomainRedirects(ctx context.Context, urlStr string) (string, string, error) {
	// Parse the original URL to get the domain
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}

	// Allow HTTP for localhost/internal addresses, require HTTPS for external domains
	if parsedURL.Scheme == "https" {
		// HTTPS is always allowed
	} else if parsedURL.Scheme == "http" && isLocalHost(parsedURL.Hostname()) {
		// HTTP is allowed for localhost/internal addresses
	} else {
		return "", "", errors.New("only HTTPS scheme is supported for external domains, HTTP is allowed for localhost/internal addresses")
	}

	originalDomain := parsedURL.Hostname()

	// Create a custom HTTP client with a redirect policy
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Check if the redirect is to the same domain
			if req.URL.Hostname() != originalDomain {
				return fmt.Errorf("redirect to different domain not allowed: %s -> %s",
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
		return "", "", fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/octet-stream") ||
		strings.Contains(contentType, "application/zip") ||
		strings.Contains(contentType, "application/pdf") ||
		strings.Contains(contentType, "image/") ||
		strings.Contains(contentType, "audio/") ||
		strings.Contains(contentType, "video/") {
		return "", "", fmt.Errorf("unsupported content type: %s", contentType)
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

func (r *WebFetchToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "web_fetch",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	if r.IsError() {
		result.Error = r.GetError()
		return result
	}

	// Determine processed type based on what happened
	processedType := "markdown" // Default for most web pages
	if r.filePath != "" {
		processedType = "saved"
	} else if r.prompt != "" {
		processedType = "ai_extracted"
	}

	result.Metadata = &tooltypes.WebFetchMetadata{
		URL:           r.url,
		ContentType:   "", // Not available in current structure
		Size:          int64(len(r.result)),
		SavedPath:     r.filePath,
		Prompt:        r.prompt,
		ProcessedType: processedType,
	}

	return result
}
