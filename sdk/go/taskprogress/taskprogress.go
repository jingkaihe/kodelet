// Package taskprogress publishes bounded, coalesced progress snapshots for a
// long-running extension tool, typically one that drives a child agent session.
//
//	progress := taskprogress.New(tc, taskprogress.Options{Kind: "code_search", ...})
//	_ = progress.Start(ctx)
//	progress.Attach(session)
//	response, err := session.RunAndWait(ctx, agent.RunOptions{Message: task})
//	snapshot := progress.Finish(ctx, err == nil, errorText(err))
//
// Each update carries {"taskRun": Snapshot} as tool data, which Kodelet hosts
// render as a live task card.
package taskprogress

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
)

const (
	updateInterval       = 200 * time.Millisecond
	maxVisibleRunning    = 8
	maxVisibleFinished   = 3
	maxActivityIDLength  = 256
	maxKindLength        = 64
	maxLabelLength       = 160
	maxPreviewLength     = 180
	maxTaskLength        = 1000
	maxCWDLength         = 4096
	snapshotVersion      = 1
	statusRunning        = "running"
	statusCompleted      = "completed"
	statusFailed         = "failed"
	activityRunning      = "running"
	activitySucceeded    = "succeeded"
	activityFailed       = "failed"
	phaseStarting        = "starting"
	phaseWorking         = "working"
	phaseResponding      = "responding"
	defaultActivityLabel = "activity"
)

// Publisher receives progress updates; *extension.ToolContext implements it.
type Publisher interface {
	Update(ctx context.Context, content string, data map[string]any) error
}

// Subscriber is an event source such as *agent.Session.
type Subscriber interface {
	Subscribe(handler func(agent.Event)) func()
}

// Labeler formats a tool call as a compact label and lower-case detail.
type Labeler func(kind string, input map[string]any, cwd string) (label, detail string)

// Options configures a Progress.
type Options struct {
	Kind             string
	Task             string
	CWD              string
	RunningTitle     string
	CompletedTitle   string
	FailedTitle      string
	RespondingDetail string
	// Labeler formats tool calls; it defaults to FormatToolActivity.
	Labeler Labeler
	// Logger receives publish failures; it defaults to slog.Default().
	Logger *slog.Logger
}

// Counts summarizes activities.
type Counts struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Running   int `json:"running"`
}

