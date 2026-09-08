package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type conversationMoveFixture struct {
	server *Server
	client *chat.Client
	store  conversations.ConversationStore
	url    string
}

func newConversationMoveFixture(t *testing.T) *conversationMoveFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	config := embeddedRunnerTestConfig(t)
	config.EmbeddedRunner = nil
	server, err := NewServer(t.Context(), config, testFrontendHandler())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, server.Close()) })
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	client, err := chat.NewClient(httpServer.URL, "web-secret", "")
	require.NoError(t, err)
	store, err := conversations.GetConversationStore(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, store.Close()) })
	return &conversationMoveFixture{server: server, client: client, store: store, url: httpServer.URL}
}

func (f *conversationMoveFixture) register(t *testing.T, name, state string) (protocol.RegisterResult, *runnerAPITestLink) {
	t.Helper()
	link := newRunnerAPITestLink()
	link.call = func(_ context.Context, method string, _, _ any) error {
		assert.Fail(t, "conversation moves must not call either runner", method)
		return errors.New("unexpected runner RPC")
	}
	registered, err := f.server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version}, DisplayName: name,
		Host:      protocol.Host{InstanceID: name, Hostname: name, OS: "linux", Arch: "amd64"},
		Workspace: protocol.Workspace{Path: "/runner/" + name, Name: name},
	}, link)
	require.NoError(t, err)
	switch state {
	case "ready":
		require.NoError(t, f.server.runnerRegistry.Heartbeat(registered.RunnerID, registered.ConnectionID, registered.Generation, protocol.HeartbeatParams{
			RunnerID: registered.RunnerID, Generation: registered.Generation, State: protocol.RunnerStateIdle,
		}))
	case "offline":
		f.server.runnerRegistry.Detach(registered.RunnerID, registered.ConnectionID, registered.Generation, nil)
	case "unready":
		// Registration without a first heartbeat must still be a usable destination.
	default:
		require.FailNow(t, "unexpected runner state", state)
	}
	return registered, link
}

func (f *conversationMoveFixture) save(t *testing.T, record convtypes.ConversationRecord) convtypes.ConversationRecord {
	t.Helper()
	require.NoError(t, f.store.Save(t.Context(), record))
	return f.load(t, record.ID)
}

func (f *conversationMoveFixture) load(t *testing.T, id string) convtypes.ConversationRecord {
	t.Helper()
	record, err := f.store.Load(t.Context(), id)
	require.NoError(t, err)
	return record
}

func (f *conversationMoveFixture) move(t *testing.T, id string, params chat.ConversationMoveRequest) chat.ConversationMoveResult {
	t.Helper()
	plan, err := f.client.MoveConversation(t.Context(), id, params)
	require.NoError(t, err)
	require.False(t, plan.Moved)
	require.NotEmpty(t, plan.Confirmation)
	params.Confirmation = plan.Confirmation
	result, err := f.client.MoveConversation(t.Context(), id, params)
	require.NoError(t, err)
	require.True(t, result.Moved)
	return result
}

func (f *conversationMoveFixture) assertDestination(t *testing.T, original convtypes.ConversationRecord, runnerID, cwd, profile string) {
	t.Helper()
	expected := original
	expected.CWD = cwd
	expected.Metadata = maps.Clone(original.Metadata)
	if expected.Metadata == nil {
		expected.Metadata = make(map[string]any)
	}
	expected.Metadata[convtypes.RunnerIDMetadataKey] = runnerID
	expected.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey] = profile
	assert.Equal(t, expected, f.load(t, original.ID), "only destination metadata and directory may change")
	// Store.Load overlays affinity, so inspect raw JSON too: otherwise a move
	// could look correct while leaving the stored metadata null or stale.
	for _, table := range []string{"conversations", "conversation_summaries"} {
		var raw string
		require.NoError(t, f.server.turns.db.GetContext(t.Context(), &raw, `SELECT metadata FROM `+table+` WHERE id = ?`, original.ID))
		var metadata map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &metadata))
		assert.Equal(t, expected.Metadata, metadata, table)
	}
	affinity, found, err := f.server.runnerRegistry.ResolveConversationAffinity(t.Context(), original.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, registry.ConversationAffinity{RunnerID: runnerID, EnvironmentProfile: profile}, affinity)
	listed, err := f.store.Query(t.Context(), convtypes.QueryOptions{RunnerID: runnerID, CWD: cwd})
	require.NoError(t, err)
	require.Len(t, listed.ConversationSummaries, 1)
	summary := listed.ConversationSummaries[0]
	assert.Equal(t, original.ID, summary.ID)
	assert.Equal(t, cwd, summary.CWD)
	assert.Equal(t, expected.Metadata, summary.Metadata)
	assert.Equal(t, original.Summary, summary.Summary)
	assert.Equal(t, original.Usage, summary.Usage)
	assert.Equal(t, original.CreatedAt, summary.CreatedAt)
	assert.Equal(t, original.UpdatedAt, summary.UpdatedAt)
}

