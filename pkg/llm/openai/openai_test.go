package openai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOpenAIThread(t *testing.T) {
	// Test with default values
	config := llm.Config{}
	thread, err := NewOpenAIThread(config, nil)
	require.NoError(t, err)

	assert.Equal(t, "gpt-4.1", thread.config.Model)
	assert.Equal(t, 8192, thread.config.MaxTokens)
	assert.Equal(t, "medium", thread.reasoningEffort)

	// Test with custom values
	config = llm.Config{
		Model:           "gpt-4o",
		MaxTokens:       4096,
		ReasoningEffort: "high",
	}
	thread, err = NewOpenAIThread(config, nil)
	require.NoError(t, err)

	assert.Equal(t, "gpt-4o", thread.config.Model)
	assert.Equal(t, 4096, thread.config.MaxTokens)
	assert.Equal(t, "high", thread.reasoningEffort)
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
	thread, err := NewOpenAIThread(llm.Config{}, nil)
	require.NoError(t, err)

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
	thread, err := NewOpenAIThread(llm.Config{}, nil)
	require.NoError(t, err)

	// Create temporary test directory
	tempDir := t.TempDir()

	// Create test files
	smallImagePath := filepath.Join(tempDir, "small.jpg")
	largeImagePath := filepath.Join(tempDir, "large.jpg")
	unsupportedPath := filepath.Join(tempDir, "document.txt")

	// Create a small valid JPEG-like file (just some bytes for testing)
	smallImageData := []byte("fake-jpeg-data-for-testing")
	err = os.WriteFile(smallImagePath, smallImageData, 0644)
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
	thread, err := NewOpenAIThread(llm.Config{}, nil)
	require.NoError(t, err)

	// Create temporary test file
	tempDir := t.TempDir()
	testImagePath := filepath.Join(tempDir, "test.png")
	imageData := []byte("fake-png-data-for-testing")
	err = os.WriteFile(testImagePath, imageData, 0644)
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
	thread, err := NewOpenAIThread(llm.Config{}, nil)
	require.NoError(t, err)

	// Create temporary test files
	tempDir := t.TempDir()
	validImagePath := filepath.Join(tempDir, "valid.jpg")
	invalidImagePath := filepath.Join(tempDir, "invalid.txt")

	// Create a valid image file
	imageData := []byte("fake-jpeg-data-for-testing")
	err = os.WriteFile(validImagePath, imageData, 0644)
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
	thread, err := NewOpenAIThread(llm.Config{}, nil)
	require.NoError(t, err)

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

func TestShouldAutoCompactOpenAI(t *testing.T) {
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
			thread, err := NewOpenAIThread(llm.Config{}, nil)
			require.NoError(t, err)

			// Mock the usage stats
			thread.usage.CurrentContextWindow = test.currentContextWindow
			thread.usage.MaxContextWindow = test.maxContextWindow

			result := thread.shouldAutoCompact(test.compactRatio)
			assert.Equal(t, test.expectedResult, result)
		})
	}
}

func TestGetLastAssistantMessageTextOpenAI(t *testing.T) {
	tests := []struct {
		name          string
		messages      []openai.ChatCompletionMessage
		expectedText  string
		expectedError bool
	}{
		{
			name: "single assistant message",
			messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Assistant response",
				},
			},
			expectedText:  "Assistant response",
			expectedError: false,
		},
		{
			name: "multiple messages with assistant last",
			messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "User message",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Assistant response",
				},
			},
			expectedText:  "Assistant response",
			expectedError: false,
		},
		{
			name: "multiple assistant messages - should get last one",
			messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "First assistant response",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "User message",
				},
				{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Second assistant response",
				},
			},
			expectedText:  "Second assistant response",
			expectedError: false,
		},
		{
			name:          "no messages",
			messages:      []openai.ChatCompletionMessage{},
			expectedText:  "",
			expectedError: true,
		},
		{
			name: "no assistant messages",
			messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "User message",
				},
			},
			expectedText:  "",
			expectedError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			thread, err := NewOpenAIThread(llm.Config{}, nil)
			require.NoError(t, err)

			thread.messages = test.messages

			result, err := thread.getLastAssistantMessageText()

			if test.expectedError {
				assert.Error(t, err)
				assert.Equal(t, test.expectedText, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expectedText, result)
			}
		})
	}
}

