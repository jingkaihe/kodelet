// Package google provides conversation persistence and management
// for Google GenAI integration.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/genai"
	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/feedback"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SaveConversation saves the current thread to the conversation store
func (t *GoogleThread) SaveConversation(ctx context.Context, summarise bool) error {
	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Marshall the messages to JSON
	rawMessages, err := json.Marshal(t.messages)
	if err != nil {
		return errors.Wrap(err, "failed to marshal conversation messages")
	}

	summary := ""
	if summarise {
		// Generate summary for the conversation
		summary = t.generateSummary(ctx)
	}

	// Create a new conversation record
	record := convtypes.ConversationRecord{
		ID:                  t.conversationID,
		RawMessages:         rawMessages,
		Provider:            "google",
		Usage:               *t.usage,
		Metadata:            map[string]interface{}{"model": t.config.Model, "backend": t.backend},
		Summary:             summary,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		FileLastAccess:      t.state.FileLastAccess(),
		ToolResults:         t.GetStructuredToolResults(),
		BackgroundProcesses: t.state.GetBackgroundProcesses(),
	}

	// Save to store
	return t.store.Save(ctx, record)
}

// LoadConversation loads a conversation from the store
func (t *GoogleThread) LoadConversation(ctx context.Context, conversationID string) error {
	if t.store == nil {
		return errors.New("conversation store not initialized")
	}

	record, err := t.store.Load(ctx, conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to load conversation")
	}

	// Deserialize messages
	if err := json.Unmarshal(record.RawMessages, &t.messages); err != nil {
		return errors.Wrap(err, "failed to deserialize messages")
	}

	// Restore state
	t.conversationID = record.ID
	t.usage = &record.Usage
	t.toolResults = record.ToolResults
	if t.toolResults == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	}

	// Restore file access times
	t.state.SetFileLastAccess(record.FileLastAccess)

	// Restore background processes
	t.restoreBackgroundProcesses(record.BackgroundProcesses)

	logger.G(ctx).WithField("conversation_id", conversationID).Info("Loaded conversation")
	return nil
}

// generateSummary generates a summary of the conversation using a weak model
func (t *GoogleThread) generateSummary(ctx context.Context) string {
	// Convert current messages to standard format for summary generation
	messages := t.convertToStandardMessages()
	if len(messages) == 0 {
		return ""
	}

	// Create a summary request
	summaryPrompt := prompts.ShortSummaryPrompt + "\n\nConversation to summarize:"
	for _, msg := range messages {
		summaryPrompt += fmt.Sprintf("\n%s: %s", msg.Role, msg.Content)
	}

	// Use weak model for summary generation
	weakModelConfig := t.config
	if t.config.WeakModel != "" {
		weakModelConfig.Model = t.config.WeakModel
	} else {
		weakModelConfig.Model = "gemini-2.5-flash" // Default weak model
	}

	// Create a temporary thread for summary generation
	summaryThread, err := NewGoogleThread(weakModelConfig, t.subagentContextFactory)
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create summary thread")
		return ""
	}

	// Generate summary
	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = summaryThread.SendMessage(ctx, summaryPrompt, handler, llmtypes.MessageOpt{
		NoSaveConversation: true,
		UseWeakModel:       true,
	})

	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to generate summary")
		return ""
	}

	return handler.CollectedText()
}

// processPendingFeedback processes any pending feedback for this conversation
func (t *GoogleThread) processPendingFeedback(ctx context.Context) error {
	if t.conversationID == "" {
		return nil
	}

	// Create feedback store
	feedbackStore, err := feedback.NewFeedbackStore()
	if err != nil {
		return errors.Wrap(err, "failed to create feedback store")
	}

	// Get pending feedback messages
	feedbackMessages, err := feedbackStore.ReadPendingFeedback(t.conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to read pending feedback")
	}

	if len(feedbackMessages) == 0 {
		return nil
	}

	logger.G(ctx).WithField("feedback_count", len(feedbackMessages)).Info("Processing pending feedback")

	// Add feedback messages to the conversation
	for _, fb := range feedbackMessages {
		t.AddUserMessage(ctx, fb.Content)
	}

	// Clear processed feedback
	return feedbackStore.ClearPendingFeedback(t.conversationID)
}

