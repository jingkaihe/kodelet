package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestBrowserNavigateRenderer(t *testing.T) {
	renderer := &BrowserNavigateRenderer{}

	t.Run("Successful navigation", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_navigate",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserNavigateMetadata{
				URL:      "https://example.com",
				FinalURL: "https://example.com/home",
				Title:    "Example Website",
				LoadTime: 500 * time.Millisecond,
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "✅ Navigated to: https://example.com") {
			t.Errorf("Expected navigation success message, got: %s", output)
		}
		if !strings.Contains(output, "Final URL: https://example.com/home") {
			t.Errorf("Expected final URL in output, got: %s", output)
		}
		if !strings.Contains(output, "Page Title: Example Website") {
			t.Errorf("Expected page title in output, got: %s", output)
		}
		if !strings.Contains(output, "Load Time: 500ms") {
			t.Errorf("Expected load time in output, got: %s", output)
		}
	})

	t.Run("Navigation with minimal metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_navigate",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserNavigateMetadata{
				URL: "https://example.com",
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "✅ Navigated to: https://example.com") {
			t.Errorf("Expected navigation success message, got: %s", output)
		}
		if strings.Contains(output, "Final URL:") {
			t.Errorf("Should not show final URL when not different from URL, got: %s", output)
		}
	})

	t.Run("Navigation error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_navigate",
			Success:   false,
			Error:     "Connection timeout",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "❌ Navigation failed: Connection timeout" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_navigate",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserClickMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for browser_navigate") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}

func TestBrowserClickRenderer(t *testing.T) {
	renderer := &BrowserClickRenderer{}

	t.Run("Successful click", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_click",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserClickMetadata{
				ElementID:    123,
				ElementFound: true,
			},
		}

		output := renderer.RenderCLI(result)

		if output != "✅ Element clicked successfully" {
			t.Errorf("Expected success message, got: %s", output)
		}
	})

	t.Run("Click error - element not found", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_click",
			Success:   false,
			Error:     "Element not found",
			Timestamp: time.Now(),
			Metadata: &tools.BrowserClickMetadata{
				ElementID:    123,
				ElementFound: false,
			},
		}

		output := renderer.RenderCLI(result)

		if output != "❌ Element not found or not clickable" {
			t.Errorf("Expected element not found message, got: %s", output)
		}
	})

	t.Run("Click error - general error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_click",
			Success:   false,
			Error:     "Page not loaded",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "❌ Click failed: Page not loaded" {
			t.Errorf("Expected general error message, got: %s", output)
		}
	})
}

func TestBrowserGetPageRenderer(t *testing.T) {
	renderer := &BrowserGetPageRenderer{}

	t.Run("Successful page retrieval", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_get_page",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserGetPageMetadata{
				URL:       "https://example.com",
				Title:     "Example Page",
				HTMLSize:  1024,
				Truncated: false,
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "✅ Page content retrieved") {
			t.Errorf("Expected success message, got: %s", output)
		}
		if !strings.Contains(output, "URL: https://example.com") {
			t.Errorf("Expected URL in output, got: %s", output)
		}
		if !strings.Contains(output, "Title: Example Page") {
			t.Errorf("Expected title in output, got: %s", output)
		}
		if !strings.Contains(output, "HTML Length: 1024 characters") {
			t.Errorf("Expected HTML length in output, got: %s", output)
		}
	})

	t.Run("Page retrieval with truncation", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_get_page",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserGetPageMetadata{
				URL:       "https://example.com",
				Title:     "Large Page",
				HTMLSize:  100000,
				Truncated: true,
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "✅ Page content retrieved (truncated)") {
			t.Errorf("Expected truncation indication, got: %s", output)
		}
	})

	t.Run("Page retrieval error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_get_page",
			Success:   false,
			Error:     "Page not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "❌ Failed to get page content: Page not found" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_get_page",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for browser_get_page") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}

func TestBrowserScreenshotRenderer(t *testing.T) {
	renderer := &BrowserScreenshotRenderer{}

	t.Run("Successful screenshot", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_screenshot",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserScreenshotMetadata{
				OutputPath: "/tmp/screenshot.png",
				Width:      1920,
				Height:     1080,
				Format:     "png",
				FullPage:   true,
				FileSize:   2048,
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "✅ Screenshot saved to: /tmp/screenshot.png") {
			t.Errorf("Expected screenshot path in output, got: %s", output)
		}
		if !strings.Contains(output, "Dimensions: 1920x1080") {
			t.Errorf("Expected dimensions in output, got: %s", output)
		}
		if !strings.Contains(output, "Format: png") {
			t.Errorf("Expected format in output, got: %s", output)
		}
		if !strings.Contains(output, "Full page: Yes") {
			t.Errorf("Expected full page indicator, got: %s", output)
		}
		if !strings.Contains(output, "File size: 2048 bytes") {
			t.Errorf("Expected file size in output, got: %s", output)
		}
	})

	t.Run("Screenshot without full page", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_screenshot",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserScreenshotMetadata{
				OutputPath: "/tmp/screenshot.png",
				Width:      800,
				Height:     600,
				Format:     "png",
				FullPage:   false,
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "✅ Screenshot saved to: /tmp/screenshot.png") {
			t.Errorf("Expected screenshot path in output, got: %s", output)
		}
		if strings.Contains(output, "Full page: Yes") {
			t.Errorf("Should not show full page indicator when false, got: %s", output)
		}
	})

	t.Run("Screenshot error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_screenshot",
			Success:   false,
			Error:     "Failed to capture screenshot",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "❌ Screenshot failed: Failed to capture screenshot" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_screenshot",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for browser_screenshot") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}

func TestBrowserTypeRenderer(t *testing.T) {
	renderer := &BrowserTypeRenderer{}

	t.Run("Successful type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_type",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserTypeMetadata{
				ElementID: 456,
				Text:      "hello world",
				Cleared:   false,
			},
		}

		output := renderer.RenderCLI(result)

		if output != "✅ Text typed successfully" {
			t.Errorf("Expected success message, got: %s", output)
		}
	})

	t.Run("Type with field cleared", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_type",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserTypeMetadata{
				ElementID: 456,
				Text:      "hello world",
				Cleared:   true,
			},
		}

		output := renderer.RenderCLI(result)

		if output != "✅ Text typed successfully (field cleared first)" {
			t.Errorf("Expected success message with cleared indicator, got: %s", output)
		}
	})

	t.Run("Type error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_type",
			Success:   false,
			Error:     "Element not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "❌ Type failed: Element not found" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_type",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for browser_type") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}

func TestBrowserWaitForRenderer(t *testing.T) {
	renderer := &BrowserWaitForRenderer{}

	t.Run("Successful wait - condition met", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_wait_for",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserWaitForMetadata{
				Condition: "visible",
				Selector:  "#submit-button",
				Found:     true,
			},
		}

		output := renderer.RenderCLI(result)

		if output != "✅ Condition met: visible" {
			t.Errorf("Expected condition met message, got: %s", output)
		}
	})

	t.Run("Successful wait - timeout", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_wait_for",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BrowserWaitForMetadata{
				Condition: "clickable",
				Selector:  "#submit-button",
				Found:     false,
			},
		}

		output := renderer.RenderCLI(result)

		if output != "⏱️ Timeout waiting for: clickable" {
			t.Errorf("Expected timeout message, got: %s", output)
		}
	})

	t.Run("Wait error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_wait_for",
			Success:   false,
			Error:     "Page not loaded",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "❌ Wait failed: Page not loaded" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_wait_for",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for browser_wait_for") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}
