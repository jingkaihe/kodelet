package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

type WaitForTool struct{}

type WaitForInput struct {
	Timeout int `json:"timeout" jsonschema:"default=30000,description=Maximum time to wait in milliseconds"`
}

type WaitForResult struct {
	Success      bool   `json:"success"`
	ConditionMet bool   `json:"condition_met"`
	Error        string `json:"error,omitempty"`
}

func (r WaitForResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	if !r.ConditionMet {
		return tools.StringifyToolResult("Wait timeout - condition not met", "")
	}
	return tools.StringifyToolResult("Wait condition met successfully", "")
}

func (r WaitForResult) IsError() bool {
	return !r.Success
}

func (r WaitForResult) GetError() string {
	return r.Error
}

func (r WaitForResult) GetResult() string {
	if r.Success && r.ConditionMet {
		return "condition met"
	}
	return r.Error
}

func (t WaitForTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[WaitForInput]()
}

func (t WaitForTool) Name() string {
	return "browser_wait_for"
}

func (t WaitForTool) Description() string {
	return `Wait for the web page to finish loading completely before proceeding.

## Parameters
- timeout: Maximum time to wait for page load in milliseconds (default: 30000)

## Behavior
- Waits for document ready state and all resources to load
- Returns immediately when the page is fully loaded
- Times out after the specified duration if page load is not complete
- Timeout is treated as a valid result, not an error
- Essential for ensuring page content is ready before screenshots or interactions

## Common Use Cases
* Waiting for page content to load after navigation
* Ensuring page is ready before taking screenshots
* Synchronizing with slow-loading resources (images, scripts, stylesheets)
* Handling pages with dynamic content that loads after initial render
* Preparing for reliable browser automation actions

## Examples
- Default timeout (30 seconds): {} or {"timeout": 30000}
- Quick timeout (10 seconds): {"timeout": 10000}
- Extended timeout (60 seconds): {"timeout": 60000}

## Timing Strategies
- Use shorter timeouts (10-15 seconds) for simple pages
- Use longer timeouts (30-60 seconds) for complex pages with many resources
- Consider network conditions when setting timeout values

## Important Notes
- Timeout is not considered a failure - check the returned condition_met status
- This tool waits for all resources including images, scripts, and stylesheets
- Use this tool before browser_screenshot for best results
- Essential for reliable browser automation workflows`
}

func (t WaitForTool) ValidateInput(state tools.State, parameters string) error {
	var input WaitForInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	return nil
}

func (t WaitForTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input WaitForInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return WaitForResult{
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
		return WaitForResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return WaitForResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Create timeout context
	timeout := time.Duration(input.Timeout) * time.Millisecond
	timeoutCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	// Wait for page load condition
	err := WaitForCondition(timeoutCtx, timeout)

	if err != nil {
		// Check if it's a timeout error
		if timeoutCtx.Err() == context.DeadlineExceeded {
			logger.G(ctx).WithField("timeout", input.Timeout).Info("Page load timeout")
			return WaitForResult{
				Success:      true,
				ConditionMet: false,
			}
		}

		logger.G(ctx).WithError(err).Info("Page load wait failed")
		return WaitForResult{
			Success: false,
			Error:   fmt.Sprintf("page load wait failed: %v", err),
		}
	}

	logger.G(ctx).Info("Page load complete")

	return WaitForResult{
		Success:      true,
		ConditionMet: true,
	}
}

func (t WaitForTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input WaitForInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("browser.wait_for.condition", "page_load"),
		attribute.Int("browser.wait_for.timeout", input.Timeout),
	}, nil
}
