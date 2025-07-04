package openai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOpenAIThread(t *testing.T) {
	// Test with default values
	config := llm.Config{}
	thread := NewOpenAIThread(config)

	assert.Equal(t, "gpt-4.1", thread.config.Model)
	assert.Equal(t, 8192, thread.config.MaxTokens)
	assert.Equal(t, "medium", thread.reasoningEffort)

	// Test with custom values
	config = llm.Config{
		Model:           "gpt-4o",
		MaxTokens:       4096,
		ReasoningEffort: "high",
	}
	thread = NewOpenAIThread(config)

	assert.Equal(t, "gpt-4o", thread.config.Model)
	assert.Equal(t, 4096, thread.config.MaxTokens)
	assert.Equal(t, "high", thread.reasoningEffort)
}

func TestGetModelPricing(t *testing.T) {
	// Test exact matches
	pricing := getModelPricing("gpt-4.1")
	assert.Equal(t, 0.000002, pricing.Input)
	assert.Equal(t, 0.000008, pricing.Output)
	assert.Equal(t, 1047576, pricing.ContextWindow)

	// Test fuzzy matches
	pricing = getModelPricing("gpt-4.1-preview")
	assert.Equal(t, 0.000002, pricing.Input) // Should match gpt-4.1

	pricing = getModelPricing("gpt-4.1-mini-preview")
	assert.Equal(t, 0.0000004, pricing.Input) // Should match gpt-4.1-mini

	pricing = getModelPricing("gpt-4o-latest")
	assert.Equal(t, 0.0000025, pricing.Input) // Should match gpt-4o

	// Test unknown model
	pricing = getModelPricing("unknown-model")
	assert.Equal(t, 0.000002, pricing.Input) // Should default to gpt-4.1
}

func TestExtractMessages(t *testing.T) {
	// Simple test case with a few messages
	messagesJSON := `[
		{"role": "system", "content": "You are a helpful AI assistant."},
		{"role": "user", "content": "Hello!"},
		{"role": "assistant", "content": "Hi there! How can I help you today?"},
		{"role": "user", "content": "Can you help me with a project?"},
		{"role": "assistant", "content": "Of course! What kind of project are you working on?"}
	]`

	messages, err := ExtractMessages([]byte(messagesJSON), nil)
	assert.NoError(t, err)
	assert.Len(t, messages, 4) // System message should be filtered out

	// Check first user message
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Hello!", messages[0].Content)

	// Check first assistant message
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "Hi there! How can I help you today?", messages[1].Content)

	// Test with tool calls
	messagesWithToolsJSON := `[
		{"role": "system", "content": "You are a helpful AI assistant."},
		{"role": "user", "content": "What time is it?"},
		{"role": "assistant", "content": "", "tool_calls": [{"id": "call_123", "function": {"name": "get_time", "arguments": "{}"}}]},
		{"role": "tool", "content": "10:30 AM", "tool_call_id": "call_123"},
		{"role": "assistant", "content": "The current time is 10:30 AM."}
	]`

	messages, err = ExtractMessages([]byte(messagesWithToolsJSON), nil)
	assert.NoError(t, err)
	assert.Len(t, messages, 4) // System message should be filtered out, tool message processed

	// Check that tool calls are properly serialized
	toolCallMessage := messages[1]
	assert.Equal(t, "assistant", toolCallMessage.Role)
	assert.Contains(t, toolCallMessage.Content, "get_time") // The content should contain the serialized tool call
}

