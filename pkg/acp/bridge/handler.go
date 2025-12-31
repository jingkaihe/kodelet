// Package bridge provides the bridge between kodelet's message handler
// system and the ACP session update protocol.
package bridge

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

var _ llmtypes.StreamingMessageHandler = (*ACPMessageHandler)(nil)

// TitleGenerator generates human-readable titles for tool calls
type TitleGenerator interface {
	GenerateTitle(toolName string, input string) string
}

// UpdateSender interface for sending session updates
type UpdateSender interface {
	SendUpdate(sessionID acptypes.SessionID, update any) error
}

// ACPMessageHandler bridges kodelet's MessageHandler to ACP session updates
type ACPMessageHandler struct {
	sender         UpdateSender
	sessionID      acptypes.SessionID
	titleGenerator TitleGenerator

	currentToolID   string
	currentToolName string
	toolMu          sync.Mutex
}

// HandlerOption is a functional option for configuring ACPMessageHandler
type HandlerOption func(*ACPMessageHandler)

// WithTitleGenerator sets a custom title generator
func WithTitleGenerator(tg TitleGenerator) HandlerOption {
	return func(h *ACPMessageHandler) {
		h.titleGenerator = tg
	}
}

// NewACPMessageHandler creates a new ACP message handler
func NewACPMessageHandler(sender UpdateSender, sessionID acptypes.SessionID, opts ...HandlerOption) *ACPMessageHandler {
	h := &ACPMessageHandler{
		sender:         sender,
		sessionID:      sessionID,
		titleGenerator: &DefaultTitleGenerator{},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
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

	title := h.titleGenerator.GenerateTitle(toolName, input)

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

// maxTitleLength is the maximum length of a generated title
const maxTitleLength = 80

// DefaultTitleGenerator generates titles using deterministic string formatting
type DefaultTitleGenerator struct{}

// GenerateTitle generates a human-readable title for a tool call
func (g *DefaultTitleGenerator) GenerateTitle(toolName string, input string) string {
	if input == "" {
		return toolName
	}

	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return toolName
	}

	var title string
	switch toolName {
	case "file_read", "file_write", "file_edit":
		if path, ok := params["file_path"].(string); ok {
			title = fmt.Sprintf("%s: %s", toolName, filepath.Base(path))
		}
	case "bash":
		if cmd, ok := params["command"].(string); ok {
			if len(cmd) > 50 {
				cmd = cmd[:50] + "..."
			}
			title = fmt.Sprintf("%s: %s", toolName, cmd)
		}
	case "grep_tool":
		if pattern, ok := params["pattern"].(string); ok {
			title = fmt.Sprintf("grep: %s", pattern)
		}
	case "glob_tool":
		if pattern, ok := params["pattern"].(string); ok {
			title = fmt.Sprintf("glob: %s", pattern)
		}
	case "web_fetch":
		if url, ok := params["url"].(string); ok {
			if len(url) > 50 {
				url = url[:50] + "..."
			}
			title = fmt.Sprintf("fetch: %s", url)
		}
	case "subagent":
		if question, ok := params["question"].(string); ok {
			if len(question) > 50 {
				question = question[:50] + "..."
			}
			title = fmt.Sprintf("subagent: %s", question)
		}
	case "image_recognition":
		if path, ok := params["image_path"].(string); ok {
			title = fmt.Sprintf("image: %s", filepath.Base(path))
		}
	}

	if title == "" {
		return toolName
	}

	if len(title) > maxTitleLength {
		title = title[:maxTitleLength-3] + "..."
	}

	return title
}
