package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestGetMediaTypeFromExtension(t *testing.T) {
	tests := []struct {
		ext      string
		expected anthropic.Base64ImageSourceMediaType
		hasError bool
	}{
		{".jpg", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".jpeg", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".JPG", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".JPEG", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".png", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{".PNG", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{".gif", anthropic.Base64ImageSourceMediaTypeImageGIF, false},
		{".GIF", anthropic.Base64ImageSourceMediaTypeImageGIF, false},
		{".webp", anthropic.Base64ImageSourceMediaTypeImageWebP, false},
		{".WEBP", anthropic.Base64ImageSourceMediaTypeImageWebP, false},
		{".bmp", "", true},
		{".svg", "", true},
		{".txt", "", true},
		{"", "", true},
	}

	for _, test := range tests {
		t.Run(test.ext, func(t *testing.T) {
			result, err := getMediaTypeFromExtension(test.ext)
			if test.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestGetConfiguredBaseURL(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")

	tests := []struct {
		name     string
		config   llmtypes.Config
		envBase  string
		expected string
	}{
		{
			name:     "no explicit base url",
			config:   llmtypes.Config{},
			expected: "",
		},
		{
			name: "config base url override",
			config: llmtypes.Config{Anthropic: &llmtypes.AnthropicConfig{
				BaseURL: "https://custom.example",
			}},
			expected: "https://custom.example",
		},
		{
			name: "env base url override",
			config: llmtypes.Config{Anthropic: &llmtypes.AnthropicConfig{
				BaseURL: "https://custom.example",
			}},
			envBase:  "https://env.example",
			expected: "https://env.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_BASE_URL", tt.envBase)
			assert.Equal(t, tt.expected, GetConfiguredBaseURL(tt.config))
		})
	}
}

func TestResolveClientBaseURL(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")

	tests := []struct {
		name       string
		config     llmtypes.Config
		useCopilot bool
		envBase    string
		expected   string
	}{
		{
			name:       "non-copilot uses SDK default base",
			config:     llmtypes.Config{},
			useCopilot: false,
			expected:   "",
		},
		{
			name:       "copilot uses copilot endpoint by default",
			config:     llmtypes.Config{},
			useCopilot: true,
			expected:   "https://api.githubcopilot.com",
		},
		{
			name: "copilot respects explicit config base override",
			config: llmtypes.Config{Anthropic: &llmtypes.AnthropicConfig{
				Platform: "copilot",
				BaseURL:  "https://custom.example",
			}},
			useCopilot: true,
			expected:   "https://custom.example",
		},
		{
			name: "copilot respects env base override",
			config: llmtypes.Config{Anthropic: &llmtypes.AnthropicConfig{
				Platform: "copilot",
				BaseURL:  "https://custom.example",
			}},
			useCopilot: true,
			envBase:    "https://env.example",
			expected:   "https://env.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_BASE_URL", tt.envBase)
			assert.Equal(t, tt.expected, resolveClientBaseURL(tt.config, tt.useCopilot))
		})
	}
}

func TestAnthropicThreadDeterministicHelpers(t *testing.T) {
	thread := &Thread{}
	assert.Equal(t, "anthropic", thread.Provider())

	assert.False(t, isMessageToolUse(anthropic.NewUserMessage()))
	assert.False(t, isMessageToolUse(anthropic.NewUserMessage(anthropic.NewTextBlock("hello"))))
	assert.True(t, isMessageToolUse(anthropic.NewAssistantMessage(anthropic.NewToolUseBlock("toolu_1", map[string]any{"command": "pwd"}, "bash"))))

	thread.messages = []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("first"), anthropic.NewToolResultBlock("toolu_old", "old", false)),
		anthropic.NewUserMessage(anthropic.NewTextBlock("last")),
	}
	thread.messages[0].Content[0].OfText.CacheControl = cacheControlEphemeralDefault()
	thread.messages[0].Content[1].OfToolResult.CacheControl = cacheControlEphemeralDefault()

	thread.cacheMessages()

	assert.Empty(t, thread.messages[0].Content[0].OfText.CacheControl.Type)
	assert.Empty(t, thread.messages[0].Content[1].OfToolResult.CacheControl.Type)
	assert.Equal(t, "ephemeral", string(thread.messages[1].Content[0].OfText.CacheControl.Type))
	assert.Empty(t, thread.messages[1].Content[0].OfText.CacheControl.TTL)
}

func TestAnthropicPromptCachePolicyMatchesOpencodeAutoPlacement(t *testing.T) {
	params := anthropic.MessageNewParams{
		Tools: toAnthropicTools([]tooltypes.Tool{
			testTool{name: "file_read"},
			testTool{name: "bash"},
		}, false),
		System: []anthropic.TextBlockParam{
			{Text: "first system"},
			{Text: "last system"},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("first user")),
			anthropic.NewAssistantMessage(anthropic.NewTextBlock("assistant")),
			anthropic.NewUserMessage(anthropic.NewToolResultBlock("toolu_1", "tool result", false)),
		},
	}
	params.Tools[0].OfTool.CacheControl = cacheControlEphemeralDefault()
	params.System[0].CacheControl = cacheControlEphemeralDefault()
	params.Messages[1].Content[0].OfText.CacheControl = cacheControlEphemeralDefault()

	applyAnthropicPromptCachePolicy(&params)

	assert.Empty(t, params.Tools[0].OfTool.CacheControl.Type)
	assert.Equal(t, "ephemeral", string(params.Tools[1].OfTool.CacheControl.Type))
	assert.Empty(t, params.Tools[1].OfTool.CacheControl.TTL)

	assert.Empty(t, params.System[0].CacheControl.Type)
	assert.Equal(t, "ephemeral", string(params.System[1].CacheControl.Type))
	assert.Empty(t, params.System[1].CacheControl.TTL)

	assert.Empty(t, params.Messages[0].Content[0].OfText.CacheControl.Type)
	assert.Empty(t, params.Messages[1].Content[0].OfText.CacheControl.Type)
	assert.Equal(t, "ephemeral", string(params.Messages[2].Content[0].OfToolResult.CacheControl.Type))
	assert.Empty(t, params.Messages[2].Content[0].OfToolResult.CacheControl.TTL)
}

