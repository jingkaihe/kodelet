package webui

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
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
	Profile        string             `json:"profile,omitempty"`
	CWD            string             `json:"cwd,omitempty"`
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
	Usage          *llmtypes.Usage                 `json:"usage,omitempty"`
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
type DefaultChatRunner struct {
	defaultCWD         string
	compactRatio       float64
	disableAutoCompact bool
}

// NewDefaultChatRunner creates a chat runner for the web UI server.
func NewDefaultChatRunner(defaultCWD string, compactRatio float64, disableAutoCompact bool) *DefaultChatRunner {
	return &DefaultChatRunner{
		defaultCWD:         defaultCWD,
		compactRatio:       compactRatio,
		disableAutoCompact: disableAutoCompact,
	}
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
			_ = mcpManager.Close(ctx)
		}()
	}

	sessionID := strings.TrimSpace(req.ConversationID)
	if sessionID == "" {
		sessionID = convtypes.GenerateID()
	}

	llmConfig, resolvedCWD, err := resolveWebChatConfig(ctx, sessionID, strings.TrimSpace(req.Profile), strings.TrimSpace(req.CWD), r.defaultCWD)
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to load configuration")
	}
	workspaceDir, err := mcp.ResolveWorkspaceDir(resolvedCWD)
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to resolve MCP workspace directory")
	}
	llmConfig.MCPExecutionMode = viper.GetString("mcp.execution_mode")
	llmConfig.MCPWorkspaceDir = workspaceDir
	llmConfig.WorkingDirectory = resolvedCWD

	appState, err := buildChatState(ctx, llmConfig, sessionID, resolvedCWD, mcpManager, customManager)
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
		logger.G(ctx).WithError(err).Debug("failed to send initial conversation event")
	}

	handler := &chatMessageHandler{
		conversationID: sessionID,
		sink:           sink,
	}

	_, err = thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
		PromptCache:        true,
		Images:             imageInputs,
		CompactRatio:       r.compactRatio,
		DisableAutoCompact: r.disableAutoCompact,
	})
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to process chat message")
	}

	return sessionID, nil
}

func resolveWebChatConfig(ctx context.Context, conversationID, requestedProfile, requestedCWD, defaultCWDInput string) (llmtypes.Config, string, error) {
	defaultCWD, err := resolveConfiguredDefaultCWD(defaultCWDInput)
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	expandedRequestedCWD, err := expandWebCWDInput(requestedCWD, defaultCWD)
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	if strings.TrimSpace(conversationID) == "" {
		config, err := resolveWebChatConfigForNewConversation(requestedProfile)
		if err != nil {
			return llmtypes.Config{}, "", err
		}
		resolution, err := conversationservice.ResolveCWD(ctx, nil, "", expandedRequestedCWD, defaultCWD, false)
		if err != nil {
			return llmtypes.Config{}, "", err
		}
		config.WorkingDirectory = resolution.CWD
		return config, resolution.CWD, nil
	}

	service, err := conversationservice.GetDefaultConversationService(ctx)
	if err != nil {
		return llmtypes.Config{}, "", errors.Wrap(err, "failed to open conversation service")
	}
	defer func() {
		_ = service.Close()
	}()

	resolution, err := conversationservice.ResolveCWD(ctx, serviceStoreAdapter{service: service}, conversationID, expandedRequestedCWD, defaultCWD, false)
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	record, err := service.GetConversation(ctx, conversationID)
	if err != nil {
		config, newErr := resolveWebChatConfigForNewConversation(requestedProfile)
		if newErr != nil {
			return llmtypes.Config{}, "", newErr
		}
		config.WorkingDirectory = resolution.CWD
		return config, resolution.CWD, nil
	}

	config, err := resolveWebChatConfigForExistingConversation(record)
	if err != nil {
		return llmtypes.Config{}, "", err
	}
	config.WorkingDirectory = resolution.CWD
	return config, resolution.CWD, nil
}

type serviceStoreAdapter struct {
	service conversationservice.ConversationServiceInterface
}

func (s serviceStoreAdapter) Save(context.Context, convtypes.ConversationRecord) error {
	return errors.New("save not implemented")
}

func (s serviceStoreAdapter) Delete(context.Context, string) error {
	return errors.New("delete not implemented")
}

func (s serviceStoreAdapter) Query(context.Context, convtypes.QueryOptions) (convtypes.QueryResult, error) {
	return convtypes.QueryResult{}, errors.New("query not implemented")
}

func (s serviceStoreAdapter) Close() error { return nil }

