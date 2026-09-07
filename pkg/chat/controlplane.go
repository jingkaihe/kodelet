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
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
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
	baseURL         string
	chatURL         string
	authToken       string
	runnerID        string
	client          *http.Client
	clientID        string
	persistentMu    sync.Mutex
	widgets         map[string]remoteWidget
	widgetRevisions map[string]string
}

// ControlPlaneHTTPError reports a non-successful control-plane response.
type ControlPlaneHTTPError struct {
	StatusCode int
	Message    string
}

func (e *ControlPlaneHTTPError) Error() string {
	if e == nil {
		return "server request failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("server returned HTTP %d: %s", e.StatusCode, strings.TrimSpace(e.Message))
	}
	return fmt.Sprintf("server returned HTTP %d", e.StatusCode)
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
		return "received an invalid chat response"
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
	DefaultRunnerID        string                      `json:"defaultRunnerId,omitempty"`
	DefaultRunnerHostID    string                      `json:"defaultRunnerHostId,omitempty"`
	DefaultRunnerReady     bool                        `json:"defaultRunnerReady"`
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
		clientID:  convtypes.GenerateID(),
		client:    &http.Client{Timeout: 0},
	}, nil
}

// Run posts one chat request and forwards NDJSON events to the TUI sink.
func (r *ControlPlaneChatRunner) Run(ctx context.Context, request ChatRequest, sink ChatEventSink) (string, error) {
	if r == nil || r.client == nil {
		return "", errors.New("the chat connection is not initialized")
	}
	if sink == nil {
		return "", errors.New("chat event sink is required")
	}
	if r.runnerID != "" {
		request.RunnerID = r.runnerID
	}
	request.ConversationID, request.TurnID = strings.TrimSpace(request.ConversationID), strings.TrimSpace(request.TurnID)
	if request.ConversationID == "" {
		request.ConversationID = convtypes.GenerateID()
	}
	if request.TurnID == "" {
		request.TurnID = convtypes.GenerateID()
	}
	capabilities := controlPlaneClientCapabilities(ctx)
	request.ClientCapabilities = &capabilities
	payload, err := json.Marshal(request)
	if err != nil {
		return "", errors.Wrap(err, "failed to encode chat request")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, r.chatURL, bytes.NewReader(payload))
	if err != nil {
		return "", errors.Wrap(err, "failed to create chat request")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set(ClientIDHeader, r.clientID)
	if r.authToken != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+r.authToken)
	}

	response, err := r.client.Do(httpRequest)
	if err != nil {
		return request.ConversationID, &UncertainSubmissionError{ConversationID: request.ConversationID, TurnID: request.TurnID, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusAccepted {
		var receipt TurnReceipt
		if err := json.NewDecoder(io.LimitReader(response.Body, maxControlPlaneConversationHistorySize)).Decode(&receipt); err != nil {
			return request.ConversationID, &UncertainSubmissionError{ConversationID: request.ConversationID, TurnID: request.TurnID, Err: err}
		}
		if err := validateTurnReceipt(receipt, request.ConversationID, request.TurnID); err != nil {
			return request.ConversationID, err
		}
		return request.ConversationID, &TurnPendingError{Receipt: receipt}
	}
	if response.StatusCode != http.StatusOK {
		err := controlPlaneResponseError(response)
		if response.StatusCode >= http.StatusInternalServerError || response.StatusCode == http.StatusRequestTimeout {
			return request.ConversationID, &UncertainSubmissionError{ConversationID: request.ConversationID, TurnID: request.TurnID, Err: err}
		}
		return "", err
	}

	terminal := false
	id, err := r.consumeChatStream(ctx, response.Body, strings.TrimSpace(request.ConversationID), shortcutEventSink(func(event ChatEvent) error {
		// Older servers end failed turns with an error event rather than done.
		terminal = terminal || event.Kind == "done" || event.Kind == "error"
		return sink.Send(event)
	}), true, false, "")
	if err != nil && !terminal {
		return id, &UncertainSubmissionError{ConversationID: request.ConversationID, TurnID: request.TurnID, Err: err}
	}
	return id, err
}

// StreamConversation follows live events for one control-plane conversation across turns.
func (r *ControlPlaneChatRunner) StreamConversation(ctx context.Context, conversationID string, sink ChatEventSink) error {
	if r == nil || r.client == nil {
		return errors.New("the chat connection is not initialized")
	}
	if sink == nil {
		return errors.New("chat event sink is required")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return errors.New("conversation ID is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "stream")
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create conversation stream request")
	}
	r.authorize(request)
	request.Header.Set(ClientIDHeader, r.clientID)
	request.Header.Set(UICapabilitiesHeader, controlPlaneUICapabilitiesHeader(ctx))
	response, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to stream conversation")
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
	persistentUI := newRemoteUIStream(ctx, r)
	defer persistentUI.close()
	var streamErr error
	completed := false
	// Prompt handlers must not block reading dismissal or execution completion.
	// Only this stream owns these contexts; losing it dismisses local dialogs,
	// but does not issue a conversation cancellation request.
	type pendingUI struct{ cancel context.CancelFunc }
	pending := make(map[string]*pendingUI)
	var pendingMu sync.Mutex
	var handlers sync.WaitGroup
	defer func() {
		pendingMu.Lock()
		for _, prompt := range pending {
			prompt.cancel()
		}
		pendingMu.Unlock()
		handlers.Wait()
	}()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxControlPlaneChatEventSize)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event ChatEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return conversationID, &ControlPlaneStreamProtocolError{err: errors.Wrap(err, "failed to decode chat event")}
		}
		eventConversationID := strings.TrimSpace(event.ConversationID)
		if eventConversationID != "" && expectedConversationID != "" && eventConversationID != expectedConversationID {
			return conversationID, &ControlPlaneStreamProtocolError{err: errors.Errorf("server streamed conversation %s while watching %s", eventConversationID, expectedConversationID)}
		}
		if eventConversationID != "" {
			conversationID = eventConversationID
		}
		eventConversationID = conversationID
		if handled, err := persistentUI.handle(ctx, conversationID, event); handled {
			if err != nil {
				return conversationID, err
			}
			continue
		}
		if event.Kind == "ui-request-end" {
			pendingMu.Lock()
			if prompt := pending[event.UIRequestID]; prompt != nil {
				prompt.cancel()
			}
			pendingMu.Unlock()
			continue
		}
		if isControlPlaneUIEvent(event.Kind) && (!asynchronousUI || controlPlaneSupportsInteractiveUI(ctx)) {
			requestID := ""
			switch {
			case event.UIInput != nil:
				requestID = event.UIInput.ID
			case event.UIConfirm != nil:
				requestID = event.UIConfirm.ID
			case event.UISelect != nil:
				requestID = event.UISelect.ID
			}
			if requestID != "" {
				promptCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				prompt := &pendingUI{cancel: cancel}
				pendingMu.Lock()
				if previous := pending[requestID]; previous != nil {
					previous.cancel()
				}
				pending[requestID] = prompt
				pendingMu.Unlock()
				handlers.Go(func() {
					defer cancel()
					_, _ = r.handleUIEvent(promptCtx, eventConversationID, event)
					pendingMu.Lock()
					if pending[requestID] == prompt {
						delete(pending, requestID)
					}
					pendingMu.Unlock()
				})
				continue
			}
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
		streamErr := errors.Wrap(err, "failed to read chat stream")
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
		return conversationID, errors.New("the connection ended before the response was complete")
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
	return r.ListConversationsInCWD(ctx, limit, "")
}

// ListConversationsInCWD lists history using runner-host CWD semantics. The daemon
// applies the workspace filter before pagination, so follow never picks a turn
// from another workspace merely because it was updated more recently.
func (r *ControlPlaneChatRunner) ListConversationsInCWD(ctx context.Context, limit int, cwd string) ([]convtypes.ConversationSummary, error) {
	result, err := r.QueryConversations(ctx, conversations.ListConversationsRequest{Limit: limit, CWD: cwd, SortBy: "updated", SortOrder: "desc"})
	if err != nil {
		return nil, err
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

// QueryConversations applies history filters centrally, without requiring an
// online runner. CWD filters match canonical persisted runner-host paths.
func (r *ControlPlaneChatRunner) QueryConversations(ctx context.Context, options conversations.ListConversationsRequest) (conversations.ListConversationsResponse, error) {
	query := url.Values{
		"format": {"raw"}, "limit": {strconv.Itoa(options.Limit)}, "offset": {strconv.Itoa(options.Offset)},
		"sortBy": {options.SortBy}, "sortOrder": {options.SortOrder}, "search": {options.SearchTerm},
		"provider": {options.Provider}, "cwd": {options.CWD}, "runnerId": {options.RunnerID},
	}
	if r != nil && r.runnerID != "" {
		query.Set("runnerId", r.runnerID)
	}
	if options.StartDate != nil {
		query.Set("startDate", options.StartDate.Format(time.RFC3339Nano))
	}
	if options.EndDate != nil {
		query.Set("endDate", options.EndDate.Format(time.RFC3339Nano))
	}
	var result conversations.ListConversationsResponse
	err := r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "conversations"}, query, &result)
	return result, err
}

// LoadConversationRecord returns the lossless export shape, not rendered UI history.
func (r *ControlPlaneChatRunner) LoadConversationRecord(ctx context.Context, conversationID string) (convtypes.ConversationRecord, error) {
	var record convtypes.ConversationRecord
	if strings.TrimSpace(conversationID) == "" {
		return record, errors.New("conversation ID is required")
	}
	err := r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "conversations", conversationID}, url.Values{"format": {"raw"}}, &record)
	return record, err
}

