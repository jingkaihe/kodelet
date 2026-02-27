package sysprompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCustomTemplatePath(t *testing.T) {
	t.Run("resolves relative path to absolute", func(t *testing.T) {
		path, err := resolveCustomTemplatePath("./test.tmpl")
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(path))
		assert.True(t, strings.HasSuffix(path, filepath.Clean("test.tmpl")))
	})

	t.Run("expands home path", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)

		path, err := resolveCustomTemplatePath("~/sysprompt.tmpl")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, "sysprompt.tmpl"), path)
	})
}

func TestLoadCustomTemplateContent(t *testing.T) {
	t.Run("loads valid template", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "custom.tmpl")
		require.NoError(t, os.WriteFile(tmplPath, []byte("Hello {{.WorkingDirectory}}"), 0o644))

		content, err := loadCustomTemplateContent(tmplPath)
		require.NoError(t, err)
		assert.Equal(t, "Hello {{.WorkingDirectory}}", content)
	})
}

func TestRendererForConfig_CustomTemplate(t *testing.T) {
	t.Run("renders with custom template override", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "custom.tmpl")
		require.NoError(t, os.WriteFile(tmplPath, []byte("CUSTOM\n{{include \"templates/sections/tooling.tmpl\" .}}"), 0o644))

		renderer, err := rendererForConfig(llmtypes.Config{Sysprompt: tmplPath})
		require.NoError(t, err)

		ctx := newPromptContext(nil)
		ctx.SubagentEnabled = true
		prompt, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err)
		assert.Contains(t, prompt, "CUSTOM")
		assert.Contains(t, prompt, "# Tool Usage")
	})

	t.Run("falls back to default renderer when invalid", func(t *testing.T) {
		renderer, err := rendererForConfig(llmtypes.Config{Sysprompt: "/no/such/file.tmpl"})
		require.Error(t, err)
		require.NotNil(t, renderer)

		ctx := newPromptContext(nil)
		prompt, renderErr := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, renderErr)
		assert.Contains(t, prompt, "You are an interactive CLI tool")
	})

	t.Run("renders with custom args", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "custom-args.tmpl")
		require.NoError(t, os.WriteFile(tmplPath, []byte("Project={{default .Args.project \"unknown\"}}\nOwner={{default .Args.owner \"none\"}}"), 0o644))

		renderer, err := rendererForConfig(llmtypes.Config{Sysprompt: tmplPath})
		require.NoError(t, err)

		ctx := newPromptContext(nil)
		ctx.Args = map[string]string{"project": "kodelet"}
		prompt, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err)
		assert.Contains(t, prompt, "Project=kodelet")
		assert.Contains(t, prompt, "Owner=none")
	})

	t.Run("renders with bash function", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "custom-bash.tmpl")
		require.NoError(t, os.WriteFile(tmplPath, []byte("Echo={{bash \"echo\" \"hello\"}}"), 0o644))

		renderer, err := rendererForConfig(llmtypes.Config{Sysprompt: tmplPath})
		require.NoError(t, err)

		ctx := newPromptContext(nil)
		prompt, err := renderer.RenderSystemPrompt(ctx)
		require.NoError(t, err)
		assert.Contains(t, prompt, "Echo=hello")
	})
}
