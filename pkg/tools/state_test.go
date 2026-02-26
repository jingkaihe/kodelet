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
	s := NewBasicState(context.TODO())

	path := "test/file.txt"
	now := time.Now()

	err := s.SetFileLastAccessed(path, now)
	assert.NoError(t, err)

	lastAccessed, err := s.GetFileLastAccessed(path)
	assert.NoError(t, err)
	assert.True(t, lastAccessed.Equal(now))

	nonExistentPath := "non/existent/file.txt"
	lastAccessed, err = s.GetFileLastAccessed(nonExistentPath)
	assert.Error(t, err)
	assert.True(t, lastAccessed.IsZero())

	tools := s.Tools()
	mainTools := GetMainTools(context.Background(), []string{}, false)
	assert.Equal(t, len(mainTools), len(tools))
	for i, tool := range tools {
		assert.Equal(t, mainTools[i].Name(), tool.Name())
	}

	// Test subagent tools configuration via WithSubAgentToolsFromConfig
	subAgentTools := NewBasicState(context.TODO(), WithSubAgentToolsFromConfig())
	expectedSubAgentTools := GetSubAgentTools(context.Background(), []string{})
	assert.Equal(t, len(expectedSubAgentTools), len(subAgentTools.Tools()))
	for i, tool := range subAgentTools.Tools() {
		assert.Equal(t, expectedSubAgentTools[i].Name(), tool.Name())
	}
}

func TestClearFileLastAccessed(t *testing.T) {
	s := NewBasicState(context.TODO())

	path := "test/file.txt"
	now := time.Now()

	err := s.SetFileLastAccessed(path, now)
	assert.NoError(t, err)

	lastAccessed, err := s.GetFileLastAccessed(path)
	assert.NoError(t, err)
	assert.True(t, lastAccessed.Equal(now))

	err = s.ClearFileLastAccessed(path)
	assert.NoError(t, err)

	lastAccessed, err = s.GetFileLastAccessed(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has not been read yet")
	assert.True(t, lastAccessed.IsZero())
}

func TestConcurrentAccess(t *testing.T) {
	s := NewBasicState(context.TODO())

	const numGoroutines = 100
	const operationsPerGoroutine = 10

	done := make(chan bool, numGoroutines)

	for i := range numGoroutines {
		go func(_ int) {
			path := "test/file.txt"
			for j := range operationsPerGoroutine {
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

	for range numGoroutines {
		<-done
	}

	assert.True(t, true)
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
	config := llmtypes.Config{
		Provider:        "anthropic",
		Model:           "claude-3-5-sonnet",
		AllowedCommands: []string{"ls *", "pwd", "echo *"},
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config))

	retrievedConfig := s.GetLLMConfig()
	assert.NotNil(t, retrievedConfig)

	llmConfig, ok := retrievedConfig.(llmtypes.Config)
	assert.True(t, ok)
	assert.Equal(t, config.Provider, llmConfig.Provider)
	assert.Equal(t, config.Model, llmConfig.Model)
	assert.Equal(t, config.AllowedCommands, llmConfig.AllowedCommands)
}

func TestBasicState_ConfigureBashTool(t *testing.T) {
	allowedCommands := []string{"ls *", "pwd", "echo *", "git status"}
	config := llmtypes.Config{
		AllowedCommands: allowedCommands,
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config))

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

	assert.NotNil(t, bashTool)
	assert.Equal(t, allowedCommands, bashTool.allowedCommands)
}

func TestBasicState_ConfigureBashTool_WithSubAgentTools(t *testing.T) {
	allowedCommands := []string{"npm *", "yarn *"}
	config := llmtypes.Config{
		AllowedCommands: allowedCommands,
		IsSubAgent:      true,
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config), WithSubAgentToolsFromConfig())

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

	assert.NotNil(t, bashTool)
	assert.Equal(t, allowedCommands, bashTool.allowedCommands)
}

func TestBasicState_ConfigureBashTool_EmptyAllowedCommands(t *testing.T) {
	config := llmtypes.Config{
		AllowedCommands: []string{},
	}

	s := NewBasicState(context.TODO(), WithLLMConfig(config))

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

	assert.NotNil(t, bashTool)
	assert.Equal(t, []string{}, bashTool.allowedCommands)
}

func TestBasicState_DiscoverContexts(t *testing.T) {
	tmpDir := t.TempDir()

	subDir := filepath.Join(tmpDir, "submodule")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	deepDir := filepath.Join(subDir, "deep", "nested")
	require.NoError(t, os.MkdirAll(deepDir, 0o755))

	rootAgents := filepath.Join(tmpDir, "AGENTS.md")
	require.NoError(t, os.WriteFile(rootAgents, []byte("# Root project context"), 0o644))

	subAgents := filepath.Join(subDir, "AGENTS.md")
	require.NoError(t, os.WriteFile(subAgents, []byte("# Submodule context"), 0o644))

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpDir))

	ctx := context.Background()
	state := NewBasicState(ctx)

	t.Run("working_directory_context_only", func(t *testing.T) {
		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 1)
		assert.Contains(t, contexts, rootAgents)
		assert.Equal(t, "# Root project context", contexts[rootAgents])
	})

	t.Run("access_based_context_discovery", func(t *testing.T) {
		testFile := filepath.Join(subDir, "test.go")
		state.SetFileLastAccessed(testFile, time.Now())

		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 2)
		assert.Contains(t, contexts, rootAgents)
		assert.Contains(t, contexts, subAgents)
		assert.Equal(t, "# Root project context", contexts[rootAgents])
		assert.Equal(t, "# Submodule context", contexts[subAgents])
	})

	t.Run("deep_nested_access", func(t *testing.T) {
		deepFile := filepath.Join(deepDir, "nested.go")
		state.SetFileLastAccessed(deepFile, time.Now())

		contexts := state.DiscoverContexts()

		assert.Contains(t, contexts, subAgents)
		assert.Equal(t, "# Submodule context", contexts[subAgents])
	})
}

