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
		Workspace:   workspace,
		Name:        name,
		Schedule:    "CRON_TZ=UTC 0 9 * * *",
		Instruction: "Summarise yesterday's commits",
		CWD:         "/work/" + workspace,
		Enabled:     true,
		NextRunAt:   &next,
	}
	require.NoError(t, store.Create(t.Context(), &schedule, next.Add(-time.Hour)))
	return schedule
}

func TestStoreScopesSchedulesByWorkspace(t *testing.T) {
	store, _ := newTestStore(t)
	next := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	a := createTestSchedule(t, store, "runner-a", "daily", next)
	b := createTestSchedule(t, store, "runner-b", "daily", next)
	duplicate := a
	assert.ErrorIs(t, store.Create(t.Context(), &duplicate, next), errNameTaken)

	schedules, err := store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, a.ID, schedules[0].ID)

	_, err = store.Get(t.Context(), "runner-a", b.ID)
	assert.ErrorIs(t, err, errNotFound)
	b.Workspace = "runner-a"
	assert.ErrorIs(t, store.Update(t.Context(), &b, b.Version, nil, next), errNotFound)
	assert.ErrorIs(t, store.Delete(t.Context(), "runner-a", b.ID), errNotFound)
}

func TestStoreEditsRespectClaimedOccurrences(t *testing.T) {
	store, path := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC) // Friday.
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)

	// The daemon advances the schedule while an edit is awaiting approval.
	daemonStore, err := openStore(t.Context(), path)
	require.NoError(t, err)
	defer daemonStore.Close()
	next := due.Add(24 * time.Hour)
	run := Run{Status: RunRunning, StartedAt: due}
	_, claimed, err := daemonStore.Claim(t.Context(), schedule.ID, due, next, run)
	require.NoError(t, err)
	require.True(t, claimed)
	_, claimed, err = store.Claim(t.Context(), schedule.ID, due, next, run)
	require.NoError(t, err)
	assert.False(t, claimed, "the stale occurrence cannot be claimed twice")

	schedule.Instruction = "New instruction"
	require.NoError(t, store.Update(t.Context(), &schedule, schedule.Version, nil, due))
	saved, err := store.Get(t.Context(), schedule.Workspace, schedule.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.NextRunAt)
	assert.True(t, next.Equal(*saved.NextRunAt), "editing the instruction must not undo the claim")

	// Even with a clock from before the claim, rescheduling skips that Friday.
	saved.Schedule = "CRON_TZ=UTC 0 9 * * 1-5"
	spec, err := parseSpec(saved.Schedule)
	require.NoError(t, err)
	weekdays := func(after time.Time) *time.Time { return nextPtr(spec.Next(after)) }
	require.NoError(t, store.Update(t.Context(), &saved, saved.Version, weekdays, due.Add(-time.Second)))
	saved, err = store.Get(t.Context(), schedule.Workspace, schedule.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.NextRunAt)
	assert.True(t, due.AddDate(0, 0, 3).Equal(*saved.NextRunAt), "next run %s, want Monday", saved.NextRunAt)
}

func TestStoreDisableInvalidatesPendingEdits(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)

	disabled, err := store.Disable(t.Context(), schedule.ID, due, Run{Status: RunFailed, StartedAt: due, FinishedAt: &due, Error: "bad schedule"})
	require.NoError(t, err)
	require.True(t, disabled)
	schedule.Instruction = "changed"
	assert.ErrorIs(t, store.Update(t.Context(), &schedule, schedule.Version, nil, due), errChanged)
	saved, err := store.Get(t.Context(), schedule.Workspace, schedule.ID)
	require.NoError(t, err)
	assert.False(t, saved.Enabled)
	assert.Nil(t, saved.NextRunAt)
	require.NotNil(t, saved.LastRun)
	assert.Equal(t, "bad schedule", saved.LastRun.Error)
}

func TestStoreSettlementPreservesProgressAndRejectsLateResults(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	runID, claimed, err := store.Claim(t.Context(), schedule.ID, due, due.Add(time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, store.SaveRunProgress(t.Context(), runID, runResult{Output: "partial", Stats: RunStats{ToolCalls: 3}}))

	settled, err := store.SettleRun(t.Context(), runID, RunFailed, "model overloaded", "", due.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, settled)
	settled, err = store.SettleRun(t.Context(), runID, RunSucceeded, "", "late", due.Add(2*time.Minute))
	require.NoError(t, err)
	assert.False(t, settled)
	require.NoError(t, store.FinishRun(t.Context(), runID, RunSucceeded, "", runResult{Output: "late"}, due.Add(2*time.Minute)))
	assert.ErrorIs(t, store.SetRunConversation(t.Context(), runID, "late"), errRunGone)

	run, err := store.GetRun(t.Context(), schedule.Workspace, runID)
	require.NoError(t, err)
	assert.Equal(t, RunFailed, run.Status)
	assert.Equal(t, "model overloaded", run.Error)
	assert.Equal(t, "partial", run.Output)
	require.NotNil(t, run.Stats)
	assert.Equal(t, 3, run.Stats.ToolCalls)
}

func TestStoreOnlyCurrentDaemonCanMarkItselfStopped(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	status := DaemonStatus{PID: 42, StartedAt: now, HeartbeatAt: now, PollInterval: 30 * time.Second}
	require.NoError(t, store.SaveHeartbeat(t.Context(), status))

	require.NoError(t, store.MarkDaemonStopped(t.Context(), 7, now))
	saved, found, err := store.Daemon(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	assert.Nil(t, saved.StoppedAt, "another daemon's shutdown is ignored")

	require.NoError(t, store.MarkDaemonStopped(t.Context(), 42, now))
	saved, _, err = store.Daemon(t.Context())
	require.NoError(t, err)
	assert.NotNil(t, saved.StoppedAt)
}

func TestStorePruningKeepsRunningRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "minutely", due)
	running, claimed, err := store.Claim(t.Context(), schedule.ID, due, due.Add(time.Minute), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)
	// A long run makes the following occurrences skip.
	for i := 1; i <= runHistoryLimit+1; i++ {
		at := due.Add(time.Duration(i) * time.Minute)
		_, claimed, err := store.Claim(t.Context(), schedule.ID, at, at.Add(time.Minute), Run{Status: RunSkipped, StartedAt: at, FinishedAt: &at})
		require.NoError(t, err)
		require.True(t, claimed)
	}

	runs, err := store.ListRuns(t.Context(), schedule.Workspace, RunFilter{Limit: runHistoryLimit + 2})
	require.NoError(t, err)
	require.Len(t, runs, runHistoryLimit+1, "the latest runs plus the long one")
	assert.Equal(t, running, runs[runHistoryLimit].ID)
	assert.True(t, due.Add(2*time.Minute).Equal(runs[runHistoryLimit-1].ScheduledFor), "the oldest skipped run was pruned")
	require.NoError(t, store.FinishRun(t.Context(), running, RunSucceeded, "", runResult{}, due.Add(time.Hour)))
	run, err := store.GetRun(t.Context(), schedule.Workspace, running)
	require.NoError(t, err)
	assert.Equal(t, RunSucceeded, run.Status)
}
