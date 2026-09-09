package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageHistoryRoutesRunnerAndSavedWorkspaceWithoutLocalStorage(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(base, []byte("no local storage"), 0o600))
	t.Setenv("KODELET_BASE_PATH", base)
	server := newRunnerTestServer(t, "")
	server.conversationService = &mockConversationService{getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
		assert.Equal(t, "saved", id, "a new entry's metadata must not be resolved as saved affinity")
		return &conversations.GetConversationResponse{ID: id, CWD: "/runner/project/subdir"}, nil
	}}
	link := newRunnerAPITestLink()
	calls := 0
	link.call = func(ctx context.Context, method string, params, result any) error {
		calls++
		_, bounded := ctx.Deadline()
		assert.True(t, bounded)
		assert.Equal(t, protocol.MethodWorkspaceMessageHistory, method)
		request, ok := params.(protocol.WorkspaceMessageHistoryParams)
		require.True(t, ok)
		assert.Equal(t, "/runner/project/subdir", request.CWD)
		response := protocol.WorkspaceMessageHistoryResult{CWD: request.CWD, ScopeCWD: "/runner/project"}
		if request.Entry == nil {
			response.Messages = []string{"/goal raw history"}
		} else {
			assert.Equal(t, " /goal raw history ", request.Entry.Text)
			assert.Empty(t, request.Entry.ScopeCWD, "the daemon must not trust or resolve a client scope")
			assert.Equal(t, "tui", request.Entry.Source)
			assert.Contains(t, []string{"saved", "not-created-yet"}, request.Entry.ConversationID)
		}
		*result.(*protocol.WorkspaceMessageHistoryResult) = response
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{WorkspaceMessageHistory: true},
		Host:             protocol.Host{InstanceID: "history-host"},
		Workspace:        protocol.Workspace{Path: "/runner/startup", Name: "startup"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "saved", registration.RunnerID))
	server.config.EmbeddedRunner = &EmbeddedRunnerConfig{Workspace: "/runner/startup"}
	server.embeddedStatus.RunnerID = registration.RunnerID
	for _, query := range []string{
		"cwd=/runner/project/subdir",
		"runnerId=" + registration.RunnerID + "&cwd=/runner/project/subdir",
		"conversationId=saved",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			entry := messagehistory.Entry{Text: " /goal raw history ", ScopeCWD: "/client/poison", ConversationID: "not-created-yet"}
			if query == "conversationId=saved" {
				entry.ConversationID = "" // The saved target supplies the metadata ID.
			}
			data, err := json.Marshal(entry)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			server.handleMessageHistory(recorder, httptest.NewRequest(method, "/api/chat/message-history?"+query, strings.NewReader(string(data))))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var result protocol.WorkspaceMessageHistoryResult
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			assert.Equal(t, "/runner/project", result.ScopeCWD)
			if method == http.MethodGet {
				assert.Equal(t, []string{"/goal raw history"}, result.Messages)
			}
		}
	}
	assert.Equal(t, 6, calls)
	data, err := os.ReadFile(base)
	require.NoError(t, err)
	assert.Equal(t, "no local storage", string(data))
}

func TestMessageHistoryRejectsInvalidRequestsAndMissingPermission(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	link.call = func(context.Context, string, any, any) error {
		assert.Fail(t, "invalid requests must not reach the runner")
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceMessageHistory: true},
		Host: protocol.Host{InstanceID: "history-validation"}, Workspace: protocol.Workspace{Path: "/runner/startup", Name: "startup"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
	endpoint := "/api/chat/message-history?runnerId=" + registration.RunnerID
	for _, body := range []string{"", "null", `{}`, `{"text":" "}`, `{"text":"valid","unknown":true}`, `{"text":"valid"} {}`, `{"text":"` + strings.Repeat("x", 1<<20) + `"}`} {
		recorder := httptest.NewRecorder()
		server.handleMessageHistory(recorder, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body)))
		assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
	for _, query := range []string{"&options={}", "&conversationId=saved"} {
		recorder := httptest.NewRecorder()
		server.handleMessageHistory(recorder, httptest.NewRequest(http.MethodPost, endpoint+query, strings.NewReader(`{"text":"valid","conversation_id":"different"}`)))
		assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, authorized := range []bool{false, true} {
			request := httptest.NewRequest(method, endpoint, strings.NewReader(`{"text":"valid"}`))
			want := http.StatusUnauthorized
			if authorized {
				request = request.WithContext(contextWithPrincipal(request.Context(), Principal{ID: "runner-admin", Roles: []string{string(RoleRunnerAdmin)}}))
				want = http.StatusForbidden
			}
			recorder := httptest.NewRecorder()
			server.requireRole(RoleUser, server.handleMessageHistory)(recorder, request)
			assert.Equal(t, want, recorder.Code)
		}
	}
}

func TestMessageHistoryUnavailableRunnerFailsClosed(t *testing.T) {
	for _, scenario := range []string{"unsupported", "offline", "missing-default", "rpc-error"} {
		t.Run(scenario, func(t *testing.T) {
			server := newRunnerTestServer(t, "")
			link := newRunnerAPITestLink()
			calls := 0
			link.call = func(context.Context, string, any, any) error {
				calls++
				return errors.New("runner failed")
			}
			registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceMessageHistory: scenario != "unsupported"},
				Host: protocol.Host{InstanceID: "history-failure"}, Workspace: protocol.Workspace{Path: "/runner/startup", Name: "startup"},
			}, link)
			require.NoError(t, err)
			require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
			query, want := "?runnerId="+registration.RunnerID, http.StatusNotImplemented
			switch scenario {
			case "offline":
				server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, errors.New("offline"))
				want = http.StatusServiceUnavailable
			case "missing-default":
				query, want = "", http.StatusServiceUnavailable
			case "rpc-error":
				want = http.StatusBadGateway
			}
			recorder := httptest.NewRecorder()
			server.handleMessageHistory(recorder, httptest.NewRequest(http.MethodGet, "/api/chat/message-history"+query, nil))
			assert.Equal(t, want, recorder.Code, recorder.Body.String())
			if scenario == "unsupported" {
				assert.Contains(t, recorder.Body.String(), "upgrade")
			}
			if scenario == "rpc-error" {
				assert.Equal(t, 1, calls)
			} else {
				assert.Zero(t, calls)
			}
		})
	}
}
