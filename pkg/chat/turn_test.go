package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTurnClientUncertainSubmissionRetainsIDsWithoutRetry(t *testing.T) {
	for _, mode := range []string{"disconnect", "incomplete", "server error", "invalid accepted"} {
		t.Run(mode, func(t *testing.T) {
			var submitted ChatRequest
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&submitted))
				switch mode {
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					require.NoError(t, err)
					require.NoError(t, conn.Close())
				case "server error":
					http.Error(w, "lost response", http.StatusInternalServerError)
				case "invalid accepted":
					w.WriteHeader(http.StatusAccepted)
				default:
					_ = json.NewEncoder(w).Encode(ChatEvent{Kind: "conversation", ConversationID: submitted.ConversationID})
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "token", "")
			require.NoError(t, err)
			id, err := client.Run(t.Context(), ChatRequest{Message: "once"}, &collectingChatSink{})
			var uncertain *UncertainSubmissionError
			require.ErrorAs(t, err, &uncertain)
			assert.False(t, uncertain.Retryable())
			assert.NotEmpty(t, id)
			assert.Equal(t, id, uncertain.ConversationID)
			assert.Equal(t, submitted.ConversationID, uncertain.ConversationID)
			assert.Equal(t, submitted.TurnID, uncertain.TurnID)
			assert.NotEmpty(t, uncertain.TurnID)
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestTurnClientReceiptQueryValidatesScopeAndStatus(t *testing.T) {
	for _, mode := range []string{"valid", "wrong conversation", "wrong turn", "unknown status", "absent"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "Bearer client-token", r.Header.Get("Authorization"))
				assert.Equal(t, "/api/conversations/conversation/turns/turn", r.URL.Path)
				receipt := TurnReceipt{ConversationID: "conversation", TurnID: "turn", Status: "succeeded", Result: new("durable result")}
				switch mode {
				case "wrong conversation":
					receipt.ConversationID = "another"
				case "wrong turn":
					receipt.TurnID = "another"
				case "unknown status":
					receipt.Status = "whatever"
				case "absent":
					http.NotFound(w, r)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(receipt))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "client-token", "")
			require.NoError(t, err)
			receipt, err := client.GetTurnReceipt(t.Context(), "conversation", "turn")
			if mode == "valid" {
				require.NoError(t, err)
				assert.True(t, receipt.Terminal())
				assert.Equal(t, "durable result", *receipt.Result)
			} else {
				require.Error(t, err)
			}
			_, err = client.GetTurnReceipt(t.Context(), "conversation", "")
			require.Error(t, err)
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}
