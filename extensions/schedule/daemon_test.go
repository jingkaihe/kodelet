package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var daemonDue = time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)

type fakeExecutor struct {
	mu    sync.Mutex
	calls []Schedule
	// startErr fails runs before their conversation starts.
	startErr error
	run      func(ctx context.Context, schedule Schedule, detach <-chan struct{}) error
	// output is the final output of every run; it defaults to "done <name>".
	output string
}

func (f *fakeExecutor) Execute(ctx context.Context, schedule Schedule, started func(string) error, detach <-chan struct{}) (runResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, schedule)
	f.mu.Unlock()
	if f.startErr != nil {
		return runResult{}, f.startErr
	}
	output := f.output
	if output == "" {
		output = "done " + schedule.Name
	}
	if err := started("conv-" + schedule.ID); err != nil {
		return runResult{}, err
	}
	result := runResult{Output: output, Stats: RunStats{ToolCalls: 2}}
	if f.run != nil {
		return result, f.run(ctx, schedule, detach)
	}
	return result, nil
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
		return agent.ConversationStatus{}, errors.Wrap(agent.ErrConversationNotFound, conversationID)
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
	d := newDaemon(store, exec, convs, config, slog.New(slog.DiscardHandler))
	clock := &testClock{now: daemonDue}
	d.now = clock.Now
	return d, clock, convs
}

// tickAndWait runs one scheduler pass and waits for the runs it started.
func tickAndWait(t *testing.T, d *daemon) {
	t.Helper()
	d.tick(t.Context(), t.Context())
	d.wg.Wait()
}

func dueDailySchedule(t *testing.T) (*Store, Schedule) {
	t.Helper()
	store, _ := newTestStore(t)
	return store, createTestSchedule(t, store, "runner-a", "daily", daemonDue)
}

func latestRun(t *testing.T, store *Store, schedule Schedule) Run {
	t.Helper()
	saved, err := store.Get(t.Context(), schedule.Workspace, schedule.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.LastRun)
	run, err := store.GetRun(t.Context(), schedule.Workspace, saved.LastRun.ID)
	require.NoError(t, err)
	return run
}

func runStatuses(t *testing.T, store *Store, schedule Schedule) []string {
	t.Helper()
	runs, err := store.ListRuns(t.Context(), schedule.Workspace, RunFilter{ScheduleID: schedule.ID, Limit: runHistoryLimit})
	require.NoError(t, err)
	var statuses []string
	for _, run := range slices.Backward(runs) {
		statuses = append(statuses, string(run.Status)+":"+run.Error)
	}
	return statuses
}

func TestDaemonRunsDueSchedules(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	exec := &fakeExecutor{}
	d, clock, _ := newTestDaemon(store, exec, nil)

	clock.Set(daemonDue.Add(-time.Second))
	tickAndWait(t, d)
	require.Empty(t, exec.calls, "not due yet")

	clock.Set(daemonDue.Add(10 * time.Second))
	tickAndWait(t, d)
	tickAndWait(t, d)
	require.Len(t, exec.calls, 1, "each occurrence runs once")
	assert.Equal(t, schedule.Workspace, exec.calls[0].Workspace)
	run := latestRun(t, store, schedule)
	assert.Equal(t, RunSucceeded, run.Status)
	assert.Equal(t, "conv-"+schedule.ID, run.ConversationID)
	assert.Equal(t, "done daily", run.Output)
	require.NotNil(t, run.Stats)
	assert.Equal(t, 2, run.Stats.ToolCalls)
	assert.True(t, daemonDue.Equal(run.ScheduledFor), "record the occurrence, not the poll time")
}

func TestDaemonSkipsMissedRuns(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	exec := &fakeExecutor{}
	d, clock, _ := newTestDaemon(store, exec, nil)

	// More than one daily occurrence was missed; skip straight to the next one.
	clock.Set(daemonDue.Add(27 * time.Hour))
	tickAndWait(t, d)

	assert.Empty(t, exec.calls)
	saved, err := store.Get(t.Context(), schedule.Workspace, schedule.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.LastRun)
	assert.Equal(t, RunSkipped, saved.LastRun.Status)
	assert.Contains(t, saved.LastRun.Error, "missed by 27h")
	require.NotNil(t, saved.NextRunAt)
	assert.True(t, daemonDue.Add(48*time.Hour).Equal(*saved.NextRunAt))
}

