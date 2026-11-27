package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/pkg/errors"
)

const (
	storeTimeout       = 10 * time.Second
	maxRunErrorLength  = 2000
	maxOutputLength    = 32 * 1024
	truncatedOutput    = "\n\n[Output truncated by the schedule extension.]"
	interruptedMessage = "the schedule daemon stopped before the run finished"
)

// daemonConfig tunes the scheduler loop.
type daemonConfig struct {
	PollInterval  time.Duration
	Concurrency   int
	RunTimeout    time.Duration
	MisfireGrace  time.Duration
	ShutdownGrace time.Duration
}

func defaultDaemonConfig() daemonConfig {
	return daemonConfig{
		PollInterval:  30 * time.Second,
		Concurrency:   2,
		RunTimeout:    time.Hour,
		MisfireGrace:  time.Hour,
		ShutdownGrace: 30 * time.Second,
	}
}

func (c daemonConfig) validate() error {
	switch {
	case c.PollInterval < time.Second:
		return errors.New("poll interval must be at least 1s")
	case c.Concurrency < 1:
		return errors.New("concurrency must be at least 1")
	case c.RunTimeout < 0:
		return errors.New("run timeout must not be negative")
	case c.MisfireGrace < c.PollInterval:
		return errors.New("misfire grace must be at least the poll interval")
	case c.ShutdownGrace < 0:
		return errors.New("shutdown grace must not be negative")
	}
	return nil
}

// executor runs one scheduled instruction to completion. It calls started
// with the conversation ID once the conversation exists, and reports what the
// run did even when it fails.
type executor interface {
	Execute(ctx context.Context, schedule Schedule, started func(conversationID string)) (runResult, error)
}

// daemon claims due schedules from the store and runs them.
type daemon struct {
	store  *Store
	exec   executor
	config daemonConfig
	now    func() time.Time
	log    *slog.Logger
	status DaemonStatus

	mu       sync.Mutex
	inFlight map[string]bool
	wg       sync.WaitGroup
	stopping atomic.Bool
}

func newDaemon(store *Store, exec executor, config daemonConfig, log *slog.Logger) *daemon {
	return &daemon{
		store:    store,
		exec:     exec,
		config:   config,
		now:      time.Now,
		log:      log,
		inFlight: map[string]bool{},
	}
}

// run schedules until ctx is cancelled, then gives in-flight runs
// ShutdownGrace to finish before cancelling them.
func (d *daemon) run(ctx context.Context) error {
	hostname, _ := os.Hostname()
	now := d.now()
	d.status = DaemonStatus{
		PID:          os.Getpid(),
		Hostname:     hostname,
		Version:      version,
		StartedAt:    now,
		PollInterval: d.config.PollInterval,
	}
	abandoned, err := d.store.AbandonRunningRuns(ctx, interruptedMessage, now)
	if err != nil {
		return err
	}
	if abandoned > 0 {
		d.log.Warn("marked runs interrupted by a previous daemon as failed", "runs", abandoned)
	}
	d.log.Info("schedule daemon started", "pollInterval", d.config.PollInterval, "concurrency", d.config.Concurrency)

	// Runs outlive ctx so that shutdown can let them finish.
	jobs, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJobs()
	ticker := time.NewTicker(d.config.PollInterval)
	defer ticker.Stop()
	for {
		d.tick(ctx, jobs)
		select {
		case <-ctx.Done():
			return d.shutdown(cancelJobs)
		case <-ticker.C:
		}
	}
}

func (d *daemon) shutdown(cancelJobs context.CancelFunc) error {
	d.stopping.Store(true)
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d.config.ShutdownGrace):
		d.log.Info("cancelling in-flight runs")
		cancelJobs()
		<-done
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	d.log.Info("schedule daemon stopped")
	return d.store.MarkDaemonStopped(ctx, d.status.PID, d.now())
}

// tick records a heartbeat and dispatches every due schedule.
func (d *daemon) tick(ctx, jobs context.Context) {
	now := d.now()
	d.status.HeartbeatAt = now
	if err := d.store.SaveHeartbeat(ctx, d.status); err != nil {
		d.log.Warn("failed to record heartbeat", "error", err)
	}
	due, err := d.store.Due(ctx, now)
	if err != nil {
		d.log.Error("failed to read due schedules", "error", err)
		return
	}
	for _, schedule := range due {
		if ctx.Err() != nil {
			return
		}
		d.dispatch(ctx, jobs, schedule, now)
	}
}

func (d *daemon) dispatch(ctx, jobs context.Context, schedule Schedule, now time.Time) {
	due := *schedule.NextRunAt
	log := d.log.With("schedule", schedule.ID, "name", schedule.Name, "workspace", schedule.Workspace)
	spec, err := parseSpec(schedule.Schedule)
	if err != nil {
		run := Run{Status: RunFailed, StartedAt: now, FinishedAt: &now, Error: err.Error() + "; the schedule was disabled"}
		if _, err := d.store.Disable(ctx, schedule.ID, due, run); err != nil {
			log.Error("failed to disable invalid schedule", "error", err)
		}
		log.Error("disabled invalid schedule", "error", run.Error)
		return
	}
	next := spec.Next(now)
	switch {
	case now.Sub(due) > d.config.MisfireGrace:
		reason := fmt.Sprintf("missed by %s, longer than the misfire grace of %s; the daemon was not running or the host was asleep",
			now.Sub(due).Round(time.Minute), d.config.MisfireGrace)
		d.skip(ctx, log, schedule.ID, due, next, now, reason)
	case d.isInFlight(schedule.ID):
		d.skip(ctx, log, schedule.ID, due, next, now, "the previous run was still in progress")
	case d.inFlightCount() >= d.config.Concurrency:
		// Leave it due; a later tick starts it once a slot frees up.
	default:
		// Run the schedule as claimed, not as polled, so an edit in between
		// cannot make the run differ from its recorded history.
		claimed, runID, ok, err := d.store.ClaimRun(ctx, schedule, due, next, Run{Status: RunRunning, StartedAt: now})
		if err != nil {
			log.Error("failed to claim schedule", "error", err)
			return
		}
		if ok {
			d.start(jobs, claimed, runID, log)
		}
	}
}

