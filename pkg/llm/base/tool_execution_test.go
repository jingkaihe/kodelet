package base

import (
	"context"
	"testing"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type multimodalTool struct{}

func (t multimodalTool) GenerateSchema() *jsonschema.Schema { return &jsonschema.Schema{} }
func (t multimodalTool) Name() string                       { return "view_image" }
func (t multimodalTool) Description() string                { return "test multimodal tool" }
func (t multimodalTool) ValidateInput(tooltypes.State, string) error {
	return nil
}

func (t multimodalTool) Execute(context.Context, tooltypes.State, string) tooltypes.ToolResult {
	return multimodalToolResult{BaseToolResult: tooltypes.BaseToolResult{Result: "image available"}}
}
func (t multimodalTool) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

type multimodalToolResult struct {
	tooltypes.BaseToolResult
}

type lateUpdateTool struct {
	callback tooltypes.ToolUpdateCallback
}

func (t *lateUpdateTool) GenerateSchema() *jsonschema.Schema { return &jsonschema.Schema{} }
func (t *lateUpdateTool) Name() string                       { return "late_update" }
func (t *lateUpdateTool) Description() string                { return "test streaming updates" }
func (t *lateUpdateTool) ValidateInput(tooltypes.State, string) error {
	return nil
}

func (t *lateUpdateTool) Execute(context.Context, tooltypes.State, string) tooltypes.ToolResult {
	return tooltypes.BaseToolResult{Result: "complete"}
}

func (t *lateUpdateTool) ExecuteStreaming(
	_ context.Context,
	_ tooltypes.State,
	_ string,
	onUpdate tooltypes.ToolUpdateCallback,
) tooltypes.ToolResult {
	t.callback = onUpdate
	onUpdate(tooltypes.BaseToolResult{Result: "running"})
	return tooltypes.BaseToolResult{Result: "complete"}
}
func (t *lateUpdateTool) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

type toolUpdateHandler struct {
	updates []string
}

func (h *toolUpdateHandler) HandleText(string)                                     {}
func (h *toolUpdateHandler) HandleToolUse(string, string, string)                  {}
func (h *toolUpdateHandler) HandleToolResult(string, string, tooltypes.ToolResult) {}
func (h *toolUpdateHandler) HandleThinking(string)                                 {}
func (h *toolUpdateHandler) HandleDone()                                           {}
func (h *toolUpdateHandler) HandleToolUpdate(_, _ string, result tooltypes.ToolResult) {
	h.updates = append(h.updates, result.GetResult())
}

func (r multimodalToolResult) StructuredData() tooltypes.StructuredToolResult {
	return tooltypes.StructuredToolResult{
		ToolName:  "view_image",
		Success:   true,
		Timestamp: time.Now(),
	}
}

func (r multimodalToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	return []tooltypes.ToolResultContentPart{{
		Type:     tooltypes.ToolResultContentPartTypeImage,
		ImageURL: "data:image/png;base64,ZmFrZQ==",
		MimeType: "image/png",
	}}
}

func TestNewThreadInitializesRendererRegistry(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-id")
	require.NotNil(t, bt.RendererRegistry)
}

func TestExecuteToolPanicsWithNilRendererRegistry(t *testing.T) {
	assert.PanicsWithValue(t, "rendererRegistry must not be nil", func() {
		ExecuteTool(
			context.Background(),
			nil,
			&mockState{},
			nil,
			"unknown_tool",
			"{}",
			"call-id",
		)
	})
}

func TestExecuteToolWithInjectedRendererRegistry(t *testing.T) {
	execution := ExecuteTool(
		context.Background(),
		nil,
		&mockState{},
		renderers.NewRendererRegistry(),
		"unknown_tool",
		"{}",
		"call-id",
	)

	assert.NotNil(t, execution.Result)
	assert.NotEmpty(t, execution.RenderedOutput)
}

func TestExecuteToolPreservesMultimodalResultWhenExtensionRuntimeIsIdle(t *testing.T) {
	state := &toolState{tools: []tooltypes.Tool{multimodalTool{}}}
	thread := &threadStub{
		config:         llmtypes.Config{Extensions: extensions.EmptyRuntime()},
		conversationID: "conv-id",
		state:          state,
	}

	execution := ExecuteTool(
		context.Background(),
		thread,
		state,
		renderers.NewRendererRegistry(),
		"view_image",
		"{}",
		"call-id",
	)

	multimodalResult, ok := execution.Result.(tooltypes.MultiModalToolResult)
	require.True(t, ok)
	assert.Equal(t, []tooltypes.ToolResultContentPart{{
		Type:     tooltypes.ToolResultContentPartTypeImage,
		ImageURL: "data:image/png;base64,ZmFrZQ==",
		MimeType: "image/png",
	}}, multimodalResult.ContentParts())
}

func TestExecuteToolWithHandlerForwardsUpdatesAndRejectsLateCallbacks(t *testing.T) {
	tool := &lateUpdateTool{}
	state := &toolState{tools: []tooltypes.Tool{tool}}
	handler := &toolUpdateHandler{}

	execution := ExecuteToolWithHandler(
		context.Background(),
		nil,
		state,
		renderers.NewRendererRegistry(),
		tool.Name(),
		`{}`,
		"call-id",
		handler,
	)

	assert.Equal(t, "complete", execution.Result.GetResult())
	assert.Equal(t, []string{"running"}, handler.updates)
	require.NotNil(t, tool.callback)

	tool.callback(tooltypes.BaseToolResult{Result: "too late"})
	assert.Equal(t, []string{"running"}, handler.updates)
}
