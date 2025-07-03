package browser

import (
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// StructuredData implementations for browser tool results
// These are temporary implementations until proper browser metadata types are defined

func (r NavigateResult) StructuredData() tools.StructuredToolResult {
	return tools.StructuredToolResult{
		ToolName:  "browser_navigate",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
		// TODO: Add proper browser metadata once BrowserNavigateMetadata is defined
	}
}

func (r ScreenshotResult) StructuredData() tools.StructuredToolResult {
	return tools.StructuredToolResult{
		ToolName:  "browser_screenshot",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
		// TODO: Add proper browser metadata once BrowserScreenshotMetadata is defined
	}
}

func (r TypeResult) StructuredData() tools.StructuredToolResult {
	return tools.StructuredToolResult{
		ToolName:  "browser_type",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
		// TODO: Add proper browser metadata once BrowserTypeMetadata is defined
	}
}

func (r WaitForResult) StructuredData() tools.StructuredToolResult {
	return tools.StructuredToolResult{
		ToolName:  "browser_wait_for",
		Success:   r.Success,
		Error:     r.Error,
		Timestamp: time.Now(),
		// TODO: Add proper browser metadata once BrowserWaitForMetadata is defined
	}
}
