package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	conversationsqlite "github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockConversationService struct {
	getFunc   func(ctx context.Context, id string) (*conversations.GetConversationResponse, error)
	closeFunc func() error
}

func (m *mockConversationService) ListConversations(context.Context, *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
	return &conversations.ListConversationsResponse{}, nil
}

func (m *mockConversationService) GetConversation(ctx context.Context, id string) (*conversations.GetConversationResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, id)
	}
	return &conversations.GetConversationResponse{}, nil
}

func (m *mockConversationService) DeleteConversation(context.Context, string) error {
	return nil
}

func (m *mockConversationService) ForkConversation(context.Context, string) (*conversations.GetConversationResponse, error) {
	return &conversations.GetConversationResponse{}, nil
}

func (m *mockConversationService) GetToolResult(context.Context, string, string) (*conversations.GetToolResultResponse, error) {
	return &conversations.GetToolResultResponse{}, nil
}

func (m *mockConversationService) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

type fakeExtensionRuntimeProvider struct {
	runtime *extensions.Runtime
	calls   int
}

func (p *fakeExtensionRuntimeProvider) Runtime(context.Context, string) (*extensions.Runtime, error) {
	p.calls++
	return p.runtime, nil
}

type fakeContextualExtensionRuntimeProvider struct {
	fakeExtensionRuntimeProvider
	contextualCalls int
	cwd             string
	callContext     extensions.ExtensionCallContext
	err             error
}

func (p *fakeContextualExtensionRuntimeProvider) RuntimeWithCallContext(_ context.Context, cwd string, callContext extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	p.contextualCalls++
	p.cwd = cwd
	p.callContext = callContext
	return p.runtime, p.err
}

type fakeMetadataThread struct {
	metadata       map[string]any
	conversationID string
	persisted      bool
	saveCalls      int
	sendCalls      int
	userMessages   []string
	assistantMsgs  []string
	closed         bool
	extensions     any
	environment    agentenv.Environment
}

func (f *fakeMetadataThread) SetState(tooltypes.State) {}

func (f *fakeMetadataThread) GetState() tooltypes.State { return nil }

func (f *fakeMetadataThread) AddUserMessage(_ context.Context, message string, _ ...string) {
	f.userMessages = append(f.userMessages, message)
}

func (f *fakeMetadataThread) AddAssistantMessage(_ context.Context, message string) {
	f.assistantMsgs = append(f.assistantMsgs, message)
}

func (f *fakeMetadataThread) SendMessage(context.Context, string, llmtypes.MessageHandler, llmtypes.MessageOpt) (string, error) {
	f.sendCalls++
	return "", nil
}

func (f *fakeMetadataThread) GetUsage() llmtypes.Usage { return llmtypes.Usage{} }

func (f *fakeMetadataThread) GetConversationID() string { return f.conversationID }

func (f *fakeMetadataThread) SetConversationID(id string) { f.conversationID = id }

func (f *fakeMetadataThread) SaveConversation(context.Context) error {
	f.saveCalls++
	return nil
}

func (f *fakeMetadataThread) IsPersisted() bool { return f.persisted }

func (f *fakeMetadataThread) EnablePersistence(_ context.Context, enabled bool) {
	f.persisted = enabled
}

func (f *fakeMetadataThread) Provider() string { return "" }

func (f *fakeMetadataThread) GetMessages() ([]llmtypes.Message, error) { return nil, nil }

func (f *fakeMetadataThread) GetConfig() llmtypes.Config { return llmtypes.Config{} }

func (f *fakeMetadataThread) AggregateSubagentUsage(llmtypes.Usage) {}

func (f *fakeMetadataThread) SetMetadataValue(key string, value any) {
	if f.metadata == nil {
		f.metadata = make(map[string]any)
	}
	f.metadata[key] = value
}

func (f *fakeMetadataThread) GetMetadata() map[string]any {
	return f.metadata
}

func (f *fakeMetadataThread) Close() error {
	f.closed = true
	return nil
}

func (f *fakeMetadataThread) SetExtensions(runtime any) {
	f.extensions = runtime
}

func (f *fakeMetadataThread) SetEnvironment(environment agentenv.Environment) {
	f.environment = environment
}

func TestNewDefaultChatRunnerStoresDefaultCWD(t *testing.T) {
	runner := NewDefaultChatRunner("/workspace")

	require.NotNil(t, runner)
	assert.Equal(t, "/workspace", runner.defaultCWD)
	assert.Equal(t, "/workspace", runner.DefaultCWD())
	assert.Empty(t, (*DefaultChatRunner)(nil).DefaultCWD())
}

func TestNewDefaultChatRunnerStoresExtensionRuntimeProvider(t *testing.T) {
	provider := &fakeExtensionRuntimeProvider{}
	runner := NewDefaultChatRunner("/workspace", provider)

	require.NotNil(t, runner)
	assert.Same(t, provider, runner.extensionRuntimes)
	assert.Same(t, provider, runner.ExtensionRuntimeProvider())
	assert.Nil(t, (*DefaultChatRunner)(nil).ExtensionRuntimeProvider())
}

