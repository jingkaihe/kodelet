package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tool results carry structured content for code-mode scripts, which read it
// as reply.data. The types below define both that content and, through
// outputSchema, each tool's declared output schema, so the two cannot drift
// apart. They omit the runner a schedule is bound to, which is internal.

// scheduleView is a schedule as tools report it.
type scheduleView struct {
	ID          string     `json:"id" jsonschema:"Schedule ID used by schedule_update and schedule_delete"`
	Name        string     `json:"name" jsonschema:"Unique schedule name"`
	Schedule    string     `json:"schedule" jsonschema:"Cron expression or descriptor, optionally prefixed with CRON_TZ=<zone>"`
	Instruction string     `json:"instruction" jsonschema:"First message of each run's conversation"`
	CWD         string     `json:"cwd" jsonschema:"Absolute directory each run starts in"`
	Profile     string     `json:"profile,omitempty" jsonschema:"Model profile of each run; omitted when runs use the server default"`
	Enabled     bool       `json:"enabled" jsonschema:"Whether the schedule is active"`
	NextRunAt   *time.Time `json:"nextRunAt,omitempty" jsonschema:"Next run time (RFC 3339); omitted when paused"`
	CreatedAt   time.Time  `json:"createdAt" jsonschema:"Creation time (RFC 3339)"`
	UpdatedAt   time.Time  `json:"updatedAt" jsonschema:"Last change time (RFC 3339)"`
	LastRun     *runView   `json:"lastRun,omitempty" jsonschema:"Most recent run; omitted before the first"`
}

// runView is one run as tools report it.
type runView struct {
	ScheduledFor   time.Time  `json:"scheduledFor" jsonschema:"Time the run was due (RFC 3339)"`
	StartedAt      time.Time  `json:"startedAt" jsonschema:"Time the daemon handled the run (RFC 3339)"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty" jsonschema:"Time the run ended (RFC 3339); omitted while running"`
	Status         RunStatus  `json:"status" jsonschema:"Run outcome"`
	ConversationID string     `json:"conversationId,omitempty" jsonschema:"Conversation the run created"`
	Error          string     `json:"error,omitempty" jsonschema:"Why the run failed, was canceled, or was skipped"`
}

// listOutput is the structured result of schedule_list.
type listOutput struct {
	Schedules []scheduleView `json:"schedules" jsonschema:"Schedules ordered by name"`
	Daemon    daemonHealth   `json:"daemon" jsonschema:"Whether the daemon that starts runs is alive"`
}

// scheduleOutput is the structured result of schedule_create and
// schedule_update.
type scheduleOutput struct {
	Schedule     scheduleView `json:"schedule" jsonschema:"The saved schedule"`
	UpcomingRuns []time.Time  `json:"upcomingRuns" jsonschema:"Next run times (RFC 3339); empty when paused"`
	Daemon       daemonHealth `json:"daemon" jsonschema:"Whether the daemon that starts runs is alive"`
}

// deleteOutput is the structured result of schedule_delete.
type deleteOutput struct {
	ID   string `json:"id" jsonschema:"ID of the deleted schedule"`
	Name string `json:"name" jsonschema:"Name of the deleted schedule"`
}

func newScheduleView(schedule Schedule) scheduleView {
	view := scheduleView{
		ID:          schedule.ID,
		Name:        schedule.Name,
		Schedule:    schedule.Schedule,
		Instruction: schedule.Instruction,
		CWD:         schedule.CWD,
		Profile:     schedule.Profile,
		Enabled:     schedule.Enabled,
		NextRunAt:   schedule.NextRunAt,
		CreatedAt:   schedule.CreatedAt,
		UpdatedAt:   schedule.UpdatedAt,
	}
	if run := schedule.LastRun; run != nil {
		view.LastRun = &runView{
			ScheduledFor:   run.ScheduledFor,
			StartedAt:      run.StartedAt,
			FinishedAt:     run.FinishedAt,
			Status:         run.Status,
			ConversationID: run.ConversationID,
			Error:          run.Error,
		}
	}
	return view
}

func newScheduleViews(schedules []Schedule) []scheduleView {
	views := make([]scheduleView, len(schedules))
	for i, schedule := range schedules {
		views[i] = newScheduleView(schedule)
	}
	return views
}

// enums lists the allowed values of string properties, which typed Go fields
// cannot express, so code-mode declarations show them as unions.
var enums = map[string][]any{
	"status": {string(RunRunning), string(RunSucceeded), string(RunFailed), string(RunCanceled), string(RunSkipped)},
	"state":  {daemonRunning, daemonStopped, daemonUnresponsive, daemonNotStarted, daemonUnknown},
}

// outputSchema infers the JSON Schema of T. Values never contain JSON null,
// since slices are always allocated and pointers are omitted when nil, so
// nullable unions collapse to their non-null type.
func outputSchema[T any]() map[string]any {
	schema, err := jsonschema.ForType(reflect.TypeFor[T](), nil)
	if err != nil {
		panic(fmt.Sprintf("schedule: output schema for %s: %v", reflect.TypeFor[T](), err))
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		panic(err)
	}
	normalizeOutputSchema(document)
	return document
}

func normalizeOutputSchema(node any) {
	switch value := node.(type) {
	case map[string]any:
		if types, ok := value["type"].([]any); ok {
			var nonNull []any
			for _, t := range types {
				if t != "null" {
					nonNull = append(nonNull, t)
				}
			}
			if len(nonNull) == 1 {
				value["type"] = nonNull[0]
			}
		}
		if properties, ok := value["properties"].(map[string]any); ok {
			for name, property := range properties {
				if values, ok := enums[name]; ok {
					if schema, ok := property.(map[string]any); ok && schema["type"] == "string" {
						schema["enum"] = values
					}
				}
			}
		}
		for _, child := range value {
			normalizeOutputSchema(child)
		}
	case []any:
		for _, child := range value {
			normalizeOutputSchema(child)
		}
	}
}
