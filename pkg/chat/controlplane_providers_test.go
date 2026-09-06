package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlPlaneProviderValidationBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewControlPlaneChatRunner(server.URL, "secret", "")
	require.NoError(t, err)
	for _, mutation := range []AnthropicAccountMutation{{}, {Action: "unknown"}, {Action: "logout", Alias: "work"}, {Action: "rename", Alias: "work"}, {Action: "remove", Alias: "bad name"}, {Action: "default", Alias: "work", NewAlias: "bad"}} {
		require.Error(t, client.MutateAnthropicAccount(t.Context(), mutation))
	}
	_, err = client.CompleteAnthropicLogin(t.Context(), "id", strings.Repeat("x", 8193), "")
	require.Error(t, err)
	_, err = client.AnthropicAccountUsage(t.Context(), "../work")
	require.Error(t, err)
	_, err = client.StartProviderDeviceLogin(t.Context(), "anthropic")
	require.Error(t, err)
	_, err = client.ProviderConnection(t.Context(), "../unknown")
	require.Error(t, err)
	require.Error(t, client.CancelProviderDeviceLogin(t.Context(), "codex", ""))
	assert.Zero(t, calls.Load())
	require.Error(t, client.MutateAnthropicAccount(t.Context(), AnthropicAccountMutation{Action: "logout"}))
	assert.EqualValues(t, 1, calls.Load(), "mutations must never be retried automatically")
}