type staticEnvironmentResolver struct{}

func (staticEnvironmentResolver) ResolveEnvironment(context.Context, ChatRequest, string, llmtypes.Config, string) (agentenv.Environment, error) {
	return nil, errors.New("environment resolution is not implemented")
}

type recordingEnvironmentResolver struct {
	environment    agentenv.Environment
	request        ChatRequest
	conversationID string
	config         llmtypes.Config
}

func (r *recordingEnvironmentResolver) ResolveEnvironment(_ context.Context, request ChatRequest, conversationID string, config llmtypes.Config, _ string) (agentenv.Environment, error) {
	r.request = request
	r.conversationID = conversationID
	r.config = config
	return r.environment, nil
}

type directCommandEnvironment struct {
	agentenv.Environment
	request  agentenv.CommandRequest
	result   agentenv.CommandResult
	manifest agentenv.Manifest
}

func (e *directCommandEnvironment) IsOpen() bool {
	return strings.TrimSpace(e.manifest.WorkingDirectory) != ""
}
func (e *directCommandEnvironment) Manifest() agentenv.Manifest { return e.manifest }
func (e *directCommandEnvironment) Close(context.Context) error { return nil }
func (e *directCommandEnvironment) ExecuteCommand(_ context.Context, request agentenv.CommandRequest) (agentenv.CommandResult, error) {
	e.request = request
	return e.result, nil
}

func TestDefaultChatRunnerStoresEnvironmentResolver(t *testing.T) {
	runner := NewDefaultChatRunner("")
	resolver := staticEnvironmentResolver{}

	runner.SetEnvironmentResolver(resolver)
	assert.Equal(t, resolver, runner.environmentResolver)
	(*DefaultChatRunner)(nil).SetEnvironmentResolver(resolver)
}

func TestRunDefaultChatPassesConversationContextToRuntimeProvider(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "claude-test")
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	workspace := t.TempDir()
	sentinel := errors.New("stop after runtime acquisition")
	provider := &fakeContextualExtensionRuntimeProvider{err: sentinel}

	conversationID, err := RunDefaultChat(
		context.Background(),
		ChatRequest{Message: "hello", CWD: workspace},
		&recordingChatSink{},
		"",
		provider,
	)

	require.ErrorIs(t, err, sentinel)
	assert.NotEmpty(t, conversationID)
	assert.Equal(t, 1, provider.contextualCalls)
	assert.Zero(t, provider.calls)
	assert.Equal(t, workspace, provider.cwd)
	assert.Equal(t, conversationID, provider.callContext.ConversationID)
	assert.Equal(t, workspace, provider.callContext.CWD)
	assert.Equal(t, "anthropic", provider.callContext.Provider)
	assert.Equal(t, "claude-test", provider.callContext.Model)
	assert.Equal(t, "main", provider.callContext.InvokedBy)
}

func TestResolveExtensionCallContextIncludesRecipeAndPersistedOrigin(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))

	initiator := convtypes.ConversationForkInitiator{
		Type:        convtypes.ConversationForkInitiatorTypeExtensionTool,
		ExtensionID: "review-extension",
		ToolName:    "subagent",
	}
	child := convtypes.ForkConversationRecordWithOptions(
		convtypes.NewConversationRecord("conversation-parent"),
		convtypes.ConversationForkOptions{
			Mode:      convtypes.ConversationForkModeLiveSnapshot,
			Initiator: &initiator,
		},
	)
	child.ID = "conversation-shortcut"
	child.Metadata["recipe_name"] = " persisted-review "
	store, err := conversations.GetConversationStore(t.Context())
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), child))
	require.NoError(t, store.Close())

	callContext, err := ResolveExtensionCallContext(t.Context(), " conversation-shortcut ", " /workspace/project ", llmtypes.Config{
		Provider: " anthropic ",
		Model:    " claude-test ",
		Profile:  " work ",
	})
	require.NoError(t, err)
	assert.Equal(t, extensions.ExtensionCallContext{
		ConversationID: "conversation-shortcut",
		CWD:            "/workspace/project",
		Provider:       "anthropic",
		Model:          "claude-test",
		Profile:        "work",
		RecipeName:     "persisted-review",
		InvokedBy:      "subagent",
	}, callContext)

	configuredContext, err := ResolveExtensionCallContext(t.Context(), "conversation-shortcut", "/workspace/project", llmtypes.Config{RecipeName: "configured-review"})
	require.NoError(t, err)
	assert.Equal(t, "configured-review", configuredContext.RecipeName)
}

func TestResolveExtensionCallContextDefaultsNewConversationOrigin(t *testing.T) {
	callContext, err := ResolveExtensionCallContext(t.Context(), "", "/workspace", llmtypes.Config{RecipeName: "review"})
	require.NoError(t, err)
	assert.Equal(t, "main", callContext.InvokedBy)
	assert.Equal(t, "review", callContext.RecipeName)
}

