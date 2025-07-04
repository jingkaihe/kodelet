package renderers

import (
	"fmt"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestWebFetchRenderer(t *testing.T) {
	renderer := &WebFetchRenderer{}

	t.Run("Web fetch with saved file", func(t *testing.T) {
		content := "This is the fetched content\nLine 2\nLine 3"
		result := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.WebFetchMetadata{
				URL:           "https://example.com",
				ProcessedType: "saved",
				SavedPath:     "/tmp/content.html",
				Size:          int64(len(content)),
				Content:       content,
			},
		}

		output := renderer.RenderCLI(result)
		expected := fmt.Sprintf("Web Fetch: %s\nSaved to: %s\n%s",
			"https://example.com", "/tmp/content.html", content)

		if output != expected {
			t.Errorf("Expected output to match RenderCLI() format:\nExpected:\n%s\nGot:\n%s", expected, output)
		}
	})

	t.Run("Web fetch with prompt", func(t *testing.T) {
		content := "Extracted information: The main points are..."
		result := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.WebFetchMetadata{
				URL:           "https://example.com",
				ProcessedType: "ai_extracted",
				Prompt:        "Extract main content",
				Size:          int64(len(content)),
				Content:       content,
			},
		}

		output := renderer.RenderCLI(result)
		expected := fmt.Sprintf("Web Fetch: %s\nPrompt: %s\n%s",
			"https://example.com", "Extract main content", content)

		if output != expected {
			t.Errorf("Expected output to match RenderCLI() format:\nExpected:\n%s\nGot:\n%s", expected, output)
		}
	})

	t.Run("Web fetch minimal (no save or prompt)", func(t *testing.T) {
		content := "<!DOCTYPE html><html>...</html>"
		result := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.WebFetchMetadata{
				URL:           "https://example.com",
				ProcessedType: "markdown",
				Size:          int64(len(content)),
				Content:       content,
			},
		}

		output := renderer.RenderCLI(result)
		expected := fmt.Sprintf("Web Fetch: %s\n%s", "https://example.com", content)

		if output != expected {
			t.Errorf("Expected output to match RenderCLI() format:\nExpected:\n%s\nGot:\n%s", expected, output)
		}
	})

	t.Run("Web fetch error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   false,
			Error:     "Failed to fetch URL: connection timeout",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "Failed to fetch URL: connection timeout" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})
}
