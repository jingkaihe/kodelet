package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chromedp/chromedp"
	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

type ScreenshotTool struct{}

type ScreenshotInput struct {
	FullPage bool   `json:"full_page" jsonschema:"default=false,description=Capture full page or just viewport"`
	Format   string `json:"format" jsonschema:"default=png,enum=png,enum=jpeg"`
}

type ScreenshotResult struct {
	Success    bool   `json:"success"`
	OutputPath string `json:"output_path,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (r ScreenshotResult) AssistantFacing() string {
	if !r.Success {
		return tools.StringifyToolResult("", r.Error)
	}
	result := fmt.Sprintf("Screenshot saved to %s (%dx%d)", r.OutputPath, r.Width, r.Height)
	return tools.StringifyToolResult(result, "")
}

func (r ScreenshotResult) IsError() bool {
	return !r.Success
}

func (r ScreenshotResult) GetError() string {
	return r.Error
}

func (r ScreenshotResult) GetResult() string {
	return r.OutputPath
}

func (t ScreenshotTool) GenerateSchema() *jsonschema.Schema {
	return generateSchema[ScreenshotInput]()
}

func (t ScreenshotTool) Name() string {
	return "browser_screenshot"
}

func (t ScreenshotTool) Description() string {
	return `Capture a screenshot of the current web page with flexible formatting and sizing options. For best results, use browser_wait_for tool with "page_load" condition before taking screenshots to ensure the page is fully loaded.

## Parameters
- full_page: Whether to capture the entire page or just the viewport (default: false)
- format: Image format for the screenshot - "png" or "jpeg" (default: "png")

## Capture Modes
- full_page=false: Captures only the visible viewport area (current browser window view)
- full_page=true: Captures the entire page including content below the fold

## Format Options
- PNG: Lossless format, larger file size, supports transparency
- JPEG: Lossy format, smaller file size, no transparency support

## Behavior
- Captures screenshot of current page state immediately
- Automatically generates a unique filename with timestamp
- Saves screenshot to a temporary directory
- Returns the file path, dimensions, and success status
- Works with the current page loaded in the browser

## Recommended Workflow
1. Navigate to page using browser_navigate
2. Wait for page load using browser_wait_for with "page_load" condition
3. Take screenshot using this tool

## Common Use Cases
* Documenting page states for testing or debugging
* Capturing visual evidence of UI issues
* Creating snapshots for comparison testing
* Recording page layouts at different viewport sizes
* Generating visual documentation

## File Management
- Screenshots are saved with timestamps for uniqueness
- Files are saved to the system temporary directory
- File paths are returned for further processing or reference

## Examples
- Viewport only (visible area): {"full_page": false} or just {}
- Viewport JPEG: {"full_page": false, "format": "jpeg"}
- Full page PNG: {"full_page": true, "format": "png"}
- Quick viewport screenshot: {} (uses default: full_page=false)

## Important Notes
- Full page screenshots may be very large for long pages
- JPEG format is recommended for large screenshots to reduce file size
- The tool requires an active browser session with a loaded page
- Screenshot quality is affected by the browser's zoom level and display settings
- Always use browser_wait_for tool before screenshots to ensure page content is fully loaded`
}

func (t ScreenshotTool) ValidateInput(state tools.State, parameters string) error {
	var input ScreenshotInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "failed to parse input")
	}

	// Validate format
	if input.Format != "" && input.Format != "png" && input.Format != "jpeg" {
		return errors.Errorf("invalid format: %s. Valid formats: png, jpeg", input.Format)
	}

	return nil
}

func (t ScreenshotTool) Execute(ctx context.Context, state tools.State, parameters string) tools.ToolResult {
	var input ScreenshotInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return ScreenshotResult{
			Success: false,
			Error:   fmt.Sprintf("failed to parse input: %v", err),
		}
	}

	// Set defaults
	if input.Format == "" {
		input.Format = "png"
	}

	// Get browser manager and ensure it's active
	manager := GetManagerFromState(state)
	if err := manager.EnsureActive(ctx); err != nil {
		return ScreenshotResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start browser: %v", err),
		}
	}

	browserCtx := manager.GetContext()
	if browserCtx == nil {
		return ScreenshotResult{
			Success: false,
			Error:   "browser context not available",
		}
	}

	// Generate screenshot path
	screenshotPath, err := GenerateScreenshotPath(input.Format)
	if err != nil {
		return ScreenshotResult{
			Success: false,
			Error:   fmt.Sprintf("failed to generate screenshot path: %v", err),
		}
	}

	// Get page dimensions
	var width, height int64
	err = chromedp.Run(browserCtx,
		chromedp.Evaluate(`window.innerWidth`, &width),
		chromedp.Evaluate(`window.innerHeight`, &height),
	)

	if err != nil {
		logger.G(ctx).WithError(err).Info("Failed to get page dimensions")
		// Continue with screenshot even if we can't get dimensions
		width, height = 0, 0
	}

	// Take screenshot
	var screenshotBytes []byte
	var screenshotAction chromedp.Action

	if input.FullPage {
		screenshotAction = chromedp.FullScreenshot(&screenshotBytes, 90)
	} else {
		// Use viewport screenshot instead of element screenshot to avoid hanging
		screenshotAction = chromedp.CaptureScreenshot(&screenshotBytes)
	}

	err = chromedp.Run(browserCtx, screenshotAction)

	if err != nil {
		logger.G(ctx).WithField("path", screenshotPath).WithError(err).Info("Screenshot failed")
		return ScreenshotResult{
			Success: false,
			Error:   fmt.Sprintf("screenshot failed: %v", err),
		}
	}

	// Save screenshot to file
	err = os.WriteFile(screenshotPath, screenshotBytes, 0644)
	if err != nil {
		return ScreenshotResult{
			Success: false,
			Error:   fmt.Sprintf("failed to save screenshot: %v", err),
		}
	}

	// Update dimensions based on screenshot type
	if input.FullPage {
		// For full page screenshots, get the scroll dimensions
		err = chromedp.Run(browserCtx,
			chromedp.Evaluate(`document.documentElement.scrollWidth`, &width),
			chromedp.Evaluate(`document.documentElement.scrollHeight`, &height),
		)
		if err != nil {
			// Use fallback dimensions if we can't get scroll dimensions
			width, height = 1920, 1080
		}
	} else {
		// For viewport screenshots, use the window inner dimensions
		if width == 0 || height == 0 {
			// Fallback to viewport dimensions if not already set
			err = chromedp.Run(browserCtx,
				chromedp.Evaluate(`window.innerWidth`, &width),
				chromedp.Evaluate(`window.innerHeight`, &height),
			)
			if err != nil {
				// Use browser window size as final fallback
				width, height = 1920, 1080
			}
		}
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"path":      screenshotPath,
		"full_page": input.FullPage,
		"format":    input.Format,
		"width":     width,
		"height":    height,
	}).Info("Screenshot successful")

	return ScreenshotResult{
		Success:    true,
		OutputPath: screenshotPath,
		Width:      int(width),
		Height:     int(height),
	}
}

func (t ScreenshotTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input ScreenshotInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.Bool("browser.screenshot.full_page", input.FullPage),
		attribute.String("browser.screenshot.format", input.Format),
	}, nil
}
