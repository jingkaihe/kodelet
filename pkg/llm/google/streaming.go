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

func (t *GoogleThread) processMessageExchange(ctx context.Context, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (*GoogleResponse, error) {
	config := &genai.GenerateContentConfig{
		Temperature:     genai.Ptr(float32(1.0)),
		MaxOutputTokens: int32(t.config.MaxTokens),
		Tools:          toGoogleTools(t.tools(opt)),
	}

	modelName := t.config.Model
	if opt.UseWeakModel && t.config.WeakModel != "" {
		modelName = t.config.WeakModel
	}

	if t.supportsThinking(modelName) && !opt.UseWeakModel {
		config.ThinkingConfig = &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  &t.thinkingBudget,
		}
	}

	prompt := t.buildPrompt()

	response := &GoogleResponse{}

	err := t.executeWithRetry(ctx, func() error {
		response = &GoogleResponse{}
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

			if chunk.UsageMetadata != nil {
				response.Usage = &genai.UsageMetadata{
					PromptTokenCount:        chunk.UsageMetadata.PromptTokenCount,
					ResponseTokenCount:      chunk.UsageMetadata.CandidatesTokenCount, // Different field name
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

	t.addAssistantMessage(response)

	return response, nil
}

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
		
		argsJSON, err := json.Marshal(toolCall.Args)
		if err != nil {
			return errors.Wrap(err, "failed to marshal tool arguments")
		}
		handler.HandleToolUse(toolCall.Name, string(argsJSON))

	case part.CodeExecutionResult != nil:
		result := fmt.Sprintf("Code execution result:\n%s", part.CodeExecutionResult.Output)
		if part.CodeExecutionResult.Outcome == genai.OutcomeUnspecified {
			result += "\nOutcome: Unspecified"
		}
		handler.HandleToolResult("code_execution", result)
		response.Text += result

	default:
		logger.G(context.Background()).Debug("Unhandled part type in Google response")
	}

	return nil
}

func (t *GoogleThread) buildPrompt() []*genai.Content {
	// TODO: system messages support
	return t.messages
}

func (t *GoogleThread) addAssistantMessage(response *GoogleResponse) {
	var parts []*genai.Part

	if response.ThinkingText != "" {
		parts = append(parts, &genai.Part{
			Text:    response.ThinkingText,
			Thought: true,
		})
	}

	if response.Text != "" {
		parts = append(parts, genai.NewPartFromText(response.Text))
	}

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

func (t *GoogleThread) supportsThinking(modelName string) bool {
	pricing, exists := ModelPricingMap[modelName]
	if !exists {
		for key, value := range ModelPricingMap {
			if strings.Contains(strings.ToLower(modelName), strings.ToLower(key)) {
				return value.HasThinking
			}
		}
		return false
	}
	return pricing.HasThinking
}

func (t *GoogleThread) processImage(imagePath string) (*genai.Part, error) {
	if strings.HasPrefix(imagePath, "http://") || strings.HasPrefix(imagePath, "https://") {
		return nil, errors.New("URL images not supported yet")
	}
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read image file: %s", imagePath)
	}

	if len(imageData) > MaxImageFileSize {
		return nil, errors.Errorf("image file %s is too large (%d bytes), maximum is %d bytes", 
			imagePath, len(imageData), MaxImageFileSize)
	}

	mimeType := getMimeTypeFromExtension(filepath.Ext(imagePath))
	if mimeType == "" {
		return nil, errors.Errorf("unsupported image format for file: %s", imagePath)
	}

	return genai.NewPartFromBytes(imageData, mimeType), nil
}

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
				
				if part.Thought {
					continue
				}

				messages = append(messages, llmtypes.Message{
					Role:    role,
					Content: part.Text,
				})

			case part.FunctionCall != nil:
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				messages = append(messages, llmtypes.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("🔧 Using tool: %s with input: %s", part.FunctionCall.Name, string(argsJSON)),
				})

			case part.FunctionResponse != nil:
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