package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExecutor struct {
	mu    sync.Mutex
	calls []Schedule
	// startErr fails runs before their conversation starts.
	startErr error
	run      func(ctx context.Context, schedule Schedule, detach <-chan struct{}) error
	// output is the final output of every run; it defaults to "done <name>".
	output string
	// sent counts the instructions sent to started conversations.
	sent int
}

func (f *fakeExecutor) Execute(ctx context.Context, schedule Schedule, started func(string) error, detach <-chan struct{}) (runResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, schedule)
	run := f.run
	output := f.output
	startErr := f.startErr
	f.mu.Unlock()
	if startErr != nil {
		return runResult{}, startErr
	}
	if output == "" {
		output = "done " + schedule.Name
	}
	if err := started("conv-" + schedule.ID); err != nil {
		return runResult{}, err
	}
	f.mu.Lock()
	f.sent++
	f.mu.Unlock()
	result := runResult{Output: output, Stats: RunStats{ToolCalls: 2, Tools: map[string]int{"bash": 2}}}
	if run != nil {
		return result, run(ctx, schedule, detach)
	}
	return result, nil
}

func (f *fakeExecutor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeConversations serves conversation statuses; unknown conversations are
// not found.
type fakeConversations struct {
	mu       sync.Mutex
	statuses map[string]agent.ConversationStatus
	err      error
	stops    []string
}

func (f *fakeConversations) Status(_ context.Context, conversationID string) (agent.ConversationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agent.ConversationStatus{}, f.err
	}
	status, ok := f.statuses[conversationID]
	if !ok {
		return agent.ConversationStatus{}, fmt.Errorf("%w: %s", agent.ErrConversationNotFound, conversationID)
	}
	return status, nil
}

func (f *fakeConversations) Stop(_ context.Context, conversationID, turnID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, conversationID+"/"+turnID)
	return nil
}

// setTurn records the conversation's latest turn.
func (f *fakeConversations) setTurn(conversationID, status, result, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statuses == nil {
		f.statuses = map[string]agent.ConversationStatus{}
	}
	f.statuses[conversationID] = agent.ConversationStatus{
		ID:         conversationID,
		Running:    status == agent.TurnAccepted || status == agent.TurnRunning,
		LatestTurn: &agent.ConversationTurn{ID: "turn-1", Status: status, Result: result, Error: message},
	}
}

func (f *fakeConversations) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeConversations) stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stops...)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func newTestDaemon(store *Store, exec executor, configure func(*daemonConfig)) (*daemon, *testClock, *fakeConversations) {
	config := defaultDaemonConfig()
	if configure != nil {
		configure(&config)
	}
	convs := &fakeConversations{}
	d := newDaemon(store, exec, convs, config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := &testClock{}
	d.now = clock.Now
	return d, clock, convs
}

// tick runs one scheduler pass and waits for the runs it started.
func tickAndWait(t *testing.T, d *daemon) {
	t.Helper()
	d.tick(t.Context(), t.Context())
	d.wg.Wait()
}

func runStatuses(t *testing.T, store *Store, scheduleID string) []string {
	t.Helper()
	rows, err := store.db.QueryContext(t.Context(), `SELECT status || ':' || error FROM runs WHERE schedule_id = ? ORDER BY id`, scheduleID)
	require.NoError(t, err)
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var status string
		require.NoError(t, rows.Scan(&status))
		statuses = append(statuses, status)
	}
	require.NoError(t, rows.Err())
	return statuses
}

