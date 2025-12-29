package sysprompt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomPromptRenderer_RenderCustomPrompt(t *testing.T) {
	ctx := context.Background()

	t.Run("render template file with built-in variables", func(t *testing.T) {
		tmpDir := t.TempDir()
		templatePath := filepath.Join(tmpDir, "test-prompt.md")
		err := os.WriteFile(templatePath, []byte(`# Custom Prompt
Date: {{.Date}}
Directory: {{.WorkingDirectory}}
Platform: {{.Platform}}
`), 0o644)
		require.NoError(t, err)

		renderer := NewCustomPromptRenderer([]string{tmpDir})
		promptCtx := NewPromptContext(nil)

		result, err := renderer.RenderCustomPrompt(ctx, templatePath, "", nil, promptCtx)
		require.NoError(t, err)

		assert.Contains(t, result, "# Custom Prompt")
		assert.Contains(t, result, promptCtx.Date)
		assert.Contains(t, result, promptCtx.WorkingDirectory)
		assert.Contains(t, result, promptCtx.Platform)
	})

	t.Run("render template with custom arguments", func(t *testing.T) {
		tmpDir := t.TempDir()
		templatePath := filepath.Join(tmpDir, "test-prompt.md")
		err := os.WriteFile(templatePath, []byte(`# {{.project}} Prompt
Version: {{.version}}
`), 0o644)
		require.NoError(t, err)

		renderer := NewCustomPromptRenderer([]string{tmpDir})
		promptCtx := NewPromptContext(nil)

		args := map[string]string{
			"project": "MyApp",
			"version": "1.0.0",
		}

		result, err := renderer.RenderCustomPrompt(ctx, templatePath, "", args, promptCtx)
		require.NoError(t, err)

		assert.Contains(t, result, "# MyApp Prompt")
		assert.Contains(t, result, "Version: 1.0.0")
	})

	t.Run("render template with default values from frontmatter", func(t *testing.T) {
		tmpDir := t.TempDir()
		templatePath := filepath.Join(tmpDir, "test-prompt.md")
		err := os.WriteFile(templatePath, []byte(`---
name: test-prompt
defaults:
  language: go
  strictness: medium
---
# Code Review Agent

Language: {{.language}}
Strictness: {{default .strictness "low"}}
`), 0o644)
		require.NoError(t, err)

		renderer := NewCustomPromptRenderer([]string{tmpDir})
		promptCtx := NewPromptContext(nil)

		result, err := renderer.RenderCustomPrompt(ctx, templatePath, "", nil, promptCtx)
		require.NoError(t, err)

		assert.Contains(t, result, "# Code Review Agent")
		assert.Contains(t, result, "Language: go")
		assert.Contains(t, result, "Strictness: medium")
	})

	t.Run("custom args override frontmatter defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		templatePath := filepath.Join(tmpDir, "test-prompt.md")
		err := os.WriteFile(templatePath, []byte(`---
defaults:
  language: go
---
Language: {{.language}}
`), 0o644)
		require.NoError(t, err)

		renderer := NewCustomPromptRenderer([]string{tmpDir})
		promptCtx := NewPromptContext(nil)

		args := map[string]string{
			"language": "python",
		}

		result, err := renderer.RenderCustomPrompt(ctx, templatePath, "", args, promptCtx)
		require.NoError(t, err)

		assert.Contains(t, result, "Language: python")
	})

	t.Run("render template with bash function", func(t *testing.T) {
		tmpDir := t.TempDir()
		templatePath := filepath.Join(tmpDir, "test-prompt.md")
		err := os.WriteFile(templatePath, []byte(`Current user: {{bash "whoami"}}
`), 0o644)
		require.NoError(t, err)

		renderer := NewCustomPromptRenderer([]string{tmpDir})
		promptCtx := NewPromptContext(nil)

		result, err := renderer.RenderCustomPrompt(ctx, templatePath, "", nil, promptCtx)
		require.NoError(t, err)

		// whoami should return the current user
		assert.NotContains(t, result, "{{bash")
		assert.NotContains(t, result, "}}")
	})

	t.Run("render template with env function", func(t *testing.T) {
		tmpDir := t.TempDir()
		templatePath := filepath.Join(tmpDir, "test-prompt.md")
		err := os.WriteFile(templatePath, []byte(`Home: {{env "HOME"}}
`), 0o644)
		require.NoError(t, err)

		renderer := NewCustomPromptRenderer([]string{tmpDir})
		promptCtx := NewPromptContext(nil)

		result, err := renderer.RenderCustomPrompt(ctx, templatePath, "", nil, promptCtx)
		require.NoError(t, err)

		home := os.Getenv("HOME")
		if home != "" {
			assert.Contains(t, result, home)
		}
	})

	t.Run("error on missing template file", func(t *testing.T) {
		renderer := NewCustomPromptRenderer([]string{})
		promptCtx := NewPromptContext(nil)

		_, err := renderer.RenderCustomPrompt(ctx, "/nonexistent/path.md", "", nil, promptCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load custom system prompt template")
	})

	t.Run("error when neither template path nor recipe name is provided", func(t *testing.T) {
		renderer := NewCustomPromptRenderer([]string{})
		promptCtx := NewPromptContext(nil)

		_, err := renderer.RenderCustomPrompt(ctx, "", "", nil, promptCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "either template path or recipe name must be specified")
	})
}

func TestCustomPromptRenderer_LoadFromRecipe(t *testing.T) {
	ctx := context.Background()

	t.Run("load recipe by name", func(t *testing.T) {
		tmpDir := t.TempDir()
		recipePath := filepath.Join(tmpDir, "code-review.md")
		err := os.WriteFile(recipePath, []byte(`---
name: code-review
---
# Code Review Prompt
Review the code carefully.
`), 0o644)
		require.NoError(t, err)

		renderer := NewCustomPromptRenderer([]string{tmpDir})
		promptCtx := NewPromptContext(nil)

		result, err := renderer.RenderCustomPrompt(ctx, "", "code-review", nil, promptCtx)
		require.NoError(t, err)

		assert.Contains(t, result, "# Code Review Prompt")
		assert.Contains(t, result, "Review the code carefully.")
	})

	t.Run("error on missing recipe", func(t *testing.T) {
		renderer := NewCustomPromptRenderer([]string{})
		promptCtx := NewPromptContext(nil)

		_, err := renderer.RenderCustomPrompt(ctx, "", "nonexistent-recipe", nil, promptCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load recipe as system prompt")
	})
}

func TestExtractBodyContent(t *testing.T) {
	t.Run("content without frontmatter", func(t *testing.T) {
		content := "# Simple Content\nNo frontmatter here."
		result := extractBodyContent(content)
		assert.Equal(t, content, result)
	})

	t.Run("content with frontmatter", func(t *testing.T) {
		content := `---
name: test
description: A test
---
# Actual Content
This is the body.`

		result := extractBodyContent(content)
		assert.Equal(t, "# Actual Content\nThis is the body.", result)
	})

	t.Run("content with unclosed frontmatter", func(t *testing.T) {
		content := `---
name: test
# This is not properly closed

Body content`

		result := extractBodyContent(content)
		assert.Equal(t, content, result)
	})
}

func TestGetFragmentDirs(t *testing.T) {
	dirs := GetFragmentDirs()
	assert.Contains(t, dirs, "./recipes")
	// Should also contain home directory recipes
	assert.Len(t, dirs, 2)
}
