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

type ClickTool struct{}

type ClickInput struct {
	Selector string `json:"selector" jsonschema:"required,description=CSS selector for element to click"`
	Timeout  int    `json:"timeout" jsonschema:"default=10000,description=Timeout to wait for element"`
}

type ClickResult struct {
	Success      bool   `json:"success"`
	ElementFound bool   `json:"element_found"`
	Error        string `json:"error,omitempty"`
}

func (r ClickResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	return tools.StringifyToolResult("Element clicked successfully", "")
}

func (r ClickResult) UserFacing() string {
	if !r.Success {
		if !r.ElementFound {
			return fmt.Sprintf("❌ Element not found or not clickable")
		}
		return fmt.Sprintf("❌ Click failed: %s", r.Error)
	}
	return "✅ Element clicked successfully"
}

func (r ClickResult) IsError() bool {
	return !r.Success
}

func (r ClickResult) GetError() string {
	return r.Error
}

func (r ClickResult) GetResult() string {
	if r.Success {
		return "clicked"
	}
	return r.Error
}

func (t ClickTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[ClickInput]()
}

func (t ClickTool) Name() string {
	return "browser_click"
}

func (t ClickTool) Description() string {
	return `Click on a web page element identified by a CSS selector.

## Parameters
- selector: CSS selector for the element to click (required)
- timeout: Maximum wait time for element to be visible and clickable in milliseconds (default: 10000)

## Behavior
- Waits for the element to be visible and accessible
- Verifies the element exists before attempting to click
- Performs a single left-click on the center of the element
- Returns success status and element found information

## CSS Selector Examples
- By ID: "#submit-button", "#login-form"
- By class: ".btn-primary", ".nav-link"
- By tag: "button", "a", "input"
- By attribute: "[data-testid='submit']", "[type='submit']"
- Complex selectors: "form.login button[type='submit']", ".modal .close-btn"

## Common Use Cases
* Clicking buttons (submit, cancel, navigation)
* Following links
* Activating form controls
* Triggering interactive elements
* Closing modals or popups

## Element Requirements
- Element must be visible on the page
- Element must not be obscured by other elements
- Element should be clickable (not disabled)

## Examples
- Click submit button: {"selector": "#submit-btn"}
- Click with custom timeout: {"selector": ".slow-loading-btn", "timeout": 20000}
- Click by attribute: {"selector": "[data-action='delete']"}

## Important Notes
- The tool will fail if the element is not found or not visible
- Use browser_wait_for tool first if you need to wait for dynamic content
- For elements that appear after page interactions, increase the timeout value
- Complex selectors may be slower - use specific selectors when possible`
}

func (t ClickTool) ValidateInput(state tools.State, parameters string) error {
	var input ClickInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.Selector == "" {
		return fmt.Errorf("selector is required")
	}

	if input.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	return nil
}

func (t ClickTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input ClickInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return ClickResult{
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
		return ClickResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return ClickResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Create timeout context
	timeout := time.Duration(input.Timeout) * time.Millisecond
	timeoutCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	// Check if element exists first
	var exists bool
	err := chromedp.Run(timeoutCtx,
		chromedp.WaitVisible(input.Selector),
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector("%s") !== null`, input.Selector), &exists),
	)

	if err != nil || !exists {
		logger.G(ctx).WithField("selector", input.Selector).WithError(err).Info("Element not found or not visible")
		return ClickResult{
			Success:      false,
			ElementFound: false,
			Error:        fmt.Sprintf("element not found or not visible: %s", input.Selector),
		}
	}

	// Perform the click
	err = chromedp.Run(timeoutCtx,
		chromedp.Click(input.Selector),
	)

	if err != nil {
		logger.G(ctx).WithField("selector", input.Selector).WithError(err).Info("Click failed")
		return ClickResult{
			Success:      false,
			ElementFound: true,
			Error:        fmt.Sprintf("click failed: %v", err),
		}
	}

	logger.G(ctx).WithField("selector", input.Selector).Info("Click successful")

	return ClickResult{
		Success:      true,
		ElementFound: true,
	}
}

func (t ClickTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input ClickInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("browser.click.selector", input.Selector),
		attribute.Int("browser.click.timeout", input.Timeout),
	}, nil
}