func TestBasicState_ContextFilePreference(t *testing.T) {
	tmpDir := t.TempDir()

	agentsFile := filepath.Join(tmpDir, "AGENTS.md")
	require.NoError(t, os.WriteFile(agentsFile, []byte("# Agents context"), 0o644))

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpDir))

	state := NewBasicState(context.Background())
	contexts := state.DiscoverContexts()

	assert.Len(t, contexts, 1)
	assert.Contains(t, contexts, agentsFile)
	assert.Equal(t, "# Agents context", contexts[agentsFile])
}

func TestBasicState_ContextFileCaching(t *testing.T) {
	tmpDir := t.TempDir()
	contextFile := filepath.Join(tmpDir, "AGENTS.md")

	initialContent := "# Initial content"
	require.NoError(t, os.WriteFile(contextFile, []byte(initialContent), 0o644))

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpDir))

	state := NewBasicState(context.Background())

	t.Run("initial_load", func(t *testing.T) {
		contexts := state.DiscoverContexts()
		assert.Len(t, contexts, 1)
		assert.Equal(t, initialContent, contexts[contextFile])
	})

	t.Run("cached_content", func(t *testing.T) {
		contexts := state.DiscoverContexts()
		assert.Equal(t, initialContent, contexts[contextFile])
	})

	t.Run("cache_invalidation", func(t *testing.T) {
		newContent := "# Updated content"
		time.Sleep(10 * time.Millisecond)
		require.NoError(t, os.WriteFile(contextFile, []byte(newContent), 0o644))

		contexts := state.DiscoverContexts()
		assert.Equal(t, newContent, contexts[contextFile])
	})
}

func TestBasicState_HomeDirectoryContext(t *testing.T) {
	tmpHome := t.TempDir()
	kodeletDir := filepath.Join(tmpHome, ".kodelet")
	require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

	homeContext := filepath.Join(kodeletDir, "AGENTS.md")
	require.NoError(t, os.WriteFile(homeContext, []byte("# User home context"), 0o644))

	tmpWork := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmpWork))

	ctx := context.Background()
	state := NewBasicState(ctx)

	state.contextDiscovery.homeDir = kodeletDir

	t.Run("home_context_discovery", func(t *testing.T) {
		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 1)
		assert.Contains(t, contexts, homeContext)
		assert.Equal(t, "# User home context", contexts[homeContext])
	})

	t.Run("multiple_context_sources", func(t *testing.T) {
		workContext := filepath.Join(tmpWork, "AGENTS.md")
		require.NoError(t, os.WriteFile(workContext, []byte("# Work context"), 0o644))

		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 2)
		assert.Contains(t, contexts, homeContext)
		assert.Contains(t, contexts, workContext)
	})
}

