package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewBasicState_ErrorHandling tests that NewBasicState handles directory access errors gracefully
func TestNewBasicState_ErrorHandling(t *testing.T) {
	ctx := context.Background()

	t.Run("normal_case", func(t *testing.T) {
		// Normal case should work fine
		state := NewBasicState(ctx)
		
		// Should have valid working directory (current dir or ".")
		assert.NotEmpty(t, state.contextDiscovery.workingDir)
		
		// Should have attempted to set up home directory (could be empty if UserHomeDir fails)
		// This test verifies the state is created successfully even if dirs are inaccessible
		assert.NotNil(t, state.contextDiscovery)
	})

	t.Run("working_dir_fallback", func(t *testing.T) {
		// Create temporary directory and then remove it to simulate os.Getwd() failure
		tmpDir := t.TempDir()
		
		// Change to the temp directory, then remove it while we're in it
		oldWd, err := os.Getwd()
		require.NoError(t, err)
		defer os.Chdir(oldWd)
		
		require.NoError(t, os.Chdir(tmpDir))
		require.NoError(t, os.Remove(tmpDir))
		
		// Now os.Getwd() should fail since the directory no longer exists
		state := NewBasicState(ctx)
		
		// Should have fallen back to "." for working directory
		assert.Equal(t, ".", state.contextDiscovery.workingDir)
		
		// State should still be functional
		assert.NotNil(t, state.contextCache)
		assert.NotEmpty(t, state.sessionID)
	})

	t.Run("home_context_disabled_on_error", func(t *testing.T) {
		// This test verifies that when home directory cannot be determined,
		// home context discovery is properly disabled
		state := NewBasicState(ctx)
		
		// Even if homeDir setup fails, the state should be created
		assert.NotNil(t, state.contextDiscovery)
		
		// Test home context loading with empty homeDir
		state.contextDiscovery.homeDir = "" // Simulate failed os.UserHomeDir()
		
		homeContext := state.loadHomeContext()
		assert.Nil(t, homeContext, "Home context should be nil when homeDir is empty")
	})

	t.Run("context_discovery_works_with_fallbacks", func(t *testing.T) {
		tmpDir := t.TempDir()
		contextFile := filepath.Join(tmpDir, "AGENTS.md")
		require.NoError(t, os.WriteFile(contextFile, []byte("# Test context"), 0644))

		// Change to temp directory
		oldWd, err := os.Getwd()
		require.NoError(t, err)
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		state := NewBasicState(ctx)
		contexts := state.DiscoverContexts()

		// Should still discover working directory context even with error handling
		assert.Len(t, contexts, 1)
		assert.Contains(t, contexts, contextFile)
		assert.Equal(t, "# Test context", contexts[contextFile])
	})
}