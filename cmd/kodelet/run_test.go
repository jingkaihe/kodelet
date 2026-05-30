package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetQueryFromStdinOrArgs(t *testing.T) {
	type result struct {
		query string
		err   error
	}

	t.Run("uses piped stdin", func(t *testing.T) {
		got := withPipeStdin(t, "from stdin\n", func() result {
			query, err := getQueryFromStdinOrArgs(nil)
			return result{query: query, err: err}
		})

		require.NoError(t, got.err)
		assert.Equal(t, "from stdin\n", got.query)
	})

	t.Run("combines args before piped stdin", func(t *testing.T) {
		got := withPipeStdin(t, "details from stdin", func() result {
			query, err := getQueryFromStdinOrArgs([]string{"summarize", "this"})
			return result{query: query, err: err}
		})

		require.NoError(t, got.err)
		assert.Equal(t, "summarize this\ndetails from stdin", got.query)
	})

	t.Run("uses args when stdin is terminal-like", func(t *testing.T) {
		got := withDevNullStdin(t, func() result {
			query, err := getQueryFromStdinOrArgs([]string{"hello", "world"})
			return result{query: query, err: err}
		})

		require.NoError(t, got.err)
		assert.Equal(t, "hello world", got.query)
	})

	t.Run("errors without args when stdin is terminal-like", func(t *testing.T) {
		got := withDevNullStdin(t, func() result {
			query, err := getQueryFromStdinOrArgs(nil)
			return result{query: query, err: err}
		})

		assert.Error(t, got.err)
		assert.Empty(t, got.query)
	})
}

func TestLoadResumeConversationConfig_UsesStoredProfileAndMetadata(t *testing.T) {
	originalSettings := viper.AllSettings()
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "base-model")
	viper.Set("profiles", map[string]any{
		"codex": map[string]any{
			"provider": "openai",
			"model":    "gpt-5.5",
			"openai": map[string]any{
				"platform": "codex",
				"api_mode": "responses",
			},
		},
	})

	ctx := context.Background()
	dbPath := filepath.Join(basePath, "storage.db")
	sqlDB, err := db.Open(ctx, dbPath)
	require.NoError(t, err)
	runner := db.NewMigrationRunner(sqlDB)
	require.NoError(t, runner.Run(ctx, migrations.All()))
	require.NoError(t, sqlDB.Close())

	store, err := conversations.NewConversationStore(ctx, &conversations.Config{
		StoreType: "sqlite",
		BasePath:  basePath,
	})
	require.NoError(t, err)
	defer func() {
		_ = store.Close()
	}()

	conversationID := convtypes.GenerateID()
	err = store.Save(ctx, convtypes.ConversationRecord{
		ID:          conversationID,
		Provider:    "openai",
		RawMessages: []byte(`[]`),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata: map[string]any{
			"profile":      "codex",
			"model":        "gpt-5.5",
			"platform":     "codex",
			"api_mode":     "responses",
			"service_tier": "fast",
		},
	})
	require.NoError(t, err)

	cmd := &cobra.Command{Use: "run"}
	config, resolvedCWD, err := loadResumeConversationConfig(ctx, cmd, conversationID, "")
	require.NoError(t, err)

	assert.Equal(t, "codex", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-5.5", config.Model)
	assert.NotEmpty(t, resolvedCWD)
	require.NotNil(t, config.OpenAI)
	assert.Equal(t, "codex", config.OpenAI.Platform)
	assert.Equal(t, llmtypes.OpenAIAPIMode("responses"), config.OpenAI.APIMode)
	assert.Equal(t, llmtypes.OpenAIServiceTierFast, config.OpenAI.ServiceTier)
}

func TestLoadResumeConversationConfig_DefaultsToCurrentConfigForNewConversation(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-5.5")

	cmd := &cobra.Command{Use: "run"}
	config, resolvedCWD, err := loadResumeConversationConfig(context.Background(), cmd, "", "")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-5.5", config.Model)
	assert.NotEmpty(t, resolvedCWD)
}

