package renderers

import (
	"fmt"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
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

		assert.Equal(t, expected, output, "Expected output to match RenderCLI() format")
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

		assert.Equal(t, expected, output, "Expected output to match RenderCLI() format")
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

		assert.Equal(t, expected, output, "Expected output to match RenderCLI() format")
	})

	t.Run("Web fetch error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   false,
			Error:     "Failed to fetch URL: connection timeout",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Failed to fetch URL: connection timeout", output, "Expected error message")
	})
}

func TestOpenAIWebSearchRenderer(t *testing.T) {
	renderer := &OpenAIWebSearchRenderer{}

	t.Run("success with all optional fields", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "openai_web_search",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.OpenAIWebSearchMetadata{
				Status:  "completed",
				Action:  "search",
				Queries: []string{"kodelet coverage", "go tests"},
				URL:     "https://example.com/page",
				Pattern: "coverage",
				Sources: []string{"https://example.com/source"},
				Results: []string{"https://example.com/result"},
			},
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "OpenAI Web Search (completed)")
		assert.Contains(t, output, "Action: search")
		assert.Contains(t, output, "Queries: kodelet coverage, go tests")
		assert.Contains(t, output, "URL: https://example.com/page")
		assert.Contains(t, output, "Pattern: coverage")
		assert.Contains(t, output, "Sources:\n- https://example.com/source")
		assert.Contains(t, output, "Results:\n- https://example.com/result")
	})

	t.Run("error and invalid metadata", func(t *testing.T) {
		assert.Equal(t, "network failed", renderer.RenderCLI(tools.StructuredToolResult{
			ToolName: "openai_web_search",
			Success:  false,
			Error:    "network failed",
		}))

		assert.Equal(t, "Error: Invalid metadata type for openai_web_search", renderer.RenderCLI(tools.StructuredToolResult{
			ToolName: "openai_web_search",
			Success:  true,
			Metadata: &tools.WebFetchMetadata{},
		}))
	})
}

func TestViewImageRenderer(t *testing.T) {
	renderer := &ViewImageRenderer{}

	output := renderer.RenderCLI(tools.StructuredToolResult{
		ToolName: "view_image",
		Success:  true,
		Metadata: &tools.ViewImageMetadata{
			Path:      "/tmp/image.png",
			MimeType:  "image/png",
			Detail:    "original",
			ImageSize: tools.ImageDimensions{Width: 640, Height: 480},
		},
	})
	assert.Equal(t, "Image: /tmp/image.png\nType: image/png\nDimensions: 640x480\nDetail: original", output)

	assert.Equal(t, "Image: /tmp/image.png", renderer.RenderCLI(tools.StructuredToolResult{
		ToolName: "view_image",
		Success:  true,
		Metadata: &tools.ViewImageMetadata{Path: "/tmp/image.png"},
	}))
	assert.Equal(t, "Error: not an image", renderer.RenderCLI(tools.StructuredToolResult{ToolName: "view_image", Success: false, Error: "not an image"}))
	assert.Equal(t, "Error: Invalid metadata type for view_image", renderer.RenderCLI(tools.StructuredToolResult{ToolName: "view_image", Success: true, Metadata: &tools.BashMetadata{}}))
}
