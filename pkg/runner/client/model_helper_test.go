package client

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modelHelperPeer struct {
	recordingPeer
	call func(context.Context, string, any, any) error
}

func (p *modelHelperPeer) Call(ctx context.Context, method string, params, result any) error {
	return p.call(ctx, method, params, result)
}

func TestRunnerModelHelperDelegatesWithoutLocalFallback(t *testing.T) {
	request := tooltypes.ModelHelperRequest{Operation: tooltypes.ModelHelperWebFetchExtract, URL: "https://example.com", Content: "document", Prompt: "Extract title"}
	for _, tt := range []struct {
		name   string
		absent bool
		err    error
	}{
		{name: "success"},
		{name: "no peer", absent: true},
		{name: "disconnected peer", err: protocol.ErrPeerClosed},
		{name: "unauthorized", err: &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: "no active tool capability"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			localCalls, remoteCalls := 0, 0
			ctx := tooltypes.ContextWithModelHelper(t.Context(), func(context.Context, tooltypes.ModelHelperRequest) (string, error) {
				localCalls++
				return "local model must not run", nil
			})
			var peer Peer
			if !tt.absent {
				peer = &modelHelperPeer{call: func(callCtx context.Context, method string, params, result any) error {
					remoteCalls++
					assert.Same(t, ctx, callCtx)
					assert.Equal(t, runnerpayload.MethodModelHelperExecute, method)
					assert.Equal(t, runnerpayload.ModelHelperParams{RunID: "run-one", ToolCallID: "tool-one", Request: request}, params)
					if tt.err == nil {
						*result.(*runnerpayload.ModelHelperResult) = runnerpayload.ModelHelperResult{Text: "central extraction"}
					}
					return tt.err
				}}
			}
			ctx = contextWithRunnerModelHelper(ctx, peer, "run-one", "tool-one")
			text, err := tooltypes.RunModelHelper(ctx, request)
			assert.Zero(t, localCalls)
			if tt.absent {
				assert.ErrorContains(t, err, "AI-assisted web extraction is unavailable")
				assert.Zero(t, remoteCalls)
			} else {
				assert.Equal(t, 1, remoteCalls)
				if tt.err != nil {
					assert.ErrorIs(t, err, tt.err)
					assert.Empty(t, text)
				} else {
					require.NoError(t, err)
					assert.Equal(t, "central extraction", text)
				}
			}
		})
	}
}

func TestRunnerModelHelperCancellation(t *testing.T) {
	request := tooltypes.ModelHelperRequest{Operation: tooltypes.ModelHelperWebFetchExtract, URL: "https://example.com", Prompt: "Extract title"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	peer := &modelHelperPeer{call: func(callCtx context.Context, _ string, _, _ any) error {
		cancel()
		<-callCtx.Done()
		return callCtx.Err()
	}}
	ctx = contextWithRunnerModelHelper(ctx, peer, "run-one", "tool-one")
	text, err := tooltypes.RunModelHelper(ctx, request)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, text)
}
