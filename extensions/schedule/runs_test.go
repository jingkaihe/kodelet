package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordRun claims the schedule's next run and finishes it.
func recordRun(t *testing.T, store *Store, schedule Schedule, due time.Time, status RunStatus, output string) int64 {
	t.Helper()
	saved, err := store.Get(t.Context(), schedule.Workspace, schedule.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.NextRunAt)
	runID, claimed, err := store.Claim(t.Context(), schedule.ID, *saved.NextRunAt, due.Add(time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, store.SetRunConversation(t.Context(), runID, "conv-"+schedule.Name))
	result := runResult{Output: output, Stats: RunStats{ToolCalls: 1, Tools: map[string]int{"bash": 1}}}
	require.NoError(t, store.FinishRun(t.Context(), runID, status, "", result, due.Add(time.Minute)))
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

	runs, err := store.ListRuns(t.Context(), "runner-a", RunFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, runs, 3)
	assert.Equal(t, []int64{third, second, first}, []int64{runs[0].ID, runs[1].ID, runs[2].ID}, "newest first")
	assert.Equal(t, daily.ID, runs[0].ScheduleID)
	assert.Equal(t, "daily", runs[0].ScheduleName)
	assert.Equal(t, "conv-daily", runs[0].ConversationID)
	assert.Empty(t, runs[0].Output, "listing does not load outputs")
	assert.Empty(t, runs[0].Instruction, "listing does not load instructions")
	require.NotNil(t, runs[0].Stats, "listing loads stats")
	assert.Equal(t, 1, runs[0].Stats.ToolCalls)

	runs, err = store.ListRuns(t.Context(), "runner-a", RunFilter{ScheduleID: daily.ID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, third, runs[0].ID)

	runs, err = store.ListRuns(t.Context(), "runner-a", RunFilter{ScheduleID: other.ID, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, runs, "another workspace's schedule")

	runs, err = store.ListRuns(t.Context(), "runner-a", RunFilter{Status: RunFailed, Limit: 10})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, second, runs[0].ID)
	runs, err = store.ListRuns(t.Context(), "runner-a", RunFilter{ScheduleID: daily.ID, Status: RunFailed, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, runs, "filters combine")

	// Editing the schedule does not rewrite what earlier runs used.
	daily.Instruction = "changed later"
	require.NoError(t, store.Update(t.Context(), &daily, daily.Version, nil, due))
	run, err := store.GetRun(t.Context(), "runner-a", first)
	require.NoError(t, err)
	assert.Equal(t, "first response", run.Output)
	assert.Equal(t, "Summarise yesterday's commits", run.Instruction)
	assert.Equal(t, "/work/runner-a", run.CWD)
	assert.Equal(t, RunSucceeded, run.Status)
	require.NotNil(t, run.FinishedAt)
	require.NotNil(t, run.Stats)
	assert.Equal(t, map[string]int{"bash": 1}, run.Stats.Tools)

	_, err = store.GetRun(t.Context(), "runner-a", theirs)
	assert.ErrorIs(t, err, errRunNotFound)
	_, err = store.GetRun(t.Context(), "runner-a", 9999)
	assert.ErrorIs(t, err, errRunNotFound)
}

func TestDaemonTruncatesLongOutputs(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	long := strings.Repeat("é", maxOutputLength)
	exec := &fakeExecutor{output: long}
	d, clock, _ := newTestDaemon(store, exec, nil)

	clock.Set(due)
	tickAndWait(t, d)

	runs, err := store.ListRuns(t.Context(), "runner-a", RunFilter{ScheduleID: schedule.ID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	run, err := store.GetRun(t.Context(), "runner-a", runs[0].ID)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(run.Output, truncatedOutput))
	assert.LessOrEqual(t, len(run.Output), maxOutputLength+len(truncatedOutput))
	assert.True(t, strings.HasPrefix(run.Output, "éé"))
}
