package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var toolNow = time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

type toolHarness struct {
	harness *extensiontest.Harness
	schemas map[string]*jsonschema.Resolved
	tools   []extension.ToolDeclaration
	store   *Store
	cwd     string
	profile string
	clock   *testClock

	approve         bool
	whileConfirming func() error
	// profiles are the registered profiles.
	profiles []extension.ProfileDeclaration
}

func newToolHarness(t *testing.T, runnerID string, interactive bool) *toolHarness {
	t.Helper()
	return newToolHarnessWith(t, runnerID, interactive, false)
}

func newToolHarnessWith(t *testing.T, runnerID string, interactive, autoApprove bool) *toolHarness {
	t.Helper()
	return newConfiguredToolHarness(t, runnerID, interactive, autoApprove, nil)
}

// newConfiguredToolHarness initializes the extension with config.
func newConfiguredToolHarness(t *testing.T, runnerID string, interactive, autoApprove bool, config map[string]any) *toolHarness {
	t.Helper()
	stores := &storeOpener{path: filepath.Join(t.TempDir(), "schedule.db")}
	t.Cleanup(func() { stores.Close() })
	ext := extension.New(extension.Metadata{Name: "schedule", Version: "test"})
	clock := &testClock{now: toolNow}
	configureExtension(ext, stores, clock.Now, autoApprove)
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	th := &toolHarness{cwd: cwd, profile: "anthropic", approve: true, clock: clock}
	var options *extensiontest.Options
	if interactive {
		options = &extensiontest.Options{Host: func(_ context.Context, call extensiontest.HostCall) (any, error) {
			if call.Method != "kodelet.ui.confirm" {
				return nil, errors.Errorf("unexpected host call: %s", call.Method)
			}
			if th.whileConfirming != nil {
				if err := th.whileConfirming(); err != nil {
					return nil, err
				}
			}
			return map[string]any{"status": "submitted", "confirmed": th.approve}, nil
		}}
	}
	th.harness = extensiontest.New(ext, options)
	t.Cleanup(th.harness.Close)
	params := extensiontest.DefaultInitializeParams()
	params.Extension.RunnerID = runnerID
	if config != nil {
		params.Extension.Config = config
	}
	params.Capabilities["profiles"] = map[string]any{"remote": true}
	init, err := th.harness.Initialize(t.Context(), &params)
	require.NoError(t, err)
	th.tools = init.Tools
	th.profiles = init.Profiles
	th.schemas = make(map[string]*jsonschema.Resolved)
	for _, tool := range init.Tools {
		require.NotNil(t, tool.OutputSchema, tool.Name)
		schema := decodeValue[jsonschema.Schema](t, tool.OutputSchema)
		th.schemas[tool.Name], err = schema.Resolve(nil)
		require.NoError(t, err, tool.Name)
	}
	th.store, err = stores.Get(t.Context())
	require.NoError(t, err)
	return th
}

func (th *toolHarness) call(t *testing.T, name string, input any) extension.ToolResult {
	t.Helper()
	call := extensiontest.WithCall(extension.CallInfo{CWD: th.cwd, Profile: th.profile})
	result, err := th.harness.ExecuteTool(t.Context(), name, input, call)
	require.NoError(t, err)
	return result
}

// Check successful replies against the same schema code-mode clients receive.
func (th *toolHarness) success(t *testing.T, name string, input any) extension.ToolResult {
	t.Helper()
	result := th.call(t, name, input)
	require.Empty(t, result.Error, name)
	require.NoError(t, th.schemas[name].Validate(result.StructuredContent), name)
	return result
}

func decodeValue[T any](t *testing.T, v any) T {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var decoded T
	require.NoError(t, json.Unmarshal(data, &decoded))
	return decoded
}

func (th *toolHarness) lastConfirm(t *testing.T) extension.ConfirmRequest {
	t.Helper()
	calls := th.harness.HostCalls()
	require.NotEmpty(t, calls)
	call := calls[len(calls)-1]
	require.Equal(t, "kodelet.ui.confirm", call.Method)
	var request extension.ConfirmRequest
	require.NoError(t, call.DecodeParams(&request))
	return request
}

func dailyInput() map[string]any {
	return map[string]any{"name": "daily", "schedule": "CRON_TZ=UTC 0 9 * * *", "instruction": "Review PRs"}
}