func TestCompactContextIntegrationOpenAI(t *testing.T) {
	// Skip if no API key is available
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}

	t.Run("real compact context with API call", func(t *testing.T) {
		thread, err := NewOpenAIThread(llm.Config{
			Model:     "gpt-4o-mini", // Use faster/cheaper model for testing
			MaxTokens: 1000,          // Limit tokens for test
		}, nil)
		require.NoError(t, err)

		// Set up some realistic conversation history
		thread.AddUserMessage(context.Background(), "Help me understand JavaScript closures", []string{}...)
		thread.messages = append(thread.messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "Closures are a fundamental concept in JavaScript. A closure is created when a function is defined inside another function and has access to the outer function's variables.",
		})
		thread.AddUserMessage(context.Background(), "Can you give me a simple example?", []string{}...)
		thread.messages = append(thread.messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "Sure! Here's a simple example: function outer() { let x = 10; return function inner() { return x; }; } const closure = outer(); console.log(closure()); // outputs 10",
		})

		// Add some tool results to verify they get cleared
		thread.toolResults = map[string]tooltypes.StructuredToolResult{
			"tool1": {ToolName: "test_tool", Success: true, Timestamp: time.Now()},
			"tool2": {ToolName: "another_tool", Success: false, Error: "test error", Timestamp: time.Now()},
		}

		// Record initial state
		initialMessageCount := len(thread.messages)
		initialToolResultCount := len(thread.toolResults)

		// Verify we have multiple messages and tool results
		assert.Greater(t, initialMessageCount, 2, "Should have multiple messages for meaningful test")
		assert.Greater(t, initialToolResultCount, 0, "Should have tool results to verify clearing")

		// Call the real CompactContext method
		err = thread.CompactContext(context.Background())
		require.NoError(t, err, "CompactContext should succeed with real API")

		// Verify the compacting worked
		assert.Equal(t, 1, len(thread.messages), "Should be compacted to single user message")
		assert.Equal(t, 0, len(thread.toolResults), "Tool results should be cleared")

		// Verify the single remaining message is a user message containing a summary
		if len(thread.messages) > 0 {
			assert.Equal(t, openai.ChatMessageRoleUser, thread.messages[0].Role)
			assert.Greater(t, len(thread.messages[0].Content), 50, "Compact summary should be substantial")
			assert.Contains(t, thread.messages[0].Content, "JavaScript", "Summary should mention the context discussed")
		}
	})

	t.Run("compact context preserves thread functionality", func(t *testing.T) {
		// Skip if no API key is available
		if os.Getenv("OPENAI_API_KEY") == "" {
			t.Skip("OPENAI_API_KEY not set, skipping integration test")
		}

		thread, err := NewOpenAIThread(llm.Config{
			Model:     "gpt-4o-mini",
			MaxTokens: 500,
		}, nil)
		require.NoError(t, err)

		// Add some conversation history
		thread.AddUserMessage(context.Background(), "What is the capital of France?", []string{}...)
		thread.messages = append(thread.messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "The capital of France is Paris.",
		})

		// Compact the context
		err = thread.CompactContext(context.Background())
		require.NoError(t, err)

		// Verify thread is still functional by sending a new message
		thread.AddUserMessage(context.Background(), "What about Germany?", []string{}...)

		// Should now have 2 messages: the compact summary + new user message
		assert.Equal(t, 2, len(thread.messages))
		assert.Equal(t, openai.ChatMessageRoleUser, thread.messages[1].Role)
	})
}

func TestWithSubAgentOpenAI(t *testing.T) {
	t.Run("WithSubAgent correctly passes compact configuration", func(t *testing.T) {
		parentThread, err := NewOpenAIThread(llm.Config{}, nil)
		require.NoError(t, err)

		// Set up a basic state for the parent thread using NewBasicState
		parentThread.SetState(tools.NewBasicState(context.Background()))

		// Test different compact configurations
		testCases := []struct {
			name               string
			compactRatio       float64
			disableAutoCompact bool
		}{
			{
				name:               "Default configuration",
				compactRatio:       0.0,
				disableAutoCompact: false,
			},
			{
				name:               "High compact ratio, enabled",
				compactRatio:       0.9,
				disableAutoCompact: false,
			},
			{
				name:               "Auto-compact disabled",
				compactRatio:       0.8,
				disableAutoCompact: true,
			},
			{
				name:               "Edge case: ratio 1.0",
				compactRatio:       1.0,
				disableAutoCompact: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Create a context with subagent configuration
				ctx := parentThread.WithSubAgent(
					context.Background(),
					&llm.StringCollectorHandler{Silent: true},
					tc.compactRatio,
					tc.disableAutoCompact,
				)

				// Retrieve the configuration from the context
				config, ok := ctx.Value(llm.SubAgentConfig{}).(llm.SubAgentConfig)
				require.True(t, ok, "SubAgentConfig should be present in context")

				// Verify the compact configuration is correctly passed
				assert.Equal(t, tc.compactRatio, config.CompactRatio,
					"CompactRatio should match the provided value")
				assert.Equal(t, tc.disableAutoCompact, config.DisableAutoCompact,
					"DisableAutoCompact should match the provided value")

				// Verify the thread and handler are also correctly set
				assert.NotNil(t, config.Thread, "Thread should be set")
				assert.NotNil(t, config.MessageHandler, "MessageHandler should be set")
			})
		}
	})

	t.Run("WithSubAgent creates independent subagent", func(t *testing.T) {
		parentThread, err := NewOpenAIThread(llm.Config{}, nil)
		require.NoError(t, err)

		// Set up a basic state for the parent thread using NewBasicState
		parentThread.SetState(tools.NewBasicState(context.Background()))

		// Create subagent context
		ctx := parentThread.WithSubAgent(
			context.Background(),
			&llm.StringCollectorHandler{Silent: true},
			0.8,
			false,
		)

		// Retrieve the configuration
		config, ok := ctx.Value(llm.SubAgentConfig{}).(llm.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be present in context")

		// Verify the subagent thread is independent
		assert.NotSame(t, parentThread, config.Thread,
			"Subagent thread should be different from parent thread")

		// Verify the subagent has the correct configuration
		assert.Equal(t, "openai", config.Thread.Provider(),
			"Subagent should use the same provider as parent")
	})
}
