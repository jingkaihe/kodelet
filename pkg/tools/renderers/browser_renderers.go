package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BrowserNavigateRenderer renders browser navigation results
type BrowserNavigateRenderer struct{}

func (r *BrowserNavigateRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("❌ Navigation failed: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.BrowserNavigateMetadata)
	if !ok {
		return "Error: Invalid metadata type for browser_navigate"
	}

	output := fmt.Sprintf("✅ Navigated to: %s\n", meta.URL)
	if meta.FinalURL != "" && meta.FinalURL != meta.URL {
		output += fmt.Sprintf("Final URL: %s\n", meta.FinalURL)
	}
	if meta.Title != "" {
		output += fmt.Sprintf("Page Title: %s\n", meta.Title)
	}
	if meta.LoadTime > 0 {
		output += fmt.Sprintf("Load Time: %v\n", meta.LoadTime)
	}

	return output
}

// BrowserClickRenderer renders browser click results
type BrowserClickRenderer struct{}

func (r *BrowserClickRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		meta, ok := result.Metadata.(*tools.BrowserClickMetadata)
		if ok && !meta.ElementFound {
			return "❌ Element not found or not clickable"
		}
		return fmt.Sprintf("❌ Click failed: %s", result.Error)
	}

	return "✅ Element clicked successfully"
}

// BrowserGetPageRenderer renders browser get page results
type BrowserGetPageRenderer struct{}

func (r *BrowserGetPageRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("❌ Failed to get page content: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.BrowserGetPageMetadata)
	if !ok {
		return "Error: Invalid metadata type for browser_get_page"
	}

	status := "✅ Page content retrieved"
	if meta.Truncated {
		status += " (truncated)"
	}

	return fmt.Sprintf("%s\nURL: %s\nTitle: %s\nHTML Length: %d characters",
		status, meta.URL, meta.Title, meta.HTMLSize)
}

// BrowserScreenshotRenderer renders browser screenshot results
type BrowserScreenshotRenderer struct{}

func (r *BrowserScreenshotRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("❌ Screenshot failed: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.BrowserScreenshotMetadata)
	if !ok {
		return "Error: Invalid metadata type for browser_screenshot"
	}

	output := fmt.Sprintf("✅ Screenshot saved to: %s\n", meta.OutputPath)
	output += fmt.Sprintf("Dimensions: %dx%d\n", meta.Width, meta.Height)
	output += fmt.Sprintf("Format: %s\n", meta.Format)
	if meta.FullPage {
		output += "Full page: Yes\n"
	}
	if meta.FileSize > 0 {
		output += fmt.Sprintf("File size: %d bytes\n", meta.FileSize)
	}

	return output
}

// BrowserTypeRenderer renders browser type results
type BrowserTypeRenderer struct{}

func (r *BrowserTypeRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("❌ Type failed: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.BrowserTypeMetadata)
	if !ok {
		return "Error: Invalid metadata type for browser_type"
	}

	output := "✅ Text typed successfully"
	if meta.Cleared {
		output += " (field cleared first)"
	}

	return output
}

// BrowserWaitForRenderer renders browser wait for results
type BrowserWaitForRenderer struct{}

func (r *BrowserWaitForRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("❌ Wait failed: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.BrowserWaitForMetadata)
	if !ok {
		return "Error: Invalid metadata type for browser_wait_for"
	}

	if meta.Found {
		return fmt.Sprintf("✅ Condition met: %s", meta.Condition)
	}
	return fmt.Sprintf("⏱️ Timeout waiting for: %s", meta.Condition)
}
