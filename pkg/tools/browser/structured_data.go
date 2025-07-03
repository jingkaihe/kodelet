package browser

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// StructuredData implementations for browser tool results

// getFormatFromPath extracts the format from the file extension
func getFormatFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "png"
	case ".jpg", ".jpeg":
		return "jpeg"
	default:
		return "png" // Default fallback
	}
}

func (r NavigateResult) StructuredData() tools.StructuredToolResult {
	result := tools.StructuredToolResult{
		ToolName:  "browser_navigate",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
	}

	if r.Success {
		result.Metadata = &tools.BrowserNavigateMetadata{
			URL:   r.URL,
			Title: r.Title,
		}
	}

	return result
}

func (r ScreenshotResult) StructuredData() tools.StructuredToolResult {
	result := tools.StructuredToolResult{
		ToolName:  "browser_screenshot",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
	}

	if r.Success {
		result.Metadata = &tools.BrowserScreenshotMetadata{
			OutputPath: r.OutputPath,
			Width:      r.Width,
			Height:     r.Height,
			Format:     getFormatFromPath(r.OutputPath),
		}
	}

	return result
}

func (r TypeResult) StructuredData() tools.StructuredToolResult {
	result := tools.StructuredToolResult{
		ToolName:  "browser_type",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
	}

	if r.Success || r.ElementFound {
		result.Metadata = &tools.BrowserTypeMetadata{
			// Note: ElementID and Text are not available in TypeResult
			// These would need to be added to TypeResult or passed differently
			Cleared: false, // Default value, could be enhanced
		}
	}

	return result
}

func (r WaitForResult) StructuredData() tools.StructuredToolResult {
	result := tools.StructuredToolResult{
		ToolName:  "browser_wait_for",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
	}

	if r.Success {
		result.Metadata = &tools.BrowserWaitForMetadata{
			Found:     r.ConditionMet,
			Condition: "page_load",
			Selector:  "body",
			// Note: Timeout are not available in WaitForResult
		}
	}

	return result
}
