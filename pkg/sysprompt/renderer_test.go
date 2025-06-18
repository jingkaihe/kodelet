package sysprompt

import (
	"strings"
	"testing"
)

// TestConditionalRendering tests that conditional template sections are included or excluded based on configuration
func TestConditionalRendering(t *testing.T) {
	// Create a test renderer with embedded templates
	renderer := NewRenderer(TemplateFS)

	// Test with specific features enabled
	t.Run("With all features enabled", func(t *testing.T) {
		ctx := NewPromptContext()
		ctx.Features["grepToolEnabled"] = true
		ctx.Features["subagentEnabled"] = true

		prompt, err := renderer.RenderSystemPrompt(ctx)
		if err != nil {
			t.Fatalf("Failed to render system prompt: %v", err)
		}

		// Check that both grep and subagent tool references are included
		if !strings.Contains(prompt, "grep_tool") {
			t.Error("Expected grep_tool reference in prompt when grepToolEnabled is true")
		}

		if !strings.Contains(prompt, "subagent") {
			t.Error("Expected subagent reference in prompt when subagentEnabled is true")
		}
	})

	t.Run("With some features disabled", func(t *testing.T) {
		ctx := NewPromptContext()

		// Disable grep tool but keep subagent
		ctx.Features["grepToolEnabled"] = false
		ctx.Features["subagentEnabled"] = true

		// Generate prompt with modified features
		config := NewDefaultConfig()
		updateContextWithConfig(ctx, config)

		prompt, err := renderer.RenderSystemPrompt(ctx)
		if err != nil {
			t.Fatalf("Failed to render system prompt: %v", err)
		}

		// In our implementation, these checks might need adjustment based on how
		// conditional sections are implemented. This is just an example approach.
		grepMentionCount := strings.Count(strings.ToLower(prompt), "grep_tool")
		subagentMentionCount := strings.Count(strings.ToLower(prompt), "subagent")

		// The exact counts will depend on template implementation, but subagent should appear
		// more often than grep when grep is disabled
		if grepMentionCount >= subagentMentionCount && grepMentionCount > 0 {
			t.Errorf("Expected fewer grep_tool mentions (%d) than subagent mentions (%d) when grepToolEnabled=false",
				grepMentionCount, subagentMentionCount)
		}
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
			if err != nil {
				t.Errorf("Failed to render component %s: %v", component, err)
				continue
			}

			if len(result) == 0 {
				t.Errorf("Rendered component %s has zero length", component)
			}
		}
	})

	// Test that template caching works
	t.Run("Template caching", func(t *testing.T) {
		// First render should parse and cache the template
		_, err := renderer.RenderPrompt(SystemTemplate, ctx)
		if err != nil {
			t.Fatalf("Failed to render system template: %v", err)
		}

		// Check that we have a cache entry for this template
		if len(renderer.cache) == 0 {
			t.Error("Template was not cached after rendering")
		}

		// Make sure the system template is in the cache
		if _, ok := renderer.cache[SystemTemplate]; !ok {
			t.Errorf("Template %s not found in cache", SystemTemplate)
		}
	})

	// Test that include function works
	t.Run("Include function", func(t *testing.T) {
		// Create a simple test template that includes another template
		// We can't directly test this without modifying the renderer, but we can check
		// that templates with includes render without errors
		_, err := renderer.RenderSystemPrompt(ctx)
		if err != nil {
			t.Errorf("Failed to render template with includes: %v", err)
		}
	})
}
