package webui

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/mcp"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// ChatRequest is the payload for a streamed web chat turn.
type ChatRequest struct {
	Message        string             `json:"message"`
	Content        []ChatContentBlock `json:"content,omitempty"`
	ConversationID string             `json:"conversationId,omitempty"`
}

// ChatContentBlock represents a multimodal user input block from the web UI.
type ChatContentBlock struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	Source   *ChatImageSource    `json:"source,omitempty"`
	ImageURL *ChatImageURLSource `json:"image_url,omitempty"`
}

// ChatImageSource represents embedded image data.
type ChatImageSource struct {
	Data      string `json:"data"`
	MediaType string `json:"media_type"`
}

// ChatImageURLSource represents URL-based image input.
type ChatImageURLSource struct {
	URL string `json:"url"`
}

// ChatEvent is a single NDJSON event sent to the React chat client.
type ChatEvent struct {
	Kind           string                          `json:"kind"`
	ConversationID string                          `json:"conversation_id,omitempty"`
	Role           string                          `json:"role,omitempty"`
	Delta          string                          `json:"delta,omitempty"`
	Content        string                          `json:"content,omitempty"`
	ToolName       string                          `json:"tool_name,omitempty"`
	ToolCallID     string                          `json:"tool_call_id,omitempty"`
	Input          string                          `json:"input,omitempty"`
	ToolResult     *tooltypes.StructuredToolResult `json:"tool_result,omitempty"`
	Error          string                          `json:"error,omitempty"`
}

// ChatEventSink receives streamed chat events.
type ChatEventSink interface {
	Send(ChatEvent) error
}

// ChatRunner executes a single persisted chat turn for the web UI.
type ChatRunner interface {
	Run(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error)
}

// DefaultChatRunner executes chat turns using the same LLM/tool stack as the CLI.
type DefaultChatRunner struct{}

// NewDefaultChatRunner creates a chat runner for the web UI server.
func NewDefaultChatRunner() *DefaultChatRunner {
	return &DefaultChatRunner{}
}

// Run executes a single persisted chat turn and streams events to the sink.
func (r *DefaultChatRunner) Run(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
	message, imageInputs, err := normalizeChatRequest(req)
	if err != nil {
		return "", err
	}

	if message == "" && len(imageInputs) == 0 {
		return "", errors.New("message cannot be empty")
	}

	llmConfig, err := llm.GetConfigFromViper()
	if err != nil {
		return "", errors.Wrap(err, "failed to load configuration")
	}

	workspaceDir, err := mcp.ResolveWorkspaceDir("")
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve MCP workspace directory")
	}
	llmConfig.MCPExecutionMode = viper.GetString("mcp.execution_mode")
	llmConfig.MCPWorkspaceDir = workspaceDir

	customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to initialize custom tools")
	}

	var mcpManager *tools.MCPManager
	mcpManager, err = tools.CreateMCPManagerFromViper(ctx)
	if err != nil && !stdErrors.Is(err, tools.ErrMCPDisabled) {
		return "", errors.Wrap(err, "failed to initialize MCP manager")
	}
	if mcpManager != nil {
		defer func() {
			if closeErr := mcpManager.Close(ctx); closeErr != nil {
				logger.G(ctx).WithError(closeErr).Warn("failed to close MCP manager")
			}
		}()
	}

	sessionID := strings.TrimSpace(req.ConversationID)
	if sessionID == "" {
		sessionID = convtypes.GenerateID()
	}

	appState, err := buildChatState(ctx, llmConfig, sessionID, mcpManager, customManager)
	if err != nil {
		return sessionID, err
	}

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to create LLM thread")
	}

	thread.SetState(appState)
	thread.SetConversationID(sessionID)
	thread.EnablePersistence(ctx, true)

	if err := sink.Send(ChatEvent{
		Kind:           "conversation",
		ConversationID: sessionID,
		Role:           "assistant",
	}); err != nil {
		return sessionID, err
	}

	handler := &chatMessageHandler{
		conversationID: sessionID,
		sink:           sink,
	}

	_, err = thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
		PromptCache: true,
		Images:      imageInputs,
	})
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to process chat message")
	}

	return sessionID, nil
}

func normalizeChatRequest(req ChatRequest) (string, []string, error) {
	message := strings.TrimSpace(req.Message)
	if len(req.Content) == 0 {
		return message, nil, nil
	}

	textParts := make([]string, 0, len(req.Content))
	imageInputs := make([]string, 0, len(req.Content))

	for _, block := range req.Content {
		switch block.Type {
		case "text":
			if trimmed := strings.TrimSpace(block.Text); trimmed != "" {
				textParts = append(textParts, trimmed)
			}
		case "image":
			if block.Source != nil {
				data := strings.TrimSpace(block.Source.Data)
				mediaType := strings.TrimSpace(block.Source.MediaType)
				if data == "" || mediaType == "" {
					return "", nil, errors.New("image source must include data and media_type")
				}
				imageInputs = append(imageInputs, fmt.Sprintf("data:%s;base64,%s", mediaType, data))
				continue
			}

			if block.ImageURL != nil {
				url := strings.TrimSpace(block.ImageURL.URL)
				if url == "" {
					return "", nil, errors.New("image_url must include url")
				}
				imageInputs = append(imageInputs, url)
				continue
			}

			return "", nil, errors.New("image block must include source or image_url")
		default:
			return "", nil, errors.Errorf("unsupported content block type: %s", block.Type)
		}
	}

	if len(textParts) > 0 {
		message = strings.Join(textParts, "\n\n")
	}

	return message, imageInputs, nil
}

