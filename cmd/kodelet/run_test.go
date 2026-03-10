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
	config, err := loadResumeConversationConfig(ctx, cmd, conversationID)
	require.NoError(t, err)

	assert.Equal(t, "premium", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "accounts/fireworks/models/kimi-k2", config.Model)
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
	viper.Set("provider", "anthropic")
	viper.Set("model", "claude-sonnet-4-6")

	cmd := &cobra.Command{Use: "run"}
	config, err := loadResumeConversationConfig(context.Background(), cmd, "")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "claude-sonnet-4-6", config.Model)
}
