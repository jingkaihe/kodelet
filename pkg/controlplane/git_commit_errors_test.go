package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCommitReturnsBoundedRunnerDiagnostics(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, scenario := range []struct {
			name   string
			err    error
			detail string
		}{
			{"no-staged-changes", &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: "no staged changes; stage changes on the selected runner first"}, "no staged changes; stage changes on the selected runner first"},
			{"git-failure", errors.Wrap(&protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: "runner Git write-tree failed: unmerged index"}, "runner call"), "runner Git write-tree failed: unmerged index"},
			{"verbose-hook", &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: strings.Repeat("x", 4095) + "界" + strings.Repeat("y", 4096)}, strings.Repeat("x", 4095) + " [truncated]"},
			{"empty-rpc-error", &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: " "}, ""},
			{"transport-failure", errors.New("private transport internals"), ""},
		} {
			t.Run(method+"/"+scenario.name, func(t *testing.T) {
				server := newRunnerTestServer(t, "")
				link := newRunnerAPITestLink()
				calls := 0
				link.call = func(context.Context, string, any, any) error {
					calls++
					return scenario.err
				}
				registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
					ProtocolVersions: []int{protocol.Version},
					Capabilities:     protocol.RunnerCapabilities{WorkspaceGitCommit: true},
					Host:             protocol.Host{InstanceID: "commit-host"},
					Workspace:        protocol.Workspace{Path: "/runner/repo", Name: "repo"},
				}, link)
				require.NoError(t, err)
				require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
				approval, err := json.Marshal(protocol.WorkspaceGitCommitParams{CWD: "/runner/repo", Tree: strings.Repeat("a", 40), Generation: registration.Generation, Message: "feat: changes"})
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				server.handleWorkspaceCommit(recorder, httptest.NewRequest(method, "/api/git/commit?runnerId="+registration.RunnerID, strings.NewReader(string(approval))))
				require.Equal(t, http.StatusBadGateway, recorder.Code)
				var response struct {
					Error string `json:"error"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				expected := "runner commit preparation failed"
				if method == http.MethodPost {
					expected = "runner commit was not acknowledged; inspect Git history before retrying"
				}
				if scenario.detail != "" {
					expected += ": " + scenario.detail
				}
				assert.Equal(t, expected, response.Error)
				assert.Equal(t, 1, calls, "failed mutations must not be retried")
			})
		}
	}
}