func buildChatState(
	ctx context.Context,
	llmConfig llmtypes.Config,
	sessionID string,
	mcpManager *tools.MCPManager,
	customManager *tools.CustomToolManager,
) (*tools.BasicState, error) {
	stateOpts := []tools.BasicStateOption{
		tools.WithSessionID(sessionID),
		tools.WithLLMConfig(llmConfig),
		tools.WithCustomTools(customManager),
		tools.WithMainTools(),
		tools.WithSkillTool(),
	}

	if !viper.GetBool("no_workflows") && !llmConfig.DisableSubagent {
		stateOpts = append(stateOpts, tools.WithSubAgentTool())
	}

	if mcpManager != nil {
		mcpSetup, err := mcp.SetupExecutionMode(ctx, mcpManager, sessionID, "")
		if err != nil && !stdErrors.Is(err, mcp.ErrDirectMode) {
			return nil, errors.Wrap(err, "failed to set up MCP execution mode")
		}

		if err == nil && mcpSetup != nil {
			stateOpts = append(stateOpts, mcpSetup.StateOpts...)
		} else {
			stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
		}
	}

	return tools.NewBasicState(ctx, stateOpts...), nil
}

type chatMessageHandler struct {
	conversationID string
	sink           ChatEventSink
}

func (h *chatMessageHandler) HandleText(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}

	_ = h.sink.Send(ChatEvent{
		Kind:           "text",
		Content:        text,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

func (h *chatMessageHandler) HandleToolUse(toolCallID string, toolName string, input string) {
	_ = h.sink.Send(ChatEvent{
		Kind:           "tool-use",
		ConversationID: h.conversationID,
		Role:           "assistant",
		ToolCallID:     toolCallID,
		ToolName:       toolName,
		Input:          input,
	})
}

func (h *chatMessageHandler) HandleToolResult(toolCallID string, toolName string, result tooltypes.ToolResult) {
	structuredResult := result.StructuredData()
	if structuredResult.ToolName == "" {
		structuredResult.ToolName = toolName
	}

	_ = h.sink.Send(ChatEvent{
		Kind:           "tool-result",
		ConversationID: h.conversationID,
		Role:           "assistant",
		ToolCallID:     toolCallID,
		ToolName:       structuredResult.ToolName,
		ToolResult:     &structuredResult,
	})
}

func (h *chatMessageHandler) HandleThinking(thinking string) {
	if strings.TrimSpace(thinking) == "" {
		return
	}

	_ = h.sink.Send(ChatEvent{
		Kind:           "thinking",
		Content:        thinking,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

func (h *chatMessageHandler) HandleDone() {}

func (h *chatMessageHandler) HandleTextDelta(delta string) {
	if delta == "" {
		return
	}

	_ = h.sink.Send(ChatEvent{
		Kind:           "text-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

func (h *chatMessageHandler) HandleThinkingStart() {
	_ = h.sink.Send(ChatEvent{
		Kind:           "thinking-start",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

func (h *chatMessageHandler) HandleThinkingDelta(delta string) {
	if delta == "" {
		return
	}

	_ = h.sink.Send(ChatEvent{
		Kind:           "thinking-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

func (h *chatMessageHandler) HandleThinkingBlockEnd() {
	_ = h.sink.Send(ChatEvent{
		Kind:           "thinking-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

func (h *chatMessageHandler) HandleContentBlockEnd() {
	_ = h.sink.Send(ChatEvent{
		Kind:           "content-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

type ndjsonEventSink struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func newNDJSONEventSink(w http.ResponseWriter) (*ndjsonEventSink, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming is not supported by this response writer")
	}

	return &ndjsonEventSink{
		w:       w,
		flusher: flusher,
	}, nil
}

func (s *ndjsonEventSink) Send(event ChatEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := json.Marshal(event)
	if err != nil {
		return errors.Wrap(err, "failed to marshal chat event")
	}

	if _, err := s.w.Write(append(payload, '\n')); err != nil {
		return errors.Wrap(err, "failed to write chat event")
	}
	s.flusher.Flush()
	return nil
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ChatRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid chat request", err)
		return
	}

	message, imageInputs, err := normalizeChatRequest(req)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid chat request", err)
		return
	}

	if message == "" && len(imageInputs) == 0 {
		s.writeErrorResponse(w, http.StatusBadRequest, "message cannot be empty", nil)
		return
	}

	sink, err := newNDJSONEventSink(w)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to initialize chat stream", err)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	conversationID, runErr := s.chatRunner.Run(ctx, req, sink)
	if runErr != nil {
		logger.G(ctx).WithError(runErr).Error("chat request failed")
		_ = sink.Send(ChatEvent{
			Kind:           "error",
			ConversationID: conversationID,
			Role:           "assistant",
			Error:          runErr.Error(),
		})
		return
	}

	_ = sink.Send(ChatEvent{
		Kind:           "done",
		ConversationID: conversationID,
		Role:           "assistant",
	})
}