func (d *daemon) skip(ctx context.Context, log *slog.Logger, scheduleID string, due, next, now time.Time, reason string) {
	run := Run{Status: RunSkipped, StartedAt: now, FinishedAt: &now, Error: reason}
	_, claimed, err := d.store.Claim(ctx, scheduleID, due, next, run)
	if err != nil {
		log.Error("failed to skip run", "error", err)
		return
	}
	if claimed {
		log.Warn("skipped run", "scheduledFor", due, "reason", reason)
	}
}

func (d *daemon) isInFlight(scheduleID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.inFlight[scheduleID]
}

func (d *daemon) inFlightCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.inFlight)
}

func (d *daemon) start(jobs context.Context, schedule Schedule, runID int64, log *slog.Logger) {
	d.mu.Lock()
	d.inFlight[schedule.ID] = true
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.inFlight, schedule.ID)
			d.mu.Unlock()
		}()
		log = log.With("run", runID)
		log.Info("starting run")
		status, message, result := d.execute(jobs, schedule, runID, log)
		ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		defer cancel()
		if err := d.store.FinishRun(ctx, runID, status, message, result, d.now()); err != nil {
			log.Error("failed to record run outcome", "error", err)
		}
		if message != "" {
			log.Warn("run finished", "status", status, "error", message)
		} else {
			log.Info("run finished", "status", status)
		}
	}()
}

func (d *daemon) execute(jobs context.Context, schedule Schedule, runID int64, log *slog.Logger) (status RunStatus, message string, result runResult) {
	ctx := jobs
	if d.config.RunTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(jobs, d.config.RunTimeout)
		defer cancel()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			status, message, result = RunFailed, fmt.Sprintf("internal error: %v", recovered), runResult{}
		}
	}()
	result, err := d.exec.Execute(ctx, schedule, func(conversationID string) {
		storeCtx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		defer cancel()
		if err := d.store.SetRunConversation(storeCtx, runID, conversationID); err != nil {
			log.Error("failed to record run conversation", "error", err)
		}
		log.Info("run conversation started", "conversation", conversationID)
	})
	result.Output = truncateOutput(result.Output)
	switch {
	case err == nil:
		return RunSucceeded, "", result
	case d.stopping.Load() && jobs.Err() != nil:
		return RunCanceled, interruptedMessage, result
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return RunFailed, fmt.Sprintf("timed out after %s", d.config.RunTimeout), result
	default:
		return RunFailed, truncate(err.Error(), maxRunErrorLength), result
	}
}

// acquireDaemonLock takes an exclusive advisory lock beside the database so
// only one daemon schedules from it. The lock ends with the process.
func acquireDaemonLock(dbPath string) (*os.File, error) {
	file, err := os.OpenFile(dbPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open daemon lock")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Errorf("another schedule daemon is already running for %s", dbPath)
		}
		return nil, errors.Wrap(err, "failed to lock daemon")
	}
	return file, nil
}

// Daemon states reported to listings.
const (
	daemonRunning      = "running"
	daemonStopped      = "stopped"
	daemonUnresponsive = "unresponsive"
	daemonNotStarted   = "not started"
	daemonUnknown      = "unknown"
)

// daemonHealth summarises the daemon heartbeat for listings.
type daemonHealth struct {
	State       string     `json:"state" jsonschema:"Daemon state; schedules fire only while it is running"`
	HeartbeatAt *time.Time `json:"heartbeatAt,omitempty" jsonschema:"Latest daemon heartbeat (RFC 3339)"`
	Warning     string     `json:"warning,omitempty" jsonschema:"What to tell the user when schedules may not fire"`
}

func assessDaemon(status DaemonStatus, found bool, now time.Time) daemonHealth {
	if !found {
		return daemonHealth{
			State:   daemonNotStarted,
			Warning: "the schedule daemon has never run on this host, so schedules will not fire; start it with `kodelet-extension-schedule daemon`",
		}
	}
	health := daemonHealth{State: daemonRunning, HeartbeatAt: &status.HeartbeatAt}
	switch {
	case status.StoppedAt != nil:
		health.State = daemonStopped
		health.Warning = fmt.Sprintf("the schedule daemon stopped at %s, so schedules will not fire until it is restarted with `kodelet-extension-schedule daemon`", formatTime(*status.StoppedAt))
	case now.Sub(status.HeartbeatAt) > 2*status.PollInterval+30*time.Second:
		health.State = daemonUnresponsive
		health.Warning = fmt.Sprintf("the schedule daemon last reported at %s and may not be running; check it or restart it with `kodelet-extension-schedule daemon`", formatTime(status.HeartbeatAt))
	}
	return health
}

// truncateOutput bounds a stored output, which runs keep for history.
func truncateOutput(output string) string {
	if len(output) <= maxOutputLength {
		return output
	}
	return strings.TrimSuffix(truncate(output, maxOutputLength), "…") + truncatedOutput
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}