func TestDaemonSkipsOverlapsButQueuesOtherSchedulesAtCapacity(t *testing.T) {
	store, _ := newTestStore(t)
	first := Schedule{
		Workspace: "runner-a", Name: "minutely", Schedule: "CRON_TZ=UTC * * * * *",
		Instruction: "p", CWD: "/w", Enabled: true, NextRunAt: &daemonDue,
	}
	require.NoError(t, store.Create(t.Context(), &first, daemonDue))
	second := createTestSchedule(t, store, "runner-a", "waiting", daemonDue.Add(time.Second))
	release := make(chan struct{})
	exec := &fakeExecutor{run: func(context.Context, Schedule, <-chan struct{}) error {
		<-release
		return nil
	}}
	d, clock, _ := newTestDaemon(store, exec, func(c *daemonConfig) { c.Concurrency = 1 })

	clock.Set(daemonDue.Add(time.Second)) // Both are due in the same pass.
	d.tick(t.Context(), t.Context())
	assert.Equal(t, 1, d.activeCount())
	clock.Set(daemonDue.Add(time.Minute))
	d.tick(t.Context(), t.Context())
	close(release)
	d.wg.Wait()

	assert.Equal(t, []string{"succeeded:", "skipped:the previous run was still in progress"}, runStatuses(t, store, first))
	assert.Empty(t, runStatuses(t, store, second), "a different schedule stays due rather than being skipped")
	tickAndWait(t, d)
	assert.Equal(t, []string{"succeeded:"}, runStatuses(t, store, second))
}

func TestDaemonRecordsFailures(t *testing.T) {
	fail := func(message string) func(context.Context, Schedule, <-chan struct{}) error {
		return func(context.Context, Schedule, <-chan struct{}) error { return errors.New(message) }
	}
	tests := []struct {
		name      string
		exec      *fakeExecutor
		turn      string
		turnError string
		timeout   time.Duration
		want      string
	}{
		{name: "start error", exec: &fakeExecutor{startErr: errors.New("runner offline")}, want: "failed:runner offline"},
		{name: "error before the turn", exec: &fakeExecutor{run: fail("unknown profile")}, want: "failed:unknown profile"},
		{
			name: "timeout", timeout: 20 * time.Millisecond, turn: agent.TurnCancelled, want: "failed:timed out after 20ms",
			exec: &fakeExecutor{run: func(ctx context.Context, _ Schedule, _ <-chan struct{}) error {
				<-ctx.Done()
				return ctx.Err()
			}},
		},
		{
			name: "panic", want: "failed:internal error: boom",
			exec: &fakeExecutor{run: func(context.Context, Schedule, <-chan struct{}) error { panic("boom") }},
		},
		{
			name: "server error takes precedence", exec: &fakeExecutor{run: fail("connection dropped")},
			turn: agent.TurnFailed, turnError: "model overloaded", want: "failed:model overloaded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, schedule := dueDailySchedule(t)
			d, _, convs := newTestDaemon(store, tt.exec, func(c *daemonConfig) { c.RunTimeout = tt.timeout })
			if tt.turn != "" {
				convs.setTurn("conv-"+schedule.ID, tt.turn, "", tt.turnError)
			}
			tickAndWait(t, d)
			assert.Equal(t, []string{tt.want}, runStatuses(t, store, schedule))
		})
	}
}

func TestDaemonRecoversFromLostConnection(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	exec := &fakeExecutor{run: func(context.Context, Schedule, <-chan struct{}) error { return errors.New("kodelet acp exited") }}
	d, _, convs := newTestDaemon(store, exec, nil)
	conversation := "conv-" + schedule.ID
	convs.setTurn(conversation, agent.TurnRunning, "", "")
	tickAndWait(t, d)
	assert.Equal(t, []string{"running:"}, runStatuses(t, store, schedule))

	convs.setErr(errors.New("server unreachable"))
	tickAndWait(t, d)
	assert.Equal(t, []string{"running:"}, runStatuses(t, store, schedule), "a failed status lookup is retried")

	convs.setErr(nil)
	convs.setTurn(conversation, agent.TurnSucceeded, "finished on the server", "")
	tickAndWait(t, d)
	run := latestRun(t, store, schedule)
	assert.Equal(t, RunSucceeded, run.Status)
	assert.Equal(t, "finished on the server", run.Output)
}

// stopDuringRun waits until the executor reaches the intended stage before shutting down.
func stopDuringRun(t *testing.T, d *daemon, started <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
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
}

