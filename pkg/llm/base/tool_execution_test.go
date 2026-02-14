package base

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestNewThreadInitializesRendererRegistry(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-id", hooks.Trigger{})
	require.NotNil(t, bt.RendererRegistry)
}

func TestExecuteToolPanicsWithNilRendererRegistry(t *testing.T) {
	assert.PanicsWithValue(t, "rendererRegistry must not be nil", func() {
		ExecuteTool(
			context.Background(),
			hooks.Trigger{},
			nil,
			&mockState{},
			nil,
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
		hooks.Trigger{},
		nil,
		&mockState{},
		nil,
		renderers.NewRendererRegistry(),
		"unknown_tool",
		"{}",
		"call-id",
	)

	assert.NotNil(t, execution.Result)
	assert.NotEmpty(t, execution.RenderedOutput)
}
