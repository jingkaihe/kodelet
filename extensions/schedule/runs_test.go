package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHistoryListsSummariesAndPreservesClaimedDetails(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	id, claimed, err := store.Claim(t.Context(), schedule.ID, due, due.Add(24*time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, store.FinishRun(t.Context(), id, RunSucceeded, "", runResult{Output: "digest"}, due.Add(time.Minute)))

	instruction := schedule.Instruction
	schedule.Instruction = "changed later"
	require.NoError(t, store.Update(t.Context(), &schedule, schedule.Version, nil, due.Add(time.Hour)))

	runs, err := store.ListRuns(t.Context(), schedule.Workspace, RunFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Empty(t, runs[0].Instruction)
	assert.Empty(t, runs[0].Output)
	run, err := store.GetRun(t.Context(), schedule.Workspace, id)
	require.NoError(t, err)
	assert.Equal(t, instruction, run.Instruction)
	assert.Equal(t, "digest", run.Output)
}

func TestDaemonTruncatesLongOutputs(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	prefix := strings.Repeat("x", maxOutputLength-1)
	d, _, _ := newTestDaemon(store, &fakeExecutor{output: prefix + "é"}, nil)
	tickAndWait(t, d)

	run := latestRun(t, store, schedule)
	assert.Equal(t, prefix+truncatedOutput, run.Output, "a byte limit must not split a rune")
}