func TestDefaultChatRunnerExecutesRemoteDirectCommandAndPersistsAffinityMetadata(t *testing.T) {
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
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	initiator := convtypes.ConversationForkInitiator{
		Type:     convtypes.ConversationForkInitiatorTypeExtensionTool,
		ToolName: "subagent",
	}
	child := convtypes.ForkConversationRecordWithOptions(
		convtypes.NewConversationRecord("conversation-parent"),
		convtypes.ConversationForkOptions{Mode: convtypes.ConversationForkModeLiveSnapshot, Initiator: &initiator},
	)
	child.ID = "conversation-remote-command"
	child.CWD = "/runner/other-project"
	child.Provider = "anthropic"
	child.Metadata[RunnerIDMetadataKey] = "runner-one"
	child.Metadata[EnvironmentProfileMetadataKey] = "gpu"
	store, err := conversations.GetConversationStore(t.Context())
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), child))
	require.NoError(t, store.Close())

	environment := &directCommandEnvironment{
		manifest: agentenv.Manifest{WorkingDirectory: "/runner/other-project"},
		result: agentenv.CommandResult{
			Matched:     true,
			Action:      agentenv.CommandActionRespond,
			CommandName: "runner-status",
			Response:    "runner ready",
			Display:     "/runner-status",
		},
	}
	resolver := &recordingEnvironmentResolver{environment: environment}
	runner := NewDefaultChatRunner("")
	runner.SetEnvironmentResolver(resolver)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })
	sink := &recordingChatSink{}

	conversationID, err := runner.Run(t.Context(), ChatRequest{
		ConversationID:     "conversation-remote-command",
		RunnerID:           "runner-one",
		EnvironmentProfile: "gpu",
		Message:            "/runner-status",
	}, sink)
	require.NoError(t, err)
	assert.Equal(t, "conversation-remote-command", conversationID)
	assert.Equal(t, "runner-one", resolver.request.RunnerID)
	assert.Equal(t, conversationID, resolver.conversationID)
	assert.Equal(t, "/runner/other-project", resolver.config.WorkingDirectory)
	assert.Equal(t, "/runner-status", environment.request.Message)
	assert.Equal(t, "gpu", environment.request.RunSpec.EnvironmentProfile)
	assert.Equal(t, "subagent", environment.request.RunSpec.InvokedBy)
	require.Len(t, sink.events, 2)
	assert.Equal(t, "conversation", sink.events[0].Kind)
	assert.Equal(t, "runner ready", sink.events[1].Content)

	service, err := conversations.GetDefaultConversationService(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	record, err := service.GetConversation(t.Context(), conversationID)
	require.NoError(t, err)
	assert.Equal(t, "/runner/other-project", record.CWD)
	assert.Equal(t, "runner-one", record.Metadata[RunnerIDMetadataKey])
	assert.Equal(t, "gpu", record.Metadata[EnvironmentProfileMetadataKey])

	config, err := ResolveRemoteConfigWithReasoning(t.Context(), "", "", "high")
	require.NoError(t, err)
	assert.Equal(t, "high", config.ReasoningEffort)
}

func TestResolveRemoteWorkingDirectoryPinsExistingConversation(t *testing.T) {
	requested, expected := resolveRemoteWorkingDirectory("", "/runner/project")
	assert.Equal(t, "/runner/project", requested)
	assert.Equal(t, "/runner/project", expected)

	requested, expected = resolveRemoteWorkingDirectory(" backend ", "/runner/project/backend")
	assert.Equal(t, "backend", requested)
	assert.Equal(t, "/runner/project/backend", expected)

	requested, expected = resolveRemoteWorkingDirectory(" ../other ", "")
	assert.Equal(t, "../other", requested)
	assert.Empty(t, expected)
}

func TestDefaultChatRunnerStreamsAndPersistsExplicitCommandDisplay(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4.1")
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	conversationID := "conversation-dictate-display"
	config, _, err := ResolveRemoteConfigWithReasoningAndEnvironmentProfile(t.Context(), conversationID, "", "", "")
	require.NoError(t, err)
	fingerprint, err := chatThreadConfigFingerprint(config)
	require.NoError(t, err)

	thread := &fakeMetadataThread{conversationID: conversationID, persisted: true}
	environment := &directCommandEnvironment{result: agentenv.CommandResult{
		Matched:         true,
		Action:          agentenv.CommandActionRunAgent,
		CommandName:     "dictate",
		Prompt:          "model-facing transcription",
		Display:         "What should I make for breakfast?",
		DisplayOverride: true,
	}}
	runner := NewDefaultChatRunner("")
	runner.sessions[conversationID] = &defaultChatSession{
		thread:            thread,
		configFingerprint: fingerprint,
		lastUsed:          time.Now(),
	}
	runner.SetEnvironmentResolver(&recordingEnvironmentResolver{environment: environment})
	t.Cleanup(func() { require.NoError(t, runner.Close()) })
	sink := &recordingChatSink{}

	gotID, err := runner.Run(t.Context(), ChatRequest{
		ConversationID: conversationID,
		RunnerID:       "runner-one",
		Message:        "/dictate",
	}, sink)

	require.NoError(t, err)
	assert.Equal(t, conversationID, gotID)
	require.Len(t, sink.events, 2)
	assert.Equal(t, "user-message-display", sink.events[0].Kind)
	assert.Equal(t, "What should I make for breakfast?", sink.events[0].Content)
	assert.Equal(t, "conversation", sink.events[1].Kind)
	assert.Equal(t, 1, thread.sendCalls)
	assert.Same(t, environment, thread.environment)
	display, ok := conversations.LookupMessageDisplay(thread.metadata, "model-facing transcription")
	require.True(t, ok)
	assert.Equal(t, "What should I make for breakfast?", display.Text)
	assert.Empty(t, display.Kind)
	assert.Empty(t, display.Command)
}

