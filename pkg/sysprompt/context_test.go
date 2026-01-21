package sysprompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFormatContexts tests the context formatting logic
func TestFormatContexts(t *testing.T) {
	t.Run("Format with contexts", func(t *testing.T) {
		ctx := &PromptContext{
			ContextFiles: map[string]string{
				"AGENTS.md": "# Agents Context",
				"README.md": "# README Content",
			},
		}

		result := ctx.FormatContexts()

		assert.Contains(t, result, "Here are some useful context")
		assert.Contains(t, result, `<context filename="AGENTS.md", dir=".">`)
		assert.Contains(t, result, "# Agents Context")
		assert.Contains(t, result, `<context filename="README.md", dir=".">`)
		assert.Contains(t, result, "# README Content")
	})

	t.Run("Format with no contexts", func(t *testing.T) {
		ctx := &PromptContext{
			ContextFiles: map[string]string{},
		}

		result := ctx.FormatContexts()

		assert.Empty(t, result, "Expected empty string when no contexts are available")
	})
}

// TestPromptContextActiveContextFile tests the ActiveContextFile field
func TestPromptContextActiveContextFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sysprompt-active-context-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	t.Run("ActiveContextFile is AGENTS.md when AGENTS.md exists", func(t *testing.T) {
		err := os.WriteFile(AgentsMd, []byte("# AGENTS Context"), 0o644)
		require.NoError(t, err)
		defer os.Remove(AgentsMd)

		ctx := NewPromptContext(nil)
		assert.Equal(t, AgentsMd, ctx.ActiveContextFile, "Expected ActiveContextFile to be AGENTS.md")
	})

	t.Run("ActiveContextFile defaults to AGENTS.md when no file exists", func(t *testing.T) {
		os.Remove(AgentsMd)

		ctx := NewPromptContext(nil)
		assert.Equal(t, AgentsMd, ctx.ActiveContextFile, "Expected ActiveContextFile to default to AGENTS.md")
	})
}

func TestResolveActiveContextFile(t *testing.T) {
	t.Run("prefers working directory match", func(t *testing.T) {
		workingDir := t.TempDir()
		contexts := map[string]string{
			filepath.Join(workingDir, "README.md"): "# README",
		}
		patterns := []string{"AGENTS.md", "README.md"}

		active := ResolveActiveContextFile(workingDir, contexts, patterns)

		assert.Equal(t, "README.md", active)
	})

	t.Run("falls back to loaded context base name", func(t *testing.T) {
		contexts := map[string]string{
			"/var/tmp/CODING.md": "# Coding",
		}
		patterns := []string{"CODING.md", "README.md"}

		active := ResolveActiveContextFile("", contexts, patterns)

		assert.Equal(t, "CODING.md", active)
	})

	t.Run("falls back to first pattern when no contexts", func(t *testing.T) {
		patterns := []string{"README.md", "AGENTS.md"}

		active := ResolveActiveContextFile("", nil, patterns)

		assert.Equal(t, "README.md", active)
	})

	t.Run("defaults to AGENTS.md when no patterns", func(t *testing.T) {
		active := ResolveActiveContextFile("", nil, nil)

		assert.Equal(t, AgentsMd, active)
	})
}
