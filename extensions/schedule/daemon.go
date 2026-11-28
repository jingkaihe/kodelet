package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/pkg/errors"
)

const (
	storeTimeout        = 10 * time.Second
	conversationTimeout = 30 * time.Second
	maxRunErrorLength   = 2000
	maxOutputLength     = 32 * 1024
	truncatedOutput     = "\n\n[Output truncated by the schedule extension.]"
	// startGrace is how long a run left behind by a previous daemon may
	// have a conversation without a turn before it counts as never started.
	startGrace = time.Minute
)

const (
	notStartedMessage   = "the schedule daemon stopped before the run started"
	neverStartedMessage = "the run never started: the schedule daemon stopped before submitting its instruction, or its conversation was deleted"
	unsupportedMessage  = "the schedule daemon lost track of the run, and this Kodelet version cannot report its outcome; update Kodelet"
)

// daemonConfig tunes the scheduler loop.
type daemonConfig struct {
	PollInterval time.Duration
	Concurrency  int
	RunTimeout   time.Duration
	MisfireGrace time.Duration
}

func defaultDaemonConfig() daemonConfig {
	return daemonConfig{
		PollInterval: 30 * time.Second,
		Concurrency:  10,
		RunTimeout:   time.Hour,
		MisfireGrace: time.Hour,
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
	}
	return nil
}

// executor runs one scheduled instruction as a new conversation. Once the
// conversation exists, it calls started with the conversation ID, and sends
// the instruction only if started succeeds. It reports what the run did even
// when it fails. Closing detach abandons the run without stopping its
// conversation.
type executor interface {
	Execute(ctx context.Context, schedule Schedule, started func(conversationID string) error, detach <-chan struct{}) (runResult, error)
}

// conversations reads and stops the conversations of runs on the server.
type conversations interface {
	Status(ctx context.Context, conversationID string) (agent.ConversationStatus, error)
	Stop(ctx context.Context, conversationID, turnID string) error
}

// daemon claims due schedules from the store and runs them. Runs belong to
// their conversations on the server, not to the daemon: the daemon records
// outcomes from those conversations, so a run outlives a daemon restart.
type daemon struct {
	store  *Store
	exec   executor
	convs  conversations
	config daemonConfig
	now    func() time.Time
	log    *slog.Logger
	status DaemonStatus

	mu     sync.Mutex
	active map[int64]bool
	// stopping holds timed-out runs whose stop is awaiting confirmation.
	stopping map[int64]bool
	wg       sync.WaitGroup
	// detached ends when the daemon stops, abandoning the runs in active.
	detached   context.Context
	detachRuns context.CancelFunc
}

func newDaemon(store *Store, exec executor, convs conversations, config daemonConfig, log *slog.Logger) *daemon {
	detached, detachRuns := context.WithCancel(context.Background())
	return &daemon{
		store:      store,
		exec:       exec,
		convs:      convs,
		config:     config,
		now:        time.Now,
		log:        log,
		active:     map[int64]bool{},
		stopping:   map[int64]bool{},
		detached:   detached,
		detachRuns: detachRuns,
	}
}

// run schedules until ctx is cancelled, then detaches from in-flight runs.
func (d *daemon) run(ctx context.Context) error {
	hostname, _ := os.Hostname()
	d.status = DaemonStatus{
		PID:          os.Getpid(),
		Hostname:     hostname,
		Version:      version,
		StartedAt:    d.now(),
		PollInterval: d.config.PollInterval,
	}
	d.log.Info("schedule daemon started", "pollInterval", d.config.PollInterval, "concurrency", d.config.Concurrency)

	// Cancelling a run's context cancels its conversation, so runs never
	// inherit ctx; shutdown detaches from them instead.
	jobs := context.WithoutCancel(ctx)
	ticker := time.NewTicker(d.config.PollInterval)
	defer ticker.Stop()
	for {
		d.tick(ctx, jobs)
		select {
		case <-ctx.Done():
			return d.shutdown()
		case <-ticker.C:
		}
	}
}

// shutdown detaches from in-flight runs, whose conversations keep running on
// the server; the next daemon records their outcomes.
func (d *daemon) shutdown() error {
	d.detachRuns()
	d.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	d.log.Info("schedule daemon stopped")
	return d.store.MarkDaemonStopped(ctx, d.status.PID, d.now())
}

// occupancy counts unfinished runs, which block their schedule's next
// occurrence and count toward the concurrency limit.
type occupancy struct {
	schedules map[string]bool
	runs      int
}