func TestDefaultChatRunnerIncludesImagesInExplicitCommandDisplay(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4.1")
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	conversationID := "conversation-dictate-display-image"
	config, _, err := ResolveRemoteConfigWithReasoningAndEnvironmentProfile(t.Context(), conversationID, "", "", "")
	require.NoError(t, err)
	fingerprint, err := chatThreadConfigFingerprint(config)
	require.NoError(t, err)

	thread := &fakeMetadataThread{conversationID: conversationID, persisted: true}
	environment := &directCommandEnvironment{result: agentenv.CommandResult{
		Matched:         true,
		Action:          agentenv.CommandActionRunAgent,
		CommandName:     "dictate",
		Prompt:          "model-facing transcription",
		Display:         "What is in this image?",
		DisplayOverride: true,
	}}
	runner := NewDefaultChatRunner("")
	runner.sessions[conversationID] = &defaultChatSession{
		thread:            thread,
		configFingerprint: fingerprint,
		lastUsed:          time.Now(),
	}
	runner.SetEnvironmentResolver(&recordingEnvironmentResolver{environment: environment})
	t.Cleanup(func() { require.NoError(t, runner.Close()) })
	sink := &recordingChatSink{}

	gotID, err := runner.Run(t.Context(), ChatRequest{
		ConversationID: conversationID,
		RunnerID:       "runner-one",
		Message:        "/dictate",
		Content: []ChatContentBlock{
			{Type: "text", Text: "/dictate"},
			{
				Type: "image",
				Source: &ChatImageSource{
					Data:      "aGVsbG8=",
					MediaType: "image/png",
				},
			},
		},
	}, sink)

	require.NoError(t, err)
	assert.Equal(t, conversationID, gotID)
	require.Len(t, sink.events, 2)
	assert.Equal(t, "user-message-display", sink.events[0].Kind)
	assert.Equal(t, []ChatContentBlock{
		{Type: "text", Text: "What is in this image?"},
		{
			Type: "image",
			Source: &ChatImageSource{
				Data:      "aGVsbG8=",
				MediaType: "image/png",
			},
		},
	}, sink.events[0].Content)
	assert.Equal(t, "conversation", sink.events[1].Kind)
	assert.Equal(t, 1, thread.sendCalls)
}

func TestDefaultChatRunnerReusesAndClosesConversationThread(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}
	fingerprint, err := chatThreadConfigFingerprint(config)
	require.NoError(t, err)
	thread := &fakeMetadataThread{}
	runner := NewDefaultChatRunner("/workspace")
	runner.sessions["conv-1"] = &defaultChatSession{
		thread:            thread,
		configFingerprint: fingerprint,
		lastUsed:          time.Now(),
	}

	acquired, newThread, release, err := acquireChatThread(runner, "conv-1", config)
	require.NoError(t, err)
	assert.Same(t, thread, acquired)
	assert.False(t, newThread)
	release()
	assert.Equal(t, 0, runner.sessions["conv-1"].inUse)

	require.NoError(t, runner.Close())
	assert.True(t, thread.closed)
	assert.Empty(t, runner.sessions)
	require.NoError(t, runner.Close())
}

func TestDefaultChatRunnerRenameCommandPersistsWithoutCallingModel(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4.1")
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	workspace := t.TempDir()
	conversationID := "conv-rename"
	config, _, err := ResolveConfigWithReasoning(context.Background(), conversationID, "", "", workspace, workspace)
	require.NoError(t, err)
	fingerprint, err := chatThreadConfigFingerprint(config)
	require.NoError(t, err)

	thread := &fakeMetadataThread{conversationID: conversationID, persisted: true}
	runner := NewDefaultChatRunner(workspace, &fakeExtensionRuntimeProvider{})
	runner.sessions[conversationID] = &defaultChatSession{
		thread:            thread,
		configFingerprint: fingerprint,
		lastUsed:          time.Now(),
	}
	sink := &recordingChatSink{}

	gotID, err := runner.Run(context.Background(), ChatRequest{
		ConversationID: conversationID,
		CWD:            workspace,
		Message:        "/rename  Authentication\n cleanup ",
	}, sink)
	require.NoError(t, err)
	assert.Equal(t, conversationID, gotID)
	assert.Equal(t, "Authentication cleanup", conversations.ExplicitConversationName(thread.metadata))
	assert.Equal(t, 1, thread.saveCalls)
	assert.Zero(t, thread.sendCalls)
	require.Len(t, sink.events, 2)
	assert.Equal(t, "conversation", sink.events[0].Kind)
	assert.Equal(t, "Authentication cleanup", sink.events[0].ConversationName)
	assert.Equal(t, "ui-notification", sink.events[1].Kind)
	require.NotNil(t, sink.events[1].UINotify)
	assert.Equal(t, "Conversation renamed", sink.events[1].UINotify.Title)
	assert.Equal(t, `Renamed to "Authentication cleanup"`, sink.events[1].UINotify.Message)
}

