package controlplane

import (
	"context"
	"sync"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type browserPolicyTestController struct {
	agentenv.RemoteController
	manifest runnerpayload.Manifest
	executed string
	opened   protocol.RunOpenParams
}

func (c *browserPolicyTestController) OpenRun(_ context.Context, _ string, params protocol.RunOpenParams) (runnerpayload.Manifest, error) {
	c.opened = params
	return c.manifest, nil
}

func (c *browserPolicyTestController) ExecuteTool(_ context.Context, params runnerpayload.ToolExecuteParams, _ func(runnerpayload.ToolUpdateParams)) (runnerpayload.ToolExecuteResult, error) {
	c.executed = params.Name
	return runnerpayload.ToolExecuteResult{}, nil
}

func TestBrowserPolicyControllerFiltersAndEnforces(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		inner := &browserPolicyTestController{manifest: runnerpayload.Manifest{
			Tools: []runnerpayload.ToolDefinition{{Name: "browser"}, {Name: "file_read"}},
		}}
		controller := browserPolicyController{RemoteController: inner, allowed: allowed}
		manifest, err := controller.OpenRun(t.Context(), "runner", protocol.RunOpenParams{BrowserEnabled: !allowed})
		require.NoError(t, err)
		assert.Equal(t, allowed, inner.opened.BrowserEnabled, "server policy must overwrite caller-supplied browser grants")
		require.Len(t, inner.manifest.Tools, 2, "do not mutate the registry's pinned manifest")
		if allowed {
			assert.Len(t, manifest.Tools, 2)
		} else {
			require.Len(t, manifest.Tools, 1)
			assert.Equal(t, "file_read", manifest.Tools[0].Name)
			digest, err := runnerpayload.ComputeManifestDigest(manifest)
			require.NoError(t, err)
			assert.Equal(t, digest, manifest.Digest)
		}
		_, err = controller.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{Name: "browser"}, nil)
		if allowed {
			require.NoError(t, err)
			assert.Equal(t, "browser", inner.executed)
		} else {
			require.ErrorContains(t, err, "browser access is disabled")
			assert.Empty(t, inner.executed, "a forged tool call must not reach the runner")
		}
		_, err = controller.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{Name: "file_read"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "file_read", inner.executed)
	}
}

type (
	ChatRequest        = chat.ChatRequest
	ChatContentBlock   = chat.ChatContentBlock
	ChatImageSource    = chat.ChatImageSource
	ChatImageURLSource = chat.ChatImageURLSource
	ChatEvent          = chat.ChatEvent
	UIInputEvent       = chat.UIInputEvent
	UIConfirmEvent     = chat.UIConfirmEvent
	UISelectEvent      = chat.UISelectEvent
	UINotifyEvent      = chat.UINotifyEvent
	ChatEventSink      = chat.ChatEventSink
)

var NewExecutor = chat.NewExecutor

type recordingChatSink struct {
	mu     sync.Mutex
	events []ChatEvent
}

func (s *recordingChatSink) Send(event ChatEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingChatSink) Events() []ChatEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChatEvent(nil), s.events...)
}
