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
		ctx := NewPromptContext(nil)
		ctx.SubagentEnabled = true

		prompt, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err, "Failed to render system prompt")

		assert.True(t, strings.Contains(prompt, "subagent"), "Expected subagent reference in prompt when subagentEnabled is true")
	})

	t.Run("With some features disabled", func(t *testing.T) {
		ctx := NewPromptContext(nil)
		ctx.SubagentEnabled = false
		ctx.TodoToolsEnabled = false

		_, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err, "Failed to render system prompt")
	})
}

// TestRenderer tests the core functionality of the template renderer
func TestRenderer(t *testing.T) {
	renderer := NewRenderer(TemplateFS)
	ctx := NewPromptContext(nil)

	t.Run("Component template rendering", func(t *testing.T) {
		components := []string{
			"templates/sections/behavior.tmpl",
			"templates/sections/tooling.tmpl",
			"templates/sections/task_management_examples.tmpl",
			"templates/sections/context_runtime.tmpl",
		}

		for _, component := range components {
			result, err := renderer.RenderPrompt(component, ctx)
			assert.NoError(t, err, "Failed to render component %s", component)

			assert.NotEqual(t, 0, len(result), "Rendered component %s has zero length", component)
		}
	})

	t.Run("Template caching", func(t *testing.T) {
		require.NoError(t, renderer.parseErr, "Failed to pre-parse templates")
		require.NotNil(t, renderer.templates, "Expected pre-parsed templates to be initialized")
		assert.NotNil(t, renderer.templates.Lookup(SystemTemplate), "Expected system template to be pre-parsed")
	})

	t.Run("Include function", func(t *testing.T) {
		// We can't directly test include functionality without modifying the renderer,
		// but we can verify that templates with includes render without errors
		_, err := renderer.RenderSystemPrompt(ctx)
		assert.NoError(t, err, "Failed to render template with includes")
	})
}
