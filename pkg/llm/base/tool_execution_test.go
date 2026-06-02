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
