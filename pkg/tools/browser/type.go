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

type TypeTool struct{}

type TypeInput struct {
	ElementID int    `json:"element_id" jsonschema:"required,description=Element ID from get_page output"`
	Text      string `json:"text" jsonschema:"required,description=Text to type"`
	Clear     bool   `json:"clear" jsonschema:"default=true,description=Clear field before typing"`
	Submit    bool   `json:"submit" jsonschema:"default=false,description=Press Enter after typing"`
	Timeout   int    `json:"timeout" jsonschema:"default=10000,description=Timeout to wait for element"`
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
	return `Type text into input fields using element ID-based targeting with automatic coordinate resolution.

## Parameters
- element_id: Element ID from browser_get_page output (required)
- text: Text content to type into the element (required)
- clear: Whether to clear the field before typing (default: true)
- submit: Whether to press Enter after typing (default: false)
- timeout: Maximum wait time for element to be visible in milliseconds (default: 10000)

## Behavior
- Uses element ID from browser_get_page output for targeting
- Resolves element coordinates from internal element buffer
- Clicks on the element first to focus it
- Optionally clears existing content (if clear=true)
- Types the specified text using keyboard simulation
- Optionally submits by pressing Enter (if submit=true)

## Supported Element Types
- Text inputs: <input type="text">, <input type="email">, <input type="password">
- Number inputs: <input type="number">
- Text areas: <textarea>
- Content editable: Elements with contentEditable="true"
- Search inputs: <input type="search">

## Clear Behavior
- clear=true (default): Selects all existing text and replaces it
- clear=false: Appends text to existing content at cursor position

## Submit Behavior
- submit=false (default): Just types the text
- submit=true: Types text then presses Enter for submission

## Workflow with browser_get_page
1. First use browser_get_page to see numbered elements: <input id=2 type=text name='username'>
2. Then use element_id: 2 to type into that specific input
3. The tool will resolve coordinates, click the element first, then type the text

## Examples
- Basic typing: {"element_id": 5, "text": "john.doe"}
- Type and submit: {"element_id": 3, "text": "search query", "submit": true}
- Append text: {"element_id": 2, "text": " additional text", "clear": false}
- With timeout: {"element_id": 1, "text": "test", "timeout": 15000}

## Common Use Cases
* Filling out forms (login, registration, contact)
* Entering search terms with automatic submission
* Updating text content in existing fields
* Providing input for web applications
* Testing form validation and submission

## Important Notes
- Element ID method requires recent browser_get_page call to populate element index
- Element must be editable (input, textarea, or contentEditable)
- Uses click-first approach for reliable focus using coordinate resolution
- Submit option provides convenient text entry with submission
- For password fields, the text will still be visible in logs`
}

func (t TypeTool) ValidateInput(state tools.State, parameters string) error {
	var input TypeInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "failed to parse input")
	}

	if input.ElementID <= 0 {
		return errors.New("element_id is required and must be positive")
	}

	if input.Text == "" {
		return errors.New("text is required")
	}

	if input.Timeout < 0 {
		return errors.New("timeout must be non-negative")
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

	element, exists := manager.(*Manager).GetElement(input.ElementID)
	if !exists {
		logger.G(ctx).WithField("element_id", input.ElementID).Info("Element ID not found in buffer")
		return TypeResult{
			Success:      false,
			ElementFound: false,
			Error:        fmt.Sprintf("element ID %d not found - ensure browser_get_page was called recently", input.ElementID),
		}
	}

	clickX := float64(element.CenterX)
	clickY := float64(element.CenterY)

	// First click on the element to focus it
	err := chromedp.Run(timeoutCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.MouseClickXY(clickX, clickY).Do(ctx)
		}),
	)

	if err != nil {
		logger.G(ctx).WithField("element_id", input.ElementID).WithError(err).Info("Failed to click element for focus")
		return TypeResult{
			Success:      false,
			ElementFound: true,
			Error:        fmt.Sprintf("failed to focus element: %v", err),
		}
	}

	// Build typing actions
	var actions []chromedp.Action

	// Clear field if requested
	if input.Clear {
		actions = append(actions,
			chromedp.KeyEvent("ctrl+a"),
		)
	}

	// Type the text using keyboard simulation
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		return chromedp.KeyEvent(input.Text).Do(ctx)
	}))

	// Submit if requested
	if input.Submit {
		actions = append(actions, chromedp.KeyEvent("\n"))
	}

	// Execute the typing actions
	err = chromedp.Run(timeoutCtx, actions...)

	if err != nil {
		logger.G(ctx).WithField("element_id", input.ElementID).WithError(err).Info("Element ID-based type failed")
		return TypeResult{
			Success:      false,
			ElementFound: true,
			Error:        fmt.Sprintf("type failed: %v", err),
		}
	}

	logger.G(ctx).WithFields(map[string]any{
		"element_id":  input.ElementID,
		"text_length": len(input.Text),
		"submit":      input.Submit,
		"clear":       input.Clear,
	}).Info("Element ID-based type successful")

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
		attribute.Int("browser.type.element_id", input.ElementID),
		attribute.Int("browser.type.text_length", len(input.Text)),
		attribute.Bool("browser.type.clear", input.Clear),
		attribute.Bool("browser.type.submit", input.Submit),
		attribute.Int("browser.type.timeout", input.Timeout),
	}, nil
}
