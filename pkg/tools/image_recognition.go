package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type ImageRecognitionToolResult struct {
	imagePath string
	prompt    string
	result    string
	err       string
}

func (r *ImageRecognitionToolResult) GetResult() string {
	return r.result
}

func (r *ImageRecognitionToolResult) GetError() string {
	return r.err
}

func (r *ImageRecognitionToolResult) IsError() bool {
	return r.err != ""
}

func (r *ImageRecognitionToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
}

func (r *ImageRecognitionToolResult) UserFacing() string {
	if r.IsError() {
		return r.GetError()
	}
	return fmt.Sprintf("Image Recognition: %s\nPrompt: %s\n%s", r.imagePath, r.prompt, r.result)
}

// ImageRecognitionTool implements the image_recognition tool for processing and understanding images.
type ImageRecognitionTool struct{}

// ImageRecognitionInput defines the input parameters for the image_recognition tool.
type ImageRecognitionInput struct {
	ImagePath string `json:"image_path" jsonschema:"description=The path to the image to be recognized. It can be a local file 'file:///path/to/image.jpg' or a remote file 'https://example.com/image.jpg'."`
	Prompt    string `json:"prompt" jsonschema:"description=The information you want to extract from the image."`
}

// Name returns the name of the tool.
func (t *ImageRecognitionTool) Name() string {
	return "image_recognition"
}

// GenerateSchema generates the JSON schema for the tool's input parameters.
func (t *ImageRecognitionTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[ImageRecognitionInput]()
}

// Description returns the description of the tool.
func (t *ImageRecognitionTool) Description() string {
	return `Process and understand images using vision-enabled AI models.

## Input
- image_path: The path to the image to be recognized. It can be a local file 'file:///path/to/image.jpg' or a remote file 'https://example.com/image.jpg'.
- prompt: The information you want to extract from the image.

## Output
The output summarizes the information extracted from the image.

## Common Use Cases
* You simply want to understand what is in the image.
* You are conducting system design and you need to understand the architecture from a diagram.
* Analyzing screenshots, mockups, or other visual content.
* Extracting text or data from images.

## DO NOT use this tool when
!!!VERY IMPORTANT!!! Do not use this tool when image content has already been shared with you directly through the messages

## Important Notes
1. Only .jpg, .jpeg, .png, .gif, .webp formats are supported.
2. The image must be less than 5MB in size.
3. For security reasons, only HTTPS URLs are supported for remote images.
4. No URL redirects are followed for security.
5. File path must be an absolute path to avoid ambiguity. e.g. "file:///home/user/pictures/image.jpg" instead of "./pictures/image.jpg"
`
}

// ValidateInput validates the input parameters for the tool.
func (t *ImageRecognitionTool) ValidateInput(state tooltypes.State, parameters string) error {
	input := &ImageRecognitionInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.ImagePath == "" {
		return errors.New("image_path is required")
	}

	if input.Prompt == "" {
		return errors.New("prompt is required")
	}

	// Validate image path format
	if err := t.validateImagePath(input.ImagePath); err != nil {
		return err
	}

	return nil
}

// validateImagePath validates the image path format and accessibility
func (t *ImageRecognitionTool) validateImagePath(imagePath string) error {
	if strings.HasPrefix(imagePath, "http://") {
		return errors.New("only HTTPS URLs are supported for security")
	} else if strings.HasPrefix(imagePath, "https://") {
		// Validate HTTPS URL format
		parsedURL, err := url.Parse(imagePath)
		if err != nil {
			return fmt.Errorf("invalid URL: %w", err)
		}
		if parsedURL.Scheme != "https" {
			return errors.New("only HTTPS URLs are supported for security")
		}
	} else if strings.HasPrefix(imagePath, "file://") {
		// Validate local file path
		filePath := strings.TrimPrefix(imagePath, "file://")
		return t.validateLocalImageFile(filePath)
	} else {
		// Treat as local file path
		return t.validateLocalImageFile(imagePath)
	}
	return nil
}

