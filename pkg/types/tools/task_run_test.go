package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTaskRunSnapshotValidatesRequiredShape(t *testing.T) {
	result := &StructuredToolResult{Metadata: &ExtensionToolMetadata{Data: map[string]any{
		"taskRun": map[string]any{
			"version":   1,
			"revision":  1,
			"kind":      "code_search",
			"status":    "running",
			"phase":     "working",
			"title":     "Searching code",
			"elapsedMs": 10,
			"counts":    map[string]any{"succeeded": 1, "failed": 0, "running": 1},
			"activities": []any{
				map[string]any{"id": "read", "sequence": 1, "kind": "file_read", "label": "Read file.go", "status": "running"},
			},
		},
	}}}

	snapshot, _, ok := ExtractTaskRunSnapshot(result)
	require.True(t, ok)
	assert.Equal(t, 1, snapshot.Counts.Succeeded)

	result.Metadata.(*ExtensionToolMetadata).Data["taskRun"] = map[string]any{"version": 1}
	_, _, ok = ExtractTaskRunSnapshot(result)
	assert.False(t, ok)

	result.Metadata.(*ExtensionToolMetadata).Data["taskRun"] = map[string]any{
		"version":    1,
		"revision":   1,
		"kind":       "code_search",
		"status":     "running",
		"phase":      "working",
		"title":      "Searching code",
		"elapsedMs":  10,
		"counts":     map[string]any{"succeeded": 0, "failed": 0, "running": 1},
		"activities": []any{map[string]any{"sequence": 1, "label": "", "status": "unknown"}},
	}
	_, _, ok = ExtractTaskRunSnapshot(result)
	assert.False(t, ok)

	activities := make([]any, maxTaskRunActivities+1)
	for index := range activities {
		activities[index] = map[string]any{
			"id": "read", "sequence": index + 1, "kind": "file_read", "label": "Read file.go", "status": "succeeded",
		}
	}
	result.Metadata.(*ExtensionToolMetadata).Data["taskRun"] = map[string]any{
		"version": 1, "revision": 1, "kind": "code_search", "status": "running", "phase": "working", "title": "Searching code", "elapsedMs": 10,
		"counts": map[string]any{"succeeded": 0, "failed": 0, "running": 0}, "activities": activities,
	}
	_, _, ok = ExtractTaskRunSnapshot(result)
	assert.False(t, ok)

	unicodeTitle := "界"
	for range maxTaskRunTitleLength - 1 {
		unicodeTitle += "界"
	}
	result.Metadata.(*ExtensionToolMetadata).Data["taskRun"] = map[string]any{
		"version": 1, "revision": 1, "kind": "code_search", "status": "running", "phase": "working", "title": unicodeTitle, "elapsedMs": 10,
		"counts": map[string]any{"succeeded": 0, "failed": 0, "running": 0}, "activities": []any{},
	}
	_, _, ok = ExtractTaskRunSnapshot(result)
	assert.True(t, ok)
}