// CompactContext performs context compaction when the context window is getting full
func (t *GoogleThread) CompactContext(ctx context.Context) error {
	logger.G(ctx).WithField("conversation_id", t.conversationID).Info("Starting context compaction")

	// Convert current messages to standard format
	messages := t.convertToStandardMessages()
	if len(messages) <= 2 {
		// Not enough messages to compact
		return nil
	}

	// Generate a compact summary of the conversation so far
	compactPrompt := prompts.CompactPrompt + "\n\nConversation to compact:"
	for _, msg := range messages {
		compactPrompt += fmt.Sprintf("\n%s: %s", msg.Role, msg.Content)
	}

	// Use weak model for compaction
	weakModelConfig := t.config
	if t.config.WeakModel != "" {
		weakModelConfig.Model = t.config.WeakModel
	} else {
		weakModelConfig.Model = "gemini-2.5-flash"
	}

	// Create a temporary thread for compaction
	compactThread, err := NewGoogleThread(weakModelConfig, t.subagentContextFactory)
	if err != nil {
		return errors.Wrap(err, "failed to create compact thread")
	}

	// Generate compact summary
	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = compactThread.SendMessage(ctx, compactPrompt, handler, llmtypes.MessageOpt{
		NoSaveConversation: true,
		UseWeakModel:       true,
	})

	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}

	compactSummary := handler.CollectedText()

	// Replace message history with the compact summary
	t.messages = []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromText(compactSummary),
		}, genai.RoleUser),
	}

	// Clear stale tool results (keep only recent ones if any)
	t.toolResults = make(map[string]tooltypes.StructuredToolResult)

	// Reset context window tracking
	t.usage.CurrentContextWindow = 0

	logger.G(ctx).WithField("conversation_id", t.conversationID).Info("Context compaction completed")
	return nil
}

// ExtractMessages parses the raw messages from a conversation record for Google provider
func ExtractMessages(rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	var googleMessages []*genai.Content
	if err := json.Unmarshal(rawMessages, &googleMessages); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal Google messages")
	}

	var messages []llmtypes.Message

	// Convert Google Content format to standard Message format
	for _, content := range googleMessages {
		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				role := "assistant"
				if content.Role == genai.RoleUser {
					role = "user"
				}

				// Skip thinking content in extracted messages
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
				result := ""
				toolName := part.FunctionResponse.Name

				// Try to use structured result if available
				if structuredResult, ok := toolResults[toolName]; ok {
					if jsonData, err := structuredResult.MarshalJSON(); err == nil {
						result = string(jsonData)
					}
				} else {
					// Fallback to raw response
					if responseJSON, err := json.Marshal(part.FunctionResponse.Response); err == nil {
						result = string(responseJSON)
					}
				}

				messages = append(messages, llmtypes.Message{
					Role:    "user",
					Content: fmt.Sprintf("🔄 Tool result:\n%s", result),
				})
			}
		}
	}

	return messages, nil
}

// restoreBackgroundProcesses restores background processes from the conversation record
func (t *GoogleThread) restoreBackgroundProcesses(processes []tooltypes.BackgroundProcess) {
	// Note: Google provider currently doesn't have specific background process restoration
	// This is a placeholder for future implementation if needed
	logger.G(context.Background()).WithField("process_count", len(processes)).Debug("Background processes restoration not implemented for Google provider")
}

// DeserializeMessages deserializes Google messages from JSON
func DeserializeMessages(rawMessages []byte) ([]*genai.Content, error) {
	var messages []*genai.Content
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize Google messages")
	}
	return messages, nil
}