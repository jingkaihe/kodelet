package tools

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewImageTool_Name(t *testing.T) {
	tool := &ViewImageTool{}
	assert.Equal(t, "view_image", tool.Name())
}

func TestViewImageTool_GenerateSchema(t *testing.T) {
	tool := NewViewImageTool("gpt-5", "openai")
	schema := tool.GenerateSchema()
	require.NotNil(t, schema)
	require.NotNil(t, schema.Properties)
	_, hasPath := schema.Properties.Get("path")
	assert.True(t, hasPath)
	_, hasDetail := schema.Properties.Get("detail")
	assert.False(t, hasDetail)

	tool = NewViewImageTool("gpt-5.3-codex", "openai")
	schema = tool.GenerateSchema()
	_, hasDetail = schema.Properties.Get("detail")
	assert.True(t, hasDetail)

	tool = NewViewImageTool("gpt-5.5", "openai")
	schema = tool.GenerateSchema()
	_, hasDetail = schema.Properties.Get("detail")
	assert.True(t, hasDetail)
}

func TestViewImageTool_ValidateInput(t *testing.T) {
	tool := NewViewImageTool("gpt-5.3-codex", "openai")
	state := NewBasicState(t.Context(), WithLLMConfig(llmtypes.Config{Model: "gpt-5.3-codex"}))

	assert.NoError(t, tool.ValidateInput(state, `{"path":"/tmp/test.png"}`))
	assert.NoError(t, tool.ValidateInput(state, `{"path":"/tmp/test.png","detail":"original"}`))

	err := tool.ValidateInput(state, `{"detail":"original"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")

	err = tool.ValidateInput(NewBasicState(t.Context(), WithLLMConfig(llmtypes.Config{Model: "gpt-5"})), `{"path":"/tmp/test.png","detail":"original"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "compatible models")

	err = tool.ValidateInput(NewBasicState(t.Context(), WithLLMConfig(llmtypes.Config{Model: "gpt-5.5"})), `{"path":"/tmp/test.png","detail":"original"}`)
	assert.NoError(t, err)
}

func TestViewImageTool_ExecuteAndStructuredData(t *testing.T) {
	ctx := t.Context()
	state := NewBasicState(ctx, WithLLMConfig(llmtypes.Config{Model: "gpt-5", Provider: "openai"}), WithWorkingDirectory(t.TempDir()))
	imagePath := filepath.Join(state.WorkingDirectory(), "sample.png")
	writeTestPNG(t, imagePath, 16, 12)

	tool := NewViewImageTool("gpt-5", "openai")
	result := tool.Execute(ctx, state, `{"path":"sample.png"}`)
	require.False(t, result.IsError())

	structured := result.StructuredData()
	assert.Equal(t, "view_image", structured.ToolName)
	assert.True(t, structured.Success)

	var meta tooltypes.ViewImageMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &meta))
	assert.Equal(t, imagePath, meta.Path)
	assert.Equal(t, 16, meta.ImageSize.Width)
	assert.Equal(t, 12, meta.ImageSize.Height)

	rich, ok := result.(interface {
		ContentParts() []tooltypes.ToolResultContentPart
	})
	require.True(t, ok)
	parts := rich.ContentParts()
	require.Len(t, parts, 1)
	assert.Equal(t, tooltypes.ToolResultContentPartTypeImage, parts[0].Type)
	assert.Contains(t, parts[0].ImageURL, "data:image/png;base64,")
}

func TestViewImageTool_TracingKVs(t *testing.T) {
	tool := NewViewImageTool("gpt-5.3-codex", "openai")
	kvs, err := tool.TracingKVs(`{"path":"/tmp/demo.png","detail":"original"}`)
	require.NoError(t, err)
	require.Len(t, kvs, 2)
	assert.Equal(t, "path", string(kvs[0].Key))
	assert.Equal(t, "/tmp/demo.png", kvs[0].Value.AsString())
	assert.Equal(t, "detail", string(kvs[1].Key))
	assert.Equal(t, "original", kvs[1].Value.AsString())
}

func writeTestPNG(t *testing.T, path string, width int, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 20, B: 30, A: 255})
		}
	}
	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()
	require.NoError(t, png.Encode(file, img))
}