func TestLoadResumeConversationConfig_ProfiledConversationPreservesExplicitFlagOverrides(t *testing.T) {
	originalSettings := viper.AllSettings()
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("profile", "current")
	viper.Set("provider", "anthropic")
	viper.Set("profiles", map[string]any{
		"current": map[string]any{
			"context": map[string]any{
				"patterns": []string{"CURRENT.md"},
			},
			"allowed_tools": []string{"bash"},
			"tool_mode":     "full",
		},
		"anthropic": map[string]any{
			"provider": "openai",
			"context": map[string]any{
				"patterns": []string{"README.md"},
			},
			"allowed_tools": []string{"grep_tool"},
			"tool_mode":     "full",
		},
	})

	ctx := context.Background()
	dbPath := filepath.Join(basePath, "storage.db")
	sqlDB, err := db.Open(ctx, dbPath)
	require.NoError(t, err)
	runner := db.NewMigrationRunner(sqlDB)
	require.NoError(t, runner.Run(ctx, migrations.All()))
	require.NoError(t, sqlDB.Close())

	store, err := conversations.NewConversationStore(ctx, &conversations.Config{
		StoreType: "sqlite",
		BasePath:  basePath,
	})
	require.NoError(t, err)
	defer func() {
		_ = store.Close()
	}()

	conversationID := convtypes.GenerateID()
	err = store.Save(ctx, convtypes.ConversationRecord{
		ID:          conversationID,
		RawMessages: []byte(`[]`),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata: map[string]any{
			"profile": "anthropic",
		},
	})
	require.NoError(t, err)

	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().StringSlice("context-patterns", []string{"AGENTS.md"}, "Context file patterns")
	err = cmd.Flags().Set("context-patterns", "CODING.md,README.md")
	require.NoError(t, err)
	cmd.Flags().StringSlice("allowed-tools", []string{}, "Allowed tools")
	err = cmd.Flags().Set("allowed-tools", "bash,file_read")
	require.NoError(t, err)
	cmd.Flags().String("tool-mode", "full", "Tool mode")
	err = cmd.Flags().Set("tool-mode", "patch")
	require.NoError(t, err)

	config, _, err := loadResumeConversationConfig(ctx, cmd, conversationID, "")
	require.NoError(t, err)

	assert.Equal(t, "anthropic", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	require.NotNil(t, config.Context)
	assert.Equal(t, []string{"CODING.md", "README.md"}, config.Context.Patterns)
	assert.Equal(t, []string{"bash", "file_read"}, config.AllowedTools)
	assert.Equal(t, llmtypes.ToolModePatch, config.ToolMode)
}

func TestLoadResumeConversationConfig_ProfileCanDisableFSSearchToolsFromRootDefault(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("disable_fs_search_tools", true)
	viper.Set("provider", "openai")
	viper.Set("profile", "anthropic")
	viper.Set("profiles", map[string]any{
		"anthropic": map[string]any{
			"provider":                "anthropic",
			"disable_fs_search_tools": false,
		},
	})

	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().Bool("disable-fs-search-tools", false, "Disable filesystem search tools")

	config, _, err := loadResumeConversationConfig(context.Background(), cmd, "", "")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.False(t, config.DisableFSSearchTools)
	assert.False(t, cmd.Flags().Changed("disable-fs-search-tools"))
}

func TestLoadResumeConversationConfig_DefaultStoredProfileIgnoresActiveProfile(t *testing.T) {
	originalSettings := viper.AllSettings()
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "base-model")
	viper.Set("profile", "work")
	viper.Set("profiles", map[string]any{
		"work": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
		},
	})

	ctx := context.Background()
	dbPath := filepath.Join(basePath, "storage.db")
	sqlDB, err := db.Open(ctx, dbPath)
	require.NoError(t, err)
	runner := db.NewMigrationRunner(sqlDB)
	require.NoError(t, runner.Run(ctx, migrations.All()))
	require.NoError(t, sqlDB.Close())

	store, err := conversations.NewConversationStore(ctx, &conversations.Config{
		StoreType: "sqlite",
		BasePath:  basePath,
	})
	require.NoError(t, err)
	defer func() {
		_ = store.Close()
	}()

	conversationID := convtypes.GenerateID()
	err = store.Save(ctx, convtypes.ConversationRecord{
		ID:          conversationID,
		Provider:    "anthropic",
		RawMessages: []byte(`[]`),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata: map[string]any{
			"profile": "default",
		},
	})
	require.NoError(t, err)

	cmd := &cobra.Command{Use: "run"}
	config, _, err := loadResumeConversationConfig(ctx, cmd, conversationID, "")
	require.NoError(t, err)

	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "base-model", config.Model)
	assert.Equal(t, "default", config.Profile)
}