// Activity is one tracked step.
type Activity struct {
	ID       string `json:"id"`
	Sequence int    `json:"sequence"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Detail   string `json:"detail"`
	// Status is "running", "succeeded", or "failed".
	Status  string `json:"status"`
	Preview string `json:"preview,omitempty"`
}

// Snapshot is the published task state.
type Snapshot struct {
	Version  int    `json:"version"`
	Revision int    `json:"revision"`
	Kind     string `json:"kind"`
	// Status is "running", "completed", or "failed".
	Status string `json:"status"`
	// Phase is "starting", "working", "responding", "completed", or "failed".
	Phase            string     `json:"phase"`
	Title            string     `json:"title"`
	Detail           string     `json:"detail"`
	Task             string     `json:"task"`
	CWD              string     `json:"cwd"`
	ElapsedMS        int64      `json:"elapsedMs"`
	Counts           Counts     `json:"counts"`
	Activities       []Activity `json:"activities"`
	OmittedSucceeded int        `json:"omittedSucceeded"`
	OmittedFailed    int        `json:"omittedFailed,omitempty"`
	OmittedRunning   int        `json:"omittedRunning,omitempty"`
}

// ActivityOptions describes an activity started with StartActivity.
type ActivityOptions struct {
	Label  string
	Detail string
	Kind   string
}

// Progress tracks task activities and publishes coalesced snapshots.
type Progress struct {
	publisher Publisher
	options   Options
	labeler   Labeler
	logger    *slog.Logger
	started   time.Time

	mu               sync.Mutex
	ctx              context.Context
	running          map[string]*Activity
	recentSucceeded  []*Activity
	recentFailed     []*Activity
	revision         int
	sequence         int
	succeededCount   int
	failedCount      int
	status           string
	phase            string
	dirty            bool
	publishing       bool
	publishDone      chan struct{}
	unsubscribe      func()
	subscriptionCode int
}

// New creates a Progress that publishes through publisher.
func New(publisher Publisher, options Options) *Progress {
	labeler := options.Labeler
	if labeler == nil {
		labeler = FormatToolActivity
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Progress{
		publisher: publisher,
		options:   options,
		labeler:   labeler,
		logger:    logger,
		started:   time.Now(),
		ctx:       context.Background(),
		running:   map[string]*Activity{},
		status:    statusRunning,
		phase:     phaseStarting,
	}
}

// Start publishes the initial snapshot. Later updates use ctx.
func (p *Progress) Start(ctx context.Context) error {
	p.mu.Lock()
	p.ctx = ctx
	p.changedLocked(true)
	p.mu.Unlock()
	return p.Flush(ctx)
}

// Attach tracks tool calls and responses from a session until Finish or the
// next Attach.
func (p *Progress) Attach(session Subscriber) {
	p.detach()
	p.mu.Lock()
	p.subscriptionCode++
	code := p.subscriptionCode
	p.mu.Unlock()
	unsubscribe := session.Subscribe(func(event agent.Event) {
		p.mu.Lock()
		current := p.subscriptionCode == code
		p.mu.Unlock()
		if current {
			p.handleEvent(event)
		}
	})
	p.mu.Lock()
	if p.subscriptionCode == code {
		p.unsubscribe = unsubscribe
		unsubscribe = nil
	}
	p.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
}

func (p *Progress) detach() {
	p.mu.Lock()
	unsubscribe := p.unsubscribe
	p.unsubscribe = nil
	p.subscriptionCode++
	p.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
}

func (p *Progress) handleEvent(event agent.Event) {
	switch data := event.Data.(type) {
	case agent.ToolCall:
		if event.Type != agent.EventToolCall {
			return
		}
		id := strings.TrimSpace(data.ToolCallID)
		if id == "" {
			return
		}
		kind := strings.TrimSpace(data.ToolName)
		if kind == "" {
			kind = "tool"
		}
		input, _ := data.Input.(map[string]any)
		if len(input) == 0 && data.RawInput != "" {
			var parsed map[string]any
			if json.Unmarshal([]byte(data.RawInput), &parsed) == nil {
				input = parsed
			}
		}
		if input == nil {
			input = map[string]any{}
		}
		label, detail := p.labeler(kind, input, p.options.CWD)
		p.StartActivity(id, ActivityOptions{Kind: kind, Label: label, Detail: detail})
	case agent.ToolResult:
		id := strings.TrimSpace(data.ToolCallID)
		switch event.Type {
		case agent.EventToolUpdate:
			p.UpdateActivity(id, strings.TrimSpace(data.Result))
		case agent.EventToolResult:
			p.FinishActivity(id, strings.TrimSpace(data.Status) != "failed", strings.TrimSpace(data.Result))
		}
	case agent.AssistantMessageDelta:
		if strings.TrimSpace(data.DeltaContent) != "" {
			p.MarkResponding()
		}
	}
}

// StartActivity starts or relabels a running activity.
func (p *Progress) StartActivity(id string, options ActivityOptions) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing := p.running[id]; existing != nil {
		existing.Kind = options.Kind
		existing.Label = singleLine(options.Label, maxLabelLength)
		existing.Detail = singleLine(options.Detail, maxLabelLength)
	} else {
		p.sequence++
		p.running[id] = &Activity{
			ID:       id,
			Sequence: p.sequence,
			Kind:     options.Kind,
			Label:    singleLine(options.Label, maxLabelLength),
			Detail:   singleLine(options.Detail, maxLabelLength),
			Status:   activityRunning,
		}
	}
	p.phase = phaseWorking
	p.changedLocked(true)
}

// UpdateActivity sets a running activity's preview to the last meaningful
// line of result.
func (p *Progress) UpdateActivity(id, result string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	activity := p.running[id]
	if activity == nil {
		return
	}
	if preview := lastLine(result); preview != "" && activity.Preview != preview {
		activity.Preview = preview
		p.changedLocked(false)
	}
}

// FinishActivity completes a running activity.
func (p *Progress) FinishActivity(id string, success bool, result string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	activity := p.running[id]
	if activity == nil {
		return
	}
	delete(p.running, id)
	if success {
		activity.Status = activitySucceeded
		activity.Preview = ""
		p.succeededCount++
		p.recentSucceeded = keepLast(append(p.recentSucceeded, activity))
	} else {
		activity.Status = activityFailed
		if preview := lastLine(result); preview != "" {
			activity.Preview = preview
		}
		p.failedCount++
		p.recentFailed = keepLast(append(p.recentFailed, activity))
	}
	p.changedLocked(true)
}

// MarkResponding switches the phase to responding.
func (p *Progress) MarkResponding() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase == phaseResponding {
		return
	}
	p.phase = phaseResponding
	p.changedLocked(true)
}

// Finish detaches, flushes pending updates, and returns the terminal
// snapshot. Running activities are marked with the task outcome.
func (p *Progress) Finish(ctx context.Context, success bool, errorMessage string) Snapshot {
	p.detach()
	_ = p.Flush(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	if success {
		p.status, p.phase = statusCompleted, statusCompleted
	} else {
		p.status, p.phase = statusFailed, statusFailed
	}
	terminal := p.sortedRunningLocked()
	p.running = map[string]*Activity{}
	for _, activity := range terminal {
		if success {
			activity.Status = activitySucceeded
		} else {
			activity.Status = activityFailed
			if preview := lastLine(errorMessage); preview != "" {
				activity.Preview = preview
			}
		}
	}
	if success {
		p.succeededCount += len(terminal)
		p.recentSucceeded = keepLast(append(p.recentSucceeded, terminal...))
	} else {
		p.failedCount += len(terminal)
		p.recentFailed = keepLast(append(p.recentFailed, terminal...))
	}
	p.revision++
	return p.snapshotLocked()
}

// Flush waits until pending changes are published.
func (p *Progress) Flush(ctx context.Context) error {
	for {
		p.mu.Lock()
		if p.publishing {
			done := p.publishDone
			p.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if !p.dirty {
			p.mu.Unlock()
			return nil
		}
		p.scheduleLocked(true)
		p.mu.Unlock()
	}
}

// Snapshot returns the current state.
func (p *Progress) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

func (p *Progress) changedLocked(immediate bool) {
	if p.status != statusRunning {
		return
	}
	p.revision++
	p.dirty = true
	p.scheduleLocked(immediate)
}

func (p *Progress) scheduleLocked(immediate bool) {
	if p.publishing {
		return
	}
	p.publishing = true
	p.publishDone = make(chan struct{})
	delay := updateInterval
	if immediate {
		delay = 0
	}
	go p.publish(delay, p.publishDone)
}

func (p *Progress) publish(delay time.Duration, done chan struct{}) {
	if delay > 0 {
		time.Sleep(delay)
	}
	for {
		p.mu.Lock()
		if !p.dirty {
			p.publishing = false
			close(done)
			p.mu.Unlock()
			return
		}
		p.dirty = false
		snapshot := p.snapshotLocked()
		ctx := p.ctx
		p.mu.Unlock()
		content := snapshot.Title
		if snapshot.Detail != "" {
			content += " - " + snapshot.Detail
		}
		if err := p.publisher.Update(ctx, content, map[string]any{"taskRun": snapshot}); err != nil {
			p.logger.Warn("failed to publish tool update", "error", err.Error())
		}
		p.mu.Lock()
		again := p.dirty
		p.mu.Unlock()
		if again {
			time.Sleep(updateInterval)
		}
	}
}

func (p *Progress) sortedRunningLocked() []*Activity {
	running := make([]*Activity, 0, len(p.running))
	for _, activity := range p.running {
		running = append(running, activity)
	}
	slices.SortFunc(running, func(a, b *Activity) int { return a.Sequence - b.Sequence })
	return running
}

func (p *Progress) snapshotLocked() Snapshot {
	running := p.sortedRunningLocked()
	visibleRunning := running
	if len(visibleRunning) > maxVisibleRunning {
		visibleRunning = visibleRunning[len(visibleRunning)-maxVisibleRunning:]
	}
	selected := append(append(append([]*Activity(nil), p.recentSucceeded...), p.recentFailed...), visibleRunning...)
	slices.SortFunc(selected, func(a, b *Activity) int { return a.Sequence - b.Sequence })
	title := p.options.RunningTitle
	switch p.status {
	case statusCompleted:
		title = p.options.CompletedTitle
	case statusFailed:
		title = p.options.FailedTitle
	}
	activities := make([]Activity, 0, len(selected))
	for _, activity := range selected {
		copied := *activity
		copied.ID = singleLine(copied.ID, maxActivityIDLength)
		copied.Kind = singleLine(copied.Kind, maxKindLength)
		copied.Label = singleLine(copied.Label, maxLabelLength)
		copied.Detail = singleLine(copied.Detail, maxLabelLength)
		copied.Preview = singleLine(copied.Preview, maxPreviewLength)
		activities = append(activities, copied)
	}
	return Snapshot{
		Version:          snapshotVersion,
		Revision:         p.revision,
		Kind:             singleLine(p.options.Kind, maxKindLength),
		Status:           p.status,
		Phase:            p.phase,
		Title:            singleLine(title, maxLabelLength),
		Detail:           singleLine(p.detailLocked(running), maxLabelLength),
		Task:             singleLine(p.options.Task, maxTaskLength),
		CWD:              singleLine(p.options.CWD, maxCWDLength),
		ElapsedMS:        time.Since(p.started).Milliseconds(),
		Counts:           Counts{Succeeded: p.succeededCount, Failed: p.failedCount, Running: len(running)},
		Activities:       activities,
		OmittedSucceeded: max(0, p.succeededCount-len(p.recentSucceeded)),
		OmittedFailed:    max(0, p.failedCount-len(p.recentFailed)),
		OmittedRunning:   max(0, len(running)-maxVisibleRunning),
	}
}

func (p *Progress) detailLocked(running []*Activity) string {
	switch {
	case p.status == statusFailed:
		return "failed"
	case p.status == statusCompleted:
		return ""
	case p.phase == phaseStarting:
		return fallback(p.options.Task, "starting task")
	case p.phase == phaseResponding:
		return p.options.RespondingDetail
	case len(running) == 1:
		return fallback(running[0].Detail, "1 action running")
	case len(running) > 1:
		return itoa(len(running)) + " actions running"
	default:
		return fallback(p.options.Task, "planning next step")
	}
}

// keepLast retains the most recent finished activities of one status.
func keepLast(activities []*Activity) []*Activity {
	if len(activities) <= maxVisibleFinished {
		return activities
	}
	return append([]*Activity(nil), activities[len(activities)-maxVisibleFinished:]...)
}

func fallback(value, alternative string) string {
	if value != "" {
		return value
	}
	return alternative
}

func itoa(n int) string {
	data, _ := json.Marshal(n)
	return string(data)
}

func singleLine(value string, limit int) string {
	collapsed := strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(collapsed) <= limit {
		return collapsed
	}
	runes := []rune(collapsed)
	return strings.TrimRight(string(runes[:max(1, limit-1)]), " \t\r\n") + "…"
}

func quoted(value string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(singleLine(value, 80))
	return strings.TrimRight(buffer.String(), "\n")
}

func displayPath(value, cwd string) string {
	if strings.TrimSpace(value) == "" {
		return "."
	}
	expanded := value
	if strings.HasPrefix(value, "~/") {
		home, _ := os.UserHomeDir()
		expanded = filepath.Join(home, value[2:])
	}
	root, _ := filepath.Abs(cwd)
	resolved := expanded
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	relative, err := filepath.Rel(root, filepath.Clean(resolved))
	if err != nil {
		return value
	}
	if relative == "." {
		return "."
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return value
	}
	return relative
}

func lastLine(value string) string {
	var last string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "~~~") {
			last = line
		}
	}
	if last == "" {
		return ""
	}
	return singleLine(last, maxPreviewLength)
}

func text(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

// FormatToolActivity is the default Labeler for Kodelet tools.
func FormatToolActivity(toolName string, input map[string]any, cwd string) (label, detail string) {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	pathValue := text(input, "file_path")
	if pathValue == "" {
		pathValue = text(input, "path")
	}
	activityPath := displayPath(pathValue, cwd)
	switch normalized {
	case "grep", "grep_tool":
		label = "Search in " + activityPath
		if pattern := text(input, "pattern"); pattern != "" {
			label = "Search " + quoted(pattern) + " in " + activityPath
		}
		detail = "searching " + activityPath
	case "glob", "glob_tool":
		label = "Find files in " + activityPath
		if pattern := text(input, "pattern"); pattern != "" {
			label = "Find files " + quoted(pattern) + " in " + activityPath
		}
		detail = "finding files in " + activityPath
	case "file_read":
		label, detail = "Read "+activityPath, "reading "+activityPath
	case "file_write":
		label, detail = "Write "+activityPath, "writing "+activityPath
	case "file_edit":
		label, detail = "Edit "+activityPath, "editing "+activityPath
	case "apply_patch":
		label, detail = "Apply patch", "applying a patch"
	case "bash":
		description := text(input, "description")
		command := text(input, "command")
		label = "Bash: " + singleLine(fallback(fallback(description, command), "command"), 120)
		detail = strings.ToLower(singleLine(fallback(description, "running a command"), 120))
	case "web_fetch":
		label, detail = "Fetch web page", "fetching a web page"
		if url := text(input, "url"); url != "" {
			label, detail = "Fetch "+url, "fetching "+url
		}
	case "web_search", "openai_web_search":
		label, detail = "Search web", "searching the web"
		if query := text(input, "query"); query != "" {
			label = "Search web for " + quoted(query)
		}
	case "view_image":
		label, detail = "View image "+activityPath, "viewing "+activityPath
	case "skill":
		skill := fallback(text(input, "skill_name"), text(input, "skillName"))
		label, detail = "Load skill", "loading a skill"
		if skill != "" {
			label, detail = "Load skill "+skill, "loading "+skill
		}
	case "code_search":
		label, detail = "Search code", "searching code"
		if query := text(input, "query"); query != "" {
			label = "Search code: " + singleLine(query, 120)
		}
	default:
		display := strings.TrimSpace(strings.ReplaceAll(normalized, "_", " "))
		if display == "" {
			display = defaultActivityLabel
		}
		first, size := utf8.DecodeRuneInString(display)
		label = strings.ToUpper(string(first)) + display[size:]
		detail = "running " + display
	}
	return singleLine(label, maxLabelLength), singleLine(detail, maxLabelLength)
}
