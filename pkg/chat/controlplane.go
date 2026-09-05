package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

const (
	maxControlPlaneChatEventSize           = 16 << 20
	maxControlPlaneConversationHistorySize = 64 << 20
)

// ControlPlaneChatRunner streams chat turns and conversation history through kodelet serve.
type ControlPlaneChatRunner struct {
	baseURL   string
	chatURL   string
	authToken string
	runnerID  string
	client    *http.Client
}

// ControlPlaneHTTPError reports a non-successful control-plane response.
type ControlPlaneHTTPError struct {
	StatusCode int
	Message    string
}

func (e *ControlPlaneHTTPError) Error() string {
	if e == nil {
		return "control plane request failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("control plane returned HTTP %d: %s", e.StatusCode, strings.TrimSpace(e.Message))
	}
	return fmt.Sprintf("control plane returned HTTP %d", e.StatusCode)
}

// Retryable reports whether retrying the request may succeed without user action.
func (e *ControlPlaneHTTPError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return e.StatusCode >= http.StatusInternalServerError
	}
}

// ControlPlaneStreamProtocolError reports invalid data from a control-plane event stream.
type ControlPlaneStreamProtocolError struct {
	err error
}

func (e *ControlPlaneStreamProtocolError) Error() string {
	if e == nil || e.err == nil {
		return "invalid control-plane stream data"
	}
	return e.err.Error()
}

func (e *ControlPlaneStreamProtocolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Retryable reports that reconnecting cannot repair deterministic stream data errors.
func (e *ControlPlaneStreamProtocolError) Retryable() bool {
	return false
}

// ControlPlaneProfileOption describes one model-policy profile advertised by the server.
type ControlPlaneProfileOption struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Active bool   `json:"active,omitempty"`
}

// ControlPlaneChatSettings contains server-owned settings for a new conversation.
type ControlPlaneChatSettings struct {
	CurrentProfile         string                      `json:"currentProfile,omitempty"`
	Profiles               []ControlPlaneProfileOption `json:"profiles"`
	ReasoningEffort        string                      `json:"reasoningEffort"`
	ReasoningEffortOptions []string                    `json:"reasoningEffortOptions"`
	DefaultCWD             string                      `json:"defaultCWD,omitempty"`
}

// NewControlPlaneChatRunner creates a TUI-compatible control-plane transport with an optional runner selection.
func NewControlPlaneChatRunner(server, authToken, runnerID string) (*ControlPlaneChatRunner, error) {
	baseURL, err := controlPlaneBaseURL(server)
	if err != nil {
		return nil, err
	}
	chatURL, err := controlPlaneEndpointURL(baseURL, "api", "chat")
	if err != nil {
		return nil, err
	}
	runnerID = strings.TrimSpace(runnerID)
	return &ControlPlaneChatRunner{
		baseURL:   baseURL,
		chatURL:   chatURL,
		authToken: strings.TrimSpace(authToken),
		runnerID:  runnerID,
		client:    &http.Client{Timeout: 0},
	}, nil
}

// Run posts one chat request and forwards NDJSON events to the TUI sink.
func (r *ControlPlaneChatRunner) Run(ctx context.Context, request ChatRequest, sink ChatEventSink) (string, error) {
	if r == nil || r.client == nil {
		return "", errors.New("control-plane chat runner is not initialized")
	}
	if sink == nil {
		return "", errors.New("chat event sink is required")
	}
	if r.runnerID != "" {
		request.RunnerID = r.runnerID
	}
	request.ClientCapabilities = &ChatClientCapabilities{
		InteractiveUI: controlPlaneSupportsInteractiveUI(ctx),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", errors.Wrap(err, "failed to encode control-plane chat request")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, r.chatURL, bytes.NewReader(payload))
	if err != nil {
		return "", errors.Wrap(err, "failed to create control-plane chat request")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if r.authToken != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+r.authToken)
	}

	response, err := r.client.Do(httpRequest)
	if err != nil {
		return "", errors.Wrap(err, "failed to stream control-plane chat")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", controlPlaneResponseError(response)
	}

	return r.consumeChatStream(ctx, response.Body, strings.TrimSpace(request.ConversationID), sink, true, false, "")
}

