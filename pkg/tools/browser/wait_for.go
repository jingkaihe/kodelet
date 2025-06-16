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
	Condition string `json:"condition" jsonschema:"required,enum=page_load,enum=element_visible,enum=element_hidden,description=Condition to wait for"`
	Selector  string `json:"selector" jsonschema:"description=CSS selector (required for element conditions)"`
	Timeout   int    `json:"timeout" jsonschema:"default=30000,description=Maximum time to wait"`
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

func (r WaitForResult) UserFacing() string {
	if !r.Success {
		return fmt.Sprintf("❌ Wait failed: %s", r.Error)
	}
	if !r.ConditionMet {
		return "⏰ Wait timeout - condition not met"
	}
	return "✅ Wait condition met successfully"
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
	return "Wait for page load or element condition"
}

func (t WaitForTool) ValidateInput(state tools.State, parameters string) error {
	var input WaitForInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	if input.Condition == "" {
		return fmt.Errorf("condition is required")
	}

	validConditions := map[string]bool{
		"page_load":        true,
		"element_visible":  true,
		"element_hidden":   true,
	}

	if !validConditions[input.Condition] {
		return fmt.Errorf("invalid condition: %s. Valid conditions: page_load, element_visible, element_hidden", input.Condition)
	}

	// Selector is required for element conditions
	if (input.Condition == "element_visible" || input.Condition == "element_hidden") && input.Selector == "" {
		return fmt.Errorf("selector is required for condition: %s", input.Condition)
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

	// Wait for the specified condition
	err := WaitForCondition(timeoutCtx, input.Condition, input.Selector, timeout)

	if err != nil {
		// Check if it's a timeout error
		if timeoutCtx.Err() == context.DeadlineExceeded {
			logger.G(ctx).WithFields(map[string]interface{}{
				"condition": input.Condition,
				"selector": input.Selector,
				"timeout": input.Timeout,
			}).Info("Wait condition timeout")
			return WaitForResult{
				Success:      true,
				ConditionMet: false,
			}
		}

		logger.G(ctx).WithFields(map[string]interface{}{
			"condition": input.Condition,
			"selector": input.Selector,
		}).WithError(err).Info("Wait condition failed")
		return WaitForResult{
			Success: false,
			Error:   fmt.Sprintf("wait failed: %v", err),
		}
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"condition": input.Condition,
		"selector": input.Selector,
	}).Info("Wait condition met")

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

	kvs := []attribute.KeyValue{
		attribute.String("browser.wait_for.condition", input.Condition),
		attribute.Int("browser.wait_for.timeout", input.Timeout),
	}

	if input.Selector != "" {
		kvs = append(kvs, attribute.String("browser.wait_for.selector", input.Selector))
	}

	return kvs, nil
}