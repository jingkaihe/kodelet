package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/pkg/errors"
)

const (
	maxNameLength        = 100
	maxInstructionLength = 64 * 1024
	upcomingRuns         = 3
)

var errNotApproved = errors.New("the user did not approve this change, or no interactive session was available to ask; do not retry unless the user asks")

type listInput struct{}

type createInput struct {
	Name        string `json:"name" jsonschema:"Short unique name"`
	Schedule    string `json:"schedule" jsonschema:"Five-field cron expression (minute hour day-of-month month day-of-week) or a descriptor such as @daily or @every 6h. Prefix with CRON_TZ=<IANA zone> to pick a timezone; otherwise the host's local time applies."`
	Instruction string `json:"instruction" jsonschema:"What each run should do, given to a new agent conversation as its first message"`
	CWD         string `json:"cwd,omitempty" jsonschema:"Directory to run in, inside the current directory; defaults to the current directory"`
	Enabled     *bool  `json:"enabled,omitempty" jsonschema:"Whether the schedule is active; defaults to true"`
}

type updateInput struct {
	ID          string  `json:"id" jsonschema:"Schedule ID from schedule_list"`
	Name        *string `json:"name,omitempty" jsonschema:"New unique name"`
	Schedule    *string `json:"schedule,omitempty" jsonschema:"New cron expression or descriptor, optionally prefixed with CRON_TZ=<IANA zone>"`
	Instruction *string `json:"instruction,omitempty" jsonschema:"New instruction"`
	CWD         *string `json:"cwd,omitempty" jsonschema:"New directory, inside the current directory"`
	Enabled     *bool   `json:"enabled,omitempty" jsonschema:"Enable or pause the schedule"`
}

type deleteInput struct {
	ID string `json:"id" jsonschema:"Schedule ID from schedule_list"`
}

// storeOpener opens the store on first use, so the extension starts even if
// the database is unavailable and never touches it unless a tool runs.
type storeOpener struct {
	path string

	mu    sync.Mutex
	store *Store
}

func (o *storeOpener) Get(ctx context.Context) (*Store, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.store != nil {
		return o.store, nil
	}
	store, err := openStore(ctx, o.path)
	if err != nil {
		return nil, err
	}
	o.store = store
	return store, nil
}

func (o *storeOpener) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.store == nil {
		return nil
	}
	err := o.store.Close()
	o.store = nil
	return err
}

type toolset struct {
	stores *storeOpener
	now    func() time.Time
}

func registerTools(ext *extension.Extension, stores *storeOpener, now func() time.Time) {
	t := &toolset{stores: stores, now: now}
	extension.RegisterTool(ext, extension.Tool[listInput]{
		Name:         "schedule_list",
		Description:  "List scheduled instructions with their next and most recent runs. Each run is a new agent conversation started by the kodelet-extension-schedule daemon, which must be running for schedules to fire.",
		Short:        "List scheduled instructions with their next and latest runs and whether the daemon is running.",
		OutputSchema: outputSchema[listOutput](),
		Execute:      t.list,
	})
	extension.RegisterTool(ext, extension.Tool[createInput]{
		Name:         "schedule_create",
		Description:  "Schedule an instruction to run as a new agent conversation on a recurring cron schedule. Runs use this conversation's model profile. The user must approve the schedule before it is saved; the call fails if they decline or cannot be asked.",
		Short:        "Schedule an instruction to run as a new conversation on a cron schedule; needs user approval.",
		OutputSchema: outputSchema[scheduleOutput](),
		Execute:      t.create,
	})
	extension.RegisterTool(ext, extension.Tool[updateInput]{
		Name:         "schedule_update",
		Description:  "Change a schedule by ID; omitted fields keep their values, and the model profile never changes. Set enabled to pause or resume it. The user must approve the change; the call fails if they decline or cannot be asked.",
		Short:        "Change, pause, or resume a schedule by ID; needs user approval.",
		OutputSchema: outputSchema[scheduleOutput](),
		Execute:      t.update,
	})
	extension.RegisterTool(ext, extension.Tool[deleteInput]{
		Name:         "schedule_delete",
		Description:  "Delete a schedule by ID, with its run history. The user must approve the deletion; the call fails if they decline or cannot be asked.",
		Short:        "Delete a schedule and its run history by ID; needs user approval.",
		OutputSchema: outputSchema[deleteOutput](),
		Execute:      t.delete,
	})
}