// StreamConversation follows live events for one control-plane conversation across turns.
func (r *ControlPlaneChatRunner) StreamConversation(ctx context.Context, conversationID string, sink ChatEventSink) error {
	if r == nil || r.client == nil {
		return errors.New("control-plane chat runner is not initialized")
	}
	if sink == nil {
		return errors.New("chat event sink is required")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return errors.New("conversation id is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "stream")
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create control-plane conversation stream request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to stream control-plane conversation")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return controlPlaneResponseError(response)
	}
	if lifecycleSink, ok := sink.(ConversationStreamLifecycleSink); ok {
		active, _ := strconv.ParseBool(response.Header.Get(ConversationStreamActiveHeader))
		if err := lifecycleSink.ConversationStreamConnected(active); err != nil {
			return err
		}
	}

	_, err = r.consumeChatStream(ctx, response.Body, conversationID, sink, false, true, conversationID)
	return err
}

func (r *ControlPlaneChatRunner) consumeChatStream(ctx context.Context, reader io.Reader, conversationID string, sink ChatEventSink, requireCompletion, asynchronousUI bool, expectedConversationID string) (string, error) {
	var streamErr error
	completed := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxControlPlaneChatEventSize)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event ChatEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return conversationID, &ControlPlaneStreamProtocolError{err: errors.Wrap(err, "failed to decode control-plane chat event")}
		}
		eventConversationID := strings.TrimSpace(event.ConversationID)
		if eventConversationID != "" && expectedConversationID != "" && eventConversationID != expectedConversationID {
			return conversationID, &ControlPlaneStreamProtocolError{err: errors.Errorf("control plane streamed conversation %s while watching %s", eventConversationID, expectedConversationID)}
		}
		if eventConversationID != "" {
			conversationID = eventConversationID
		}
		if asynchronousUI {
			if controlPlaneSupportsInteractiveUI(ctx) && isControlPlaneUIEvent(event.Kind) {
				go func(event ChatEvent, eventConversationID string) {
					_, _ = r.handleUIEvent(ctx, eventConversationID, event)
				}(event, conversationID)
				continue
			}
		} else {
			handled, err := r.handleUIEvent(ctx, conversationID, event)
			if err != nil {
				return conversationID, err
			}
			if handled {
				continue
			}
		}
		if err := sink.Send(event); err != nil {
			return conversationID, err
		}
		if requireCompletion && event.Kind == "done" {
			completed = true
		}
		if requireCompletion && event.Kind == "error" && strings.TrimSpace(event.Error) != "" {
			streamErr = errors.New(event.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		streamErr := errors.Wrap(err, "failed to read control-plane chat stream")
		if errors.Is(err, bufio.ErrTooLong) {
			return conversationID, &ControlPlaneStreamProtocolError{err: streamErr}
		}
		return conversationID, streamErr
	}
	if !requireCompletion {
		return conversationID, nil
	}
	if streamErr != nil {
		return conversationID, streamErr
	}
	if !completed {
		if err := ctx.Err(); err != nil {
			return conversationID, err
		}
		return conversationID, errors.New("control-plane chat stream ended before completion")
	}
	return conversationID, nil
}

func isControlPlaneUIEvent(kind string) bool {
	switch kind {
	case "ui-input", "ui-input-request", "ui-confirm", "ui-confirm-request", "ui-select", "ui-select-request", "ui-notify", "ui-notification":
		return true
	default:
		return false
	}
}