func TestAnthropicPromptCachePolicyFallsBackToLatestToolResultMessage(t *testing.T) {
	params := anthropic.MessageNewParams{
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewToolResultBlock("toolu_1", "first", false)),
			anthropic.NewUserMessage(anthropic.NewToolResultBlock("toolu_2", "last", false)),
		},
	}

	applyAnthropicPromptCachePolicy(&params)

	assert.Empty(t, params.Messages[0].Content[0].OfToolResult.CacheControl.Type)
	assert.Equal(t, "ephemeral", string(params.Messages[1].Content[0].OfToolResult.CacheControl.Type))
	assert.Empty(t, params.Messages[1].Content[0].OfToolResult.CacheControl.TTL)
}

func TestAnthropicPromptCachePolicyCachesExplicitGoalContinuationMessage(t *testing.T) {
	params := anthropic.MessageNewParams{
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("user request")),
			anthropic.NewUserMessage(anthropic.NewTextBlock(goals.RenderContext(goals.New("finish task", time.Now())))),
		},
	}

	applyAnthropicPromptCachePolicy(&params)

	assert.Empty(t, params.Messages[0].Content[0].OfText.CacheControl.Type)
	assert.Equal(t, "ephemeral", string(params.Messages[1].Content[0].OfText.CacheControl.Type))
}

func TestAnthropicPromptCachePolicySkipsEmptyTextBlocks(t *testing.T) {
	params := anthropic.MessageNewParams{
		System: []anthropic.TextBlockParam{
			{Text: "cacheable system"},
			{Text: "   "},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock("cacheable user"),
				anthropic.NewTextBlock("   "),
			),
		},
	}

	applyAnthropicPromptCachePolicy(&params)

	assert.Equal(t, "ephemeral", string(params.System[0].CacheControl.Type))
	assert.Empty(t, params.System[1].CacheControl.Type)
	assert.Equal(t, "ephemeral", string(params.Messages[0].Content[0].OfText.CacheControl.Type))
	assert.Empty(t, params.Messages[0].Content[1].OfText.CacheControl.Type)
}

func TestAnthropicProcessMessageExchangeDoesNotInjectGoalContextFromMetadata(t *testing.T) {
	var capturedRequest struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, r.Body.Close())
		require.NoError(t, json.Unmarshal(data, &capturedRequest))

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n\n" +
			"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}` + "\n\n" +
			"event: content_block_stop\n" +
			`data: {"type":"content_block_stop","index":0}` + "\n\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":1,"output_tokens":1}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n"))
	}))
	defer server.Close()

	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "conv-test", hooks.Trigger{}),
		client: anthropic.NewClient(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test-key"),
		),
		messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hello"))},
	}
	thread.SetMetadataValue(goals.MetadataKey, goals.New("find server cores and ram", time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)))

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, err := thread.processMessageExchange(context.Background(), handler, "claude-sonnet-4-6", 256, "system", llmtypes.MessageOpt{NoToolUse: true, DisableUsageLog: true})
	require.NoError(t, err)

	require.Len(t, capturedRequest.Messages, 1)
	require.Len(t, capturedRequest.Messages[0].Content, 1)
	assert.Equal(t, "hello", capturedRequest.Messages[0].Content[0].Text)
	assert.NotContains(t, capturedRequest.Messages[0].Content[0].Text, "<goal_context>")
}

func TestAnthropicToolResultBlockUsesMultimodalPartsWhenAvailable(t *testing.T) {
	result := fakeAnthropicMultiModalToolResult{
		BaseToolResult: tooltypes.BaseToolResult{Result: "fallback"},
		parts: []tooltypes.ToolResultContentPart{
			{Type: tooltypes.ToolResultContentPartTypeText, Text: "  "},
			{Type: tooltypes.ToolResultContentPartTypeText, Text: "caption"},
			{Type: tooltypes.ToolResultContentPartTypeImage, ImageURL: "data:image/bmp;base64,ignored"},
			{Type: tooltypes.ToolResultContentPartTypeImage, ImageURL: "data:image/png;base64,aGVsbG8="},
		},
	}

	block := anthropicToolResultBlock("toolu_1", result)

	require.NotNil(t, block.OfToolResult)
	assert.Equal(t, "toolu_1", block.OfToolResult.ToolUseID)
	assert.False(t, block.OfToolResult.IsError.Value)
	require.Len(t, block.OfToolResult.Content, 2)
	assert.Equal(t, "caption", block.OfToolResult.Content[0].OfText.Text)
	require.NotNil(t, block.OfToolResult.Content[1].OfImage)
	assert.Equal(t, "aGVsbG8=", block.OfToolResult.Content[1].OfImage.Source.OfBase64.Data)
	assert.Equal(t, anthropic.Base64ImageSourceMediaTypeImagePNG, block.OfToolResult.Content[1].OfImage.Source.OfBase64.MediaType)
}

func TestAnthropicToolResultBlockFallsBackToAssistantFacing(t *testing.T) {
	block := anthropicToolResultBlock("toolu_err", tooltypes.BaseToolResult{Error: "boom"})

	require.NotNil(t, block.OfToolResult)
	assert.True(t, block.OfToolResult.IsError.Value)
	require.Len(t, block.OfToolResult.Content, 1)
	assert.Contains(t, block.OfToolResult.Content[0].OfText.Text, "boom")
}

