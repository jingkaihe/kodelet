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

	schedules, err := store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, a.ID, schedules[0].ID)
	assert.Equal(t, "Summarise yesterday's commits", schedules[0].Instruction)
	assert.True(t, next.Equal(*schedules[0].NextRunAt))

	all, err := store.List(t.Context(), "")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	_, err = store.Get(t.Context(), "runner-a", b.ID)
	assert.ErrorIs(t, err, errNotFound)
	b.Workspace = "runner-a"
	assert.ErrorIs(t, store.Update(t.Context(), &b, b.Version, nil, next), errNotFound)
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

	duplicate := Schedule{Workspace: "runner-a", Name: "daily", Schedule: "@daily", Instruction: "p", CWD: "/w", Enabled: true}
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

	schedule.Instruction = "New instruction"
	require.NoError(t, store.Update(t.Context(), &schedule, schedule.Version, nil, due))
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.Equal(t, "New instruction", saved.Instruction)
	assert.True(t, claimedNext.Equal(*saved.NextRunAt), "the stale next run must not undo the claim")

	schedule.Enabled = false
	pause := func(time.Time) *time.Time { return nil }
	require.NoError(t, store.Update(t.Context(), &schedule, schedule.Version, pause, due))
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
	require.NoError(t, store.FinishRun(t.Context(), runID, RunSucceeded, "", runResult{Output: "all good"}, due.Add(time.Minute)))
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
	disabled, err := store.Disable(t.Context(), schedule.ID, last, Run{Status: RunFailed, StartedAt: last, FinishedAt: &last, Error: "bad"})
	require.NoError(t, err)
	require.True(t, disabled)
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.False(t, saved.Enabled)
	assert.Nil(t, saved.NextRunAt)
	assert.Equal(t, "bad", saved.LastRun.Error)
}

func TestStoreSettlesRunningRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	runID, _, err := store.Claim(t.Context(), schedule.ID, due, due.Add(time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.NoError(t, store.SetRunConversation(t.Context(), runID, "conv-1"))
	require.NoError(t, store.SaveRunProgress(t.Context(), runID, runResult{Output: "partial", Stats: RunStats{ToolCalls: 3}}))

	running, err := store.RunningRuns(t.Context())
	require.NoError(t, err)
	require.Len(t, running, 1)
	assert.Equal(t, runID, running[0].ID)
	assert.Equal(t, schedule.ID, running[0].ScheduleID)
	assert.Equal(t, "conv-1", running[0].ConversationID)
	assert.True(t, due.Equal(running[0].StartedAt))

	settled, err := store.SettleRun(t.Context(), runID, RunFailed, "model overloaded", "", due.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, settled)
	settled, err = store.SettleRun(t.Context(), runID, RunSucceeded, "", "late", due.Add(2*time.Minute))
	require.NoError(t, err)
	assert.False(t, settled, "a finished run is never settled again")
	require.NoError(t, store.FinishRun(t.Context(), runID, RunSucceeded, "", runResult{Output: "late"}, due.Add(2*time.Minute)))

	run, err := store.GetRun(t.Context(), "runner-a", runID)
	require.NoError(t, err)
	assert.Equal(t, RunFailed, run.Status)
	assert.Equal(t, "model overloaded", run.Error)
	assert.Equal(t, "partial", run.Output, "an empty settled output keeps the saved progress")
	require.NotNil(t, run.Stats)
	assert.Equal(t, 3, run.Stats.ToolCalls)
	require.NotNil(t, run.FinishedAt)
	running, err = store.RunningRuns(t.Context())
	require.NoError(t, err)
	assert.Empty(t, running)
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

func TestStoreRescheduleSkipsClaimedOccurrences(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC) // A Friday.
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	read, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)

	// The daemon claims 09:00 while an edit read before 09:00 awaits approval.
	_, claimed, err := store.Claim(t.Context(), schedule.ID, due, due.Add(24*time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)

	read.Schedule = "CRON_TZ=UTC 0 9 * * 1-5"
	spec, err := parseSpec(read.Schedule)
	require.NoError(t, err)
	weekdays := func(after time.Time) *time.Time {
		next := spec.Next(after)
		return &next
	}
	// Even with a clock from before 09:00, the claimed occurrence is not
	// queued again.
	require.NoError(t, store.Update(t.Context(), &read, read.Version, weekdays, due.Add(-time.Second)))

	monday := time.Date(2026, 11, 30, 9, 0, 0, 0, time.UTC)
	assert.True(t, monday.Equal(*read.NextRunAt), "next run %s", read.NextRunAt)
	again, err := store.Due(t.Context(), due.Add(time.Minute))
	require.NoError(t, err)
	assert.Empty(t, again)
}

func TestStoreUpdateRejectsStaleEdits(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	pausing, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	renaming := pausing

	pausing.Enabled = false
	require.NoError(t, store.Update(t.Context(), &pausing, pausing.Version, func(time.Time) *time.Time { return nil }, due))
	renaming.Name = "renamed"
	assert.ErrorIs(t, store.Update(t.Context(), &renaming, renaming.Version, nil, due), errChanged)

	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.False(t, saved.Enabled, "the stale rename must not undo the pause")
	assert.Nil(t, saved.NextRunAt)
	assert.Equal(t, "daily", saved.Name)
}

func TestStoreDisableCountsAsAnEdit(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	read, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)

	disabled, err := store.Disable(t.Context(), schedule.ID, due, Run{Status: RunFailed, StartedAt: due, FinishedAt: &due})
	require.NoError(t, err)
	require.True(t, disabled)

	read.Instruction = "changed"
	assert.ErrorIs(t, store.Update(t.Context(), &read, read.Version, nil, due), errChanged)
}

func TestStorePruningKeepsRunningRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "minutely", due)
	running, claimed, err := store.Claim(t.Context(), schedule.ID, due, due.Add(time.Minute), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)
	// A long run makes the following occurrences skip.
	for i := 1; i <= runHistoryLimit+5; i++ {
		at := due.Add(time.Duration(i) * time.Minute)
		_, claimed, err := store.Claim(t.Context(), schedule.ID, at, at.Add(time.Minute), Run{Status: RunSkipped, StartedAt: at, FinishedAt: &at})
		require.NoError(t, err)
		require.True(t, claimed)
	}

	require.NoError(t, store.FinishRun(t.Context(), running, RunSucceeded, "", runResult{Output: "finally"}, due.Add(time.Hour)))

	run, err := store.GetRun(t.Context(), "runner-a", running)
	require.NoError(t, err)
	assert.Equal(t, RunSucceeded, run.Status)
	assert.Equal(t, "finally", run.Output)
	var count int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runs WHERE schedule_id = ?`, schedule.ID).Scan(&count))
	assert.Equal(t, runHistoryLimit+1, count, "the latest runs plus the long one")
}

func TestStoreSetRunConversationRequiresARunningRun(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	finished, _, err := store.Claim(t.Context(), schedule.ID, due, due.Add(time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.NoError(t, store.FinishRun(t.Context(), finished, RunFailed, "boom", runResult{}, due))
	assert.ErrorIs(t, store.SetRunConversation(t.Context(), finished, "conv-1"), errRunGone)

	deleted, _, err := store.Claim(t.Context(), schedule.ID, due.Add(time.Hour), due.Add(2*time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.NoError(t, store.Delete(t.Context(), "runner-a", schedule.ID))
	assert.ErrorIs(t, store.SetRunConversation(t.Context(), deleted, "conv-2"), errRunGone)
	assert.ErrorIs(t, store.SetRunConversation(t.Context(), 9999, "conv-3"), errRunGone)
}
