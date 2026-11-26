package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	run   func(ctx context.Context, schedule Schedule) error
	// response is returned by every run; it defaults to "done <name>".
	response string
}

func (f *fakeExecutor) Execute(ctx context.Context, schedule Schedule, started func(string)) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, schedule)
	run := f.run
	response := f.response
	f.mu.Unlock()
	if response == "" {
		response = "done " + schedule.Name
	}
	started("conv-" + schedule.ID)
	if run != nil {
		return response, run(ctx, schedule)
	}
	return response, nil
}

func (f *fakeExecutor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
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

func newTestDaemon(store *Store, exec executor, configure func(*daemonConfig)) (*daemon, *testClock) {
	config := defaultDaemonConfig()
	if configure != nil {
		configure(&config)
	}
	d := newDaemon(store, exec, config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := &testClock{}
	d.now = clock.Now
	return d, clock
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
	d, clock := newTestDaemon(store, exec, nil)

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
	assert.Equal(t, "done daily", run.Response)

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
	d, clock := newTestDaemon(store, exec, nil)

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
	exec := &fakeExecutor{run: func(context.Context, Schedule) error {
		<-release
		return nil
	}}
	d, clock := newTestDaemon(store, exec, nil)

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
	exec := &fakeExecutor{run: func(context.Context, Schedule) error {
		<-release
		return nil
	}}
	d, clock := newTestDaemon(store, exec, func(c *daemonConfig) { c.Concurrency = 1 })

	clock.Set(due)
	d.tick(t.Context(), t.Context())
	assert.Equal(t, 1, d.inFlightCount())
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
		run       func(ctx context.Context, schedule Schedule) error
		want      string
	}{
		{
			name: "error",
			run:  func(context.Context, Schedule) error { return errors.New("runner offline") },
			want: "failed:runner offline",
		},
		{
			name:      "timeout",
			configure: func(c *daemonConfig) { c.RunTimeout = 20 * time.Millisecond },
			run: func(ctx context.Context, _ Schedule) error {
				<-ctx.Done()
				return ctx.Err()
			},
			want: "failed:timed out after 20ms",
		},
		{
			name: "panic",
			run:  func(context.Context, Schedule) error { panic("boom") },
			want: "failed:internal error: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
			schedule := createTestSchedule(t, store, "runner-a", "daily", due)
			d, clock := newTestDaemon(store, &fakeExecutor{run: tt.run}, tt.configure)

			clock.Set(due)
			tickAndWait(t, d)

			assert.Equal(t, []string{tt.want}, runStatuses(t, store, schedule.ID))
		})
	}
}

func TestDaemonDisablesInvalidSchedules(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := Schedule{Workspace: "runner-a", Name: "broken", Schedule: "not cron", Instruction: "p", CWD: "/w", Enabled: true, NextRunAt: &due}
	require.NoError(t, store.Create(t.Context(), &schedule, due))
	exec := &fakeExecutor{}
	d, clock := newTestDaemon(store, exec, nil)

	clock.Set(due)
	tickAndWait(t, d)

	assert.Zero(t, exec.callCount())
	saved, err := store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.False(t, saved.Enabled)
	assert.Equal(t, RunFailed, saved.LastRun.Status)
	assert.Contains(t, saved.LastRun.Error, "the schedule was disabled")
}

func TestDaemonShutdownCancelsRunsAfterGrace(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	started := make(chan struct{})
	exec := &fakeExecutor{run: func(ctx context.Context, _ Schedule) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	d, clock := newTestDaemon(store, exec, func(c *daemonConfig) { c.ShutdownGrace = 10 * time.Millisecond })
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

	assert.Equal(t, []string{"canceled:" + interruptedMessage}, runStatuses(t, store, schedule.ID))
	status, found, err := store.Daemon(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	assert.NotNil(t, status.StoppedAt)
}

func TestDaemonAbandonsRunsFromAPreviousDaemon(t *testing.T) {
	store, _ := newTestStore(t)
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	schedule := createTestSchedule(t, store, "runner-a", "daily", due)
	_, _, err := store.Claim(t.Context(), schedule.ID, due, due.Add(24*time.Hour), Run{Status: RunRunning, StartedAt: due})
	require.NoError(t, err)
	d, clock := newTestDaemon(store, &fakeExecutor{}, nil)
	clock.Set(due.Add(time.Minute))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- d.run(ctx) }()
	require.Eventually(t, func() bool {
		_, found, err := store.Daemon(t.Context())
		return err == nil && found
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, []string{"failed:" + interruptedMessage}, runStatuses(t, store, schedule.ID))
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