func TestMovePreservesHistoryAcrossOfflineRunners(t *testing.T) {
	f := newConversationMoveFixture(t)
	first, _ := f.register(t, "source", "offline")
	second, _ := f.register(t, "destination", "offline")
	record := convtypes.NewConversationRecord("move-history")
	record.Provider, record.CWD, record.Summary = "unavailable-provider", "/old/missing/workspace", "history survives moving"
	record.CreatedAt = time.Date(2024, 7, 1, 2, 3, 4, 0, time.UTC)
	record.RawMessages = json.RawMessage(`[{"role":"user","content":"preserved question"}]`)
	record.Usage = llmtypes.Usage{InputTokens: 17, OutputTokens: 9, InputCost: 0.25}
	record.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey] = "unavailable-profile"
	record.Metadata[conversations.ConfigSnapshotMetadataKey] = map[string]any{"version": 999, "model": "unavailable-model", "profile": "removed-model-profile"}
	record.Metadata["custom"] = map[string]any{"unchanged": "yes"}
	record.Metadata["session_extension_ids"] = []string{"reattach-on-resume"}
	record = f.save(t, record)
	params := chat.ConversationMoveRequest{RunnerID: first.RunnerID}
	plan, err := f.client.MoveConversation(t.Context(), record.ID, params)
	require.NoError(t, err)
	assert.False(t, plan.Moved)
	assert.Empty(t, plan.SourceRunnerID)
	assert.Equal(t, record.CWD, plan.SourceCWD)
	assert.Equal(t, record.CWD, plan.CWD)
	assert.Equal(t, "source", plan.RunnerName)
	assert.Equal(t, "unavailable-profile", plan.EnvironmentProfile)
	assert.Equal(t, record, f.load(t, record.ID), "planning is read-only")
	_, bound, err := f.server.runnerRegistry.ResolveConversationAffinity(t.Context(), record.ID)
	require.NoError(t, err)
	assert.False(t, bound)
	f.move(t, record.ID, params)
	f.assertDestination(t, record, first.RunnerID, record.CWD, "unavailable-profile")

	result := f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: second.RunnerID})
	assert.Equal(t, first.RunnerID, result.SourceRunnerID)
	assert.Equal(t, record.CWD, result.SourceCWD)
	f.assertDestination(t, record, second.RunnerID, record.CWD, "unavailable-profile")
	oldList, err := f.store.Query(t.Context(), convtypes.QueryOptions{RunnerID: first.RunnerID})
	require.NoError(t, err)
	assert.Empty(t, oldList.ConversationSummaries)

	missing := filepath.Join(t.TempDir(), "absent", "project with spaces:branch")
	f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: first.RunnerID, CWD: missing})
	f.assertDestination(t, record, first.RunnerID, missing, "unavailable-profile")
	_, err = os.Stat(missing)
	assert.ErrorIs(t, err, os.ErrNotExist, "moving must not create the destination directory")
	before := f.load(t, record.ID)
	f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: first.RunnerID})
	assert.Equal(t, before, f.load(t, record.ID), "moving to the current destination is a harmless no-op")
}