// DeleteConversation deletes persisted history on the daemon. Active executions
// are rejected by the server; transport failures are never retried implicitly.
func (r *ControlPlaneChatRunner) DeleteConversation(ctx context.Context, conversationID string) error {
	if strings.TrimSpace(conversationID) == "" {
		return errors.New("conversation ID is required")
	}
	return r.conversationAPIRequest(ctx, http.MethodDelete, []string{"api", "conversations", conversationID}, nil, nil)
}

// ForkConversation creates a centrally persisted copy with normal lineage and affinity.
func (r *ControlPlaneChatRunner) ForkConversation(ctx context.Context, conversationID string) (string, error) {
	if strings.TrimSpace(conversationID) == "" {
		return "", errors.New("conversation ID is required")
	}
	var result struct {
		Success        bool   `json:"success"`
		ConversationID string `json:"conversation_id"`
	}
	if err := r.conversationAPIRequest(ctx, http.MethodPost, []string{"api", "conversations", conversationID, "fork"}, nil, &result); err != nil {
		return "", err
	}
	if !result.Success || strings.TrimSpace(result.ConversationID) == "" {
		return "", errors.New("server returned an invalid conversation fork response")
	}
	return result.ConversationID, nil
}

func (r *ControlPlaneChatRunner) conversationAPIRequest(ctx context.Context, method string, path []string, query url.Values, result any) error {
	if r == nil || r.client == nil {
		return errors.New("the chat connection is not initialized")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, path...)
	if err != nil {
		return err
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create conversation request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "the conversation request failed; check the conversation before repeating any changes")
	}
	defer response.Body.Close()
	wantStatus := http.StatusOK
	if method == http.MethodDelete {
		wantStatus = http.StatusNoContent
	}
	if response.StatusCode != wantStatus {
		return controlPlaneResponseError(response)
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, maxControlPlaneConversationHistorySize)).Decode(result); err != nil {
			return errors.Wrap(err, "failed to decode conversation response")
		}
	}
	return nil
}

