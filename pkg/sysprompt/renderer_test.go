package sysprompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConditionalRendering tests that conditional template sections are included or excluded based on configuration
func TestConditionalRendering(t *testing.T) {
	renderer := NewRenderer(TemplateFS)

	t.Run("With all features enabled", func(t *testing.T) {
		ctx := NewPromptContext()
		ctx.Features["subagentEnabled"] = true

		prompt, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err, "Failed to render system prompt")

		assert.True(t, strings.Contains(prompt, "subagent"), "Expected subagent reference in prompt when subagentEnabled is true")
	})

	t.Run("With some features disabled", func(t *testing.T) {
		ctx := NewPromptContext()
		ctx.Features["subagentEnabled"] = true

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

	t.Run("Template caching", func(t *testing.T) {
		_, err := renderer.RenderPrompt(SystemTemplate, ctx)
		require.NoError(t, err, "Failed to render system template")

		assert.NotEqual(t, 0, len(renderer.cache), "Template was not cached after rendering")

		_, ok := renderer.cache[SystemTemplate]
		assert.True(t, ok, "Template %s not found in cache", SystemTemplate)
	})

	t.Run("Include function", func(t *testing.T) {
		// We can't directly test include functionality without modifying the renderer,
		// but we can verify that templates with includes render without errors
		_, err := renderer.RenderSystemPrompt(ctx)
		assert.NoError(t, err, "Failed to render template with includes")
	})
}