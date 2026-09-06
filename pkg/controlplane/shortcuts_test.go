package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type shortcutFixture struct {
	server       *Server
	request      chat.WorkspaceShortcutRequest
	registration protocol.RegisterResult
	manifest     runnerpayload.Manifest
	methods      []string
	opened       protocol.RunOpenParams
	onShortcut   func(context.Context) error
}

func newShortcutFixture(t *testing.T) *shortcutFixture {
	t.Helper()
	f := &shortcutFixture{server: newRunnerTestServer(t, "")}
	link := newRunnerAPITestLink()
	registration, err := f.server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceDiscovery: true, WorkspaceCWD: true, ConcurrentRuns: true},
		Host: protocol.Host{InstanceID: "shortcut-host", Hostname: "worker", OS: "linux", Arch: "amd64"}, Workspace: protocol.Workspace{Path: "/runner/startup", Name: "startup"},
	}, link)
	require.NoError(t, err)
	f.registration = registration
	require.NoError(t, f.server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
	f.manifest = runnerpayload.Manifest{
		ProtocolVersion: protocol.Version, RunnerID: registration.RunnerID, Generation: registration.Generation, WorkingDirectory: "/runner/selected",
		Shortcuts: []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "review", Generation: 1}},
		Config:    runnerpayload.EnvironmentConfig{Options: &llmtypes.ExecutionOptions{NoSkills: new(true), AllowedTools: &[]string{}}},
	}
	f.manifest.Digest, err = runnerpayload.ComputeManifestDigest(f.manifest)
	require.NoError(t, err)
	digest, err := runnerpayload.ComputeDiscoveryDigest(f.manifest)
	require.NoError(t, err)
	f.request = chat.WorkspaceShortcutRequest{Target: chat.WorkspaceTarget{RunnerID: registration.RunnerID, CWD: "/runner/selected", EnvironmentProfile: "review", Options: f.manifest.Config.Options.Clone()}, Digest: digest, Shortcut: f.manifest.Shortcuts[0]}
	link.call = func(ctx context.Context, method string, params, result any) error {
		f.methods = append(f.methods, method)
		switch method {
		case protocol.MethodRunOpen:
			f.opened = params.(protocol.RunOpenParams)
			assert.Equal(t, "/runner/selected", f.opened.CWD)
			assert.Equal(t, "review", f.opened.Agent.EnvironmentProfile)
			assert.Equal(t, f.manifest.Config.Options, f.opened.Options)
			manifest := f.manifest
			manifest.RunID = f.opened.RunID
			manifest.Shortcuts = append([]protocol.ShortcutDescriptor(nil), manifest.Shortcuts...)
			manifest.Shortcuts[0].Generation = 99
			*result.(*runnerpayload.Manifest) = manifest
		case protocol.MethodShortcutExecute:
			value := params.(runnerpayload.ShortcutExecuteParams)
			assert.Equal(t, f.opened.RunID, value.RunID)
			assert.Equal(t, f.manifest.Digest, value.Digest, "runner execution still uses the full pinned manifest")
			assert.Equal(t, uint64(99), value.Shortcut.Generation, "invoke the opened generation, not the expired probe")
			assert.Equal(t, "review", value.Shortcut.ExtensionID)
			_, bounded := ctx.Deadline()
			assert.True(t, bounded)
			if f.onShortcut != nil {
				if err := f.onShortcut(ctx); err != nil {
					return err
				}
			}
			*result.(*runnerpayload.ShortcutExecuteResult) = runnerpayload.ShortcutExecuteResult{Matched: true, Result: &extensions.ShortcutResult{Action: "submit", Message: "/review"}}
		case protocol.MethodRunCancel, protocol.MethodRunClose:
			assert.NoError(t, ctx.Err(), "cleanup must survive cancelled caller")
			_, bounded := ctx.Deadline()
			assert.True(t, bounded)
		default:
			assert.Fail(t, "shortcut must not start a provider turn or discover locally", method)
		}
		return nil
	}
	return f
}

func (f *shortcutFixture) invoke(ctx context.Context, t *testing.T, request chat.WorkspaceShortcutRequest, clientID string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/chat/shortcuts", strings.NewReader(string(mustRunnerJSON(t, request)))).WithContext(ctx)
	r.Header.Set(chat.ClientIDHeader, clientID)
	f.server.handleWorkspaceShortcut(recorder, r)
	return recorder
}

func TestWorkspaceShortcutIdleLeasePinsGenerationAndReleasesScope(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cancelled"}[cancelled], func(t *testing.T) {
			f := newShortcutFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				f.onShortcut = func(ctx context.Context) error { cancel(); <-ctx.Done(); return ctx.Err() }
			}
			response := f.invoke(ctx, t, f.request, "owner")
			assert.Equal(t, http.StatusOK, response.Code)
			if cancelled {
				assert.Contains(t, response.Body.String(), "context canceled")
			} else {
				assert.Contains(t, response.Body.String(), `"action":"submit"`)
			}
			assert.Equal(t, []string{protocol.MethodRunOpen, protocol.MethodShortcutExecute, protocol.MethodRunCancel, protocol.MethodRunClose}, f.methods)
			assert.Empty(t, f.server.activeChats)
			_, found := f.server.runnerRegistry.RunnerForConversation(f.opened.ConversationID)
			assert.False(t, found, "temporary affinity must be released")
			runner, _ := f.server.runnerRegistry.Runner(f.registration.RunnerID)
			assert.Empty(t, runner.ActiveRunIDs)
		})
	}
}

