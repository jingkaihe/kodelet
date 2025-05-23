# ADR 011: Image Input Support for Kodelet

## Status
Proposed

## Context
Currently, Kodelet only supports text input through the CLI, limiting its ability to process visual content. With the increasing capabilities of modern LLMs in vision tasks, adding image input support would significantly expand Kodelet's use cases. Users should be able to provide images through local file paths or URLs, enabling tasks like:

- Code review from screenshots
- Architecture diagram analysis
- UI/UX feedback and mockup analysis
- Documentation with visual elements
- General computer vision tasks for development workflows

The Anthropic Claude models already support vision capabilities, making this a natural extension of Kodelet's functionality.

## Decision
We will implement image input support for Kodelet with the following specifications:

1. **CLI Interface**: Add `--image` flag that can be specified multiple times to accept a list of image inputs
2. **Input Formats**: Support both local file paths and HTTPS URLs only (for security)
3. **Provider Support**: Initially implement for Anthropic provider only (Claude models support vision)
4. **Supported Image Types**: JPEG, PNG, GIF, and WebP formats only
5. **Processing**: 
   - Local files will be base64 encoded and sent to the model
   - HTTPS URLs will be sent directly to the model as image references
5. **Architecture**: Extend the existing Thread interface and message handling system

## Architecture Details

### CLI Changes

#### Command Line Interface
Update the `run` command to accept image inputs:
```bash
# Single image from local file
kodelet run --image /path/to/screenshot.png "What's wrong with this UI?"

# Multiple images
kodelet run --image /path/to/diagram.png --image https://example.com/mockup.jpg "Compare these designs"

# Mixed local and remote images  
kodelet run --image /path/to/local.png --image https://remote.com/image.jpg "Analyze both"
```

#### Flag Implementation
```go
// RunOptions contains all options for the run command
type RunOptions struct {
    resumeConvID string
    noSave       bool
    images       []string  // New field for image paths/URLs
}
```

### Interface Changes

#### Thread Interface Extension
Extend the `Thread` interface in `pkg/types/llm/thread.go`:
```go
// Thread represents a conversation thread with an LLM
type Thread interface {
    // ... existing methods ...
    
    // AddUserMessageWithImages adds a user message with optional images to the thread
    AddUserMessageWithImages(message string, images []string)
}
```

#### Supported Image Types
The implementation will only support the following image formats for security and compatibility:
- JPEG (`image/jpeg`)
- PNG (`image/png`) 
- GIF (`image/gif`)
- WebP (`image/webp`)

Note: We don't need custom content types since we'll work directly with Anthropic's existing `MessageContentParam` types.

### Anthropic Implementation

#### Enhanced AddUserMessage
Update `pkg/llm/anthropic/anthropic.go` to support images:
```go
// AddUserMessage adds a user message to the thread (backward compatibility)
func (t *AnthropicThread) AddUserMessage(message string) {
    t.AddUserMessageWithImages(message, nil)
}

// AddUserMessageWithImages adds a user message with optional images to the thread
func (t *AnthropicThread) AddUserMessageWithImages(message string, images []string) {
    contentBlocks := []anthropic.MessageContentParam{
        anthropic.NewTextBlock(message),
    }
    
    // Process images and add them as content blocks
    for _, imagePath := range images {
        imageBlock, err := t.processImage(imagePath)
        if err != nil {
            logrus.Warnf("Failed to process image %s: %v", imagePath, err)
            continue
        }
        contentBlocks = append(contentBlocks, imageBlock)
    }
    
    t.messages = append(t.messages, anthropic.NewUserMessage(contentBlocks...))
}
```

