package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceInspectionTargetsRunnerAndRejectsInvalidRequests(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "supported", false: "old-runner"}[supported], func(t *testing.T) {
			server := newRunnerTestServer(t, "")
			link := newRunnerAPITestLink()
			calls := 0
			link.call = func(ctx context.Context, method string, params, result any) error {
				calls++
				_, bounded := ctx.Deadline()
				assert.True(t, bounded)
				assert.Equal(t, protocol.MethodWorkspaceInspect, method)
				assert.Equal(t, protocol.WorkspaceInspectParams{Operation: "recipe.show", Name: "review", Arguments: map[string]string{"target": "a b"}, CWD: "/runner/only", Profile: "model", EnvironmentProfile: "review"}, params)
				*result.(*protocol.WorkspaceInspectResult) = protocol.WorkspaceInspectResult{CWD: "/runner/only"}
				return nil
			}
			registered, err := server.runnerRegistry.Register(protocol.RegisterParams{ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceInspection: supported}, Host: protocol.Host{InstanceID: "inspection-host", Hostname: "worker", OS: "linux", Arch: "amd64"}, Workspace: protocol.Workspace{Path: "/runner/startup", Name: "startup"}}, link)
			require.NoError(t, err)
			require.NoError(t, server.runnerRegistry.Heartbeat(registered.RunnerID, registered.ConnectionID, registered.Generation, protocol.HeartbeatParams{RunnerID: registered.RunnerID, Generation: registered.Generation, State: protocol.RunnerStateIdle}))
			endpoint := "/api/chat/workspace-inspection?runnerId=" + registered.RunnerID + "&cwd=/runner/only&profile=model&environmentProfile=review"
			response := httptest.NewRecorder()
			body := `{"operation":"recipe.show","name":"review","arguments":{"target":"a b"}}`
			server.handleWorkspaceInspection(response, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body)))
			if supported {
				assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.Equal(t, 1, calls)
			} else {
				assert.Equal(t, http.StatusNotImplemented, response.Code, response.Body.String())
				assert.Contains(t, response.Body.String(), "upgrade")
				assert.Zero(t, calls)
			}
			before := calls
			for _, invalid := range []string{`{}`, `null`, `{"operation":"unknown"}`, `{"operation":"recipe.show"}`, `{"operation":"extension.list","arguments":{"x":"y"}}`, `{"operation":"recipe.list","cwd":"/client"}`, `{"operation":"recipe.list","unknown":true}`, `{"operation":"recipe.list"} {}`} {
				response = httptest.NewRecorder()
				server.handleWorkspaceInspection(response, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(invalid)))
				assert.Equal(t, http.StatusBadRequest, response.Code, invalid)
			}
			for _, extra := range []string{"&conversationId=saved", "&options={}"} {
				response = httptest.NewRecorder()
				server.handleWorkspaceInspection(response, httptest.NewRequest(http.MethodPost, endpoint+extra, strings.NewReader(body)))
				assert.Equal(t, http.StatusBadRequest, response.Code)
			}
			request := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
			request = request.WithContext(contextWithPrincipal(request.Context(), Principal{ID: "runner-admin", Roles: []string{string(RoleRunnerAdmin)}}))
			response = httptest.NewRecorder()
			server.requireRole(RoleUser, server.handleWorkspaceInspection)(response, request)
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Equal(t, before, calls, "invalid requests must not reach the runner")
		})
	}
}