// tick records a heartbeat, settles finished runs, and dispatches every due
// schedule.
func (d *daemon) tick(ctx, jobs context.Context) {
	now := d.now()
	d.status.HeartbeatAt = now
	if err := d.store.SaveHeartbeat(ctx, d.status); err != nil {
		d.log.Warn("failed to record heartbeat", "error", err)
	}
	occupied, err := d.reconcileRuns(ctx)
	if err != nil {
		d.log.Error("failed to read running runs", "error", err)
		return
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
		d.dispatch(ctx, jobs, schedule, now, occupied)
	}
}

// reconcileRuns settles the unfinished runs this process is not executing
// whose conversations have finished, and returns the runs still going.
func (d *daemon) reconcileRuns(ctx context.Context) (*occupancy, error) {
	runs, err := d.store.RunningRuns(ctx)
	if err != nil {
		return nil, err
	}
	occupied := &occupancy{schedules: map[string]bool{}}
	for _, run := range runs {
		if ctx.Err() == nil && !d.isActive(run.ID) && d.reconcile(ctx, run, nil) {
			continue
		}
		occupied.schedules[run.ScheduleID] = true
		occupied.runs++
	}
	return occupied, nil
}

func (d *daemon) dispatch(ctx, jobs context.Context, schedule Schedule, now time.Time, occupied *occupancy) {
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
	case occupied.schedules[schedule.ID]:
		d.skip(ctx, log, schedule.ID, due, next, now, "the previous run was still in progress")
	case occupied.runs >= d.config.Concurrency:
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
			occupied.schedules[schedule.ID] = true
			occupied.runs++
			d.start(jobs, claimed, runningRun{ID: runID, ScheduleID: schedule.ID, StartedAt: now}, log)
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

func (d *daemon) isActive(runID int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active[runID]
}

// markStopping records that a timed-out run is being stopped and reports
// whether it was not already.
func (d *daemon) markStopping(runID int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopping[runID] {
		return false
	}
	d.stopping[runID] = true
	return true
}

func (d *daemon) activeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.active)
}

func (d *daemon) start(jobs context.Context, schedule Schedule, run runningRun, log *slog.Logger) {
	d.mu.Lock()
	d.active[run.ID] = true
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.active, run.ID)
			d.mu.Unlock()
		}()
		d.execute(jobs, schedule, run, log.With("run", run.ID))
	}()
}

// execute runs a claimed run and records its outcome once it is known. A run
// that ends without a confirmed outcome, such as when kodelet acp exits, is
// settled from its conversation, now or by a later tick.
func (d *daemon) execute(jobs context.Context, schedule Schedule, run runningRun, log *slog.Logger) {
	log.Info("starting run")
	ctx := jobs
	if d.config.RunTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(jobs, d.config.RunTimeout)
		defer cancel()
	}
	result, panicked, err := d.callExecutor(ctx, schedule, &run, log)
	result.Output = truncateOutput(result.Output)
	ended := &runEnd{err: err, timedOut: err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)}
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(jobs), storeTimeout)
	defer cancel()
	finish := func(status RunStatus, message string) {
		if err := d.store.FinishRun(storeCtx, run.ID, status, message, result, d.now()); err != nil {
			log.Error("failed to record run outcome", "error", err)
			return
		}
		logOutcome(log, status, message)
	}
	switch {
	case err == nil:
		finish(RunSucceeded, "")
	case panicked:
		finish(RunFailed, err.Error())
	case errors.Is(err, errRunGone):
		// There is no run left to record the outcome on.
		log.Warn("run not started", "error", err)
	case run.ConversationID == "" && d.detached.Err() != nil:
		finish(RunCanceled, notStartedMessage)
	case run.ConversationID == "":
		finish(RunFailed, ended.message(d.config))
	default:
		// The conversation may still be running on the server.
		if err := d.store.SaveRunProgress(storeCtx, run.ID, result); err != nil {
			log.Error("failed to record run progress", "error", err)
		}
		if d.detached.Err() != nil {
			log.Info("left the run running on the server; the next daemon start records its outcome")
			return
		}
		if !d.reconcile(d.detached, run, ended) {
			log.Info("waiting for the run's conversation to finish", "error", err)
		}
	}
}

func (d *daemon) callExecutor(ctx context.Context, schedule Schedule, run *runningRun, log *slog.Logger) (result runResult, panicked bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result, panicked, err = runResult{}, true, errors.Errorf("internal error: %v", recovered)
		}
	}()
	result, err = d.exec.Execute(ctx, schedule, func(conversationID string) error {
		// Without the conversation ID on record, a daemon that crashes could
		// never find the run, so the instruction is not sent.
		storeCtx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		defer cancel()
		if err := d.store.SetRunConversation(storeCtx, run.ID, conversationID); err != nil {
			return errors.Wrap(err, "did not send the instruction")
		}
		run.ConversationID = conversationID
		log.Info("run conversation started", "conversation", conversationID)
		return nil
	}, d.detached.Done())
	return result, false, err
}

