package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/invopop/jsonschema"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type WebFetchToolResult struct {
	url    string
	prompt string
	result string
	err    string
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
	return fmt.Sprintf("Web Fetch: %s\nPrompt: %s\n%s", r.url, r.prompt, r.result)
}

// WebFetchTool implements the web_fetch tool for retrieving and processing web content.
type WebFetchTool struct{}

// WebFetchInput defines the input parameters for the web_fetch tool.
type WebFetchInput struct {
	URL    string `json:"url" jsonschema:"description=The URL to fetch content from"`
	Prompt string `json:"prompt" jsonschema:"description=Information to extract from the content"`
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
	return `Fetches content from a web URL and extracts specific information.

## Input
- url: The URL to fetch content from. The URL must be a valid HTTPS URL.
- prompt: Information that should be extracted from the content

## Behavior
- Makes an HTTP GET request to the specified URL
- Follows redirects as long as the domain does not change
- Converts HTML content to Markdown for better readability
- Uses AI to extract the requested information from the content

## Common Use Cases
* Retrieving specific information from documentation websites
* Extracting data from public web pages
* Analyzing content from online resources
* Providing context from web-based reference material

## Important Notes
1. Only public URLs that don't require authentication can be accessed
2. For security reasons, redirects are only followed within the same domain
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

	if parsedURL.Scheme != "https" {
		return errors.New("only HTTPS scheme is supported")
	}

	if input.Prompt == "" {
		return errors.New("prompt is required")
	}

	return nil
}

// Execute executes the web_fetch tool.
func (t *WebFetchTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResultInterface {
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
	content, contentType, err := fetchWithSameDomainRedirects(input.URL)
	if err != nil {
		return &WebFetchToolResult{
			url:    input.URL,
			prompt: input.Prompt,
			err:    fmt.Sprintf("Failed to fetch URL: %s", err),
		}
	}

	// 2. Convert HTML to Markdown if appropriate
	var processedContent string
	if strings.Contains(contentType, "text/html") {
		processedContent = convertHTMLToMarkdown(content)
	} else {
		processedContent = content
	}

	// 3. Use weak LLM to extract the requested information
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

// fetchWithSameDomainRedirects fetches content from a URL and follows redirects
// only if they stay within the same domain.
func fetchWithSameDomainRedirects(urlStr string) (string, string, error) {
	// Parse the original URL to get the domain
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
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

	// Make the request
	resp, err := client.Get(urlStr)
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
func convertHTMLToMarkdown(htmlContent string) string {
	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(htmlContent)
	if err != nil {
		logrus.WithError(err).Warn("Failed to convert HTML to Markdown, returning raw HTML")
		return htmlContent
	}
	return markdown
}
