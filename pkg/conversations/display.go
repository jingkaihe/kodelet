package conversations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

const (
	ConversationNameMetadataKey      = "conversation_name"
	ConversationAutoNameMetadataKey  = "conversation_auto_name"
	MessageDisplayMetadataKey        = "message_display"
	legacyMessageDisplayMetadataKey  = "message_display_overrides"
	MessageDisplayVersion            = "v1"
	MessageDisplayKindSlashCommand   = "slash-command"
	MessageDisplayKindResourceReload = "resources-reloaded"
	// MessageDisplayKindResourceReloadRefused marks a reload that left the
	// previous environment in place.
	MessageDisplayKindResourceReloadRefused = "resource-reload-refused"
)

// UserMessageProvenance is host-owned attribution, never inferred from message text.
type UserMessageProvenance struct {
	ExtensionID     string `json:"extensionId"`
	Visibility      string `json:"visibility"`
	DelegatedSource string `json:"-"`
}

// ProjectHistorySegment converts display history while omitting hidden extension
// input. userOrdinal spans archived segments and the visible current tail, and
// counts every raw user item, including tool feedback omitted by renderers.
// Raw history and provider conversion semantics remain unchanged; only attributed
// user items are isolated so their converted entries can receive a trusted source.
func ProjectHistorySegment[T any](
	raw json.RawMessage, metadata map[string]any, userOrdinal *int,
	convert func(json.RawMessage) ([]T, error), attribute func(*T, UserMessageProvenance),
) ([]T, error) {
	if metadata["extension_messages"] == nil && metadata["delegated_user_sources"] == nil {
		return convert(raw)
	}
	var extensions map[string]UserMessageProvenance
	if value := metadata["extension_messages"]; value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, errors.Wrap(err, "failed to encode extension message provenance")
		}
		if err := json.Unmarshal(encoded, &extensions); err != nil {
			return nil, errors.Wrap(err, "failed to decode extension message provenance")
		}
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	var messages []T
	var batch []json.RawMessage
	flush := func(source UserMessageProvenance) error {
		if len(batch) == 0 {
			return nil
		}
		encoded, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		entries, err := convert(encoded)
		if err != nil {
			return err
		}
		if source.ExtensionID != "" || source.DelegatedSource != "" {
			for i := range entries {
				attribute(&entries[i], source)
			}
		}
		messages = append(messages, entries...)
		batch = nil
		return nil
	}
	for _, item := range items {
		var header struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(item, &header); err != nil {
			return nil, err
		}
		var source UserMessageProvenance
		if header.Role == "user" {
			key := strconv.Itoa(*userOrdinal)
			source = extensions[key]
			switch sources := metadata["delegated_user_sources"].(type) {
			case map[string]string:
				source.DelegatedSource = sources[key]
			case map[string]any:
				source.DelegatedSource, _ = sources[key].(string)
			}
			*userOrdinal++
		}
		if source.ExtensionID == "" && source.DelegatedSource == "" {
			batch = append(batch, item)
			continue
		}
		if err := flush(UserMessageProvenance{}); err != nil {
			return nil, err
		}
		if source.ExtensionID != "" && source.Visibility == "hidden" {
			continue
		}
		batch = append(batch, item)
		if err := flush(source); err != nil {
			return nil, err
		}
	}
	if err := flush(UserMessageProvenance{}); err != nil {
		return nil, err
	}
	return messages, nil
}

// IsResourceReloadDisplayKind reports a host marker for a resource reload outcome.
func IsResourceReloadDisplayKind(kind string) bool {
	return kind == MessageDisplayKindResourceReload || kind == MessageDisplayKindResourceReloadRefused
}

type (
	conversationNameOverrideContextKey struct{}
	conversationForkNameContextKey     struct{}
)

// NormalizeConversationName converts a conversation name into a single-line label.
func NormalizeConversationName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// ContextWithConversationForkName attaches an explicit name requested for a new fork.
func ContextWithConversationForkName(ctx context.Context, name string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	name = NormalizeConversationName(name)
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, conversationForkNameContextKey{}, name)
}

// ConversationForkNameFromContext returns the explicit name requested for a new fork.
func ConversationForkNameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(conversationForkNameContextKey{}).(string)
	return NormalizeConversationName(name)
}

// SetConversationName records an explicit user-facing conversation name in metadata.
func SetConversationName(metadata map[string]any, name string) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if name = NormalizeConversationName(name); name != "" {
		metadata[ConversationNameMetadataKey] = name
	}
	return metadata
}

