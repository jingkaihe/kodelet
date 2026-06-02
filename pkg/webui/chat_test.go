package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
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

func TestTransformWebChatSlashCommandHandlesGoal(t *testing.T) {
	prompt, expansion, goalUpdate, err := transformWebChatSlashCommand(context.Background(), "/goal find server cores and ram", t.TempDir())

	require.NoError(t, err)
	assert.Nil(t, expansion)
	require.NotNil(t, goalUpdate)
	assert.Contains(t, prompt, "<goal_context>")
	assert.Contains(t, prompt, "find server cores and ram")
	assert.Equal(t, "Objective: find server cores and ram", goalUpdate.Display)
}

func TestTransformWebChatSlashCommandIfNeededSkipsExtensionPrompt(t *testing.T) {
	prompt, expansion, goalUpdate, err := transformWebChatSlashCommandIfNeeded(context.Background(), "/tmp/path/from-extension", t.TempDir(), false)

	require.NoError(t, err)
	assert.Equal(t, "/tmp/path/from-extension", prompt)
	assert.Nil(t, expansion)
	assert.Nil(t, goalUpdate)
}

func TestTryWebExtensionCommandRoutesCommand(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "commands")
	writeWebExtensionExecutable(t, filepath.Join(extDir, "kodelet-extension-commands"))

	runtime, err := extensions.NewRuntime(
		context.Background(),
		extensions.WithConfig(extensions.DefaultConfig()),
		extensions.WithWorkingDir(rootDir),
		extensions.WithRoots(extensions.Root{Dir: rootDir, Kind: extensions.SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	result, handled, err := tryWebExtensionCommand(
		context.Background(),
		"/doctor verbose=true",
		runtime,
		llmtypes.Config{Provider: "anthropic", Model: "claude-test"},
		"conv-web",
		rootDir,
	)

	require.NoError(t, err)
	assert.True(t, handled)
	require.NotNil(t, result)
	assert.Equal(t, extensions.CommandActionRespond, result.Action)
	assert.Equal(t, "All extensions are healthy for conv-web.", result.Response)
}

func writeWebExtensionExecutable(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\nKODELET_WEBUI_TEST_EXTENSION_HELPER=1 exec %q -test.run TestWebExtensionHelperProcess --\n", executable)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func TestWebExtensionHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_WEBUI_TEST_EXTENSION_HELPER") != "1" {
		return
	}
	runWebExtensionHelperProcess()
	os.Exit(0)
}

func runWebExtensionHelperProcess() {
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readWebRPCFrame(reader)
		if err != nil {
			return
		}

		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			writeWebRPCResponse(request.ID, nil, map[string]any{"code": -32700, "message": err.Error()})
			continue
		}

		switch request.Method {
		case "extension.initialize":
			writeWebRPCResponse(request.ID, extensions.InitializeResult{
				Name: "commands",
				Commands: []extensions.CommandRegistration{{
					Name:        "doctor",
					Aliases:     []string{"/doctor"},
					Description: "Inspect extension runtime health",
				}, {
					Name:        "review",
					Aliases:     []string{"/review"},
					Description: "Review local git changes",
					Kind:        "recipe",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"target": map[string]any{"type": "string", "default": "HEAD"},
							"focus":  map[string]any{"type": "string", "default": "correctness, tests"},
						},
					},
				}},
			}, nil)
		case "extension.command.execute":
			var params struct {
				Name    string                          `json:"name"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeWebRPCResponse(request.ID, nil, map[string]any{"code": -32602, "message": err.Error()})
				continue
			}
			writeWebRPCResponse(request.ID, extensions.CommandResult{
				Action:   extensions.CommandActionRespond,
				Response: fmt.Sprintf("All extensions are healthy for %s.", params.Context.ConversationID),
			}, nil)
		default:
			writeWebRPCResponse(request.ID, nil, map[string]any{"code": -32601, "message": "method not found"})
		}
	}
}

func readWebRPCFrame(reader *bufio.Reader) ([]byte, error) {
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
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &contentLength)
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	payload := make([]byte, contentLength)
	_, err := reader.Read(payload)
	return payload, err
}

func writeWebRPCResponse(id int64, result any, rpcErr any) {
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	payload, _ := json.Marshal(response)
	fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
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