#### Image Processing
Add image processing functionality:
```go
// processImage converts an image path/URL to an Anthropic image content block
func (t *AnthropicThread) processImage(imagePath string) (anthropic.MessageContentParam, error) {
    // Only allow HTTPS URLs for security
    if strings.HasPrefix(imagePath, "https://") {
        return t.processImageURL(imagePath)
    }
    
    // Treat everything else as a local file path
    return t.processImageFile(imagePath)
}

// processImageURL creates an image block from an HTTPS URL
func (t *AnthropicThread) processImageURL(url string) (anthropic.MessageContentParam, error) {
    // Validate URL format (HTTPS only)
    if !strings.HasPrefix(url, "https://") {
        return nil, fmt.Errorf("only HTTPS URLs are supported for security: %s", url)
    }
    
    return anthropic.NewImageBlock(anthropic.URLImageSourceParam{
        Type: "url",
        URL:  url,
    }), nil
}

// processImageFile creates an image block from a local file
func (t *AnthropicThread) processImageFile(filePath string) (anthropic.MessageContentParam, error) {
    // Check if file exists
    if _, err := os.Stat(filePath); os.IsNotExist(err) {
        return nil, fmt.Errorf("image file not found: %s", filePath)
    }
    
    // Determine media type from file extension first
    mediaType, err := getMediaTypeFromExtension(filepath.Ext(filePath))
    if err != nil {
        return nil, fmt.Errorf("unsupported image format: %s (supported: .jpg, .jpeg, .png, .gif, .webp)", filepath.Ext(filePath))
    }
    
    // Check file size
    fileInfo, err := os.Stat(filePath)
    if err != nil {
        return nil, fmt.Errorf("failed to get file info: %w", err)
    }
    if fileInfo.Size() > MaxImageFileSize {
        return nil, fmt.Errorf("image file too large: %d bytes (max: %d bytes)", fileInfo.Size(), MaxImageFileSize)
    }
    
    // Read and encode the file
    imageData, err := os.ReadFile(filePath)
    if err != nil {
        return nil, fmt.Errorf("failed to read image file: %w", err)
    }
    
    // Encode to base64
    base64Data := base64.StdEncoding.EncodeToString(imageData)
    
    return anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
        Type:      "base64",
        MediaType: mediaType,
        Data:      base64Data,
    }), nil
}

// getMediaTypeFromExtension returns the Anthropic media type for supported image formats only
func getMediaTypeFromExtension(ext string) (anthropic.Base64ImageSourceMediaType, error) {
    switch strings.ToLower(ext) {
    case ".jpg", ".jpeg":
        return anthropic.Base64ImageSourceMediaTypeImageJPEG, nil
    case ".png":
        return anthropic.Base64ImageSourceMediaTypeImagePNG, nil
    case ".gif":
        return anthropic.Base64ImageSourceMediaTypeImageGIF, nil
    case ".webp":
        return anthropic.Base64ImageSourceMediaTypeImageWebP, nil
    default:
        return "", fmt.Errorf("unsupported format")
    }
}
```

### CLI Integration

#### Update Run Command
Modify `cmd/kodelet/run.go` to handle image inputs:
```go
// RunOptions contains all options for the run command
type RunOptions struct {
    resumeConvID string
    noSave       bool
    images       []string  // New field for image inputs
}

// In the run command logic:
// Send the message with images
_, err = thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
    PromptCache: true,
    Images:      runOptions.images,  // Pass images to the message options
})
```

#### Flag Registration
```go
func init() {
    runCmd.Flags().StringVar(&runOptions.resumeConvID, "resume", "", "Resume a specific conversation")
    runCmd.Flags().BoolVar(&runOptions.noSave, "no-save", false, "Disable conversation persistence")
    runCmd.Flags().StringSliceVar(&runOptions.images, "image", []string{}, "Add image input (can be used multiple times)")
}
```

### OpenAI Noop Implementation

Add a no-operation implementation in `pkg/llm/openai/openai.go` for graceful fallback:
```go
// AddUserMessage adds a user message to the thread (backward compatibility)
func (t *OpenAIThread) AddUserMessage(message string) {
    t.AddUserMessageWithImages(message, nil)
}

// AddUserMessageWithImages adds a user message with optional images to the thread
// Note: OpenAI vision support is not yet implemented, images will be ignored
func (t *OpenAIThread) AddUserMessageWithImages(message string, images []string) {
    if len(images) > 0 {
        logrus.Warnf("Image input not yet supported for OpenAI provider, processing text only")
        // TODO: Implement OpenAI vision support in future versions
    }
    
    // Fall back to text-only processing
    t.messages = append(t.messages, openai.ChatCompletionMessage{
        Role:    openai.ChatMessageRoleUser,
        Content: message,
    })
}
```