// LoadConversation returns normalized history from the control plane.
func (r *ControlPlaneChatRunner) LoadConversation(ctx context.Context, conversationID string) (ConversationHistory, error) {
	if r == nil || r.client == nil {
		return ConversationHistory{}, errors.New("the chat connection is not initialized")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ConversationHistory{}, errors.New("conversation ID is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID)
	if err != nil {
		return ConversationHistory{}, err
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to parse conversation URL")
	}
	query := parsedEndpoint.Query()
	query.Set("format", "stream")
	parsedEndpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedEndpoint.String(), nil)
	if err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to create conversation request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to load conversation")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ConversationHistory{}, controlPlaneResponseError(response)
	}
	var result controlPlaneConversationResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxControlPlaneConversationHistorySize)).Decode(&result); err != nil {
		return ConversationHistory{}, errors.Wrap(err, "failed to decode conversation")
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
					return nil, errors.Wrap(err, "failed to encode tool result")
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
		return "", errors.Wrap(err, "failed to decode conversation message content")
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
		return ControlPlaneChatSettings{}, errors.New("the chat connection is not initialized")
	}
	settingsURL, err := controlPlaneEndpointURL(r.baseURL, "api", "chat", "settings")
	if err != nil {
		return ControlPlaneChatSettings{}, err
	}
	parsed, err := url.Parse(settingsURL)
	if err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "failed to parse chat settings URL")
	}
	if profile = strings.TrimSpace(profile); profile != "" {
		query := parsed.Query()
		query.Set("profile", profile)
		parsed.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "failed to create chat settings request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "could not load chat settings; check that 'kodelet serve' is running and --server points to it")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ControlPlaneChatSettings{}, controlPlaneResponseError(response)
	}
	var settings ControlPlaneChatSettings
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&settings); err != nil {
		return ControlPlaneChatSettings{}, errors.Wrap(err, "failed to decode chat settings")
	}
	return settings, nil
}

// WorkspaceTarget selects runner-owned discovery without interpreting paths on
// the client. Profile selects the daemon model profile independently of the
// runner's EnvironmentProfile; blank inherits the daemon default, while "default"
// explicitly selects the base profile. ConversationID pins discovery to persisted
// runner/CWD affinity and the stored model profile; clients should omit Profile
// when discovering a saved conversation.
type WorkspaceTarget struct {
	Profile            string                     `json:"profile,omitempty"`
	RunnerID           string                     `json:"runnerId,omitempty"`
	CWD                string                     `json:"cwd,omitempty"`
	EnvironmentProfile string                     `json:"environmentProfile,omitempty"`
	ConversationID     string                     `json:"conversationId,omitempty"`
	Options            *llmtypes.ExecutionOptions `json:"options,omitempty"`
}

