package sysprompt

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetContextFileName tests the context file name resolution logic
func TestGetContextFileName(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "sysprompt-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Save the current working directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	// Change to the temporary directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	t.Run("No context files", func(t *testing.T) {
		// Ensure no context files exist
		os.Remove(AgentsMd)
		os.Remove(KodeletMd)

		result := getContextFileName()
		assert.Equal(t, AgentsMd, result, "Expected AGENTS.md when no context files exist (default)")
	})

	t.Run("Only KODELET.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(AgentsMd)

		// Create KODELET.md
		err := os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)

		result := getContextFileName()
		assert.Equal(t, KodeletMd, result, "Expected KODELET.md when only KODELET.md exists")
	})

	t.Run("Only AGENTS.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(KodeletMd)

		// Create AGENTS.md
		err := os.WriteFile(AgentsMd, []byte("# AGENTS Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentsMd)

		result := getContextFileName()
		assert.Equal(t, AgentsMd, result, "Expected AGENTS.md when only AGENTS.md exists")
	})

	t.Run("Both AGENTS.md and KODELET.md exist", func(t *testing.T) {
		// Create both files
		err := os.WriteFile(AgentsMd, []byte("# AGENTS Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentsMd)

		err = os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)

		result := getContextFileName()
		assert.Equal(t, AgentsMd, result, "Expected AGENTS.md to take precedence when both files exist")
	})
}

// TestLoadContexts tests the context loading logic
func TestLoadContexts(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "sysprompt-context-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Save the current working directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	// Change to the temporary directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	t.Run("Load AGENTS.md when present", func(t *testing.T) {
		// Create AGENTS.md and README.md
		agentsContent := "# AGENTS Context\nThis is the agents context."
		readmeContent := "# README\nThis is the readme."

		err := os.WriteFile(AgentsMd, []byte(agentsContent), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentsMd)

		err = os.WriteFile(ReadmeMd, []byte(readmeContent), 0644)
		require.NoError(t, err)
		defer os.Remove(ReadmeMd)

		contexts := loadContexts()

		assert.Contains(t, contexts, AgentsMd, "Expected AGENTS.md to be loaded")
		assert.Equal(t, agentsContent, contexts[AgentsMd], "Expected correct AGENTS.md content")
		assert.Contains(t, contexts, ReadmeMd, "Expected README.md to be loaded")
		assert.Equal(t, readmeContent, contexts[ReadmeMd], "Expected correct README.md content")
		assert.NotContains(t, contexts, KodeletMd, "Expected KODELET.md not to be loaded when AGENTS.md exists")
	})

	t.Run("Fall back to KODELET.md when AGENTS.md not present", func(t *testing.T) {
		// Clean up AGENTS.md if it exists
		os.Remove(AgentsMd)

		// Create KODELET.md and README.md
		kodeletContent := "# KODELET Context\nThis is the kodelet context."
		readmeContent := "# README\nThis is the readme."

		err := os.WriteFile(KodeletMd, []byte(kodeletContent), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)

		err = os.WriteFile(ReadmeMd, []byte(readmeContent), 0644)
		require.NoError(t, err)
		defer os.Remove(ReadmeMd)

		contexts := loadContexts()

		assert.Contains(t, contexts, KodeletMd, "Expected KODELET.md to be loaded as fallback")
		assert.Equal(t, kodeletContent, contexts[KodeletMd], "Expected correct KODELET.md content")
		assert.Contains(t, contexts, ReadmeMd, "Expected README.md to be loaded")
		assert.Equal(t, readmeContent, contexts[ReadmeMd], "Expected correct README.md content")
		assert.NotContains(t, contexts, AgentsMd, "Expected AGENTS.md not to be loaded when it doesn't exist")
	})

	t.Run("Handle missing context files gracefully", func(t *testing.T) {
		// Clean up all files
		os.Remove(AgentsMd)
		os.Remove(KodeletMd)
		os.Remove(ReadmeMd)

		contexts := loadContexts()

		assert.Empty(t, contexts, "Expected empty map when no context files exist")
	})
}

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

		assert.Contains(t, result, "Here are some useful context", "Expected context header")
		assert.Contains(t, result, `<context filename="AGENTS.md">`, "Expected AGENTS.md context tag")
		assert.Contains(t, result, "# Agents Context", "Expected AGENTS.md content")
		assert.Contains(t, result, `<context filename="README.md">`, "Expected README.md context tag")
		assert.Contains(t, result, "# README Content", "Expected README.md content")
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
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "sysprompt-active-context-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Save the current working directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	// Change to the temporary directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	t.Run("ActiveContextFile is AGENTS.md when AGENTS.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(KodeletMd)

		// Create AGENTS.md
		err := os.WriteFile(AgentsMd, []byte("# AGENTS Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentsMd)

		ctx := NewPromptContext()
		assert.Equal(t, AgentsMd, ctx.ActiveContextFile, "Expected ActiveContextFile to be AGENTS.md")
	})

	t.Run("ActiveContextFile is KODELET.md when only KODELET.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(AgentsMd)

		// Create KODELET.md
		err := os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)

		ctx := NewPromptContext()
		assert.Equal(t, KodeletMd, ctx.ActiveContextFile, "Expected ActiveContextFile to be KODELET.md")
	})

	t.Run("ActiveContextFile defaults to AGENTS.md when neither file exists", func(t *testing.T) {
		// Clean up both files
		os.Remove(AgentsMd)
		os.Remove(KodeletMd)

		ctx := NewPromptContext()
		assert.Equal(t, AgentsMd, ctx.ActiveContextFile, "Expected ActiveContextFile to default to AGENTS.md")
	})

	t.Run("ActiveContextFile prefers AGENTS.md when both files exist", func(t *testing.T) {
		// Create both files
		err := os.WriteFile(AgentsMd, []byte("# AGENTS Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentsMd)

		err = os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)

		ctx := NewPromptContext()
		assert.Equal(t, AgentsMd, ctx.ActiveContextFile, "Expected ActiveContextFile to prefer AGENTS.md")
	})
}