func TestApplyRunToolRestrictions_NoToolsWinsOverFragmentAllowedTools(t *testing.T) {
	llmConfig := llmtypes.Config{}
	fragmentMetadata := &fragments.Metadata{
		AllowedTools: []string{"bash", "file_read"},
	}

	applyRunToolRestrictions(&llmConfig, fragmentMetadata, true)

	assert.Equal(t, []string{tools.NoToolsMarker}, llmConfig.AllowedTools)
}

func TestProcessFragmentBuildsMessageDisplay(t *testing.T) {
	config := NewRunConfig()
	config.FragmentName = "github/pr"
	config.FragmentArgs = map[string]string{"target": "develop"}

	query, display, metadata, err := processFragment(context.Background(), config, []string{"focus", "tests"}, nil)

	require.NoError(t, err)
	require.NotNil(t, metadata)
	assert.Contains(t, query, "focus tests")
	assert.Equal(t, "/github/pr target=develop focus tests", display)
}

func TestProcessFragmentRoutesExtensionRecipeWithArgs(t *testing.T) {
	rootDir := t.TempDir()
	writeRunExtensionExecutable(t, filepath.Join(rootDir, "reviewer", "kodelet-extension-reviewer"))
	runtime, err := extensions.NewRuntime(
		context.Background(),
		extensions.WithConfig(extensions.DefaultConfig()),
		extensions.WithWorkingDir(rootDir),
		extensions.WithRoots(extensions.Root{Dir: rootDir, Kind: extensions.SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	config := NewRunConfig()
	config.FragmentName = "review"
	config.FragmentArgs = map[string]string{"target": "main"}

	query, display, metadata, err := processFragment(context.Background(), config, []string{"focus", "tests"}, runtime)

	require.NoError(t, err)
	require.NotNil(t, metadata)
	assert.Equal(t, "Review main with focus tests", query)
	assert.Equal(t, "/review target=main focus tests", display)
	assert.Equal(t, "review", metadata.Name)
	assert.Equal(t, "Run extension review", metadata.Description)
}

func TestFormatFragmentDisplayArgs(t *testing.T) {
	assert.Equal(t, `draft=true title="my feature"`, formatFragmentDisplayArgs(map[string]string{
		"title": "my feature",
		"draft": "true",
	}))
	assert.Equal(t, "", formatFragmentDisplayArgs(nil))
	assert.Equal(t, "b=2 c=3", formatFragmentDisplayArgs(map[string]string{"": "ignored", "c": "3", "b": "2"}))
}

func TestCreateRunToolManagers_NoToolsSkipsToolInitialization(t *testing.T) {
	mcpManager, extensionRuntime, err := createRunToolManagers(context.Background(), &RunConfig{NoTools: true})

	require.NoError(t, err)
	assert.Nil(t, mcpManager)
	assert.Nil(t, extensionRuntime)
}

func TestCreateRunToolManagersSkipsExtensionsWhenDisabled(t *testing.T) {
	originalSettings := viper.AllSettings()
	originalConfigFile := viper.ConfigFileUsed()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		if originalConfigFile != "" {
			viper.SetConfigFile(originalConfigFile)
			_ = viper.ReadInConfig()
		}
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})

	mcpManager, extensionRuntime, err := createRunToolManagers(context.Background(), &RunConfig{NoMCP: true, NoExtensions: true})

	require.NoError(t, err)
	assert.Nil(t, mcpManager)
	assert.Nil(t, extensionRuntime)
}

func TestCreateRunToolManagersTreatsDisabledMCPAsNil(t *testing.T) {
	originalSettings := viper.AllSettings()
	originalConfigFile := viper.ConfigFileUsed()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		if originalConfigFile != "" {
			viper.SetConfigFile(originalConfigFile)
			_ = viper.ReadInConfig()
		}
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})

	viper.Set("mcp.enabled", false)
	config := NewRunConfig()
	config.NoExtensions = true
	mcpManager, extensionRuntime, err := createRunToolManagers(context.Background(), config)

	require.NoError(t, err)
	assert.Nil(t, mcpManager)
	assert.Nil(t, extensionRuntime)
}

