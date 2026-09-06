package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adoptionTestModelPolicy(t *testing.T) {
	t.Helper()
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "adoption-local-presence-only")
	viper.Set("provider", "anthropic")
	viper.Set("model", "adoption-main")
	viper.Set("weak_model", "adoption-weak")
	viper.Set("anthropic_api_access", "api-key")
}

func startAdoptionStandaloneRunner(t *testing.T, server *Server, endpoint, workspace string, settings map[string]any) string {
	t.Helper()
	loader, err := runnerclient.NewWorkspaceConfigLoader(settings)
	require.NoError(t, err)
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	runner, err := runnerclient.NewRunner(t.Context(), runnerclient.RunnerConfig{
		Server: endpoint, Workspace: workspace, Store: store,
		ServiceOptions: runnerclient.ServiceOptions{WorkspaceConfigLoader: loader},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			assert.Fail(t, "standalone adoption runner did not stop")
		}
	})
	var id string
	require.Eventually(t, func() bool {
		for _, candidate := range server.runnerRegistry.Runners() {
			if candidate.ID != server.EmbeddedRunnerStatus().RunnerID && candidate.Connected && candidate.Status == "idle" {
				id = candidate.ID
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	return id
}

func TestAdoptionPreservesLegacyHistoryAcrossRunnerPlacements(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		name := "standalone"
		if embedded {
			name = "embedded"
		}
		t.Run(name, func(t *testing.T) {
			adoptionTestModelPolicy(t)
			config := embeddedRunnerTestConfig(t)
			workspace, settings := config.EmbeddedRunner.Workspace, config.EmbeddedRunner.Settings
			settings["environment_profiles"] = map[string]any{"restricted": map[string]any{"allowed_tools": []string{"file_read"}}}
			if !embedded {
				config.EmbeddedRunner = nil
			}
			server, endpoint, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
			var runnerID string
			if embedded {
				require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
				runnerID = server.EmbeddedRunnerStatus().RunnerID
			} else {
				runnerID = startAdoptionStandaloneRunner(t, server, endpoint, workspace, settings)
			}
			store, err := conversations.GetConversationStore(t.Context())
			require.NoError(t, err)
			defer store.Close()
			record := convtypes.NewConversationRecord("legacy-adoption")
			record.Provider, record.CWD, record.Summary = "anthropic", workspace, "history survives adoption"
			record.CreatedAt = time.Date(2024, 7, 1, 2, 3, 4, 0, time.UTC)
			record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"old question"}]}]`)
			record.Usage = llmtypes.Usage{InputTokens: 17, OutputTokens: 9, InputCost: 0.25}
			record.Metadata["custom"] = map[string]any{"unchanged": "yes"}
			model, err := chat.ResolveConfigForNewConversation("default")
			require.NoError(t, err)
			record.Metadata, err = conversations.AddConfigSnapshot(record.Metadata, model)
			require.NoError(t, err)
			require.NoError(t, store.Save(t.Context(), record))
			record, err = store.Load(t.Context(), record.ID)
			require.NoError(t, err)
			client, err := chat.NewControlPlaneChatRunner(endpoint, "web-secret", "")
			require.NoError(t, err)
			params := chat.ConversationAdoptionRequest{RunnerID: runnerID, EnvironmentProfile: "restricted"}
			preview, err := client.AdoptConversation(t.Context(), record.ID, params)
			require.NoError(t, err)
			assert.False(t, preview.Adopted)
			assert.Equal(t, workspace, preview.CWD)
			assert.NotEmpty(t, preview.HostInstanceID)
			unchanged, err := store.Load(t.Context(), record.ID)
			require.NoError(t, err)
			assert.Equal(t, record, unchanged)
			_, bound := server.runnerRegistry.RunnerForConversation(record.ID)
			assert.False(t, bound)
			params.Confirmation = "not-the-confirmed-preview"
			_, err = client.AdoptConversation(t.Context(), record.ID, params)
			require.ErrorContains(t, err, "preview changed")
			unchanged, err = store.Load(t.Context(), record.ID)
			require.NoError(t, err)
			assert.Equal(t, record, unchanged)
			params.Confirmation = preview.Confirmation
			adopted, err := client.AdoptConversation(t.Context(), record.ID, params)
			require.NoError(t, err)
			assert.True(t, adopted.Adopted)
			affinity, found, err := server.runnerRegistry.ResolveConversationAffinity(t.Context(), record.ID)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, runnerID, affinity.RunnerID)
			assert.Equal(t, "restricted", affinity.EnvironmentProfile)
			updated, err := store.Load(t.Context(), record.ID)
			require.NoError(t, err)
			assert.Equal(t, runnerID, updated.Metadata[convtypes.RunnerIDMetadataKey])
			assert.Equal(t, "restricted", updated.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey])
			delete(updated.Metadata, convtypes.RunnerIDMetadataKey)
			delete(updated.Metadata, convtypes.RunnerEnvironmentProfileMetadataKey)
			assert.Equal(t, record, updated)
			listed, err := store.Query(t.Context(), convtypes.QueryOptions{RunnerID: runnerID, CWD: workspace})
			require.NoError(t, err)
			require.Len(t, listed.ConversationSummaries, 1)
			assert.Equal(t, runnerID, listed.ConversationSummaries[0].Metadata[convtypes.RunnerIDMetadataKey])
			_, err = client.AdoptConversation(t.Context(), record.ID, params)
			require.ErrorContains(t, err, "already bound")
			// Validate a real resumed environment, without constructing a model or
			// dispatching a tool, proving adoption agrees with run.open targeting.
			_, err = server.runnerRegistry.OpenRun(t.Context(), runnerID, protocol.RunOpenParams{
				RunID: "adopted-open", ConversationID: record.ID, CWD: workspace, ExpectedCWD: workspace,
				Agent: protocol.AgentDescriptor{EnvironmentProfile: "restricted"},
			})
			require.NoError(t, err)
			require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), "adopted-open", registry.RunStatusSucceeded, nil))
		})
	}
}

func TestAdoptionRejectsUnsafeTargetsAndRollsBack(t *testing.T) {
	adoptionTestModelPolicy(t)
	config := embeddedRunnerTestConfig(t)
	server, endpoint, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	client, err := chat.NewControlPlaneChatRunner(endpoint, "web-secret", "")
	require.NoError(t, err)
	store, err := conversations.GetConversationStore(t.Context())
	require.NoError(t, err)
	defer store.Close()
	path, err := db.DefaultDBPath()
	require.NoError(t, err)
	database, err := db.Open(t.Context(), path)
	require.NoError(t, err)
	defer database.Close()
	for _, scenario := range []string{"missing cwd", "unavailable directory", "unavailable profile", "model policy", "credentials", "provenance", "confirmation", "active receipt", "changed record", "summary rollback", "concurrent confirmation"} {
		t.Run(scenario, func(t *testing.T) {
			record := convtypes.NewConversationRecord(convtypes.GenerateID())
			record.Provider, record.CWD = "anthropic", config.EmbeddedRunner.Workspace
			record.RawMessages = json.RawMessage(`[{"role":"user","content":"legacy text"}]`)
			params := chat.ConversationAdoptionRequest{RunnerID: runnerID}
			switch scenario {
			case "missing cwd":
				record.CWD = ""
			case "unavailable directory":
				record.CWD = filepath.Join(record.CWD, "absent")
			case "unavailable profile":
				params.EnvironmentProfile = "missing-profile"
			case "model policy":
				record.Metadata["model"] = "not-in-daemon-policy"
			case "credentials":
				t.Setenv("ANTHROPIC_API_KEY", "")
			case "provenance":
				record.Metadata[convtypes.RunnerIDMetadataKey] = "previous-runner"
			}
			require.NoError(t, store.Save(t.Context(), record))
			record, err = store.Load(t.Context(), record.ID)
			require.NoError(t, err)
			preview, previewErr := client.AdoptConversation(t.Context(), record.ID, params)
			switch scenario {
			case "confirmation", "active receipt", "changed record", "summary rollback", "concurrent confirmation":
				require.NoError(t, previewErr)
				params.Confirmation = preview.Confirmation
				switch scenario {
				case "confirmation":
					params.Confirmation = "wrong"
				case "active receipt":
					_, err := database.Exec(`INSERT INTO chat_turns (conversation_id,turn_id,status,created_at,updated_at) VALUES (?,'active','accepted',?,?)`, record.ID, time.Now(), time.Now())
					require.NoError(t, err)
					runner, found := server.runnerRegistry.Runner(runnerID)
					require.True(t, found)
					require.ErrorIs(t, server.runnerRegistry.AdoptConversation(t.Context(), record, runnerID, runner.Generation, ""), registry.ErrAdoptionConflict, "transaction must reject admission after validation too")
				case "changed record":
					record.Summary = "newer history"
					require.NoError(t, store.Save(t.Context(), record))
					record, err = store.Load(t.Context(), record.ID)
					require.NoError(t, err)
				case "summary rollback":
					_, err := database.Exec(`CREATE TRIGGER reject_adoption_summary BEFORE UPDATE ON conversation_summaries BEGIN SELECT RAISE(ABORT, 'summary unavailable'); END`)
					require.NoError(t, err)
					defer func() { _, err := database.Exec(`DROP TRIGGER reject_adoption_summary`); require.NoError(t, err) }()
				case "concurrent confirmation":
					var wg sync.WaitGroup
					results := make(chan error, 2)
					for range 2 {
						wg.Go(func() { _, err := client.AdoptConversation(t.Context(), record.ID, params); results <- err })
					}
					wg.Wait()
					first, second := <-results, <-results
					assert.True(t, (first == nil) != (second == nil), "exactly one commit succeeds: %v, %v", first, second)
					return
				}
				_, err = client.AdoptConversation(t.Context(), record.ID, params)
				require.Error(t, err)
			default:
				require.Error(t, previewErr)
			}
			unchanged, err := store.Load(t.Context(), record.ID)
			require.NoError(t, err)
			assert.Equal(t, record, unchanged)
			_, bound, err := server.runnerRegistry.ResolveConversationAffinity(t.Context(), record.ID)
			require.NoError(t, err)
			assert.False(t, bound)
		})
	}
	for _, body := range []string{`{}`, `{"runnerId":"` + runnerID + `","rawMessages":[]}`, `{"runnerId":"` + runnerID + `"} {}`} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/api/conversations/legacy/adopt", bytes.NewBufferString(body))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer web-secret")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, response.StatusCode)
		require.NoError(t, response.Body.Close())
	}
	unauthorized, err := chat.NewControlPlaneChatRunner(endpoint, "wrong", "")
	require.NoError(t, err)
	_, err = unauthorized.AdoptConversation(t.Context(), "legacy", chat.ConversationAdoptionRequest{RunnerID: runnerID})
	var httpErr *chat.ControlPlaneHTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusUnauthorized, httpErr.StatusCode)
	_, err = os.Stat(filepath.Join(config.EmbeddedRunner.Workspace, "absent"))
	assert.True(t, os.IsNotExist(err), "failed validation must not create a directory")
}

func TestAdoptionFencesHostGenerationAndActiveReservation(t *testing.T) {
	adoptionTestModelPolicy(t)
	config := embeddedRunnerTestConfig(t)
	server, endpoint, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	firstID := server.EmbeddedRunnerStatus().RunnerID
	secondID := startAdoptionStandaloneRunner(t, server, endpoint, config.EmbeddedRunner.Workspace, config.EmbeddedRunner.Settings)
	first, found := server.runnerRegistry.Runner(firstID)
	require.True(t, found)
	second, found := server.runnerRegistry.Runner(secondID)
	require.True(t, found)
	assert.Equal(t, first.Workspace.Path, second.Workspace.Path)
	assert.NotEqual(t, first.Host.InstanceID, second.Host.InstanceID)
	store, err := conversations.GetConversationStore(t.Context())
	require.NoError(t, err)
	defer store.Close()
	record := convtypes.NewConversationRecord("legacy-host-fence")
	record.Provider, record.CWD = "anthropic", first.Workspace.Path
	record.Metadata = nil // Early records may predate metadata and snapshots.
	record.RawMessages = json.RawMessage(`[]`)
	require.NoError(t, store.Save(t.Context(), record))
	record, err = store.Load(t.Context(), record.ID)
	require.NoError(t, err)
	client, err := chat.NewControlPlaneChatRunner(endpoint, "web-secret", "")
	require.NoError(t, err)
	params := chat.ConversationAdoptionRequest{RunnerID: firstID}
	preview, err := client.AdoptConversation(t.Context(), record.ID, params)
	require.NoError(t, err)
	params.RunnerID, params.Confirmation = secondID, preview.Confirmation
	_, err = client.AdoptConversation(t.Context(), record.ID, params)
	require.ErrorContains(t, err, "preview changed", "a matching path cannot reuse consent to a different host")
	err = server.runnerRegistry.AdoptConversation(t.Context(), record, firstID, first.Generation+1, "")
	require.ErrorContains(t, err, "connection changed")
	unchanged, err := store.Load(t.Context(), record.ID)
	require.NoError(t, err)
	assert.Equal(t, record, unchanged)
	_, bound := server.runnerRegistry.RunnerForConversation(record.ID)
	assert.False(t, bound)
	// Even an opening/reserved registry lease without a chat receipt conflicts.
	_, err = server.runnerRegistry.OpenRun(t.Context(), firstID, protocol.RunOpenParams{
		RunID: "adoption-reserved", ConversationID: record.ID, CWD: record.CWD, ExpectedCWD: record.CWD,
	})
	require.NoError(t, err)
	err = server.runnerRegistry.AdoptConversation(t.Context(), record, firstID, first.Generation, "")
	require.ErrorIs(t, err, registry.ErrAdoptionConflict)
	require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), "adoption-reserved", registry.RunStatusFailed, nil))
	server.runnerRegistry.ReleasePendingConversationAffinity(record.ID)
	// Fresh explicit confirmation for the second host is allowed; no heuristic
	// silently chooses it, and nil legacy metadata remains adoptable.
	params.Confirmation = ""
	preview, err = client.AdoptConversation(t.Context(), record.ID, params)
	require.NoError(t, err)
	assert.Equal(t, second.Host.InstanceID, preview.HostInstanceID)
	params.Confirmation = preview.Confirmation
	result, err := client.AdoptConversation(t.Context(), record.ID, params)
	require.NoError(t, err)
	assert.True(t, result.Adopted)
}