// validateLocalImageFile validates local image file existence and format
func (t *ImageRecognitionTool) validateLocalImageFile(filePath string) error {
	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("image file not found: %s", filePath)
	}

	// Check file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	supportedFormats := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	isSupported := false
	for _, format := range supportedFormats {
		if ext == format {
			isSupported = true
			break
		}
	}
	if !isSupported {
		return fmt.Errorf("unsupported image format: %s (supported: %v)", ext, supportedFormats)
	}

	// Check file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	if fileInfo.Size() > 5*1024*1024 { // 5MB limit
		return fmt.Errorf("image file too large: %d bytes (max: 5MB)", fileInfo.Size())
	}

	return nil
}

// Execute executes the image_recognition tool.
func (t *ImageRecognitionTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &ImageRecognitionInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &ImageRecognitionToolResult{
			imagePath: input.ImagePath,
			prompt:    input.Prompt,
			err:       err.Error(),
		}
	}

	// Validate remote URL if it's an HTTPS URL
	if strings.HasPrefix(input.ImagePath, "https://") {
		if err := t.validateRemoteImage(input.ImagePath); err != nil {
			return &ImageRecognitionToolResult{
				imagePath: input.ImagePath,
				prompt:    input.Prompt,
				err:       fmt.Sprintf("Failed to validate remote image: %s", err),
			}
		}
	}

	// Get sub-agent config from context for LLM interaction
	subAgentConfig, ok := ctx.Value(llm.SubAgentConfig{}).(llm.SubAgentConfig)
	if !ok {
		return &ImageRecognitionToolResult{
			imagePath: input.ImagePath,
			prompt:    input.Prompt,
			err:       "sub-agent config not found in context",
		}
	}

	// Create a prompt for image analysis
	analysisPrompt := fmt.Sprintf(`Please analyze the provided image and %s

Here are the details of what I need:
%s

Please provide a clear and detailed response based on what you can see in the image.`,
		input.Prompt, input.Prompt)

	// Prepare image paths for the LLM
	imagePaths := []string{input.ImagePath}

	// Use the LLM to analyze the image
	analysisResult, err := subAgentConfig.Thread.SendMessage(ctx,
		analysisPrompt,
		&llm.ConsoleMessageHandler{
			Silent: true,
		},
		llm.MessageOpt{
			PromptCache:        true,
			Images:             imagePaths,
			NoSaveConversation: true,
		},
	)

	if err != nil {
		return &ImageRecognitionToolResult{
			imagePath: input.ImagePath,
			prompt:    input.Prompt,
			err:       fmt.Sprintf("Failed to analyze image: %s", err),
		}
	}

	return &ImageRecognitionToolResult{
		imagePath: input.ImagePath,
		prompt:    input.Prompt,
		result:    analysisResult,
	}
}

// validateRemoteImage validates that a remote HTTPS image is accessible
func (t *ImageRecognitionTool) validateRemoteImage(imageURL string) error {
	// Create a simple HTTP HEAD request to check if the image is accessible
	// without downloading the full content
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// For security, don't follow redirects
			return fmt.Errorf("redirects are not allowed for security reasons")
		},
	}

	resp, err := client.Head(imageURL)
	if err != nil {
		return fmt.Errorf("failed to access image URL: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Check content type to ensure it's an image
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		logrus.Warnf("No Content-Type header found for image URL: %s", imageURL)
	} else if !strings.HasPrefix(contentType, "image/") {
		return fmt.Errorf("URL does not point to an image (Content-Type: %s)", contentType)
	}

	// Check content length if available
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		// Parse content length and check size limit
		var size int64
		if _, err := fmt.Sscanf(contentLength, "%d", &size); err == nil {
			if size > 5*1024*1024 { // 5MB limit
				return fmt.Errorf("image file too large: %d bytes (max: 5MB)", size)
			}
		}
	}

	return nil
}

// TracingKVs returns tracing key-value pairs for observability.
func (t *ImageRecognitionTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &ImageRecognitionInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	attrs := []attribute.KeyValue{
		attribute.String("image_path", input.ImagePath),
		attribute.Int("prompt_length", len(input.Prompt)),
	}

	// Add image type information
	if strings.HasPrefix(input.ImagePath, "https://") {
		attrs = append(attrs, attribute.String("image_type", "remote_url"))
	} else {
		attrs = append(attrs, attribute.String("image_type", "local_file"))
		if filepath.Ext(input.ImagePath) != "" {
			attrs = append(attrs, attribute.String("image_format", strings.ToLower(filepath.Ext(input.ImagePath))))
		}
	}

	return attrs, nil
}
