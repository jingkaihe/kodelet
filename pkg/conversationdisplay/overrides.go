// Package conversationdisplay manages user-facing display overrides for persisted conversations.
package conversationdisplay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const (
	MetadataKey = "message_display_overrides"
	VersionKey  = "v1"
	KindSlash   = "slash-command"
)

// Override describes a user-facing replacement for persisted model-facing text.
type Override struct {
	Display string `json:"display"`
	Kind    string `json:"kind,omitempty"`
	Command string `json:"command,omitempty"`
}

// KeyForText returns the metadata lookup key for a model-facing text message.
func KeyForText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AddSlashCommandOverride records a slash-command display override in metadata.
func AddSlashCommandOverride(metadata map[string]any, expandedText, display, command string) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if strings.TrimSpace(expandedText) == "" || strings.TrimSpace(display) == "" {
		return metadata
	}

	overrides := overridesMap(metadata)
	overrides[KeyForText(expandedText)] = Override{
		Display: strings.TrimSpace(display),
		Kind:    KindSlash,
		Command: strings.TrimSpace(command),
	}
	rawOverrides := make(map[string]any, len(overrides))
	for key, override := range overrides {
		rawOverrides[key] = override
	}
	metadata[MetadataKey] = map[string]any{VersionKey: rawOverrides}
	return metadata
}

// Lookup returns the display override for the provided model-facing text.
func Lookup(metadata map[string]any, text string) (Override, bool) {
	if strings.TrimSpace(text) == "" {
		return Override{}, false
	}
	overrides := parseOverrides(metadata)
	override, ok := overrides[KeyForText(text)]
	if !ok || strings.TrimSpace(override.Display) == "" {
		return Override{}, false
	}
	return override, true
}

// ApplyToStreamableMessages returns a copy of messages with display overrides applied to text messages.
func ApplyToStreamableMessages(messages []conversations.StreamableMessage, metadata map[string]any) []conversations.StreamableMessage {
	if len(messages) == 0 || len(metadata) == 0 {
		return messages
	}

	overrides := parseOverrides(metadata)
	if len(overrides) == 0 {
		return messages
	}

	result := make([]conversations.StreamableMessage, len(messages))
	copy(result, messages)
	for i := range result {
		if result[i].Kind != "text" || result[i].Role != "user" || strings.TrimSpace(result[i].Content) == "" {
			continue
		}
		if override, ok := overrides[KeyForText(result[i].Content)]; ok && strings.TrimSpace(override.Display) != "" {
			result[i].Content = override.Display
		}
	}
	return result
}

// ApplyToLLMMessages returns a copy of messages with display overrides applied.
func ApplyToLLMMessages(messages []llmtypes.Message, metadata map[string]any) []llmtypes.Message {
	if len(messages) == 0 || len(metadata) == 0 {
		return messages
	}

	overrides := parseOverrides(metadata)
	if len(overrides) == 0 {
		return messages
	}

	result := make([]llmtypes.Message, len(messages))
	copy(result, messages)
	for i := range result {
		if result[i].Role != "user" || strings.TrimSpace(result[i].Content) == "" {
			continue
		}
		if override, ok := overrides[KeyForText(result[i].Content)]; ok && strings.TrimSpace(override.Display) != "" {
			result[i].Content = override.Display
		}
	}
	return result
}

func overridesMap(metadata map[string]any) map[string]Override {
	existing := parseOverrides(metadata)
	if existing == nil {
		return map[string]Override{}
	}
	return existing
}

func parseOverrides(metadata map[string]any) map[string]Override {
	if len(metadata) == 0 {
		return nil
	}

	rawRoot, ok := metadata[MetadataKey]
	if !ok {
		return nil
	}

	root := mapValue(rawRoot)
	if root == nil {
		return nil
	}

	rawVersion, ok := root[VersionKey]
	if !ok {
		return nil
	}

	versionMap := mapValue(rawVersion)
	if versionMap == nil {
		return nil
	}

	overrides := make(map[string]Override, len(versionMap))
	for key, rawOverride := range versionMap {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if override, ok := parseOverride(rawOverride); ok {
			overrides[key] = override
		}
	}
	return overrides
}

func mapValue(raw any) map[string]any {
	switch value := raw.(type) {
	case map[string]any:
		return value
	case map[string]Override:
		mapped := make(map[string]any, len(value))
		for key, override := range value {
			mapped[key] = override
		}
		return mapped
	default:
		return nil
	}
}

func parseOverride(raw any) (Override, bool) {
	switch value := raw.(type) {
	case Override:
		return value, true
	case map[string]any:
		return Override{
			Display: stringValue(value["display"]),
			Kind:    stringValue(value["kind"]),
			Command: stringValue(value["command"]),
		}, true
	case map[string]string:
		return Override{
			Display: value["display"],
			Kind:    value["kind"],
			Command: value["command"],
		}, true
	default:
		return Override{}, false
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
