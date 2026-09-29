// Package searchindex maintains the full-text search index of saved
// conversations. The index is a rebuildable projection: it is derived from
// saved records, never written on the conversation save path, and indexing
// failures never affect conversation persistence.
package searchindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

// Version identifies the rules that turn transcripts into search entries.
// Increment it whenever those rules change; the indexer then rebuilds every
// conversation in the background.
const Version = 1

const (
	maxTextPayloadBytes      = 16 * 1024
	maxToolInputPayloadBytes = 4 * 1024
)

// BuildDocument projects a conversation into search entries: a title entry
// with the name, summary, working directory, and ID; user and assistant text;
// tool call inputs; and compaction summaries. Thinking and tool results are
// left out as noise. When the transcript cannot be parsed, the document still
// holds the title entry and the parse error is returned alongside it.
func BuildDocument(record convtypes.ConversationRecord) (convtypes.SearchDocument, error) {
	document := convtypes.SearchDocument{
		ConversationID:  record.ID,
		SourceUpdatedAt: record.UpdatedAt,
		Version:         Version,
		Entries:         []convtypes.SearchEntry{titleEntry(record)},
	}
	// Tool results are not indexed, so skip rendering them.
	record.ToolResults = nil
	messages, err := llm.ExtractConversationRecordEntries(record)
	if err == nil {
		for index, message := range messages {
			if entry, ok := searchEntry(index, message); ok {
				document.Entries = append(document.Entries, entry)
			}
		}
	}
	document.ContentHash = contentHash(document.Entries)
	return document, errors.Wrap(err, "failed to extract the conversation transcript")
}

func titleEntry(record convtypes.ConversationRecord) convtypes.SearchEntry {
	var lines []string
	add := func(value string) {
		if value = conversations.NormalizeConversationName(value); value != "" && !slices.Contains(lines, value) {
			lines = append(lines, value)
		}
	}
	add(conversations.ResolveConversationName(record.Metadata, record.Summary))
	add(record.Summary)
	add(record.CWD)
	add(record.ID)
	return convtypes.SearchEntry{
		EntryIndex: -1,
		Kind:       convtypes.SearchEntryKindTitle,
		Payload:    truncateUTF8(strings.Join(lines, "\n"), maxTextPayloadBytes),
	}
}

func searchEntry(index int, message conversations.StreamableMessage) (convtypes.SearchEntry, bool) {
	entry := convtypes.SearchEntry{EntryIndex: index, Role: message.Role}
	limit := maxTextPayloadBytes
	switch message.Kind {
	case "text":
		entry.Kind = convtypes.SearchEntryKindText
		entry.Payload = message.Content
	case "tool-use":
		entry.Kind = convtypes.SearchEntryKindToolUse
		entry.Payload = message.ToolName + "\n" + flattenToolInput(message.Input)
		limit = maxToolInputPayloadBytes
	case "context-compacted":
		if message.Compaction == nil {
			return entry, false
		}
		entry.Kind = convtypes.SearchEntryKindCompaction
		entry.Payload = message.Compaction.Summary
	default:
		return entry, false
	}
	entry.Payload = truncateUTF8(strings.TrimSpace(entry.Payload), limit)
	return entry, entry.Payload != ""
}

// flattenToolInput turns JSON tool arguments into "key: value" lines, so the
// indexed text and snippets are free of JSON quoting and escapes.
func flattenToolInput(input string) string {
	input = strings.TrimSpace(input)
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return input
	}
	var lines []string
	appendToolInputLines(&lines, "", value)
	return strings.Join(lines, "\n")
}

func appendToolInputLines(lines *[]string, key string, value any) {
	switch typed := value.(type) {
	case nil:
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for name := range typed {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		for _, name := range keys {
			child := name
			if key != "" {
				child = key + "." + name
			}
			appendToolInputLines(lines, child, typed[name])
		}
	case []any:
		for _, item := range typed {
			appendToolInputLines(lines, key, item)
		}
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" {
			return
		}
		if key != "" {
			text = key + ": " + text
		}
		*lines = append(*lines, text)
	}
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func contentHash(entries []convtypes.SearchEntry) string {
	hash := sha256.New()
	for _, entry := range entries {
		fmt.Fprintf(hash, "%d\x00%s\x00%s\x00%d:%s\x00", entry.EntryIndex, entry.Role, entry.Kind, len(entry.Payload), entry.Payload)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
