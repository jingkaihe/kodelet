package tools

import "encoding/json"

// CodeExecutionMetadata is the persisted parent snapshot of a code invocation.
// Child details are UI-only; model output contains only explicit selections.
type CodeExecutionMetadata struct {
	Status     string                `json:"status"`
	DurationMs int64                 `json:"durationMs"`
	Outputs    []json.RawMessage     `json:"outputs,omitempty"` // Legacy text/JSON snapshots.
	Items      []CodeExecutionOutput `json:"items,omitempty"`
	Calls      []CodeExecutionCall   `json:"calls"`
}

// CodeExecutionOutput distinguishes explicit media selections from ordinary JSON.
// Binary image data is stored separately, never in code execution metadata.
type CodeExecutionOutput struct {
	Type       string          `json:"type"`
	Value      json.RawMessage `json:"value,omitempty"`
	ArtifactID string          `json:"artifactId,omitempty"`
	Detail     string          `json:"detail,omitempty"`
}

// ToolType identifies the code execution result renderer.
func (CodeExecutionMetadata) ToolType() string { return "code_execute" }

// CodeExecutionCall describes one child, with bounded final details for the UI.
type CodeExecutionCall struct {
	CallID         string                `json:"callId"`
	ToolName       string                `json:"toolName"`
	Status         string                `json:"status"`
	DurationMs     int64                 `json:"durationMs"`
	ErrorKind      string                `json:"errorKind,omitempty"`
	Input          json.RawMessage       `json:"input,omitempty"`
	Result         *StructuredToolResult `json:"result,omitempty"`
	DetailsOmitted bool                  `json:"detailsOmitted,omitempty"`
}
