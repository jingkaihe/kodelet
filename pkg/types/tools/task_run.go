package tools

import (
	"encoding/json"
	"unicode/utf8"
)

const (
	maxTaskRunActivities       = 14
	maxTaskRunKindLength       = 64
	maxTaskRunTitleLength      = 160
	maxTaskRunDetailLength     = 160
	maxTaskRunTaskLength       = 1000
	maxTaskRunCWDLength        = 4096
	maxTaskRunActivityIDLength = 256
	maxTaskRunPreviewLength    = 180
)

// TaskRunSnapshot is a bounded accumulated view of a long-running task.
type TaskRunSnapshot struct {
	Version          int               `json:"version"`
	Revision         int               `json:"revision"`
	Kind             string            `json:"kind"`
	Status           string            `json:"status"`
	Phase            string            `json:"phase"`
	Title            string            `json:"title"`
	Detail           string            `json:"detail,omitempty"`
	Task             string            `json:"task,omitempty"`
	CWD              string            `json:"cwd,omitempty"`
	ElapsedMS        int64             `json:"elapsedMs"`
	Counts           TaskRunCounts     `json:"counts"`
	Activities       []TaskRunActivity `json:"activities"`
	OmittedSucceeded int               `json:"omittedSucceeded,omitempty"`
	OmittedFailed    int               `json:"omittedFailed,omitempty"`
	OmittedRunning   int               `json:"omittedRunning,omitempty"`
}

// TaskRunCounts contains observed activity counts by state.
type TaskRunCounts struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Running   int `json:"running"`
}

// Total returns the number of activities observed so far.
func (c TaskRunCounts) Total() int {
	return c.Succeeded + c.Failed + c.Running
}

// TaskRunActivity describes one visible task activity.
type TaskRunActivity struct {
	ID       string `json:"id"`
	Sequence int    `json:"sequence"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Detail   string `json:"detail,omitempty"`
	Status   string `json:"status"`
	Preview  string `json:"preview,omitempty"`
}

// ExtractTaskRunSnapshot reads data.taskRun from an extension tool result.
func ExtractTaskRunSnapshot(result *StructuredToolResult) (TaskRunSnapshot, ExtensionToolMetadata, bool) {
	if result == nil {
		return TaskRunSnapshot{}, ExtensionToolMetadata{}, false
	}

	var metadata ExtensionToolMetadata
	if !ExtractMetadata(result.Metadata, &metadata) || metadata.Data == nil {
		return TaskRunSnapshot{}, ExtensionToolMetadata{}, false
	}
	raw, ok := metadata.Data["taskRun"]
	if !ok {
		return TaskRunSnapshot{}, metadata, false
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return TaskRunSnapshot{}, metadata, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || !hasTaskRunFields(fields) {
		return TaskRunSnapshot{}, metadata, false
	}
	var snapshot TaskRunSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil || !snapshot.Valid() {
		return TaskRunSnapshot{}, metadata, false
	}
	return snapshot, metadata, true
}

func hasTaskRunFields(fields map[string]json.RawMessage) bool {
	for _, name := range []string{"version", "revision", "kind", "status", "phase", "title", "elapsedMs", "counts", "activities"} {
		if len(fields[name]) == 0 || string(fields[name]) == "null" {
			return false
		}
	}
	return true
}

// Valid reports whether a task-run snapshot can be rendered safely.
func (s TaskRunSnapshot) Valid() bool {
	if s.Version != 1 || s.Revision < 0 || s.ElapsedMS < 0 || s.Activities == nil || len(s.Activities) > maxTaskRunActivities || s.Counts.Succeeded < 0 || s.Counts.Failed < 0 || s.Counts.Running < 0 || s.OmittedSucceeded < 0 || s.OmittedFailed < 0 || s.OmittedRunning < 0 {
		return false
	}
	if !validTaskRunString(s.Kind, maxTaskRunKindLength, true) || !validTaskRunString(s.Title, maxTaskRunTitleLength, true) || !validTaskRunString(s.Detail, maxTaskRunDetailLength, false) || !validTaskRunString(s.Task, maxTaskRunTaskLength, false) || !validTaskRunString(s.CWD, maxTaskRunCWDLength, false) {
		return false
	}
	switch s.Status {
	case "running", "completed", "failed":
	default:
		return false
	}
	switch s.Phase {
	case "starting", "working", "responding", "completed", "failed":
	default:
		return false
	}
	for _, activity := range s.Activities {
		if activity.Sequence < 0 || !validTaskRunString(activity.ID, maxTaskRunActivityIDLength, true) || !validTaskRunString(activity.Kind, maxTaskRunKindLength, false) || !validTaskRunString(activity.Label, maxTaskRunTitleLength, true) || !validTaskRunString(activity.Detail, maxTaskRunDetailLength, false) || !validTaskRunString(activity.Preview, maxTaskRunPreviewLength, false) {
			return false
		}
		switch activity.Status {
		case "running", "succeeded", "failed":
		default:
			return false
		}
	}
	return true
}

func validTaskRunString(value string, maxLength int, required bool) bool {
	if required && value == "" {
		return false
	}
	return utf8.RuneCountInString(value) <= maxLength
}