// ListConversations returns control-plane conversations visible to this client.
func (r *ControlPlaneChatRunner) ListConversations(ctx context.Context, limit int) ([]convtypes.ConversationSummary, error) {
	if r == nil || r.client == nil {
		return nil, errors.New("control-plane chat runner is not initialized")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations")
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse control-plane conversations URL")
	}
	query := parsed.Query()
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	query.Set("sortBy", "updated")
	query.Set("sortOrder", "desc")
	if r.runnerID != "" {
		query.Set("runnerId", r.runnerID)
	}
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create control-plane conversations request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list control-plane conversations")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, controlPlaneResponseError(response)
	}
	var result conversations.ListConversationsResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&result); err != nil {
		return nil, errors.Wrap(err, "failed to decode control-plane conversations")
	}
	if r.runnerID == "" {
		return result.Conversations, nil
	}
	filtered := make([]convtypes.ConversationSummary, 0, len(result.Conversations))
	for _, summary := range result.Conversations {
		runnerID, _ := summary.Metadata[RunnerIDMetadataKey].(string)
		if strings.TrimSpace(runnerID) == r.runnerID {
			filtered = append(filtered, summary)
		}
	}
	return filtered, nil
}

// LoadConversation returns normalized history from the control plane.
func (r *ControlPlaneChatRunner) LoadConversation(ctx context.Context, conversationID string) (ConversationHistory, error) {
	if r == nil || r.client == nil {
		return ConversationHistory{}, errors.New("control-plane chat runner is not initialized")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ConversationHistory{}, errors.New("conversation id is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID)
	if err != nil {
		return ConversationHistory{}, err
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to parse control-plane conversation URL")
	}
	query := parsedEndpoint.Query()
	query.Set("format", "stream")
	parsedEndpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedEndpoint.String(), nil)
	if err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to create control-plane conversation request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to load control-plane conversation")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ConversationHistory{}, controlPlaneResponseError(response)
	}
	var result controlPlaneConversationResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxControlPlaneConversationHistorySize)).Decode(&result); err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to decode control-plane conversation")
	}
	messages := normalizeControlPlaneConversationEntries(result.Entries, result.ToolResults)
	if len(messages) == 0 && len(result.Messages) > 0 {
		messages, err = normalizeControlPlaneConversationMessages(result.Messages, result.ToolResults)
		if err != nil {
			return ConversationHistory{}, err
		}
	}
	title := strings.TrimSpace(result.Summary)
	if title == "" {
		title = conversationID
	}
	return ConversationHistory{
		ID:                 firstNonEmptyString(result.ID, conversationID),
		CWD:                strings.TrimSpace(result.CWD),
		Title:              title,
		Provider:           strings.TrimSpace(result.Provider),
		Profile:            strings.TrimSpace(result.Profile),
		ReasoningEffort:    strings.TrimSpace(result.ReasoningEffort),
		RunnerID:           strings.TrimSpace(result.RunnerID),
		EnvironmentProfile: strings.TrimSpace(result.EnvironmentProfile),
		UpdatedAt:          result.UpdatedAt,
		Usage:              result.Usage,
		Messages:           messages,
	}, nil
}

type controlPlaneConversationResponse struct {
	ID                 string                                    `json:"id"`
	UpdatedAt          time.Time                                 `json:"updatedAt"`
	Provider           string                                    `json:"provider"`
	CWD                string                                    `json:"cwd"`
	Profile            string                                    `json:"profile"`
	ReasoningEffort    string                                    `json:"reasoningEffort"`
	RunnerID           string                                    `json:"runnerId"`
	EnvironmentProfile string                                    `json:"environmentProfile"`
	Summary            string                                    `json:"summary"`
	Usage              llmtypes.Usage                            `json:"usage"`
	Messages           []controlPlaneConversationMessage         `json:"messages"`
	Entries            []conversations.StreamableMessage         `json:"entries"`
	ToolResults        map[string]tooltypes.StructuredToolResult `json:"toolResults"`
}

type controlPlaneConversationMessage struct {
	Role          string                 `json:"role"`
	Content       json.RawMessage        `json:"content"`
	ToolCalls     []controlPlaneToolCall `json:"toolCalls"`
	ThinkingText  string                 `json:"thinkingText"`
	ThinkingTexts []string               `json:"thinkingTexts"`
}

type controlPlaneToolCall struct {
	ID       string                       `json:"id"`
	Function controlPlaneToolCallFunction `json:"function"`
}

type controlPlaneToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func normalizeControlPlaneConversationEntries(entries []conversations.StreamableMessage, toolResults map[string]tooltypes.StructuredToolResult) []conversations.StreamableMessage {
	result := make([]conversations.StreamableMessage, len(entries))
	copy(result, entries)
	for index := range result {
		entry := &result[index]
		if entry.Kind != "tool-result" {
			continue
		}
		structured, ok := toolResults[entry.ToolCallID]
		if !ok {
			continue
		}
		if entry.ToolName == "" {
			entry.ToolName = structured.ToolName
		}
		if entry.ToolOutput == "" {
			var embedded tooltypes.StructuredToolResult
			if json.Unmarshal([]byte(entry.Content), &embedded) != nil {
				entry.ToolOutput = entry.Content
			} else {
				entry.ToolOutput = renderers.NewRendererRegistry().Render(structured)
			}
		}
		if payload, err := json.Marshal(structured); err == nil {
			entry.Content = string(payload)
		}
	}
	return result
}

func normalizeControlPlaneConversationMessages(messages []controlPlaneConversationMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]conversations.StreamableMessage, error) {
	result := make([]conversations.StreamableMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		content, err := controlPlaneMessageText(message.Content)
		if err != nil {
			return nil, err
		}
		if role == "user" {
			if strings.TrimSpace(content) != "" {
				result = append(result, conversations.StreamableMessage{Kind: "text", Role: role, Content: content})
			}
			continue
		}

		thinkingTexts := message.ThinkingTexts
		if len(thinkingTexts) == 0 && strings.TrimSpace(message.ThinkingText) != "" {
			thinkingTexts = []string{message.ThinkingText}
		}
		for _, thinking := range thinkingTexts {
			if thinking = strings.TrimSpace(thinking); thinking != "" {
				result = append(result, conversations.StreamableMessage{Kind: "thinking", Role: "assistant", Content: thinking})
			}
		}
		for _, toolCall := range message.ToolCalls {
			toolCallID := strings.TrimSpace(toolCall.ID)
			toolName := strings.TrimSpace(toolCall.Function.Name)
			result = append(result, conversations.StreamableMessage{
				Kind:       "tool-use",
				Role:       "assistant",
				ToolCallID: toolCallID,
				ToolName:   toolName,
				Input:      firstNonEmptyString(toolCall.Function.Arguments, "{}"),
			})
			if toolResult, ok := toolResults[toolCallID]; ok {
				payload, err := json.Marshal(toolResult)
				if err != nil {
					return nil, errors.Wrap(err, "failed to encode control-plane tool result")
				}
				result = append(result, conversations.StreamableMessage{
					Kind:       "tool-result",
					Role:       "user",
					Content:    string(payload),
					ToolOutput: renderers.NewRendererRegistry().Render(toolResult),
					ToolCallID: toolCallID,
					ToolName:   firstNonEmptyString(toolResult.ToolName, toolName),
				})
			}
		}
		if strings.TrimSpace(content) != "" {
			result = append(result, conversations.StreamableMessage{Kind: "text", Role: "assistant", Content: content})
		}
	}
	return result, nil
}

func controlPlaneMessageText(content json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(content)) == 0 || bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return text, nil
	}
	var blocks []ChatContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", errors.Wrap(err, "failed to decode control-plane conversation message content")
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch strings.TrimSpace(block.Type) {
		case "text", "input_text":
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		case "image", "input_image", "image_url":
			parts = append(parts, "[Image attachment]")
		}
	}
	return strings.Join(parts, "\n"), nil
}

// ChatSettings fetches control-plane model profiles and reasoning policy.
func (r *ControlPlaneChatRunner) ChatSettings(ctx context.Context, profile string) (ControlPlaneChatSettings, error) {
	if r == nil || r.client == nil {
		return ControlPlaneChatSettings{}, errors.New("control-plane chat runner is not initialized")
	}
	settingsURL, err := controlPlaneEndpointURL(r.baseURL, "api", "chat", "settings")
	if err != nil {
		return ControlPlaneChatSettings{}, err
	}
	parsed, err := url.Parse(settingsURL)
	if err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "failed to parse control-plane chat settings URL")
	}
	if profile = strings.TrimSpace(profile); profile != "" {
		query := parsed.Query()
		query.Set("profile", profile)
		parsed.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "failed to create control-plane chat settings request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "failed to load control-plane chat settings")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ControlPlaneChatSettings{}, controlPlaneResponseError(response)
	}
	var settings ControlPlaneChatSettings
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&settings); err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "failed to decode control-plane chat settings")
	}
	return settings, nil
}

