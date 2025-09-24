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

func (t *GoogleThread) SaveConversation(ctx context.Context, summarise bool) error {
	if !t.isPersisted || t.store == nil {
		return nil
	}

	rawMessages, err := json.Marshal(t.messages)
	if err != nil {
		return errors.Wrap(err, "failed to marshal conversation messages")
	}

	summary := ""
	if summarise {
		summary = t.generateSummary(ctx)
	}

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

	return t.store.Save(ctx, record)
}

func (t *GoogleThread) LoadConversation(ctx context.Context, conversationID string) error {
	if t.store == nil {
		return errors.New("conversation store not initialized")
	}

	record, err := t.store.Load(ctx, conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to load conversation")
	}

	if err := json.Unmarshal(record.RawMessages, &t.messages); err != nil {
		return errors.Wrap(err, "failed to deserialize messages")
	}
	t.conversationID = record.ID
	t.usage = &record.Usage
	t.toolResults = record.ToolResults
	if t.toolResults == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	}

	t.state.SetFileLastAccess(record.FileLastAccess)

	t.restoreBackgroundProcesses(record.BackgroundProcesses)

	logger.G(ctx).WithField("conversation_id", conversationID).Info("Loaded conversation")
	return nil
}

func (t *GoogleThread) generateSummary(ctx context.Context) string {
	messages := t.convertToStandardMessages()
	if len(messages) == 0 {
		return ""
	}

	summaryPrompt := prompts.ShortSummaryPrompt + "\n\nConversation to summarize:"
	for _, msg := range messages {
		summaryPrompt += fmt.Sprintf("\n%s: %s", msg.Role, msg.Content)
	}

	weakModelConfig := t.config
	if t.config.WeakModel != "" {
		weakModelConfig.Model = t.config.WeakModel
	} else {
		weakModelConfig.Model = "gemini-2.5-flash"
	}

	summaryThread, err := NewGoogleThread(weakModelConfig, t.subagentContextFactory)
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create summary thread")
		return ""
	}

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

func (t *GoogleThread) processPendingFeedback(ctx context.Context) error {
	if t.conversationID == "" {
		return nil
	}

	feedbackStore, err := feedback.NewFeedbackStore()
	if err != nil {
		return errors.Wrap(err, "failed to create feedback store")
	}

	feedbackMessages, err := feedbackStore.ReadPendingFeedback(t.conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to read pending feedback")
	}

	if len(feedbackMessages) == 0 {
		return nil
	}

	logger.G(ctx).WithField("feedback_count", len(feedbackMessages)).Info("Processing pending feedback")

	for _, fb := range feedbackMessages {
		t.AddUserMessage(ctx, fb.Content)
	}

	return feedbackStore.ClearPendingFeedback(t.conversationID)
}

func (t *GoogleThread) CompactContext(ctx context.Context) error {
	logger.G(ctx).WithField("conversation_id", t.conversationID).Info("Starting context compaction")

	messages := t.convertToStandardMessages()
	if len(messages) <= 2 {
		return nil
	}

	compactPrompt := prompts.CompactPrompt + "\n\nConversation to compact:"
	for _, msg := range messages {
		compactPrompt += fmt.Sprintf("\n%s: %s", msg.Role, msg.Content)
	}

	weakModelConfig := t.config
	if t.config.WeakModel != "" {
		weakModelConfig.Model = t.config.WeakModel
	} else {
		weakModelConfig.Model = "gemini-2.5-flash"
	}

	compactThread, err := NewGoogleThread(weakModelConfig, t.subagentContextFactory)
	if err != nil {
		return errors.Wrap(err, "failed to create compact thread")
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = compactThread.SendMessage(ctx, compactPrompt, handler, llmtypes.MessageOpt{
		NoSaveConversation: true,
		UseWeakModel:       true,
	})

	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}

	compactSummary := handler.CollectedText()
	t.messages = []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromText(compactSummary),
		}, genai.RoleUser),
	}

	t.toolResults = make(map[string]tooltypes.StructuredToolResult)

	t.usage.CurrentContextWindow = 0

	logger.G(ctx).WithField("conversation_id", t.conversationID).Info("Context compaction completed")
	return nil
}

func ExtractMessages(rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	var googleMessages []*genai.Content
	if err := json.Unmarshal(rawMessages, &googleMessages); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal Google messages")
	}

	var messages []llmtypes.Message

	for _, content := range googleMessages {
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
				result := ""
				toolName := part.FunctionResponse.Name

				if structuredResult, ok := toolResults[toolName]; ok {
					if jsonData, err := structuredResult.MarshalJSON(); err == nil {
						result = string(jsonData)
					}
				} else {
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

func (t *GoogleThread) restoreBackgroundProcesses(processes []tooltypes.BackgroundProcess) {
	// TODO: Google provider background process restoration
	logger.G(context.Background()).WithField("process_count", len(processes)).Debug("Background processes restoration not implemented for Google provider")
}

func DeserializeMessages(rawMessages []byte) ([]*genai.Content, error) {
	var messages []*genai.Content
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize Google messages")
	}
	return messages, nil
}