func TestGetModelPricingMatchesFamiliesAndDefault(t *testing.T) {
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeSonnet4_6], getModelPricing(anthropic.ModelClaudeSonnet4_6))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeSonnet4_5], getModelPricing("claude-sonnet-4-5-latest"))
	assert.Equal(t, ModelPricingMap[modelClaudeSonnet4_0], getModelPricing("claude-sonnet-4-20250514"))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeOpus4_8], getModelPricing("claude-opus-4-8-latest"))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeOpus4_7], getModelPricing("claude-opus-4-7-latest"))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeOpus4_6], getModelPricing("claude-opus-4-6-custom"))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeOpus4_5_20251101], getModelPricing("claude-opus-4-5-custom"))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeOpus4_1_20250805], getModelPricing("claude-opus-4-1-custom"))
	assert.Equal(t, ModelPricingMap[modelClaudeOpus4_0], getModelPricing("claude-opus-4-20250514"))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeHaiku4_5], getModelPricing("claude-haiku-4-5-custom"))
	assert.Equal(t, ModelPricingMap[modelClaude35Haiku], getModelPricing("claude-3-5-haiku-20241022"))
	assert.Equal(t, ModelPricingMap[anthropic.ModelClaudeSonnet4_6], getModelPricing("unknown-model"))
}

func TestOpus47PricingMatchesScreenshot(t *testing.T) {
	pricing := ModelPricingMap[anthropic.ModelClaudeOpus4_7]

	assert.Equal(t, 0.000005, pricing.Input)
	assert.Equal(t, 0.00000625, pricing.PromptCachingWrite5m)
	assert.Equal(t, 0.00001, pricing.PromptCachingWrite1h)
	assert.Equal(t, 0.0000005, pricing.PromptCachingRead)
	assert.Equal(t, 0.000025, pricing.Output)
}

func TestCacheCreationCostUsesTTLBreakdown(t *testing.T) {
	pricing := ModelPricingMap[anthropic.ModelClaudeOpus4_7]
	usage := anthropic.Usage{
		CacheCreationInputTokens: 300,
		CacheCreation: anthropic.CacheCreation{
			Ephemeral5mInputTokens: 100,
			Ephemeral1hInputTokens: 200,
		},
	}

	assert.Equal(t, (100*pricing.PromptCachingWrite5m)+(200*pricing.PromptCachingWrite1h), cacheCreationCost(usage, pricing))
}

func TestCacheCreationCostFallsBackToLegacyAggregateTokens(t *testing.T) {
	pricing := ModelPricingMap[anthropic.ModelClaudeOpus4_7]
	usage := anthropic.Usage{CacheCreationInputTokens: 300}

	assert.Equal(t, 300*pricing.PromptCachingWrite5m, cacheCreationCost(usage, pricing))
}

