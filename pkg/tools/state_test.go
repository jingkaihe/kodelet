package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBasicState(t *testing.T) {
	// Create a new BasicState using the constructor
	s := NewBasicState(context.TODO())

	// Test setting and getting a file's last modified time
	path := "test/file.txt"
	now := time.Now()

	err := s.SetFileLastAccessed(path, now)
	assert.NoError(t, err, "SetFileLastAccessed should not return an error")

	lastAccessed, err := s.GetFileLastAccessed(path)
	assert.NoError(t, err, "GetFileLastAccessed should not return an error")
	assert.True(t, lastAccessed.Equal(now), "lastAccessed should equal the time that was set")

	// Test getting a non-existent file
	nonExistentPath := "non/existent/file.txt"
	lastAccessed, err = s.GetFileLastAccessed(nonExistentPath)
	assert.Error(t, err, "Expected error for non-existent file")
	assert.True(t, lastAccessed.IsZero(), "Time for non-existent file should be zero")

	// Test tools
	tools := s.Tools()
	mainTools := GetMainTools(context.Background(), []string{})
	assert.Equal(t, len(mainTools), len(tools), "Should have the correct number of tools")
	for i, tool := range tools {
		assert.Equal(t, mainTools[i].Name(), tool.Name(), "Tool names should match")
	}

	// Create a basic config for sub-agent tools test
	basicConfig := llmtypes.Config{}
	subAgentTools := NewBasicState(context.TODO(), WithSubAgentTools(basicConfig))
	expectedSubAgentTools := GetSubAgentTools(context.Background(), []string{})
	assert.Equal(t, len(expectedSubAgentTools), len(subAgentTools.Tools()), "Should have the correct number of subagent tools")
	for i, tool := range subAgentTools.Tools() {
		assert.Equal(t, expectedSubAgentTools[i].Name(), tool.Name(), "Subagent tool names should match")
	}
}

func TestClearFileLastAccessed(t *testing.T) {
	s := NewBasicState(context.TODO())

	// Set a file's last modified time
	path := "test/file.txt"
	now := time.Now()

	err := s.SetFileLastAccessed(path, now)
	assert.NoError(t, err, "SetFileLastAccessed should not return an error")

	// Verify it was set
	lastAccessed, err := s.GetFileLastAccessed(path)
	assert.NoError(t, err, "GetFileLastAccessed should not return an error")
	assert.True(t, lastAccessed.Equal(now), "lastAccessed should equal the time that was set")

	// Clear the file's last modified time
	err = s.ClearFileLastAccessed(path)
	assert.NoError(t, err, "ClearFileLastAccessed should not return an error")

	// Verify it was cleared - we now expect an error
	lastAccessed, err = s.GetFileLastAccessed(path)
	assert.Error(t, err, "Should get an error after clearing access time")
	assert.Contains(t, err.Error(), "has not been read yet", "Error message should indicate the file hasn't been read")
	assert.True(t, lastAccessed.IsZero(), "Time should be zero after clearing")
}

