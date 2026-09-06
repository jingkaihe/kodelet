package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/delegation"
	openaillm "github.com/jingkaihe/kodelet/pkg/llm/openai"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildConfigurationCeiling(t *testing.T) {
	parent := llmtypes.Config{
		Provider: "openai", Model: "gpt-4o", WeakModel: "gpt-4o-mini", MaxTokens: 4096, WeakModelMaxTokens: 2048, ReasoningEffort: "medium",
		ExecutionOptions: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool", "search"}), AllowedCommands: new([]string{}), MaxTurns: new(8), NoSkills: new(true), EnableFSSearchTools: new(true)},
	}
	preset := &llmtypes.ExecutionOptions{Model: new("gpt-4o-mini"), AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool"}), NoExtensions: new(true), NoSkills: new(true), MaxTurns: new(3)}
	config, err := childConfiguration(parent, preset, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o-mini", config.Model)
	assert.Equal(t, []string{"file_read", "grep_tool", "glob_tool"}, *config.ExecutionOptions.AllowedTools)
	assert.True(t, *config.ExecutionOptions.NoExtensions)
	assert.Equal(t, 8, *parent.ExecutionOptions.MaxTurns)
	for _, options := range []*llmtypes.ExecutionOptions{
		{Provider: new("anthropic")},
		{Model: new("unconfigured-private-model")},
		{MaxTokens: new(4097)},
		{MaxTurns: new(9)},
		{MaxTurns: new(0)},
		{NoExtensions: new(false)},
		{NoSkills: new(false)},
		{AllowedTools: new([]string{"bash"})},
		{AllowedCommands: new([]string{"*"})},
	} {
		_, err := childConfiguration(parent, preset, options)
		assert.Error(t, err, "%+v", options)
	}
	config, err = childConfiguration(parent, preset, &llmtypes.ExecutionOptions{AllowedTools: new([]string{}), MaxTurns: new(1)})
	require.NoError(t, err)
	assert.True(t, config.ExecutionOptions.ToolsDisabled())
}

func TestChildPresetResumeKeepsCeiling(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4o-mini", MaxTokens: 256, ReasoningEffort: "medium"}
	preset := childPresetSnapshot{Name: "code_search", SystemPrompt: "pinned prompt", Options: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool"}), NoExtensions: new(true), NoSkills: new(true), MaxTurns: new(3)}}
	for _, requested := range []*llmtypes.ExecutionOptions{nil, {AllowedTools: new([]string{"file_read"})}, {NoTools: new(true)}} {
		resumed, err := applyChildPresetSnapshot(config, requested, preset)
		require.NoError(t, err)
		assert.True(t, *resumed.ExecutionOptions.NoExtensions)
		assert.True(t, *resumed.ExecutionOptions.NoSkills)
		assert.Equal(t, 3, *resumed.ExecutionOptions.MaxTurns)
	}
	for _, requested := range []*llmtypes.ExecutionOptions{{NoExtensions: new(false)}, {NoSkills: new(false)}, {AllowedTools: new([]string{"bash"})}, {MaxTurns: new(0)}, {MaxTurns: new(4)}} {
		_, err := applyChildPresetSnapshot(config, requested, preset)
		require.Error(t, err)
	}
	assert.Equal(t, []string{"file_read", "grep_tool", "glob_tool"}, *preset.Options.AllowedTools)
}

func TestChildFollowupUsageCountsOnlyThisTurn(t *testing.T) {
	initial := llmtypes.Usage{InputTokens: 100, OutputTokens: 50, CacheCreationInputTokens: 30, CacheReadInputTokens: 20, InputCost: 10, OutputCost: 5, CacheCreationCost: 3, CacheReadCost: 2, CurrentContextWindow: 999, MaxContextWindow: 2000}
	total := llmtypes.Usage{InputTokens: 107, OutputTokens: 55, CacheCreationInputTokens: 33, CacheReadInputTokens: 22, InputCost: 11, OutputCost: 6, CacheCreationCost: 4, CacheReadCost: 3, CurrentContextWindow: 1234, MaxContextWindow: 2000}
	assert.Equal(t, llmtypes.Usage{InputTokens: 7, OutputTokens: 5, CacheCreationInputTokens: 3, CacheReadInputTokens: 2, InputCost: 1, OutputCost: 1, CacheCreationCost: 1, CacheReadCost: 1}, childTurnUsage(total, initial))
}

func childChatFixture(t *testing.T) (llmtypes.Config, delegation.Identity, delegation.Preset, conversationservice.ConversationStore) {
	t.Helper()
	t.Setenv("KODELET_BASE_PATH", filepath.Join(t.TempDir(), "state"))
	t.Setenv("CHILD_TEST_API_KEY", "not-a-real-key")
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	path, err := db.DefaultDBPath()
	require.NoError(t, err)
	database, err := db.Open(t.Context(), path)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO runner_registrations(id,owner_id,host_instance_id,workspace_path,workspace_name,status,created_at,updated_at) VALUES ('runner','local','host','/work','work','ready',?,?)`, time.Now().UTC(), time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, database.Close())
	store, err := conversationservice.GetConversationStore(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	config := llmtypes.Config{
		Provider: "openai", Model: "gpt-4o", WeakModel: "gpt-4o-mini", MaxTokens: 1024, WeakModelMaxTokens: 512, ReasoningEffort: "medium", WorkingDirectory: "/work",
		OpenAI:           &llmtypes.OpenAIConfig{APIKeyEnvVar: "CHILD_TEST_API_KEY", APIMode: llmtypes.OpenAIAPIModeChatCompletions},
		ExecutionOptions: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read", "grep_tool"}), NoSkills: new(true), NoExtensions: new(true), MaxTurns: new(5)},
	}
	identity := delegation.Identity{ConversationID: "child", RunID: "first", ParentConversationID: "parent", ParentRunID: "parent-run", ExtensionID: "subagent", Profile: "research", RunnerID: "runner", HostInstanceID: "host"}
	preset := delegation.Preset{Profile: delegation.Profile{Name: "research", SystemPrompt: "frozen child prompt", Options: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read"}), MaxTurns: new(3)}}, ExtensionID: "subagent", Generation: 1}
	return config, identity, preset, store
}

func TestChildForkSnapshotsLiveProviderHistoryWithoutPublishingParentOrTemporaryChild(t *testing.T) {
	config, id, preset, store := childChatFixture(t)
	parent, err := openaillm.NewOpenAIThread(config)
	require.NoError(t, err)
	parent.SetConversationID("parent")
	parent.EnablePersistence(t.Context(), true)
	t.Cleanup(func() { require.NoError(t, parent.Store.Close()) })
	parent.AddUserMessage(t.Context(), "persisted parent input")
	require.NoError(t, parent.SaveConversation(t.Context()))
	parent.AddAssistantMessage(t.Context(), "live assistant context not yet saved")
	parent.Usage.InputTokens = 99
	request := delegation.Request{ContextMode: "fork", RequestID: "fork", Profile: "research", Message: "task", CWD: "/work/subdir"}
	state, err := prepareChild(t.Context(), parent, config, request, preset, id, "")
	require.NoError(t, err)
	require.NotNil(t, state.record)
	assert.Contains(t, string(state.record.RawMessages), "live assistant context not yet saved")
	assert.Zero(t, state.record.Usage.InputTokens)
	before, err := store.Query(t.Context(), convtypes.QueryOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, before.Total)
	parentStored, err := store.Load(t.Context(), "parent")
	require.NoError(t, err)
	assert.NotContains(t, string(parentStored.RawMessages), "live assistant context not yet saved")
	require.NoError(t, persistChildIdentity(t.Context(), state, "runner", ""))
	child, err := store.Load(t.Context(), "child")
	require.NoError(t, err)
	assert.Contains(t, string(child.RawMessages), "live assistant context not yet saved")
	assert.Equal(t, "/work/subdir", child.CWD)
	var saved childPresetSnapshot
	require.NoError(t, decodeChildMetadata(child.Metadata["execution_preset"], &saved))
	assert.Equal(t, "frozen child prompt", saved.SystemPrompt)
	assert.Equal(t, []string{"file_read"}, *saved.Options.AllowedTools)
	var origin delegation.Identity
	require.NoError(t, decodeChildMetadata(child.Metadata["delegation"], &origin))
	assert.Equal(t, id, origin)
	after, err := store.Query(t.Context(), convtypes.QueryOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, after.Total)
	assert.Equal(t, 99, parent.Usage.InputTokens)
}

func TestChildResumeValidatesProvenanceAndFreezesPolicyWithoutOverwritingHistory(t *testing.T) {
	config, id, preset, store := childChatFixture(t)
	record := convtypes.NewConversationRecord(id.ConversationID)
	record.Provider, record.CWD = "openai", "/work/subdir"
	record.RawMessages = json.RawMessage(`[{"role":"user","content":"original task"},{"role":"assistant","content":"original answer"}]`)
	record.Usage.InputTokens = 123
	record.Metadata["delegation"] = id
	record.Metadata["execution_preset"] = childPresetSnapshot{Name: preset.Name, Options: preset.Options, SystemPrompt: preset.SystemPrompt}
	record.Metadata[RunnerIDMetadataKey], record.Metadata[EnvironmentProfileMetadataKey] = "runner", ""
	var err error
	record.Metadata, err = conversationservice.AddConfigSnapshot(record.Metadata, config)
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), record))
	request := delegation.Request{RequestID: "followup", Profile: "research", Message: "continue", Resume: "child"}
	next := id
	next.RunID = "second"
	next.ParentRunID = "new-parent-run"
	changed := preset
	changed.SystemPrompt = "must not replace frozen prompt"
	changed.Options = &llmtypes.ExecutionOptions{AllowedTools: new([]string{"bash"})}
	state, err := prepareChild(t.Context(), nil, config, request, changed, next, "")
	require.NoError(t, err)
	assert.Equal(t, "frozen child prompt", state.prompt)
	assert.Equal(t, "/work/subdir", state.config.WorkingDirectory)
	assert.Equal(t, []string{"file_read"}, *state.config.ExecutionOptions.AllowedTools)
	for _, change := range []func(*delegation.Request, *delegation.Identity){
		func(_ *delegation.Request, i *delegation.Identity) { i.ParentConversationID = "foreign" },
		func(_ *delegation.Request, i *delegation.Identity) { i.ExtensionID = "foreign" },
		func(_ *delegation.Request, i *delegation.Identity) { i.HostInstanceID = "replacement" },
		func(_ *delegation.Request, i *delegation.Identity) { i.RunnerID = "replacement" },
		func(r *delegation.Request, _ *delegation.Identity) { r.Profile = "other" },
		func(r *delegation.Request, _ *delegation.Identity) { r.CWD = "/work" },
		func(r *delegation.Request, _ *delegation.Identity) { r.SystemPrompt = "new" },
		func(r *delegation.Request, _ *delegation.Identity) {
			r.Options = &llmtypes.ExecutionOptions{Model: new("gpt-4o-mini")}
		},
		func(r *delegation.Request, _ *delegation.Identity) {
			r.Options = &llmtypes.ExecutionOptions{AllowedTools: new([]string{"grep_tool"})}
		},
	} {
		req, badID := request, next
		change(&req, &badID)
		_, err := prepareChild(t.Context(), nil, config, req, changed, badID, "")
		require.Error(t, err)
	}
	_, err = prepareChild(t.Context(), nil, config, request, changed, next, "different-env")
	require.Error(t, err)
	narrower := config.Clone()
	narrower.ExecutionOptions.AllowedTools = new([]string{})
	narrowed, err := prepareChild(t.Context(), nil, narrower, request, changed, next, "")
	require.NoError(t, err)
	assert.True(t, narrowed.config.ExecutionOptions.ToolsDisabled())
	require.NoError(t, persistChildIdentity(t.Context(), state, "runner", ""))
	after, err := store.Load(t.Context(), "child")
	require.NoError(t, err)
	assert.Equal(t, record.RawMessages, after.RawMessages)
	assert.Equal(t, 123, after.Usage.InputTokens)
	var origin delegation.Identity
	require.NoError(t, decodeChildMetadata(after.Metadata["delegation"], &origin))
	assert.Equal(t, id, origin, "followup must preserve originating provenance")
	// No second grant can overwrite the admitted child's snapshot or history.
	require.Error(t, persistChildIdentity(t.Context(), state, "runner", ""))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, persistChildIdentity(ctx, state, "runner", ""))
}
