package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var toolNow = time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

type toolHarness struct {
	harness *extensiontest.Harness
	init    extension.InitializeResult
	store   *Store
	cwd     string
	profile string

	mu       sync.Mutex
	approve  bool
	confirms []extension.ConfirmRequest
}

// newToolHarness starts the extension in a runner workspace. With interactive
// set, the host approves confirmations unless approve is cleared.
func newToolHarness(t *testing.T, runnerID string, interactive bool) *toolHarness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "schedule.db")
	stores := &storeOpener{path: dbPath}
	t.Cleanup(func() { stores.Close() })
	ext := extension.New(extension.Metadata{Name: "schedule", Version: "test"})
	registerTools(ext, stores, func() time.Time { return toolNow })

	th := &toolHarness{cwd: t.TempDir(), profile: "anthropic", approve: true}
	var options *extensiontest.Options
	if interactive {
		options = &extensiontest.Options{Host: func(_ context.Context, call extensiontest.HostCall) (any, error) {
			if call.Method != "kodelet.ui.confirm" {
				return map[string]any{}, nil
			}
			var request extension.ConfirmRequest
			if err := call.DecodeParams(&request); err != nil {
				return nil, err
			}
			th.mu.Lock()
			defer th.mu.Unlock()
			th.confirms = append(th.confirms, request)
			return map[string]any{"status": "submitted", "confirmed": th.approve}, nil
		}}
	}
	th.harness = extensiontest.New(ext, options)
	t.Cleanup(th.harness.Close)
	params := extensiontest.DefaultInitializeParams()
	params.Extension.RunnerID = runnerID
	init, err := th.harness.Initialize(t.Context(), &params)
	require.NoError(t, err)
	th.init = init

	store, err := stores.Get(t.Context())
	require.NoError(t, err)
	th.store = store
	return th
}

func (th *toolHarness) call(t *testing.T, name string, input any) extension.ToolResult {
	t.Helper()
	result, err := th.harness.ExecuteTool(t.Context(), name, input, th.callInfo())
	require.NoError(t, err)
	return result
}

func (th *toolHarness) callInfo() extensiontest.CallOption {
	return extensiontest.WithCall(extension.CallInfo{CWD: th.cwd, Profile: th.profile})
}

func (th *toolHarness) lastConfirm(t *testing.T) extension.ConfirmRequest {
	t.Helper()
	th.mu.Lock()
	defer th.mu.Unlock()
	require.NotEmpty(t, th.confirms)
	return th.confirms[len(th.confirms)-1]
}

func TestCreateBindsScheduleToCurrentConversation(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)

	result := th.call(t, "schedule_create", map[string]any{
		"name":        "nightly review",
		"schedule":    "CRON_TZ=UTC 0 9 * * *",
		"instruction": "Review open pull requests",
	})

	require.Empty(t, result.Error)
	assert.Contains(t, result.Content, `Created schedule "nightly review"`)
	confirm := th.lastConfirm(t)
	assert.Equal(t, "Create", confirm.ConfirmButtonText)
	assert.Contains(t, confirm.Message, "Review open pull requests")
	assert.Contains(t, confirm.Message, "Directory: "+th.cwd)
	assert.Contains(t, confirm.Message, "Profile: anthropic")

	schedules, err := th.store.List(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	schedule := schedules[0]
	assert.Equal(t, "runner-a", schedule.Workspace)
	assert.Equal(t, th.cwd, schedule.CWD)
	assert.Equal(t, "anthropic", schedule.Profile)
	assert.True(t, schedule.Enabled)
	require.NotNil(t, schedule.NextRunAt)
	assert.True(t, time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC).Equal(*schedule.NextRunAt))
}

func TestToolsRejectModelChosenWorkspaceAndProfile(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	result := th.call(t, "schedule_create", map[string]any{"name": "daily", "schedule": "@daily", "instruction": "p"})
	require.Empty(t, result.Error)
	schedules, err := th.store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	id := schedules[0].ID

	for _, field := range []string{"workspace", "profile"} {
		t.Run(field, func(t *testing.T) {
			_, err := th.harness.ExecuteTool(t.Context(), "schedule_create", map[string]any{
				"name": "other", "schedule": "@daily", "instruction": "p", field: "chosen",
			}, th.callInfo())
			assert.Error(t, err)
			_, err = th.harness.ExecuteTool(t.Context(), "schedule_update", map[string]any{
				"id": id, field: "chosen",
			}, th.callInfo())
			assert.Error(t, err)
		})
	}

	schedules, err = th.store.List(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, "runner-a", schedules[0].Workspace)
	assert.Equal(t, "anthropic", schedules[0].Profile)
}

