package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/steer"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func childAdmissionStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "children.db")
	setupTestDB(t, path)
	store, err := NewStore(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UTC()
	_, err = store.db.Exec(`INSERT INTO runner_registrations (id,owner_id,host_instance_id,workspace_path,workspace_name,status,created_at,updated_at)
		VALUES ('runner','local','host','/work','work','ready',?,?)`, now, now)
	require.NoError(t, err)
	return store, path
}

func TestChildAdmissionPreservesHistoryAndTransfersOnlyOwnedGuidance(t *testing.T) {
	store, path := childAdmissionStore(t)
	record := convtypes.NewConversationRecord("child")
	record.Provider, record.CWD = "openai", "/work/subdir"
	record.RawMessages = json.RawMessage(`[{"role":"user","content":"original history"}]`)
	record.Metadata["delegation"] = "original provenance"
	admission := convtypes.ChildAdmission{TurnAdmission: convtypes.TurnAdmission{ConversationID: "child", RunID: "first", RunnerID: "runner"}}
	require.NoError(t, store.Save(convtypes.ContextWithChildAdmission(t.Context(), admission), record))
	queue, err := steer.NewSteerStore(t.Context(), steer.WithDBPath(path))
	require.NoError(t, err)
	defer queue.Close()
	injected, err := queue.EnqueueChild(t.Context(), "child", "first", "next owned followup")
	require.NoError(t, err)
	require.True(t, injected)
	ordinaryPending, err := queue.HasPending(t.Context(), "child")
	require.NoError(t, err)
	assert.False(t, ordinaryPending, "scoped guidance cannot trigger an unrelated ordinary turn continuation")
	childPending, err := queue.HasPending(steer.WithChildRun(t.Context(), "first"), "child")
	require.NoError(t, err)
	assert.True(t, childPending)
	require.NoError(t, store.FinishChildTurn(t.Context(), "child", "first", true, context.Canceled))
	record, err = store.Load(t.Context(), "child")
	require.NoError(t, err)
	before := record
	ordinary, err := queue.Consume(t.Context(), "child")
	require.NoError(t, err)
	assert.Empty(t, ordinary)
	admission.RunID, admission.ExpectedUpdatedAt = "second", record.UpdatedAt
	require.NoError(t, store.Save(convtypes.ContextWithChildAdmission(t.Context(), admission), record))
	loaded, err := store.Load(t.Context(), "child")
	require.NoError(t, err)
	assert.Equal(t, before.RawMessages, loaded.RawMessages)
	assert.Equal(t, before.CreatedAt, loaded.CreatedAt)
	assert.Equal(t, before.Metadata, loaded.Metadata)
	assert.Equal(t, before.CWD, loaded.CWD)
	old, err := queue.Consume(steer.WithChildRun(t.Context(), "first"), "child")
	require.NoError(t, err)
	assert.Empty(t, old)
	pending, err := queue.Consume(steer.WithChildRun(t.Context(), "second"), "child")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "next owned followup", pending[0].Content)
	_, err = queue.EnqueueChild(t.Context(), "child", "first", "stale")
	require.NoError(t, err)
	var receipts int
	require.NoError(t, store.db.Get(&receipts, `SELECT COUNT(*) FROM chat_turns WHERE conversation_id='child'`))
	assert.Equal(t, 2, receipts)
}

func TestChildAdmissionRejectsChangedBusyCancelledAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"ordinary accepted", "ordinary running", "stale record", "moved directory", "same run cancelled", "wrong affinity", "summary failure", "insert exists", "cancelled context"} {
		t.Run(scenario, func(t *testing.T) {
			store, _ := childAdmissionStore(t)
			record := convtypes.NewConversationRecord("child")
			record.Provider = "openai"
			record.RawMessages = json.RawMessage(`[{"role":"user","content":"must survive"}]`)
			require.NoError(t, store.Save(t.Context(), record))
			record, err := store.Load(t.Context(), record.ID)
			require.NoError(t, err)
			admission := convtypes.ChildAdmission{TurnAdmission: convtypes.TurnAdmission{ConversationID: "child", RunID: "new", RunnerID: "runner"}, ExpectedUpdatedAt: record.UpdatedAt}
			ctx := t.Context()
			now := time.Now().UTC()
			switch scenario {
			case "ordinary accepted", "ordinary running":
				status := "accepted"
				if scenario == "ordinary running" {
					status = "running"
				}
				_, err = store.db.Exec(`INSERT INTO chat_turns (conversation_id,turn_id,status,created_at,updated_at) VALUES ('child','ordinary',?,?,?)`, status, now, now)
			case "stale record":
				admission.ExpectedUpdatedAt = now.Add(-time.Hour)
			case "moved directory":
				// Moves retain updated_at, but a stale resume must not restore CWD.
				_, err = store.db.Exec(`UPDATE conversations SET cwd='/new/workspace' WHERE id='child'`)
			case "same run cancelled":
				_, err = store.db.Exec(`INSERT INTO chat_turns (conversation_id,turn_id,status,cancel_requested,created_at,updated_at) VALUES ('child','new','cancelled',TRUE,?,?)`, now, now)
			case "wrong affinity":
				require.NoError(t, store.BindConversationRunnerAffinity(ctx, "child", "runner", "different"))
			case "summary failure":
				_, err = store.db.Exec(`CREATE TRIGGER reject_summary BEFORE INSERT ON conversation_summaries BEGIN SELECT RAISE(ABORT,'summary failure'); END`)
			case "insert exists":
				admission.ExpectedUpdatedAt = time.Time{}
			case "cancelled context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			require.NoError(t, err)
			changed := record
			changed.RawMessages = json.RawMessage(`[]`)
			require.Error(t, store.Save(convtypes.ContextWithChildAdmission(ctx, admission), changed))
			after, err := store.Load(t.Context(), "child")
			require.NoError(t, err)
			assert.Equal(t, record.RawMessages, after.RawMessages)
			assert.Equal(t, record.UpdatedAt, after.UpdatedAt)
			if scenario == "moved directory" {
				assert.Equal(t, "/new/workspace", after.CWD)
			}
			var count int
			require.NoError(t, store.db.Get(&count, `SELECT COUNT(*) FROM chat_turns WHERE conversation_id='child' AND turn_id='new' AND status='running'`))
			assert.Zero(t, count)
		})
	}
}

func TestChildAdmissionConcurrentWithOrdinaryReceipt(t *testing.T) {
	store, _ := childAdmissionStore(t)
	record := convtypes.NewConversationRecord("child")
	record.Provider = "openai"
	record.RawMessages = json.RawMessage(`[]`)
	admission := convtypes.ChildAdmission{TurnAdmission: convtypes.TurnAdmission{ConversationID: "child", RunID: "child-run", RunnerID: "runner"}}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		results <- store.Save(convtypes.ContextWithChildAdmission(t.Context(), admission), record)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := store.db.Exec(`INSERT INTO chat_turns (conversation_id,turn_id,status,created_at,updated_at) VALUES ('child','ordinary','accepted',?,?)`, time.Now().UTC(), time.Now().UTC())
		results <- err
	}()
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	assert.Equal(t, 1, success, "unique active receipt linearizes child against ordinary admission")
}
