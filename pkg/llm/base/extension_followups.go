package base

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ExtensionMessagesMetadataKey maps archive-spanning raw user ordinals to
// extension provenance. It shares the delegated_user_sources ordinal scheme.
const ExtensionMessagesMetadataKey = "extension_messages"

// FollowUpRevisionsMetadataKey stores per-extension/key revision watermarks.
// Decimal strings preserve the entire int64 range through JSON metadata loads.
const FollowUpRevisionsMetadataKey = "extension_follow_up_revisions"

// ExtensionMessageMetadata identifies the source and display policy of one
// exact history entry without changing its model-facing content.
type ExtensionMessageMetadata struct {
	ExtensionID string `json:"extensionId"`
	Visibility  string `json:"visibility"`
}

type extensionFollowUpContextKey struct{}

func followUpRevisions(value any) map[string]map[string]string {
	revisions := make(map[string]map[string]string)
	if raw, err := json.Marshal(value); err == nil && value != nil {
		_ = json.Unmarshal(raw, &revisions)
	}
	return revisions
}

// recordExtensionMessageLocked runs only after a provider appends a user item.
// Metadata maps are replaced, not mutated, so NoSave snapshots remain valid.
func (t *Thread) recordExtensionMessageLocked(ctx context.Context, ordinal string) {
	message, isExtension := ctx.Value(extensionFollowUpContextKey{}).(extensions.FollowUpMessage)
	if !isExtension && t.Metadata[ExtensionMessagesMetadataKey] == nil {
		return
	}
	entries := make(map[string]ExtensionMessageMetadata)
	if value := t.Metadata[ExtensionMessagesMetadataKey]; value != nil {
		if raw, err := json.Marshal(value); err == nil {
			_ = json.Unmarshal(raw, &entries)
		}
	}
	if isExtension {
		entries[ordinal] = ExtensionMessageMetadata{ExtensionID: message.ExtensionID, Visibility: message.Visibility}
		if message.Key != "" && message.Revision > 0 {
			revisions := followUpRevisions(t.Metadata[FollowUpRevisionsMetadataKey])
			if revisions[message.ExtensionID] == nil {
				revisions[message.ExtensionID] = make(map[string]string)
			}
			revisions[message.ExtensionID][message.Key] = strconv.FormatInt(message.Revision, 10)
			t.Metadata[FollowUpRevisionsMetadataKey] = revisions
		}
	} else {
		delete(entries, ordinal)
	}
	t.Metadata[ExtensionMessagesMetadataKey] = entries
}

// RestoreMessageMetadata rolls back provenance and deduplication with temporary
// history, without reverting unrelated metadata updated during the operation.
func (t *Thread) RestoreMessageMetadata(metadata map[string]any) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	for _, key := range []string{UserMessageSourcesMetadataKey, ExtensionMessagesMetadataKey, FollowUpRevisionsMetadataKey} {
		if value := metadata[key]; value != nil {
			if t.Metadata == nil {
				t.Metadata = make(map[string]any)
			}
			t.Metadata[key] = value
		} else {
			delete(t.Metadata, key)
		}
	}
}

func appendExtensionFollowUps(ctx context.Context, thread llmtypes.Thread, handler llmtypes.MessageHandler, messages []extensions.FollowUpMessage) bool {
	accepted, deduplicated := 0, 0
	for _, message := range messages {
		if ctx.Err() != nil {
			break
		}
		if message.ExtensionID == "" || !message.Valid() {
			continue
		}
		if message.Key != "" && message.Revision > 0 {
			revisions := followUpRevisions(thread.GetMetadata()[FollowUpRevisionsMetadataKey])
			previous, _ := strconv.ParseInt(revisions[message.ExtensionID][message.Key], 10, 64)
			if message.Revision <= previous {
				deduplicated++
				continue
			}
		}
		if message.Visibility == "" {
			message.Visibility = "visible"
		}
		messageCtx := context.WithValue(llmtypes.WithUserMessageSource(ctx, ""), extensionFollowUpContextKey{}, message)
		thread.AddUserMessage(messageCtx, message.Text)
		accepted++
		if message.Visibility != "hidden" && handler != nil {
			switch h := handler.(type) {
			case llmtypes.ExtensionUserMessageHandler:
				h.HandleExtensionUserMessage(message.Text, message.ExtensionID)
			case llmtypes.UserMessageHandler:
				h.HandleUserMessage("Extension: "+message.ExtensionID+"\n\n"+message.Text, nil)
			default:
				handler.HandleText(fmt.Sprintf("\n📨 Extension follow-up (%s): %s\n", message.ExtensionID, message.Text))
			}
		}
	}
	if accepted > 0 {
		logger.G(ctx).WithField("count", accepted).Info("accepted extension follow-ups")
	}
	if deduplicated > 0 {
		logger.G(ctx).WithField("count", deduplicated).Debug("deduplicated extension follow-ups")
	}
	return accepted > 0
}