func TestBasicState_ContextDiscoveryEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("no_context_files", func(t *testing.T) {
		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		state := NewBasicState(context.Background())
		contexts := state.DiscoverContexts()

		assert.Empty(t, contexts)
	})

	t.Run("permission_denied", func(t *testing.T) {
		restrictedFile := filepath.Join(tmpDir, "AGENTS.md")
		require.NoError(t, os.WriteFile(restrictedFile, []byte("# Restricted"), 0o644))
		require.NoError(t, os.Chmod(restrictedFile, 0o000))
		defer os.Chmod(restrictedFile, 0o644)

		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		state := NewBasicState(context.Background())
		contexts := state.DiscoverContexts()

		assert.Empty(t, contexts)
	})

	t.Run("file_access_outside_working_directory", func(t *testing.T) {
		otherDir := filepath.Join(tmpDir, "other")
		require.NoError(t, os.MkdirAll(otherDir, 0o755))

		otherContext := filepath.Join(otherDir, "AGENTS.md")
		require.NoError(t, os.WriteFile(otherContext, []byte("# Other context"), 0o644))

		workDir := filepath.Join(tmpDir, "work")
		require.NoError(t, os.MkdirAll(workDir, 0o755))

		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(workDir))

		state := NewBasicState(context.Background())

		accessedFile := filepath.Join(otherDir, "test.go")
		state.SetFileLastAccessed(accessedFile, time.Now())

		contexts := state.DiscoverContexts()

		// With the new behavior, files outside working directory should NOT have their contexts discovered
		assert.NotContains(t, contexts, otherContext)
		assert.Empty(t, contexts) // Should only find working directory contexts (none in this case)
	})
}

func TestBasicState_ContextTraversalAndDeduplication(t *testing.T) {
	tmpDir := t.TempDir()

	fooDir := filepath.Join(tmpDir, "foo")
	barDir := filepath.Join(fooDir, "bar")
	bazDir := filepath.Join(barDir, "baz")
	require.NoError(t, os.MkdirAll(bazDir, 0o755))

	fooAgents := filepath.Join(fooDir, "AGENTS.md")
	barAgents := filepath.Join(barDir, "AGENTS.md")
	bazAgents := filepath.Join(bazDir, "AGENTS.md")

	require.NoError(t, os.WriteFile(fooAgents, []byte("# Foo context"), 0o644))
	require.NoError(t, os.WriteFile(barAgents, []byte("# Bar context"), 0o644))
	require.NoError(t, os.WriteFile(bazAgents, []byte("# Baz context"), 0o644))

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(fooDir))

	ctx := context.Background()
	state := NewBasicState(ctx)

	t.Run("single_deep_file_access", func(t *testing.T) {
		bazFile := filepath.Join(bazDir, "test.go")
		state.SetFileLastAccessed(bazFile, time.Now())

		contexts := state.DiscoverContexts()

		// Should find:
		// 1. Working directory context: fooAgents (from step 1 of DiscoverContexts)
		// 2. Traversal contexts: bazAgents, barAgents (from step 2 - traverse up from baz to working dir boundary)
		expectedContexts := []string{fooAgents, barAgents, bazAgents}

		assert.Len(t, contexts, len(expectedContexts), "Should find exactly %d contexts", len(expectedContexts))
		for _, expected := range expectedContexts {
			assert.Contains(t, contexts, expected, "Should contain context file: %s", expected)
		}

		assert.Equal(t, "# Foo context", contexts[fooAgents])
		assert.Equal(t, "# Bar context", contexts[barAgents])
		assert.Equal(t, "# Baz context", contexts[bazAgents])
	})

	t.Run("multiple_file_access_with_deduplication", func(t *testing.T) {
		state = NewBasicState(ctx)

		barFile := filepath.Join(barDir, "service.go")
		bazFile := filepath.Join(bazDir, "handler.go")
		state.SetFileLastAccessed(barFile, time.Now())
		state.SetFileLastAccessed(bazFile, time.Now())

		contexts := state.DiscoverContexts()

		// Should find:
		// 1. Working directory context: fooAgents (from step 1)
		// 2. From barFile traversal: barAgents (stops at working dir boundary)
		// 3. From bazFile traversal: bazAgents, barAgents (but barAgents already found, should not duplicate)
		expectedContexts := []string{fooAgents, barAgents, bazAgents}

		assert.Len(t, contexts, len(expectedContexts), "Should find exactly %d unique contexts (no duplicates)", len(expectedContexts))
		for _, expected := range expectedContexts {
			assert.Contains(t, contexts, expected, "Should contain context file: %s", expected)
		}

		assert.Equal(t, "# Foo context", contexts[fooAgents])
		assert.Equal(t, "# Bar context", contexts[barAgents])
		assert.Equal(t, "# Baz context", contexts[bazAgents])
	})

	t.Run("traversal_stops_at_working_directory_boundary", func(t *testing.T) {
		rootAgents := filepath.Join(tmpDir, "AGENTS.md")
		require.NoError(t, os.WriteFile(rootAgents, []byte("# Root context"), 0o644))

		state = NewBasicState(ctx)
		bazFile := filepath.Join(bazDir, "deep.go")
		state.SetFileLastAccessed(bazFile, time.Now())

		contexts := state.DiscoverContexts()

		// Should NOT find rootAgents because traversal stops at working directory boundary
		assert.NotContains(t, contexts, rootAgents, "Should NOT traverse above working directory")

		expectedContexts := []string{fooAgents, barAgents, bazAgents}
		assert.Len(t, contexts, len(expectedContexts))
		for _, expected := range expectedContexts {
			assert.Contains(t, contexts, expected)
		}

		os.Remove(rootAgents)
	})

	t.Run("missing_intermediate_context_files", func(t *testing.T) {
		require.NoError(t, os.Remove(barAgents))

		state = NewBasicState(ctx)
		bazFile := filepath.Join(bazDir, "missing.go")
		state.SetFileLastAccessed(bazFile, time.Now())

		contexts := state.DiscoverContexts()

		// Should find fooAgents (working dir) and bazAgents (direct), but skip missing barAgents
		expectedContexts := []string{fooAgents, bazAgents}
		assert.Len(t, contexts, len(expectedContexts))
		assert.Contains(t, contexts, fooAgents)
		assert.Contains(t, contexts, bazAgents)
		assert.NotContains(t, contexts, barAgents, "Should not find removed context file")

		require.NoError(t, os.WriteFile(barAgents, []byte("# Bar context"), 0o644))
	})
}

