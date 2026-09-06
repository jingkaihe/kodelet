package client

import (
	"context"

	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func contextWithRunnerModelHelper(ctx context.Context, peer Peer, runID, toolCallID string) context.Context {
	var helper tooltypes.ModelHelper
	if peer != nil {
		helper = func(ctx context.Context, request tooltypes.ModelHelperRequest) (string, error) {
			var result runnerpayload.ModelHelperResult
			err := peer.Call(ctx, runnerpayload.MethodModelHelperExecute, runnerpayload.ModelHelperParams{
				RunID: runID, ToolCallID: toolCallID, Request: request,
			}, &result)
			return result.Text, err
		}
	}
	// Replace any inherited helper even when the peer is absent: embedding must
	// never accidentally turn into direct-local model execution.
	return tooltypes.ContextWithModelHelper(ctx, helper)
}
