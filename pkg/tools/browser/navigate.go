package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

type NavigateTool struct{}

type NavigateInput struct {
	URL     string `json:"url" jsonschema:"required,format=uri,description=URL to navigate to"`
	Timeout int    `json:"timeout" jsonschema:"default=30000,description=Timeout in milliseconds"`
}

type NavigateResult struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Error   string `json:"error,omitempty"`
}

func (r NavigateResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	result := fmt.Sprintf("Navigated to %s\nTitle: %s", r.URL, r.Title)
	return tools.StringifyToolResult(result, "")
}

func (r NavigateResult) UserFacing() string {
	if !r.Success {
		return fmt.Sprintf("❌ Navigation failed: %s", r.Error)
	}
	return fmt.Sprintf("✅ Navigated to %s\nTitle: %s", r.URL, r.Title)
}

func (r NavigateResult) IsError() bool {
	return !r.Success
}

func (r NavigateResult) GetError() string {
	return r.Error
}

func (r NavigateResult) GetResult() string {
	return r.URL
}

func (t NavigateTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[NavigateInput]()
}

func (t NavigateTool) Name() string {
	return "browser_navigate"
}

func (t NavigateTool) Description() string {
	return "Navigate to a URL"
}

func (t NavigateTool) ValidateInput(state tools.State, parameters string) error {
	var input NavigateInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.URL == "" {
		return fmt.Errorf("url is required")
	}

	// Validate URL format - must be absolute URL with scheme
	parsedURL, err := url.Parse(input.URL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}
	if !parsedURL.IsAbs() {
		return fmt.Errorf("URL must be absolute (include scheme)")
	}

	if input.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	return nil
}

func (t NavigateTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input NavigateInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return NavigateResult{
			Success: false,
			Error:   fmt.Sprintf("failed to parse input: %v", err),
		}
	}

	// Set default timeout if not provided
	if input.Timeout == 0 {
		input.Timeout = 30000
	}

	// Get browser manager and ensure it's active
	manager := GetManagerFromState(state)
	if err := manager.EnsureActive(ctx); err != nil {
		return NavigateResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return NavigateResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Create timeout context
	timeout := time.Duration(input.Timeout) * time.Millisecond
	timeoutCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	// Navigate to URL
	var currentURL, title string
	err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(input.URL),
		chromedp.WaitReady("body"),
		chromedp.Location(&currentURL),
		chromedp.Title(&title),
	)

	if err != nil {
		logger.G(ctx).WithField("url", input.URL).WithError(err).Info("Navigation failed")
		return NavigateResult{
			Success: false,
			Error:   fmt.Sprintf("navigation failed: %v", err),
		}
	}

	logger.G(ctx).WithField("url", currentURL).WithField("title", title).Info("Navigation successful")
	
	return NavigateResult{
		Success: true,
		URL:     currentURL,
		Title:   title,
	}
}

func (t NavigateTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input NavigateInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("browser.navigate.url", input.URL),
		attribute.Int("browser.navigate.timeout", input.Timeout),
	}, nil
}