func TestNewAnthropicThreadCopilotUsesConfiguredBaseURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "")

	_, err := auth.SaveCopilotCredentials(&auth.CopilotCredentials{
		AccessToken:    "github-access-token",
		CopilotToken:   "copilot-access-token",
		Scope:          "copilot",
		CopilotExpires: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	var requestPath string
	var authorization string
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		authorization = r.Header.Get("Authorization")
		userAgent = r.Header.Get("User-Agent")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	thread, err := NewAnthropicThread(llmtypes.Config{
		Anthropic: &llmtypes.AnthropicConfig{
			Platform: "copilot",
			BaseURL:  server.URL,
		},
	})
	require.NoError(t, err)

	var response map[string]bool
	err = thread.client.Post(context.Background(), "/v1/messages", map[string]string{"ping": "pong"}, &response)
	require.NoError(t, err)

	assert.Equal(t, "/v1/messages", requestPath)
	assert.Equal(t, "Bearer copilot-access-token", authorization)
	assert.Equal(t, "GitHubCopilotChat/0.26.7", userAgent)
	assert.True(t, response["ok"])
}

func TestProcessImageURL(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)
	assert.Equal(t, anthropic.ModelClaudeOpus4_7, thread.Config.Model)

	tests := []struct {
		name     string
		url      string
		hasError bool
	}{
		{"Valid HTTPS URL", "https://example.com/image.jpg", false},
		{"HTTP URL (should fail)", "http://example.com/image.jpg", true},
		{"Invalid URL format", "not-a-url", true},
		{"FTP URL (should fail)", "ftp://example.com/image.jpg", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImageURL(test.url)
			if test.hasError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestProcessImageDataURL(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

	// A minimal valid 1x1 PNG image encoded in base64
	validPNGBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

	tests := []struct {
		name     string
		dataURL  string
		hasError bool
	}{
		{"Valid PNG data URL", "data:image/png;base64," + validPNGBase64, false},
		{"Valid JPEG data URL", "data:image/jpeg;base64," + validPNGBase64, false},
		{"Valid GIF data URL", "data:image/gif;base64," + validPNGBase64, false},
		{"Valid WebP data URL", "data:image/webp;base64," + validPNGBase64, false},
		{"Missing data: prefix", "image/png;base64," + validPNGBase64, true},
		{"Missing base64 separator", "data:image/png," + validPNGBase64, true},
		{"Unsupported mime type", "data:image/bmp;base64," + validPNGBase64, true},
		{"Unsupported mime type svg", "data:image/svg+xml;base64," + validPNGBase64, true},
		{"Empty data URL", "", true},
		// Note: Invalid base64 is not validated client-side by Anthropic SDK, validation happens server-side
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImageDataURL(test.dataURL)
			if test.hasError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestMimeTypeToAnthropicMediaType(t *testing.T) {
	tests := []struct {
		mimeType string
		expected anthropic.Base64ImageSourceMediaType
		hasError bool
	}{
		{"image/jpeg", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{"image/png", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{"image/gif", anthropic.Base64ImageSourceMediaTypeImageGIF, false},
		{"image/webp", anthropic.Base64ImageSourceMediaTypeImageWebP, false},
		{"IMAGE/JPEG", anthropic.Base64ImageSourceMediaTypeImageJPEG, false}, // Case insensitive
		{"IMAGE/PNG", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{"image/bmp", "", true},
		{"image/svg+xml", "", true},
		{"text/plain", "", true},
		{"", "", true},
	}

	for _, test := range tests {
		t.Run(test.mimeType, func(t *testing.T) {
			result, err := mimeTypeToAnthropicMediaType(test.mimeType)
			if test.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestProcessImage_DataURLRouting(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

	validPNGBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

	tests := []struct {
		name      string
		imagePath string
		hasError  bool
	}{
		{"Data URL is routed correctly", "data:image/png;base64," + validPNGBase64, false},
		{"HTTPS URL is routed correctly", "https://example.com/image.jpg", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImage(test.imagePath)
			if test.hasError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestProcessImageFile(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a small test image file (PNG)
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk header
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 pixel
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, // bit depth, color type, etc.
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, // IEND chunk
		0x42, 0x60, 0x82,
	}

	testImagePath := filepath.Join(tempDir, "test.png")
	err = os.WriteFile(testImagePath, pngData, 0o644)
	require.NoError(t, err)

	// Create a large test file (exceeds base.MaxImageFileSize)
	largeFilePath := filepath.Join(tempDir, "large.png")
	largeData := make([]byte, base.MaxImageFileSize+1)
	err = os.WriteFile(largeFilePath, largeData, 0o644)
	require.NoError(t, err)

	// Create a file with unsupported extension
	unsupportedPath := filepath.Join(tempDir, "test.bmp")
	err = os.WriteFile(unsupportedPath, pngData, 0o644)
	require.NoError(t, err)

	tests := []struct {
		name     string
		filePath string
		hasError bool
	}{
		{"Valid PNG file", testImagePath, false},
		{"Non-existent file", filepath.Join(tempDir, "nonexistent.png"), true},
		{"File too large", largeFilePath, true},
		{"Unsupported format", unsupportedPath, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImageFile(test.filePath)
			if test.hasError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestAddUserMessage(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a small test image file
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk header
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 pixel
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, // bit depth, color type, etc.
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, // IEND chunk
		0x42, 0x60, 0x82,
	}

	testImagePath := filepath.Join(tempDir, "test.png")
	err = os.WriteFile(testImagePath, pngData, 0o644)
	require.NoError(t, err)

	tests := []struct {
		name        string
		message     string
		images      []string
		expectCount int // Expected number of content blocks
	}{
		{"Text only", "Hello world", nil, 1},
		{"Text with valid image", "Analyze this image", []string{testImagePath}, 2},
		{"Text with HTTPS URL", "Check this URL", []string{"https://example.com/image.jpg"}, 2},
		{"Text with mixed valid/invalid images", "Mixed test", []string{testImagePath, "invalid-path.png"}, 2}, // Only valid image should be added
		{"Too many images", "Many images", make([]string, base.MaxImageCount+5), 1 + base.MaxImageCount},       // Should cap at base.MaxImageCount
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initialCount := len(thread.messages)

			// For the "too many images" test, fill the slice with valid HTTPS URLs
			if test.name == "Too many images" {
				for i := range test.images {
					test.images[i] = "https://example.com/image.jpg"
				}
			}

			thread.AddUserMessage(context.Background(), test.message, test.images...)

			// Should have added exactly one message
			assert.Equal(t, initialCount+1, len(thread.messages))

			// Check the last message
			lastMessage := thread.messages[len(thread.messages)-1]
			assert.Equal(t, anthropic.MessageParamRoleUser, lastMessage.Role)

			// Check content block count (text + valid images)
			expectedBlocks := test.expectCount
			if test.name == "Text with mixed valid/invalid images" {
				// Only the text and the valid image should be added
				expectedBlocks = 2
			}
			assert.Equal(t, expectedBlocks, len(lastMessage.Content))
		})
	}
}

func TestAddUserMessageGoalContextWithImagesSeparatesAttachments(t *testing.T) {
	thread := &Thread{}
	goalContext := "<goal_context>\nContinue working.\n</goal_context>"

	thread.AddUserMessage(context.Background(), goalContext, "data:image/png;base64,aGVsbG8=")

	require.Len(t, thread.messages, 2)
	attachments := thread.messages[0]
	assert.Equal(t, anthropic.MessageParamRoleUser, attachments.Role)
	require.Len(t, attachments.Content, 1)
	assert.NotNil(t, attachments.Content[0].OfImage)

	goalMessage := thread.messages[1]
	assert.Equal(t, anthropic.MessageParamRoleUser, goalMessage.Role)
	require.Len(t, goalMessage.Content, 1)
	require.NotNil(t, goalMessage.Content[0].OfText)
	assert.Equal(t, goalContext, goalMessage.Content[0].OfText.Text)
}

func TestProcessPendingSteerWithImages(t *testing.T) {
	homeDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	require.NoError(t, os.Setenv("HOME", homeDir))
	defer func() {
		if originalHome == "" {
			os.Unsetenv("HOME")
			return
		}
		require.NoError(t, os.Setenv("HOME", originalHome))
	}()

	steerStore, err := steer.NewSteerStore()
	require.NoError(t, err)
	require.NoError(t, steerStore.WriteSteerWithImages("conv-test", "Use this image", []string{"data:image/png;base64,aGVsbG8="}))

	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "conv-test", hooks.Trigger{}),
	}
	params := &anthropic.MessageNewParams{}
	handler := &llmtypes.StringCollectorHandler{Silent: true}

	err = thread.processPendingSteer(context.Background(), params, handler)
	require.NoError(t, err)

	require.Len(t, params.Messages, 1)
	require.Len(t, params.Messages[0].Content, 2)
	assert.NotNil(t, params.Messages[0].Content[0].OfImage)
	assert.Equal(t, "Use this image", params.Messages[0].Content[1].OfText.Text)
	assert.Contains(t, handler.CollectedText(), "🗣️ User steering: Use this image (1 image)")
	assert.False(t, steerStore.HasPendingSteer("conv-test"))
}

func TestProcessPendingSteerWithUserMessageHandler(t *testing.T) {
	homeDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	require.NoError(t, os.Setenv("HOME", homeDir))
	defer func() {
		if originalHome == "" {
			os.Unsetenv("HOME")
			return
		}
		require.NoError(t, os.Setenv("HOME", originalHome))
	}()

	steerStore, err := steer.NewSteerStore()
	require.NoError(t, err)
	require.NoError(t, steerStore.WriteSteerWithImages("conv-test", "Use this image", []string{"data:image/png;base64,aGVsbG8="}))

	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "conv-test", hooks.Trigger{}),
	}
	params := &anthropic.MessageNewParams{}
	handler := &captureUserMessageHandler{}

	err = thread.processPendingSteer(context.Background(), params, handler)
	require.NoError(t, err)

	assert.Equal(t, "Use this image", handler.content)
	assert.Equal(t, []string{"data:image/png;base64,aGVsbG8="}, handler.images)
	assert.Empty(t, handler.CollectedText())
}

func TestExecuteToolsParallelStreamsAndOrdersResults(t *testing.T) {
	firstBlock := anthropicToolUseBlockForTest(t, "toolu-first", map[string]any{"value": "first"}, "first_tool")
	secondBlock := anthropicToolUseBlockForTest(t, "toolu-second", map[string]any{"value": "second"}, "second_tool")
	state := tools.NewBasicState(
		context.Background(),
		tools.WithLLMConfig(llmtypes.Config{AllowedTools: []string{"first_tool", "second_tool"}}),
		tools.WithExtraMCPTools([]tooltypes.Tool{testTool{name: "first_tool"}, testTool{name: "second_tool"}}),
	)
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{NoHooks: true}, "conv-test", hooks.Trigger{}),
	}
	thread.SetState(state)
	handler := &captureAnthropicToolHandler{}

	results, err := thread.executeToolsParallel(context.Background(), handler, []struct {
		block   anthropic.ContentBlockUnion
		variant anthropic.ToolUseBlock
	}{
		{block: firstBlock, variant: firstBlock.AsToolUse()},
		{block: secondBlock, variant: secondBlock.AsToolUse()},
	}, llmtypes.MessageOpt{})

	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, []string{
		`toolu-first:first_tool:{"value":"first"}`,
		`toolu-second:second_tool:{"value":"second"}`,
	}, handler.toolUses)
	assert.Equal(t, "toolu-first", results[0].blockID)
	assert.Equal(t, "first_tool", results[0].toolName)
	assert.Equal(t, `{"value":"first"}`, results[0].input)
	assert.Equal(t, "toolu-second", results[1].blockID)
	assert.Equal(t, "second_tool", results[1].toolName)
	assert.ElementsMatch(t, []string{"toolu-first:first_tool", "toolu-second:second_tool"}, handler.toolResults)
}

func TestExecuteToolsParallelHandlesEmptyCancelledAndSubscriptionNames(t *testing.T) {
	thread := &Thread{Thread: base.NewThread(llmtypes.Config{NoHooks: true}, "conv-test", hooks.Trigger{})}
	results, err := thread.executeToolsParallel(context.Background(), &captureAnthropicToolHandler{}, nil, llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.Nil(t, results)

	block := anthropicToolUseBlockForTest(t, "toolu-cancelled", map[string]any{}, "Missing_tool")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	thread.useSubscription = true
	results, err = thread.executeToolsParallel(cancelled, &captureAnthropicToolHandler{}, []struct {
		block   anthropic.ContentBlockUnion
		variant anthropic.ToolUseBlock
	}{{block: block, variant: block.AsToolUse()}}, llmtypes.MessageOpt{})

	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, results)
}

type captureUserMessageHandler struct {
	llmtypes.StringCollectorHandler
	content string
	images  []string
}

func (h *captureUserMessageHandler) HandleUserMessage(content string, images []string) {
	h.content = content
	h.images = append([]string(nil), images...)
}

type captureAnthropicToolHandler struct {
	toolUses    []string
	toolResults []string
}

func (h *captureAnthropicToolHandler) HandleText(string) {}

func (h *captureAnthropicToolHandler) HandleToolUse(toolCallID, toolName, input string) {
	h.toolUses = append(h.toolUses, toolCallID+":"+toolName+":"+input)
}

func (h *captureAnthropicToolHandler) HandleToolResult(toolCallID, toolName string, _ tooltypes.ToolResult) {
	h.toolResults = append(h.toolResults, toolCallID+":"+toolName)
}

func (h *captureAnthropicToolHandler) HandleThinking(string) {}
func (h *captureAnthropicToolHandler) HandleDone()           {}

func anthropicToolUseBlockForTest(t *testing.T, id string, input any, name string) anthropic.ContentBlockUnion {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"id":    id,
		"type":  "tool_use",
		"name":  name,
		"input": input,
	})
	require.NoError(t, err)

	var block anthropic.ContentBlockUnion
	require.NoError(t, json.Unmarshal(data, &block))
	return block
}

func TestShouldAutoCompact(t *testing.T) {
	tests := []struct {
		name                 string
		compactRatio         float64
		currentContextWindow int
		maxContextWindow     int
		expectedResult       bool
	}{
		{
			name:                 "should compact when ratio exceeded",
			compactRatio:         0.8,
			currentContextWindow: 80,
			maxContextWindow:     100,
			expectedResult:       true,
		},
		{
			name:                 "should not compact when ratio not exceeded",
			compactRatio:         0.8,
			currentContextWindow: 70,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when ratio is zero",
			compactRatio:         0.0,
			currentContextWindow: 90,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when ratio is negative",
			compactRatio:         -0.5,
			currentContextWindow: 90,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when ratio is greater than 1",
			compactRatio:         1.5,
			currentContextWindow: 90,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when max context window is zero",
			compactRatio:         0.8,
			currentContextWindow: 80,
			maxContextWindow:     0,
			expectedResult:       false,
		},
		{
			name:                 "should compact when ratio is exactly at threshold",
			compactRatio:         0.8,
			currentContextWindow: 80,
			maxContextWindow:     100,
			expectedResult:       true,
		},
		{
			name:                 "should compact when ratio is 1.0 and context is full",
			compactRatio:         1.0,
			currentContextWindow: 100,
			maxContextWindow:     100,
			expectedResult:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			thread, err := NewAnthropicThread(llmtypes.Config{})
			require.NoError(t, err)

			// Mock the usage stats
			thread.Usage.CurrentContextWindow = test.currentContextWindow
			thread.Usage.MaxContextWindow = test.maxContextWindow

			result := thread.ShouldAutoCompact(test.compactRatio)
			assert.Equal(t, test.expectedResult, result)
		})
	}
}

func TestIsThinkingModel(t *testing.T) {
	tests := []struct {
		name     string
		model    anthropic.Model
		expected bool
	}{
		{
			name:     "opus 4.8 supports thinking",
			model:    anthropic.ModelClaudeOpus4_8,
			expected: true,
		},
		{
			name:     "mythos preview supports thinking",
			model:    anthropic.ModelClaudeMythosPreview,
			expected: true,
		},
		{
			name:     "haiku 4.5 alias supports thinking",
			model:    anthropic.ModelClaudeHaiku4_5,
			expected: true,
		},
		{
			name:     "haiku 4.5 dated model supports thinking",
			model:    anthropic.ModelClaudeHaiku4_5_20251001,
			expected: true,
		},
		{
			name:     "sonnet 4.5 supports thinking",
			model:    anthropic.ModelClaudeSonnet4_5,
			expected: true,
		},
		{
			name:     "unsupported model does not support thinking",
			model:    anthropic.Model("unsupported-model"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isThinkingModel(tt.model))
		})
	}
}

func TestThinkingConfigForModel(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{ThinkingBudgetTokens: 4096})
	require.NoError(t, err)

	t.Run("adaptive models use adaptive thinking", func(t *testing.T) {
		config, ok := thread.thinkingConfigForModel(anthropic.ModelClaudeOpus4_7)
		require.True(t, ok)
		require.NotNil(t, config.OfAdaptive)
		assert.Nil(t, config.GetBudgetTokens())
		require.NotNil(t, config.GetType())
		assert.Equal(t, "adaptive", *config.GetType())
		assert.Equal(t, anthropic.ThinkingConfigAdaptiveDisplaySummarized, config.OfAdaptive.Display)
	})

	t.Run("legacy models keep budgeted thinking", func(t *testing.T) {
		config, ok := thread.thinkingConfigForModel(anthropic.ModelClaudeSonnet4_5)
		require.True(t, ok)
		require.NotNil(t, config.OfEnabled)
		require.NotNil(t, config.GetBudgetTokens())
		assert.EqualValues(t, 4096, *config.GetBudgetTokens())
		require.NotNil(t, config.GetType())
		assert.Equal(t, "enabled", *config.GetType())
		assert.Equal(t, anthropic.ThinkingConfigEnabledDisplaySummarized, config.OfEnabled.Display)
	})

	t.Run("adaptive models ignore zero budget", func(t *testing.T) {
		disabledThread, err := NewAnthropicThread(llmtypes.Config{ThinkingBudgetTokens: 0})
		require.NoError(t, err)

		config, ok := disabledThread.thinkingConfigForModel(anthropic.ModelClaudeOpus4_7)
		require.True(t, ok)
		require.NotNil(t, config.OfAdaptive)
		assert.Equal(t, "adaptive", *config.GetType())
	})

	t.Run("reasoning effort none disables adaptive thinking", func(t *testing.T) {
		disabledThread, err := NewAnthropicThread(llmtypes.Config{ReasoningEffort: "none"})
		require.NoError(t, err)

		config, ok := disabledThread.thinkingConfigForModel(anthropic.ModelClaudeOpus4_7)
		assert.False(t, ok)
		assert.Nil(t, config.GetType())
	})

	t.Run("zero budget disables manual thinking", func(t *testing.T) {
		disabledThread, err := NewAnthropicThread(llmtypes.Config{ThinkingBudgetTokens: 0})
		require.NoError(t, err)

		config, ok := disabledThread.thinkingConfigForModel(anthropic.ModelClaudeSonnet4_5)
		assert.False(t, ok)
		assert.Nil(t, config.GetType())
	})
}

func TestValidateThinkingConfigForModel(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{ReasoningEffort: "none"})
	require.NoError(t, err)

	t.Run("mythos rejects disabled adaptive thinking", func(t *testing.T) {
		err := thread.validateThinkingConfigForModel(anthropic.ModelClaudeMythosPreview)
		require.Error(t, err)
		assert.ErrorContains(t, err, "does not support disabling adaptive thinking")
	})

	t.Run("opus 4.7 allows disabled adaptive thinking", func(t *testing.T) {
		err := thread.validateThinkingConfigForModel(anthropic.ModelClaudeOpus4_7)
		assert.NoError(t, err)
	})
}

func TestAnthropicReasoningEffortForModel(t *testing.T) {
	tests := []struct {
		name       string
		model      anthropic.Model
		configured string
		expected   anthropic.OutputConfigEffort
		ok         bool
	}{
		{
			name:       "adaptive model defaults to medium",
			model:      anthropic.ModelClaudeOpus4_7,
			configured: "",
			expected:   anthropic.OutputConfigEffortMedium,
			ok:         true,
		},
		{
			name:       "none maps to low for anthropic",
			model:      anthropic.ModelClaudeOpus4_7,
			configured: "none",
			expected:   anthropic.OutputConfigEffortLow,
			ok:         true,
		},
		{
			name:       "minimal maps to low for anthropic",
			model:      anthropic.ModelClaudeOpus4_7,
			configured: "minimal",
			expected:   anthropic.OutputConfigEffortLow,
			ok:         true,
		},
		{
			name:       "xhigh is preserved on opus 4.7",
			model:      anthropic.ModelClaudeOpus4_7,
			configured: "xhigh",
			expected:   anthropic.OutputConfigEffortXhigh,
			ok:         true,
		},
		{
			name:       "xhigh falls back to high on sonnet 4.6",
			model:      anthropic.ModelClaudeSonnet4_6,
			configured: "xhigh",
			expected:   anthropic.OutputConfigEffortHigh,
			ok:         true,
		},
		{
			name:       "max is supported on adaptive models",
			model:      anthropic.ModelClaudeSonnet4_6,
			configured: "max",
			expected:   anthropic.OutputConfigEffortMax,
			ok:         true,
		},
		{
			name:       "non adaptive models do not get output config",
			model:      anthropic.ModelClaudeSonnet4_5,
			configured: "medium",
			expected:   "",
			ok:         false,
		},
		{
			name:       "invalid values are ignored",
			model:      anthropic.ModelClaudeOpus4_7,
			configured: "banana",
			expected:   "",
			ok:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effort, ok := anthropicReasoningEffortForModel(tt.model, tt.configured)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.expected, effort)
		})
	}
}