// StopConversation requests cancellation of central and runner work before the client stream closes.
func (r *ControlPlaneChatRunner) StopConversation(ctx context.Context, conversationID string) error {
	return r.StopConversationTurn(ctx, conversationID, "")
}

// StopConversationTurn requests cancellation of one specific control-plane turn.
func (r *ControlPlaneChatRunner) StopConversationTurn(ctx context.Context, conversationID, turnID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return errors.New("conversation id is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "stop")
	if err != nil {
		return err
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return errors.Wrap(err, "failed to parse control-plane stop URL")
		}
		query := parsed.Query()
		query.Set("turnId", turnID)
		parsed.RawQuery = query.Encode()
		endpoint = parsed.String()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create control-plane stop request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to stop control-plane conversation")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return controlPlaneResponseError(response)
	}
	var result struct {
		Stopped bool `json:"stopped"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return errors.Wrap(err, "failed to decode control-plane stop response")
	}
	if !result.Stopped {
		if turnID != "" {
			// A scoped stop that no longer matches is a successful no-op: the
			// requested turn has already ended or a newer turn now owns the stream.
			return nil
		}
		return errors.New("control-plane conversation is not active")
	}
	return nil
}

// SteerConversation queues steering content in the control plane that owns the active provider loop.
func (r *ControlPlaneChatRunner) SteerConversation(ctx context.Context, conversationID, message string, images []string) (bool, error) {
	conversationID = strings.TrimSpace(conversationID)
	message = strings.TrimSpace(message)
	if conversationID == "" {
		return false, errors.New("conversation id is required")
	}
	if message == "" {
		return false, errors.New("steering message is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "steer")
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(struct {
		Message string             `json:"message"`
		Content []ChatContentBlock `json:"content,omitempty"`
	}{
		Message: message,
		Content: ContentBlocksForUserInput(message, images),
	})
	if err != nil {
		return false, errors.Wrap(err, "failed to encode control-plane steering request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, errors.Wrap(err, "failed to create control-plane steering request")
	}
	request.Header.Set("Content-Type", "application/json")
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return false, errors.Wrap(err, "failed to queue control-plane steering message")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, controlPlaneResponseError(response)
	}
	var result struct {
		Success        bool   `json:"success"`
		ConversationID string `json:"conversation_id"`
		Queued         bool   `json:"queued"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return false, errors.Wrap(err, "failed to decode control-plane steering response")
	}
	if !result.Success || strings.TrimSpace(result.ConversationID) != conversationID {
		return false, errors.New("control-plane returned an invalid steering response")
	}
	return result.Queued, nil
}

func (r *ControlPlaneChatRunner) authorize(request *http.Request) {
	if r != nil && request != nil && r.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+r.authToken)
	}
}

func controlPlaneBaseURL(rawServer string) (string, error) {
	return controlplaneurl.NormalizeBase(rawServer)
}

func controlPlaneEndpointURL(baseURL string, parts ...string) (string, error) {
	return controlplaneurl.Endpoint(baseURL, parts...)
}

func controlPlaneSupportsInteractiveUI(ctx context.Context) bool {
	_, hasInput := extensions.UIInputBrokerFromContext(ctx)
	_, hasConfirm := extensions.UIConfirmBrokerFromContext(ctx)
	_, hasSelect := extensions.UISelectBrokerFromContext(ctx)
	_, hasNotify := extensions.UINotifyBrokerFromContext(ctx)
	return hasInput && hasConfirm && hasSelect && hasNotify
}