func (t *toolset) list(ctx context.Context, _ listInput, tc *extension.ToolContext) (extension.ToolResult, error) {
	workspace, err := currentWorkspace(tc)
	if err != nil {
		return failure(err), nil
	}
	store, err := t.stores.Get(ctx)
	if err != nil {
		return failure(err), nil
	}
	schedules, err := store.List(ctx, workspace)
	if err != nil {
		return failure(err), nil
	}
	health := t.daemonHealth(ctx, store)

	var b strings.Builder
	switch len(schedules) {
	case 0:
		b.WriteString("No schedules.\n")
	default:
		fmt.Fprintf(&b, "%s.\n", countLabel(len(schedules)))
	}
	for _, schedule := range schedules {
		b.WriteString("\n")
		writeSchedule(&b, schedule)
	}
	writeWarning(&b, health)
	return extension.ToolResult{
		Content:           b.String(),
		StructuredContent: listOutput{Schedules: newScheduleViews(schedules), Daemon: health},
		Data:              listPresentation(schedules, health),
	}, nil
}

func (t *toolset) create(ctx context.Context, in createInput, tc *extension.ToolContext) (extension.ToolResult, error) {
	workspace, err := currentWorkspace(tc)
	if err != nil {
		return failure(err), nil
	}
	now := t.now()
	schedule := Schedule{
		Workspace: workspace,
		Schedule:  strings.TrimSpace(in.Schedule),
		Profile:   currentProfile(tc),
		Enabled:   in.Enabled == nil || *in.Enabled,
	}
	spec, err := validateSpec(schedule.Schedule, now)
	if err != nil {
		return failure(err), nil
	}
	if schedule.Name, err = normalizeName(in.Name); err != nil {
		return failure(err), nil
	}
	if schedule.Instruction, err = normalizeInstruction(in.Instruction); err != nil {
		return failure(err), nil
	}
	if schedule.CWD, err = resolveCWD(tc, in.CWD); err != nil {
		return failure(err), nil
	}
	if schedule.Enabled {
		next := spec.Next(now)
		schedule.NextRunAt = &next
	}
	runs := upcomingRunsOf(schedule)

	store, err := t.stores.Get(ctx)
	if err != nil {
		return failure(err), nil
	}
	if err := checkNameAvailable(ctx, store, workspace, schedule.Name, ""); err != nil {
		return failure(err), nil
	}
	if err := confirm(ctx, tc, "Create schedule?", describeSchedule(schedule, runs), "Create"); err != nil {
		return failure(err), nil
	}
	if err := store.Create(ctx, &schedule, now); err != nil {
		return failure(err), nil
	}
	health := t.daemonHealth(ctx, store)

	var b strings.Builder
	fmt.Fprintf(&b, "Created schedule %q (%s).\n\n", schedule.Name, schedule.ID)
	writeSchedule(&b, schedule)
	if len(runs) > 0 {
		fmt.Fprintf(&b, "  upcoming runs: %s\n", formatTimes(runs))
	}
	writeWarning(&b, health)
	return extension.ToolResult{
		Content:           b.String(),
		StructuredContent: scheduleOutput{Schedule: newScheduleView(schedule), UpcomingRuns: runs, Daemon: health},
		Data:              createPresentation(schedule, runs, health),
	}, nil
}

