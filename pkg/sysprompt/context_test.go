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
		os.Remove(AgentMd)
		os.Remove(KodeletMd)
		
		result := getContextFileName()
		assert.Equal(t, AgentMd, result, "Expected AGENT.md when no context files exist (default)")
	})

	t.Run("Only KODELET.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(AgentMd)
		
		// Create KODELET.md
		err := os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)
		
		result := getContextFileName()
		assert.Equal(t, KodeletMd, result, "Expected KODELET.md when only KODELET.md exists")
	})

	t.Run("Only AGENT.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(KodeletMd)
		
		// Create AGENT.md
		err := os.WriteFile(AgentMd, []byte("# AGENT Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentMd)
		
		result := getContextFileName()
		assert.Equal(t, AgentMd, result, "Expected AGENT.md when only AGENT.md exists")
	})

	t.Run("Both AGENT.md and KODELET.md exist", func(t *testing.T) {
		// Create both files
		err := os.WriteFile(AgentMd, []byte("# AGENT Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentMd)
		
		err = os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)
		
		result := getContextFileName()
		assert.Equal(t, AgentMd, result, "Expected AGENT.md to take precedence when both files exist")
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

	t.Run("Load AGENT.md when present", func(t *testing.T) {
		// Create AGENT.md and README.md
		agentContent := "# AGENT Context\nThis is the agent context."
		readmeContent := "# README\nThis is the readme."
		
		err := os.WriteFile(AgentMd, []byte(agentContent), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentMd)
		
		err = os.WriteFile(ReadmeMd, []byte(readmeContent), 0644)
		require.NoError(t, err)
		defer os.Remove(ReadmeMd)
		
		contexts := loadContexts()
		
		assert.Contains(t, contexts, AgentMd, "Expected AGENT.md to be loaded")
		assert.Equal(t, agentContent, contexts[AgentMd], "Expected correct AGENT.md content")
		assert.Contains(t, contexts, ReadmeMd, "Expected README.md to be loaded")
		assert.Equal(t, readmeContent, contexts[ReadmeMd], "Expected correct README.md content")
		assert.NotContains(t, contexts, KodeletMd, "Expected KODELET.md not to be loaded when AGENT.md exists")
	})

	t.Run("Fall back to KODELET.md when AGENT.md not present", func(t *testing.T) {
		// Clean up AGENT.md if it exists
		os.Remove(AgentMd)
		
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
		assert.NotContains(t, contexts, AgentMd, "Expected AGENT.md not to be loaded when it doesn't exist")
	})

	t.Run("Handle missing context files gracefully", func(t *testing.T) {
		// Clean up all files
		os.Remove(AgentMd)
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
				"AGENT.md": "# Agent Context",
				"README.md": "# README Content",
			},
		}
		
		result := ctx.FormatContexts()
		
		assert.Contains(t, result, "Here are some useful context", "Expected context header")
		assert.Contains(t, result, `<context filename="AGENT.md">`, "Expected AGENT.md context tag")
		assert.Contains(t, result, "# Agent Context", "Expected AGENT.md content")
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

	t.Run("ActiveContextFile is AGENT.md when AGENT.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(KodeletMd)
		
		// Create AGENT.md
		err := os.WriteFile(AgentMd, []byte("# AGENT Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentMd)
		
		ctx := NewPromptContext()
		assert.Equal(t, AgentMd, ctx.ActiveContextFile, "Expected ActiveContextFile to be AGENT.md")
	})

	t.Run("ActiveContextFile is KODELET.md when only KODELET.md exists", func(t *testing.T) {
		// Clean up any existing files
		os.Remove(AgentMd)
		
		// Create KODELET.md
		err := os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)
		
		ctx := NewPromptContext()
		assert.Equal(t, KodeletMd, ctx.ActiveContextFile, "Expected ActiveContextFile to be KODELET.md")
	})

	t.Run("ActiveContextFile defaults to AGENT.md when neither file exists", func(t *testing.T) {
		// Clean up both files
		os.Remove(AgentMd)
		os.Remove(KodeletMd)
		
		ctx := NewPromptContext()
		assert.Equal(t, AgentMd, ctx.ActiveContextFile, "Expected ActiveContextFile to default to AGENT.md")
	})

	t.Run("ActiveContextFile prefers AGENT.md when both files exist", func(t *testing.T) {
		// Create both files
		err := os.WriteFile(AgentMd, []byte("# AGENT Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(AgentMd)
		
		err = os.WriteFile(KodeletMd, []byte("# KODELET Context"), 0644)
		require.NoError(t, err)
		defer os.Remove(KodeletMd)
		
		ctx := NewPromptContext()
		assert.Equal(t, AgentMd, ctx.ActiveContextFile, "Expected ActiveContextFile to prefer AGENT.md")
	})
}