func TestOutputConfigForModel(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{ReasoningEffort: "xhigh"})
	require.NoError(t, err)

	t.Run("adaptive models emit output config", func(t *testing.T) {
		config, ok := thread.outputConfigForModel(anthropic.ModelClaudeOpus4_7)
		require.True(t, ok)
		assert.Equal(t, anthropic.OutputConfigEffortXhigh, config.Effort)
	})

	t.Run("unsupported models omit output config", func(t *testing.T) {
		config, ok := thread.outputConfigForModel(anthropic.ModelClaudeSonnet4_5)
		assert.False(t, ok)
		assert.Equal(t, anthropic.OutputConfigEffort(""), config.Effort)
	})

	t.Run("adaptive models keep low effort when thinking is disabled", func(t *testing.T) {
		disabledThread, err := NewAnthropicThread(llmtypes.Config{ReasoningEffort: "none"})
		require.NoError(t, err)

		config, ok := disabledThread.outputConfigForModel(anthropic.ModelClaudeOpus4_7)
		require.True(t, ok)
		assert.Equal(t, anthropic.OutputConfigEffortLow, config.Effort)
	})
}

func TestRequiresInterleavedThinkingBeta(t *testing.T) {
	t.Run("adaptive thinking does not need beta header", func(t *testing.T) {
		params := anthropic.MessageNewParams{
			Thinking: anthropic.ThinkingConfigParamUnion{
				OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
			},
		}

		assert.False(t, requiresInterleavedThinkingBeta(params))
	})

	t.Run("manual thinking keeps beta header", func(t *testing.T) {
		params := anthropic.MessageNewParams{
			Thinking: anthropic.ThinkingConfigParamUnion{
				OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: 4096},
			},
		}

		assert.True(t, requiresInterleavedThinkingBeta(params))
	})
}