func (t *toolset) update(ctx context.Context, in updateInput, tc *extension.ToolContext) (extension.ToolResult, error) {
	workspace, err := currentWorkspace(tc)
	if err != nil {
		return failure(err), nil
	}
	store, err := t.stores.Get(ctx)
	if err != nil {
		return failure(err), nil
	}
	current, err := store.Get(ctx, workspace, strings.TrimSpace(in.ID))
	if err != nil {
		return failure(err), nil
	}
	now := t.now()
	updated := current
	updated.LastRun = nil
	// changes describe each change for approval; changed names the fields.
	var changes, changed []string
	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return failure(err), nil
		}
		if name != current.Name {
			if err := checkNameAvailable(ctx, store, workspace, name, current.ID); err != nil {
				return failure(err), nil
			}
			updated.Name = name
			changes = append(changes, describeChange("Name", current.Name, name))
			changed = append(changed, "name")
		}
	}
	if in.Schedule != nil {
		spec := strings.TrimSpace(*in.Schedule)
		if spec != current.Schedule {
			if _, err := validateSpec(spec, now); err != nil {
				return failure(err), nil
			}
			updated.Schedule = spec
			changes = append(changes, describeChange("Schedule", current.Schedule, spec))
			changed = append(changed, "schedule")
		}
	}
	if in.Instruction != nil {
		instruction, err := normalizeInstruction(*in.Instruction)
		if err != nil {
			return failure(err), nil
		}
		if instruction != current.Instruction {
			updated.Instruction = instruction
			changes = append(changes, "Instruction:\n"+instruction)
			changed = append(changed, "instruction")
		}
	}
	if in.CWD != nil {
		cwd, err := resolveCWD(tc, *in.CWD)
		if err != nil {
			return failure(err), nil
		}
		if cwd != current.CWD {
			updated.CWD = cwd
			changes = append(changes, describeChange("Directory", current.CWD, cwd))
			changed = append(changed, "directory")
		}
	}
	if in.Enabled != nil && *in.Enabled != current.Enabled {
		updated.Enabled = *in.Enabled
		changes = append(changes, describeChange("Enabled", yesNo(current.Enabled), yesNo(updated.Enabled)))
		if updated.Enabled {
			changed = append(changed, "resumed")
		} else {
			changed = append(changed, "paused")
		}
	}
	if len(changes) == 0 {
		return failure(errors.Errorf("no changes to schedule %q", current.Name)), nil
	}

	// A new schedule or a resumed one starts from now. Otherwise next_run_at is
	// kept, so an edit cannot undo a run the daemon claims meanwhile.
	resetNext := updated.Schedule != current.Schedule || updated.Enabled != current.Enabled
	if resetNext {
		updated.NextRunAt = nil
		if updated.Enabled {
			spec, err := parseSpec(updated.Schedule)
			if err != nil {
				return failure(err), nil
			}
			next := spec.Next(now)
			updated.NextRunAt = &next
		}
	}
	message := strings.Join(changes, "\n")
	if runs := upcomingRunsOf(updated); resetNext && len(runs) > 0 {
		message += "\nUpcoming runs: " + formatTimes(runs)
	}
	if err := confirm(ctx, tc, fmt.Sprintf("Update schedule %q?", current.Name), message, "Update"); err != nil {
		return failure(err), nil
	}
	if err := store.Update(ctx, &updated, resetNext, now); err != nil {
		return failure(err), nil
	}
	if saved, err := store.Get(ctx, workspace, updated.ID); err == nil {
		updated = saved
	}
	runs := upcomingRunsOf(updated)
	health := t.daemonHealth(ctx, store)

	var b strings.Builder
	fmt.Fprintf(&b, "Updated schedule %q (%s).\n\n", updated.Name, updated.ID)
	writeSchedule(&b, updated)
	writeWarning(&b, health)
	return extension.ToolResult{
		Content:           b.String(),
		StructuredContent: scheduleOutput{Schedule: newScheduleView(updated), UpcomingRuns: runs, Daemon: health},
		Data:              updatePresentation(updated, changed, runs, health),
	}, nil
}