func TestExtractMessagesWithMultipleToolResults(t *testing.T) {
	// Test with multiple tool calls and results
	messagesWithMultipleToolsJSON := `[
		{"role": "system", "content": "You are a helpful AI assistant."},
		{"role": "user", "content": "Get weather and time"},
		{"role": "assistant", "content": "", "tool_calls": [
			{"id": "call_time", "function": {"name": "get_time", "arguments": "{}"}},
			{"id": "call_weather", "function": {"name": "get_weather", "arguments": "{\"location\": \"NYC\"}"}}
		]},
		{"role": "tool", "content": "Current time: 10:30 AM", "tool_call_id": "call_time"},
		{"role": "tool", "content": "Weather: 72°F, sunny", "tool_call_id": "call_weather"},
		{"role": "assistant", "content": "Here's the info you requested."}
	]`

	toolResults := map[string]tooltypes.StructuredToolResult{
		"call_time": {
			ToolName:  "get_time",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		},
		"call_weather": {
			ToolName:  "get_weather",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		},
	}

	messages, err := ExtractMessages([]byte(messagesWithMultipleToolsJSON), toolResults)
	assert.NoError(t, err)
	assert.Len(t, messages, 6) // user + 2 tool calls + 2 tool results + assistant final

	// Check first tool call
	firstToolCall := messages[1]
	assert.Equal(t, "assistant", firstToolCall.Role)
	assert.Contains(t, firstToolCall.Content, "get_time")

	// Check second tool call
	secondToolCall := messages[2]
	assert.Equal(t, "assistant", secondToolCall.Role)
	assert.Contains(t, secondToolCall.Content, "get_weather")

	// Check first tool result uses CLI rendering
	firstToolResult := messages[3]
	assert.Equal(t, "assistant", firstToolResult.Role)
	assert.Contains(t, firstToolResult.Content, "get_time")

	// Check second tool result uses CLI rendering
	secondToolResult := messages[4]
	assert.Equal(t, "assistant", secondToolResult.Role)
	assert.Contains(t, secondToolResult.Content, "get_weather")
}

// Image Processing Tests

func TestGetImageMediaType(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
		hasError bool
	}{
		{".jpg", "image/jpeg", false},
		{".jpeg", "image/jpeg", false},
		{".JPG", "image/jpeg", false},
		{".JPEG", "image/jpeg", false},
		{".png", "image/png", false},
		{".PNG", "image/png", false},
		{".gif", "image/gif", false},
		{".GIF", "image/gif", false},
		{".webp", "image/webp", false},
		{".WEBP", "image/webp", false},
		{".txt", "", true},
		{".doc", "", true},
		{".pdf", "", true},
		{"", "", true},
	}

	for _, test := range tests {
		t.Run(test.ext, func(t *testing.T) {
			mediaType, err := getImageMediaType(test.ext)
			if test.hasError {
				assert.Error(t, err)
				assert.Empty(t, mediaType)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, mediaType)
			}
		})
	}
}

func TestProcessImageURL(t *testing.T) {
	thread := NewOpenAIThread(llm.Config{})

	tests := []struct {
		name        string
		url         string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid HTTPS URL",
			url:         "https://example.com/image.jpg",
			expectError: false,
		},
		{
			name:        "Valid HTTPS URL with query params",
			url:         "https://cdn.example.com/images/photo.png?size=large",
			expectError: false,
		},
		{
			name:        "Invalid HTTP URL",
			url:         "http://example.com/image.jpg",
			expectError: true,
			errorMsg:    "only HTTPS URLs are supported for security",
		},
		{
			name:        "Invalid FTP URL",
			url:         "ftp://example.com/image.jpg",
			expectError: true,
			errorMsg:    "only HTTPS URLs are supported for security",
		},
		{
			name:        "Malformed URL",
			url:         "not-a-url",
			expectError: true,
			errorMsg:    "only HTTPS URLs are supported for security",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			part, err := thread.processImageURL(test.url)

			if test.expectError {
				assert.Error(t, err)
				assert.Nil(t, part)
				if test.errorMsg != "" {
					assert.Contains(t, err.Error(), test.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, part)
				assert.Equal(t, openai.ChatMessagePartTypeImageURL, part.Type)
				assert.NotNil(t, part.ImageURL)
				assert.Equal(t, test.url, part.ImageURL.URL)
				assert.Equal(t, openai.ImageURLDetailAuto, part.ImageURL.Detail)
			}
		})
	}
}