func TestBasicState_ConfigurableContextPatterns(t *testing.T) {
	ctx := context.Background()

	t.Run("readme_not_loaded_by_default", func(t *testing.T) {
		tmpDir := t.TempDir()
		readmePath := filepath.Join(tmpDir, "README.md")
		require.NoError(t, os.WriteFile(readmePath, []byte("# Project README"), 0o644))

		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		state := NewBasicState(ctx)
		contexts := state.DiscoverContexts()

		assert.NotContains(t, contexts, readmePath, "README.md should not be loaded by default")
	})

	t.Run("readme_loaded_when_configured", func(t *testing.T) {
		tmpDir := t.TempDir()
		readmePath := filepath.Join(tmpDir, "README.md")
		require.NoError(t, os.WriteFile(readmePath, []byte("# Project README\n\nProject documentation"), 0o644))

		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		config := llmtypes.Config{
			Context: &llmtypes.ContextConfig{
				Patterns: []string{"README.md"},
			},
		}
		state := NewBasicState(ctx, WithLLMConfig(config))
		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 1)
		assert.Contains(t, contexts, readmePath)
		assert.Equal(t, "# Project README\n\nProject documentation", contexts[readmePath])
	})

	t.Run("multiple_patterns_first_match_wins", func(t *testing.T) {
		tmpDir := t.TempDir()
		agentsPath := filepath.Join(tmpDir, "AGENTS.md")
		readmePath := filepath.Join(tmpDir, "README.md")
		require.NoError(t, os.WriteFile(agentsPath, []byte("# Agents Context"), 0o644))
		require.NoError(t, os.WriteFile(readmePath, []byte("# Project README"), 0o644))

		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		config := llmtypes.Config{
			Context: &llmtypes.ContextConfig{
				Patterns: []string{"AGENTS.md", "README.md"},
			},
		}
		state := NewBasicState(ctx, WithLLMConfig(config))
		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 1, "Only first matching pattern should be loaded per directory")
		assert.Contains(t, contexts, agentsPath)
		assert.NotContains(t, contexts, readmePath)
	})

	t.Run("custom_pattern", func(t *testing.T) {
		tmpDir := t.TempDir()
		customPath := filepath.Join(tmpDir, "CODING.md")
		require.NoError(t, os.WriteFile(customPath, []byte("# Coding Guidelines"), 0o644))

		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		config := llmtypes.Config{
			Context: &llmtypes.ContextConfig{
				Patterns: []string{"CODING.md"},
			},
		}
		state := NewBasicState(ctx, WithLLMConfig(config))
		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 1)
		assert.Contains(t, contexts, customPath)
		assert.Equal(t, "# Coding Guidelines", contexts[customPath])
	})

	t.Run("context_caching_with_configured_patterns", func(t *testing.T) {
		tmpDir := t.TempDir()
		readmePath := filepath.Join(tmpDir, "README.md")

		oldWd, _ := os.Getwd()
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		config := llmtypes.Config{
			Context: &llmtypes.ContextConfig{
				Patterns: []string{"README.md"},
			},
		}
		state := NewBasicState(ctx, WithLLMConfig(config))

		initialContent := "# Initial README"
		require.NoError(t, os.WriteFile(readmePath, []byte(initialContent), 0o644))

		contexts := state.DiscoverContexts()
		assert.Len(t, contexts, 1)
		assert.Equal(t, initialContent, contexts[readmePath])

		contexts = state.DiscoverContexts()
		assert.Equal(t, initialContent, contexts[readmePath])

		newContent := "# Updated README\n\nNew documentation"
		time.Sleep(10 * time.Millisecond)
		require.NoError(t, os.WriteFile(readmePath, []byte(newContent), 0o644))

		contexts = state.DiscoverContexts()
		assert.Equal(t, newContent, contexts[readmePath])
	})
}

