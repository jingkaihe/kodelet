// Package bridge provides the bridge between kodelet's message handler
// system and the ACP session update protocol.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

var _ llmtypes.StreamingMessageHandler = (*ACPMessageHandler)(nil)

// UpdateSender interface for sending session updates
type UpdateSender interface {
	SendUpdate(sessionID acptypes.SessionID, update any) error
}

// ACPMessageHandler bridges kodelet's MessageHandler to ACP session updates
type ACPMessageHandler struct {
	sender    UpdateSender
	sessionID acptypes.SessionID

	currentToolID   string
	currentToolName string
	toolMu          sync.Mutex
}

// NewACPMessageHandler creates a new ACP message handler
func NewACPMessageHandler(sender UpdateSender, sessionID acptypes.SessionID) *ACPMessageHandler {
	return &ACPMessageHandler{
		sender:    sender,
		sessionID: sessionID,
	}
}

// HandleText sends complete text as agent_message_chunk
func (h *ACPMessageHandler) HandleText(text string) {
	h.sender.SendUpdate(h.sessionID, map[string]any{
		"sessionUpdate": acptypes.UpdateAgentMessageChunk,
		"content": map[string]any{
			"type": acptypes.ContentTypeText,
			"text": text,
		},
	})
}

// HandleTextDelta sends streaming text deltas
func (h *ACPMessageHandler) HandleTextDelta(delta string) {
	h.sender.SendUpdate(h.sessionID, map[string]any{
		"sessionUpdate": acptypes.UpdateAgentMessageChunk,
		"content": map[string]any{
			"type": acptypes.ContentTypeText,
			"text": delta,
		},
	})
}

// HandleToolUse creates a new tool_call update
func (h *ACPMessageHandler) HandleToolUse(toolCallID string, toolName string, input string) {
	h.toolMu.Lock()
	h.currentToolID = toolCallID
	h.currentToolName = toolName
	h.toolMu.Unlock()

	var rawInput json.RawMessage
	if input != "" {
		rawInput = json.RawMessage(input)
	}

	title := GenerateToolTitle(toolName, input)

	h.sender.SendUpdate(h.sessionID, map[string]any{
		"sessionUpdate": acptypes.UpdateToolCall,
		"toolCallId":    toolCallID,
		"title":         title,
		"kind":          ToACPToolKind(toolName),
		"status":        acptypes.ToolStatusPending,
		"rawInput":      rawInput,
	})

	h.sender.SendUpdate(h.sessionID, map[string]any{
		"sessionUpdate": acptypes.UpdateToolCallUpdate,
		"toolCallId":    toolCallID,
		"status":        acptypes.ToolStatusInProgress,
	})
}

// HandleToolResult sends tool_call_update with result
func (h *ACPMessageHandler) HandleToolResult(toolCallID string, _ string, result string) {
	status := acptypes.ToolStatusCompleted
	if strings.HasPrefix(result, "Error:") || strings.Contains(result, "error:") {
		status = acptypes.ToolStatusFailed
	}

	h.sender.SendUpdate(h.sessionID, map[string]any{
		"sessionUpdate": acptypes.UpdateToolCallUpdate,
		"toolCallId":    toolCallID,
		"status":        status,
		"content": []map[string]any{
			{
				"type": "content",
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": result,
				},
			},
		},
	})
}

// HandleThinking sends agent_thought_chunk
func (h *ACPMessageHandler) HandleThinking(thinking string) {
	h.sender.SendUpdate(h.sessionID, map[string]any{
		"sessionUpdate": acptypes.UpdateThoughtChunk,
		"content": map[string]any{
			"type": acptypes.ContentTypeText,
			"text": thinking,
		},
	})
}

// HandleThinkingStart is called when thinking starts
func (h *ACPMessageHandler) HandleThinkingStart() {
}

// HandleThinkingDelta sends streaming thinking chunks
func (h *ACPMessageHandler) HandleThinkingDelta(delta string) {
	h.sender.SendUpdate(h.sessionID, map[string]any{
		"sessionUpdate": acptypes.UpdateThoughtChunk,
		"content": map[string]any{
			"type": acptypes.ContentTypeText,
			"text": delta,
		},
	})
}

// HandleContentBlockEnd is called when a content block ends
func (h *ACPMessageHandler) HandleContentBlockEnd() {
}

// HandleDone is called when message processing is complete
func (h *ACPMessageHandler) HandleDone() {
}

// ToACPToolKind maps kodelet tool names to ACP tool kinds
func ToACPToolKind(toolName string) acptypes.ToolKind {
	switch toolName {
	case "file_read", "grep_tool", "glob_tool":
		return acptypes.ToolKindRead
	case "file_write", "file_edit":
		return acptypes.ToolKindEdit
	case "bash", "code_execution":
		return acptypes.ToolKindExecute
	case "web_fetch":
		return acptypes.ToolKindFetch
	case "thinking":
		return acptypes.ToolKindThink
	case "subagent":
		return acptypes.ToolKindSearch
	default:
		return acptypes.ToolKindOther
	}
}

// ContentBlocksToMessage converts ACP content blocks to a message string and image paths
func ContentBlocksToMessage(blocks []acptypes.ContentBlock) (string, []string) {
	var textParts []string
	var images []string

	for _, block := range blocks {
		switch block.Type {
		case acptypes.ContentTypeText:
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case acptypes.ContentTypeImage:
			if block.Data != "" {
				images = append(images, "data:"+block.MimeType+";base64,"+block.Data)
			} else if block.URI != "" {
				images = append(images, block.URI)
			}
		case acptypes.ContentTypeResource:
			if block.Resource != nil {
				if block.Resource.Text != "" {
					textParts = append(textParts, fmt.Sprintf("--- %s ---\n%s", block.Resource.URI, block.Resource.Text))
				}
			}
		case acptypes.ContentTypeResourceLink:
			if block.URI != "" {
				textParts = append(textParts, fmt.Sprintf("[Resource: %s]", block.URI))
			}
		}
	}

	return strings.Join(textParts, "\n\n"), images
}

// titleGenerationTimeout is the maximum time to wait for title generation
const titleGenerationTimeout = 5 * time.Second

// maxInputLength is the maximum length of input to send for summarization
const maxInputLength = 500

// GenerateToolTitle generates a human-readable title for a tool call.
// It uses kodelet to summarize the input, with a fallback to the tool name.
func GenerateToolTitle(toolName string, input string) string {
	if input == "" {
		return toolName
	}

	truncatedInput := input
	if len(input) > maxInputLength {
		truncatedInput = input[:maxInputLength] + "..."
	}

	ctx, cancel := context.WithTimeout(context.Background(), titleGenerationTimeout)
	defer cancel()

	prompt := fmt.Sprintf("Summarize this tool call in under 10 words. Tool: %s. Input: %s", toolName, truncatedInput)

	// get executable itself
	exe, err := os.Executable()
	if err != nil {
		return toolName
	}

	cmd := exec.CommandContext(ctx, exe,
		"run",
		"--no-save",
		"--use-weak-model",
		"--result-only",
		"--no-hooks",
		"--no-skills",
		"--no-mcp",
		prompt,
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return toolName
	}

	title := strings.TrimSpace(stdout.String())
	if title == "" {
		return toolName
	}

	if len(title) > 80 {
		title = title[:77] + "..."
	}

	return title
}