// ExplicitConversationName returns the user-provided conversation name, if present.
func ExplicitConversationName(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	name, _ := metadata[ConversationNameMetadataKey].(string)
	return NormalizeConversationName(name)
}

// SetAutomaticConversationName records the deterministic first-message name.
func SetAutomaticConversationName(metadata map[string]any, name string) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if name = NormalizeConversationName(name); name != "" {
		metadata[ConversationAutoNameMetadataKey] = name
	}
	return metadata
}

// AutomaticConversationName returns the persisted first-message name, if present.
func AutomaticConversationName(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	name, _ := metadata[ConversationAutoNameMetadataKey].(string)
	return NormalizeConversationName(name)
}

// ResolveConversationName prefers an explicit name, then the persisted automatic name, over the deterministic fallback.
func ResolveConversationName(metadata map[string]any, fallback string) string {
	if name := ExplicitConversationName(metadata); name != "" {
		return name
	}
	if name := AutomaticConversationName(metadata); name != "" {
		return name
	}
	return NormalizeConversationName(fallback)
}

// EnsureConversationName persists the deterministic fallback the first time it is available.
func EnsureConversationName(metadata map[string]any, fallback string) (map[string]any, string) {
	if ExplicitConversationName(metadata) == "" && AutomaticConversationName(metadata) == "" {
		metadata = SetAutomaticConversationName(metadata, fallback)
	}
	return metadata, ResolveConversationName(metadata, fallback)
}

// PreserveStoredConversationName keeps a persisted explicit name from being overwritten by stale thread metadata.
func PreserveStoredConversationName(ctx context.Context, store ConversationStore, conversationID string, metadata map[string]any) map[string]any {
	if store == nil || strings.TrimSpace(conversationID) == "" {
		return metadata
	}

	record, err := store.Load(ctx, conversationID)
	if err != nil {
		return metadata
	}
	storedName := ExplicitConversationName(record.Metadata)
	if storedName == "" || preferCurrentConversationName(ctx) {
		return metadata
	}
	return SetConversationName(metadata, storedName)
}

func preferCurrentConversationName(ctx context.Context) bool {
	preferCurrent, _ := ctx.Value(conversationNameOverrideContextKey{}).(bool)
	return preferCurrent
}

// RenameThread persists an explicit name on an active conversation thread.
func RenameThread(ctx context.Context, thread llmtypes.Thread, name string) (string, error) {
	if thread == nil {
		return "", errors.New("conversation thread is unavailable")
	}
	if !thread.IsPersisted() {
		return "", errors.New("conversation persistence is disabled")
	}
	name = NormalizeConversationName(name)
	if name == "" {
		return "", errors.New("conversation name must not be empty")
	}

	thread.SetMetadataValue(ConversationNameMetadataKey, name)
	ctx = context.WithValue(ctx, conversationNameOverrideContextKey{}, true)
	if err := thread.SaveConversation(ctx); err != nil {
		return "", errors.Wrap(err, "failed to save renamed conversation")
	}
	return name, nil
}

// MessageDisplay describes user-facing text for a model-facing message.
type MessageDisplay struct {
	Text    string `json:"text"`
	Kind    string `json:"kind,omitempty"`
	Command string `json:"command,omitempty"`
}

// MessageDisplayKey returns the metadata lookup key for a model-facing text message.
func MessageDisplayKey(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AddSlashCommandDisplay records compact slash-command display text in metadata.
func AddSlashCommandDisplay(metadata map[string]any, modelText, displayText, command string) map[string]any {
	return AddMessageDisplay(metadata, modelText, displayText, MessageDisplayKindSlashCommand, command)
}

// AddMessageDisplay records user-facing display text in metadata.
func AddMessageDisplay(metadata map[string]any, modelText, displayText, kind, command string) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if strings.TrimSpace(modelText) == "" || strings.TrimSpace(displayText) == "" {
		return metadata
	}

	displays := messageDisplays(metadata)
	if displays == nil {
		displays = map[string]MessageDisplay{}
	}
	displays[MessageDisplayKey(modelText)] = MessageDisplay{
		Text:    strings.TrimSpace(displayText),
		Kind:    strings.TrimSpace(kind),
		Command: strings.TrimSpace(command),
	}
	metadata[MessageDisplayMetadataKey] = map[string]any{MessageDisplayVersion: rawMessageDisplays(displays)}
	delete(metadata, legacyMessageDisplayMetadataKey)
	return metadata
}

