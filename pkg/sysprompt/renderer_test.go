package sysprompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConditionalRendering tests that conditional template sections are included or excluded based on configuration
func TestConditionalRendering(t *testing.T) {
	// Create a test renderer with embedded templates
	renderer := NewRenderer(TemplateFS)

	// Test with specific features enabled
	t.Run("With all features enabled", func(t *testing.T) {
		ctx := NewPromptContext()
		ctx.Features["subagentEnabled"] = true

		prompt, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err, "Failed to render system prompt")

		assert.True(t, strings.Contains(prompt, "subagent"), "Expected subagent reference in prompt when subagentEnabled is true")
	})

	t.Run("With some features disabled", func(t *testing.T) {
		ctx := NewPromptContext()

		// Disable grep tool but keep subagent
		ctx.Features["subagentEnabled"] = true

		// Generate prompt with modified features
		config := NewDefaultConfig()
		updateContextWithConfig(ctx, config)

		_, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err, "Failed to render system prompt")
	})
}

// TestRenderer tests the core functionality of the template renderer
func TestRenderer(t *testing.T) {
	renderer := NewRenderer(TemplateFS)
	ctx := NewPromptContext()

	// Test that we can render individual component templates
	t.Run("Component template rendering", func(t *testing.T) {
		components := []string{
			"templates/components/tone.tmpl",
			"templates/components/tools.tmpl",
			"templates/components/task_management.tmpl",
			"templates/components/context.tmpl",
		}

		for _, component := range components {
			result, err := renderer.RenderPrompt(component, ctx)
			assert.NoError(t, err, "Failed to render component %s", component)
			
			assert.NotEqual(t, 0, len(result), "Rendered component %s has zero length", component)
		}
	})

	// Test that template caching works
	t.Run("Template caching", func(t *testing.T) {
		// First render should parse and cache the template
		_, err := renderer.RenderPrompt(SystemTemplate, ctx)
		require.NoError(t, err, "Failed to render system template")

		// Check that we have a cache entry for this template
		assert.NotEqual(t, 0, len(renderer.cache), "Template was not cached after rendering")

		// Make sure the system template is in the cache
		_, ok := renderer.cache[SystemTemplate]
		assert.True(t, ok, "Template %s not found in cache", SystemTemplate)
	})

	// Test that include function works
	t.Run("Include function", func(t *testing.T) {
		// Create a simple test template that includes another template
		// We can't directly test this without modifying the renderer, but we can check
		// that templates with includes render without errors
		_, err := renderer.RenderSystemPrompt(ctx)
		assert.NoError(t, err, "Failed to render template with includes")
	})
}