func (r *ControlPlaneChatRunner) handleUIEvent(ctx context.Context, conversationID string, event ChatEvent) (bool, error) {
	var (
		requestID string
		response  extensions.UIInputResponse
		err       error
	)
	switch event.Kind {
	case "ui-input", "ui-input-request":
		if event.UIInput == nil {
			return true, errors.New("control plane sent ui input event without a request")
		}
		requestID = event.UIInput.ID
		broker, ok := extensions.UIInputBrokerFromContext(ctx)
		if !ok {
			response = extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui input is not available"}
			break
		}
		response, err = broker.Input(ctx, extensions.UIInputRequest{
			ID:               event.UIInput.ID,
			Title:            event.UIInput.Title,
			HelpText:         event.UIInput.HelpText,
			Message:          event.UIInput.Message,
			Placeholder:      event.UIInput.Placeholder,
			DefaultValue:     event.UIInput.DefaultValue,
			SubmitButtonText: event.UIInput.SubmitButtonText,
			CancelButtonText: event.UIInput.CancelButtonText,
			Required:         event.UIInput.Required,
			Secret:           event.UIInput.Secret,
		})
	case "ui-confirm", "ui-confirm-request":
		if event.UIConfirm == nil {
			return true, errors.New("control plane sent ui confirmation event without a request")
		}
		requestID = event.UIConfirm.ID
		broker, ok := extensions.UIConfirmBrokerFromContext(ctx)
		if !ok {
			response = extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui confirmation is not available"}
			break
		}
		response, err = broker.Confirm(ctx, extensions.UIConfirmRequest{
			ID:                event.UIConfirm.ID,
			Title:             event.UIConfirm.Title,
			Message:           event.UIConfirm.Message,
			ConfirmButtonText: event.UIConfirm.ConfirmButtonText,
			CancelButtonText:  event.UIConfirm.CancelButtonText,
		})
	case "ui-select", "ui-select-request":
		if event.UISelect == nil {
			return true, errors.New("control plane sent ui selection event without a request")
		}
		requestID = event.UISelect.ID
		broker, ok := extensions.UISelectBrokerFromContext(ctx)
		if !ok {
			response = extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui selection is not available"}
			break
		}
		response, err = broker.Select(ctx, extensions.UISelectRequest{
			ID:               event.UISelect.ID,
			Title:            event.UISelect.Title,
			Message:          event.UISelect.Message,
			Options:          append([]string(nil), event.UISelect.Options...),
			SubmitButtonText: event.UISelect.SubmitButtonText,
			CancelButtonText: event.UISelect.CancelButtonText,
		})
	case "ui-notify", "ui-notification":
		if event.UINotify == nil {
			return true, errors.New("control plane sent ui notification event without a request")
		}
		broker, ok := extensions.UINotifyBrokerFromContext(ctx)
		if !ok {
			return false, nil
		}
		_, err = broker.Notify(ctx, extensions.UINotifyRequest{Title: event.UINotify.Title, Message: event.UINotify.Message})
		return true, err
	default:
		return false, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return true, ctxErr
	}
	if err != nil {
		return true, err
	}
	if response.Status == "" {
		response.Status = extensions.UIInputStatusDismissed
	}
	responseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return true, r.respondToUIInput(responseCtx, conversationID, requestID, response)
}

func (r *ControlPlaneChatRunner) respondToUIInput(ctx context.Context, conversationID, requestID string, response extensions.UIInputResponse) error {
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if conversationID == "" || requestID == "" {
		return errors.New("control-plane ui response is missing conversation or request id")
	}
	responseURL, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "ui-input", requestID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return errors.Wrap(err, "failed to encode control-plane ui response")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(payload))
	if err != nil {
		return errors.Wrap(err, "failed to create control-plane ui response")
	}
	request.Header.Set("Content-Type", "application/json")
	if r.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+r.authToken)
	}
	httpResponse, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to send control-plane ui response")
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		return controlPlaneResponseError(httpResponse)
	}
	return nil
}

func controlPlaneResponseError(response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var value struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(payload, &value) == nil {
		if message := firstNonEmptyString(value.Error, value.Message); message != "" {
			return &ControlPlaneHTTPError{StatusCode: response.StatusCode, Message: message}
		}
	}
	return &ControlPlaneHTTPError{StatusCode: response.StatusCode}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

var _ ChatRunner = (*ControlPlaneChatRunner)(nil)