### Message Options Extension

Update `pkg/types/llm/thread.go` to include images in message options:
```go
// MessageOpt represents options for sending messages
type MessageOpt struct {
    // ... existing fields ...
    Images []string // Image paths or URLs to include with the message
}
```

Update the `SendMessage` method to handle images:
```go
// SendMessage implementation would use AddUserMessageWithImages when images are provided
func (t *AnthropicThread) SendMessage(
    ctx context.Context,
    message string,
    handler llmtypes.MessageHandler,
    opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
    // ... existing tracing setup ...
    
    // Add user message with images if provided
    if len(opt.Images) > 0 {
        t.AddUserMessageWithImages(message, opt.Images)
    } else {
        t.AddUserMessage(message)
    }
    
    // ... rest of the existing implementation ...
}
```

## Configuration & Validation

### File Size Limits
Implement reasonable limits for image processing:
```go
const (
    MaxImageFileSize = 5 * 1024 * 1024 // 5MB limit
    MaxImageCount    = 10              // Maximum 10 images per message
)
```

### Error Handling
Provide clear error messages for common issues:
- File not found
- Unsupported image format  
- File too large
- Invalid URL format
- Network errors for remote images

### Security Considerations
- **HTTPS Only**: Only HTTPS URLs are supported for remote images (no HTTP)
- **Limited File Types**: Only support JPEG, PNG, GIF, and WebP formats
- **File Size Limits**: Implement size limits to prevent abuse (5MB max)
- **Path Validation**: Validate file paths to prevent directory traversal attacks
- **MIME Type Validation**: Validate file extensions match expected image formats

## Backwards Compatibility
The implementation maintains full backwards compatibility:
- Existing `AddUserMessage(string)` method remains unchanged
- All existing CLI commands continue to work
- Thread interface is extended, not modified
- Message handling for text-only content is unchanged

## Provider Limitations
- **Initial Implementation**: Anthropic Claude only
- **OpenAI Noop Implementation**: Implement a no-operation `AddUserMessageWithImages` method in `./pkg/llm/openai` package that logs a warning and falls back to text-only processing
- **Future Expansion**: OpenAI provider support can be added later when implementing similar patterns
- **Graceful Degradation**: If images are provided to a non-vision model, log warnings and continue with text-only

## Testing Strategy
1. **Unit Tests**: Test image processing functions with various formats and edge cases
2. **Integration Tests**: Test CLI flag parsing and Thread interface integration
3. **Error Handling Tests**: Test file not found, invalid formats, network errors
4. **End-to-End Tests**: Test complete workflow from CLI to LLM API

## Implementation Plan
1. **Phase 1**: Extend Thread interface with `AddUserMessageWithImages` method
2. **Phase 2**: Implement image processing utilities and validation
3. **Phase 3**: Update AnthropicThread to support images with full functionality
4. **Phase 4**: Implement noop `AddUserMessageWithImages` in OpenAI package for graceful fallback
5. **Phase 5**: Add CLI flags and integrate with run command
6. **Phase 6**: Add comprehensive tests and error handling
7. **Phase 7**: Update documentation and examples

## Consequences

### Positive
- Enables vision-based use cases for Kodelet
- Maintains clean architecture with multimodal content support
- Provides flexible input methods (local files and URLs)
- Backwards compatible with existing workflows

### Negative
- Increases complexity of message handling
- Adds file I/O and network dependencies
- Initial implementation limited to Anthropic provider
- Potential security considerations with file and URL handling

## Future Considerations
- **OpenAI Support**: Extend implementation to OpenAI models with vision capabilities
- **Additional Formats**: Support for other media types (audio, video) if LLM capabilities expand
- **Image Preprocessing**: Automatic resizing, format conversion, or optimization
- **Caching**: Cache processed images to avoid re-encoding for conversation continuations
- **Interactive Mode**: Support image inputs in the interactive chat mode