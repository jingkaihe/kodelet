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
	ElementID int `json:"element_id" jsonschema:"required,description=Element ID from get_page output"`
	Timeout   int `json:"timeout" jsonschema:"default=10000,description=Timeout to wait for element"`
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
			return "❌ Element not found or not clickable"
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
	return `Click on a web page element using element ID-based targeting with automatic coordinate resolution.

## Parameters
- **element_id**: Element ID from browser_get_page output (e.g., element with id=5 would use element_id: 5)
- **timeout**: Maximum wait time for element in milliseconds (default: 10000)

## Behavior
- Uses element ID from browser_get_page output for targeting
- Automatically resolves element coordinates from internal element buffer
- Performs precise clicking at element center coordinates
- Removes target="_blank" attributes to prevent new tabs/windows
- Handles viewport scrolling and element positioning

## Workflow with browser_get_page
1. First use browser_get_page to see numbered elements: <button id=0>Submit</button>
2. Then use element_id: 0 to click that specific button
3. The tool will resolve the element's coordinates and perform precise clicking

## Examples
- Basic click: {"element_id": 5}
- With timeout: {"element_id": 3, "timeout": 15000}

## Common Use Cases
* Clicking buttons (submit, cancel, navigation)
* Following links
* Activating form controls
* Triggering interactive elements
* Closing modals or popups

## Advanced Features
- Element ID-based targeting (more reliable than CSS selector clicking)
- Automatic link target removal to prevent new tabs
- Element center coordinate calculation for precise clicking
- Integration with the element indexing system from browser_get_page

## Important Notes
- Requires recent browser_get_page call to populate element index
- Element must be visible and within viewport for reliable clicking
- Uses element ID resolution with coordinate-based clicking for better automation reliability
- Automatically handles link target removal to prevent new tabs`
}

func (t ClickTool) ValidateInput(state tools.State, parameters string) error {
	var input ClickInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.ElementID <= 0 {
		return fmt.Errorf("element_id is required and must be positive")
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

	element, exists := manager.(*Manager).GetElement(input.ElementID)
	if !exists {
		logger.G(ctx).WithField("element_id", input.ElementID).Info("Element ID not found in buffer")
		return ClickResult{
			Success:      false,
			ElementFound: false,
			Error:        fmt.Sprintf("element ID %d not found - ensure browser_get_page was called recently", input.ElementID),
		}
	}

	clickX := float64(element.CenterX)
	clickY := float64(element.CenterY)

	err := chromedp.Run(timeoutCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`
				const links = document.getElementsByTagName("a");
				for (var i = 0; i < links.length; i++) {
					links[i].removeAttribute("target");
				}
			`, nil).Do(ctx)
		}),
	)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to remove target attributes (non-critical)")
	}

	// Perform coordinate-based click
	err = chromedp.Run(timeoutCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Use mouse click at exact coordinates
			return chromedp.MouseClickXY(clickX, clickY).Do(ctx)
		}),
	)

	if err != nil {
		logger.G(ctx).WithFields(map[string]any{
			"x":          clickX,
			"y":          clickY,
			"element_id": input.ElementID,
			"method":     t.getClickMethod(input),
		}).WithError(err).Info("Element ID-based click failed")
		return ClickResult{
			Success:      false,
			ElementFound: true,
			Error:        fmt.Sprintf("click failed: %v", err),
		}
	}

	logger.G(ctx).WithFields(map[string]any{
		"x":          clickX,
		"y":          clickY,
		"element_id": input.ElementID,
		"method":     t.getClickMethod(input),
	}).Info("Element ID-based click successful")

	return ClickResult{
		Success:      true,
		ElementFound: true,
	}
}

// getClickMethod returns a string describing which click method was used
func (t ClickTool) getClickMethod(_ ClickInput) string {
	return "element_id"
}

func (t ClickTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input ClickInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("browser.click.method", t.getClickMethod(input)),
		attribute.Int("browser.click.element_id", input.ElementID),
		attribute.Int("browser.click.timeout", input.Timeout),
	}, nil
}