func TestAddRunMessageDisplay(t *testing.T) {
	thread := newFakeRunThread()
	config := NewRunConfig()
	config.MessageDisplay = " /commit short=true "
	config.FragmentName = "commit"

	addRunMessageDisplay(thread, "model-facing prompt", config)

	display, ok := conversations.LookupMessageDisplay(thread.metadata, "model-facing prompt")
	require.True(t, ok)
	assert.Equal(t, "/commit short=true", display.Text)
	assert.Equal(t, "commit", display.Command)
}

func TestAddRunMessageDisplaySkipsBlankInputs(t *testing.T) {
	thread := newFakeRunThread()
	config := NewRunConfig()
	config.MessageDisplay = "display"

	addRunMessageDisplay(thread, " ", config)
	assert.Empty(t, thread.metadata)

	config.MessageDisplay = " "
	addRunMessageDisplay(thread, "query", config)
	assert.Empty(t, thread.metadata)
}

func TestAddRunGoalDisplay(t *testing.T) {
	thread := newFakeRunThread()
	update, handled, err := goals.ParseSlashCommand("goal", "ship coverage", time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.True(t, handled)

	addRunGoalDisplay(thread, &update)

	assert.Equal(t, update.Goal, thread.metadata[goals.MetadataKey])
	display, ok := conversations.LookupMessageDisplay(thread.metadata, update.ModelPrompt)
	require.True(t, ok)
	assert.Equal(t, update.Display, display.Text)
	assert.Equal(t, goals.SlashCommandName, display.Command)

	addRunGoalDisplay(nil, &update)
	addRunGoalDisplay(thread, nil)
}

func TestApplyFragmentRestrictions(t *testing.T) {
	t.Run("applies valid restrictions", func(t *testing.T) {
		config := llmtypes.Config{}
		applyFragmentRestrictions(&config, &fragments.Metadata{
			AllowedTools:    []string{"bash", "file_read"},
			AllowedCommands: []string{"go test ./..."},
		})

		assert.Equal(t, []string{"bash", "file_read"}, config.AllowedTools)
		assert.Equal(t, []string{"go test ./..."}, config.AllowedCommands)
	})

	t.Run("ignores invalid tools but applies commands", func(t *testing.T) {
		config := llmtypes.Config{AllowedTools: []string{"existing"}}
		applyFragmentRestrictions(&config, &fragments.Metadata{
			AllowedTools:    []string{"not-a-real-tool"},
			AllowedCommands: []string{"ls"},
		})

		assert.Equal(t, []string{"existing"}, config.AllowedTools)
		assert.Equal(t, []string{"ls"}, config.AllowedCommands)
	})

	t.Run("nil metadata is no-op", func(t *testing.T) {
		config := llmtypes.Config{AllowedTools: []string{"existing"}}
		applyFragmentRestrictions(&config, nil)
		assert.Equal(t, []string{"existing"}, config.AllowedTools)
	})
}

func TestNormalizeConversationProfile(t *testing.T) {
	assert.Equal(t, "", normalizeConversationProfile(""))
	assert.Equal(t, "", normalizeConversationProfile(" default "))
	assert.Equal(t, "work", normalizeConversationProfile(" work "))
}

func TestGetRunConfigFromFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "run"}
	defaults := NewRunConfig()
	cmd.Flags().String("resume", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-save", defaults.NoSave, "")
	cmd.Flags().Bool("headless", defaults.Headless, "")
	cmd.Flags().Bool("stream-deltas", defaults.StreamDeltas, "")
	cmd.Flags().StringSliceP("image", "I", defaults.Images, "")
	cmd.Flags().Int("max-turns", defaults.MaxTurns, "")
	cmd.Flags().StringP("recipe", "r", defaults.FragmentName, "")
	cmd.Flags().StringToString("arg", defaults.FragmentArgs, "")
	cmd.Flags().StringSlice("fragment-dirs", defaults.FragmentDirs, "")
	cmd.Flags().Bool("include-history", defaults.IncludeHistory, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-mcp", defaults.NoMCP, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")
	cmd.Flags().Bool("disable-fs-search-tools", defaults.DisableFSSearchTools, "")
	cmd.Flags().Bool("disable-subagent", defaults.DisableSubagent, "")
	cmd.Flags().String("sysprompt", defaults.Sysprompt, "")
	cmd.Flags().StringToString("sysprompt-arg", defaults.SyspromptArgs, "")
	cmd.Flags().Bool("result-only", defaults.ResultOnly, "")
	cmd.Flags().Bool("use-weak-model", defaults.UseWeakModel, "")
	cmd.Flags().String("account", defaults.Account, "")
	cmd.Flags().Bool("as-subagent", defaults.AsSubagent, "")

	require.NoError(t, cmd.Flags().Set("resume", "conv-1"))
	require.NoError(t, cmd.Flags().Set("cwd", " /tmp/project "))
	require.NoError(t, cmd.Flags().Set("no-save", "false"))
	require.NoError(t, cmd.Flags().Set("stream-deltas", "true"))
	require.NoError(t, cmd.Flags().Set("headless", "true"))
	require.NoError(t, cmd.Flags().Set("image", "a.png,b.png"))
	require.NoError(t, cmd.Flags().Set("max-turns", "-5"))
	require.NoError(t, cmd.Flags().Set("recipe", "commit"))
	require.NoError(t, cmd.Flags().Set("arg", "short=true,target=main"))
	require.NoError(t, cmd.Flags().Set("fragment-dirs", "recipes,more-recipes"))
	require.NoError(t, cmd.Flags().Set("include-history", "true"))
	require.NoError(t, cmd.Flags().Set("no-extensions", "true"))
	require.NoError(t, cmd.Flags().Set("no-mcp", "true"))
	require.NoError(t, cmd.Flags().Set("no-tools", "true"))
	require.NoError(t, cmd.Flags().Set("disable-fs-search-tools", "true"))
	require.NoError(t, cmd.Flags().Set("disable-subagent", "true"))
	require.NoError(t, cmd.Flags().Set("sysprompt", "prompt.md"))
	require.NoError(t, cmd.Flags().Set("sysprompt-arg", "project=kodelet"))
	require.NoError(t, cmd.Flags().Set("result-only", "true"))
	require.NoError(t, cmd.Flags().Set("use-weak-model", "true"))
	require.NoError(t, cmd.Flags().Set("account", "work"))
	require.NoError(t, cmd.Flags().Set("as-subagent", "true"))

	config := getRunConfigFromFlags(context.Background(), cmd)

	assert.Equal(t, "conv-1", config.ResumeConvID)
	assert.Equal(t, "/tmp/project", config.CWD)
	assert.False(t, config.NoSave)
	assert.True(t, config.Headless)
	assert.True(t, config.StreamDeltas)
	assert.Equal(t, []string{"a.png", "b.png"}, config.Images)
	assert.Equal(t, 0, config.MaxTurns)
	assert.Equal(t, "commit", config.FragmentName)
	assert.Equal(t, map[string]string{"short": "true", "target": "main"}, config.FragmentArgs)
	assert.Equal(t, []string{"recipes", "more-recipes"}, config.FragmentDirs)
	assert.True(t, config.IncludeHistory)
	assert.True(t, config.NoExtensions)
	assert.True(t, config.NoMCP)
	assert.True(t, config.NoTools)
	assert.True(t, config.DisableFSSearchTools)
	assert.True(t, config.DisableSubagent)
	assert.Equal(t, "prompt.md", config.Sysprompt)
	assert.Equal(t, map[string]string{"project": "kodelet"}, config.SyspromptArgs)
	assert.True(t, config.ResultOnly)
	assert.True(t, config.UseWeakModel)
	assert.Equal(t, "work", config.Account)
	assert.True(t, config.AsSubagent)
}