func TestProcessImageFile(t *testing.T) {
	thread := NewOpenAIThread(llm.Config{})

	// Create temporary test directory
	tempDir := t.TempDir()

	// Create test files
	smallImagePath := filepath.Join(tempDir, "small.jpg")
	largeImagePath := filepath.Join(tempDir, "large.jpg")
	unsupportedPath := filepath.Join(tempDir, "document.txt")

	// Create a small valid JPEG-like file (just some bytes for testing)
	smallImageData := []byte("fake-jpeg-data-for-testing")
	err := os.WriteFile(smallImagePath, smallImageData, 0644)
	require.NoError(t, err)

	// Create a large file (exceeding MaxImageFileSize)
	largeImageData := make([]byte, MaxImageFileSize+1)
	err = os.WriteFile(largeImagePath, largeImageData, 0644)
	require.NoError(t, err)

	// Create an unsupported file
	textData := []byte("This is not an image")
	err = os.WriteFile(unsupportedPath, textData, 0644)
	require.NoError(t, err)

	tests := []struct {
		name        string
		filePath    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid small image file",
			filePath:    smallImagePath,
			expectError: false,
		},
		{
			name:        "File too large",
			filePath:    largeImagePath,
			expectError: true,
			errorMsg:    "image file too large",
		},
		{
			name:        "Unsupported file format",
			filePath:    unsupportedPath,
			expectError: true,
			errorMsg:    "unsupported image format",
		},
		{
			name:        "Non-existent file",
			filePath:    filepath.Join(tempDir, "nonexistent.jpg"),
			expectError: true,
			errorMsg:    "image file not found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			part, err := thread.processImageFile(test.filePath)

			if test.expectError {
				assert.Error(t, err)
				assert.Nil(t, part)
				if test.errorMsg != "" {
					assert.Contains(t, err.Error(), test.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, part)
				assert.Equal(t, openai.ChatMessagePartTypeImageURL, part.Type)
				assert.NotNil(t, part.ImageURL)
				assert.Contains(t, part.ImageURL.URL, "data:image/jpeg;base64,")
				assert.Equal(t, openai.ImageURLDetailAuto, part.ImageURL.Detail)
			}
		})
	}
}

func TestProcessImage(t *testing.T) {
	thread := NewOpenAIThread(llm.Config{})

	// Create temporary test file
	tempDir := t.TempDir()
	testImagePath := filepath.Join(tempDir, "test.png")
	imageData := []byte("fake-png-data-for-testing")
	err := os.WriteFile(testImagePath, imageData, 0644)
	require.NoError(t, err)

	tests := []struct {
		name        string
		imagePath   string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "HTTPS URL",
			imagePath:   "https://example.com/image.jpg",
			expectError: false,
		},
		{
			name:        "File URL",
			imagePath:   "file://" + testImagePath,
			expectError: false,
		},
		{
			name:        "Local file path",
			imagePath:   testImagePath,
			expectError: false,
		},
		{
			name:        "HTTP URL (should fail)",
			imagePath:   "http://example.com/image.jpg",
			expectError: true,
			errorMsg:    "only HTTPS URLs are supported for security",
		},
		{
			name:        "Non-existent local file",
			imagePath:   filepath.Join(tempDir, "nonexistent.jpg"),
			expectError: true,
			errorMsg:    "image file not found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			part, err := thread.processImage(test.imagePath)

			if test.expectError {
				assert.Error(t, err)
				assert.Nil(t, part)
				if test.errorMsg != "" {
					assert.Contains(t, err.Error(), test.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, part)
				assert.Equal(t, openai.ChatMessagePartTypeImageURL, part.Type)
				assert.NotNil(t, part.ImageURL)
			}
		})
	}
}