func TestCompactContextIntegration(t *testing.T) {
	// Skip if no API key is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
	}

	t.Run("real compact context with API call", func(t *testing.T) {
		thread, err := NewAnthropicThread(llmtypes.Config{
			Model:     "claude-haiku-4-5-20251001", // Use faster/cheaper model for testing
			MaxTokens: 1000,                        // Limit tokens for test
		})
		require.NoError(t, err)

		// Set up some realistic conversation history
		thread.AddUserMessage(context.Background(), "Help me debug this Python function", []string{}...)
		thread.messages = append(thread.messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("I'd be happy to help you debug your Python function. Could you please share the code?"),
			},
		})
		thread.AddUserMessage(context.Background(), "Here's the function: def add(a, b): return a + b", []string{}...)
		thread.messages = append(thread.messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("Your function looks correct. It's a simple addition function that takes two parameters and returns their sum."),
			},
		})

		// Add some tool results to verify they get cleared
		thread.ToolResults = map[string]tooltypes.StructuredToolResult{
			"tool1": {ToolName: "test_tool", Success: true, Timestamp: time.Now()},
			"tool2": {ToolName: "another_tool", Success: false, Error: "test error", Timestamp: time.Now()},
		}

		// Record initial state
		initialMessageCount := len(thread.messages)
		initialToolResultCount := len(thread.ToolResults)

		// Verify we have multiple messages and tool results
		assert.Greater(t, initialMessageCount, 2, "Should have multiple messages for meaningful test")
		assert.Greater(t, initialToolResultCount, 0, "Should have tool results to verify clearing")

		// Call the real CompactContext method with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = thread.CompactContext(ctx)
		require.NoError(t, err, "CompactContext should succeed with real API")

		// Verify the compacting worked
		assert.Equal(t, 1, len(thread.messages), "Should be compacted to single user message")
		assert.Equal(t, 0, len(thread.ToolResults), "Tool results should be cleared")

		// Verify the single remaining message is a user message containing a summary
		if len(thread.messages) > 0 {
			assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[0].Role)
			assert.Greater(t, len(thread.messages[0].Content), 0, "Compact message should have content")

			// Extract text content and verify it's a reasonable summary
			var messageText string
			for _, block := range thread.messages[0].Content {
				if block.OfText != nil {
					messageText += block.OfText.Text
				}
			}
			assert.Greater(t, len(messageText), 50, "Compact summary should be substantial")
			assert.Contains(t, messageText, "Python", "Summary should mention the context discussed")
		}
	})

	t.Run("compact context preserves thread functionality", func(t *testing.T) {
		// Skip if no API key is available
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
		}

		thread, err := NewAnthropicThread(llmtypes.Config{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 500,
		})
		require.NoError(t, err)

		// Add some conversation history
		thread.AddUserMessage(context.Background(), "What is 2+2?", []string{}...)
		thread.messages = append(thread.messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("2+2 equals 4."),
			},
		})

		// Compact the context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = thread.CompactContext(ctx)
		require.NoError(t, err)

		// Verify thread is still functional by sending a new message
		thread.AddUserMessage(context.Background(), "What about 3+3?", []string{}...)

		// Should now have 2 messages: the compact summary + new user message
		assert.Equal(t, 2, len(thread.messages))
		assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[1].Role)
	})
}

