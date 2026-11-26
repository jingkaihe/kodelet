package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordRun claims the schedule's next run and finishes it.
func recordRun(t *testing.T, store *Store, schedule Schedule, due time.Time, status RunStatus, response string) int64 {
	t.Helper()
	saved, err := store.Get(t.Context(), schedule.Workspace, schedule.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.NextRunAt)
	runID, claimed, err := store.Claim(t.Context(), schedule.ID, *saved.NextRunAt, due.Add(time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, store.SetRunConversation(t.Context(), runID, "conv-"+schedule.Name))
	require.NoError(t, store.FinishRun(t.Context(), runID, status, "", response, due.Add(time.Minute)))
	return runID
}

func TestStoreListsAndGetsRunsInAWorkspace(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	daily := createTestSchedule(t, store, "runner-a", "daily", due)
	weekly := createTestSchedule(t, store, "runner-a", "weekly", due)
	other := createTestSchedule(t, store, "runner-b", "theirs", due)

	first := recordRun(t, store, daily, due, RunSucceeded, "first response")
	second := recordRun(t, store, weekly, due.Add(time.Hour), RunFailed, "")
	third := recordRun(t, store, daily, due.Add(2*time.Hour), RunSucceeded, "third response")
	theirs := recordRun(t, store, other, due, RunSucceeded, "secret")

	runs, err := store.ListRuns(t.Context(), "runner-a", "", 10)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	assert.Equal(t, []int64{third, second, first}, []int64{runs[0].ID, runs[1].ID, runs[2].ID}, "newest first")
	assert.Equal(t, daily.ID, runs[0].ScheduleID)
	assert.Equal(t, "daily", runs[0].ScheduleName)
	assert.Equal(t, "conv-daily", runs[0].ConversationID)
	assert.Empty(t, runs[0].Response, "listing does not load responses")

	runs, err = store.ListRuns(t.Context(), "runner-a", daily.ID, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, third, runs[0].ID)

	runs, err = store.ListRuns(t.Context(), "runner-a", other.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, runs, "another workspace's schedule")

	run, err := store.GetRun(t.Context(), "runner-a", first)
	require.NoError(t, err)
	assert.Equal(t, "first response", run.Response)
	assert.Equal(t, RunSucceeded, run.Status)
	require.NotNil(t, run.FinishedAt)

	_, err = store.GetRun(t.Context(), "runner-a", theirs)
	assert.ErrorIs(t, err, errRunNotFound)
	_, err = store.GetRun(t.Context(), "runner-a", 9999)
	assert.ErrorIs(t, err, errRunNotFound)
}

func TestResponseMigrationKeepsExistingRuns(t *testing.T) {
	path := t.TempDir() + "/schedule.db"
	db := openTestDB(t, path)
	initial := scheduleMigrations()[:1]
	require.NoError(t, migrate(t.Context(), db, initial))
	_, err := db.ExecContext(t.Context(), `
INSERT INTO schedules (id, workspace, name, schedule, instruction, cwd, created_at, updated_at)
VALUES ('s1', 'runner-a', 'daily', '@daily', 'p', '/w', 0, 0);
INSERT INTO runs (schedule_id, scheduled_for, started_at, finished_at, status, conversation_id)
VALUES ('s1', 0, 0, 1, 'succeeded', 'conv-1');`)
	require.NoError(t, err)

	store, err := openStore(t.Context(), path)
	require.NoError(t, err)
	defer store.Close()

	assert.Len(t, appliedVersions(t, db), len(scheduleMigrations()))
	runs, err := store.ListRuns(t.Context(), "runner-a", "", 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	run, err := store.GetRun(t.Context(), "runner-a", runs[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "conv-1", run.ConversationID)
	assert.Empty(t, run.Response)
}

func TestDaemonRecordsResponses(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	long := strings.Repeat("é", maxResponseLength)
	exec := &fakeExecutor{response: long, run: func(context.Context, Schedule) error { return nil }}
	d, clock := newTestDaemon(store, exec, nil)

	clock.Set(due)
	tickAndWait(t, d)

	runs, err := store.ListRuns(t.Context(), "runner-a", schedule.ID, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	run, err := store.GetRun(t.Context(), "runner-a", runs[0].ID)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(run.Response, truncatedResponse))
	assert.LessOrEqual(t, len(run.Response), maxResponseLength+len(truncatedResponse))
	assert.True(t, strings.HasPrefix(run.Response, "éé"))
}
