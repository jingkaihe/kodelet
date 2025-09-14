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

// StreamOpts contains options for streaming conversation data
type StreamOpts struct {
	Interval       time.Duration
	IncludeHistory bool
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

// streamState holds the current streaming state
type streamState struct {
	lastUpdateTime  time.Time
	streamedEntries int
}

// StreamLiveUpdates watches for conversation updates and streams entries based on options
func (cs *ConversationStreamer) StreamLiveUpdates(ctx context.Context, conversationID string, streamOpts StreamOpts) error {
	logger.G(ctx).WithField("conversationID", conversationID).WithField("interval", streamOpts.Interval).WithField("includeHistory", streamOpts.IncludeHistory).Debug("Starting stream for conversation")

	ticker := time.NewTicker(streamOpts.Interval)
	defer ticker.Stop()

	state, err := cs.initializeStream(ctx, conversationID, streamOpts.IncludeHistory)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			cs.processLiveUpdate(ctx, conversationID, state)
		}
	}
}

// initializeStream sets up the initial streaming state and optionally streams history
func (cs *ConversationStreamer) initializeStream(ctx context.Context, conversationID string, includeHistory bool) (*streamState, error) {
	response, err := cs.service.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get conversation")
	}

	parser, exists := cs.messageParsers[response.Provider]
	if !exists {
		return nil, errors.Errorf("no message parser registered for provider: %s", response.Provider)
	}

	messages, err := parser(response.RawMessages, response.ToolResults)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse messages")
	}

	state := &streamState{
		lastUpdateTime:  response.UpdatedAt,
		streamedEntries: len(messages),
	}

	if includeHistory {
		for _, msg := range messages {
			entry := cs.convertToStreamEntry(msg)
			if err := cs.outputStreamEntry(entry); err != nil {
				return nil, errors.Wrap(err, "failed to output stream entry")
			}
		}
		logger.G(ctx).WithField("messageCount", len(messages)).Debug("Streamed historical messages")
	} else {
		logger.G(ctx).WithField("skippedMessages", len(messages)).Debug("Initialized for live-only streaming")
	}

	return state, nil
}

// processLiveUpdate checks for new messages and streams them
func (cs *ConversationStreamer) processLiveUpdate(ctx context.Context, conversationID string, state *streamState) {
	response, err := cs.service.GetConversation(ctx, conversationID)
	if err != nil {
		return // Continue on error, as per original logic
	}

	if !response.UpdatedAt.After(state.lastUpdateTime) {
		return
	}

	newlyStreamed, err := cs.streamNewMessagesSince(ctx, response, state.streamedEntries)
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to stream new messages")
		return
	}

	if newlyStreamed > 0 {
		state.streamedEntries += newlyStreamed
		state.lastUpdateTime = response.UpdatedAt
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
		logger.G(ctx).WithField("newMessageCount", len(newMessages)).Debug("Streaming new messages")
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