// LookupMessageDisplay returns user-facing display text for a model-facing message.
func LookupMessageDisplay(metadata map[string]any, modelText string) (MessageDisplay, bool) {
	if strings.TrimSpace(modelText) == "" {
		return MessageDisplay{}, false
	}
	display, ok := messageDisplays(metadata)[MessageDisplayKey(modelText)]
	if !ok || strings.TrimSpace(display.Text) == "" {
		return MessageDisplay{}, false
	}
	return display, true
}

// ApplyDisplayToStreamableMessages projects user display overrides and host markers without changing model history.
func ApplyDisplayToStreamableMessages(messages []StreamableMessage, metadata map[string]any) []StreamableMessage {
	if len(messages) == 0 {
		return messages
	}

	displays := messageDisplays(metadata)

	result := make([]StreamableMessage, 0, len(messages))
	for _, message := range messages {
		if message.Kind != "text" || strings.TrimSpace(message.Content) == "" {
			result = append(result, message)
			continue
		}
		if display, ok := displays[MessageDisplayKey(message.Content)]; ok && strings.TrimSpace(display.Text) != "" {
			if IsResourceReloadDisplayKind(display.Kind) {
				message.ResourceReloadID = MessageDisplayKey(message.Content)
				message.Kind = display.Kind
				message.Role = "assistant"
				message.Content = display.Text
				message.RawItem = nil
			} else if message.Role == "user" {
				message.Content = display.Text
			}
		}
		result = append(result, message)
	}
	return result
}

// ApplyDisplayToLLMMessages projects display text and host markers for plain-text conversation readers.
func ApplyDisplayToLLMMessages(messages []llmtypes.Message, metadata map[string]any) []llmtypes.Message {
	if len(messages) == 0 {
		return messages
	}

	displays := messageDisplays(metadata)

	result := make([]llmtypes.Message, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.Content) == "" {
			result = append(result, message)
			continue
		}
		if display, ok := displays[MessageDisplayKey(message.Content)]; ok && strings.TrimSpace(display.Text) != "" {
			if IsResourceReloadDisplayKind(display.Kind) {
				message.Role = "assistant"
				message.Content = display.Text
			} else if message.Role == "user" {
				message.Content = display.Text
			}
		}
		result = append(result, message)
	}
	return result
}

func rawMessageDisplays(displays map[string]MessageDisplay) map[string]any {
	raw := make(map[string]any, len(displays))
	for key, display := range displays {
		raw[key] = display
	}
	return raw
}

func messageDisplays(metadata map[string]any) map[string]MessageDisplay {
	if len(metadata) == 0 {
		return nil
	}

	displays := parseMessageDisplays(metadata[legacyMessageDisplayMetadataKey])
	currentDisplays := parseMessageDisplays(metadata[MessageDisplayMetadataKey])
	if len(currentDisplays) == 0 {
		return displays
	}
	if displays == nil {
		return currentDisplays
	}
	for key, display := range currentDisplays {
		displays[key] = display
	}
	return displays
}

func parseMessageDisplays(rawRoot any) map[string]MessageDisplay {
	root := mapValue(rawRoot)
	if root == nil {
		return nil
	}

	rawVersion, ok := root[MessageDisplayVersion]
	if !ok {
		return nil
	}

	versionMap := mapValue(rawVersion)
	if versionMap == nil {
		return nil
	}

	displays := make(map[string]MessageDisplay, len(versionMap))
	for key, rawDisplay := range versionMap {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if display, ok := parseMessageDisplay(rawDisplay); ok {
			displays[key] = display
		}
	}
	return displays
}

func mapValue(raw any) map[string]any {
	switch value := raw.(type) {
	case map[string]any:
		return value
	case map[string]MessageDisplay:
		mapped := make(map[string]any, len(value))
		for key, display := range value {
			mapped[key] = display
		}
		return mapped
	default:
		return nil
	}
}

func parseMessageDisplay(raw any) (MessageDisplay, bool) {
	switch value := raw.(type) {
	case MessageDisplay:
		return value, true
	case map[string]any:
		return MessageDisplay{
			Text:    firstStringValue(value, "text", "display"),
			Kind:    stringValue(value["kind"]),
			Command: stringValue(value["command"]),
		}, true
	case map[string]string:
		text := value["text"]
		if text == "" {
			text = value["display"]
		}
		return MessageDisplay{
			Text:    text,
			Kind:    value["kind"],
			Command: value["command"],
		}, true
	default:
		return MessageDisplay{}, false
	}
}

func firstStringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
