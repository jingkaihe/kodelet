package base

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ExtensionMessagesMetadataKey maps archive-spanning raw user ordinals to
// extension provenance. It shares the delegated_user_sources ordinal scheme.
const ExtensionMessagesMetadataKey = convtypes.ExtensionMessagesMetadataKey

// FollowUpRevisionsMetadataKey stores per-extension/key revision watermarks.
// Decimal strings preserve the entire int64 range through JSON metadata loads.
const FollowUpRevisionsMetadataKey = convtypes.FollowUpRevisionsMetadataKey

// ExtensionMessageMetadata identifies the source and display policy of one
// exact history entry without changing its model-facing content.
type ExtensionMessageMetadata = convtypes.ExtensionMessageMetadata

type extensionFollowUpContextKey struct{}

// followUpRevisions returns a read-only view; callers must copy before writing.
func followUpRevisions(value any) map[string]map[string]string {
	if revisions, ok := value.(map[string]map[string]string); ok {
		return revisions
	}
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
	switch value := t.Metadata[ExtensionMessagesMetadataKey].(type) {
	case map[string]ExtensionMessageMetadata:
		maps.Copy(entries, value)
	default:
		if raw, err := json.Marshal(value); err == nil {
			_ = json.Unmarshal(raw, &entries)
		}
	}
	if entries == nil {
		entries = make(map[string]ExtensionMessageMetadata)
	}
	if isExtension {
		entries[ordinal] = ExtensionMessageMetadata{ExtensionID: message.ExtensionID, Visibility: message.Visibility}
		if message.Key != "" && message.Revision > 0 {
			revisions := maps.Clone(followUpRevisions(t.Metadata[FollowUpRevisionsMetadataKey]))
			if revisions == nil {
				revisions = make(map[string]map[string]string)
			}
			watermarks := maps.Clone(revisions[message.ExtensionID])
			if watermarks == nil {
				watermarks = make(map[string]string)
			}
			watermarks[message.Key] = strconv.FormatInt(message.Revision, 10)
			revisions[message.ExtensionID] = watermarks
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
