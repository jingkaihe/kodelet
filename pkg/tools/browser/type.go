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

type TypeTool struct{}

type TypeInput struct {
	Selector string `json:"selector" jsonschema:"required,description=CSS selector for input element"`
	Text     string `json:"text" jsonschema:"required,description=Text to type"`
	Clear    bool   `json:"clear" jsonschema:"default=true,description=Clear field before typing"`
	Timeout  int    `json:"timeout" jsonschema:"default=10000,description=Timeout to wait for element"`
}

type TypeResult struct {
	Success      bool   `json:"success"`
	ElementFound bool   `json:"element_found"`
	Error        string `json:"error,omitempty"`
}

func (r TypeResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	return tools.StringifyToolResult("Text typed successfully", "")
}

func (r TypeResult) UserFacing() string {
	if !r.Success {
		if !r.ElementFound {
			return fmt.Sprintf("❌ Input element not found")
		}
		return fmt.Sprintf("❌ Type failed: %s", r.Error)
	}
	return "✅ Text typed successfully"
}

func (r TypeResult) IsError() bool {
	return !r.Success
}

func (r TypeResult) GetError() string {
	return r.Error
}

func (r TypeResult) GetResult() string {
	if r.Success {
		return "typed"
	}
	return r.Error
}

func (t TypeTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[TypeInput]()
}

func (t TypeTool) Name() string {
	return "browser_type"
}

func (t TypeTool) Description() string {
	return `Type text into input fields, text areas, or other editable elements.

## Parameters
- selector: CSS selector for the input element (required)
- text: Text content to type into the element (required)
- clear: Whether to clear the field before typing (default: true)
- timeout: Maximum wait time for element to be visible in milliseconds (default: 10000)

## Behavior
- Waits for the input element to be visible and editable
- Optionally clears existing content (if clear=true)
- Types the specified text character by character
- Works with input fields, textareas, and contentEditable elements

## Supported Element Types
- Text inputs: <input type="text">, <input type="email">, <input type="password">
- Number inputs: <input type="number">
- Text areas: <textarea>
- Content editable: Elements with contentEditable="true"
- Search inputs: <input type="search">

## Clear Behavior
- clear=true (default): Selects all existing text and replaces it
- clear=false: Appends text to existing content at cursor position

## Common Use Cases
* Filling out forms (login, registration, contact)
* Entering search terms
* Updating text content
* Providing input for web applications
* Testing form validation

## CSS Selector Examples
- By ID: "#username", "#email-input"
- By name: "[name='password']", "[name='search']"
- By class: ".form-control", ".search-input"
- By type: "input[type='text']", "input[type='email']"

## Examples
- Fill username: {"selector": "#username", "text": "john.doe"}
- Append to existing text: {"selector": "#comment", "text": " Additional text", "clear": false}
- With timeout: {"selector": ".dynamic-input", "text": "test", "timeout": 15000}

## Important Notes
- Element must be editable (input, textarea, or contentEditable)
- Use clear=false to append text rather than replace
- For password fields, the text will still be visible in logs
- Some fields may have input validation or formatting that affects the final value`
}

func (t TypeTool) ValidateInput(state tools.State, parameters string) error {
	var input TypeInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.Selector == "" {
		return fmt.Errorf("selector is required")
	}

	if input.Text == "" {
		return fmt.Errorf("text is required")
	}

	if input.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	return nil
}

func (t TypeTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input TypeInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return TypeResult{
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
		return TypeResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return TypeResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Create timeout context
	timeout := time.Duration(input.Timeout) * time.Millisecond
	timeoutCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	// Check if element exists and is an input element
	var exists bool
	err := chromedp.Run(timeoutCtx,
		chromedp.WaitVisible(input.Selector),
		chromedp.Evaluate(fmt.Sprintf(`
			const el = document.querySelector("%s");
			el !== null && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.contentEditable === 'true')
		`, input.Selector), &exists),
	)

	if err != nil || !exists {
		logger.G(ctx).WithField("selector", input.Selector).WithError(err).Info("Input element not found or not editable")
		return TypeResult{
			Success:      false,
			ElementFound: false,
			Error:        fmt.Sprintf("input element not found or not editable: %s", input.Selector),
		}
	}

	// Build typing actions
	var actions []chromedp.Action

	// Clear field if requested
	if input.Clear {
		actions = append(actions,
			chromedp.Click(input.Selector),
			chromedp.KeyEvent("ctrl+a"),
		)
	} else {
		actions = append(actions, chromedp.Click(input.Selector))
	}

	// Type the text
	actions = append(actions, chromedp.SendKeys(input.Selector, input.Text))

	// Execute the typing actions
	err = chromedp.Run(timeoutCtx, actions...)

	if err != nil {
		logger.G(ctx).WithField("selector", input.Selector).WithError(err).Info("Type failed")
		return TypeResult{
			Success:      false,
			ElementFound: true,
			Error:        fmt.Sprintf("type failed: %v", err),
		}
	}

	logger.G(ctx).WithField("selector", input.Selector).WithField("text_length", len(input.Text)).Info("Type successful")

	return TypeResult{
		Success:      true,
		ElementFound: true,
	}
}

func (t TypeTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input TypeInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("browser.type.selector", input.Selector),
		attribute.Int("browser.type.text_length", len(input.Text)),
		attribute.Bool("browser.type.clear", input.Clear),
		attribute.Int("browser.type.timeout", input.Timeout),
	}, nil
}