func TestAutoCompactTriggerLogic(t *testing.T) {
	t.Run("auto-compact triggers when ratio exceeded", func(t *testing.T) {
		thread, err := NewAnthropicThread(llmtypes.Config{})
		require.NoError(t, err)

		// Set up context window to trigger auto-compact
		thread.Usage.CurrentContextWindow = 85 // 85% utilization
		thread.Usage.MaxContextWindow = 100

		// Verify ShouldAutoCompact returns true for ratio 0.8
		assert.True(t, thread.ShouldAutoCompact(0.8),
			"Should trigger auto-compact when ratio (0.85) exceeds threshold (0.8)")
	})

	t.Run("auto-compact does not trigger when ratio not exceeded", func(t *testing.T) {
		thread, err := NewAnthropicThread(llmtypes.Config{})
		require.NoError(t, err)

		// Set up context window below auto-compact threshold
		thread.Usage.CurrentContextWindow = 75 // 75% utilization
		thread.Usage.MaxContextWindow = 100

		// Verify ShouldAutoCompact returns false for ratio 0.8
		assert.False(t, thread.ShouldAutoCompact(0.8),
			"Should not trigger auto-compact when ratio (0.75) below threshold (0.8)")
	})

	t.Run("auto-compact respects different compact ratios", func(t *testing.T) {
		tests := []struct {
			name          string
			ratio         float64
			utilization   int
			shouldTrigger bool
		}{
			{
				name:          "conservative ratio - should not trigger",
				ratio:         0.9,
				utilization:   85,
				shouldTrigger: false,
			},
			{
				name:          "conservative ratio - should trigger",
				ratio:         0.9,
				utilization:   95,
				shouldTrigger: true,
			},
			{
				name:          "aggressive ratio - should trigger",
				ratio:         0.5,
				utilization:   60,
				shouldTrigger: true,
			},
			{
				name:          "aggressive ratio - should not trigger",
				ratio:         0.5,
				utilization:   40,
				shouldTrigger: false,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				thread, err := NewAnthropicThread(llmtypes.Config{})
				require.NoError(t, err)

				// Set up context window
				thread.Usage.CurrentContextWindow = test.utilization
				thread.Usage.MaxContextWindow = 100

				result := thread.ShouldAutoCompact(test.ratio)
				assert.Equal(t, test.shouldTrigger, result,
					"Compact ratio %f with %d%% utilization should trigger: %v",
					test.ratio, test.utilization, test.shouldTrigger)
			})
		}
	})
}