func TestNewBasicState_ErrorHandling(t *testing.T) {
	ctx := context.Background()

	t.Run("normal_case", func(t *testing.T) {
		state := NewBasicState(ctx)
		assert.NotEmpty(t, state.contextDiscovery.workingDir)
		assert.NotNil(t, state.contextDiscovery)
	})

	t.Run("home_context_disabled_on_error", func(t *testing.T) {
		state := NewBasicState(ctx)
		assert.NotNil(t, state.contextDiscovery)

		state.contextDiscovery.homeDir = ""
		homeContext := state.loadContextFromPatterns("")
		assert.Nil(t, homeContext, "Home context should be nil when homeDir is empty")
	})

	t.Run("context_discovery_works_with_fallbacks", func(t *testing.T) {
		tmpDir := t.TempDir()
		contextFile := filepath.Join(tmpDir, "AGENTS.md")
		require.NoError(t, os.WriteFile(contextFile, []byte("# Test context"), 0o644))

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		defer os.Chdir(oldWd)
		require.NoError(t, os.Chdir(tmpDir))

		state := NewBasicState(ctx)
		contexts := state.DiscoverContexts()

		assert.Len(t, contexts, 1)
		assert.Contains(t, contexts, contextFile)
		assert.Equal(t, "# Test context", contexts[contextFile])
	})
}

func TestWithSubAgentToolsFromConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("excludes subagent tool", func(t *testing.T) {
		state := NewBasicState(ctx, WithSubAgentToolsFromConfig())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "subagent", "Subagent tools should not contain subagent to prevent recursion")
	})

	t.Run("includes expected tools", func(t *testing.T) {
		state := NewBasicState(ctx, WithSubAgentToolsFromConfig())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		// Should include basic tools
		assert.Contains(t, toolNames, "bash")
		assert.Contains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "grep_tool")
		assert.Contains(t, toolNames, "glob_tool")
	})

	t.Run("respects allowed_tools from config", func(t *testing.T) {
		config := llmtypes.Config{
			AllowedTools: []string{"file_read", "grep_tool"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithSubAgentToolsFromConfig())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		// Should include requested tools plus meta tools
		assert.Contains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "grep_tool")
		assert.Contains(t, toolNames, "glob_tool") // meta tool always included
	})

	t.Run("no_tools with NoToolsMarker", func(t *testing.T) {
		config := llmtypes.Config{
			AllowedTools: []string{NoToolsMarker},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithSubAgentToolsFromConfig())

		assert.Empty(t, state.Tools(), "NoToolsMarker should result in no tools")
	})

	t.Run("ApplyPatchEnabled removes file_write and file_edit", func(t *testing.T) {
		config := llmtypes.Config{
			ApplyPatchEnabled: true,
			AllowedTools:      []string{"file_read", "file_write", "file_edit"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithSubAgentToolsFromConfig())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "apply_patch")
		assert.Contains(t, toolNames, "file_read")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})

	t.Run("ApplyPatchEnabled still removes file_write and file_edit after fallback", func(t *testing.T) {
		config := llmtypes.Config{
			ApplyPatchEnabled: true,
			AllowedTools:      []string{"file_read", "unknown_tool"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithSubAgentToolsFromConfig())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "apply_patch")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})
}