type fakeRunThread struct {
	metadata map[string]any
}

func newFakeRunThread() *fakeRunThread {
	return &fakeRunThread{metadata: make(map[string]any)}
}

func (f *fakeRunThread) SetState(tooltypes.State)                          {}
func (f *fakeRunThread) GetState() tooltypes.State                         { return nil }
func (f *fakeRunThread) AddUserMessage(context.Context, string, ...string) {}
func (f *fakeRunThread) SendMessage(context.Context, string, llmtypes.MessageHandler, llmtypes.MessageOpt) (string, error) {
	return "", nil
}
func (f *fakeRunThread) GetUsage() llmtypes.Usage                     { return llmtypes.Usage{} }
func (f *fakeRunThread) GetConversationID() string                    { return "conv" }
func (f *fakeRunThread) SetConversationID(string)                     {}
func (f *fakeRunThread) SaveConversation(context.Context, bool) error { return nil }
func (f *fakeRunThread) IsPersisted() bool                            { return false }
func (f *fakeRunThread) EnablePersistence(context.Context, bool)      {}
func (f *fakeRunThread) Provider() string                             { return "fake" }
func (f *fakeRunThread) GetMessages() ([]llmtypes.Message, error)     { return nil, nil }
func (f *fakeRunThread) GetConfig() llmtypes.Config                   { return llmtypes.Config{} }
func (f *fakeRunThread) AggregateSubagentUsage(llmtypes.Usage)        {}
func (f *fakeRunThread) SetMetadataValue(key string, value any)       { f.metadata[key] = value }
func (f *fakeRunThread) GetMetadata() map[string]any                  { return f.metadata }

