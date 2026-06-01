package webui

import (
	"context"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/extensions"
)

type webUIInputBroker struct {
	conversationID string
	sink           ChatEventSink
	pending        map[string]chan extensions.UIInputResponse
	mu             sync.Mutex
}

func newWebUIInputBroker(conversationID string, sink ChatEventSink) *webUIInputBroker {
	return &webUIInputBroker{
		conversationID: conversationID,
		sink:           sink,
		pending:        make(map[string]chan extensions.UIInputResponse),
	}
}

func (b *webUIInputBroker) Input(ctx context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.sink == nil {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "web ui input is not available"}, nil
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = extensions.NewUIInputRequestID()
	}

	return b.prompt(ctx, request.ID, ChatEvent{
		Kind:           "ui-input-request",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UIInput: &UIInputEvent{
			ID:               request.ID,
			Title:            request.Title,
			HelpText:         request.HelpText,
			Message:          request.Message,
			Placeholder:      request.Placeholder,
			DefaultValue:     request.DefaultValue,
			SubmitButtonText: request.SubmitButtonText,
			CancelButtonText: request.CancelButtonText,
			Required:         request.Required,
			Secret:           request.Secret,
		},
	})
}

func (b *webUIInputBroker) Confirm(ctx context.Context, request extensions.UIConfirmRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.sink == nil {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "web ui confirm is not available"}, nil
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = extensions.NewUIInputRequestID()
	}

	return b.prompt(ctx, request.ID, ChatEvent{
		Kind:           "ui-confirm-request",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UIConfirm: &UIConfirmEvent{
			ID:                request.ID,
			Title:             request.Title,
			Message:           request.Message,
			ConfirmButtonText: request.ConfirmButtonText,
			CancelButtonText:  request.CancelButtonText,
		},
	})
}

func (b *webUIInputBroker) Select(ctx context.Context, request extensions.UISelectRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.sink == nil {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "web ui select is not available"}, nil
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = extensions.NewUIInputRequestID()
	}

	return b.prompt(ctx, request.ID, ChatEvent{
		Kind:           "ui-select-request",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UISelect: &UISelectEvent{
			ID:               request.ID,
			Title:            request.Title,
			Message:          request.Message,
			Options:          append([]string{}, request.Options...),
			SubmitButtonText: request.SubmitButtonText,
			CancelButtonText: request.CancelButtonText,
		},
	})
}

func (b *webUIInputBroker) Notify(ctx context.Context, request extensions.UINotifyRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.sink == nil {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "web ui notify is not available"}, nil
	}
	if err := ctx.Err(); err != nil {
		return extensions.UIInputResponse{}, err
	}
	if err := b.sink.Send(ChatEvent{
		Kind:           "ui-notification",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UINotify: &UINotifyEvent{
			Title:   request.Title,
			Message: request.Message,
		},
	}); err != nil {
		return extensions.UIInputResponse{}, err
	}
	return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}, nil
}

func (b *webUIInputBroker) prompt(ctx context.Context, requestID string, event ChatEvent) (extensions.UIInputResponse, error) {
	responseCh := make(chan extensions.UIInputResponse, 1)

	b.mu.Lock()
	if previous, ok := b.pending[requestID]; ok {
		select {
		case previous <- extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed}:
		default:
		}
	}
	b.pending[requestID] = responseCh
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
	}()

	if event.Kind == "" {
		event.Kind = "ui-input-request"
	}
	if event.ConversationID == "" {
		event.ConversationID = b.conversationID
	}
	if event.Role == "" {
		event.Role = "assistant"
	}
	if err := b.sink.Send(event); err != nil {
		return extensions.UIInputResponse{}, err
	}

	select {
	case <-ctx.Done():
		return extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed}, ctx.Err()
	case response := <-responseCh:
		if response.Status == "" {
			response.Status = extensions.UIInputStatusDismissed
		}
		return response, nil
	}
}

func (b *webUIInputBroker) Respond(requestID string, response extensions.UIInputResponse) bool {
	if b == nil {
		return false
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false
	}
	b.mu.Lock()
	responseCh, ok := b.pending[requestID]
	b.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case responseCh <- response:
		return true
	default:
		return false
	}
}
