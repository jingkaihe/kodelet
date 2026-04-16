package webui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingChatSink struct {
	events []ChatEvent
}

func (s *recordingChatSink) Send(event ChatEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestBuildChatState_BindsTodoPathToConversationID(t *testing.T) {
	customManager, err := tools.NewCustomToolManager()
	require.NoError(t, err)

	state, err := buildChatState(
		context.Background(),
		llmtypes.Config{
			DisableSubagent: true,
		},
		"conv-web-123",
		"/workspace/project",
		nil,
		customManager,
	)
	require.NoError(t, err)

	todoPath, err := state.TodoFilePath()
	require.NoError(t, err)

	assert.Equal(t, "conv-web-123.json", filepath.Base(todoPath))
	assert.Equal(t, "todos", filepath.Base(filepath.Dir(todoPath)))
}

func TestResolveWebChatConfigForExistingConversation_UsesStoredProfileAndMetadata(t *testing.T) {
	originalSettings := viper.AllSettings()
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
		"anthropic": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
			"openai": map[string]any{
				"platform": "openai",
			},
		},
	})

	config, err := resolveWebChatConfigForExistingConversation(&conversations.GetConversationResponse{
		ID:       "conv-123",
		Provider: "openai",
		Metadata: map[string]any{
			"profile":  "anthropic",
			"model":    "accounts/fireworks/models/kimi-k2",
			"platform": "fireworks",
			"api_mode": "responses",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "accounts/fireworks/models/kimi-k2", config.Model)
	require.NotNil(t, config.OpenAI)
	assert.Equal(t, "fireworks", config.OpenAI.Platform)
	assert.Equal(t, llmtypes.OpenAIAPIMode("responses"), config.OpenAI.APIMode)
}

func TestResolveWebChatConfigForNewConversation_DefaultProfileNameIgnoresActiveProfile(t *testing.T) {
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
	viper.Set("profile", "work")
	viper.Set("profiles", map[string]any{
		"work": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
		},
	})

	config, err := resolveWebChatConfigForNewConversation("default")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-5.4", config.Model)
	assert.Equal(t, "default", config.Profile)
}

func TestResolveWebChatConfigForExistingConversation_DefaultProfileIgnoresActiveProfile(t *testing.T) {
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
	viper.Set("profile", "work")
	viper.Set("profiles", map[string]any{
		"work": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
		},
	})

	config, err := resolveWebChatConfigForExistingConversation(&conversations.GetConversationResponse{
		ID:       "conv-default",
		Provider: "openai",
		Metadata: map[string]any{
			"profile": "default",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-5.4", config.Model)
	assert.Equal(t, "default", config.Profile)
}

func TestResolveWebChatConfig_ResolvesRelativeCWDFromDefaultWorkspace(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	rootDir := t.TempDir()
	backendDir := filepath.Join(rootDir, "backend")
	require.NoError(t, os.Mkdir(backendDir, 0o755))

	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-5.4")

	config, resolvedCWD, err := resolveWebChatConfig(
		context.Background(),
		"",
		"default",
		"backend",
		rootDir,
	)
	require.NoError(t, err)
	assert.Equal(t, backendDir, resolvedCWD)
	assert.Equal(t, backendDir, config.WorkingDirectory)
}

func TestChatMessageHandler_HandleUsageDeduplicatesSnapshots(t *testing.T) {
	sink := &recordingChatSink{}
	handler := &chatMessageHandler{
		conversationID: "conv-123",
		sink:           sink,
	}

	usage := llmtypes.Usage{InputTokens: 100, OutputTokens: 50}
	handler.HandleUsage(usage)
	handler.HandleUsage(usage)
	handler.HandleText("done")

	require.Len(t, sink.events, 2)
	assert.Equal(t, "usage", sink.events[0].Kind)
	if assert.NotNil(t, sink.events[0].Usage) {
		assert.Equal(t, usage, *sink.events[0].Usage)
	}
	assert.Equal(t, "text", sink.events[1].Kind)
}

func TestChatMessageHandler_HandleToolResultBackfillsToolName(t *testing.T) {
	sink := &recordingChatSink{}
	handler := &chatMessageHandler{
		conversationID: "conv-123",
		sink:           sink,
	}

	handler.HandleToolResult("tool-1", "bash", tooltypes.BaseToolResult{Result: "ok"})

	require.Len(t, sink.events, 1)
	assert.Equal(t, "tool-result", sink.events[0].Kind)
	if assert.NotNil(t, sink.events[0].ToolResult) {
		assert.Equal(t, "bash", sink.events[0].ToolResult.ToolName)
	}
}
