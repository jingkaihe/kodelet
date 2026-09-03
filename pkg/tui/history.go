package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/pkg/errors"
)

func loadInitialHistory(ctx context.Context, conversationID, requestedCWD string) tea.Cmd {
	return loadConversationHistory(ctx, "", conversationID, requestedCWD)
}

func loadConversationHistory(ctx context.Context, conversationKey, conversationID, requestedCWD string) tea.Cmd {
	return loadConversationHistoryFromSource(ctx, conversationKey, conversationID, requestedCWD, nil)
}

func loadConversationHistoryFromSource(ctx context.Context, conversationKey, conversationID, requestedCWD string, source chat.ConversationSource) tea.Cmd {
	return func() tea.Msg {
		result := initialHistoryMsg{
			conversationKey: strings.TrimSpace(conversationKey),
			conversationID:  strings.TrimSpace(conversationID),
		}
		if strings.TrimSpace(conversationID) == "" {
			return result
		}
		if source != nil {
			history, err := source.LoadConversation(ctx, conversationID)
			if err != nil {
				result.err = errors.Wrap(err, "failed to load control-plane conversation")
				return result
			}
			result.loaded = true
			result.entries = entriesFromHistory(history.Messages)
			result.usage = history.Usage
			result.cwd = strings.TrimSpace(history.CWD)
			result.title = strings.TrimSpace(history.Title)
			result.updatedAt = history.UpdatedAt
			result.profile = strings.TrimSpace(history.Profile)
			result.provider = strings.TrimSpace(history.Provider)
			result.reasoningEffort = strings.TrimSpace(history.ReasoningEffort)
			return result
		}

		service, err := conversations.GetDefaultConversationService(ctx)
		if err != nil {
			result.err = errors.Wrap(err, "failed to open conversation store")
			return result
		}
		defer service.Close()

		response, err := service.GetConversation(ctx, conversationID)
		if err != nil {
			result.err = errors.Wrap(err, "failed to load conversation")
			return result
		}
		if strings.TrimSpace(requestedCWD) != "" && strings.TrimSpace(response.CWD) != "" {
			defaultCWD, err := chat.ResolveConfiguredDefaultCWD("")
			if err != nil {
				result.err = errors.Wrap(err, "failed to resolve requested cwd")
				return result
			}
			expandedCWD, err := chat.ExpandCWDInput(requestedCWD, defaultCWD)
			if err != nil {
				result.err = errors.Wrap(err, "failed to resolve requested cwd")
				return result
			}
			resolvedRequested, err := conversations.NormalizeCWD(expandedCWD)
			if err != nil {
				result.err = errors.Wrap(err, "failed to resolve requested cwd")
				return result
			}
			resolvedStored, err := conversations.NormalizeCWD(response.CWD)
			if err != nil {
				result.err = errors.Wrap(err, "failed to resolve conversation cwd")
				return result
			}
			if resolvedRequested != resolvedStored {
				result.err = errors.Wrapf(
					conversations.ErrCWDConflict,
					"conversation %s is bound to %s, not %s",
					conversationID,
					resolvedStored,
					resolvedRequested,
				)
				return result
			}
		}

		messages, err := llm.ExtractConversationEntries(response.Provider, response.RawMessages, response.Metadata, response.ToolResults)
		if err != nil {
			result.err = errors.Wrap(err, "failed to parse conversation")
			return result
		}

		provider := strings.TrimSpace(response.Provider)
		model := ""
		profile := profileFromMetadata(response.Metadata)
		reasoningEffort := ""
		if snapshot, ok, snapshotErr := conversations.ConfigSnapshotFromMetadata(response.Metadata); snapshotErr != nil {
			result.err = errors.Wrap(snapshotErr, "failed to load conversation config snapshot")
			return result
		} else if ok {
			provider = strings.TrimSpace(snapshot.Provider)
			model = strings.TrimSpace(snapshot.Model)
			profile = displayProfile(snapshot.Profile)
			reasoningEffort = snapshot.ReasoningEffort
		} else if config, configErr := chat.ResolveConfigForExistingConversation(response, ""); configErr == nil {
			provider = strings.TrimSpace(config.Provider)
			model = strings.TrimSpace(config.Model)
			profile = displayProfile(config.Profile)
			reasoningEffort = config.ReasoningEffort
		}

		fallbackTitle := strings.TrimSpace(response.Summary)
		if fallbackTitle == "" {
			fallbackTitle = shortID(response.ID)
		}
		result.loaded = true
		result.entries = entriesFromHistory(messages)
		result.usage = response.Usage
		result.cwd = strings.TrimSpace(response.CWD)
		result.title = conversations.ResolveConversationName(response.Metadata, fallbackTitle)
		result.updatedAt = response.UpdatedAt
		result.profile = profile
		result.provider = provider
		result.model = model
		result.reasoningEffort = reasoningEffort
		return result
	}
}