func TestConcurrentAccess(t *testing.T) {
	s := NewBasicState(context.TODO())

	const numGoroutines = 100
	const operationsPerGoroutine = 10

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(_ int) {
			path := "test/file.txt"
			for j := 0; j < operationsPerGoroutine; j++ {
				now := time.Now()
				_ = s.SetFileLastAccessed(path, now)
				_, _ = s.GetFileLastAccessed(path)
				if j%2 == 0 {
					_ = s.ClearFileLastAccessed(path)
				}
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to finish
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// If we got here without deadlock or panic, the test passes
	assert.True(t, true, "Concurrent access test completed without deadlock or panic")
}

func TestBasicState_MCPTools(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TEST") == "true" {
		t.Skip("Skipping docker test")
	}
	config := goldenMCPConfig
	manager, err := NewMCPManager(config)
	assert.NoError(t, err)

	err = manager.Initialize(context.Background())
	assert.NoError(t, err)

	s := NewBasicState(context.TODO(), WithMCPTools(manager))

	tools := s.MCPTools()
	assert.NotNil(t, tools)
	assert.Equal(t, len(tools), 3)
}

func TestBasicState_LLMConfig(t *testing.T) {
	// Test setting and getting LLM config
	config := llmtypes.Config{
		Provider:        "anthropic",
		Model:           "claude-3-5-sonnet",
		AllowedCommands: []string{"ls *", "pwd", "echo *"},
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config))

	// Test that config is stored correctly
	retrievedConfig := s.GetLLMConfig()
	assert.NotNil(t, retrievedConfig)

	// Cast back to the expected type
	llmConfig, ok := retrievedConfig.(llmtypes.Config)
	assert.True(t, ok, "Config should be of type llmtypes.Config")
	assert.Equal(t, config.Provider, llmConfig.Provider)
	assert.Equal(t, config.Model, llmConfig.Model)
	assert.Equal(t, config.AllowedCommands, llmConfig.AllowedCommands)
}

func TestBasicState_ConfigureBashTool(t *testing.T) {
	// Test that BashTool is properly configured with allowed commands
	allowedCommands := []string{"ls *", "pwd", "echo *", "git status"}
	config := llmtypes.Config{
		AllowedCommands: allowedCommands,
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config))

	// Find the bash tool in the tools list
	tools := s.BasicTools()
	var bashTool *BashTool
	for _, tool := range tools {
		if tool.Name() == "bash" {
			if bt, ok := tool.(*BashTool); ok {
				bashTool = bt
				break
			}
		}
	}

	assert.NotNil(t, bashTool, "BashTool should be found in tools list")
	assert.Equal(t, allowedCommands, bashTool.allowedCommands, "BashTool should have correct allowed commands")
}

func TestBasicState_ConfigureBashTool_WithSubAgentTools(t *testing.T) {
	// Test that BashTool is configured correctly when using sub-agent tools
	allowedCommands := []string{"npm *", "yarn *"}
	config := llmtypes.Config{
		AllowedCommands: allowedCommands,
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config), WithSubAgentTools(config))

	// Find the bash tool in the tools list
	tools := s.BasicTools()
	var bashTool *BashTool
	for _, tool := range tools {
		if tool.Name() == "bash" {
			if bt, ok := tool.(*BashTool); ok {
				bashTool = bt
				break
			}
		}
	}

	assert.NotNil(t, bashTool, "BashTool should be found in sub-agent tools list")
	assert.Equal(t, allowedCommands, bashTool.allowedCommands, "BashTool should have correct allowed commands")
}

func TestBasicState_ConfigureBashTool_EmptyAllowedCommands(t *testing.T) {
	// Test that BashTool works correctly with empty allowed commands (should use banned commands)
	config := llmtypes.Config{
		AllowedCommands: []string{}, // Empty list
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config))

	// Find the bash tool in the tools list
	tools := s.BasicTools()
	var bashTool *BashTool
	for _, tool := range tools {
		if tool.Name() == "bash" {
			if bt, ok := tool.(*BashTool); ok {
				bashTool = bt
				break
			}
		}
	}

	assert.NotNil(t, bashTool, "BashTool should be found in tools list")
	assert.Equal(t, []string{}, bashTool.allowedCommands, "BashTool should have empty allowed commands")
}