func TestCreateBindsScheduleAtApproval(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	due := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	th.clock.Set(due.Add(-time.Minute))
	th.whileConfirming = func() error {
		th.clock.Set(due.Add(time.Minute))
		return nil
	}

	result := th.success(t, "schedule_create", dailyInput())
	confirm := th.lastConfirm(t)
	assert.Contains(t, confirm.Message, "Review PRs")
	assert.Contains(t, confirm.Message, "Directory: "+th.cwd)
	assert.Contains(t, confirm.Message, "Profile: anthropic")
	created := decodeValue[scheduleOutput](t, result.StructuredContent)
	schedule, err := th.store.Get(t.Context(), "runner-a", created.Schedule.ID)
	require.NoError(t, err)
	assert.Equal(t, th.cwd, schedule.CWD)
	assert.Equal(t, "anthropic", schedule.Profile)
	require.NotNil(t, schedule.NextRunAt)
	assert.Equal(t, due.AddDate(0, 0, 1), schedule.NextRunAt.UTC(), "today's run passed during approval")

	for _, reply := range []extension.ToolResult{result, th.success(t, "schedule_list", map[string]any{})} {
		data, err := json.Marshal(reply)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "runner-a", "the runner binding is internal")
	}
}

func TestToolsRejectModelChosenBindings(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	for _, field := range []string{"workspace", "profile"} {
		for name, input := range map[string]map[string]any{
			"schedule_create": dailyInput(), "schedule_update": {"id": "existing"},
		} {
			input[field] = "chosen"
			_, err := th.harness.ExecuteTool(t.Context(), name, input)
			assert.Error(t, err, "%s should reject %s in its input", name, field)
		}
	}
}

func TestCreateValidatesBeforeAskingForApproval(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	require.NoError(t, os.WriteFile(filepath.Join(th.cwd, "file"), nil, 0o600))
	createTestSchedule(t, th.store, "runner-a", "taken", toolNow)
	for _, tt := range []struct{ field, value, want string }{
		{"schedule", "nope", "invalid schedule"},
		{"name", " ", "name is required"},
		{"name", "taken", "already exists"},
		{"instruction", " ", "instruction is required"},
		{"cwd", "..", "must be inside the current directory"},
		{"cwd", "missing", "does not exist"},
		{"cwd", "file", "not a directory"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			input := dailyInput()
			input[tt.field] = tt.value
			assert.Contains(t, th.call(t, "schedule_create", input).Error, tt.want)
		})
	}
	assert.Empty(t, th.harness.HostCalls())
}

func TestCreateResolvesSymlinksWithinConversationDirectory(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	real := filepath.Join(th.cwd, "sub")
	require.NoError(t, os.Mkdir(real, 0o755))
	require.NoError(t, os.Symlink(real, filepath.Join(th.cwd, "inside")))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(th.cwd, "outside")))
	link := filepath.Join(t.TempDir(), "conversation")
	require.NoError(t, os.Symlink(th.cwd, link))
	th.cwd = link

	input := dailyInput()
	input["cwd"] = "outside"
	assert.Contains(t, th.call(t, "schedule_create", input).Error, "must be inside the current directory")
	input["cwd"] = "inside"
	result := th.success(t, "schedule_create", input)
	assert.Equal(t, real, decodeValue[scheduleOutput](t, result.StructuredContent).Schedule.CWD)
}

func TestMutationsRequireApproval(t *testing.T) {
	t.Run("without interactive UI", func(t *testing.T) {
		th := newToolHarness(t, "runner-a", false)
		assert.Equal(t, errNotApproved.Error(), th.call(t, "schedule_create", dailyInput()).Error)
		schedules, err := th.store.List(t.Context(), "runner-a")
		require.NoError(t, err)
		assert.Empty(t, schedules)
	})
	t.Run("declined", func(t *testing.T) {
		th := newToolHarness(t, "runner-a", true)
		schedule := createTestSchedule(t, th.store, "runner-a", "daily", toolNow)
		th.approve = false
		assert.Equal(t, errNotApproved.Error(), th.call(t, "schedule_update", map[string]any{"id": schedule.ID, "instruction": "changed"}).Error)
		assert.Equal(t, errNotApproved.Error(), th.call(t, "schedule_delete", map[string]any{"id": schedule.ID}).Error)
		assert.Contains(t, th.lastConfirm(t).Title, `Delete schedule "daily"`)
		saved, err := th.store.Get(t.Context(), "runner-a", schedule.ID)
		require.NoError(t, err)
		assert.Equal(t, schedule.Instruction, saved.Instruction)
	})
}

