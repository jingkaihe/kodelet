package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBasicState(t *testing.T) {
	s := NewBasicState(context.TODO())

	tools := s.Tools()
	mainTools := GetMainTools(context.Background(), []string{})
	assert.Equal(t, len(mainTools), len(tools))
	for i, tool := range tools {
		assert.Equal(t, mainTools[i].Name(), tool.Name())
	}
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

func TestToolContextHelpers(t *testing.T) {
	ctx := ContextWithConversationID(context.Background(), "  conv-123  ")
	toolCtx := toolContextFromContext(ctx)
	assert.Equal(t, "conv-123", toolCtx.ConversationID)

	emptyCtx := ContextWithToolContext(context.Background(), ToolContext{})
	assert.Equal(t, ToolContext{}, toolContextFromContext(emptyCtx))

	store := &testMetadataStore{metadata: map[string]any{"k": "v"}}
	fromThread := ToolContextFromThreadState(
		llmtypes.Config{Provider: " openai ", Model: " gpt-5 ", Profile: " work ", RecipeName: " review ", WorkingDirectory: " /repo "},
		" conv-456 ",
		" /state ",
		store,
	)
	assert.Equal(t, "conv-456", fromThread.ConversationID)
	assert.Equal(t, "/state", fromThread.WorkingDir)
	assert.Equal(t, "openai", fromThread.Provider)
	assert.Equal(t, "gpt-5", fromThread.Model)
	assert.Equal(t, "work", fromThread.Profile)
	assert.Equal(t, "review", fromThread.RecipeName)
	assert.Same(t, store, fromThread.MetadataStore)

	fromConfigWorkingDir := ToolContextFromThreadState(llmtypes.Config{WorkingDirectory: " /config "}, "", " ", nil)
	assert.Equal(t, "/config", fromConfigWorkingDir.WorkingDir)
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

func TestBasicState_ConfigureBashTool_CustomTimeout(t *testing.T) {
	config := llmtypes.Config{
		Bash: &llmtypes.BashConfig{Timeout: 5 * time.Minute},
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
	assert.Equal(t, 5*time.Minute, bashTool.maxTimeout)
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

	t.Run("nested_context_files_are_not_discovered", func(t *testing.T) {
		contexts := state.DiscoverContexts()

		assert.NotContains(t, contexts, subAgents)
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

func TestWithMainTools(t *testing.T) {
	ctx := context.Background()

	t.Run("respects allowed_tools from config", func(t *testing.T) {
		config := llmtypes.Config{
			AllowedTools: []string{"bash", "file_read"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "bash")
		assert.Contains(t, toolNames, "file_read")
		assert.NotContains(t, toolNames, "subagent")
	})

	t.Run("no_tools with NoToolsMarker", func(t *testing.T) {
		config := llmtypes.Config{
			AllowedTools: []string{NoToolsMarker},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		assert.Empty(t, state.Tools(), "NoToolsMarker should result in no tools")
	})

	t.Run("patch removes file_read file_write and file_edit from defaults", func(t *testing.T) {
		config := llmtypes.Config{
			ToolMode: llmtypes.ToolModePatch,
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "apply_patch")
		assert.NotContains(t, toolNames, "file_read")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})

	t.Run("patch with allowed_tools keeps apply_patch and removes file_read file_write and file_edit", func(t *testing.T) {
		config := llmtypes.Config{
			ToolMode:     llmtypes.ToolModePatch,
			AllowedTools: []string{"bash", "file_read", "file_write", "file_edit"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "bash")
		assert.NotContains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "apply_patch")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})

	t.Run("patch respects NoToolsMarker", func(t *testing.T) {
		config := llmtypes.Config{
			ToolMode:     llmtypes.ToolModePatch,
			AllowedTools: []string{NoToolsMarker},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())
		assert.Empty(t, state.Tools(), "NoToolsMarker should still disable all tools")
	})

	t.Run("patch still removes file_read file_write and file_edit after fallback", func(t *testing.T) {
		config := llmtypes.Config{
			ToolMode:     llmtypes.ToolModePatch,
			AllowedTools: []string{"bash", "unknown_tool"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "apply_patch")
		assert.NotContains(t, toolNames, "file_read")
		assert.NotContains(t, toolNames, "file_write")
		assert.NotContains(t, toolNames, "file_edit")
	})

	t.Run("full mode removes apply_patch from defaults", func(t *testing.T) {
		config := llmtypes.Config{
			ToolMode: llmtypes.ToolModeFull,
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "apply_patch")
		assert.Contains(t, toolNames, "file_write")
		assert.Contains(t, toolNames, "file_edit")
	})

	t.Run("full mode removes apply_patch from allowed_tools", func(t *testing.T) {
		config := llmtypes.Config{
			ToolMode:     llmtypes.ToolModeFull,
			AllowedTools: []string{"bash", "apply_patch", "file_read"},
		}
		state := NewBasicState(ctx, WithLLMConfig(config), WithMainTools())

		toolNames := make([]string, len(state.Tools()))
		for i, tool := range state.Tools() {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "bash")
		assert.Contains(t, toolNames, "file_read")
		assert.NotContains(t, toolNames, "apply_patch")
	})
}

func TestNoToolsConfigured_PreventsAdditionalToolRegistration(t *testing.T) {
	ctx := context.Background()
	config := llmtypes.Config{AllowedTools: []string{NoToolsMarker}}

	state := NewBasicState(
		ctx,
		WithLLMConfig(config),
		WithMainTools(),
		WithExtensionTools([]tooltypes.Tool{&testTool{name: "extension_tool"}}),
		WithSkillTool(),
	)

	assert.Empty(t, state.Tools(), "NoToolsMarker should block all later tool registration")
}

func TestExplicitExecutionToolSelection(t *testing.T) {
	readTools := []string{"file_read", "grep_tool", "glob_tool"}
	for _, mode := range []llmtypes.ToolMode{llmtypes.ToolModeFull, llmtypes.ToolModePatch} {
		for _, withMainTools := range []bool{false, true} {
			for _, tt := range []struct {
				name   string
				legacy []string
				opts   *llmtypes.ExecutionOptions
				want   []string
			}{
				{"read only", nil, &llmtypes.ExecutionOptions{AllowedTools: &readTools}, readTools},
				{"explicit patch", nil, &llmtypes.ExecutionOptions{AllowedTools: new([]string{"apply_patch"})}, []string{"apply_patch"}},
				{"legacy ceiling", []string{"file_read"}, &llmtypes.ExecutionOptions{AllowedTools: &readTools}, []string{"file_read"}},
				{"legacy deny", []string{NoToolsMarker}, &llmtypes.ExecutionOptions{AllowedTools: &readTools}, nil},
				{"empty", nil, &llmtypes.ExecutionOptions{AllowedTools: new([]string{})}, nil},
				{"no tools", nil, &llmtypes.ExecutionOptions{AllowedTools: &readTools, NoTools: new(true)}, nil},
				{"disabled search", nil, &llmtypes.ExecutionOptions{AllowedTools: &readTools, EnableFSSearchTools: new(false)}, []string{"file_read"}},
				{"unknown never defaults", nil, &llmtypes.ExecutionOptions{AllowedTools: new([]string{"not-a-tool"})}, nil},
			} {
				t.Run(string(mode)+"/"+tt.name+"/main="+strconv.FormatBool(withMainTools), func(t *testing.T) {
					config := llmtypes.Config{ToolMode: mode, AllowedTools: tt.legacy, EnableFSSearchTools: true, ExecutionOptions: tt.opts}
					opts := []BasicStateOption{WithLLMConfig(config)}
					if withMainTools {
						opts = append(opts, WithMainTools())
					}
					state := NewBasicState(t.Context(), opts...)
					var names []string
					for _, tool := range state.Tools() {
						names = append(names, tool.Name())
					}
					assert.ElementsMatch(t, tt.want, names)
					assert.Equal(t, mode, state.llmConfig.ToolMode)
				})
			}
		}
	}
}

func TestOmittedExecutionToolSelectionPreservesMode(t *testing.T) {
	for _, options := range []*llmtypes.ExecutionOptions{nil, {}, {NoSkills: new(true)}} {
		state := NewBasicState(t.Context(), WithLLMConfig(llmtypes.Config{ToolMode: llmtypes.ToolModePatch, ExecutionOptions: options}), WithMainTools())
		var names []string
		for _, tool := range state.Tools() {
			names = append(names, tool.Name())
		}
		assert.Contains(t, names, "apply_patch")
		for _, name := range []string{"file_read", "file_write", "file_edit", "grep_tool", "glob_tool"} {
			assert.NotContains(t, names, name)
		}
	}
}

func TestWithSkillTool_RespectsExplicitAllowlist(t *testing.T) {
	ctx := context.Background()
	state := NewBasicState(
		ctx,
		WithLLMConfig(llmtypes.Config{AllowedTools: []string{"file_read", "grep_tool", "glob_tool"}, EnableFSSearchTools: true}),
		WithMainTools(),
		WithSkillTool(),
	)

	toolNames := make([]string, len(state.Tools()))
	for i, tool := range state.Tools() {
		toolNames[i] = tool.Name()
	}

	assert.Equal(t, []string{"grep_tool", "glob_tool", "get_goal", "update_goal", "file_read"}, toolNames)
	assert.NotContains(t, toolNames, "skill")
}

func TestWithExtensionTools_RespectsExplicitAllowlist(t *testing.T) {
	ctx := context.Background()
	state := NewBasicState(
		ctx,
		WithLLMConfig(llmtypes.Config{AllowedTools: []string{"file_read", "grep_tool", "glob_tool"}, EnableFSSearchTools: true}),
		WithMainTools(),
		WithExtensionTools([]tooltypes.Tool{&testTool{name: "not_allowed_extension_tool"}}),
	)

	toolNames := make([]string, len(state.Tools()))
	for i, tool := range state.Tools() {
		toolNames[i] = tool.Name()
	}

	assert.Equal(t, []string{"grep_tool", "glob_tool", "get_goal", "update_goal", "file_read"}, toolNames)
	assert.Empty(t, state.ExtensionTools())
	assert.NotContains(t, toolNames, "not_allowed_extension_tool")
}

func TestTools_RejectsExtensionToolCollisionWithBuiltIn(t *testing.T) {
	ctx := context.Background()
	state := NewBasicState(
		ctx,
		WithMainTools(),
		WithExtensionTools([]tooltypes.Tool{&testTool{name: "bash"}, &testTool{name: "extension_unique"}}),
	)

	var bashCount int
	var toolNames []string
	for _, tool := range state.Tools() {
		toolNames = append(toolNames, tool.Name())
		if tool.Name() == "bash" {
			bashCount++
		}
	}

	assert.Equal(t, 1, bashCount)
	assert.Contains(t, toolNames, "extension_unique")
}

func TestWithSkillTool_RespectsNoSkillsFlag(t *testing.T) {
	ctx := context.Background()
	viper.Set("no_skills", true)
	t.Cleanup(func() {
		viper.Set("no_skills", false)
	})

	state := NewBasicState(
		ctx,
		WithLLMConfig(llmtypes.Config{}),
		WithMainTools(),
		WithSkillTool(),
	)

	toolNames := make([]string, len(state.Tools()))
	for i, tool := range state.Tools() {
		toolNames[i] = tool.Name()
	}

	assert.NotContains(t, toolNames, "skill")
}