func (s serviceStoreAdapter) Load(ctx context.Context, id string) (convtypes.ConversationRecord, error) {
	record, err := s.service.GetConversation(ctx, id)
	if err != nil {
		return convtypes.ConversationRecord{}, err
	}
	return convtypes.ConversationRecord{
		ID:          record.ID,
		CWD:         record.CWD,
		Provider:    record.Provider,
		Metadata:    record.Metadata,
		RawMessages: record.RawMessages,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
		Usage:       record.Usage,
		Summary:     record.Summary,
		ToolResults: record.ToolResults,
	}, nil
}

func resolveWebChatConfigForNewConversation(requestedProfile string) (llmtypes.Config, error) {
	requestedProfile = strings.TrimSpace(requestedProfile)
	if strings.EqualFold(requestedProfile, "default") {
		config, err := llm.GetConfigFromViperWithoutProfile()
		if err != nil {
			return llmtypes.Config{}, err
		}
		config.Profile = "default"
		return config, nil
	}

	profileName := normalizeRequestedProfile(requestedProfile)
	if profileName != "" {
		return llm.GetConfigFromViperWithProfile(profileName)
	}

	return llm.GetConfigFromViper()
}

func resolveWebChatConfigForExistingConversation(record *conversationservice.GetConversationResponse) (llmtypes.Config, error) {
	profileName := ""
	hasStoredProfile := false
	if record != nil && record.Metadata != nil {
		if rawProfile, ok := record.Metadata["profile"].(string); ok {
			hasStoredProfile = true
			profileName = normalizeRequestedProfile(rawProfile)
		}
	}

	var (
		config llmtypes.Config
		err    error
	)
	if hasStoredProfile {
		if profileName != "" {
			config, err = llm.GetConfigFromViperWithProfile(profileName)
		} else {
			config, err = llm.GetConfigFromViperWithoutProfile()
		}
	} else if profileName != "" {
		config, err = llm.GetConfigFromViperWithProfile(profileName)
	} else {
		config, err = llm.GetConfigFromViper()
	}
	if err != nil {
		return llmtypes.Config{}, err
	}

	if record == nil {
		return config, nil
	}

	if strings.TrimSpace(record.Provider) != "" {
		config.Provider = strings.TrimSpace(record.Provider)
	}
	if record.Metadata != nil {
		if model, ok := record.Metadata["model"].(string); ok && strings.TrimSpace(model) != "" {
			config.Model = strings.TrimSpace(model)
		}

		if strings.EqualFold(config.Provider, "openai") {
			if config.OpenAI == nil {
				config.OpenAI = &llmtypes.OpenAIConfig{}
			}
			if platform, ok := record.Metadata["platform"].(string); ok && strings.TrimSpace(platform) != "" {
				config.OpenAI.Platform = strings.TrimSpace(platform)
			}
			if apiMode, ok := record.Metadata["api_mode"].(string); ok && strings.TrimSpace(apiMode) != "" {
				config.OpenAI.APIMode = llmtypes.OpenAIAPIMode(strings.TrimSpace(apiMode))
			}
		}
	}

	if hasStoredProfile && profileName == "" {
		config.Profile = "default"
	} else {
		config.Profile = profileName
	}
	return config, nil
}

