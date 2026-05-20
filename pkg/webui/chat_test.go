package webui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/fragments"
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
		"codex": map[string]any{
			"provider": "openai",
			"model":    "gpt-5.5",
			"openai": map[string]any{
				"platform": "codex",
				"api_mode": "responses",
			},
		},
	})

	config, err := resolveWebChatConfigForExistingConversation(&conversations.GetConversationResponse{
		ID:       "conv-123",
		Provider: "openai",
		Metadata: map[string]any{
			"profile":      "codex",
			"model":        "gpt-5.5",
			"platform":     "codex",
			"api_mode":     "responses",
			"service_tier": "fast",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "codex", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-5.5", config.Model)
	require.NotNil(t, config.OpenAI)
	assert.Equal(t, "codex", config.OpenAI.Platform)
	assert.Equal(t, llmtypes.OpenAIAPIMode("responses"), config.OpenAI.APIMode)
	assert.Equal(t, llmtypes.OpenAIServiceTierFast, config.OpenAI.ServiceTier)
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
	viper.Set("model", "gpt-5.5")
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
	assert.Equal(t, "gpt-5.5", config.Model)
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
	viper.Set("model", "gpt-5.5")
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
	assert.Equal(t, "gpt-5.5", config.Model)
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
	viper.Set("model", "gpt-5.5")

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

func TestChatMessageHandler_HandleUserMessageEmitsRenderableContent(t *testing.T) {
	sink := &recordingChatSink{}
	handler := &chatMessageHandler{
		conversationID: "conv-123",
		sink:           sink,
	}

	handler.HandleUserMessage("Use this screenshot", []string{"data:image/png;base64,aGVsbG8="})

	require.Len(t, sink.events, 1)
	assert.Equal(t, "user-message", sink.events[0].Kind)
	assert.Equal(t, "user", sink.events[0].Role)

	blocks, ok := sink.events[0].Content.([]WebContentBlock)
	require.True(t, ok)
	require.Len(t, blocks, 2)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "Use this screenshot", blocks[0].Text)
	assert.Equal(t, "image", blocks[1].Type)
	require.NotNil(t, blocks[1].Source)
	assert.Equal(t, "image/png", blocks[1].Source.MediaType)
	assert.Equal(t, "aGVsbG8=", blocks[1].Source.Data)
}

func TestExpandWebChatSlashCommandUsesResolvedCWD(t *testing.T) {
	workspace := t.TempDir()
	writeWebChatRecipe(t, workspace, "limited", `---
description: Limited recipe
allowed_tools:
  - bash
allowed_commands:
  - git status
---
Hello {{.name}}
`)

	prompt, expansion, err := expandWebChatSlashCommand(context.Background(), "/limited name=Web check this", workspace)

	require.NoError(t, err)
	require.NotNil(t, expansion)
	assert.Contains(t, prompt, "Hello Web")
	assert.Contains(t, prompt, "Additional instructions:\ncheck this")
	assert.Equal(t, []string{"bash"}, expansion.Metadata.AllowedTools)
	assert.Equal(t, []string{"git status"}, expansion.Metadata.AllowedCommands)
}

func TestWebSlashCommandRestrictionsApplyBeforeBuildingState(t *testing.T) {
	metadata := expandLimitedWebChatRecipe(t)

	config := llmtypes.Config{}
	applyWebFragmentRestrictions(context.Background(), &config, metadata)
	assert.Equal(t, []string{"bash"}, config.AllowedTools)
	assert.Equal(t, []string{"git status"}, config.AllowedCommands)

	state, err := buildChatState(context.Background(), config, "session-1", t.TempDir(), nil, nil)
	require.NoError(t, err)

	var toolNames []string
	for _, tool := range state.Tools() {
		toolNames = append(toolNames, tool.Name())
	}
	assert.Contains(t, toolNames, "bash")
	assert.NotContains(t, toolNames, "subagent")
	assert.NotContains(t, toolNames, "file_write")
	assert.NotContains(t, toolNames, "file_edit")
	assert.NotContains(t, toolNames, "web_fetch")
	assert.NotContains(t, toolNames, "view_image")
	assert.NotContains(t, toolNames, "skill")
}

func expandLimitedWebChatRecipe(t *testing.T) *fragments.Metadata {
	t.Helper()

	workspace := t.TempDir()
	writeWebChatRecipe(t, workspace, "limited", `---
allowed_tools:
  - bash
allowed_commands:
  - git status
---
Restricted prompt
`)

	_, expansion, err := expandWebChatSlashCommand(context.Background(), "/limited", workspace)
	require.NoError(t, err)
	require.NotNil(t, expansion)
	return &expansion.Metadata
}

func writeWebChatRecipe(t *testing.T, workspace, name, content string) {
	t.Helper()

	recipeDir := filepath.Join(workspace, ".kodelet", "recipes")
	require.NoError(t, os.MkdirAll(recipeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipeDir, name+".md"), []byte(content), 0o644))
}
