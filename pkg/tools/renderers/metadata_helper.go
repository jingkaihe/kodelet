package renderers

import (
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// extractMetadata is a helper that handles both pointer and value type assertions
// This is necessary because JSON unmarshaling creates value types, while
// direct creation uses pointer types
func extractMetadata(metadata tools.ToolMetadata, target interface{}) bool {
	if metadata == nil {
		return false
	}

	switch target := target.(type) {
	case *tools.FileReadMetadata:
		switch m := metadata.(type) {
		case *tools.FileReadMetadata:
			*target = *m
			return true
		case tools.FileReadMetadata:
			*target = m
			return true
		}
	case *tools.FileWriteMetadata:
		switch m := metadata.(type) {
		case *tools.FileWriteMetadata:
			*target = *m
			return true
		case tools.FileWriteMetadata:
			*target = m
			return true
		}
	case *tools.FileEditMetadata:
		switch m := metadata.(type) {
		case *tools.FileEditMetadata:
			*target = *m
			return true
		case tools.FileEditMetadata:
			*target = m
			return true
		}
	case *tools.FileMultiEditMetadata:
		switch m := metadata.(type) {
		case *tools.FileMultiEditMetadata:
			*target = *m
			return true
		case tools.FileMultiEditMetadata:
			*target = m
			return true
		}
	case *tools.BashMetadata:
		switch m := metadata.(type) {
		case *tools.BashMetadata:
			*target = *m
			return true
		case tools.BashMetadata:
			*target = m
			return true
		}
	case *tools.GrepMetadata:
		switch m := metadata.(type) {
		case *tools.GrepMetadata:
			*target = *m
			return true
		case tools.GrepMetadata:
			*target = m
			return true
		}
	case *tools.GlobMetadata:
		switch m := metadata.(type) {
		case *tools.GlobMetadata:
			*target = *m
			return true
		case tools.GlobMetadata:
			*target = m
			return true
		}
	case *tools.TodoMetadata:
		switch m := metadata.(type) {
		case *tools.TodoMetadata:
			*target = *m
			return true
		case tools.TodoMetadata:
			*target = m
			return true
		}
	case *tools.ThinkingMetadata:
		switch m := metadata.(type) {
		case *tools.ThinkingMetadata:
			*target = *m
			return true
		case tools.ThinkingMetadata:
			*target = m
			return true
		}
	case *tools.BatchMetadata:
		switch m := metadata.(type) {
		case *tools.BatchMetadata:
			*target = *m
			return true
		case tools.BatchMetadata:
			*target = m
			return true
		}
	case *tools.SubAgentMetadata:
		switch m := metadata.(type) {
		case *tools.SubAgentMetadata:
			*target = *m
			return true
		case tools.SubAgentMetadata:
			*target = m
			return true
		}
	case *tools.ImageRecognitionMetadata:
		switch m := metadata.(type) {
		case *tools.ImageRecognitionMetadata:
			*target = *m
			return true
		case tools.ImageRecognitionMetadata:
			*target = m
			return true
		}
	case *tools.WebFetchMetadata:
		switch m := metadata.(type) {
		case *tools.WebFetchMetadata:
			*target = *m
			return true
		case tools.WebFetchMetadata:
			*target = m
			return true
		}
	case *tools.ViewBackgroundProcessesMetadata:
		switch m := metadata.(type) {
		case *tools.ViewBackgroundProcessesMetadata:
			*target = *m
			return true
		case tools.ViewBackgroundProcessesMetadata:
			*target = m
			return true
		}
	case *tools.MCPToolMetadata:
		switch m := metadata.(type) {
		case *tools.MCPToolMetadata:
			*target = *m
			return true
		case tools.MCPToolMetadata:
			*target = m
			return true
		}
	case *tools.BrowserNavigateMetadata:
		switch m := metadata.(type) {
		case *tools.BrowserNavigateMetadata:
			*target = *m
			return true
		case tools.BrowserNavigateMetadata:
			*target = m
			return true
		}
	case *tools.BrowserClickMetadata:
		switch m := metadata.(type) {
		case *tools.BrowserClickMetadata:
			*target = *m
			return true
		case tools.BrowserClickMetadata:
			*target = m
			return true
		}
	case *tools.BrowserTypeMetadata:
		switch m := metadata.(type) {
		case *tools.BrowserTypeMetadata:
			*target = *m
			return true
		case tools.BrowserTypeMetadata:
			*target = m
			return true
		}
	case *tools.BrowserScreenshotMetadata:
		switch m := metadata.(type) {
		case *tools.BrowserScreenshotMetadata:
			*target = *m
			return true
		case tools.BrowserScreenshotMetadata:
			*target = m
			return true
		}
	case *tools.BrowserGetPageMetadata:
		switch m := metadata.(type) {
		case *tools.BrowserGetPageMetadata:
			*target = *m
			return true
		case tools.BrowserGetPageMetadata:
			*target = m
			return true
		}
	case *tools.BrowserWaitForMetadata:
		switch m := metadata.(type) {
		case *tools.BrowserWaitForMetadata:
			*target = *m
			return true
		case tools.BrowserWaitForMetadata:
			*target = m
			return true
		}
	}
	
	return false
}