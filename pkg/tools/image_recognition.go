package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/shlex"
	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ImageRecognitionToolResult represents the result of an image recognition operation
type ImageRecognitionToolResult struct {
	imagePath string
	prompt    string
	result    string
	err       string
}

// GetResult returns the recognized text
func (r *ImageRecognitionToolResult) GetResult() string {
	return r.result
}

// GetError returns the error message
func (r *ImageRecognitionToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *ImageRecognitionToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *ImageRecognitionToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
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
	return `Analyze an image with a vision model.

# Input
- image_path: required image path (local file or HTTPS URL)
- prompt: required question/instruction for what to extract

# Rules
- Supported formats: .jpg, .jpeg, .png, .gif, .webp
- Max size: 5MB
- Remote images must use HTTPS
- Redirects are not followed
- Use absolute local paths (for example: file:///home/user/pictures/image.jpg)

# Use when
- You need details from a screenshot, diagram, photo, or mockup
- You need OCR-style extraction of visible text
- You need targeted visual analysis based on a question

# Do not use when
- The image content is already provided directly in the chat
`
}

// ValidateInput validates the input parameters for the tool.
// ValidateInput validates the input parameters for the tool
func (t *ImageRecognitionTool) ValidateInput(_ tooltypes.State, parameters string) error {
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
			return errors.Wrap(err, "invalid URL")
		}
		if parsedURL.Scheme != "https" {
			return errors.New("only HTTPS URLs are supported for security")
		}
	} else if filePath, ok := strings.CutPrefix(imagePath, "file://"); ok {
		// Validate local file path
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
		return errors.Errorf("image file not found: %s", filePath)
	}

	// Check file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	supportedFormats := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	if !slices.Contains(supportedFormats, ext) {
		return errors.Errorf("unsupported image format: %s (supported: %v)", ext, supportedFormats)
	}

	// Check file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return errors.Wrap(err, "failed to get file info")
	}
	if fileInfo.Size() > 5*1024*1024 { // 5MB limit
		return errors.Errorf("image file too large: %d bytes (max: 5MB)", fileInfo.Size())
	}

	return nil
}

// Execute executes the image_recognition tool using shell-out pattern.
// This spawns a subagent process via `kodelet run --as-subagent --image` for image analysis.
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
		if err := t.validateRemoteImage(ctx, input.ImagePath); err != nil {
			return &ImageRecognitionToolResult{
				imagePath: input.ImagePath,
				prompt:    input.Prompt,
				err:       fmt.Sprintf("Failed to validate remote image: %s", err),
			}
		}
	}

	// Get the current executable path
	exe, err := os.Executable()
	if err != nil {
		return &ImageRecognitionToolResult{
			imagePath: input.ImagePath,
			prompt:    input.Prompt,
			err:       fmt.Sprintf("Failed to get executable path: %s", err),
		}
	}

	// Create a prompt for image analysis
	analysisPrompt := fmt.Sprintf(`Examine the image and respond to the request below.

<request>
%s
</request>

Focus only on information relevant to the request:
- State observable facts, not assumptions
- Quote visible text, labels, or annotations exactly when relevant
- Describe layout and relationships between key elements
- Highlight technical details when applicable (UI components, architecture patterns, data flows)
- Clearly note anything unclear or ambiguous

Keep the response clear and actionable.`, input.Prompt)

	// Build command arguments - no tools needed for image analysis
	args := []string{"run", "--result-only", "--as-subagent", "--no-tools", "--image", input.ImagePath}

	// Add subagent args from config if available
	if llmConfig, ok := state.GetLLMConfig().(llmtypes.Config); ok && llmConfig.SubagentArgs != "" {
		parsedArgs, err := shlex.Split(llmConfig.SubagentArgs)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("failed to parse subagent_args, ignoring")
		} else {
			args = append(args, parsedArgs...)
		}
	}

	// Add the analysis prompt as the query
	args = append(args, analysisPrompt)

	// Execute the subagent
	cmd := exec.CommandContext(ctx, exe, args...)

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ImageRecognitionToolResult{
				imagePath: input.ImagePath,
				prompt:    input.Prompt,
				err:       fmt.Sprintf("Failed to analyze image: %s\nstderr: %s", err, string(exitErr.Stderr)),
			}
		}
		return &ImageRecognitionToolResult{
			imagePath: input.ImagePath,
			prompt:    input.Prompt,
			err:       fmt.Sprintf("Failed to analyze image: %s", err),
		}
	}

	return &ImageRecognitionToolResult{
		imagePath: input.ImagePath,
		prompt:    input.Prompt,
		result:    strings.TrimSpace(string(output)),
	}
}

// validateRemoteImage validates that a remote HTTPS image is accessible
func (t *ImageRecognitionTool) validateRemoteImage(ctx context.Context, imageURL string) error {
	// Create a simple HTTP HEAD request to check if the image is accessible
	// without downloading the full content
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// For security, don't follow redirects
			return errors.New("redirects are not allowed for security reasons")
		},
	}

	resp, err := client.Head(imageURL)
	if err != nil {
		return errors.Wrap(err, "failed to access image URL")
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Check content type to ensure it's an image
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		logger.G(ctx).Warnf("No Content-Type header found for image URL: %s", imageURL)
	} else if !strings.HasPrefix(contentType, "image/") {
		return errors.Errorf("URL does not point to an image (Content-Type: %s)", contentType)
	}

	// Check content length if available
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		// Parse content length and check size limit
		var size int64
		if _, err := fmt.Sscanf(contentLength, "%d", &size); err == nil {
			if size > 5*1024*1024 { // 5MB limit
				return errors.Errorf("image file too large: %d bytes (max: 5MB)", size)
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

// StructuredData returns structured metadata about the image recognition operation
func (r *ImageRecognitionToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "image_recognition",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Determine if image is local or remote
	imageType := "local"
	if strings.HasPrefix(r.imagePath, "http://") || strings.HasPrefix(r.imagePath, "https://") {
		imageType = "remote"
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.ImageRecognitionMetadata{
		ImagePath: r.imagePath,
		ImageType: imageType,
		Prompt:    r.prompt,
		Analysis:  r.result,
		// ImageSize would require additional processing to extract
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}