func TestCreateWithoutConversationProfileUsesServerDefault(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	th.profile = ""

	result := th.call(t, "schedule_create", map[string]any{"name": "daily", "schedule": "@daily", "instruction": "p"})

	require.Empty(t, result.Error)
	assert.Contains(t, th.lastConfirm(t).Message, "Profile: server default")
	schedules, err := th.store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	assert.Empty(t, schedules[0].Profile)
}

func TestCreateValidatesInput(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	require.NoError(t, os.Mkdir(filepath.Join(th.cwd, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(th.cwd, "file"), nil, 0o600))

	tests := []struct {
		name  string
		input map[string]any
		err   string
	}{
		{name: "bad schedule", input: map[string]any{"name": "a", "schedule": "nope", "instruction": "p"}, err: "invalid schedule"},
		{name: "blank name", input: map[string]any{"name": " ", "schedule": "@daily", "instruction": "p"}, err: "name is required"},
		{name: "blank instruction", input: map[string]any{"name": "a", "schedule": "@daily", "instruction": " "}, err: "instruction is required"},
		{name: "cwd outside", input: map[string]any{"name": "a", "schedule": "@daily", "instruction": "p", "cwd": "/"}, err: "must be inside the current directory"},
		{name: "cwd escapes", input: map[string]any{"name": "a", "schedule": "@daily", "instruction": "p", "cwd": "../"}, err: "must be inside the current directory"},
		{name: "cwd missing", input: map[string]any{"name": "a", "schedule": "@daily", "instruction": "p", "cwd": "missing"}, err: "does not exist"},
		{name: "cwd file", input: map[string]any{"name": "a", "schedule": "@daily", "instruction": "p", "cwd": "file"}, err: "not a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := th.call(t, "schedule_create", tt.input)
			assert.Contains(t, result.Error, tt.err)
		})
	}

	result := th.call(t, "schedule_create", map[string]any{"name": "a", "schedule": "@daily", "instruction": "p", "cwd": "sub"})
	require.Empty(t, result.Error)
	result = th.call(t, "schedule_create", map[string]any{"name": "a", "schedule": "@daily", "instruction": "p"})
	assert.Contains(t, result.Error, "already exists")

	schedules, err := th.store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, filepath.Join(th.cwd, "sub"), schedules[0].CWD)
}

func TestMutationsRequireApproval(t *testing.T) {
	t.Run("without interactive UI", func(t *testing.T) {
		th := newToolHarness(t, "runner-a", false)
		result := th.call(t, "schedule_create", map[string]any{"name": "a", "schedule": "@daily", "instruction": "p"})
		assert.Equal(t, errNotApproved.Error(), result.Error)
		schedules, err := th.store.List(t.Context(), "")
		require.NoError(t, err)
		assert.Empty(t, schedules)
	})

	t.Run("declined", func(t *testing.T) {
		th := newToolHarness(t, "runner-a", true)
		result := th.call(t, "schedule_create", map[string]any{"name": "a", "schedule": "@daily", "instruction": "p"})
		require.Empty(t, result.Error)
		schedules, err := th.store.List(t.Context(), "runner-a")
		require.NoError(t, err)
		id := schedules[0].ID

		th.mu.Lock()
		th.approve = false
		th.mu.Unlock()
		result = th.call(t, "schedule_update", map[string]any{"id": id, "instruction": "changed"})
		assert.Equal(t, errNotApproved.Error(), result.Error)
		result = th.call(t, "schedule_delete", map[string]any{"id": id})
		assert.Equal(t, errNotApproved.Error(), result.Error)

		saved, err := th.store.Get(t.Context(), "runner-a", id)
		require.NoError(t, err)
		assert.Equal(t, "p", saved.Instruction)
	})
}