func TestResolveConfigRejectsRunnerBoundConversationBeforeResolvingRemoteCWD(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	dbPath := filepath.Join(basePath, "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())

	store, err := conversationsqlite.NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, store.Save(t.Context(), convtypes.ConversationRecord{
		ID:          "remote-conversation",
		CWD:         filepath.Join(basePath, "runner-only-workspace", "missing"),
		Provider:    "openai",
		RawMessages: json.RawMessage(`[]`),
		Metadata:    map[string]any{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}))
	require.NoError(t, store.Close())

	database, err = db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	_, err = database.ExecContext(t.Context(), `
		INSERT INTO runner_registrations (
			id, owner_id, host_instance_id, workspace_path, workspace_name, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "runner-one", "local", "host-one", "/runner/workspace", "workspace", "offline", now, now)
	require.NoError(t, err)
	_, err = database.ExecContext(t.Context(), `
		INSERT INTO conversation_runner_affinity (conversation_id, runner_id, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, "remote-conversation", "runner-one", now, now)
	require.NoError(t, err)
	require.NoError(t, database.Close())

	_, _, err = ResolveConfigWithReasoning(t.Context(), "remote-conversation", "", "", "", t.TempDir())
	require.ErrorContains(t, err, "bound to runner runner-one")
	assert.NotContains(t, err.Error(), "cwd directory does not exist")
}

func TestPersistDirectCommandResponseWithoutCallingModel(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
	fingerprint, err := chatThreadConfigFingerprint(config)
	require.NoError(t, err)
	thread := &fakeMetadataThread{conversationID: "conv-command"}
	runner := NewDefaultChatRunner("/workspace")
	runner.sessions[thread.conversationID] = &defaultChatSession{
		thread:            thread,
		configFingerprint: fingerprint,
		lastUsed:          time.Now(),
	}

	require.NoError(t, persistDirectCommandResponse(
		t.Context(),
		runner,
		thread.conversationID,
		config,
		"",
		"",
		"/doctor",
		"Everything is healthy.",
		nil,
	))
	assert.True(t, thread.persisted)
	assert.Equal(t, []string{"/doctor"}, thread.userMessages)
	assert.Equal(t, []string{"Everything is healthy."}, thread.assistantMsgs)
	assert.Equal(t, 1, thread.saveCalls)
	assert.Zero(t, thread.sendCalls)
}

func TestAcquireChatThreadRejectsSessionDetachedDuringClose(t *testing.T) {
	config := llmtypes.Config{Provider: "unsupported", Model: "test"}
	fingerprint, err := chatThreadConfigFingerprint(config)
	require.NoError(t, err)
	thread := &fakeMetadataThread{}
	runner := NewDefaultChatRunner("/workspace")
	session := &defaultChatSession{
		thread:            thread,
		configFingerprint: fingerprint,
		lastUsed:          time.Now(),
	}
	runner.sessions["conv-1"] = session

	session.mu.Lock()
	sessionLocked := true
	defer func() {
		if sessionLocked {
			session.mu.Unlock()
		}
	}()

	type acquireResult struct {
		thread    llmtypes.Thread
		newThread bool
		err       error
	}
	acquireDone := make(chan acquireResult, 1)
	go func() {
		acquired, newThread, release, acquireErr := acquireChatThread(runner, "conv-1", config)
		if release != nil {
			release()
		}
		acquireDone <- acquireResult{thread: acquired, newThread: newThread, err: acquireErr}
	}()

	require.Eventually(t, func() bool {
		runner.sessionsMu.Lock()
		defer runner.sessionsMu.Unlock()
		return session.inUse == 1
	}, time.Second, time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- runner.Close()
	}()
	require.Eventually(t, func() bool {
		runner.sessionsMu.Lock()
		defer runner.sessionsMu.Unlock()
		return runner.closed && len(runner.sessions) == 0
	}, time.Second, time.Millisecond)

	session.mu.Unlock()
	sessionLocked = false

	var result acquireResult
	require.Eventually(t, func() bool {
		select {
		case result = <-acquireDone:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.Error(t, result.err)
	assert.ErrorContains(t, result.err, "chat runner is closed")
	assert.Nil(t, result.thread)
	assert.False(t, result.newThread)

	var closeErr error
	require.Eventually(t, func() bool {
		select {
		case closeErr = <-closeDone:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.NoError(t, closeErr)
	assert.True(t, thread.closed)
	runner.sessionsMu.Lock()
	assert.Equal(t, 0, session.inUse)
	runner.sessionsMu.Unlock()
}

func TestDefaultChatRunnerEvictsIdleConversationThreads(t *testing.T) {
	runner := NewDefaultChatRunner("/workspace")
	defer func() { require.NoError(t, runner.Close()) }()
	idleThread := &fakeMetadataThread{}
	runner.sessions["idle"] = &defaultChatSession{
		thread:   idleThread,
		lastUsed: time.Now().Add(-defaultChatSessionIdleTTL - time.Minute),
	}
	runner.sessions["active"] = &defaultChatSession{
		thread:   &fakeMetadataThread{},
		lastUsed: time.Now().Add(-defaultChatSessionIdleTTL - time.Minute),
		inUse:    1,
	}

	runner.sessionsMu.Lock()
	evicted := runner.evictIdleSessionsLocked("new", time.Now())
	runner.sessionsMu.Unlock()
	require.Len(t, evicted, 1)
	require.NoError(t, closeDefaultChatSession(evicted[0]))
	assert.True(t, idleThread.closed)
	assert.NotContains(t, runner.sessions, "idle")
	assert.Contains(t, runner.sessions, "active")
}

func TestDefaultChatRunnerClosesDeletedConversationThread(t *testing.T) {
	runner := NewDefaultChatRunner("/workspace")
	defer func() { require.NoError(t, runner.Close()) }()
	thread := &fakeMetadataThread{}
	runner.sessions["conv-1"] = &defaultChatSession{thread: thread, lastUsed: time.Now()}

	require.NoError(t, runner.CloseConversation(" conv-1 "))
	assert.True(t, thread.closed)
	assert.NotContains(t, runner.sessions, "conv-1")
}

func TestServiceStoreAdapterLoadAndUnsupportedMethods(t *testing.T) {
	now := time.Now().UTC()
	toolResults := map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "bash", Success: true},
	}
	service := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			assert.Equal(t, "conv-123", id)
			return &conversations.GetConversationResponse{
				ID:          id,
				CWD:         "/workspace/project",
				Provider:    "openai",
				Metadata:    map[string]any{"profile": "work"},
				RawMessages: json.RawMessage(`[{}]`),
				CreatedAt:   now,
				UpdatedAt:   now.Add(time.Minute),
				Usage:       llmtypes.Usage{InputTokens: 11, OutputTokens: 7},
				Summary:     "summary",
				ToolResults: toolResults,
			}, nil
		},
	}
	adapter := ServiceStoreAdapter{Service: service}

	record, err := adapter.Load(context.Background(), "conv-123")
	require.NoError(t, err)
	assert.Equal(t, "conv-123", record.ID)
	assert.Equal(t, "/workspace/project", record.CWD)
	assert.Equal(t, "openai", record.Provider)
	assert.Equal(t, map[string]any{"profile": "work"}, record.Metadata)
	assert.Equal(t, json.RawMessage(`[{}]`), record.RawMessages)
	assert.Equal(t, now, record.CreatedAt)
	assert.Equal(t, now.Add(time.Minute), record.UpdatedAt)
	assert.Equal(t, llmtypes.Usage{InputTokens: 11, OutputTokens: 7}, record.Usage)
	assert.Equal(t, "summary", record.Summary)
	assert.Equal(t, toolResults, record.ToolResults)

	require.ErrorContains(t, adapter.Save(context.Background(), convtypes.ConversationRecord{}), "save not implemented")
	require.ErrorContains(t, adapter.Delete(context.Background(), "conv-123"), "delete not implemented")
	_, err = adapter.Query(context.Background(), convtypes.QueryOptions{})
	require.ErrorContains(t, err, "query not implemented")
	assert.NoError(t, adapter.Close())
}

func TestNormalizeChatRequestAdditionalBranches(t *testing.T) {
	tests := []struct {
		name          string
		req           ChatRequest
		wantMessage   string
		wantImages    []string
		wantErrSubstr string
	}{
		{
			name:        "message only trims whitespace",
			req:         ChatRequest{Message: "  hello  "},
			wantMessage: "hello",
		},
		{
			name: "text content replaces message and joins blocks",
			req: ChatRequest{
				Message: "ignored",
				Content: []ChatContentBlock{
					{Type: "text", Text: " first "},
					{Type: "text", Text: ""},
					{Type: "text", Text: "second"},
				},
			},
			wantMessage: "first\n\nsecond",
			wantImages:  []string{},
		},
		{
			name: "image url keeps caption message",
			req: ChatRequest{
				Message: " caption ",
				Content: []ChatContentBlock{{
					Type:     "image",
					ImageURL: &ChatImageURLSource{URL: " https://example.com/image.png "},
				}},
			},
			wantMessage: "caption",
			wantImages:  []string{"https://example.com/image.png"},
		},
		{
			name: "image source becomes data url",
			req: ChatRequest{Content: []ChatContentBlock{{
				Type:   "image",
				Source: &ChatImageSource{Data: " aGVsbG8= ", MediaType: " image/png "},
			}}},
			wantImages: []string{"data:image/png;base64,aGVsbG8="},
		},
		{
			name: "image source requires data",
			req: ChatRequest{Content: []ChatContentBlock{{
				Type:   "image",
				Source: &ChatImageSource{MediaType: "image/png"},
			}}},
			wantErrSubstr: "image source must include data and media_type",
		},
		{
			name: "image url requires url",
			req: ChatRequest{Content: []ChatContentBlock{{
				Type:     "image",
				ImageURL: &ChatImageURLSource{},
			}}},
			wantErrSubstr: "image_url must include url",
		},
		{
			name:          "image block requires source",
			req:           ChatRequest{Content: []ChatContentBlock{{Type: "image"}}},
			wantErrSubstr: "image block must include source or image_url",
		},
		{
			name:          "unsupported block type",
			req:           ChatRequest{Content: []ChatContentBlock{{Type: "audio"}}},
			wantErrSubstr: "unsupported content block type: audio",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, images, err := NormalizeRequest(tt.req)
			if tt.wantErrSubstr != "" {
				require.ErrorContains(t, err, tt.wantErrSubstr)
				assert.Empty(t, message)
				assert.Nil(t, images)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMessage, message)
			assert.Equal(t, tt.wantImages, images)
		})
	}
}

func TestResolveWebChatConfigForNewConversationProfileBranches(t *testing.T) {
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
	viper.Set("profile", "active")
	viper.Set("profiles", map[string]any{
		"active": map[string]any{"provider": "openai", "model": "active-model"},
		"work":   map[string]any{"provider": "openai", "model": "work-model"},
	})

	config, err := ResolveConfigForNewConversation(" work ")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "work-model", config.Model)
	assert.Equal(t, "work", config.Profile)

	config, err = ResolveConfigForNewConversation("   ")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "active-model", config.Model)
	assert.Equal(t, "active", config.Profile)

	_, err = ResolveConfigForNewConversation("missing")
	require.ErrorContains(t, err, "profile 'missing' not found")

	assert.Equal(t, "", NormalizeRequestedProfile(""))
	assert.Equal(t, "", NormalizeRequestedProfile(" default "))
	assert.Equal(t, "team", NormalizeRequestedProfile(" team "))
}

func TestResolveWebChatConfigForExistingConversationNilAndFallbackBranches(t *testing.T) {
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
		"work": map[string]any{"provider": "openai", "model": "work-model"},
	})

	config, err := ResolveConfigForExistingConversation(nil)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "base-model", config.Model)

	config, err = ResolveConfigForExistingConversation(&conversations.GetConversationResponse{
		ID:       "conv-123",
		Provider: "  anthropic  ",
		Metadata: map[string]any{"profile": " work ", "model": " stored-model "},
	})
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "stored-model", config.Model)
	assert.Equal(t, "work", config.Profile)

	_, err = ResolveConfigForExistingConversation(&conversations.GetConversationResponse{
		Metadata: map[string]any{"profile": "missing"},
	})
	require.ErrorContains(t, err, "profile 'missing' not found")
}