func writeRunExtensionExecutable(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\nKODELET_RUN_TEST_EXTENSION_HELPER=1 exec %q -test.run TestRunExtensionHelperProcess --\n", executable)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func TestRunExtensionHelperProcess(t *testing.T) {
	t.Helper()
	if os.Getenv("KODELET_RUN_TEST_EXTENSION_HELPER") != "1" {
		return
	}
	runRunExtensionHelperProcess()
	os.Exit(0)
}

func runRunExtensionHelperProcess() {
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := extensionsReadFrame(reader)
		if err != nil {
			return
		}

		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			extensionsWriteRPCResponse(request.ID, nil, map[string]any{"code": -32700, "message": err.Error()})
			continue
		}

		switch request.Method {
		case "extension.initialize":
			extensionsWriteRPCResponse(request.ID, extensions.InitializeResult{
				Name:    "reviewer",
				Version: "0.1.0",
				Commands: []extensions.CommandRegistration{{
					Name:        "review",
					Aliases:     []string{"/review"},
					Description: "Run extension review",
					Kind:        "recipe",
				}},
			}, nil)
		case "extension.command.execute":
			var params struct {
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				extensionsWriteRPCResponse(request.ID, nil, map[string]any{"code": -32602, "message": err.Error()})
				continue
			}
			target, _ := params.Input["target"].(string)
			text, _ := params.Input["text"].(string)
			if target == "" {
				target = "HEAD"
			}
			prompt := "Review " + target
			if text != "" {
				prompt += " with " + text
			}
			extensionsWriteRPCResponse(request.ID, extensions.CommandResult{Action: extensions.CommandActionRunAgent, Prompt: prompt, RecipeName: "review"}, nil)
		default:
			extensionsWriteRPCResponse(request.ID, nil, map[string]any{"code": -32601, "message": "method not found"})
		}
	}
}

func extensionsReadFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &contentLength)
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func extensionsWriteRPCResponse(id int64, result any, rpcErr map[string]any) {
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	payload, _ := json.Marshal(response)
	fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func withPipeStdin[T any](t *testing.T, input string, f func() T) T {
	t.Helper()

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = r.Close()
	})

	return f()
}

func withDevNullStdin[T any](t *testing.T, f func() T) T {
	t.Helper()

	oldStdin := os.Stdin
	devNull, err := os.Open(os.DevNull)
	require.NoError(t, err)
	os.Stdin = devNull
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = devNull.Close()
	})

	return f()
}