func TestAutoApproveSkipsUI(t *testing.T) {
	th := newToolHarnessWith(t, "runner-a", false, true)
	th.profile = "" // unattended runs may use the server default
	created := th.success(t, "schedule_create", dailyInput())
	schedule := decodeValue[scheduleOutput](t, created.StructuredContent).Schedule
	assert.Empty(t, schedule.Profile)
	assert.Contains(t, presentationOf(t, created).Body, "Profile:** server default")
	th.success(t, "schedule_update", map[string]any{"id": schedule.ID, "enabled": false})
	th.success(t, "schedule_delete", map[string]any{"id": schedule.ID})
	assert.Empty(t, th.harness.HostCalls())
	_, err := th.store.Get(t.Context(), "runner-a", schedule.ID)
	assert.ErrorIs(t, err, errNotFound)
}

func TestToolDescriptionsDoNotDependOnApproval(t *testing.T) {
	asking := newToolHarnessWith(t, "runner-a", false, false)
	auto := newToolHarnessWith(t, "runner-a", false, true)
	assert.Equal(t, asking.tools, auto.tools)
	for _, tool := range auto.tools {
		assert.NotEmpty(t, tool.Short, tool.Name)
		assert.NotContains(t, tool.Description+tool.Short, "approv", tool.Name)
	}
}

func TestToolsCannotReachOtherWorkspaces(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	other := createTestSchedule(t, th.store, "runner-b", "theirs", toolNow)
	createTestSchedule(t, th.store, "runner-a", "ours", toolNow)
	listed := decodeValue[listOutput](t, th.success(t, "schedule_list", map[string]any{}).StructuredContent)
	require.Len(t, listed.Schedules, 1)
	assert.Equal(t, "ours", listed.Schedules[0].Name)

	assert.Equal(t, errNotFound.Error(), th.call(t, "schedule_update", map[string]any{"id": other.ID, "enabled": false}).Error)
	assert.Equal(t, errNotFound.Error(), th.call(t, "schedule_delete", map[string]any{"id": other.ID}).Error)
	assert.Empty(t, th.harness.HostCalls(), "must not even present the foreign schedule for approval")
}

func TestToolsRequireARunner(t *testing.T) {
	th := newToolHarness(t, "", true)
	assert.Contains(t, th.call(t, "schedule_list", map[string]any{}).Error, "Kodelet runner")
	assert.Contains(t, th.call(t, "schedule_create", dailyInput()).Error, "Kodelet runner")
}

func TestPauseAndResume(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	created := th.success(t, "schedule_create", dailyInput())
	id := decodeValue[scheduleOutput](t, created.StructuredContent).Schedule.ID
	th.profile = "openai" // changing the editing conversation does not rebind the schedule
	result := th.success(t, "schedule_update", map[string]any{"id": id, "enabled": false, "schedule": "CRON_TZ=UTC 0 11 * * *"})
	paused := decodeValue[scheduleOutput](t, result.StructuredContent)
	assert.Nil(t, paused.Schedule.NextRunAt)
	assert.Empty(t, paused.UpcomingRuns)
	assert.Contains(t, presentationOf(t, result).Body, "**Changed:** schedule, paused")
	assert.Contains(t, th.lastConfirm(t).Message, "Schedule: CRON_TZ=UTC 0 9 * * * → CRON_TZ=UTC 0 11 * * *")
	assert.Contains(t, th.call(t, "schedule_update", map[string]any{"id": id, "enabled": false}).Error, "no changes")

	th.whileConfirming = func() error {
		th.clock.Set(toolNow.Add(2 * time.Hour)) // 11:00 passes while the resume prompt is open
		return nil
	}
	resumed := th.success(t, "schedule_update", map[string]any{"id": id, "enabled": true})
	schedule := decodeValue[scheduleOutput](t, resumed.StructuredContent).Schedule
	assert.Equal(t, "anthropic", schedule.Profile)
	require.NotNil(t, schedule.NextRunAt)
	assert.Equal(t, time.Date(2026, 11, 27, 11, 0, 0, 0, time.UTC), schedule.NextRunAt.UTC())
}

func TestUpdateFailsIfTheScheduleChangesDuringApproval(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	schedule := createTestSchedule(t, th.store, "runner-a", "daily", toolNow)
	th.whileConfirming = func() error {
		schedule.Instruction = "edited elsewhere"
		return th.store.Update(t.Context(), &schedule, schedule.Version, nil, toolNow)
	}
	result := th.call(t, "schedule_update", map[string]any{"id": schedule.ID, "name": "renamed"})
	assert.Equal(t, errChanged.Error(), result.Error)
	saved, err := th.store.Get(t.Context(), "runner-a", schedule.ID)
	require.NoError(t, err)
	assert.Equal(t, "daily", saved.Name)
	assert.Equal(t, "edited elsewhere", saved.Instruction)
}

