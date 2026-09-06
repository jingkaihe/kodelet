package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationTurnQueriesDaemonWithoutLocalStore(t *testing.T) {
	localState := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(localState, []byte("client cannot open a database"), 0o600))
	t.Setenv("KODELET_BASE_PATH", localState)
	for _, status := range []string{"running", "succeeded", "failed", "cancelled", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/conversations/conversation/turns/turn", r.URL.Path)
				assert.Equal(t, "Bearer client-only", r.Header.Get("Authorization"))
				require.NoError(t, json.NewEncoder(w).Encode(chat.TurnReceipt{ConversationID: "conversation", TurnID: "turn", Status: status}))
			}))
			defer server.Close()
			cmd := &cobra.Command{Use: "turn", RunE: runConversationTurnCommand}
			cmd.Flags().String("server", "", "")
			cmd.Flags().String("auth-token", "", "")
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetArgs([]string{"--server=" + server.URL, "--auth-token=client-only", "conversation", "turn"})
			require.NoError(t, cmd.ExecuteContext(t.Context()))
			var receipt chat.TurnReceipt
			require.NoError(t, json.Unmarshal(output.Bytes(), &receipt))
			assert.Equal(t, status, receipt.Status, "status query exit success means retrieval, not execution success")
		})
	}
}