func TestNormalizeToolName(t *testing.T) {
	tests := []struct {
		name            string
		useSubscription bool
		toolName        string
		expected        string
	}{
		{
			name:            "subscription mode decapitalizes",
			useSubscription: true,
			toolName:        "File_read",
			expected:        "file_read",
		},
		{
			name:            "subscription mode already lowercase",
			useSubscription: true,
			toolName:        "file_read",
			expected:        "file_read",
		},
		{
			name:            "subscription mode empty string",
			useSubscription: true,
			toolName:        "",
			expected:        "",
		},
		{
			name:            "non-subscription mode normal name",
			useSubscription: false,
			toolName:        "file_read",
			expected:        "file_read",
		},
		{
			name:            "non-subscription mode capitalized input preserved",
			useSubscription: false,
			toolName:        "File_read",
			expected:        "File_read",
		},
		{
			name:            "non-subscription mode empty string",
			useSubscription: false,
			toolName:        "",
			expected:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thread := &Thread{useSubscription: tt.useSubscription}
			result := thread.normalizeToolName(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCapitalizeToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase first letter",
			input:    "file_read",
			expected: "File_read",
		},
		{
			name:     "already capitalized",
			input:    "File_read",
			expected: "File_read",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single character",
			input:    "a",
			expected: "A",
		},
		{
			name:     "single uppercase character",
			input:    "A",
			expected: "A",
		},
		{
			name:     "underscore first",
			input:    "_test",
			expected: "_test",
		},
		{
			name:     "unicode character",
			input:    "über_tool",
			expected: "Über_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := capitalizeToolName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDecapitalizeToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "uppercase first letter",
			input:    "File_read",
			expected: "file_read",
		},
		{
			name:     "already lowercase",
			input:    "file_read",
			expected: "file_read",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single character",
			input:    "A",
			expected: "a",
		},
		{
			name:     "single lowercase character",
			input:    "a",
			expected: "a",
		},
		{
			name:     "underscore first",
			input:    "_Test",
			expected: "_Test",
		},
		{
			name:     "unicode character",
			input:    "Über_tool",
			expected: "über_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decapitalizeToolName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToAnthropicTools(t *testing.T) {
	t.Run("with subscription", func(t *testing.T) {
		tool := testTool{name: "file_read"}
		tools := toAnthropicTools([]tooltypes.Tool{tool}, true)
		require.Len(t, tools, 1)
		require.NotNil(t, tools[0].OfTool)
		assert.Equal(t, "File_read", tools[0].OfTool.Name)
	})

	t.Run("without subscription", func(t *testing.T) {
		tool := testTool{name: "file_read"}
		tools := toAnthropicTools([]tooltypes.Tool{tool}, false)
		require.Len(t, tools, 1)
		require.NotNil(t, tools[0].OfTool)
		assert.Equal(t, "file_read", tools[0].OfTool.Name)
	})

	t.Run("empty tools slice", func(t *testing.T) {
		tools := toAnthropicTools([]tooltypes.Tool{}, true)
		require.Len(t, tools, 0)
	})

	t.Run("multiple tools", func(t *testing.T) {
		toolList := []tooltypes.Tool{
			testTool{name: "file_read"},
			testTool{name: "bash"},
			testTool{name: "grep_tool"},
		}
		tools := toAnthropicTools(toolList, true)
		require.Len(t, tools, 3)
		assert.Equal(t, "File_read", tools[0].OfTool.Name)
		assert.Equal(t, "Bash", tools[1].OfTool.Name)
		assert.Equal(t, "Grep_tool", tools[2].OfTool.Name)
	})
}

type testTool struct {
	name string
}

type fakeAnthropicMultiModalToolResult struct {
	tooltypes.BaseToolResult
	parts []tooltypes.ToolResultContentPart
}

func (r fakeAnthropicMultiModalToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	return r.parts
}

func (t testTool) GenerateSchema() *jsonschema.Schema {
	return &jsonschema.Schema{}
}

func (t testTool) Name() string {
	return t.name
}

func (t testTool) Description() string {
	return "test"
}

func (t testTool) ValidateInput(_ tooltypes.State, _ string) error {
	return nil
}

func (t testTool) Execute(_ context.Context, _ tooltypes.State, _ string) tooltypes.ToolResult {
	return tooltypes.BaseToolResult{}
}

func (t testTool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
	return nil, nil
}