func TestToolsOfferConfiguredProfiles(t *testing.T) {
	th := newConfiguredToolHarness(t, "runner-a", false, true, profilesConfig())
	require.Len(t, th.profiles, 2, "the defined profiles are registered")
	assert.Equal(t, extension.ProfileDeclaration{
		Name: "schedule-deep", Hidden: true,
		Options: map[string]any{"provider": "anthropic", "model": "claude-opus", "reasoning_effort": "high"},
	}, th.profiles[0])
	assert.False(t, th.profiles[1].Hidden)
	for _, tool := range th.tools {
		if tool.Name != "schedule_create" && tool.Name != "schedule_update" {
			continue
		}
		assert.Contains(t, tool.Description, "- schedule-deep: Suitable for deep engineering work\n- schedule-visible\n- smart: Everyday checks and summaries", tool.Name)
		properties := tool.InputSchema["properties"].(map[string]any)
		assert.Contains(t, properties, "profile", tool.Name)
		assert.Contains(t, properties, "instruction", tool.Name)
	}

	inherited := th.success(t, "schedule_create", dailyInput())
	assert.Equal(t, "anthropic", decodeValue[scheduleOutput](t, inherited.StructuredContent).Schedule.Profile, "omitted profiles use this conversation's")
	input := dailyInput()
	input["name"], input["profile"] = "deep", "deep"
	deep := decodeValue[scheduleOutput](t, th.success(t, "schedule_create", input).StructuredContent).Schedule
	assert.Equal(t, "schedule-deep", deep.Profile, "a defined profile is chosen by its configured name")
	input["name"], input["profile"] = "unknown", "opus"
	assert.Contains(t, th.call(t, "schedule_create", input).Error, `unknown profile "opus"`)

	updated := th.success(t, "schedule_update", map[string]any{"id": deep.ID, "profile": "smart"})
	assert.Equal(t, "smart", decodeValue[scheduleOutput](t, updated.StructuredContent).Schedule.Profile)
	assert.Contains(t, presentationOf(t, updated).Body, "**Changed:** profile")
	assert.Contains(t, th.call(t, "schedule_update", map[string]any{"id": deep.ID, "profile": "smart"}).Error, "no changes")
	assert.Contains(t, th.call(t, "schedule_update", map[string]any{"id": deep.ID, "profile": "opus"}).Error, "unknown profile")
}

func TestToolsWithoutProfilesKeepTheConversationProfile(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	assert.Empty(t, th.profiles)
	for _, tool := range th.tools {
		if tool.Name == "schedule_create" || tool.Name == "schedule_update" {
			assert.NotContains(t, tool.InputSchema["properties"], "profile", tool.Name)
		}
	}
	input := dailyInput()
	input["profile"] = "deep"
	_, err := th.harness.ExecuteTool(t.Context(), "schedule_create", input)
	assert.ErrorContains(t, err, "unexpected additional properties")
}

func TestDefinedProfilesNeedHostSupport(t *testing.T) {
	stores := &storeOpener{path: filepath.Join(t.TempDir(), "schedule.db")}
	t.Cleanup(func() { stores.Close() })
	ext := extension.New(extension.Metadata{Name: "schedule"})
	configureExtension(ext, stores, time.Now, true)
	harness := extensiontest.New(ext, nil)
	t.Cleanup(harness.Close)
	params := extensiontest.DefaultInitializeParams()
	params.Extension.Config = profilesConfig()
	init, err := harness.Initialize(t.Context(), &params)
	require.NoError(t, err, "an older host still gets the tools")
	assert.Empty(t, init.Profiles)
	for _, tool := range init.Tools {
		if tool.Name == "schedule_create" {
			assert.Contains(t, tool.Description, "- smart: Everyday checks")
			assert.NotContains(t, tool.Description, "schedule-deep")
		}
	}
}

func TestInvalidProfilesFailInitialization(t *testing.T) {
	ext := extension.New(extension.Metadata{Name: "schedule"})
	configureExtension(ext, &storeOpener{path: filepath.Join(t.TempDir(), "schedule.db")}, time.Now, true)
	harness := extensiontest.New(ext, nil)
	t.Cleanup(harness.Close)
	params := extensiontest.DefaultInitializeParams()
	params.Extension.Config = map[string]any{"profiles": map[string]any{"deep": map[string]any{"provider": "openai", "model": "m", "base_url": "https://evil.example"}}}
	_, err := harness.Initialize(t.Context(), &params)
	assert.ErrorContains(t, err, `invalid schedule extension config: invalid profile "deep": base_url cannot be set`)
}