func TestMoveAcceptsUnreadyDestinationsAndLegacyMetadata(t *testing.T) {
	for _, scenario := range []string{"nil metadata", "metadata provenance", "empty saved directory", "authoritative profile"} {
		t.Run(scenario, func(t *testing.T) {
			f := newConversationMoveFixture(t)
			source, _ := f.register(t, "source", "offline")
			target, _ := f.register(t, "target", "unready")
			record := convtypes.NewConversationRecord("legacy")
			record.CWD = "/unavailable/saved/directory"
			profile := ""
			switch scenario {
			case "nil metadata":
				record.Metadata = nil
			case "metadata provenance":
				record.Metadata[convtypes.RunnerIDMetadataKey] = "no-longer-registered"
				record.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey] = "saved-profile"
				profile = "saved-profile"
			case "empty saved directory":
				record.CWD = ""
			case "authoritative profile":
				record.Metadata[convtypes.RunnerIDMetadataKey] = source.RunnerID
				record.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey] = "stale-metadata-profile"
				profile = "durable-profile"
			}
			record = f.save(t, record)
			if scenario == "authoritative profile" {
				require.NoError(t, f.server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), record.ID, source.RunnerID, profile))
			}
			f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: target.RunnerID})
			f.assertDestination(t, record, target.RunnerID, record.CWD, profile)
		})
	}
}

func TestMoveConfirmationIgnoresRunnerReadinessAndGeneration(t *testing.T) {
	f := newConversationMoveFixture(t)
	target, _ := f.register(t, "target", "unready")
	record := f.save(t, convtypes.NewConversationRecord("offline-confirmation"))
	params := chat.ConversationMoveRequest{RunnerID: target.RunnerID, CWD: "/not/validated"}
	plan, err := f.client.MoveConversation(t.Context(), record.ID, params)
	require.NoError(t, err)
	f.server.runnerRegistry.Detach(target.RunnerID, target.ConnectionID, target.Generation, nil)
	reattached, _ := f.register(t, "target", "ready")
	assert.Equal(t, target.RunnerID, reattached.RunnerID)
	assert.Greater(t, reattached.Generation, target.Generation)
	f.server.runnerRegistry.Detach(reattached.RunnerID, reattached.ConnectionID, reattached.Generation, nil)
	params.Confirmation = plan.Confirmation
	result, err := f.client.MoveConversation(t.Context(), record.ID, params)
	require.NoError(t, err, "disconnects and reconnects must not invalidate a metadata-only move")
	assert.True(t, result.Moved)
	f.assertDestination(t, record, target.RunnerID, params.CWD, "")
}

func TestMoveRejectsStaleConfirmationAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"wrong confirmation", "target runner", "target directory", "target name", "source affinity", "changed history", "summary rollback", "accepted turn", "running turn", "opening run", "running run", "active chat"} {
		t.Run(scenario, func(t *testing.T) {
			f := newConversationMoveFixture(t)
			source, _ := f.register(t, "source", "offline")
			target, _ := f.register(t, "target", "offline")
			other, _ := f.register(t, "other", "offline")
			record := convtypes.NewConversationRecord("move-conflict")
			record.CWD = "/saved/workspace"
			record = f.save(t, record)
			f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: source.RunnerID})
			record = f.load(t, record.ID)
			affinity := registry.ConversationAffinity{RunnerID: source.RunnerID}
			params := chat.ConversationMoveRequest{RunnerID: target.RunnerID, CWD: "/destination/workspace"}
			plan, err := f.client.MoveConversation(t.Context(), record.ID, params)
			require.NoError(t, err)
			params.Confirmation = plan.Confirmation
			expectedRunner := source.RunnerID
			expectedSummaryMetadata := maps.Clone(record.Metadata)
			switch scenario {
			case "wrong confirmation":
				params.Confirmation = "wrong"
			case "target runner":
				params.RunnerID = other.RunnerID
			case "target directory":
				params.CWD = "/different/destination"
			case "target name":
				registered, err := f.server.runnerRegistry.Register(protocol.RegisterParams{
					ProtocolVersions: []int{protocol.Version}, DisplayName: "renamed destination",
					Host:      protocol.Host{InstanceID: "target", Hostname: "target", OS: "linux", Arch: "amd64"},
					Workspace: protocol.Workspace{Path: "/runner/target", Name: "target"},
				}, newRunnerAPITestLink())
				require.NoError(t, err)
				require.Equal(t, target.RunnerID, registered.RunnerID)
			case "source affinity":
				_, err := f.server.turns.db.Exec(`UPDATE conversation_runner_affinity SET runner_id = ? WHERE conversation_id = ?`, other.RunnerID, record.ID)
				require.NoError(t, err)
				expectedRunner = other.RunnerID
				require.ErrorIs(t, f.server.runnerRegistry.MoveConversation(t.Context(), record, affinity, target.RunnerID, params.CWD), registry.ErrMoveConflict)
				record = f.load(t, record.ID) // Load overlays authoritative affinity even before raw metadata is updated.
			case "changed history":
				record.RawMessages = json.RawMessage(`[{"role":"user","content":"newer history"}]`)
				record = f.save(t, record)
			case "summary rollback":
				_, err := f.server.turns.db.Exec(`CREATE TRIGGER reject_move_summary BEFORE UPDATE ON conversation_summaries BEGIN SELECT RAISE(ABORT, 'summary unavailable'); END`)
				require.NoError(t, err)
			case "accepted turn", "running turn":
				status := "accepted"
				if scenario == "running turn" {
					status = "running"
				}
				_, err := f.server.turns.db.Exec(`INSERT INTO chat_turns (conversation_id, turn_id, status, created_at, updated_at) VALUES (?, 'active', ?, ?, ?)`, record.ID, status, time.Now(), time.Now())
				require.NoError(t, err)
				require.ErrorIs(t, f.server.runnerRegistry.MoveConversation(t.Context(), record, affinity, target.RunnerID, params.CWD), registry.ErrMoveConflict, "the transaction must reject turns admitted after planning")
			case "opening run", "running run":
				status := "opening"
				if scenario == "running run" {
					status = "running"
				}
				_, err := f.server.turns.db.Exec(`INSERT INTO runner_runs (id, conversation_id, runner_id, status, created_at, updated_at) VALUES ('active', ?, ?, ?, ?, ?)`, record.ID, source.RunnerID, status, time.Now(), time.Now())
				require.NoError(t, err)
				require.ErrorIs(t, f.server.runnerRegistry.MoveConversation(t.Context(), record, affinity, target.RunnerID, params.CWD), registry.ErrMoveConflict, "durable reservations must be checked even without a cached lease")
			case "active chat":
				run := newActiveChatRun(func() {})
				require.True(t, f.server.registerActiveChat(record.ID, run))
				t.Cleanup(func() { f.server.unregisterActiveChat(record.ID, run) })
			}
			_, err = f.client.MoveConversation(t.Context(), record.ID, params)
			var httpErr *chat.ControlPlaneHTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusConflict, httpErr.StatusCode)
			assert.Equal(t, record, f.load(t, record.ID), "failed moves must not mutate conversation history or destination")
			current, found, err := f.server.runnerRegistry.ResolveConversationAffinity(t.Context(), record.ID)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, expectedRunner, current.RunnerID)
			listed, err := f.store.Query(t.Context(), convtypes.QueryOptions{RunnerID: expectedRunner, CWD: record.CWD})
			require.NoError(t, err)
			require.Len(t, listed.ConversationSummaries, 1)
			assert.Equal(t, expectedSummaryMetadata, listed.ConversationSummaries[0].Metadata)
		})
	}
}

func TestMoveConcurrentConfirmationsCommitOnce(t *testing.T) {
	f := newConversationMoveFixture(t)
	source, _ := f.register(t, "source", "offline")
	target, _ := f.register(t, "target", "offline")
	record := f.save(t, convtypes.NewConversationRecord("concurrent-move"))
	f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: source.RunnerID})
	params := chat.ConversationMoveRequest{RunnerID: target.RunnerID, CWD: "/new/workspace"}
	plan, err := f.client.MoveConversation(t.Context(), record.ID, params)
	require.NoError(t, err)
	params.Confirmation = plan.Confirmation
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			<-start
			_, err := f.client.MoveConversation(t.Context(), record.ID, params)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	first, second := <-results, <-results
	assert.True(t, (first == nil) != (second == nil), "exactly one commit succeeds: %v, %v", first, second)
	f.assertDestination(t, record, target.RunnerID, params.CWD, "")
}

