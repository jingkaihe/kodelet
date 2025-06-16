package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chromedp/chromedp"
	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

type GetPageTool struct{}

type GetPageInput struct {
	Simplify  bool `json:"simplify" jsonschema:"default=true,description=Remove styling scripts and non-essential attributes"`
	MaxLength int  `json:"max_length" jsonschema:"default=50000,description=Maximum HTML length to return"`
}

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

func (r GetPageResult) UserFacing() string {
	if !r.Success {
		return fmt.Sprintf("❌ Failed to get page content: %s", r.Error)
	}
	
	status := "✅ Page content retrieved"
	if r.Truncated {
		status += " (truncated)"
	}
	
	return fmt.Sprintf("%s\nURL: %s\nTitle: %s\nHTML Length: %d characters", 
		status, r.URL, r.Title, len(r.HTML))
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

func (t GetPageTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[GetPageInput]()
}

func (t GetPageTool) Name() string {
	return "browser_get_page"
}

func (t GetPageTool) Description() string {
	return "Get cleaned HTML DOM of current page for LLM analysis"
}

func (t GetPageTool) ValidateInput(state tools.State, parameters string) error {
	var input GetPageInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.MaxLength < 0 {
		return fmt.Errorf("max_length must be non-negative")
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

	// Set defaults
	if input.MaxLength == 0 {
		input.MaxLength = 50000
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

	// Get page content
	var html, currentURL, title string
	err := chromedp.Run(browserCtx,
		chromedp.OuterHTML("html", &html),
		chromedp.Location(&currentURL),
		chromedp.Title(&title),
	)

	if err != nil {
		logger.G(ctx).WithError(err).Info("Failed to get page content")
		return GetPageResult{
			Success: false,
			Error:   fmt.Sprintf("failed to get page content: %v", err),
		}
	}

	// Simplify HTML if requested
	truncated := false
	if input.Simplify {
		html, truncated = SimplifyHTML(html, input.MaxLength)
	} else if len(html) > input.MaxLength {
		html = html[:input.MaxLength]
		truncated = true
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"url": currentURL,
		"title": title,
		"html_length": len(html),
		"truncated": truncated,
		"simplified": input.Simplify,
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
	var input GetPageInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.Bool("browser.get_page.simplify", input.Simplify),
		attribute.Int("browser.get_page.max_length", input.MaxLength),
	}, nil
}