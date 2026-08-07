package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
)

const maxControlPlaneChatEventSize = 16 << 20

// ControlPlaneChatRunner streams chat turns through kodelet serve for one selected runner.
type ControlPlaneChatRunner struct {
	baseURL   string
	chatURL   string
	authToken string
	runnerID  string
	client    *http.Client
}

// NewControlPlaneChatRunner creates a TUI-compatible remote chat transport.
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
	if runnerID == "" {
		return nil, errors.New("runner id is required")
	}
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
	request.RunnerID = r.runnerID
	request.CWD = ""
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

	conversationID := strings.TrimSpace(request.ConversationID)
	var streamErr error
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), maxControlPlaneChatEventSize)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event ChatEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return conversationID, errors.Wrap(err, "failed to decode control-plane chat event")
		}
		if strings.TrimSpace(event.ConversationID) != "" {
			conversationID = strings.TrimSpace(event.ConversationID)
		}
		handled, err := r.handleUIEvent(ctx, conversationID, event)
		if err != nil {
			return conversationID, err
		}
		if handled {
			continue
		}
		if err := sink.Send(event); err != nil {
			return conversationID, err
		}
		if event.Kind == "error" && strings.TrimSpace(event.Error) != "" {
			streamErr = errors.New(event.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		return conversationID, errors.Wrap(err, "failed to read control-plane chat stream")
	}
	return conversationID, streamErr
}

func controlPlaneBaseURL(rawServer string) (string, error) {
	rawServer = strings.TrimSpace(rawServer)
	if rawServer == "" {
		return "", errors.New("control-plane URL is required")
	}
	parsed, err := url.Parse(rawServer)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse control-plane URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("control-plane URL must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("control-plane URL must contain only scheme, host, and an optional base path")
	}
	if parsed.Scheme == "http" && !controlPlaneLoopbackHostname(parsed.Hostname()) {
		return "", errors.New("remote control-plane connections require https; http is allowed only for loopback servers")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}

func controlPlaneEndpointURL(baseURL string, parts ...string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse control-plane URL")
	}
	segments := append([]string{parsed.Path}, parts...)
	parsed.Path = path.Join(segments...)
	parsed.RawPath = ""
	return parsed.String(), nil
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
	if err != nil && ctx.Err() == nil {
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

func controlPlaneLoopbackHostname(hostname string) bool {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func controlPlaneResponseError(response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var value struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(payload, &value) == nil {
		if message := firstNonEmptyString(value.Error, value.Message); message != "" {
			return errors.Errorf("control plane returned HTTP %d: %s", response.StatusCode, message)
		}
	}
	return errors.Errorf("control plane returned HTTP %d", response.StatusCode)
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
