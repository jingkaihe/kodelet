package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCommitRoutesPreparedApprovalWithoutLocalGit(t *testing.T) {
	server := newRunnerTestServer(t, "")
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return &conversations.GetConversationResponse{ID: "commit-conversation", CWD: "/runner/selected"}, nil
	}}
	link := newRunnerAPITestLink()
	calls := 0
	approval := protocol.WorkspaceGitCommitParams{CWD: "/runner/selected", Head: strings.Repeat("a", 40), HeadRef: "refs/heads/main", Tree: strings.Repeat("b", 40), Message: "feat: approved", SignOff: true}
	link.call = func(ctx context.Context, method string, params, result any) error {
		calls++
		_, bounded := ctx.Deadline()
		assert.True(t, bounded)
		switch method {
		case protocol.MethodWorkspaceGitPrepare:
			assert.Equal(t, protocol.WorkspaceGitDiffParams{CWD: "/runner/selected"}, params)
			*result.(*protocol.WorkspaceGitCommitSnapshot) = protocol.WorkspaceGitCommitSnapshot{CWD: "/runner/selected", GitRoot: "/runner/selected", Head: approval.Head, HeadRef: approval.HeadRef, Tree: approval.Tree, Diff: "staged diff"}
		case protocol.MethodWorkspaceGitCommit:
			assert.Equal(t, approval, params)
			*result.(*protocol.WorkspaceGitCommitResult) = protocol.WorkspaceGitCommitResult{Commit: strings.Repeat("c", 40)}
		default:
			t.Errorf("unexpected RPC: %s", method)
		}
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{WorkspaceGitCommit: true},
		Host:             protocol.Host{InstanceID: "commit-host"},
		Workspace:        protocol.Workspace{Path: "/runner/startup", Name: "startup"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "commit-conversation", registration.RunnerID))
	approval.Generation = registration.Generation
	for _, query := range []string{"runnerId=" + registration.RunnerID + "&cwd=/runner/selected", "conversationId=commit-conversation"} {
		recorder := httptest.NewRecorder()
		server.handleWorkspaceCommit(recorder, httptest.NewRequest(http.MethodGet, "/api/git/commit?"+query, nil))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var snapshot protocol.WorkspaceGitCommitSnapshot
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &snapshot))
		assert.Equal(t, registration.RunnerID, snapshot.RunnerID)
		assert.Equal(t, registration.Generation, snapshot.Generation)
		data, err := json.Marshal(approval)
		require.NoError(t, err)
		recorder = httptest.NewRecorder()
		server.handleWorkspaceCommit(recorder, httptest.NewRequest(http.MethodPost, "/api/git/commit?"+query, strings.NewReader(string(data))))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}
	assert.Equal(t, 4, calls)
	for _, scenario := range []string{"generation", "directory", "unknown", "null", "multiple", "empty-message"} {
		changed := approval
		switch scenario {
		case "generation":
			changed.Generation++
		case "directory":
			changed.CWD = "/runner/other"
		case "empty-message":
			changed.Message = ""
		}
		data, err := json.Marshal(changed)
		require.NoError(t, err)
		body := string(data)
		switch scenario {
		case "unknown":
			body = strings.TrimSuffix(body, "}") + `,"extra":true}`
		case "null":
			body = "null"
		case "multiple":
			body += "{}"
		}
		recorder := httptest.NewRecorder()
		server.handleWorkspaceCommit(recorder, httptest.NewRequest(http.MethodPost, "/api/git/commit?conversationId=commit-conversation", strings.NewReader(body)))
		assert.Contains(t, []int{http.StatusBadRequest, http.StatusConflict}, recorder.Code, scenario+": "+recorder.Body.String())
	}
	recorder := httptest.NewRecorder()
	server.handleWorkspaceCommit(recorder, httptest.NewRequest(http.MethodGet, "/api/git/commit?cwd=/daemon", nil))
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, 4, calls, "invalid or untargeted requests must not reach any Git executor")
}

func TestWorkspaceCommitOldRunnerCapabilityFailsClosed(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	link.call = func(context.Context, string, any, any) error {
		t.Error("older runner must not receive commit RPC")
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceGitDiff: true}, Host: protocol.Host{InstanceID: "old-host"}, Workspace: protocol.Workspace{Path: "/runner/repo", Name: "repo"}}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
	recorder := httptest.NewRecorder()
	server.handleWorkspaceCommit(recorder, httptest.NewRequest(http.MethodGet, "/api/git/commit?runnerId="+registration.RunnerID, nil))
	assert.Equal(t, http.StatusNotImplemented, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "runner does not support workspace commits; upgrade the runner")
}
