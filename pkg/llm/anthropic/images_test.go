package anthropic

import (
	"bytes"
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
	large := encodedPNG(t, 3200, 8)
	for _, tt := range []struct {
		name                 string
		count                int
		directSize, toolSize image.Point
	}{
		{"20 images allow 8000 px", 20, image.Pt(1, 8000), image.Pt(3200, 8)},
		{"21 images cap dimensions at 3000 px", 21, image.Pt(1, 3000), image.Pt(3000, 7)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			toolImages := []anthropic.ToolResultBlockParamContentUnion{
				imageToolResultContent(large),
				imageToolResultContent(small),
			}
			for range tt.count - 3 {
				toolImages = append(toolImages, anthropic.ToolResultBlockParamContentUnion{
					OfImage: anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: "https://example.com/a.png"}).OfImage,
				})
			}
			messages := []anthropic.MessageParam{
				anthropic.NewUserMessage(
					anthropic.NewImageBlockBase64("image/png", encodedPNG(t, 2, 8001)),
					anthropic.NewTextBlock("what is this?"),
				),
				anthropic.NewAssistantMessage(anthropic.NewToolUseBlock("toolu_1", map[string]any{}, "view_image")),
				anthropic.NewUserMessage(anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
					ToolUseID: "toolu_1",
					Content:   toolImages,
				}}),
			}

			fitRequestImages(t.Context(), messages)

			assert.Equal(t, tt.directSize, encodedImageSize(t, messages[0].Content[0].OfImage.Source.OfBase64.Data))
			assert.Equal(t, tt.toolSize, encodedImageSize(t, toolImages[0].OfImage.Source.OfBase64.Data))
			if tt.count == 20 {
				assert.Equal(t, large, toolImages[0].OfImage.Source.OfBase64.Data)
			}
			assert.Equal(t, small, toolImages[1].OfImage.Source.OfBase64.Data)
			assert.Equal(t, "https://example.com/a.png", toolImages[2].OfImage.Source.OfURL.URL)
		})
	}

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
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&capturedRequest)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

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
	small := encodedPNG(t, 2, 2)
	for range 20 {
		screenshots = append(screenshots, anthropic.NewImageBlockBase64("image/png", small))
	}
	screenshots = append(screenshots, anthropic.NewTextBlock("and these?"))
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "conv-test"),
		client: anthropic.NewClient(
			option.WithoutEnvironmentDefaults(),
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
	thread.SetState(tools.NewBasicState(t.Context()))

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, err := thread.processMessageExchange(
		t.Context(), handler, "claude-sonnet-4-6", 256, "system",
		llmtypes.MessageOpt{DisableUsageLog: true},
	)
	require.NoError(t, err)

	require.NotEmpty(t, capturedRequest.Messages)
	require.NotEmpty(t, capturedRequest.Messages[0].Content)
	require.Equal(t, "image", capturedRequest.Messages[0].Content[0].Type)
	assert.Equal(t, image.Pt(3000, 4), encodedImageSize(t, capturedRequest.Messages[0].Content[0].Source.Data))
	assert.Equal(
		t,
		capturedRequest.Messages[0].Content[0].Source.Data,
		thread.messages[0].Content[0].OfImage.Source.OfBase64.Data,
		"the saved conversation keeps the fitted image",
	)
}

func imageToolResultContent(data string) anthropic.ToolResultBlockParamContentUnion {
	return anthropic.ToolResultBlockParamContentUnion{OfImage: anthropic.NewImageBlockBase64("image/png", data).OfImage}
}

func encodedPNG(t *testing.T, width, height int) string {
	t.Helper()

	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))))
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func encodedImageSize(t *testing.T, data string) image.Point {
	t.Helper()

	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	require.NoError(t, err)
	return image.Pt(config.Width, config.Height)
}
