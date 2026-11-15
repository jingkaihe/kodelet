package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestFitRequestImages(t *testing.T) {
	small := encodedPNG(t, 2, 2)

	t.Run("caps images at 3000 px when the request has more than 20 images", func(t *testing.T) {
		toolResultImages := []anthropic.ToolResultBlockParamContentUnion{
			imageToolResultContent(encodedPNG(t, 3200, 8)),
		}
		for range 19 {
			toolResultImages = append(toolResultImages, imageToolResultContent(small))
		}
		messages := []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewImageBlockBase64("image/png", encodedPNG(t, 8, 3100)),
				anthropic.NewTextBlock("what is this?"),
			),
			anthropic.NewAssistantMessage(anthropic.NewToolUseBlock("toolu_1", map[string]any{}, "view_image")),
			anthropic.NewUserMessage(anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
				ToolUseID: "toolu_1",
				Content:   toolResultImages,
			}}),
		}

		fitRequestImages(t.Context(), messages)

		width, height := encodedImageSize(t, messages[0].Content[0].OfImage.Source.OfBase64.Data)
		assert.Equal(t, 7, width)
		assert.Equal(t, 3000, height)
		resultImages := messages[2].Content[0].OfToolResult.Content
		width, height = encodedImageSize(t, resultImages[0].OfImage.Source.OfBase64.Data)
		assert.Equal(t, 3000, width)
		assert.Equal(t, 7, height)
		for _, content := range resultImages[1:] {
			assert.Equal(t, small, content.OfImage.Source.OfBase64.Data)
		}
	})

	t.Run("keeps images up to 8000 px when the request has at most 20 images", func(t *testing.T) {
		large := encodedPNG(t, 3200, 8)
		blocks := []anthropic.ContentBlockParamUnion{anthropic.NewImageBlockBase64("image/png", large)}
		for range 19 {
			blocks = append(blocks, anthropic.NewImageBlockBase64("image/png", small))
		}
		messages := []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)}

		fitRequestImages(t.Context(), messages)

		assert.Equal(t, large, messages[0].Content[0].OfImage.Source.OfBase64.Data)
	})

	t.Run("caps any image at 8000 px", func(t *testing.T) {
		messages := []anthropic.MessageParam{anthropic.NewUserMessage(
			anthropic.NewImageBlockBase64("image/png", encodedPNG(t, 8001, 2)),
		)}

		fitRequestImages(t.Context(), messages)

		width, height := encodedImageSize(t, messages[0].Content[0].OfImage.Source.OfBase64.Data)
		assert.Equal(t, 8000, width)
		assert.Equal(t, 1, height)
	})

	t.Run("counts URL images without changing them", func(t *testing.T) {
		blocks := []anthropic.ContentBlockParamUnion{anthropic.NewImageBlockBase64("image/png", encodedPNG(t, 3001, 2))}
		for range 20 {
			blocks = append(blocks, anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: "https://example.com/a.png"}))
		}
		messages := []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)}

		fitRequestImages(t.Context(), messages)

		width, _ := encodedImageSize(t, messages[0].Content[0].OfImage.Source.OfBase64.Data)
		assert.Equal(t, 3000, width)
		assert.Equal(t, "https://example.com/a.png", messages[0].Content[1].OfImage.Source.OfURL.URL)
	})

	t.Run("leaves undecodable images unchanged", func(t *testing.T) {
		invalid := base64.StdEncoding.EncodeToString([]byte("not an image"))
		messages := []anthropic.MessageParam{anthropic.NewUserMessage(
			anthropic.NewImageBlockBase64("image/png", invalid),
		)}

		fitRequestImages(t.Context(), messages)

		assert.Equal(t, invalid, messages[0].Content[0].OfImage.Source.OfBase64.Data)
	})
}

func TestProcessMessageExchangeFitsResentImagesForManyImageRequests(t *testing.T) {
	var capturedRequest struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Source struct {
					Data string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &capturedRequest))

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, compactionTestStream(
			`{"type":"text","text":"done"}`,
			"end_turn",
			`{"input_tokens":1,"output_tokens":1}`,
		))
	}))
	defer server.Close()

	// The first image was accepted when it was sent alone; later screenshots push
	// the resent history past the many-image threshold.
	screenshots := make([]anthropic.ContentBlockParamUnion, 0, 21)
	for range 20 {
		screenshots = append(screenshots, anthropic.NewImageBlockBase64("image/png", encodedPNG(t, 2, 2)))
	}
	screenshots = append(screenshots, anthropic.NewTextBlock("and these?"))
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "conv-test"),
		client: anthropic.NewClient(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test-key"),
			option.WithMaxRetries(0),
		),
		messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewImageBlockBase64("image/png", encodedPNG(t, 4032, 6)),
				anthropic.NewTextBlock("what is this?"),
			),
			anthropic.NewAssistantMessage(anthropic.NewTextBlock("a photo")),
			anthropic.NewUserMessage(screenshots...),
		},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, err := thread.processMessageExchange(
		context.Background(), handler, "claude-sonnet-4-6", 256, "system",
		llmtypes.MessageOpt{DisableUsageLog: true},
	)
	require.NoError(t, err)

	require.NotEmpty(t, capturedRequest.Messages)
	require.NotEmpty(t, capturedRequest.Messages[0].Content)
	require.Equal(t, "image", capturedRequest.Messages[0].Content[0].Type)
	width, height := encodedImageSize(t, capturedRequest.Messages[0].Content[0].Source.Data)
	assert.Equal(t, 3000, width)
	assert.Equal(t, 4, height)
	assert.Equal(
		t,
		capturedRequest.Messages[0].Content[0].Source.Data,
		thread.messages[0].Content[0].OfImage.Source.OfBase64.Data,
		"the saved conversation keeps the fitted image",
	)
}

func imageToolResultContent(data string) anthropic.ToolResultBlockParamContentUnion {
	return anthropic.ToolResultBlockParamContentUnion{OfImage: &anthropic.ImageBlockParam{
		Source: anthropic.ImageBlockParamSourceUnion{
			OfBase64: &anthropic.Base64ImageSourceParam{Data: data, MediaType: "image/png"},
		},
	}}
}

func encodedPNG(t *testing.T, width, height int) string {
	t.Helper()

	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))))
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func encodedImageSize(t *testing.T, data string) (int, int) {
	t.Helper()

	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	require.NoError(t, err)
	return config.Width, config.Height
}
