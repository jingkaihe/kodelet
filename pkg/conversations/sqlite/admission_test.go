package sqlite

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmissionCheckpointAtomicallyFencesHistoryAndAffinity(t *testing.T) {
	for _, name := range []string{"success", "cancelled", "completed", "wrong run", "wrong conversation", "wrong runner", "closed run", "existing affinity", "summary failure"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoint.db")
			setupTestDB(t, path)
			store, err := NewStore(t.Context(), path)
			require.NoError(t, err)
			defer store.Close()
			now := time.Now().UTC()
			_, err = store.db.Exec(`INSERT INTO runner_registrations
				(id, owner_id, host_instance_id, workspace_path, workspace_name, status, created_at, updated_at)
				VALUES ('runner', 'local', 'host', '/work/project', 'project', 'busy', ?, ?)`, now, now)
			require.NoError(t, err)
			_, err = store.db.Exec(`INSERT INTO runner_runs (id, conversation_id, runner_id, status, created_at, updated_at)
				VALUES ('run', 'conversation', 'runner', 'opening', ?, ?)`, now, now)
			require.NoError(t, err)
			_, err = store.db.Exec(`INSERT INTO chat_turns (conversation_id, turn_id, run_id, status, created_at, updated_at)
				VALUES ('conversation', 'turn', 'run', 'running', ?, ?)`, now, now)
			require.NoError(t, err)
			admission := convtypes.TurnAdmission{ConversationID: "conversation", RunID: "run", RunnerID: "runner", EnvironmentProfile: "restricted"}
			switch name {
			case "cancelled":
				_, err = store.db.Exec(`UPDATE chat_turns SET cancel_requested = true`)
			case "completed":
				_, err = store.db.Exec(`UPDATE chat_turns SET status = 'succeeded'`)
			case "wrong run":
				admission.RunID = "other"
			case "wrong conversation":
				admission.ConversationID = "other"
			case "wrong runner":
				admission.RunnerID = "other"
			case "closed run":
				_, err = store.db.Exec(`UPDATE runner_runs SET status = 'cancelled'`)
			case "existing affinity":
				_, err = store.db.Exec(`INSERT INTO conversation_runner_affinity (conversation_id, runner_id, environment_profile, created_at, updated_at)
					VALUES ('conversation', 'runner', 'different', ?, ?)`, now, now)
			case "summary failure":
				_, err = store.db.Exec(`CREATE TRIGGER fail_summary BEFORE INSERT ON conversation_summaries BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
			}
			require.NoError(t, err)
			record := convtypes.NewConversationRecord("conversation")
			record.CWD, record.Provider = "/work/project", "anthropic"
			record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"admitted input"}]}]`)
			err = store.Save(convtypes.ContextWithTurnAdmission(t.Context(), admission), record)
			if name == "success" {
				require.NoError(t, err)
				// Reload from a fresh connection: all three durable views agree.
				reopened, err := NewStore(t.Context(), path)
				require.NoError(t, err)
				defer reopened.Close()
				loaded, err := reopened.Load(t.Context(), record.ID)
				require.NoError(t, err)
				assert.Equal(t, "runner", loaded.Metadata[convtypes.RunnerIDMetadataKey])
				assert.Equal(t, "restricted", loaded.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey])
				listed, err := reopened.Query(t.Context(), convtypes.QueryOptions{})
				require.NoError(t, err)
				require.Len(t, listed.ConversationSummaries, 1)
				assert.Equal(t, "admitted input", listed.ConversationSummaries[0].FirstMessage)
			} else {
				require.Error(t, err)
				for _, table := range []string{"conversations", "conversation_summaries", "conversation_runner_affinity"} {
					var count int
					require.NoError(t, store.db.Get(&count, "SELECT COUNT(*) FROM "+table))
					if name == "existing affinity" && table == "conversation_runner_affinity" {
						assert.Equal(t, 1, count)
					} else {
						assert.Zero(t, count, table)
					}
				}
			}
		})
	}
}
