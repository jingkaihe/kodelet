// Package google provides streaming response handling and message conversion
// for Google GenAI integration.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genai"
	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// processMessageExchange handles the core message exchange with Google GenAI
func (t *GoogleThread) processMessageExchange(ctx context.Context, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (*GoogleResponse, error) {
	// Build the generation config
	config := &genai.GenerateContentConfig{
		Temperature:     genai.Ptr(float32(1.0)), // Default temperature for Google GenAI
		MaxOutputTokens: int32(t.config.MaxTokens),
		Tools:          toGoogleTools(t.tools(opt), t.config),
	}

	// Get model name (weak model override)
	modelName := t.config.Model
	if opt.UseWeakModel && t.config.WeakModel != "" {
		modelName = t.config.WeakModel
	}

	// Enable thinking if supported and model supports it
	if t.supportsThinking(modelName) && !opt.UseWeakModel {
		config.ThinkingConfig = &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  &t.thinkingBudget,
		}
	}

	// Build the prompt from messages
	prompt := t.buildPrompt()

	response := &GoogleResponse{}

	// Execute streaming with retry logic
	err := t.executeWithRetry(ctx, func() error {
		// Reset response for retry attempts
		response = &GoogleResponse{}
		
		// Use streaming generation
		for chunk, err := range t.client.Models.GenerateContentStream(ctx, modelName, prompt, config) {
			if err != nil {
				return errors.Wrap(err, "streaming failed")
			}

			if len(chunk.Candidates) == 0 {
				continue
			}

			candidate := chunk.Candidates[0]
			if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
				continue
			}

			// Process each part in the response
			for _, part := range candidate.Content.Parts {
				if err := t.processPart(part, response, handler); err != nil {
					logger.G(ctx).WithError(err).Warn("Failed to process response part")
				}
			}

			// Update usage from chunk (the types are different but compatible)
			if chunk.UsageMetadata != nil {
				// Convert the usage metadata type
				response.Usage = &genai.UsageMetadata{
					PromptTokenCount:        chunk.UsageMetadata.PromptTokenCount,
					ResponseTokenCount:      chunk.UsageMetadata.CandidatesTokenCount, // Note: different field name
					CachedContentTokenCount: chunk.UsageMetadata.CachedContentTokenCount,
					TotalTokenCount:         chunk.UsageMetadata.TotalTokenCount,
				}
			}
		}
		return nil
	})
	
	if err != nil {
		return nil, err
	}

	// Add assistant message to history
	t.addAssistantMessage(response)

	return response, nil
}

// processPart processes individual parts of a Google GenAI response
func (t *GoogleThread) processPart(part *genai.Part, response *GoogleResponse, handler llmtypes.MessageHandler) error {
	switch {
	case part.Text != "":
		if part.Thought {
			handler.HandleThinking(part.Text)
			response.ThinkingText += part.Text
		} else {
			handler.HandleText(part.Text)
			response.Text += part.Text
		}

	case part.FunctionCall != nil:
		toolCall := &GoogleToolCall{
			ID:   generateToolCallID(),
			Name: part.FunctionCall.Name,
			Args: part.FunctionCall.Args,
		}
		response.ToolCalls = append(response.ToolCalls, toolCall)
		
		// Convert arguments to JSON for handler
		argsJSON, err := json.Marshal(toolCall.Args)
		if err != nil {
			return errors.Wrap(err, "failed to marshal tool arguments")
		}
		handler.HandleToolUse(toolCall.Name, string(argsJSON))

	case part.CodeExecutionResult != nil:
		// Handle Google's built-in code execution results
		result := fmt.Sprintf("Code execution result:\n%s", part.CodeExecutionResult.Output)
		if part.CodeExecutionResult.Outcome == genai.OutcomeUnspecified {
			result += "\nOutcome: Unspecified"
		}
		handler.HandleToolResult("code_execution", result)
		response.Text += result

	default:
		// Handle any other part types that might be added in the future
		logger.G(context.Background()).Debug("Unhandled part type in Google response")
	}

	return nil
}

// buildPrompt builds the prompt content from the current message history
func (t *GoogleThread) buildPrompt() []*genai.Content {
	// For now, return the current messages
	// In a full implementation, this might include system messages, etc.
	return t.messages
}

// addAssistantMessage adds the assistant's response to the message history
func (t *GoogleThread) addAssistantMessage(response *GoogleResponse) {
	var parts []*genai.Part

	// Add thinking content if present
	if response.ThinkingText != "" {
		parts = append(parts, &genai.Part{
			Text:    response.ThinkingText,
			Thought: true,
		})
	}

	// Add regular text content
	if response.Text != "" {
		parts = append(parts, genai.NewPartFromText(response.Text))
	}

	// Add tool calls
	for _, toolCall := range response.ToolCalls {
		parts = append(parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				Name: toolCall.Name,
				Args: toolCall.Args,
			},
		})
	}

	if len(parts) > 0 {
		content := genai.NewContentFromParts(parts, genai.RoleModel)
		t.messages = append(t.messages, content)
	}
}

// supportsThinking checks if the given model supports thinking capability
func (t *GoogleThread) supportsThinking(modelName string) bool {
	pricing, exists := ModelPricingMap[modelName]
	if !exists {
		// Try to find a match with different casing or partial match
		for key, value := range ModelPricingMap {
			if strings.Contains(strings.ToLower(modelName), strings.ToLower(key)) {
				return value.HasThinking
			}
		}
		return false
	}
	return pricing.HasThinking
}

// processImage processes an image path and returns a Google Part
func (t *GoogleThread) processImage(imagePath string) (*genai.Part, error) {
	// Check if it's a URL or file path
	if strings.HasPrefix(imagePath, "http://") || strings.HasPrefix(imagePath, "https://") {
		// For URLs, we would need to download the image first
		// For now, return an error for URLs
		return nil, errors.New("URL images not supported yet")
	}

	// Handle file path
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read image file: %s", imagePath)
	}

	// Check file size
	if len(imageData) > MaxImageFileSize {
		return nil, errors.Errorf("image file %s is too large (%d bytes), maximum is %d bytes", 
			imagePath, len(imageData), MaxImageFileSize)
	}

	// Determine MIME type from file extension
	mimeType := getMimeTypeFromExtension(filepath.Ext(imagePath))
	if mimeType == "" {
		return nil, errors.Errorf("unsupported image format for file: %s", imagePath)
	}

	// Create image part
	return genai.NewPartFromBytes(imageData, mimeType), nil
}

// getMimeTypeFromExtension returns the MIME type for common image extensions
func getMimeTypeFromExtension(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

// convertToStandardMessages converts Google Content format to standard Message format
func (t *GoogleThread) convertToStandardMessages() []llmtypes.Message {
	var messages []llmtypes.Message

	for _, content := range t.messages {
		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				role := "assistant"
				if content.Role == genai.RoleUser {
					role = "user"
				}
				
				// Skip thinking content in standard messages
				if part.Thought {
					continue
				}

				messages = append(messages, llmtypes.Message{
					Role:    role,
					Content: part.Text,
				})

			case part.FunctionCall != nil:
				// Convert tool calls to readable format
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				messages = append(messages, llmtypes.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("🔧 Using tool: %s with input: %s", part.FunctionCall.Name, string(argsJSON)),
				})

			case part.FunctionResponse != nil:
				// Convert tool results to readable format
				resultJSON, _ := json.Marshal(part.FunctionResponse.Response)
				messages = append(messages, llmtypes.Message{
					Role:    "user",
					Content: fmt.Sprintf("🔄 Tool result:\n%s", string(resultJSON)),
				})
			}
		}
	}

	return messages
}