func TestAddUserMessageWithImages(t *testing.T) {
	thread := NewOpenAIThread(llm.Config{})

	// Create temporary test files
	tempDir := t.TempDir()
	validImagePath := filepath.Join(tempDir, "valid.jpg")
	invalidImagePath := filepath.Join(tempDir, "invalid.txt")

	// Create a valid image file
	imageData := []byte("fake-jpeg-data-for-testing")
	err := os.WriteFile(validImagePath, imageData, 0644)
	require.NoError(t, err)

	// Create an invalid file
	textData := []byte("This is not an image")
	err = os.WriteFile(invalidImagePath, textData, 0644)
	require.NoError(t, err)

	tests := []struct {
		name              string
		message           string
		imagePaths        []string
		expectedPartCount int
		expectedLastType  openai.ChatMessagePartType
		expectedLastText  string
	}{
		{
			name:              "Text only message",
			message:           "Hello, world!",
			imagePaths:        []string{},
			expectedPartCount: 1,
			expectedLastType:  openai.ChatMessagePartTypeText,
			expectedLastText:  "Hello, world!",
		},
		{
			name:              "Text with valid image",
			message:           "Analyze this image",
			imagePaths:        []string{validImagePath},
			expectedPartCount: 2,
			expectedLastType:  openai.ChatMessagePartTypeText,
			expectedLastText:  "Analyze this image",
		},
		{
			name:              "Text with HTTPS URL",
			message:           "Look at this",
			imagePaths:        []string{"https://example.com/image.jpg"},
			expectedPartCount: 2,
			expectedLastType:  openai.ChatMessagePartTypeText,
			expectedLastText:  "Look at this",
		},
		{
			name:              "Text with multiple valid images",
			message:           "Compare these",
			imagePaths:        []string{validImagePath, "https://example.com/image.jpg"},
			expectedPartCount: 3,
			expectedLastType:  openai.ChatMessagePartTypeText,
			expectedLastText:  "Compare these",
		},
		{
			name:              "Text with invalid image (should only have text)",
			message:           "This should work",
			imagePaths:        []string{invalidImagePath},
			expectedPartCount: 1, // Invalid image should be skipped
			expectedLastType:  openai.ChatMessagePartTypeText,
			expectedLastText:  "This should work",
		},
		{
			name:              "Text with mix of valid and invalid images",
			message:           "Mixed content",
			imagePaths:        []string{invalidImagePath, validImagePath, "https://example.com/image.jpg"},
			expectedPartCount: 3, // Text + 2 valid images (invalid one skipped)
			expectedLastType:  openai.ChatMessagePartTypeText,
			expectedLastText:  "Mixed content",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initialMessageCount := len(thread.messages)

			thread.AddUserMessage(context.Background(), test.message, test.imagePaths...)

			// Check that a message was added
			assert.Equal(t, initialMessageCount+1, len(thread.messages))

			// Get the last message (the one we just added)
			lastMessage := thread.messages[len(thread.messages)-1]

			// Check message properties
			assert.Equal(t, openai.ChatMessageRoleUser, lastMessage.Role)
			assert.Len(t, lastMessage.MultiContent, test.expectedPartCount)

			// Check last part (should always be text)
			if len(lastMessage.MultiContent) > 0 {
				lastPart := lastMessage.MultiContent[len(lastMessage.MultiContent)-1]
				assert.Equal(t, test.expectedLastType, lastPart.Type)
				assert.Equal(t, test.expectedLastText, lastPart.Text)
			}

			// Check that preceding parts are images if expected
			for i := 0; i < len(lastMessage.MultiContent)-1; i++ {
				part := lastMessage.MultiContent[i]
				assert.Equal(t, openai.ChatMessagePartTypeImageURL, part.Type)
				assert.NotNil(t, part.ImageURL)
			}
		})
	}
}

func TestAddUserMessageWithTooManyImages(t *testing.T) {
	thread := NewOpenAIThread(llm.Config{})

	// Create more image paths than the maximum allowed
	imagePaths := make([]string, MaxImageCount+5)
	for i := 0; i < len(imagePaths); i++ {
		imagePaths[i] = "https://example.com/image" + string(rune('0'+i)) + ".jpg"
	}

	initialMessageCount := len(thread.messages)
	thread.AddUserMessage(context.Background(), "Too many images", imagePaths...)

	// Check that a message was added
	assert.Equal(t, initialMessageCount+1, len(thread.messages))

	// Get the last message
	lastMessage := thread.messages[len(thread.messages)-1]

	// Should have text + MaxImageCount images
	expectedPartCount := 1 + MaxImageCount
	assert.Equal(t, expectedPartCount, len(lastMessage.MultiContent))

	// Last part should be text
	lastPartIndex := len(lastMessage.MultiContent) - 1
	assert.Equal(t, openai.ChatMessagePartTypeText, lastMessage.MultiContent[lastPartIndex].Type)
	assert.Equal(t, "Too many images", lastMessage.MultiContent[lastPartIndex].Text)

	// Preceding parts should be images
	for i := 0; i < len(lastMessage.MultiContent)-1; i++ {
		assert.Equal(t, openai.ChatMessagePartTypeImageURL, lastMessage.MultiContent[i].Type)
	}
}

func TestConstants(t *testing.T) {
	// Test that constants are set to expected values
	assert.Equal(t, 5*1024*1024, MaxImageFileSize)
	assert.Equal(t, 10, MaxImageCount)
}
