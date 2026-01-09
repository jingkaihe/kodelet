package google

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/genai"

	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SaveConversation persists the current conversation state to the conversation store
func (t *Thread) SaveConversation(ctx context.Context, summarise bool) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
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

	var fileLastAccess map[string]time.Time
	var backgroundProcesses []tooltypes.BackgroundProcess

	if t.State != nil {
		fileLastAccess = t.State.FileLastAccess()
		backgroundProcesses = t.State.GetBackgroundProcesses()
	}

	record := convtypes.ConversationRecord{
		ID:                  t.ConversationID,
		RawMessages:         rawMessages,
		Provider:            "google",
		Usage:               *t.Usage,
		Metadata:            map[string]interface{}{"model": t.Config.Model, "backend": t.backend},
		Summary:             summary,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		FileLastAccess:      fileLastAccess,
		ToolResults:         t.GetStructuredToolResults(),
		BackgroundProcesses: backgroundProcesses,
	}

	return t.Store.Save(ctx, record)
}

// LoadConversationByID loads a conversation from the conversation store by ID.
// This is different from the loadConversation callback which loads the current conversation.
func (t *Thread) LoadConversationByID(ctx context.Context, conversationID string) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if t.Store == nil {
		return errors.New("conversation store not initialized")
	}

	record, err := t.Store.Load(ctx, conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to load conversation")
	}

	if err := json.Unmarshal(record.RawMessages, &t.messages); err != nil {
		return errors.Wrap(err, "failed to deserialize messages")
	}
	t.ConversationID = record.ID
	t.Usage = &record.Usage
	t.SetStructuredToolResults(record.ToolResults)

	if t.State != nil {
		t.State.SetFileLastAccess(record.FileLastAccess)
		t.restoreBackgroundProcesses(record.BackgroundProcesses)
	}

	logger.G(ctx).WithField("conversation_id", conversationID).Info("Loaded conversation")
	return nil
}

func (t *Thread) generateSummary(ctx context.Context) string {
	messages := t.convertToStandardMessages()
	if len(messages) == 0 {
		return ""
	}

	summaryPrompt := prompts.ShortSummaryPrompt + "\n\nConversation to summarize:"
	for _, msg := range messages {
		summaryPrompt += fmt.Sprintf("\n%s: %s", msg.Role, msg.Content)
	}

	weakModelConfig := t.Config
	if t.Config.WeakModel != "" {
		weakModelConfig.Model = t.Config.WeakModel
	} else {
		weakModelConfig.Model = "gemini-2.5-flash"
	}

	summaryThread, err := NewGoogleThread(weakModelConfig, t.SubagentContextFactory)
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create summary thread")
		return ""
	}

	// Set the state so tools are available
	summaryThread.SetState(t.State)

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

// ExtractMessages converts raw Google GenAI message bytes to standard message format
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

// DeserializeMessages deserializes raw message bytes into Google GenAI Content objects
func DeserializeMessages(rawMessages []byte) ([]*genai.Content, error) {
	var messages []*genai.Content
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize Google messages")
	}
	return messages, nil
}
