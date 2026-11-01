package taskprogress

import (
	"context"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type update struct {
	content string
	data    map[string]any
}

type recorder struct {
	mu      sync.Mutex
	updates []update
}

func (r *recorder) Update(_ context.Context, content string, data map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, update{content, data})
	return nil
}

func (r *recorder) last() update {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates[len(r.updates)-1]
}

type fakeSession struct {
	mu       sync.Mutex
	handlers map[int]func(agent.Event)
	next     int
}

func (s *fakeSession) Subscribe(handler func(agent.Event)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handlers == nil {
		s.handlers = map[int]func(agent.Event){}
	}
	id := s.next
	s.next++
	s.handlers[id] = handler
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.handlers, id)
	}
}

func (s *fakeSession) emit(eventType string, data any) {
	s.mu.Lock()
	handlers := make([]func(agent.Event), 0, len(s.handlers))
	for _, handler := range s.handlers {
		handlers = append(handlers, handler)
	}
	s.mu.Unlock()
	for _, handler := range handlers {
		handler(agent.Event{Type: eventType, Data: data})
	}
}

func (s *fakeSession) listeners() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.handlers)
}

func options(kind, task string) Options {
	return Options{
		Kind: kind, Task: task, CWD: "/workspace",
		RunningTitle: "Searching code", CompletedTitle: "Searched code", FailedTitle: "Code search failed",
		RespondingDetail: "writing summary",
	}
}

func TestProgressTracksBoundedChildSessionActivity(t *testing.T) {
	ctx := t.Context()
	publisher := &recorder{}
	session := &fakeSession{}
	progress := New(publisher, options("code_search", "Find the update path"))
	progress.Attach(session)
	require.NoError(t, progress.Start(ctx))

	for index := range 5 {
		id := "call-" + string(rune('0'+index))
		session.emit(agent.EventToolCall, agent.ToolCall{ToolCallID: id, ToolName: "file_read", Input: map[string]any{"file_path": "/workspace/pkg/file.go"}})
		session.emit(agent.EventToolResult, agent.ToolResult{ToolCallID: id, ToolName: "file_read", Status: "completed", Result: "done"})
	}
	session.emit(agent.EventToolCall, agent.ToolCall{ToolCallID: "running", ToolName: "grep_tool", RawInput: `{"pattern":"HandleToolUpdate","path":"/workspace/pkg"}`})
	session.emit(agent.EventToolUpdate, agent.ToolResult{ToolCallID: "running", Result: "match 1\nmatch 2\n"})
	require.NoError(t, progress.Flush(ctx))

	snapshot := progress.Snapshot()
	assert.Equal(t, Counts{Succeeded: 5, Running: 1}, snapshot.Counts)
	assert.Equal(t, 2, snapshot.OmittedSucceeded)
	assert.Equal(t, "searching pkg", snapshot.Detail)
	require.Len(t, snapshot.Activities, 4)
	assert.Equal(t, "match 2", snapshot.Activities[3].Preview)
	assert.Equal(t, `Search "HandleToolUpdate" in pkg`, snapshot.Activities[3].Label)
	last := publisher.last()
	assert.Equal(t, "Searching code - searching pkg", last.content)
	assert.IsType(t, Snapshot{}, last.data["taskRun"])

	session.emit(agent.EventAssistantMessageDelta, agent.AssistantMessageDelta{DeltaContent: "Summary"})
	require.NoError(t, progress.Flush(ctx))
	assert.Equal(t, "writing summary", progress.Snapshot().Detail)

	final := progress.Finish(ctx, true, "")
	assert.Equal(t, "completed", final.Status)
	assert.Equal(t, 0, final.Counts.Running)
	assert.Equal(t, 6, final.Counts.Succeeded)
	assert.Equal(t, "Searched code", final.Title)
	assert.Equal(t, 0, session.listeners())
}

