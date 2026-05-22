package conversations

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/goals"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const (
	MessageDisplayMetadataKey       = "message_display"
	legacyMessageDisplayMetadataKey = "message_display_overrides"
	MessageDisplayVersion           = "v1"
	MessageDisplayKindSlashCommand  = "slash-command"
	MessageDisplayKindGoal          = "goal"
)

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

// ApplyDisplayToStreamableMessages returns a copy of messages with display metadata applied to user text messages.
func ApplyDisplayToStreamableMessages(messages []StreamableMessage, metadata map[string]any) []StreamableMessage {
	if len(messages) == 0 {
		return messages
	}

	displays := messageDisplays(metadata)
	consumedDisplays := map[string]struct{}{}

	result := make([]StreamableMessage, len(messages))
	copy(result, messages)
	for i := range result {
		if result[i].Kind != "text" || result[i].Role != "user" || strings.TrimSpace(result[i].Content) == "" {
			continue
		}
		if goals.IsContextText(result[i].Content) {
			if display, ok := consumeDisplay(displays, consumedDisplays, result[i].Content); ok {
				result[i].Content = display.Text
				continue
			}
			result[i].Content = ""
			continue
		}
		if display, ok := displays[MessageDisplayKey(result[i].Content)]; ok && strings.TrimSpace(display.Text) != "" {
			result[i].Content = display.Text
			continue
		}
	}
	return result
}

// ApplyDisplayToLLMMessages returns a copy of messages with display metadata applied to user messages.
func ApplyDisplayToLLMMessages(messages []llmtypes.Message, metadata map[string]any) []llmtypes.Message {
	if len(messages) == 0 {
		return messages
	}

	displays := messageDisplays(metadata)
	consumedDisplays := map[string]struct{}{}

	result := make([]llmtypes.Message, len(messages))
	copy(result, messages)
	for i := range result {
		if result[i].Role != "user" || strings.TrimSpace(result[i].Content) == "" {
			continue
		}
		if goals.IsContextText(result[i].Content) {
			if display, ok := consumeDisplay(displays, consumedDisplays, result[i].Content); ok {
				result[i].Content = display.Text
				continue
			}
			result[i].Content = ""
			continue
		}
		if display, ok := displays[MessageDisplayKey(result[i].Content)]; ok && strings.TrimSpace(display.Text) != "" {
			result[i].Content = display.Text
			continue
		}
	}
	return result
}

func consumeDisplay(displays map[string]MessageDisplay, consumed map[string]struct{}, text string) (MessageDisplay, bool) {
	if len(displays) == 0 || strings.TrimSpace(text) == "" {
		return MessageDisplay{}, false
	}
	key := MessageDisplayKey(text)
	if _, ok := consumed[key]; ok {
		return MessageDisplay{}, false
	}
	display, ok := displays[key]
	if !ok || strings.TrimSpace(display.Text) == "" {
		return MessageDisplay{}, false
	}
	consumed[key] = struct{}{}
	return display, true
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