func (t *toolset) delete(ctx context.Context, in deleteInput, tc *extension.ToolContext) (extension.ToolResult, error) {
	workspace, err := currentWorkspace(tc)
	if err != nil {
		return failure(err), nil
	}
	store, err := t.stores.Get(ctx)
	if err != nil {
		return failure(err), nil
	}
	current, err := store.Get(ctx, workspace, strings.TrimSpace(in.ID))
	if err != nil {
		return failure(err), nil
	}
	if err := confirm(ctx, tc, fmt.Sprintf("Delete schedule %q?", current.Name), describeSchedule(current, nil), "Delete"); err != nil {
		return failure(err), nil
	}
	if err := store.Delete(ctx, workspace, current.ID); err != nil {
		return failure(err), nil
	}
	return extension.ToolResult{
		Content:           fmt.Sprintf("Deleted schedule %q (%s).", current.Name, current.ID),
		StructuredContent: deleteOutput{ID: current.ID, Name: current.Name},
		Data:              deletePresentation(current),
	}, nil
}

// upcomingRunsOf returns the next runs of an enabled schedule, starting at
// its stored next run; it is empty when the schedule is paused.
func upcomingRunsOf(schedule Schedule) []time.Time {
	runs := []time.Time{}
	if !schedule.Enabled || schedule.NextRunAt == nil {
		return runs
	}
	runs = append(runs, *schedule.NextRunAt)
	spec, err := parseSpec(schedule.Schedule)
	if err != nil {
		return runs
	}
	return append(runs, upcoming(spec, *schedule.NextRunAt, upcomingRuns-1)...)
}

func (t *toolset) daemonHealth(ctx context.Context, store *Store) daemonHealth {
	status, found, err := store.Daemon(ctx)
	if err != nil {
		return daemonHealth{State: daemonUnknown, Warning: "Could not read the schedule daemon status: " + err.Error()}
	}
	return assessDaemon(status, found, t.now())
}

// currentWorkspace returns the runner hosting this extension. Schedules are
// bound to it and never chosen by the model, so model-facing text omits it.
func currentWorkspace(tc *extension.ToolContext) (string, error) {
	if id := strings.TrimSpace(tc.RunnerID); id != "" {
		return id, nil
	}
	return "", errors.New("schedules can only be managed from a conversation running on a Kodelet runner")
}

// currentProfile returns the model profile of this conversation, which is
// often provided by an extension. Extensions cannot list the available
// profiles, so schedules inherit the current one rather than letting the model
// name one. The server resolves it again for the schedule's runner and
// directory when each run starts.
func currentProfile(tc *extension.ToolContext) string {
	return strings.TrimSpace(tc.Profile)
}

// resolveCWD resolves requested against the conversation directory, which it
// must not escape, and checks that it is a directory.
func resolveCWD(tc *extension.ToolContext, requested string) (string, error) {
	path, err := tc.ResolveWorkspacePath(strings.TrimSpace(requested))
	if err != nil {
		return "", errors.Errorf("cwd %q must be inside the current directory %s", requested, tc.CWD)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", errors.Errorf("cwd %s does not exist", path)
	}
	if !info.IsDir() {
		return "", errors.Errorf("cwd %s is not a directory", path)
	}
	return path, nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("name is required")
	case utf8.RuneCountInString(name) > maxNameLength:
		return "", errors.Errorf("name must be at most %d characters", maxNameLength)
	case strings.ContainsAny(name, "\r\n"):
		return "", errors.New("name must be a single line")
	}
	return name, nil
}

func normalizeInstruction(instruction string) (string, error) {
	instruction = strings.TrimSpace(instruction)
	switch {
	case instruction == "":
		return "", errors.New("instruction is required")
	case len(instruction) > maxInstructionLength:
		return "", errors.Errorf("instruction must be at most %d bytes", maxInstructionLength)
	}
	return instruction, nil
}