func TestToolsRequireARunner(t *testing.T) {
	th := newToolHarness(t, "", true)
	for name, input := range map[string]map[string]any{
		"schedule_list":   {},
		"schedule_create": {"name": "a", "schedule": "@daily", "instruction": "p"},
		"schedule_delete": {"id": "abc"},
	} {
		result := th.call(t, name, input)
		assert.Contains(t, result.Error, "Kodelet runner", name)
	}
}

func TestToolsCannotReachOtherWorkspaces(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	next := toolNow.Add(time.Hour)
	other := createTestSchedule(t, th.store, "runner-b", "theirs", next)
	createTestSchedule(t, th.store, "runner-a", "ours", next)

	result := th.call(t, "schedule_list", map[string]any{})
	require.Empty(t, result.Error)
	assert.Contains(t, result.Content, "1 schedule.")
	assert.Contains(t, result.Content, "ours")
	assert.NotContains(t, result.Content, "theirs")

	result = th.call(t, "schedule_update", map[string]any{"id": other.ID, "enabled": false})
	assert.Equal(t, errNotFound.Error(), result.Error)
	result = th.call(t, "schedule_delete", map[string]any{"id": other.ID})
	assert.Equal(t, errNotFound.Error(), result.Error)
	_, err := th.store.Get(t.Context(), "runner-b", other.ID)
	assert.NoError(t, err)
}

func TestUpdateAndDelete(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	result := th.call(t, "schedule_create", map[string]any{"name": "daily", "schedule": "CRON_TZ=UTC 0 9 * * *", "instruction": "p"})
	require.Empty(t, result.Error)
	schedules, err := th.store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	id := schedules[0].ID

	// Editing from a conversation on another profile keeps the schedule's.
	th.profile = "openai"
	result = th.call(t, "schedule_update", map[string]any{"id": id, "schedule": "CRON_TZ=UTC 30 18 * * *"})
	require.Empty(t, result.Error)
	confirm := th.lastConfirm(t)
	assert.Contains(t, confirm.Message, "Schedule: CRON_TZ=UTC 0 9 * * * → CRON_TZ=UTC 30 18 * * *")
	assert.NotContains(t, confirm.Message, "Profile")
	saved, err := th.store.Get(t.Context(), "runner-a", id)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", saved.Profile)
	assert.True(t, time.Date(2026, 11, 26, 18, 30, 0, 0, time.UTC).Equal(*saved.NextRunAt))

	result = th.call(t, "schedule_update", map[string]any{"id": id, "enabled": false})
	require.Empty(t, result.Error)
	saved, err = th.store.Get(t.Context(), "runner-a", id)
	require.NoError(t, err)
	assert.False(t, saved.Enabled)
	assert.Nil(t, saved.NextRunAt)

	result = th.call(t, "schedule_update", map[string]any{"id": id, "enabled": false, "instruction": "p"})
	assert.Contains(t, result.Error, "no changes")

	result = th.call(t, "schedule_update", map[string]any{"id": id, "enabled": true})
	require.Empty(t, result.Error)
	saved, err = th.store.Get(t.Context(), "runner-a", id)
	require.NoError(t, err)
	assert.True(t, saved.Enabled)
	assert.True(t, time.Date(2026, 11, 26, 18, 30, 0, 0, time.UTC).Equal(*saved.NextRunAt))

	result = th.call(t, "schedule_delete", map[string]any{"id": id})
	require.Empty(t, result.Error)
	assert.Contains(t, th.lastConfirm(t).Title, `Delete schedule "daily"?`)
	_, err = th.store.Get(t.Context(), "runner-a", id)
	assert.ErrorIs(t, err, errNotFound)
}

func TestListReportsDaemonHealth(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)

	result := th.call(t, "schedule_list", map[string]any{})
	assert.Contains(t, result.Content, "No schedules.")
	assert.Contains(t, result.Content, "has never run on this host")

	require.NoError(t, th.store.SaveHeartbeat(t.Context(), DaemonStatus{
		PID: 1, StartedAt: toolNow, HeartbeatAt: toolNow.Add(-10 * time.Second), PollInterval: 30 * time.Second,
	}))
	result = th.call(t, "schedule_list", map[string]any{})
	assert.NotContains(t, result.Content, "Warning")
}