func TestDaemonRunsDueSchedules(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	exec := &fakeExecutor{}
	d, clock, _ := newTestDaemon(store, exec, nil)

	clock.Set(due.Add(-time.Second))
	tickAndWait(t, d)
	assert.Zero(t, exec.callCount(), "not due yet")

	clock.Set(due.Add(10 * time.Second))
	tickAndWait(t, d)

	require.Equal(t, 1, exec.callCount())
	ran := exec.calls[0]
	assert.Equal(t, "runner-a", ran.Workspace)
	assert.Equal(t, "/work/runner-a", ran.CWD)
	assert.Equal(t, schedule.Instruction, ran.Instruction)
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.Equal(t, RunSucceeded, saved.LastRun.Status)
	assert.Equal(t, "conv-"+schedule.ID, saved.LastRun.ConversationID)
	assert.True(t, due.Equal(saved.LastRun.ScheduledFor))
	assert.True(t, due.Add(24*time.Hour).Equal(*saved.NextRunAt))
	run, err := store.GetRun(t.Context(), "runner-a", saved.LastRun.ID)
	require.NoError(t, err)
	assert.Equal(t, "done daily", run.Output)
	assert.Equal(t, schedule.Instruction, run.Instruction)
	assert.Equal(t, schedule.CWD, run.CWD)
	require.NotNil(t, run.Stats)
	assert.Equal(t, map[string]int{"bash": 2}, run.Stats.Tools)

	tickAndWait(t, d)
	assert.Equal(t, 1, exec.callCount(), "each occurrence runs once")

	status, found, err := store.Daemon(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, due.Add(10*time.Second).Equal(status.HeartbeatAt))
}

func TestDaemonSkipsMissedRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	exec := &fakeExecutor{}
	d, clock, _ := newTestDaemon(store, exec, nil)

	clock.Set(due.Add(3 * time.Hour))
	tickAndWait(t, d)

	assert.Zero(t, exec.callCount())
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.Equal(t, RunSkipped, saved.LastRun.Status)
	assert.Contains(t, saved.LastRun.Error, "missed by 3h0m0s")
	assert.True(t, due.Add(24*time.Hour).Equal(*saved.NextRunAt), "the next run follows the current time, without backfilling")
}

func TestDaemonSkipsOverlappingRuns(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := Schedule{Workspace: "runner-a", Name: "minutely", Schedule: "CRON_TZ=UTC * * * * *", Instruction: "p", CWD: "/w", Enabled: true, NextRunAt: &due}
	require.NoError(t, store.Create(t.Context(), &schedule, due))
	release := make(chan struct{})
	exec := &fakeExecutor{run: func(context.Context, Schedule, <-chan struct{}) error {
		<-release
		return nil
	}}
	d, clock, _ := newTestDaemon(store, exec, nil)

	clock.Set(due)
	d.tick(t.Context(), t.Context())
	clock.Set(due.Add(time.Minute))
	d.tick(t.Context(), t.Context())
	close(release)
	d.wg.Wait()

	assert.Equal(t, 1, exec.callCount())
	assert.Equal(t, []string{"succeeded:", "skipped:the previous run was still in progress"}, runStatuses(t, store, schedule.ID))
}

func TestDaemonLimitsConcurrency(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	first := createTestSchedule(t, store, "runner-a", "first", due)
	second := createTestSchedule(t, store, "runner-a", "second", due)
	release := make(chan struct{})
	exec := &fakeExecutor{run: func(context.Context, Schedule, <-chan struct{}) error {
		<-release
		return nil
	}}
	d, clock, _ := newTestDaemon(store, exec, func(c *daemonConfig) { c.Concurrency = 1 })

	clock.Set(due)
	d.tick(t.Context(), t.Context())
	assert.Equal(t, 1, d.activeCount())
	waiting, err := store.Due(t.Context(), due)
	require.NoError(t, err)
	require.Len(t, waiting, 1, "the second schedule stays due until a slot frees up")
	close(release)
	d.wg.Wait()

	tickAndWait(t, d)
	assert.Equal(t, 2, exec.callCount())
	assert.Equal(t, []string{"succeeded:"}, runStatuses(t, store, first.ID))
	assert.Equal(t, []string{"succeeded:"}, runStatuses(t, store, second.ID))
}

func TestDaemonRecordsFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*daemonConfig)
		startErr  error
		run       func(ctx context.Context, schedule Schedule, detach <-chan struct{}) error
		// turn is the conversation's latest turn status, error, and result.
		turn []string
		want string
	}{
		{
			name:     "start error",
			startErr: errors.New("runner offline"),
			want:     "failed:runner offline",
		},
		{
			name: "error before the turn",
			run:  func(context.Context, Schedule, <-chan struct{}) error { return errors.New("unknown profile") },
			want: "failed:unknown profile",
		},
		{
			name:      "timeout",
			configure: func(c *daemonConfig) { c.RunTimeout = 20 * time.Millisecond },
			run: func(ctx context.Context, _ Schedule, _ <-chan struct{}) error {
				<-ctx.Done()
				return ctx.Err()
			},
			turn: []string{agent.TurnCancelled, "", ""},
			want: "failed:timed out after 20ms",
		},
		{
			name: "panic",
			run:  func(context.Context, Schedule, <-chan struct{}) error { panic("boom") },
			want: "failed:internal error: boom",
		},
		{
			name: "cancelled from outside",
			run:  func(context.Context, Schedule, <-chan struct{}) error { return errRunCancelled },
			turn: []string{agent.TurnCancelled, "", ""},
			want: "canceled:the run was cancelled",
		},
		{
			name: "turn failed on the server",
			run: func(context.Context, Schedule, <-chan struct{}) error {
				return errors.New("the run failed: prompt error")
			},
			turn: []string{agent.TurnFailed, "model overloaded", ""},
			want: "failed:model overloaded",
		},
		{
			name: "turn succeeded after the connection dropped",
			run:  func(context.Context, Schedule, <-chan struct{}) error { return errors.New("kodelet acp exited") },
			turn: []string{agent.TurnSucceeded, "", "from the server"},
			want: "succeeded:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
			schedule := createTestSchedule(t, store, "runner-a", "daily", due)
			d, clock, convs := newTestDaemon(store, &fakeExecutor{startErr: tt.startErr, run: tt.run}, tt.configure)
			if tt.turn != nil {
				convs.setTurn("conv-"+schedule.ID, tt.turn[0], tt.turn[2], tt.turn[1])
			}

			clock.Set(due)
			tickAndWait(t, d)

			assert.Equal(t, []string{tt.want}, runStatuses(t, store, schedule.ID))
			if tt.turn != nil && tt.turn[2] != "" {
				runs, err := store.ListRuns(t.Context(), "runner-a", RunFilter{Limit: 1})
				require.NoError(t, err)
				run, err := store.GetRun(t.Context(), "runner-a", runs[0].ID)
				require.NoError(t, err)
				assert.Equal(t, tt.turn[2], run.Output)
				require.NotNil(t, run.Stats, "the stats observed before the connection dropped are kept")
				assert.Equal(t, 2, run.Stats.ToolCalls)
			}
		})
	}
}

func TestDaemonWaitsForRunsWhoseConversationIsStillGoing(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := Schedule{Workspace: "runner-a", Name: "minutely", Schedule: "CRON_TZ=UTC * * * * *", Instruction: "p", CWD: "/w", Enabled: true, NextRunAt: &due}
	require.NoError(t, store.Create(t.Context(), &schedule, due))
	exec := &fakeExecutor{run: func(context.Context, Schedule, <-chan struct{}) error { return errors.New("kodelet acp exited") }}
	d, clock, convs := newTestDaemon(store, exec, nil)
	conversation := "conv-" + schedule.ID
	convs.setTurn(conversation, agent.TurnRunning, "", "")

	clock.Set(due)
	tickAndWait(t, d)
	assert.Equal(t, []string{"running:"}, runStatuses(t, store, schedule.ID))

	clock.Set(due.Add(time.Minute))
	convs.setErr(errors.New("server unreachable"))
	tickAndWait(t, d)
	assert.Equal(t, []string{"running:", "skipped:the previous run was still in progress"}, runStatuses(t, store, schedule.ID))

	clock.Set(due.Add(2 * time.Minute))
	convs.setErr(nil)
	convs.setTurn(conversation, agent.TurnSucceeded, "finished on the server", "")
	tickAndWait(t, d)
	assert.Equal(t, []string{"succeeded:", "skipped:the previous run was still in progress", "succeeded:"}, runStatuses(t, store, schedule.ID))
	assert.Equal(t, 2, exec.callCount())
}

