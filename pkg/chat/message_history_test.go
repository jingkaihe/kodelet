package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlPlaneMessageHistoryTransport(t *testing.T) {
	entry := messagehistory.Entry{Text: " /goal keep raw text ", ConversationID: "new-id", Profile: "model", Source: "tui"}
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "workspace", true: "saved"}[saved], func(t *testing.T) {
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/prefix/api/chat/message-history", r.URL.Path)
				assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
				assert.False(t, r.URL.Query().Has("options"))
				if saved {
					assert.Equal(t, "saved", r.URL.Query().Get("conversationId"))
					assert.False(t, r.URL.Query().Has("runnerId"))
				} else {
					assert.Equal(t, "runner", r.URL.Query().Get("runnerId"))
					assert.Equal(t, "~/project with spaces", r.URL.Query().Get("cwd"))
					assert.Equal(t, "model", r.URL.Query().Get("profile"))
					assert.Equal(t, "environment", r.URL.Query().Get("environmentProfile"))
				}
				result := protocol.WorkspaceMessageHistoryResult{CWD: "/runner/project/subdir", ScopeCWD: "/runner/project"}
				if r.Method == http.MethodPost {
					writes.Add(1)
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
					var received messagehistory.Entry
					require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
					assert.Equal(t, entry, received)
				} else {
					reads.Add(1)
					assert.Equal(t, http.MethodGet, r.Method)
					result.Messages = []string{"/goal keep raw text"}
				}
				require.NoError(t, json.NewEncoder(w).Encode(result))
			}))
			defer server.Close()
			client, err := NewControlPlaneChatRunner(server.URL+"/prefix", "client", "runner")
			require.NoError(t, err)
			target := WorkspaceTarget{CWD: "~/project with spaces", Profile: "model", EnvironmentProfile: "environment"}
			if saved {
				target = WorkspaceTarget{ConversationID: "saved"}
			}
			result, err := client.LoadMessageHistory(t.Context(), target)
			require.NoError(t, err)
			assert.Equal(t, "/runner/project", result.ScopeCWD)
			assert.Equal(t, []string{"/goal keep raw text"}, result.Messages)
			require.NoError(t, client.AppendMessageHistory(t.Context(), target, entry))
			assert.EqualValues(t, 1, reads.Load())
			assert.EqualValues(t, 1, writes.Load())
		})
	}
}

func TestControlPlaneMessageHistoryValidationAndErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("runnerId") == "invalid-json" {
			_, _ = w.Write([]byte("{"))
			return
		}
		http.Error(w, "history unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	client, err := NewControlPlaneChatRunner(server.URL, "", "")
	require.NoError(t, err)
	_, err = client.LoadMessageHistory(t.Context(), WorkspaceTarget{})
	require.ErrorContains(t, err, "requires a runner or conversation")
	_, err = client.LoadMessageHistory(t.Context(), WorkspaceTarget{RunnerID: "runner", Options: &llmtypes.ExecutionOptions{}})
	require.ErrorContains(t, err, "execution options")
	require.ErrorContains(t, client.AppendMessageHistory(t.Context(), WorkspaceTarget{RunnerID: "runner"}, messagehistory.Entry{Text: " "}), "nonempty text")
	require.ErrorContains(t, client.AppendMessageHistory(t.Context(), WorkspaceTarget{RunnerID: "runner"}, messagehistory.Entry{Text: strings.Repeat("x", 1<<20)}), "exceeds 1 MiB")
	var uninitialized *ControlPlaneChatRunner
	_, err = uninitialized.LoadMessageHistory(t.Context(), WorkspaceTarget{RunnerID: "runner"})
	require.ErrorContains(t, err, "not initialized")
	assert.Zero(t, calls.Load())
	_, err = client.LoadMessageHistory(t.Context(), WorkspaceTarget{RunnerID: "invalid-json"})
	require.ErrorContains(t, err, "decode message history")
	_, err = client.LoadMessageHistory(t.Context(), WorkspaceTarget{RunnerID: "runner"})
	require.Error(t, err)
	var responseErr *ControlPlaneHTTPError
	require.ErrorAs(t, err, &responseErr)
	assert.Equal(t, http.StatusBadGateway, responseErr.StatusCode)
	require.Error(t, client.AppendMessageHistory(t.Context(), WorkspaceTarget{RunnerID: "runner"}, messagehistory.Entry{Text: "raw text"}))
	assert.EqualValues(t, 3, calls.Load(), "failed history appends must not be retried")
}