func TestProgressSupportsDirectNonAgentActivity(t *testing.T) {
	publisher := &recorder{}
	progress := New(publisher, options("download", "Fetch artifacts"))
	require.NoError(t, progress.Start(t.Context()))
	progress.StartActivity("artifact-1", ActivityOptions{Kind: "download", Label: "Download artifact.tar.zst", Detail: "downloading artifact.tar.zst"})
	progress.FinishActivity("artifact-1", true, "")
	require.NoError(t, progress.Flush(t.Context()))
	assert.Equal(t, 1, progress.Snapshot().Counts.Succeeded)
	assert.Equal(t, "running", progress.Snapshot().Status)
}

func TestProgressUsesBoundedTaskDetailBetweenActivities(t *testing.T) {
	publisher := &recorder{}
	instruction := "Investigate the task progress renderer " + strings.Repeat("carefully ", 30)
	progress := New(publisher, Options{Kind: "subagent", Task: instruction, CWD: "/workspace", RunningTitle: "Delegated task"})
	require.NoError(t, progress.Start(t.Context()))
	detail := progress.Snapshot().Detail
	assert.LessOrEqual(t, utf8.RuneCountInString(detail), 160)
	assert.True(t, strings.HasSuffix(detail, "…"))
	assert.Equal(t, "Delegated task - "+detail, publisher.last().content)

	progress.StartActivity("read-1", ActivityOptions{Kind: "file_read", Label: "Read renderer"})
	progress.FinishActivity("read-1", true, "")
	require.NoError(t, progress.Flush(t.Context()))
	assert.Equal(t, detail, progress.Snapshot().Detail)
}

func TestProgressSkipsMarkdownFencesInFailedPreviews(t *testing.T) {
	progress := New(&recorder{}, options("build", "Run tests"))
	require.NoError(t, progress.Start(t.Context()))
	progress.StartActivity("test-1", ActivityOptions{Kind: "bash", Label: "Run tests"})
	progress.FinishActivity("test-1", false, "```text\nTypeScript tests failed\n```")
	progress.StartActivity("test-2", ActivityOptions{Kind: "bash", Label: "Run more tests"})
	progress.FinishActivity("test-2", false, "```")
	require.NoError(t, progress.Flush(t.Context()))
	var failed []Activity
	for _, activity := range progress.Snapshot().Activities {
		if activity.Status == "failed" {
			failed = append(failed, activity)
		}
	}
	require.Len(t, failed, 2)
	assert.Equal(t, "TypeScript tests failed", failed[0].Preview)
	assert.Empty(t, failed[1].Preview)

	progress.StartActivity("test-3", ActivityOptions{Kind: "bash", Label: "Run final tests"})
	final := progress.Finish(t.Context(), false, "Final tests failed\n```")
	assert.Equal(t, "failed", final.Detail)
	for _, activity := range final.Activities {
		if activity.ID == "test-3" {
			assert.Equal(t, "Final tests failed", activity.Preview)
		}
	}
}

func TestFormatToolActivityUsesWorkspaceRelativePaths(t *testing.T) {
	label, detail := FormatToolActivity("grep_tool", map[string]any{"pattern": "HandleToolUpdate", "path": "/workspace/pkg"}, "/workspace")
	assert.Equal(t, `Search "HandleToolUpdate" in pkg`, label)
	assert.Equal(t, "searching pkg", detail)
	label, detail = FormatToolActivity("file_read", map[string]any{"file_path": "/etc/hosts"}, "/workspace")
	assert.Equal(t, "Read /etc/hosts", label)
	assert.Equal(t, "reading /etc/hosts", detail)
	label, detail = FormatToolActivity("bash", map[string]any{"description": "Run Unit Tests", "command": "go test"}, "/workspace")
	assert.Equal(t, "Bash: Run Unit Tests", label)
	assert.Equal(t, "run unit tests", detail)
	label, detail = FormatToolActivity("custom_tool", map[string]any{}, "/workspace")
	assert.Equal(t, "Custom tool", label)
	assert.Equal(t, "running custom tool", detail)
	label, _ = FormatToolActivity("web_search", map[string]any{"query": "a <b> & c"}, "/workspace")
	assert.Equal(t, `Search web for "a <b> & c"`, label)
}