// TestBasicState_GetRelevantContexts tests the context discovery functionality
func TestBasicState_GetRelevantContexts(t *testing.T) {
	// Create temporary test directory structure
	tmpDir := t.TempDir()
	
	// Create test directories
	subDir := filepath.Join(tmpDir, "submodule")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	
	deepDir := filepath.Join(subDir, "deep", "nested")
	require.NoError(t, os.MkdirAll(deepDir, 0755))

	// Create context files
	rootAgents := filepath.Join(tmpDir, "AGENTS.md")
	require.NoError(t, os.WriteFile(rootAgents, []byte("# Root project context"), 0644))
	
	subKodelet := filepath.Join(subDir, "KODELET.md")
	require.NoError(t, os.WriteFile(subKodelet, []byte("# Submodule context"), 0644))
	
	// Change to test directory
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpDir))

	// Create state
	ctx := context.Background()
	state := NewBasicState(ctx)

	t.Run("working_directory_context_only", func(t *testing.T) {
		// With no accessed files, should only find working directory context
		contexts := state.GetRelevantContexts()
		
		assert.Len(t, contexts, 1, "Should find exactly 1 context file")
		assert.Contains(t, contexts, rootAgents, "Should contain root AGENTS.md")
		assert.Equal(t, "# Root project context", contexts[rootAgents])
	})

	t.Run("access_based_context_discovery", func(t *testing.T) {
		// Simulate accessing a file in the submodule
		testFile := filepath.Join(subDir, "test.go")
		state.SetFileLastAccessed(testFile, time.Now())
		
		contexts := state.GetRelevantContexts()
		
		assert.Len(t, contexts, 2, "Should find exactly 2 context files")
		assert.Contains(t, contexts, rootAgents, "Should contain root AGENTS.md")
		assert.Contains(t, contexts, subKodelet, "Should contain submodule KODELET.md")
		assert.Equal(t, "# Root project context", contexts[rootAgents])
		assert.Equal(t, "# Submodule context", contexts[subKodelet])
	})

	t.Run("deep_nested_access", func(t *testing.T) {
		// Simulate accessing a file deep in the hierarchy
		deepFile := filepath.Join(deepDir, "nested.go")
		state.SetFileLastAccessed(deepFile, time.Now())
		
		contexts := state.GetRelevantContexts()
		
		// Should still find the submodule context by walking up the tree
		assert.Contains(t, contexts, subKodelet, "Should find submodule KODELET.md by walking up")
		assert.Equal(t, "# Submodule context", contexts[subKodelet])
	})
}

func TestBasicState_ContextFilePreference(t *testing.T) {
	// Test AGENTS.md vs KODELET.md preference
	tmpDir := t.TempDir()
	
	// Create both AGENTS.md and KODELET.md in the same directory
	agentsFile := filepath.Join(tmpDir, "AGENTS.md")
	kodeletFile := filepath.Join(tmpDir, "KODELET.md")
	require.NoError(t, os.WriteFile(agentsFile, []byte("# Agents context"), 0644))
	require.NoError(t, os.WriteFile(kodeletFile, []byte("# Kodelet context"), 0644))
	
	// Change to test directory
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpDir))

	state := NewBasicState(context.Background())
	contexts := state.GetRelevantContexts()
	
	assert.Len(t, contexts, 1, "Should find exactly 1 context file")
	assert.Contains(t, contexts, agentsFile, "Should prefer AGENTS.md over KODELET.md")
	assert.Equal(t, "# Agents context", contexts[agentsFile])
}

func TestBasicState_ContextFileCaching(t *testing.T) {
	tmpDir := t.TempDir()
	contextFile := filepath.Join(tmpDir, "AGENTS.md")
	
	// Create initial context file
	initialContent := "# Initial content"
	require.NoError(t, os.WriteFile(contextFile, []byte(initialContent), 0644))
	
	// Change to test directory
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpDir))

	state := NewBasicState(context.Background())
	
	t.Run("initial_load", func(t *testing.T) {
		contexts := state.GetRelevantContexts()
		assert.Len(t, contexts, 1)
		assert.Equal(t, initialContent, contexts[contextFile])
	})

	t.Run("cached_content", func(t *testing.T) {
		// Should return cached content without reading from disk
		contexts := state.GetRelevantContexts()
		assert.Equal(t, initialContent, contexts[contextFile])
	})

	t.Run("cache_invalidation", func(t *testing.T) {
		// Modify the file
		newContent := "# Updated content"
		time.Sleep(10 * time.Millisecond) // Ensure different modification time
		require.NoError(t, os.WriteFile(contextFile, []byte(newContent), 0644))
		
		// Should detect the change and reload
		contexts := state.GetRelevantContexts()
		assert.Equal(t, newContent, contexts[contextFile], "Should reload modified file")
	})
}