func TestModelFacingTextHidesRunnerBinding(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)

	declarations, err := json.Marshal(th.init.Tools)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(declarations)), "workspace")

	results := []extension.ToolResult{
		th.call(t, "schedule_create", map[string]any{"name": "daily", "schedule": "@daily", "instruction": "p"}),
		th.call(t, "schedule_list", map[string]any{}),
	}
	schedules, err := th.store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	results = append(results, th.call(t, "schedule_update", map[string]any{"id": schedules[0].ID, "instruction": "q"}))
	for _, result := range results {
		require.Empty(t, result.Error)
		encoded, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		data, err := json.Marshal(result.Data)
		require.NoError(t, err)
		for _, text := range []string{result.Content, string(encoded), string(data)} {
			assert.NotContains(t, strings.ToLower(text), "workspace")
			assert.NotContains(t, text, "runner-a")
		}
	}
}

func TestToolsDeclareCodeModeMetadata(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)

	require.Len(t, th.init.Tools, 4)
	for _, tool := range th.init.Tools {
		assert.NotEmpty(t, tool.Short, tool.Name)
		assert.LessOrEqual(t, len(tool.Short), 120, tool.Name)
		require.NotNil(t, tool.OutputSchema, tool.Name)
		assert.Equal(t, "object", tool.OutputSchema["type"], tool.Name)
		_, err := resolveSchema(tool.OutputSchema)
		assert.NoError(t, err, tool.Name)
	}
}

func resolveSchema(document map[string]any) (*jsonschema.Resolved, error) {
	data, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}

// TestStructuredContentMatchesOutputSchema checks every result against the
// schema code mode validates it with, including optional fields.
func TestStructuredContentMatchesOutputSchema(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	schemas := map[string]*jsonschema.Resolved{}
	for _, tool := range th.init.Tools {
		resolved, err := resolveSchema(tool.OutputSchema)
		require.NoError(t, err)
		schemas[tool.Name] = resolved
	}
	check := func(name string, result extension.ToolResult) {
		t.Helper()
		require.Empty(t, result.Error, name)
		require.NotNil(t, result.StructuredContent, name)
		assert.NoError(t, schemas[name].Validate(result.StructuredContent), name)
	}

	check("schedule_list", th.call(t, "schedule_list", map[string]any{}))
	created := th.call(t, "schedule_create", map[string]any{"name": "daily", "schedule": "@daily", "instruction": "p"})
	check("schedule_create", created)
	check("schedule_create", th.call(t, "schedule_create", map[string]any{"name": "paused", "schedule": "@daily", "instruction": "p", "enabled": false}))
	schedules, err := th.store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	id := schedules[0].ID
	runID, claimed, err := th.store.Claim(t.Context(), id, *schedules[0].NextRunAt, toolNow.Add(48*time.Hour), Run{Status: RunRunning, StartedAt: toolNow})
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, th.store.SetRunConversation(t.Context(), runID, "conv-1"))
	require.NoError(t, th.store.FinishRun(t.Context(), runID, RunFailed, "runner offline", runResult{}, toolNow))
	require.NoError(t, th.store.SaveHeartbeat(t.Context(), DaemonStatus{PID: 1, StartedAt: toolNow, HeartbeatAt: toolNow, PollInterval: time.Minute}))

	listed := th.call(t, "schedule_list", map[string]any{})
	check("schedule_list", listed)
	assert.Contains(t, mustJSON(t, listed.StructuredContent), "runner offline", "the last run is included")
	check("schedule_update", th.call(t, "schedule_update", map[string]any{"id": id, "enabled": false}))
	check("schedule_update", th.call(t, "schedule_update", map[string]any{"id": id, "instruction": "q"}))
	check("schedule_delete", th.call(t, "schedule_delete", map[string]any{"id": id}))
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func presentationOf(t *testing.T, result extension.ToolResult) (summary, body string) {
	t.Helper()
	var data struct {
		Presentation extension.Presentation `json:"presentation"`
	}
	require.NoError(t, json.Unmarshal([]byte(mustJSON(t, result.Data)), &data))
	assert.Equal(t, "markdown", data.Presentation.Format)
	return data.Presentation.Summary, data.Presentation.Body
}
