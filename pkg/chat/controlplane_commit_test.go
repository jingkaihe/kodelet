package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlPlaneCommitUncertainResultIsNeverRetried(t *testing.T) {
	for _, failure := range []string{"lost-connection", "invalid-json", "empty-result", "http-error"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1) // The runner may already have created the commit.
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
				assert.Equal(t, "runner", r.URL.Query().Get("runnerId"))
				switch failure {
				case "lost-connection":
					conn, _, err := w.(http.Hijacker).Hijack()
					require.NoError(t, err)
					_ = conn.Close()
				case "invalid-json":
					_, _ = w.Write([]byte("{"))
				case "empty-result":
					_, _ = w.Write([]byte("{}"))
				case "http-error":
					http.Error(w, "runner commit was not acknowledged; inspect Git history before retrying", http.StatusBadGateway)
				}
			}))
			defer server.Close()
			client, err := NewControlPlaneChatRunner(server.URL, "client", "runner")
			require.NoError(t, err)
			_, err = client.CreateCommit(t.Context(), WorkspaceTarget{}, protocol.WorkspaceGitCommitParams{CWD: "/runner/repo", Tree: strings.Repeat("a", 40), Generation: 1, Message: "feat: test"})
			require.ErrorContains(t, err, "check 'git log'")
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestControlPlaneCommitRejectsMalformedApprovalBeforeSubmission(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := NewControlPlaneChatRunner(server.URL, "", "runner")
	require.NoError(t, err)
	_, err = client.CreateCommit(t.Context(), WorkspaceTarget{}, protocol.WorkspaceGitCommitParams{})
	require.Error(t, err)
	assert.Zero(t, calls.Load())
}