func TestDaemonShutdownLeavesRunsToTheNextDaemon(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	started := make(chan struct{})
	exec := &fakeExecutor{run: func(ctx context.Context, _ Schedule, detach <-chan struct{}) error {
		close(started)
		select {
		case <-detach:
			return errors.New("kodelet acp process is closed")
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	d, clock, _ := newTestDaemon(store, exec, nil)
	clock.Set(due)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- d.run(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
	assert.Equal(t, []string{"running:"}, runStatuses(t, store, schedule.ID), "the run is left to its conversation")
	status, found, err := store.Daemon(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	assert.NotNil(t, status.StoppedAt)

	// The next daemon finds the run still going, then records its outcome.
	next, clock, convs := newTestDaemon(store, exec, nil)
	convs.setTurn("conv-"+schedule.ID, agent.TurnRunning, "", "")
	clock.Set(due.Add(5 * time.Minute))
	tickAndWait(t, next)
	assert.Equal(t, []string{"running:"}, runStatuses(t, store, schedule.ID))
	convs.setTurn("conv-"+schedule.ID, agent.TurnSucceeded, "all done", "")
	tickAndWait(t, next)
	assert.Equal(t, []string{"succeeded:"}, runStatuses(t, store, schedule.ID))
	assert.Equal(t, 1, exec.callCount(), "a restart never starts the occurrence again")
	runs, err := store.ListRuns(t.Context(), "runner-a", RunFilter{Limit: 1})
	require.NoError(t, err)
	run, err := store.GetRun(t.Context(), "runner-a", runs[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "all done", run.Output)
	require.NotNil(t, run.Stats)
	assert.Equal(t, 2, run.Stats.ToolCalls, "progress saved by the first daemon is kept")
}

func TestDaemonShutdownCancelsRunsThatHaveNotStarted(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	starting := make(chan struct{})
	exec := executorFunc(func(_ context.Context, _ Schedule, _ func(string) error, detach <-chan struct{}) (runResult, error) {
		close(starting)
		<-detach
		return runResult{}, errors.New("failed to start the conversation: context canceled")
	})
	d, clock, _ := newTestDaemon(store, exec, nil)
	clock.Set(due)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- d.run(ctx) }()
	<-starting
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, []string{"canceled:" + notStartedMessage}, runStatuses(t, store, schedule.ID))
}

type executorFunc func(ctx context.Context, schedule Schedule, started func(string) error, detach <-chan struct{}) (runResult, error)

func (f executorFunc) Execute(ctx context.Context, schedule Schedule, started func(string) error, detach <-chan struct{}) (runResult, error) {
	return f(ctx, schedule, started, detach)
}

func TestDaemonReconcilesRunsLeftByAPreviousDaemon(t *testing.T) {
	tests := []struct {
		name string
		// noConversation leaves the run without a conversation.
		noConversation bool
		age            time.Duration
		turn           []string
		statusErr      error
		want           string
		wantStop       bool
	}{
		{name: "no conversation", noConversation: true, age: time.Second, want: "failed:" + notStartedMessage},
		{name: "no turn yet", age: 30 * time.Second, want: "running:"},
		{name: "never started", age: 2 * time.Minute, want: "failed:" + neverStartedMessage},
		{name: "still running", age: 10 * time.Minute, turn: []string{agent.TurnRunning}, want: "running:"},
		{name: "accepted past the timeout", age: 61 * time.Minute, turn: []string{agent.TurnAccepted}, want: "running:", wantStop: true},
		{name: "long past the timeout", age: 75 * time.Minute, turn: []string{agent.TurnRunning}, want: "running:", wantStop: true},
		{name: "succeeded", age: 10 * time.Minute, turn: []string{agent.TurnSucceeded}, want: "succeeded:"},
		{name: "failed", age: 10 * time.Minute, turn: []string{agent.TurnFailed, "model overloaded"}, want: "failed:model overloaded"},
		{name: "cancelled", age: 10 * time.Minute, turn: []string{agent.TurnCancelled}, want: "canceled:the run was cancelled"},
		{name: "stopped after the timeout", age: 62 * time.Minute, turn: []string{agent.TurnCancelled}, want: "failed:timed out after 1h0m0s"},
		{name: "interrupted", age: 10 * time.Minute, turn: []string{agent.TurnInterrupted}, want: "failed:the Kodelet server stopped before the run finished"},
		{name: "server unreachable", age: 10 * time.Minute, statusErr: errors.New("connection refused"), want: "running:"},
		{name: "old Kodelet", age: 10 * time.Minute, statusErr: agent.ErrConversationsUnsupported, want: "failed:" + unsupportedMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
			schedule := createTestSchedule(t, store, "runner-a", "daily", due)
			runID, _, err := store.Claim(t.Context(), schedule.ID, due, due.Add(24*time.Hour), Run{Status: RunRunning, StartedAt: due})
			require.NoError(t, err)
			if !tt.noConversation {
				require.NoError(t, store.SetRunConversation(t.Context(), runID, "conv-old"))
			}
			exec := &fakeExecutor{}
			d, clock, convs := newTestDaemon(store, exec, nil)
			if tt.turn != nil {
				message := ""
				if len(tt.turn) > 1 {
					message = tt.turn[1]
				}
				convs.setTurn("conv-old", tt.turn[0], "", message)
			}
			convs.setErr(tt.statusErr)
			clock.Set(due.Add(tt.age))

			tickAndWait(t, d)

			assert.Equal(t, []string{tt.want}, runStatuses(t, store, schedule.ID))
			if tt.wantStop {
				assert.Equal(t, []string{"conv-old/turn-1"}, convs.stopped())
			} else {
				assert.Empty(t, convs.stopped())
			}
			assert.Zero(t, exec.callCount())
		})
	}
}

func TestDaemonHoldsTheSlotUntilATimedOutRunStops(t *testing.T) {
	store, _ := newTestStore(t)
	start := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := Schedule{Workspace: "runner-a", Name: "hourly", Schedule: "CRON_TZ=UTC 0 * * * *", Instruction: "p", CWD: "/w", Enabled: true, NextRunAt: &start}
	require.NoError(t, store.Create(t.Context(), &schedule, start))
	runID, _, err := store.Claim(t.Context(), schedule.ID, start, start.Add(time.Hour), Run{Status: RunRunning, StartedAt: start})
	require.NoError(t, err)
	require.NoError(t, store.SetRunConversation(t.Context(), runID, "conv-old"))
	exec := &fakeExecutor{}
	// The daemon was offline from just after 09:00 until 10:15, and the 09:00
	// run is still going on the server.
	d, clock, convs := newTestDaemon(store, exec, nil)
	convs.setTurn("conv-old", agent.TurnRunning, "", "")

	clock.Set(start.Add(75 * time.Minute))
	tickAndWait(t, d)
	clock.Set(start.Add(76 * time.Minute))
	tickAndWait(t, d)
	assert.Equal(t, []string{"running:", "skipped:the previous run was still in progress"}, runStatuses(t, store, schedule.ID))
	assert.Equal(t, []string{"conv-old/turn-1", "conv-old/turn-1"}, convs.stopped(), "the stop is requested until the server confirms it")
	assert.Zero(t, exec.callCount())

	convs.setTurn("conv-old", agent.TurnCancelled, "", "")
	clock.Set(start.Add(77 * time.Minute))
	tickAndWait(t, d)
	assert.Equal(t, []string{"failed:timed out after 1h0m0s", "skipped:the previous run was still in progress"}, runStatuses(t, store, schedule.ID))

	clock.Set(start.Add(2 * time.Hour))
	tickAndWait(t, d)
	assert.Equal(t, 1, exec.callCount(), "the next occurrence starts once the slot is free")
}

func TestDaemonSendsNoInstructionWithoutARecordedConversation(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	_, err := store.db.ExecContext(t.Context(), `
CREATE TRIGGER fail_conversation BEFORE UPDATE OF conversation_id ON runs
BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`)
	require.NoError(t, err)
	exec := &fakeExecutor{}
	d, clock, _ := newTestDaemon(store, exec, nil)

	clock.Set(due)
	tickAndWait(t, d)

	assert.Equal(t, 1, exec.callCount())
	assert.Zero(t, exec.sent, "a run whose conversation is not on record is never sent")
	statuses := runStatuses(t, store, schedule.ID)
	require.Len(t, statuses, 1)
	assert.Contains(t, statuses[0], "failed:did not send the instruction: failed to record run conversation")
	assert.Contains(t, statuses[0], "disk I/O error")
}

func TestDaemonCountsRunsLeftByAPreviousDaemonTowardConcurrency(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	old := createTestSchedule(t, store, "runner-a", "old", due)
	runID, _, err := store.Claim(t.Context(), old.ID, due, due.Add(24*time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	require.NoError(t, store.SetRunConversation(t.Context(), runID, "conv-old"))
	waiting := createTestSchedule(t, store, "runner-a", "waiting", due.Add(time.Minute))
	exec := &fakeExecutor{}
	d, clock, convs := newTestDaemon(store, exec, func(c *daemonConfig) { c.Concurrency = 1 })
	convs.setTurn("conv-old", agent.TurnRunning, "", "")

	clock.Set(due.Add(time.Minute))
	tickAndWait(t, d)
	assert.Zero(t, exec.callCount(), "the previous daemon's run holds the only slot")

	convs.setTurn("conv-old", agent.TurnSucceeded, "", "")
	tickAndWait(t, d)
	assert.Equal(t, 1, exec.callCount())
	assert.Equal(t, []string{"succeeded:"}, runStatuses(t, store, waiting.ID))
}

func TestAcquireDaemonLock(t *testing.T) {
	_, path := newTestStore(t)

	lock, err := acquireDaemonLock(path)
	require.NoError(t, err)
	_, err = acquireDaemonLock(path)
	assert.ErrorContains(t, err, "already running")

	require.NoError(t, lock.Close())
	lock, err = acquireDaemonLock(path)
	require.NoError(t, err)
	lock.Close()
}

func TestAssessDaemon(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	running := DaemonStatus{HeartbeatAt: now.Add(-time.Minute), PollInterval: 30 * time.Second}
	stale := DaemonStatus{HeartbeatAt: now.Add(-2 * time.Minute), PollInterval: 30 * time.Second}
	stopped := running
	stopped.StoppedAt = &now

	assert.Equal(t, daemonNotStarted, assessDaemon(DaemonStatus{}, false, now).State)
	assert.Equal(t, daemonRunning, assessDaemon(running, true, now).State)
	assert.Empty(t, assessDaemon(running, true, now).Warning)
	assert.Equal(t, daemonUnresponsive, assessDaemon(stale, true, now).State)
	assert.Equal(t, daemonStopped, assessDaemon(stopped, true, now).State)
	assert.Contains(t, assessDaemon(stopped, true, now).Warning, "will not fire")
}

func TestDaemonConfigValidate(t *testing.T) {
	assert.NoError(t, defaultDaemonConfig().validate())
	config := defaultDaemonConfig()
	config.MisfireGrace = time.Second
	assert.ErrorContains(t, config.validate(), "misfire grace")
	config = defaultDaemonConfig()
	config.Concurrency = 0
	assert.ErrorContains(t, config.validate(), "concurrency")
}

func TestDescribeAgentErrorAddsStderr(t *testing.T) {
	err := describeAgentError(&agent.RunError{Message: "kodelet acp exited with status 1", Stderr: "starting\nError: runner not found: abc\n"}, "failed to start the conversation")
	assert.EqualError(t, err, "failed to start the conversation: kodelet acp exited with status 1: Error: runner not found: abc")

	err = describeAgentError(errors.New("boom"), "the run failed")
	assert.EqualError(t, err, "the run failed: boom")
}

func TestListCommand(t *testing.T) {
	store, path := newTestStore(t)
	next := time.Now().Add(time.Hour)
	createTestSchedule(t, store, "runner-a", "daily", next)
	createTestSchedule(t, store, "runner-b", "weekly", next)

	var text bytes.Buffer
	require.NoError(t, listSchedules(t.Context(), path, false, &text))
	assert.Contains(t, text.String(), "runner-a")
	assert.Contains(t, text.String(), "weekly")
	assert.Contains(t, text.String(), "Daemon: not started")

	var data bytes.Buffer
	require.NoError(t, listSchedules(t.Context(), path, true, &data))
	var decoded struct {
		Schedules []Schedule   `json:"schedules"`
		Daemon    daemonHealth `json:"daemon"`
	}
	require.NoError(t, json.Unmarshal(data.Bytes(), &decoded))
	assert.Len(t, decoded.Schedules, 2)
	assert.Equal(t, daemonNotStarted, decoded.Daemon.State)
}

func TestDaemonRunsTheInstructionItRecords(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	polled, err := store.Due(t.Context(), due)
	require.NoError(t, err)
	require.Len(t, polled, 1)
	// The schedule is edited after the daemon polls it but before it claims.
	schedule.Instruction = "Edited instruction"
	schedule.CWD = "/work/edited"
	require.NoError(t, store.Update(t.Context(), &schedule, schedule.Version, nil, due))
	exec := &fakeExecutor{}
	d, clock, _ := newTestDaemon(store, exec, nil)
	clock.Set(due)

	d.dispatch(t.Context(), t.Context(), polled[0], due, &occupancy{schedules: map[string]bool{}})
	d.wg.Wait()

	require.Equal(t, 1, exec.callCount())
	assert.Equal(t, "Edited instruction", exec.calls[0].Instruction)
	assert.Equal(t, "/work/edited", exec.calls[0].CWD)
	runs, err := store.ListRuns(t.Context(), "runner-a", RunFilter{Limit: 1})
	require.NoError(t, err)
	run, err := store.GetRun(t.Context(), "runner-a", runs[0].ID)
	require.NoError(t, err)
	assert.Equal(t, exec.calls[0].Instruction, run.Instruction)
	assert.Equal(t, exec.calls[0].CWD, run.CWD)
}

func TestDaemonSendsNoInstructionWhenTheScheduleIsDeletedFirst(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	createTestSchedule(t, store, "runner-a", "daily", due)
	var deleteErr, startErr error
	sent := false
	// The user deletes the schedule after the daemon claims the run but
	// before the instruction is sent.
	exec := executorFunc(func(ctx context.Context, schedule Schedule, started func(string) error, _ <-chan struct{}) (runResult, error) {
		deleteErr = store.Delete(ctx, schedule.Workspace, schedule.ID)
		if startErr = started("conv-deleted"); startErr != nil {
			return runResult{}, startErr
		}
		sent = true
		return runResult{}, nil
	})
	d, clock, _ := newTestDaemon(store, exec, nil)

	clock.Set(due)
	tickAndWait(t, d)

	require.NoError(t, deleteErr)
	assert.ErrorIs(t, startErr, errRunGone)
	assert.False(t, sent, "nothing could track the conversation, so the instruction is not sent")
	var runs int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runs`).Scan(&runs))
	assert.Zero(t, runs)
}