func entriesFromHistory(messages []conversations.StreamableMessage) []chatEntry {
	entries := make([]chatEntry, 0)
	toolIndex := map[string][2]int{}

	ensureAssistant := func() int {
		if len(entries) == 0 || entries[len(entries)-1].kind != entryAssistant {
			entries = append(entries, chatEntry{kind: entryAssistant})
		}
		return len(entries) - 1
	}

	for _, msg := range messages {
		switch msg.Kind {
		case "text":
			switch msg.Role {
			case "user":
				entries = append(entries, chatEntry{kind: entryUser, content: strings.TrimSpace(msg.Content)})
			case "assistant":
				idx := ensureAssistant()
				appendTextBlock(&entries[idx], msg.Content)
			}
		case "thinking":
			idx := ensureAssistant()
			blockIdx := appendThoughtBlock(&entries[idx])
			entries[idx].blocks[blockIdx].thoughts = append(entries[idx].blocks[blockIdx].thoughts, thoughtBlock{text: msg.Content, done: true})
		case "tool-use":
			idx := ensureAssistant()
			blockIdx := appendToolBlock(&entries[idx])
			toolIndex[msg.ToolCallID] = [2]int{idx, len(entries[idx].blocks[blockIdx].tools)}
			entries[idx].blocks[blockIdx].tools = append(entries[idx].blocks[blockIdx].tools, toolCall{id: msg.ToolCallID, name: msg.ToolName, input: msg.Input})
		case "tool-result":
			idx := ensureAssistant()
			structuredResult, hasStructuredResult := parseStructuredToolResult(msg.Content)
			resultText := msg.Content
			failed := false
			if hasStructuredResult {
				resultText = structuredToolResultText(structuredResult)
				failed = !structuredResult.Success
				if msg.ToolName == "" {
					msg.ToolName = structuredResult.ToolName
				}
			}
			if location, ok := toolIndex[msg.ToolCallID]; ok && location[0] < len(entries) {
				blockIdx, toolIdx := findToolLocation(entries[location[0]], msg.ToolCallID)
				if blockIdx >= 0 && toolIdx >= 0 {
					tool := &entries[location[0]].blocks[blockIdx].tools[toolIdx]
					tool.result = resultText
					tool.done = true
					tool.failed = failed
					if hasStructuredResult {
						tool.structured = structuredResult
						if tool.name == "" {
							tool.name = structuredResult.ToolName
						}
					}
				}
			} else {
				blockIdx := appendToolBlock(&entries[idx])
				entries[idx].blocks[blockIdx].tools = append(entries[idx].blocks[blockIdx].tools, toolCall{id: msg.ToolCallID, name: msg.ToolName, result: resultText, done: true, failed: failed, structured: structuredResult})
			}
		}
	}

	for i := range entries {
		entries[i].content = strings.TrimSpace(entries[i].content)
		trimEntryBlocks(&entries[i])
	}
	return entries
}

func appendTextBlock(entry *chatEntry, text string) {
	if text == "" {
		return
	}
	if len(entry.blocks) > 0 && entry.blocks[len(entry.blocks)-1].kind == blockText {
		last := len(entry.blocks) - 1
		entry.blocks[last].text += text
		entry.content += text
		return
	}
	entry.blocks = append(entry.blocks, assistantBlock{kind: blockText, text: text})
	entry.content += text
}

func appendThoughtBlock(entry *chatEntry) int {
	if len(entry.blocks) > 0 && entry.blocks[len(entry.blocks)-1].kind == blockThoughts {
		return len(entry.blocks) - 1
	}
	entry.blocks = append(entry.blocks, assistantBlock{kind: blockThoughts})
	return len(entry.blocks) - 1
}

func appendToolBlock(entry *chatEntry) int {
	if len(entry.blocks) > 0 && entry.blocks[len(entry.blocks)-1].kind == blockTools {
		return len(entry.blocks) - 1
	}
	entry.blocks = append(entry.blocks, assistantBlock{kind: blockTools})
	return len(entry.blocks) - 1
}

func findToolLocation(entry chatEntry, toolCallID string) (int, int) {
	for blockIdx, block := range entry.blocks {
		if block.kind != blockTools {
			continue
		}
		for toolIdx, tool := range block.tools {
			if tool.id == toolCallID {
				return blockIdx, toolIdx
			}
		}
	}
	return -1, -1
}

func trimEntryBlocks(entry *chatEntry) {
	for i := range entry.blocks {
		entry.blocks[i].text = strings.TrimSpace(entry.blocks[i].text)
	}
}