func checkNameAvailable(ctx context.Context, store *Store, workspace, name, exceptID string) error {
	schedules, err := store.List(ctx, workspace)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if schedule.Name == name && schedule.ID != exceptID {
			return errors.Wrapf(errNameTaken, "name %q", name)
		}
	}
	return nil
}

// confirm asks the user to approve a change. It fails closed when the host
// has no interactive UI, so unattended runs cannot create or alter schedules.
func confirm(ctx context.Context, tc *extension.ToolContext, title, message, button string) error {
	approved, err := tc.UI.Confirm(ctx, extension.ConfirmRequest{
		Title:             title,
		Message:           message,
		ConfirmButtonText: button,
		CancelButtonText:  "Cancel",
	})
	if err != nil {
		return errors.Wrap(err, "failed to ask the user for approval")
	}
	if !approved {
		return errNotApproved
	}
	return nil
}

func describeSchedule(schedule Schedule, runs []time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name: %s\n", schedule.Name)
	fmt.Fprintf(&b, "Schedule: %s\n", schedule.Schedule)
	if schedule.Enabled && len(runs) > 0 {
		fmt.Fprintf(&b, "Upcoming runs: %s\n", formatTimes(runs))
	}
	fmt.Fprintf(&b, "Directory: %s\n", schedule.CWD)
	fmt.Fprintf(&b, "Profile: %s\n", profileLabel(schedule.Profile))
	fmt.Fprintf(&b, "Enabled: %s\n", yesNo(schedule.Enabled))
	fmt.Fprintf(&b, "Instruction:\n%s", schedule.Instruction)
	return b.String()
}

func describeChange(field, from, to string) string {
	return fmt.Sprintf("%s: %s → %s", field, from, to)
}

// writeSchedule writes a model-facing description of schedule, including the
// full instruction so the model can edit it.
func writeSchedule(b *strings.Builder, schedule Schedule) {
	state := "enabled"
	if !schedule.Enabled {
		state = "paused"
	}
	fmt.Fprintf(b, "[%s] %s (%s)\n", schedule.ID, schedule.Name, state)
	fmt.Fprintf(b, "  schedule: %s\n", schedule.Schedule)
	if schedule.Enabled {
		if schedule.NextRunAt != nil {
			fmt.Fprintf(b, "  next run: %s\n", formatTime(*schedule.NextRunAt))
		} else {
			b.WriteString("  next run: none\n")
		}
	}
	fmt.Fprintf(b, "  cwd: %s\n", schedule.CWD)
	fmt.Fprintf(b, "  profile: %s\n", profileLabel(schedule.Profile))
	if run := schedule.LastRun; run != nil {
		fmt.Fprintf(b, "  last run: %s, scheduled for %s", run.Status, formatTime(run.ScheduledFor))
		if run.ConversationID != "" {
			fmt.Fprintf(b, ", conversation %s", run.ConversationID)
		}
		if run.Error != "" {
			fmt.Fprintf(b, ", error: %s", run.Error)
		}
		b.WriteString("\n")
	}
	b.WriteString("  instruction:\n")
	for line := range strings.SplitSeq(schedule.Instruction, "\n") {
		fmt.Fprintf(b, "    %s\n", line)
	}
}

func writeWarning(b *strings.Builder, health daemonHealth) {
	if health.Warning != "" {
		fmt.Fprintf(b, "\nWarning: %s\n", health.Warning)
	}
}

func formatTime(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}

func formatTimes(times []time.Time) string {
	formatted := make([]string, len(times))
	for i, t := range times {
		formatted[i] = formatTime(t)
	}
	return strings.Join(formatted, ", ")
}

func profileLabel(profile string) string {
	if profile == "" {
		return "server default"
	}
	return profile
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func failure(err error) extension.ToolResult {
	return extension.ToolResult{Error: err.Error()}
}
