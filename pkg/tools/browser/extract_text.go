package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

type ExtractTextTool struct{}

type ExtractTextInput struct {
	Selector string `json:"selector" jsonschema:"required,description=CSS selector for element(s)"`
	Multiple bool   `json:"multiple" jsonschema:"default=false,description=Extract from all matching elements"`
	Timeout  int    `json:"timeout" jsonschema:"default=10000,description=Timeout to wait for element"`
}

type ExtractTextResult struct {
	Success bool        `json:"success"`
	Text    interface{} `json:"text"` // string or []string
	Count   int         `json:"count"`
	Error   string      `json:"error,omitempty"`
}

func (r ExtractTextResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	if r.Count == 0 {
		return tools.StringifyToolResult("No text found", "")
	}
	var result string
	if texts, ok := r.Text.([]string); ok {
		result = fmt.Sprintf("Extracted text from %d elements:\n%s", r.Count, strings.Join(texts, "\n"))
	} else {
		result = fmt.Sprintf("Extracted text: %s", r.Text)
	}
	return tools.StringifyToolResult(result, "")
}

func (r ExtractTextResult) UserFacing() string {
	if !r.Success {
		return fmt.Sprintf("❌ Text extraction failed: %s", r.Error)
	}
	
	if r.Count == 0 {
		return "📝 No text found"
	}
	
	return fmt.Sprintf("✅ Text extracted from %d element(s)", r.Count)
}

func (r ExtractTextResult) IsError() bool {
	return !r.Success
}

func (r ExtractTextResult) GetError() string {
	return r.Error
}

func (r ExtractTextResult) GetResult() string {
	if texts, ok := r.Text.([]string); ok {
		return strings.Join(texts, "\n")
	}
	if text, ok := r.Text.(string); ok {
		return text
	}
	return ""
}

func (t ExtractTextTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[ExtractTextInput]()
}

func (t ExtractTextTool) Name() string {
	return "browser_extract_text"
}

func (t ExtractTextTool) Description() string {
	return "Extract text from element(s)"
}

func (t ExtractTextTool) ValidateInput(state tools.State, parameters string) error {
	var input ExtractTextInput
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

func (t ExtractTextTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input ExtractTextInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return ExtractTextResult{
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
		return ExtractTextResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return ExtractTextResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Create timeout context
	timeout := time.Duration(input.Timeout) * time.Millisecond
	timeoutCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	if input.Multiple {
		// Extract text from all matching elements
		var texts []string
		err := chromedp.Run(timeoutCtx,
			chromedp.WaitVisible(input.Selector),
			chromedp.Evaluate(fmt.Sprintf(`
				Array.from(document.querySelectorAll("%s")).map(el => el.textContent.trim())
			`, input.Selector), &texts),
		)

		if err != nil {
			logger.G(ctx).WithField("selector", input.Selector).WithField("multiple", true).WithError(err).Info("Text extraction failed")
			return ExtractTextResult{
				Success: false,
				Error:   fmt.Sprintf("text extraction failed: %v", err),
			}
		}

		// Filter out empty strings
		var filteredTexts []string
		for _, text := range texts {
			if text != "" {
				filteredTexts = append(filteredTexts, text)
			}
		}

		logger.G(ctx).WithField("selector", input.Selector).WithField("multiple", true).WithField("count", len(filteredTexts)).Info("Text extraction successful")

		return ExtractTextResult{
			Success: true,
			Text:    filteredTexts,
			Count:   len(filteredTexts),
		}
	} else {
		// Extract text from single element
		var text string
		err := chromedp.Run(timeoutCtx,
			chromedp.WaitVisible(input.Selector),
			chromedp.Text(input.Selector, &text),
		)

		if err != nil {
			logger.G(ctx).WithField("selector", input.Selector).WithField("multiple", false).WithError(err).Info("Text extraction failed")
			return ExtractTextResult{
				Success: false,
				Error:   fmt.Sprintf("text extraction failed: %v", err),
			}
		}

		count := 0
		if text != "" {
			count = 1
		}

		logger.G(ctx).WithField("selector", input.Selector).WithField("multiple", false).WithField("text_length", len(text)).Info("Text extraction successful")

		return ExtractTextResult{
			Success: true,
			Text:    text,
			Count:   count,
		}
	}
}

func (t ExtractTextTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input ExtractTextInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("browser.extract_text.selector", input.Selector),
		attribute.Bool("browser.extract_text.multiple", input.Multiple),
		attribute.Int("browser.extract_text.timeout", input.Timeout),
	}, nil
}