func TestMoveRejectsActiveRegistryReservation(t *testing.T) {
	f := newConversationMoveFixture(t)
	source, link := f.register(t, "source", "ready")
	target, _ := f.register(t, "target", "offline")
	record := convtypes.NewConversationRecord("reserved-move")
	record.CWD = "/runner/source"
	record = f.save(t, record)
	f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: source.RunnerID})
	record = f.load(t, record.ID)
	params := chat.ConversationMoveRequest{RunnerID: target.RunnerID}
	plan, err := f.client.MoveConversation(t.Context(), record.ID, params)
	require.NoError(t, err)
	params.Confirmation = plan.Confirmation
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	link.call = func(ctx context.Context, method string, value, result any) error {
		switch method {
		case protocol.MethodRunOpen:
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			open := value.(protocol.RunOpenParams)
			manifest := runnerpayload.Manifest{
				ProtocolVersion: protocol.Version, RunnerID: source.RunnerID, Generation: source.Generation,
				RunID: open.RunID, WorkingDirectory: open.CWD,
			}
			var err error
			manifest.Digest, err = runnerpayload.ComputeManifestDigest(manifest)
			if err != nil {
				return err
			}
			*result.(*runnerpayload.Manifest) = manifest
		case protocol.MethodRunClose:
		default:
			assert.Fail(t, "unexpected runner RPC", method)
		}
		return nil
	}
	opened := make(chan error, 1)
	go func() {
		_, err := f.server.runnerRegistry.OpenRun(t.Context(), source.RunnerID, protocol.RunOpenParams{
			RunID: "reserved", ConversationID: record.ID, CWD: record.CWD, ExpectedCWD: record.CWD,
		})
		opened <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "run.open did not reserve the conversation")
	}
	for _, state := range []string{"opening", "running"} {
		t.Run(state, func(t *testing.T) {
			if state == "running" {
				unblock()
				require.NoError(t, <-opened)
			}
			affinity := registry.ConversationAffinity{RunnerID: source.RunnerID}
			require.ErrorIs(t, f.server.runnerRegistry.MoveConversation(t.Context(), record, affinity, target.RunnerID, record.CWD), registry.ErrMoveConflict)
			_, err := f.client.MoveConversation(t.Context(), record.ID, params)
			require.Error(t, err)
			assert.Equal(t, record, f.load(t, record.ID))
		})
	}
	require.NoError(t, f.server.runnerRegistry.CloseRun(t.Context(), "reserved", registry.RunStatusSucceeded, nil))
	f.move(t, record.ID, chat.ConversationMoveRequest{RunnerID: target.RunnerID})
	f.assertDestination(t, record, target.RunnerID, record.CWD, "")
}

func TestMoveRejectsUnknownTargetsMalformedRequestsAndUnauthorizedClients(t *testing.T) {
	f := newConversationMoveFixture(t)
	target, _ := f.register(t, "target", "offline")
	record := f.save(t, convtypes.NewConversationRecord("request-validation"))
	for _, test := range []struct{ id, runnerID string }{{"missing", target.RunnerID}, {record.ID, "unknown-runner"}} {
		_, err := f.client.MoveConversation(t.Context(), test.id, chat.ConversationMoveRequest{RunnerID: test.runnerID})
		var httpErr *chat.ControlPlaneHTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusNotFound, httpErr.StatusCode)
	}
	for _, body := range []string{
		`{}`, `null`, `{`, `{"runnerId":" "}`,
		`{"runnerId":"` + target.RunnerID + `","cwd":" "}`,
		`{"runnerId":"` + target.RunnerID + `","rawMessages":[]}`,
		`{"runnerId":"` + target.RunnerID + `","environmentProfile":"replacement"}`,
		`{"runnerId":"` + target.RunnerID + `"} {}`,
	} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.url+"/api/conversations/"+record.ID+"/move", bytes.NewBufferString(body))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer web-secret")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, response.StatusCode, body)
		require.NoError(t, response.Body.Close())
	}
	unauthorized, err := chat.NewClient(f.url, "wrong", "")
	require.NoError(t, err)
	_, err = unauthorized.MoveConversation(t.Context(), record.ID, chat.ConversationMoveRequest{RunnerID: target.RunnerID})
	var httpErr *chat.ControlPlaneHTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusUnauthorized, httpErr.StatusCode)
	assert.Equal(t, record, f.load(t, record.ID))
	_, bound, err := f.server.runnerRegistry.ResolveConversationAffinity(t.Context(), record.ID)
	require.NoError(t, err)
	assert.False(t, bound)
}
