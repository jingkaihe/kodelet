package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
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

		assert.Contains(t, output, "✅ Navigated to: https://example.com", "Expected navigation success message")
		assert.Contains(t, output, "Final URL: https://example.com/home", "Expected final URL in output")
		assert.Contains(t, output, "Page Title: Example Website", "Expected page title in output")
		assert.Contains(t, output, "Load Time: 500ms", "Expected load time in output")
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

		assert.Contains(t, output, "✅ Navigated to: https://example.com", "Expected navigation success message")
		assert.NotContains(t, output, "Final URL:", "Should not show final URL when not different from URL")
	})

	t.Run("Navigation error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_navigate",
			Success:   false,
			Error:     "Connection timeout",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "❌ Navigation failed: Connection timeout", output, "Expected error message")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_navigate",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserClickMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for browser_navigate", "Expected invalid metadata error")
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

		assert.Equal(t, "✅ Element clicked successfully", output, "Expected success message")
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

		assert.Equal(t, "❌ Element not found or not clickable", output, "Expected element not found message")
	})

	t.Run("Click error - general error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_click",
			Success:   false,
			Error:     "Page not loaded",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "❌ Click failed: Page not loaded", output, "Expected general error message")
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

		assert.Contains(t, output, "✅ Page content retrieved", "Expected success message")
		assert.Contains(t, output, "URL: https://example.com", "Expected URL in output")
		assert.Contains(t, output, "Title: Example Page", "Expected title in output")
		assert.Contains(t, output, "HTML Length: 1024 characters", "Expected HTML length in output")
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

		assert.Contains(t, output, "✅ Page content retrieved (truncated)", "Expected truncation indication")
	})

	t.Run("Page retrieval error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_get_page",
			Success:   false,
			Error:     "Page not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "❌ Failed to get page content: Page not found", output, "Expected error message")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_get_page",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for browser_get_page", "Expected invalid metadata error")
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

		assert.Contains(t, output, "✅ Screenshot saved to: /tmp/screenshot.png", "Expected screenshot path in output")
		assert.Contains(t, output, "Dimensions: 1920x1080", "Expected dimensions in output")
		assert.Contains(t, output, "Format: png", "Expected format in output")
		assert.Contains(t, output, "Full page: Yes", "Expected full page indicator")
		assert.Contains(t, output, "File size: 2048 bytes", "Expected file size in output")
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

		assert.Contains(t, output, "✅ Screenshot saved to: /tmp/screenshot.png", "Expected screenshot path in output")
		assert.NotContains(t, output, "Full page: Yes", "Should not show full page indicator when false")
	})

	t.Run("Screenshot error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_screenshot",
			Success:   false,
			Error:     "Failed to capture screenshot",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "❌ Screenshot failed: Failed to capture screenshot", output, "Expected error message")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_screenshot",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for browser_screenshot", "Expected invalid metadata error")
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

		assert.Equal(t, "✅ Text typed successfully", output, "Expected success message")
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

		assert.Equal(t, "✅ Text typed successfully (field cleared first)", output, "Expected success message with cleared indicator")
	})

	t.Run("Type error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_type",
			Success:   false,
			Error:     "Element not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "❌ Type failed: Element not found", output, "Expected error message")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_type",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for browser_type", "Expected invalid metadata error")
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

		assert.Equal(t, "✅ Condition met: visible", output, "Expected condition met message")
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

		assert.Equal(t, "⏱️ Timeout waiting for: clickable", output, "Expected timeout message")
	})

	t.Run("Wait error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_wait_for",
			Success:   false,
			Error:     "Page not loaded",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "❌ Wait failed: Page not loaded", output, "Expected error message")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "browser_wait_for",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.BrowserNavigateMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for browser_wait_for", "Expected invalid metadata error")
	})
}
