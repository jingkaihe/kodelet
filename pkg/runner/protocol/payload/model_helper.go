package payload

import tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"

// MethodModelHelperExecute is a runner-to-control-plane request. It is not a
// user execution endpoint and requires an active delegated tool capability.
const MethodModelHelperExecute = "model.helper.execute"

// ModelHelperParams identifies the run and tool that own an internal utility.
type ModelHelperParams struct {
	RunID      string                       `json:"runId"`
	ToolCallID string                       `json:"toolCallId"`
	Request    tooltypes.ModelHelperRequest `json:"request"`
}

// ModelHelperResult carries only extracted text, never provider configuration.
type ModelHelperResult struct {
	Text string `json:"text"`
}