// DiscoverWorkspace validates the target and discovers its slash commands on
// the runner without starting a model turn or a workspace run lease.
func (r *ControlPlaneChatRunner) DiscoverWorkspace(ctx context.Context, target WorkspaceTarget) (protocol.WorkspaceDiscoverResult, error) {
	var result protocol.WorkspaceDiscoverResult
	err := r.workspaceDiscovery(ctx, "slash-commands", target, "", &result)
	return result, err
}

// WorkspaceCWDSuggestions resolves directory hints using runner-host paths.
func (r *ControlPlaneChatRunner) WorkspaceCWDSuggestions(ctx context.Context, target WorkspaceTarget, query string) (protocol.WorkspaceCWDHintsResult, error) {
	var result protocol.WorkspaceCWDHintsResult
	err := r.workspaceDiscovery(ctx, "cwd-suggestions", target, query, &result)
	return result, err
}

func (r *ControlPlaneChatRunner) workspaceDiscovery(ctx context.Context, endpoint string, target WorkspaceTarget, query string, result any) error {
	if target.RunnerID == "" && target.ConversationID == "" {
		target.RunnerID = r.runnerID
	}
	if target.RunnerID == "" && target.ConversationID == "" {
		return errors.New("runnerId or conversationId is required for runner workspace discovery")
	}
	if err := (protocol.WorkspaceDiscoverParams{Options: target.Options}).Validate(); err != nil {
		return err
	}
	values := url.Values{}
	if target.Options != nil {
		data, err := json.Marshal(target.Options)
		if err != nil {
			return err
		}
		if len(data) > 16*1024 {
			return errors.New("discovery options exceed 16 KiB")
		}
		values.Set("options", string(data))
	}
	for name, value := range map[string]string{"runnerId": target.RunnerID, "cwd": target.CWD, "profile": target.Profile, "environmentProfile": target.EnvironmentProfile, "conversationId": target.ConversationID, "q": query} {
		if value != "" {
			values.Set(name, value)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "chat", endpoint}, values, result)
}

// StopConversation requests cancellation of central and runner work before the client stream closes.
func (r *ControlPlaneChatRunner) StopConversation(ctx context.Context, conversationID string) error {
	return r.StopConversationTurn(ctx, conversationID, "")
}

// StopConversationTurn requests cancellation of one specific control-plane turn.
func (r *ControlPlaneChatRunner) StopConversationTurn(ctx context.Context, conversationID, turnID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return errors.New("conversation ID is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "stop")
	if err != nil {
		return err
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return errors.Wrap(err, "failed to parse server stop URL")
		}
		query := parsed.Query()
		query.Set("turnId", turnID)
		parsed.RawQuery = query.Encode()
		endpoint = parsed.String()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create server stop request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to stop conversation")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return controlPlaneResponseError(response)
	}
	var result struct {
		Stopped bool `json:"stopped"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return errors.Wrap(err, "failed to decode server stop response")
	}
	if !result.Stopped {
		if turnID != "" {
			// A scoped stop that no longer matches is a successful no-op: the
			// requested turn has already ended or a newer turn now owns the stream.
			return nil
		}
		return errors.New("conversation is not active")
	}
	return nil
}

// SteerConversation queues steering content in the control plane that owns the active provider loop.
func (r *ControlPlaneChatRunner) SteerConversation(ctx context.Context, conversationID, message string, images []string) (bool, error) {
	conversationID = strings.TrimSpace(conversationID)
	message = strings.TrimSpace(message)
	if conversationID == "" {
		return false, errors.New("conversation ID is required")
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
		return false, errors.Wrap(err, "failed to encode steering request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, errors.Wrap(err, "failed to create steering request")
	}
	request.Header.Set("Content-Type", "application/json")
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return false, errors.Wrap(err, "failed to queue steering message")
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
		return false, errors.Wrap(err, "failed to decode steering response")
	}
	if !result.Success || strings.TrimSpace(result.ConversationID) != conversationID {
		return false, errors.New("server returned an invalid steering response")
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
			return true, errors.New("server sent ui input event without a request")
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
			return true, errors.New("server sent ui confirmation event without a request")
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
			return true, errors.New("server sent ui selection event without a request")
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
			return true, errors.New("server sent ui notification event without a request")
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
		return errors.New("interactive prompt response is missing conversation or request id")
	}
	responseURL, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "ui-input", requestID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return errors.Wrap(err, "failed to encode interactive prompt response")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(payload))
	if err != nil {
		return errors.Wrap(err, "failed to create interactive prompt response")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ClientIDHeader, r.clientID)
	if r.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+r.authToken)
	}
	httpResponse, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to send interactive prompt response")
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
