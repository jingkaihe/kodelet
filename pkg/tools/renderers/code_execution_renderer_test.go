package renderers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestCodeExecutionRenderer(t *testing.T) {
	result := tools.StructuredToolResult{ToolName: "code_execute", Error: "Stopped", Metadata: tools.CodeExecutionMetadata{
		Status: "failed", DurationMs: 15,
		Outputs: []json.RawMessage{json.RawMessage(`"Selected text"`), json.RawMessage(`{"id":1}`)},
		Calls: []tools.CodeExecutionCall{
			{ToolName: "search", Status: "completed"},
			{ToolName: "edit", Status: "blocked", ErrorKind: "blocked"},
			{ToolName: "wait", Status: "running"},
		},
	}}
	output := NewRendererRegistry().Render(result)
	assert.Contains(t, output, "1 succeeded · 1 failed · 1 running")
	assert.Contains(t, output, "Selected text")
	assert.Contains(t, output, `{"id":1}`)
	assert.Contains(t, output, "blocked  edit")
	assert.Contains(t, output, "Stopped")
	assert.Equal(t, "Code execution: Bad metadata", (&CodeExecutionRenderer{}).RenderCLI(tools.StructuredToolResult{Error: "Bad metadata"}))
}

func TestCodeExecutionRendererTypedOutputOrder(t *testing.T) {
	result := tools.StructuredToolResult{ToolName: "code_execute", Success: true, Metadata: tools.CodeExecutionMetadata{
		Status:  "completed",
		Outputs: []json.RawMessage{json.RawMessage(`"Superseded legacy output"`)},
		Items: []tools.CodeExecutionOutput{
			{Type: "json", Value: json.RawMessage(`"Before"`)},
			{Type: "image", ArtifactID: "art_viewed", Detail: "original"},
			{Type: "json", Value: json.RawMessage(`{"type":"image","artifactId":"not-media"}`)},
			{Type: "artifact", ArtifactID: "art_retained"},
			{Type: "json", Value: json.RawMessage(`"After"`)},
		},
	}}
	output := (&CodeExecutionRenderer{}).RenderCLI(result)
	assert.Equal(t, []string{
		"Before",
		"Image sent to model: art_viewed (original detail)",
		`{"type":"image","artifactId":"not-media"}`,
		"Retained artifact (not sent to model): art_retained",
		"After",
	}, strings.Split(output, "\n\n")[1:])
}
