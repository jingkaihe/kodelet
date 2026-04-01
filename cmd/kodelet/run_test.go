package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		"premium": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
			"openai": map[string]any{
				"platform": "openai",
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
			"profile":  "premium",
			"model":    "accounts/fireworks/models/kimi-k2",
			"platform": "fireworks",
			"api_mode": "responses",
		},
	})
	require.NoError(t, err)

	cmd := &cobra.Command{Use: "run"}
	config, resolvedCWD, err := loadResumeConversationConfig(ctx, cmd, conversationID, "")
	require.NoError(t, err)

	assert.Equal(t, "premium", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "accounts/fireworks/models/kimi-k2", config.Model)
	assert.NotEmpty(t, resolvedCWD)
	require.NotNil(t, config.OpenAI)
	assert.Equal(t, "fireworks", config.OpenAI.Platform)
	assert.Equal(t, llmtypes.OpenAIAPIMode("responses"), config.OpenAI.APIMode)
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
	viper.Set("model", "gpt-5.4")

	cmd := &cobra.Command{Use: "run"}
	config, resolvedCWD, err := loadResumeConversationConfig(context.Background(), cmd, "", "")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-5.4", config.Model)
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
		"premium": map[string]any{
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
			"profile": "premium",
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

	assert.Equal(t, "premium", config.Profile)
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
	viper.Set("profile", "premium")
	viper.Set("profiles", map[string]any{
		"premium": map[string]any{
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
