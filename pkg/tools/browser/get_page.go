package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

type GetPageTool struct{}

type GetPageInput struct{}

type GetPageResult struct {
	Success   bool   `json:"success"`
	HTML      string `json:"html"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Truncated bool   `json:"truncated"`
	Error     string `json:"error,omitempty"`
}

func (r GetPageResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	result := fmt.Sprintf("URL: %s\nTitle: %s\nHTML Length: %d characters\nTruncated: %t", r.URL, r.Title, len(r.HTML), r.Truncated)
	return tools.StringifyToolResult(result+"\n\n"+r.HTML, "")
}

func (r GetPageResult) IsError() bool {
	return !r.Success
}

func (r GetPageResult) GetError() string {
	return r.Error
}

func (r GetPageResult) GetResult() string {
	return r.HTML
}

func (r GetPageResult) StructuredData() tools.StructuredToolResult {
	result := tools.StructuredToolResult{
		ToolName:  "browser_get_page",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
	}

	if r.Success {
		result.Metadata = &tools.BrowserGetPageMetadata{
			URL:       r.URL,
			Title:     r.Title,
			HTMLSize:  len(r.HTML),
			Truncated: r.Truncated,
		}
	}

	return result
}

func (t GetPageTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[GetPageInput]()
}

func (t GetPageTool) Name() string {
	return "browser_get_page"
}

func (t GetPageTool) Description() string {
	return `Extract and return the simplified content of the current web page optimized for LLM analysis.

## Content Processing
This tool automatically:
- Extracts only meaningful content: links, buttons, inputs, images, and text
- Numbers each element for easy reference: [0] <button> Click me
- Removes all styling, scripts, and non-interactive elements
- Provides a clean, structured view similar to how a user sees the page

## Output Format
Elements are formatted as: [index] <type> content
- [0] <link> Home [/home]
- [1] <button> Submit
- [2] <input> type=text placeholder='Enter email'
- [3] <text> Welcome to our site

## Element Types
- link: Clickable links with href shown
- button: Clickable buttons or elements with onclick
- input: Form inputs with type, placeholder, value, and name
- select: Dropdowns with available options
- textarea: Text areas with placeholder
- img: Images with alt text and src
- text: Regular text content

## Return Information
- Simplified page content in structured format
- Current page URL and title
- Content length and truncation status
- Success/error status

## Common Use Cases
* Finding and interacting with page elements
* Understanding page structure and navigation
* Extracting form fields and options
* Analyzing page content for automation
* Preparing content for further processing

## Examples
- Default usage: {}

## Best Practices
- The numbered elements make it easy to reference specific items in other browser tools
- Use in combination with browser_click, browser_type, etc. for automation`
}

func (t GetPageTool) ValidateInput(state tools.State, parameters string) error {
	var input GetPageInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "failed to parse input")
	}

	return nil
}

func (t GetPageTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input GetPageInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return GetPageResult{
			Success: false,
			Error:   fmt.Sprintf("failed to parse input: %v", err),
		}
	}

	// Get browser manager and ensure it's active
	manager := GetManagerFromState(state)
	if err := manager.EnsureActive(ctx); err != nil {
		return GetPageResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return GetPageResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Get page metadata
	var currentURL, title string
	err := chromedp.Run(browserCtx,
		chromedp.Location(&currentURL),
		chromedp.Title(&title),
	)

	if err != nil {
		logger.G(ctx).WithError(err).Info("Failed to get page metadata")
		return GetPageResult{
			Success: false,
			Error:   fmt.Sprintf("failed to get page metadata: %v", err),
		}
	}

	html, truncated, err := manager.(*Manager).Crawl(ctx, 500000)
	if err != nil {
		logger.G(ctx).WithError(err).Info("Failed to crawl page content")
		return GetPageResult{
			Success: false,
			Error:   fmt.Sprintf("failed to crawl page content: %v", err),
		}
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"url":         currentURL,
		"title":       title,
		"html":        html,
		"html_length": len(html),
		"truncated":   truncated,
	}).Info("Page content retrieved")

	return GetPageResult{
		Success:   true,
		HTML:      html,
		URL:       currentURL,
		Title:     title,
		Truncated: truncated,
	}
}

func (t GetPageTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	return []attribute.KeyValue{}, nil
}