func TestBasicState_HomeDirectoryContext(t *testing.T) {
	// Create temporary home directory
	tmpHome := t.TempDir()
	kodeletDir := filepath.Join(tmpHome, ".kodelet")
	require.NoError(t, os.MkdirAll(kodeletDir, 0755))
	
	homeContext := filepath.Join(kodeletDir, "AGENTS.md")
	require.NoError(t, os.WriteFile(homeContext, []byte("# User home context"), 0644))

	// Create temporary working directory (without context files)
	tmpWork := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpWork))

	// Create state with custom home directory
	ctx := context.Background()
	state := NewBasicState(ctx)
	
	// Manually set the home directory in context discovery
	state.contextDiscovery.homeDir = kodeletDir

	t.Run("home_context_discovery", func(t *testing.T) {
		contexts := state.GetRelevantContexts()
		
		assert.Len(t, contexts, 1, "Should find home directory context")
		assert.Contains(t, contexts, homeContext, "Should contain home AGENTS.md")
		assert.Equal(t, "# User home context", contexts[homeContext])
	})

	t.Run("multiple_context_sources", func(t *testing.T) {
		// Create working directory context
		workContext := filepath.Join(tmpWork, "KODELET.md")
		require.NoError(t, os.WriteFile(workContext, []byte("# Work context"), 0644))
		
		contexts := state.GetRelevantContexts()
		
		assert.Len(t, contexts, 2, "Should find both home and work contexts")
		assert.Contains(t, contexts, homeContext, "Should contain home context")
		assert.Contains(t, contexts, workContext, "Should contain work context")
	})
}

func TestBasicState_ContextDiscoveryEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	
	t.Run("no_context_files", func(t *testing.T) {
		// Change to directory with no context files
		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		state := NewBasicState(context.Background())
		contexts := state.GetRelevantContexts()
		
		assert.Empty(t, contexts, "Should return empty map when no context files exist")
	})

	t.Run("permission_denied", func(t *testing.T) {
		// Create a context file and make it unreadable
		restrictedFile := filepath.Join(tmpDir, "AGENTS.md")
		require.NoError(t, os.WriteFile(restrictedFile, []byte("# Restricted"), 0644))
		require.NoError(t, os.Chmod(restrictedFile, 0000)) // No permissions
		defer os.Chmod(restrictedFile, 0644) // Cleanup
		
		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		state := NewBasicState(context.Background())
		contexts := state.GetRelevantContexts()
		
		// Should gracefully handle permission errors
		assert.Empty(t, contexts, "Should handle permission errors gracefully")
	})

	t.Run("file_access_outside_working_directory", func(t *testing.T) {
		// Create context in a different location
		otherDir := filepath.Join(tmpDir, "other")
		require.NoError(t, os.MkdirAll(otherDir, 0755))
		
		otherContext := filepath.Join(otherDir, "KODELET.md")
		require.NoError(t, os.WriteFile(otherContext, []byte("# Other context"), 0644))
		
		// Set working directory to somewhere else
		workDir := filepath.Join(tmpDir, "work")
		require.NoError(t, os.MkdirAll(workDir, 0755))
		
		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(workDir))

		state := NewBasicState(context.Background())
		
		// Simulate accessing a file in the other directory
		accessedFile := filepath.Join(otherDir, "test.go")
		state.SetFileLastAccessed(accessedFile, time.Now())
		
		contexts := state.GetRelevantContexts()
		
		assert.Contains(t, contexts, otherContext, "Should find context in accessed file's directory")
		assert.Equal(t, "# Other context", contexts[otherContext])
	})
}


