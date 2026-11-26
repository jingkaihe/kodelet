package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schedule", "schedule.db")
	store, err := openStore(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store, path
}

func createTestSchedule(t *testing.T, store *Store, workspace, name string, next time.Time) Schedule {
	t.Helper()
	schedule := Schedule{
		Workspace: workspace,
		Name:      name,
		Schedule:  "CRON_TZ=UTC 0 9 * * *",
		Task:      "Summarise yesterday's commits",
		CWD:       "/work/" + workspace,
		Enabled:   true,
		NextRunAt: &next,
	}
	require.NoError(t, store.Create(t.Context(), &schedule, next.Add(-time.Hour)))
	return schedule
}

func TestStoreScopesSchedulesByWorkspace(t *testing.T) {
	store, _ := newTestStore(t)
	next := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	a := createTestSchedule(t, store, "runner-a", "daily", next)
	b := createTestSchedule(t, store, "runner-b", "daily", next)

	schedules, err := store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, a.ID, schedules[0].ID)
	assert.Equal(t, "Summarise yesterday's commits", schedules[0].Task)
	assert.True(t, next.Equal(*schedules[0].NextRunAt))

	all, err := store.List(t.Context(), "")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	_, err = store.Get(t.Context(), "runner-a", b.ID)
	assert.ErrorIs(t, err, errNotFound)
	b.Workspace = "runner-a"
	assert.ErrorIs(t, store.Update(t.Context(), &b, false, next), errNotFound)
	assert.ErrorIs(t, store.Delete(t.Context(), "runner-a", b.ID), errNotFound)

	require.NoError(t, store.Delete(t.Context(), "runner-b", b.ID))
	all, err = store.List(t.Context(), "")
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestStoreRejectsDuplicateNamesInAWorkspace(t *testing.T) {
	store, _ := newTestStore(t)
	next := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	createTestSchedule(t, store, "runner-a", "daily", next)

	duplicate := Schedule{Workspace: "runner-a", Name: "daily", Schedule: "@daily", Task: "p", CWD: "/w", Enabled: true}
	assert.ErrorIs(t, store.Create(t.Context(), &duplicate, next), errNameTaken)
}

func TestStoreUpdateKeepsClaimedNextRunUnlessReset(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)

	// The daemon claims the run while an edit is awaiting approval.
	claimedNext := due.Add(24 * time.Hour)
	_, claimed, err := store.Claim(t.Context(), schedule.ID, due, claimedNext, Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)

	schedule.Task = "New task"
	require.NoError(t, store.Update(t.Context(), &schedule, false, due))
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.Equal(t, "New task", saved.Task)
	assert.True(t, claimedNext.Equal(*saved.NextRunAt), "the stale next run must not undo the claim")

	schedule.Enabled = false
	schedule.NextRunAt = nil
	require.NoError(t, store.Update(t.Context(), &schedule, true, due))
	saved, err = store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.False(t, saved.Enabled)
	assert.Nil(t, saved.NextRunAt)
}

func TestStoreClaimIsExclusiveAndRecordsRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)

	schedules, err := store.Due(t.Context(), due.Add(-time.Second))
	require.NoError(t, err)
	assert.Empty(t, schedules)
	schedules, err = store.Due(t.Context(), due)
	require.NoError(t, err)
	require.Len(t, schedules, 1)

	next := due.Add(24 * time.Hour)
	runID, claimed, err := store.Claim(t.Context(), schedule.ID, due, next, Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)
	_, claimed, err = store.Claim(t.Context(), schedule.ID, due, next, Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	assert.False(t, claimed, "a run is claimed once")

	require.NoError(t, store.SetRunConversation(t.Context(), runID, "conv-1"))
	require.NoError(t, store.FinishRun(t.Context(), runID, RunSucceeded, "", due.Add(time.Minute)))
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.LastRun)
	assert.Equal(t, RunSucceeded, saved.LastRun.Status)
	assert.Equal(t, "conv-1", saved.LastRun.ConversationID)
	assert.True(t, due.Equal(saved.LastRun.ScheduledFor))
	assert.True(t, next.Equal(*saved.NextRunAt))

	schedules, err = store.Due(t.Context(), due)
	require.NoError(t, err)
	assert.Empty(t, schedules)
}

func TestStoreDisableAndPruneRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)

	for i := range runHistoryLimit + 5 {
		next := due.Add(time.Duration(i+1) * time.Minute)
		_, claimed, err := store.Claim(t.Context(), schedule.ID, due.Add(time.Duration(i)*time.Minute), next,
			Run{Status: RunSkipped, StartedAt: due, FinishedAt: &due})
		require.NoError(t, err)
		require.True(t, claimed)
	}
	var runs int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runs WHERE schedule_id = ?`, schedule.ID).Scan(&runs))
	assert.Equal(t, runHistoryLimit, runs)

	last := due.Add(time.Duration(runHistoryLimit+5) * time.Minute)
	_, disabled, err := store.Disable(t.Context(), schedule.ID, last, Run{Status: RunFailed, StartedAt: last, FinishedAt: &last, Error: "bad"})
	require.NoError(t, err)
	require.True(t, disabled)
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.False(t, saved.Enabled)
	assert.Nil(t, saved.NextRunAt)
	assert.Equal(t, "bad", saved.LastRun.Error)
}

func TestStoreAbandonsRunningRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	_, _, err := store.Claim(t.Context(), schedule.ID, due, due.Add(time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)

	abandoned, err := store.AbandonRunningRuns(t.Context(), interruptedMessage, due.Add(time.Minute))
	require.NoError(t, err)
	assert.EqualValues(t, 1, abandoned)
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.Equal(t, RunFailed, saved.LastRun.Status)
	assert.Equal(t, interruptedMessage, saved.LastRun.Error)
	require.NotNil(t, saved.LastRun.FinishedAt)
}

func TestStoreDaemonHeartbeat(t *testing.T) {
	store, _ := newTestStore(t)
	_, found, err := store.Daemon(t.Context())
	require.NoError(t, err)
	assert.False(t, found)

	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	status := DaemonStatus{PID: 42, Hostname: "host", Version: "test", StartedAt: now, HeartbeatAt: now, PollInterval: 30 * time.Second}
	require.NoError(t, store.SaveHeartbeat(t.Context(), status))
	saved, found, err := store.Daemon(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 42, saved.PID)
	assert.Equal(t, 30*time.Second, saved.PollInterval)
	assert.Nil(t, saved.StoppedAt)

	require.NoError(t, store.MarkDaemonStopped(t.Context(), 7, now), "another daemon's shutdown is ignored")
	saved, _, err = store.Daemon(t.Context())
	require.NoError(t, err)
	assert.Nil(t, saved.StoppedAt)

	require.NoError(t, store.MarkDaemonStopped(t.Context(), 42, now))
	saved, _, err = store.Daemon(t.Context())
	require.NoError(t, err)
	require.NotNil(t, saved.StoppedAt)
}

func TestOpenStoreIsIdempotentAndSharedAcrossHandles(t *testing.T) {
	store, path := newTestStore(t)
	next := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	createTestSchedule(t, store, "runner-a", "daily", next)

	other, err := openStore(t.Context(), path)
	require.NoError(t, err)
	defer other.Close()
	schedules, err := other.List(t.Context(), "runner-a")
	require.NoError(t, err)
	assert.Len(t, schedules, 1)
}

func TestDefaultDBPath(t *testing.T) {
	base := t.TempDir()
	t.Setenv("KODELET_SCHEDULE_DB", "")
	t.Setenv("KODELET_BASE_PATH", base)
	path, err := defaultDBPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "schedule", "schedule.db"), path)

	t.Setenv("KODELET_SCHEDULE_DB", filepath.Join(base, "custom.db"))
	path, err = defaultDBPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "custom.db"), path)
}
