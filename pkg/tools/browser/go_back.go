package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

type GoBackTool struct{}

type GoBackInput struct {
	Timeout int `json:"timeout" jsonschema:"default=10000,description=Timeout for navigation"`
}

type GoBackResult struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
	Error   string `json:"error,omitempty"`
}

func (r GoBackResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	result := fmt.Sprintf("Navigated back to: %s", r.URL)
	return tools.StringifyToolResult(result, "")
}

func (r GoBackResult) UserFacing() string {
	if !r.Success {
		return fmt.Sprintf("❌ Go back failed: %s", r.Error)
	}
	return fmt.Sprintf("⬅️ Navigated back to: %s", r.URL)
}

func (r GoBackResult) IsError() bool {
	return !r.Success
}

func (r GoBackResult) GetError() string {
	return r.Error
}

func (r GoBackResult) GetResult() string {
	return r.URL
}

func (t GoBackTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[GoBackInput]()
}

func (t GoBackTool) Name() string {
	return "browser_go_back"
}

func (t GoBackTool) Description() string {
	return `Navigate back to the previous page in the browser's history, similar to clicking the back button.

## Parameters
- timeout: Maximum time to wait for navigation to complete in milliseconds (default: 10000)

## Behavior
- Checks if there is a previous page in the browser history
- Navigates back one step in the browser's history stack
- Waits for the page body to be ready after navigation
- Returns the URL and title of the page navigated to

## History Requirements
- Requires at least one previous page in the browser session
- Will fail if there is no previous page to navigate to
- History is maintained within the same browser session

## Common Use Cases
* Returning to a previous form or listing page
* Undoing navigation to test alternate paths
* Implementing back functionality in web testing scenarios
* Navigating backwards through multi-step processes
* Testing browser history management in web applications

## Navigation Behavior
- Equivalent to pressing the browser's back button
- Preserves form data and scroll position (browser-dependent)
- May trigger page reload if the previous page is not cached
- Respects browser security policies for cross-origin navigation

## Examples
- Simple go back: {}
- With custom timeout: {"timeout": 15000}

## Error Conditions
- No previous page in history: Returns error with appropriate message
- Network issues: May fail if the previous page is no longer accessible
- Cross-origin restrictions: Some pages may prevent navigation

## Important Notes
- Always check the success status before assuming the navigation occurred
- The returned URL shows where the browser actually navigated to
- Use this tool sparingly as it affects the browser's navigation state
- Consider using browser_navigate with specific URLs for more predictable behavior
- History navigation may not work as expected with single-page applications`
}

func (t GoBackTool) ValidateInput(state tools.State, parameters string) error {
	var input GoBackInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	return nil
}

func (t GoBackTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input GoBackInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return GoBackResult{
			Success: false,
			Error:   fmt.Sprintf("failed to parse input: %v", err),
		}
	}

	// Set default timeout if not provided
	if input.Timeout == 0 {
		input.Timeout = 10000
	}

	// Get browser manager and ensure it's active
	manager := GetManagerFromState(state)
	if err := manager.EnsureActive(ctx); err != nil {
		return GoBackResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return GoBackResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Create timeout context
	timeout := time.Duration(input.Timeout) * time.Millisecond
	timeoutCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	// Check if we can go back
	var canGoBack bool
	err := chromedp.Run(timeoutCtx,
		chromedp.Evaluate(`window.history.length > 1`, &canGoBack),
	)

	if err != nil {
		logger.G(ctx).WithError(err).Info("Failed to check history")
		return GoBackResult{
			Success: false,
			Error:   fmt.Sprintf("failed to check browser history: %v", err),
		}
	}

	if !canGoBack {
		logger.G(ctx).Info("No previous page to go back to")
		return GoBackResult{
			Success: false,
			Error:   "no previous page in history",
		}
	}

	// Go back to the previous page
	var currentURL string
	err = chromedp.Run(timeoutCtx,
		chromedp.NavigateBack(),
		chromedp.WaitReady("body"),
		chromedp.Location(&currentURL),
	)

	if err != nil {
		logger.G(ctx).WithError(err).Info("Go back failed")
		return GoBackResult{
			Success: false,
			Error:   fmt.Sprintf("go back failed: %v", err),
		}
	}

	logger.G(ctx).WithField("url", currentURL).Info("Go back successful")

	return GoBackResult{
		Success: true,
		URL:     currentURL,
	}
}

func (t GoBackTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input GoBackInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.Int("browser.go_back.timeout", input.Timeout),
	}, nil
}