func TestDaemonShutdownLeavesRunsToTheNextDaemon(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	started := make(chan struct{})
	var jobErr error
	exec := &fakeExecutor{run: func(ctx context.Context, _ Schedule, detach <-chan struct{}) error {
		close(started)
		<-detach
		jobErr = ctx.Err()
		return errors.New("kodelet acp process is closed")
	}}
	d, _, _ := newTestDaemon(store, exec, nil)
	stopDuringRun(t, d, started)
	require.NoError(t, jobErr, "shutdown must detach, not cancel the conversation")
	assert.Equal(t, []string{"running:"}, runStatuses(t, store, schedule))
	status, found, err := store.Daemon(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	assert.NotNil(t, status.StoppedAt)

	next, clock, convs := newTestDaemon(store, exec, nil)
	convs.setTurn("conv-"+schedule.ID, agent.TurnSucceeded, "all done", "")
	clock.Set(daemonDue.Add(5 * time.Minute))
	tickAndWait(t, next)
	assert.Len(t, exec.calls, 1, "a restart never starts the occurrence again")
	run := latestRun(t, store, schedule)
	assert.Equal(t, RunSucceeded, run.Status)
	assert.Equal(t, "all done", run.Output)
	require.NotNil(t, run.Stats)
	assert.Equal(t, 2, run.Stats.ToolCalls, "progress survives a daemon restart")
}

func TestDaemonShutdownCancelsRunsThatHaveNotStarted(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	starting := make(chan struct{})
	exec := executorFunc(func(_ context.Context, _ Schedule, _ func(string) error, detach <-chan struct{}) (runResult, error) {
		close(starting)
		<-detach
		return runResult{}, errors.New("failed to start the conversation: context canceled")
	})
	d, _, _ := newTestDaemon(store, exec, nil)
	stopDuringRun(t, d, starting)
	assert.Equal(t, []string{"canceled:" + notStartedMessage}, runStatuses(t, store, schedule))
}

type executorFunc func(ctx context.Context, schedule Schedule, started func(string) error, detach <-chan struct{}) (runResult, error)

func (f executorFunc) Execute(ctx context.Context, schedule Schedule, started func(string) error, detach <-chan struct{}) (runResult, error) {
	return f(ctx, schedule, started, detach)
}

func claimUnfinishedRun(t *testing.T, store *Store, schedule Schedule, next time.Time, conversation string) {
	t.Helper()
	started := *schedule.NextRunAt
	runID, claimed, err := store.Claim(t.Context(), schedule.ID, started, next, Run{Status: RunRunning, StartedAt: started})
	require.NoError(t, err)
	require.True(t, claimed)
	if conversation != "" {
		require.NoError(t, store.SetRunConversation(t.Context(), runID, conversation))
	}
}

func TestDaemonReconcilesRunsLeftByAPreviousDaemon(t *testing.T) {
	tests := []struct {
		name         string
		conversation string
		age          time.Duration
		turn         string
		statusErr    error
		want         string
	}{
		{name: "no conversation", want: "failed:" + notStartedMessage},
		{name: "no turn yet", conversation: "conv-old", age: 30 * time.Second, want: "running:"},
		{name: "never started", conversation: "conv-old", age: 2 * time.Minute, want: "failed:" + neverStartedMessage},
		{name: "cancelled", conversation: "conv-old", turn: agent.TurnCancelled, want: "canceled:the run was cancelled"},
		{name: "interrupted", conversation: "conv-old", turn: agent.TurnInterrupted, want: "failed:the Kodelet server stopped before the run finished"},
		{name: "old Kodelet", conversation: "conv-old", statusErr: agent.ErrConversationsUnsupported, want: "failed:" + unsupportedMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, schedule := dueDailySchedule(t)
			claimUnfinishedRun(t, store, schedule, daemonDue.Add(24*time.Hour), tt.conversation)
			d, clock, convs := newTestDaemon(store, &fakeExecutor{}, nil)
			if tt.turn != "" {
				convs.setTurn(tt.conversation, tt.turn, "", "")
			}
			convs.setErr(tt.statusErr)
			clock.Set(daemonDue.Add(tt.age))
			tickAndWait(t, d)
			assert.Equal(t, []string{tt.want}, runStatuses(t, store, schedule))
		})
	}
}

func TestDaemonHoldsTheSlotUntilATimedOutRunStops(t *testing.T) {
	store, _ := newTestStore(t)
	hourly := Schedule{
		Workspace: "runner-a", Name: "hourly", Schedule: "CRON_TZ=UTC 0 * * * *",
		Instruction: "p", CWD: "/w", Enabled: true, NextRunAt: &daemonDue,
	}
	require.NoError(t, store.Create(t.Context(), &hourly, daemonDue))
	claimUnfinishedRun(t, store, hourly, daemonDue.Add(time.Hour), "conv-old")
	waiting := createTestSchedule(t, store, "runner-a", "waiting", daemonDue.Add(time.Minute))
	exec := &fakeExecutor{}
	d, clock, convs := newTestDaemon(store, exec, func(c *daemonConfig) { c.Concurrency = 1 })
	convs.setTurn("conv-old", agent.TurnAccepted, "", "")

	clock.Set(daemonDue.Add(time.Minute))
	tickAndWait(t, d)
	assert.Empty(t, exec.calls, "a previous daemon's run holds the only slot")
	assert.Empty(t, convs.stopped(), "before the timeout")

	// The original run has now timed out and its next occurrence is due.
	// Keep the waiting schedule inside its misfire grace through both polls.
	clock.Set(daemonDue.Add(time.Hour))
	tickAndWait(t, d)
	convs.setTurn("conv-old", agent.TurnRunning, "", "")
	clock.Set(daemonDue.Add(time.Hour + time.Second))
	tickAndWait(t, d)
	assert.Equal(t, []string{"conv-old/turn-1", "conv-old/turn-1"}, convs.stopped(), "retry the stop until confirmed")
	assert.Equal(t, []string{"running:", "skipped:the previous run was still in progress"}, runStatuses(t, store, hourly))
	assert.Empty(t, exec.calls)

	convs.setTurn("conv-old", agent.TurnCancelled, "", "")
	tickAndWait(t, d)
	assert.Equal(t, []string{"failed:timed out after 1h0m0s", "skipped:the previous run was still in progress"}, runStatuses(t, store, hourly))
	assert.Equal(t, []string{"succeeded:"}, runStatuses(t, store, waiting), "stop confirmation frees the slot")
}