func TestWorkspaceShortcutRoutesModelProfileToTemporaryRun(t *testing.T) {
	for _, test := range []struct {
		name     string
		profile  string
		stored   *string
		want     string
		conflict bool
	}{
		{name: "daemon default"},
		{name: "explicit default", profile: "default", want: "default"},
		{name: "named", profile: "model-profile", want: "model-profile"},
		{name: "stored", stored: new("stored-profile"), want: "stored-profile"},
		{name: "stored default", stored: new("default"), want: "default"},
		{name: "stored empty pins base", stored: new(""), want: "default"},
		{name: "conflicting", profile: "default", stored: new("stored-profile"), conflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newShortcutFixture(t)
			f.request.Target.Profile = test.profile
			if test.stored != nil {
				metadata, err := conversations.AddConfigSnapshot(nil, llmtypes.Config{Profile: *test.stored, Provider: "openai", Model: "stored-model", ReasoningEffort: "medium"})
				require.NoError(t, err)
				f.server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
					return &conversations.GetConversationResponse{ID: "saved", CWD: "/runner/selected", Metadata: metadata}, nil
				}}
				require.NoError(t, f.server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "saved", f.registration.RunnerID, "review"))
				f.request.Target = chat.WorkspaceTarget{ConversationID: "saved", Profile: test.profile, Options: f.request.Target.Options}
			}
			response := f.invoke(t.Context(), t, f.request, "owner")
			if test.conflict {
				assert.Equal(t, http.StatusBadRequest, response.Code)
				assert.Empty(t, f.methods, "conflicts must fail before opening a temporary run")
				return
			}
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"matched":true`)
			assert.Equal(t, test.want, f.opened.Agent.Profile)
			assert.Equal(t, "review", f.opened.Agent.EnvironmentProfile)
		})
	}
}

func TestWorkspaceShortcutDiscoveryAllowsExecutionToolsButRejectsPolicyChanges(t *testing.T) {
	for _, policyChanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "execution-only tools", true: "changed permissions"}[policyChanged], func(t *testing.T) {
			f := newShortcutFixture(t)
			f.manifest.Config.Options.AllowedTools = nil
			digest, err := runnerpayload.ComputeDiscoveryDigest(f.manifest)
			require.NoError(t, err)
			f.request.Digest = digest
			f.request.Target.Options = f.manifest.Config.Options.Clone()
			f.manifest.Tools = []runnerpayload.ToolDefinition{{Name: "background_only", InputSchema: map[string]any{"type": "object"}, Placement: "environment"}}
			if policyChanged {
				f.manifest.Config.AllowedCommands = []string{"git status"}
			}
			f.manifest.Digest, err = runnerpayload.ComputeManifestDigest(f.manifest)
			require.NoError(t, err)
			response := f.invoke(t.Context(), t, f.request, "owner")
			if policyChanged {
				assert.Contains(t, response.Body.String(), "workspace settings changed")
				assert.NotContains(t, f.methods, protocol.MethodShortcutExecute)
			} else {
				assert.Contains(t, response.Body.String(), `"matched":true`)
				assert.Contains(t, f.methods, protocol.MethodShortcutExecute)
			}
		})
	}
}

func TestWorkspaceShortcutRejectsInvalidOrStaleBeforeExecution(t *testing.T) {
	f := newShortcutFixture(t)
	valid := string(mustRunnerJSON(t, f.request))
	for _, raw := range []string{"null", `{"unknown":true}`, `{"noExtensions":true}`, `{"noSkills":null}`, `{"model":"gpt-4.1"}`} {
		// Construct the option envelope independently of struct field ordering.
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(valid), &body))
		var option any
		require.NoError(t, json.Unmarshal([]byte(raw), &option))
		body["target"].(map[string]any)["options"] = option
		payload := string(mustRunnerJSON(t, body))
		recorder := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/chat/shortcuts", strings.NewReader(payload))
		r.Header.Set(chat.ClientIDHeader, "owner")
		f.server.handleWorkspaceShortcut(recorder, r)
		assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
	assert.Empty(t, f.methods, "bad options must fail before any runner call")
	f.server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return &conversations.GetConversationResponse{ID: "opening", CWD: "/runner/selected"}, nil
	}}
	require.NoError(t, f.server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "opening", f.registration.RunnerID, "review"))
	opening := newActiveChatRun(func() {})
	require.True(t, f.server.registerActiveChat("opening", opening))
	request := f.request
	request.Target = chat.WorkspaceTarget{ConversationID: "opening"}
	response := f.invoke(t.Context(), t, request, "observer")
	assert.Equal(t, http.StatusConflict, response.Code, "opening conversation must not fall back to an idle shortcut lease")
	assert.Empty(t, f.methods)
	f.server.unregisterActiveChat("opening", opening)
	stale := f.request
	stale.Shortcut.ExtensionID = "same-key-other-extension"
	response = f.invoke(t.Context(), t, stale, "owner")
	assert.Contains(t, response.Body.String(), "shortcut changed")
	assert.Equal(t, []string{protocol.MethodRunOpen, protocol.MethodRunCancel, protocol.MethodRunClose}, f.methods)
}

func TestWorkspaceShortcutActiveUsesPinnedDiscoveryAndCurrentOwner(t *testing.T) {
	f := newShortcutFixture(t)
	manifest, err := f.server.runnerRegistry.OpenRun(t.Context(), f.registration.RunnerID, protocol.RunOpenParams{RunID: "active-run", ConversationID: "conversation", CWD: f.request.Target.CWD, Agent: protocol.AgentDescriptor{Profile: "model-profile", EnvironmentProfile: "review"}, Options: f.request.Target.Options})
	require.NoError(t, err)
	f.server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return &conversations.GetConversationResponse{ID: "conversation", CWD: "/runner/selected", Metadata: map[string]any{"profile": "model-profile"}}, nil
	}}
	sink := &runnerUIEventSink{events: make(chan ChatEvent, 8)}
	broker := newWebUIInputBroker("conversation", sink)
	t.Cleanup(broker.close)
	detach := broker.setOwner(t.Context(), "owner", sink)
	t.Cleanup(detach)
	active := newActiveChatRun(func() {})
	active.uiInput = broker
	require.True(t, f.server.registerActiveChat("conversation", active))
	f.request.Target = chat.WorkspaceTarget{ConversationID: "conversation", Options: f.request.Target.Options}
	f.request.RunID, f.request.Shortcut = manifest.RunID, manifest.Shortcuts[0]
	f.methods = nil
	recorder := httptest.NewRecorder()
	f.server.handleGetSlashCommands(recorder, httptest.NewRequest(http.MethodGet, "/api/chat/slash-commands?conversationId=conversation", nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var discovery protocol.WorkspaceDiscoverResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &discovery))
	assert.Equal(t, manifest.RunID, discovery.RunID)
	assert.Equal(t, f.request.Digest, discovery.Digest)
	assert.Equal(t, manifest.Shortcuts, discovery.Shortcuts)
	assert.Empty(t, f.methods, "active discovery must not start another runtime")
	recorder = httptest.NewRecorder()
	f.server.handleGetSlashCommands(recorder, httptest.NewRequest(http.MethodGet, "/api/chat/slash-commands?conversationId=conversation&profile=default", nil))
	assert.Equal(t, http.StatusBadRequest, recorder.Code, "active discovery rejects conflicting profiles without rediscovery")
	conflict := f.request
	conflict.Target.Profile = "default"
	assert.Equal(t, http.StatusBadRequest, f.invoke(t.Context(), t, conflict, "owner").Code)
	assert.Empty(t, f.methods)
	response := f.invoke(t.Context(), t, f.request, "observer")
	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Empty(t, f.methods)
	stale := f.request
	stale.Shortcut.Generation--
	response = f.invoke(t.Context(), t, stale, "owner")
	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Empty(t, f.methods)
	response = f.invoke(t.Context(), t, f.request, "owner")
	assert.Contains(t, response.Body.String(), `"matched":true`)
	assert.Equal(t, []string{protocol.MethodShortcutExecute}, f.methods)
	entered := make(chan struct{})
	f.onShortcut = func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }
	done := make(chan struct{})
	go func() { defer close(done); f.invoke(t.Context(), t, f.request, "owner") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		require.FailNow(t, "shortcut did not start")
	}
	newDetach := broker.setOwner(t.Context(), "new-owner", sink)
	defer newDetach()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "ownership loss did not cancel shortcut")
	}
	assert.Equal(t, []string{protocol.MethodShortcutExecute, protocol.MethodShortcutExecute}, f.methods, "must not cancel active conversation")
	run, found := f.server.runnerRegistry.Run("active-run")
	require.True(t, found)
	assert.Equal(t, runnerregistry.RunStatusRunning, run.Status)
}

func TestPinnedShortcutOptionsRespectMandatoryAndEmptyRestrictions(t *testing.T) {
	manifest := runnerpayload.Manifest{Config: runnerpayload.EnvironmentConfig{Options: &llmtypes.ExecutionOptions{NoSkills: new(true), AllowedTools: &[]string{}}}}
	for _, options := range []*llmtypes.ExecutionOptions{nil, {}, {NoSkills: new(false)}, {AllowedTools: &[]string{"bash"}}, {AllowedTools: &[]string{}}} {
		assert.NoError(t, validatePinnedDiscoveryOptions(options, manifest))
	}
	assert.Error(t, validatePinnedDiscoveryOptions(&llmtypes.ExecutionOptions{NoExtensions: new(true)}, manifest))
	assert.Error(t, validatePinnedDiscoveryOptions(&llmtypes.ExecutionOptions{AllowedCommands: &[]string{}}, manifest))
	assert.True(t, *manifest.Config.Options.NoSkills)
	assert.Empty(t, *manifest.Config.Options.AllowedTools)
}
