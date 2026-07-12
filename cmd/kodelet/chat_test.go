package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/tui"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChatConfigFromFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "chat"}
	defaults := NewChatConfig()
	cmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().String("theme", tui.DefaultThemeName, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")

	require.NoError(t, cmd.Flags().Set("resume", "conv-1"))
	require.NoError(t, cmd.Flags().Set("cwd", " /tmp/project "))
	require.NoError(t, cmd.Flags().Set("theme", " tokyo-night "))
	require.NoError(t, cmd.Flags().Set("no-extensions", "true"))
	require.NoError(t, cmd.Flags().Set("no-tools", "true"))

	config := getChatConfigFromFlags(context.Background(), cmd)

	assert.Equal(t, "conv-1", config.ResumeConvID)
	assert.Equal(t, "/tmp/project", config.CWD)
	assert.Equal(t, "tokyo-night", config.Theme)
	assert.True(t, config.NoExtensions)
	assert.True(t, config.NoTools)
}

func TestChatResumeShortFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "chat"}
	defaults := NewChatConfig()
	cmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().String("theme", tui.DefaultThemeName, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")

	require.NoError(t, cmd.ParseFlags([]string{"-r", "conv-short"}))

	config := getChatConfigFromFlags(context.Background(), cmd)
	assert.Equal(t, "conv-short", config.ResumeConvID)
}

func TestChatNoToolsFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "chat"}
	defaults := NewChatConfig()
	cmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().String("theme", tui.DefaultThemeName, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")

	require.NoError(t, cmd.Flags().Set("no-tools", "true"))

	config := getChatConfigFromFlags(context.Background(), cmd)
	assert.True(t, config.NoTools)
}

func TestChatNoToolsDisablesExtensionStartup(t *testing.T) {
	originalSettings := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})

	applyChatRuntimeRestrictions(&ChatConfig{NoTools: true})

	assert.False(t, viper.GetBool("extensions.enabled"))
	assert.Equal(t, []string{"none"}, viper.GetStringSlice("allowed_tools"))
	assert.False(t, extensions.LoadConfigFromViper().Enabled)
}

func TestValidateChatResumeConversationRejectsMissingConversation(t *testing.T) {
	setupChatConversationStore(t)

	err := validateChatResumeConversation(context.Background(), "missing-conversation")

	require.Error(t, err)
	assert.ErrorContains(t, err, "conversation not found: missing-conversation")
}

func TestValidateChatResumeConversationAcceptsExistingConversation(t *testing.T) {
	basePath := setupChatConversationStore(t)
	ctx := context.Background()
	store, err := conversations.NewConversationStore(ctx, &conversations.Config{
		StoreType: "sqlite",
		BasePath:  basePath,
	})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	record := convtypes.NewConversationRecord("conversation-123")
	record.Provider = "openai"
	record.UpdatedAt = time.Now()
	require.NoError(t, store.Save(ctx, record))

	require.NoError(t, validateChatResumeConversation(ctx, " conversation-123 "))
}

func TestValidateChatResumeConversationRejectsReasoningConflict(t *testing.T) {
	basePath := setupChatConversationStore(t)
	ctx := context.Background()
	store, err := conversations.NewConversationStore(ctx, &conversations.Config{
		StoreType: "sqlite",
		BasePath:  basePath,
	})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	metadata, err := conversations.AddConfigSnapshot(nil, llmtypes.Config{
		Provider:        "openai",
		Model:           "gpt-5",
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	record := convtypes.NewConversationRecord("conversation-reasoning")
	record.Provider = "openai"
	record.Metadata = metadata
	require.NoError(t, store.Save(ctx, record))

	require.NoError(t, validateChatResumeConversation(ctx, record.ID, "high"))
	err = validateChatResumeConversation(ctx, record.ID, "low")
	require.ErrorContains(t, err, "locked to \"high\"")

	legacy := convtypes.NewConversationRecord("legacy-conversation-reasoning")
	legacy.Provider = "openai"
	legacy.Metadata = map[string]any{"model": "gpt-4.1"}
	require.NoError(t, store.Save(ctx, legacy))
	err = validateChatResumeConversation(ctx, legacy.ID, "high")
	require.ErrorContains(t, err, "legacy conversation without config_snapshot")
}

func TestValidateChatResumeConversationAllowsEmptyConversation(t *testing.T) {
	require.NoError(t, validateChatResumeConversation(context.Background(), "   "))
}

func setupChatConversationStore(t *testing.T) string {
	t.Helper()
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	ctx := context.Background()
	dbPath := filepath.Join(basePath, "storage.db")
	sqlDB, err := db.Open(ctx, dbPath)
	require.NoError(t, err)
	runner := db.NewMigrationRunner(sqlDB)
	require.NoError(t, runner.Run(ctx, migrations.All()))
	require.NoError(t, sqlDB.Close())

	return basePath
}