func TestDaemonRejectsStartWhenConversationCannotBeRecorded(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	_, err := store.db.ExecContext(t.Context(), `
CREATE TRIGGER fail_conversation BEFORE UPDATE OF conversation_id ON runs
BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`)
	require.NoError(t, err)
	var startErr error
	exec := executorFunc(func(_ context.Context, _ Schedule, started func(string) error, _ <-chan struct{}) (runResult, error) {
		startErr = started("conv-untracked")
		return runResult{}, startErr
	})
	d, _, _ := newTestDaemon(store, exec, nil)
	tickAndWait(t, d)

	require.ErrorContains(t, startErr, "disk I/O error", "the executor must not proceed with the instruction")
	run := latestRun(t, store, schedule)
	assert.Equal(t, RunFailed, run.Status)
	assert.Contains(t, run.Error, "did not send the instruction")
}

func TestAcquireDaemonLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.db")

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
	status := DaemonStatus{HeartbeatAt: daemonDue, PollInterval: 30 * time.Second}
	assert.Equal(t, daemonRunning, assessDaemon(status, true, daemonDue.Add(time.Minute)).State)
	later := daemonDue.Add(2 * time.Minute)
	assert.Equal(t, daemonUnresponsive, assessDaemon(status, true, later).State)
	status.StoppedAt = &daemonDue
	assert.Equal(t, daemonStopped, assessDaemon(status, true, later).State, "a clean stop is not a stale heartbeat")
}

func TestDescribeAgentErrorAddsStderr(t *testing.T) {
	err := describeAgentError(&agent.RunError{
		Message: "kodelet acp exited with status 1",
		Stderr:  "starting\nError: runner not found: abc\n",
	}, "failed to start the conversation")
	assert.EqualError(t, err, "failed to start the conversation: kodelet acp exited with status 1: Error: runner not found: abc")
}

func TestDaemonRunsTheInstructionItRecords(t *testing.T) {
	store, schedule := dueDailySchedule(t)
	polled := schedule
	schedule.Instruction = "Edited instruction"
	schedule.CWD = "/work/edited"
	require.NoError(t, store.Update(t.Context(), &schedule, schedule.Version, nil, daemonDue))
	exec := &fakeExecutor{}
	d, _, _ := newTestDaemon(store, exec, nil)

	// Dispatch must claim the current instruction even though it polled the old one.
	d.dispatch(t.Context(), t.Context(), polled, daemonDue, &occupancy{schedules: map[string]bool{}})
	d.wg.Wait()

	require.Len(t, exec.calls, 1)
	run := latestRun(t, store, schedule)
	assert.Equal(t, schedule.Instruction, exec.calls[0].Instruction)
	assert.Equal(t, schedule.CWD, exec.calls[0].CWD)
	assert.Equal(t, exec.calls[0].Instruction, run.Instruction)
	assert.Equal(t, exec.calls[0].CWD, run.CWD)
}

func TestDaemonRejectsStartWhenTheScheduleIsDeletedFirst(t *testing.T) {
	store, _ := dueDailySchedule(t)
	var deleteErr, startErr error
	exec := executorFunc(func(ctx context.Context, schedule Schedule, started func(string) error, _ <-chan struct{}) (runResult, error) {
		// Delete after the claim but before the executor sends its instruction.
		deleteErr = store.Delete(ctx, schedule.Workspace, schedule.ID)
		startErr = started("conv-deleted")
		return runResult{}, startErr
	})
	d, _, _ := newTestDaemon(store, exec, nil)
	tickAndWait(t, d)

	require.NoError(t, deleteErr)
	assert.ErrorIs(t, startErr, errRunGone)
	var runs int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runs`).Scan(&runs))
	assert.Zero(t, runs)
}