func TestServiceStoreAdapterLoadPropagatesServiceError(t *testing.T) {
	wantErr := errors.New("conversation missing")
	adapter := ServiceStoreAdapter{Service: &mockConversationService{
		getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return nil, wantErr
		},
	}}

	_, err := adapter.Load(context.Background(), "missing")
	assert.ErrorIs(t, err, wantErr)
}

func TestAddWebChatDisplayMetadata(t *testing.T) {
	thread := &fakeMetadataThread{}
	expansion := &slashcommands.Expansion{
		Command: "limited",
		Prompt:  "Rendered recipe prompt",
		Display: "/limited name=Web",
	}

	AddSlashCommandDisplay(thread, expansion)

	display, ok := conversations.LookupMessageDisplay(thread.metadata, expansion.Prompt)
	require.True(t, ok)
	assert.Equal(t, conversations.MessageDisplayKindSlashCommand, display.Kind)
	assert.Equal(t, expansion.Display, display.Text)
	assert.Equal(t, expansion.Command, display.Command)

	extensionResult := &extensions.RoutedCommandResult{
		CommandName: "review",
		Prompt:      "Review the current diff",
		Display:     "/review target=HEAD",
	}
	AddExtensionCommandDisplay(thread, extensionResult)

	display, ok = conversations.LookupMessageDisplay(thread.metadata, extensionResult.Prompt)
	require.True(t, ok)
	assert.Equal(t, conversations.MessageDisplayKindSlashCommand, display.Kind)
	assert.Equal(t, extensionResult.Display, display.Text)
	assert.Equal(t, extensionResult.CommandName, display.Command)

	goalUpdate := &goals.CommandUpdate{
		ModelPrompt: goals.ModelPrompt("find cores"),
		Display:     goals.DisplayText("find cores"),
		Goal:        goals.New("find cores", time.Now()),
	}
	AddGoalDisplay(thread, goalUpdate)

	assert.Equal(t, goalUpdate.Goal, thread.metadata[goals.MetadataKey])
	display, ok = conversations.LookupMessageDisplay(thread.metadata, goalUpdate.ModelPrompt)
	require.True(t, ok)
	assert.Equal(t, conversations.MessageDisplayKindGoal, display.Kind)
	assert.Equal(t, goalUpdate.Display, display.Text)
	assert.Equal(t, goals.SlashCommandName, display.Command)

	environmentResult := agentenv.CommandResult{
		Matched:         true,
		CommandName:     "runner-review",
		Prompt:          "Review on the runner",
		Display:         "/runner-review staged",
		AllowedTools:    []string{"file_read"},
		AllowedCommands: []string{"git diff --cached"},
	}
	AddEnvironmentCommandDisplay(thread, environmentResult)
	display, ok = conversations.LookupMessageDisplay(thread.metadata, environmentResult.Prompt)
	require.True(t, ok)
	assert.Equal(t, environmentResult.Display, display.Text)
	assert.Equal(t, environmentResult.CommandName, display.Command)

	explicitEnvironmentResult := agentenv.CommandResult{
		Matched:         true,
		CommandName:     "dictate",
		Prompt:          "internal transcription",
		Display:         "What should I make for breakfast?",
		DisplayOverride: true,
	}
	AddEnvironmentCommandDisplay(thread, explicitEnvironmentResult)
	display, ok = conversations.LookupMessageDisplay(thread.metadata, explicitEnvironmentResult.Prompt)
	require.True(t, ok)
	assert.Equal(t, explicitEnvironmentResult.Display, display.Text)
	assert.Empty(t, display.Kind)
	assert.Empty(t, display.Command)

	config := llmtypes.Config{}
	ApplyCommandRestrictions(t.Context(), &config, environmentResult)
	assert.Equal(t, []string{"file_read"}, config.AllowedTools)
	assert.Equal(t, []string{"git diff --cached"}, config.AllowedCommands)
	AddEnvironmentCommandDisplay(nil, environmentResult)
	AddEnvironmentCommandDisplay(thread, agentenv.CommandResult{})
}