func normalizeRequestedProfile(profile string) string {
	normalized := strings.TrimSpace(profile)
	if normalized == "" || strings.EqualFold(normalized, "default") {
		return ""
	}
	return normalized
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
	workingDir string,
	mcpManager *tools.MCPManager,
	customManager *tools.CustomToolManager,
) (*tools.BasicState, error) {
	stateOpts := []tools.BasicStateOption{
		tools.WithSessionID(sessionID),
		tools.WithWorkingDirectory(workingDir),
		tools.WithLLMConfig(llmConfig),
		tools.WithCustomTools(customManager),
		tools.WithMainTools(),
		tools.WithSkillTool(),
	}

	if !viper.GetBool("no_workflows") && !llmConfig.DisableSubagent {
		stateOpts = append(stateOpts, tools.WithSubAgentTool())
	}

	if mcpManager != nil {
		mcpSetup, err := mcp.SetupExecutionMode(ctx, mcpManager, sessionID, workingDir)
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
	broadcast      func(string, ChatEvent)
	usageMu        sync.Mutex
	hasLastUsage   bool
	lastUsage      llmtypes.Usage
}

func (h *chatMessageHandler) sendEvent(event ChatEvent) {
	_ = h.sink.Send(event)
	if h.broadcast != nil {
		h.broadcast(h.conversationID, event)
	}
}

func (h *chatMessageHandler) HandleText(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}

	event := ChatEvent{
		Kind:           "text",
		Content:        text,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleToolUse(toolCallID string, toolName string, input string) {
	event := ChatEvent{
		Kind:           "tool-use",
		ConversationID: h.conversationID,
		Role:           "assistant",
		ToolCallID:     toolCallID,
		ToolName:       toolName,
		Input:          input,
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleToolResult(toolCallID string, toolName string, result tooltypes.ToolResult) {
	structuredResult := result.StructuredData()
	if structuredResult.ToolName == "" || structuredResult.ToolName == "unknown" {
		structuredResult.ToolName = toolName
	}

	event := ChatEvent{
		Kind:           "tool-result",
		ConversationID: h.conversationID,
		Role:           "assistant",
		ToolCallID:     toolCallID,
		ToolName:       structuredResult.ToolName,
		ToolResult:     &structuredResult,
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinking(thinking string) {
	if strings.TrimSpace(thinking) == "" {
		return
	}

	event := ChatEvent{
		Kind:           "thinking",
		Content:        thinking,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleDone() {}

func (h *chatMessageHandler) HandleUsage(usage llmtypes.Usage) {
	h.usageMu.Lock()
	if h.hasLastUsage && h.lastUsage == usage {
		h.usageMu.Unlock()
		return
	}
	h.lastUsage = usage
	h.hasLastUsage = true
	h.usageMu.Unlock()

	h.sendEvent(ChatEvent{
		Kind:           "usage",
		ConversationID: h.conversationID,
		Role:           "assistant",
		Usage:          &usage,
	})
}

func (h *chatMessageHandler) HandleTextDelta(delta string) {
	if delta == "" {
		return
	}

	event := ChatEvent{
		Kind:           "text-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinkingStart() {
	event := ChatEvent{
		Kind:           "thinking-start",
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinkingDelta(delta string) {
	if delta == "" {
		return
	}

	event := ChatEvent{
		Kind:           "thinking-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinkingBlockEnd() {
	event := ChatEvent{
		Kind:           "thinking-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleContentBlockEnd() {
	event := ChatEvent{
		Kind:           "content-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

type ndjsonEventSink struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

type subscriberEventSink struct {
	ch   chan ChatEvent
	once sync.Once
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

func newSubscriberEventSink() *subscriberEventSink {
	return &subscriberEventSink{ch: make(chan ChatEvent, 128)}
}

func (s *subscriberEventSink) Send(event ChatEvent) error {
	select {
	case s.ch <- event:
		return nil
	default:
		return errors.New("subscriber buffer full")
	}
}

func (s *subscriberEventSink) Close() {
	s.once.Do(func() {
		close(s.ch)
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	requestCtx := r.Context()

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

	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		conversationID = convtypes.GenerateID()
		req.ConversationID = conversationID
	}

	ctx, cancel := context.WithCancel(s.chatExecutionContext(requestCtx))
	run := newActiveChatRun(cancel)
	if !s.registerActiveChat(conversationID, run) {
		cancel()
		s.writeErrorResponse(w, http.StatusConflict, "conversation already has an active run", nil)
		return
	}
	defer s.unregisterActiveChat(conversationID, run)
	defer s.closeChatSubscribers(conversationID)
	defer cancel()

	broadcastingSink := &broadcastingEventSink{
		primary:        sink,
		broadcast:      s.broadcastChatEvent,
		conversationID: conversationID,
	}

	conversationID, runErr := s.chatRunner.Run(ctx, req, broadcastingSink)
	if runErr != nil {
		if stdErrors.Is(runErr, io.ErrClosedPipe) || stdErrors.Is(runErr, context.Canceled) {
			logger.G(requestCtx).WithError(runErr).Debug("chat stream disconnected")
			return
		}

		logger.G(ctx).WithError(runErr).Error("chat request failed")
		s.broadcastChatEvent(conversationID, ChatEvent{
			Kind:           "error",
			ConversationID: conversationID,
			Role:           "assistant",
			Error:          runErr.Error(),
		})
		_ = sink.Send(ChatEvent{
			Kind:           "error",
			ConversationID: conversationID,
			Role:           "assistant",
			Error:          runErr.Error(),
		})
		return
	}

	s.broadcastChatEvent(conversationID, ChatEvent{
		Kind:           "done",
		ConversationID: conversationID,
		Role:           "assistant",
	})
	_ = sink.Send(ChatEvent{
		Kind:           "done",
		ConversationID: conversationID,
		Role:           "assistant",
	})
}

type broadcastingEventSink struct {
	primary        ChatEventSink
	broadcast      func(string, ChatEvent)
	conversationID string
}

func (s *broadcastingEventSink) Send(event ChatEvent) error {
	if err := s.primary.Send(event); err != nil {
		if s.broadcast != nil {
			s.broadcast(s.conversationID, event)
		}
		return err
	}

	if s.broadcast != nil {
		s.broadcast(s.conversationID, event)
	}
	return nil
}