func TestWithMainTools(t *testing.T) {
	ctx := context.Background()

	t.Run("includes subagent tool", func(t *testing.T) {
		state := NewBasicState(ctx, WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "subagent", "Main tools should include subagent")
	})

	t.Run("todo tools disabled by default", func(t *testing.T) {
		state := NewBasicState(ctx, WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "todo_read")
		assert.NotContains(t, toolNames, "todo_write")
	})

	t.Run("EnableTodos includes todo tools", func(t *testing.T) {
		config := llmtypes.Config{
			EnableTodos: true,
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "todo_read")
		assert.Contains(t, toolNames, "todo_write")
	})

	t.Run("respects allowed_tools from config", func(t *testing.T) {
		config := llmtypes.Config{
			AllowedTools: []string{"bash", "file_read", "subagent"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "bash")
		assert.Contains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "subagent")
	})

	t.Run("no_tools with NoToolsMarker", func(t *testing.T) {
		config := llmtypes.Config{
			AllowedTools: []string{NoToolsMarker},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		assert.Empty(t, state.Tools(), "NoToolsMarker should result in no tools")
	})

	t.Run("DisableSubagent excludes subagent tool", func(t *testing.T) {
		config := llmtypes.Config{
			DisableSubagent: true,
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "subagent", "DisableSubagent should exclude subagent tool")
		assert.Contains(t, toolNames, "bash", "Other tools should remain")
		assert.Contains(t, toolNames, "web_fetch", "web_fetch should remain when subagent is disabled")
		assert.Contains(t, toolNames, "image_recognition", "image_recognition should remain when subagent is disabled")
	})

	t.Run("DisableSubagent with allowed_tools still excludes subagent", func(t *testing.T) {
		config := llmtypes.Config{
			DisableSubagent: true,
			AllowedTools:    []string{"bash", "file_read", "subagent", "web_fetch"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "subagent", "DisableSubagent should exclude subagent even from allowed_tools")
		assert.Contains(t, toolNames, "bash")
		assert.Contains(t, toolNames, "web_fetch")
	})

	t.Run("DisableSubagent false keeps subagent tool", func(t *testing.T) {
		config := llmtypes.Config{
			DisableSubagent: false,
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "subagent", "subagent should remain when DisableSubagent is false")
	})

	t.Run("ApplyPatchEnabled removes file_write and file_edit from defaults", func(t *testing.T) {
		config := llmtypes.Config{
			ApplyPatchEnabled: true,
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "apply_patch")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})

	t.Run("ApplyPatchEnabled with allowed_tools keeps apply_patch and removes file_write/file_edit", func(t *testing.T) {
		config := llmtypes.Config{
			ApplyPatchEnabled: true,
			AllowedTools:      []string{"bash", "file_read", "file_write", "file_edit"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "bash")
		assert.Contains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "apply_patch")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})

	t.Run("ApplyPatchEnabled respects NoToolsMarker", func(t *testing.T) {
		config := llmtypes.Config{
			ApplyPatchEnabled: true,
			AllowedTools:      []string{NoToolsMarker},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())
		assert.Empty(t, state.Tools(), "NoToolsMarker should still disable all tools")
	})

	t.Run("ApplyPatchEnabled still removes file_write and file_edit after fallback", func(t *testing.T) {
		config := llmtypes.Config{
			ApplyPatchEnabled: true,
			AllowedTools:      []string{"bash", "unknown_tool"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "apply_patch")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})
}

func TestGetLLMConfig_ReturnsSubagentArgs(t *testing.T) {
	ctx := context.Background()

	t.Run("returns config with subagent_args", func(t *testing.T) {
		config := llmtypes.Config{
			SubagentArgs: "--profile cheap --use-weak-model",
		}
		state := NewBasicState(ctx, WithLLMConfig(config))

		retrievedConfig, ok := state.GetLLMConfig().(llmtypes.Config)
		assert.True(t, ok, "GetLLMConfig should return llmtypes.Config")
		assert.Equal(t, "--profile cheap --use-weak-model", retrievedConfig.SubagentArgs)
	})

	t.Run("returns empty subagent_args by default", func(t *testing.T) {
		state := NewBasicState(ctx)

		retrievedConfig, ok := state.GetLLMConfig().(llmtypes.Config)
		assert.True(t, ok, "GetLLMConfig should return llmtypes.Config")
		assert.Empty(t, retrievedConfig.SubagentArgs)
	})
}