func TestChatMessageHandlerEmitsStreamingEventsAndBroadcasts(t *testing.T) {
	sink := &recordingChatSink{}
	var broadcasted []ChatEvent
	handler := &chatMessageHandler{
		conversationID: "conv-123",
		sink:           sink,
		broadcast: func(conversationID string, event ChatEvent) {
			assert.Equal(t, "conv-123", conversationID)
			broadcasted = append(broadcasted, event)
		},
	}

	handler.HandleText("   ")
	handler.HandleText("hello")
	handler.HandleToolUse("tool-1", "bash", `{"command":"pwd"}`)
	handler.HandleThinking("thought")
	handler.HandleThinking("   ")
	handler.HandleTextDelta("delta")
	handler.HandleTextDelta("")
	handler.HandleThinkingStart()
	handler.HandleThinkingDelta("think")
	handler.HandleThinkingDelta("")
	handler.HandleThinkingBlockEnd()
	handler.HandleContentBlockEnd()
	handler.HandleDone()

	wantKinds := []string{
		"text",
		"tool-use",
		"thinking",
		"text-delta",
		"thinking-start",
		"thinking-delta",
		"thinking-end",
		"content-end",
	}
	require.Len(t, sink.events, len(wantKinds))
	require.Len(t, broadcasted, len(wantKinds))
	for i, wantKind := range wantKinds {
		assert.Equal(t, wantKind, sink.events[i].Kind)
		assert.Equal(t, sink.events[i], broadcasted[i])
		assert.Equal(t, "conv-123", sink.events[i].ConversationID)
		assert.Equal(t, "assistant", sink.events[i].Role)
	}
	assert.Equal(t, "hello", sink.events[0].Content)
	assert.Equal(t, "tool-1", sink.events[1].ToolCallID)
	assert.Equal(t, "bash", sink.events[1].ToolName)
	assert.Equal(t, "delta", sink.events[3].Delta)
	assert.Equal(t, "think", sink.events[5].Delta)
}

func TestChatContentBlocksForUserInputHandlesURLsAndLocalFiles(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png-bytes"), 0o644))

	blocks := ContentBlocksForUserInput("  see this  ", []string{
		"https://example.com/shot.png",
		imagePath,
		"file://" + imagePath,
		"relative-missing.png",
		"   ",
	})

	require.Len(t, blocks, 5)
	assert.Equal(t, ChatContentBlock{Type: "text", Text: "see this"}, blocks[0])
	require.NotNil(t, blocks[1].ImageURL)
	assert.Equal(t, "https://example.com/shot.png", blocks[1].ImageURL.URL)

	require.NotNil(t, blocks[2].Source)
	assert.Equal(t, "image/png", blocks[2].Source.MediaType)
	assert.Equal(t, "cG5nLWJ5dGVz", blocks[2].Source.Data)
	require.NotNil(t, blocks[3].Source)
	assert.Equal(t, blocks[2].Source, blocks[3].Source)

	require.NotNil(t, blocks[4].ImageURL)
	assert.Equal(t, "relative-missing.png", blocks[4].ImageURL.URL)

	assert.Nil(t, ContentBlocksForUserInput("text only", nil))
	assert.Empty(t, ContentBlocksForUserInput("   ", []string{"   "}))
}