// runEnd is how this process's execution of a run ended, when it ended
// without a confirmed outcome.
type runEnd struct {
	err      error
	timedOut bool
}

func (e *runEnd) message(config daemonConfig) string {
	if e.timedOut {
		return config.timeoutMessage()
	}
	return truncate(e.err.Error(), maxRunErrorLength)
}

func (c daemonConfig) timeoutMessage() string {
	return fmt.Sprintf("timed out after %s", c.RunTimeout)
}

// reconcile records the outcome of an unfinished run from its conversation on
// the server, and stops the conversation once the run times out. ended is how
// this process's execution of the run ended, or nil for a run a previous
// daemon left behind. It reports whether the run is finished.
func (d *daemon) reconcile(ctx context.Context, run runningRun, ended *runEnd) bool {
	log := d.log.With("schedule", run.ScheduleID, "run", run.ID, "conversation", run.ConversationID)
	age := d.now().Sub(run.StartedAt)
	timedOut := (d.config.RunTimeout > 0 && age >= d.config.RunTimeout) || (ended != nil && ended.timedOut)
	if run.ConversationID == "" {
		return d.settle(ctx, log, run, RunFailed, notStartedMessage, "")
	}
	statusCtx, cancel := context.WithTimeout(ctx, conversationTimeout)
	status, err := d.convs.Status(statusCtx, run.ConversationID)
	cancel()
	switch {
	case errors.Is(err, agent.ErrConversationNotFound):
	case errors.Is(err, agent.ErrConversationsUnsupported):
		if ended != nil {
			return d.settle(ctx, log, run, RunFailed, ended.message(d.config), "")
		}
		return d.settle(ctx, log, run, RunFailed, unsupportedMessage, "")
	case err != nil:
		log.Warn("failed to check the run's conversation; retrying on the next poll", "error", err)
		return false
	}
	turn := status.LatestTurn
	if turn == nil {
		switch {
		case ended != nil:
			return d.settle(ctx, log, run, RunFailed, ended.message(d.config), "")
		case age < startGrace:
			return false
		default:
			return d.settle(ctx, log, run, RunFailed, neverStartedMessage, "")
		}
	}
	switch turn.Status {
	case agent.TurnAccepted, agent.TurnRunning:
		if !timedOut {
			return false
		}
		// The run keeps its slot until the server confirms the turn ended;
		// recording it as timed out sooner could overlap the next run.
		if d.markStopping(run.ID) {
			log.Warn("stopping a run that exceeded the run timeout", "timeout", d.config.RunTimeout)
		}
		stopCtx, cancel := context.WithTimeout(ctx, conversationTimeout)
		defer cancel()
		if err := d.convs.Stop(stopCtx, run.ConversationID, turn.ID); err != nil {
			log.Warn("failed to stop the run's conversation; retrying on the next poll", "error", err)
		}
		return false
	case agent.TurnSucceeded:
		return d.settle(ctx, log, run, RunSucceeded, "", turn.Result)
	case agent.TurnCancelled:
		if timedOut {
			return d.settle(ctx, log, run, RunFailed, d.config.timeoutMessage(), "")
		}
		return d.settle(ctx, log, run, RunCanceled, errRunCancelled.Error(), "")
	case agent.TurnInterrupted:
		return d.settle(ctx, log, run, RunFailed, firstNonEmpty(turn.Error, "the Kodelet server stopped before the run finished"), "")
	default:
		message := turn.Error
		if message == "" && ended != nil {
			message = ended.message(d.config)
		}
		return d.settle(ctx, log, run, RunFailed, firstNonEmpty(message, "the run failed"), "")
	}
}

// settle records a reconciled outcome. It reports whether the run is
// finished, including when it already was.
func (d *daemon) settle(ctx context.Context, log *slog.Logger, run runningRun, status RunStatus, message, output string) bool {
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	settled, err := d.store.SettleRun(storeCtx, run.ID, status, truncate(message, maxRunErrorLength), truncateOutput(output), d.now())
	if err != nil {
		log.Error("failed to record run outcome", "error", err)
		return false
	}
	d.mu.Lock()
	delete(d.stopping, run.ID)
	d.mu.Unlock()
	if settled {
		logOutcome(log, status, message)
	}
	return true
}

func logOutcome(log *slog.Logger, status RunStatus, message string) {
	if message != "" {
		log.Warn("run finished", "status", status, "error", message)
	} else {
		log.Info("run finished", "status", status)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
