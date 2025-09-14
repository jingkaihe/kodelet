package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// MessageParser is a function type that can parse provider-specific raw messages into streamable format
type MessageParser func(rawMessages json.RawMessage, toolResults map[string]tools.StructuredToolResult) ([]StreamableMessage, error)

// StreamEntry represents a single stream entry in the unified JSON format
type StreamEntry struct {
	Kind       string  `json:"kind"`                   // "text", "tool-use", "tool-result", "thinking"
	Content    *string `json:"content,omitempty"`      // Text content for text and thinking
	ToolName   *string `json:"tool_name,omitempty"`    // Tool name for tool-use and tool-result
	Input      *string `json:"input,omitempty"`        // JSON input for tool-use
	Result     *string `json:"result,omitempty"`       // Tool execution result for tool-result
	Role       string  `json:"role"`                   // "user", "assistant", "system"
	ToolCallID *string `json:"tool_call_id,omitempty"` // For matching tool calls to results
}

// StreamableMessage contains parsed message data for streaming
type StreamableMessage struct {
	Kind       string // "text", "tool-use", "tool-result", "thinking"
	Role       string // "user", "assistant", "system"
	Content    string // Text content
	ToolName   string // For tool use/result
	ToolCallID string // For matching tool results
	Input      string // For tool use (JSON string)
}

// ConversationStreamer handles streaming conversation data in structured JSON format
type ConversationStreamer struct {
	service        ConversationServiceInterface
	messageParsers map[string]MessageParser // Map provider name to parser function
}

// NewConversationStreamer creates a new conversation streamer
func NewConversationStreamer(service ConversationServiceInterface) *ConversationStreamer {
	return &ConversationStreamer{
		service:        service,
		messageParsers: make(map[string]MessageParser),
	}
}

// RegisterMessageParser registers a message parser for a specific provider
func (cs *ConversationStreamer) RegisterMessageParser(provider string, parser MessageParser) {
	cs.messageParsers[provider] = parser
}

// StreamHistoricalData streams all existing conversation data
func (cs *ConversationStreamer) StreamHistoricalData(ctx context.Context, conversationID string) error {
	logger.G(ctx).WithField("conversationID", conversationID).Debug("Streaming historical conversation data")

	response, err := cs.service.GetConversation(ctx, conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to get conversation")
	}

	parser, exists := cs.messageParsers[response.Provider]
	if !exists {
		return errors.Errorf("no message parser registered for provider: %s", response.Provider)
	}

	streamableMessages, err := parser(response.RawMessages, response.ToolResults)
	if err != nil {
		return errors.Wrap(err, "failed to parse messages")
	}

	for _, msg := range streamableMessages {
		entry := cs.convertToStreamEntry(msg)
		if err := cs.outputStreamEntry(entry); err != nil {
			return errors.Wrap(err, "failed to output stream entry")
		}
	}

	logger.G(ctx).WithField("messageCount", len(streamableMessages)).Debug("Completed streaming historical data")
	return nil
}

// StreamLiveUpdates watches for conversation updates and streams new entries
func (cs *ConversationStreamer) StreamLiveUpdates(ctx context.Context, conversationID string, interval time.Duration) error {
	logger.G(ctx).WithField("conversationID", conversationID).WithField("interval", interval).Debug("Starting live stream for conversation")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastUpdateTime time.Time
	var streamedEntries int // Track how many entries we've already streamed

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			response, err := cs.service.GetConversation(ctx, conversationID)
			if err != nil {
				continue
			}

			if response.UpdatedAt.After(lastUpdateTime) {
				newlyStreamed, err := cs.streamNewMessagesSince(ctx, response, streamedEntries)
				if err != nil {
					logger.G(ctx).WithError(err).Error("Failed to stream new messages")
					continue
				}

				if newlyStreamed > 0 {
					streamedEntries += newlyStreamed
					lastUpdateTime = response.UpdatedAt
				}
			}
		}
	}
}

// streamNewMessagesSince streams only the new messages since the last streamed count
func (cs *ConversationStreamer) streamNewMessagesSince(ctx context.Context, response *GetConversationResponse, alreadyStreamed int) (int, error) {
	parser, exists := cs.messageParsers[response.Provider]
	if !exists {
		return 0, errors.Errorf("no message parser registered for provider: %s", response.Provider)
	}

	streamableMessages, err := parser(response.RawMessages, response.ToolResults)
	if err != nil {
		return 0, errors.Wrap(err, "failed to parse messages")
	}

	newlyStreamed := 0
	if len(streamableMessages) > alreadyStreamed {
		newMessages := streamableMessages[alreadyStreamed:]
		for _, msg := range newMessages {
			entry := cs.convertToStreamEntry(msg)
			if err := cs.outputStreamEntry(entry); err != nil {
				return newlyStreamed, errors.Wrap(err, "failed to output stream entry")
			}
			newlyStreamed++
		}
	}

	return newlyStreamed, nil
}

// convertToStreamEntry converts a StreamableMessage to a StreamEntry
func (cs *ConversationStreamer) convertToStreamEntry(msg StreamableMessage) StreamEntry {
	entry := StreamEntry{
		Kind: msg.Kind,
		Role: msg.Role,
	}

	switch msg.Kind {
	case "text", "thinking":
		entry.Content = &msg.Content
	case "tool-use":
		entry.ToolName = &msg.ToolName
		entry.Input = &msg.Input
		if msg.ToolCallID != "" {
			entry.ToolCallID = &msg.ToolCallID
		}
	case "tool-result":
		if msg.ToolName != "" {
			entry.ToolName = &msg.ToolName
		}
		entry.Result = &msg.Content
		if msg.ToolCallID != "" {
			entry.ToolCallID = &msg.ToolCallID
		}
	}

	return entry
}

// outputStreamEntry outputs a stream entry as JSON to stdout
func (cs *ConversationStreamer) outputStreamEntry(entry StreamEntry) error {
	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		return errors.Wrap(err, "failed to marshal stream entry")
	}

	fmt.Fprintf(os.Stdout, "%s\n", string(jsonBytes))
	return nil
}
