package controlplane

import (
	"context"
	"strings"
	"sync"
	"time"

	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
)

type webUIInputBroker struct {
	conversationID  string
	sink            chat.ChatEventSink
	pending         map[string]chan extensions.UIInputResponse
	mu              sync.Mutex
	transferMu      sync.Mutex
	owner           *uiInputOwner
	closed          bool
	native          *nativeUIState
	shortcutCancels map[string]context.CancelFunc
}

type uiInputOwner struct {
	clientID string
	sink     chat.ChatEventSink
	ctx      context.Context
	stop     func() bool
}

func newWebUIInputBroker(conversationID string, sink chat.ChatEventSink) *webUIInputBroker {
	return &webUIInputBroker{
		conversationID: conversationID,
		sink:           sink,
		pending:        make(map[string]chan extensions.UIInputResponse),
		owner:          &uiInputOwner{sink: sink, ctx: context.Background()},
	}
}

// setOwner transfers only future interactions. Pending prompts are dismissed,
// never replayed to another client with their old response authority.
func (b *webUIInputBroker) setOwner(ctx context.Context, clientID string, sink chat.ChatEventSink) func() {
	owner := &uiInputOwner{clientID: clientID, sink: sink, ctx: ctx}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return func() {}
	}
	if b.owner != nil && b.owner.stop != nil {
		b.owner.stop()
	}
	b.dismissPendingLocked()
	b.invalidateNativeUILocked()
	b.owner = owner
	owner.stop = context.AfterFunc(ctx, func() { b.detachOwner(owner) })
	b.mu.Unlock()
	return func() { owner.stop(); b.detachOwner(owner) }
}

func (b *webUIInputBroker) detachOwner(owner *uiInputOwner) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner == owner {
		b.invalidateNativeUILocked()
		b.owner = nil
		b.dismissPendingLocked()
	}
}

func (b *webUIInputBroker) detachSink(sink chat.ChatEventSink) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner != nil && b.owner.sink == sink {
		b.invalidateNativeUILocked()
		if b.owner.stop != nil {
			b.owner.stop()
		}
		b.owner = nil
		b.dismissPendingLocked()
	}
}

func (b *webUIInputBroker) dismissPendingLocked() {
	for id, cancel := range b.shortcutCancels {
		cancel()
		delete(b.shortcutCancels, id)
	}
	for id, response := range b.pending {
		delete(b.pending, id)
		select {
		case response <- extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed, Reason: "interactive client detached or ownership changed"}:
		default:
		}
	}
}

func (b *webUIInputBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.invalidateNativeUILocked()
	if b.owner != nil && b.owner.stop != nil {
		b.owner.stop()
	}
	b.owner = nil
	b.dismissPendingLocked()
}

func (b *webUIInputBroker) Input(ctx context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.sink == nil {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "web ui input is not available"}, nil
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = extensions.NewUIInputRequestID()
	}

	return b.prompt(ctx, request.ID, chat.ChatEvent{
		Kind:           "ui-input-request",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UIInput: &chat.UIInputEvent{
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

	return b.prompt(ctx, request.ID, chat.ChatEvent{
		Kind:           "ui-confirm-request",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UIConfirm: &chat.UIConfirmEvent{
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

	return b.prompt(ctx, request.ID, chat.ChatEvent{
		Kind:           "ui-select-request",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UISelect: &chat.UISelectEvent{
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
	b.mu.Lock()
	owner := b.owner
	b.mu.Unlock()
	if owner == nil || owner.ctx.Err() != nil {
		return unavailableUIResponse("no interactive client owns this execution"), nil
	}
	if err := owner.sink.Send(chat.ChatEvent{
		Kind:           "ui-notification",
		ConversationID: b.conversationID,
		Role:           "assistant",
		UINotify: &chat.UINotifyEvent{
			Title:   request.Title,
			Message: request.Message,
		},
	}); err != nil {
		return extensions.UIInputResponse{}, err
	}
	return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}, nil
}

func (b *webUIInputBroker) prompt(ctx context.Context, requestID string, event chat.ChatEvent) (extensions.UIInputResponse, error) {
	responseCh := make(chan extensions.UIInputResponse, 1)

	b.mu.Lock()
	owner := b.owner
	if b.closed || owner == nil || owner.ctx.Err() != nil {
		b.mu.Unlock()
		return unavailableUIResponse("no interactive client owns this execution"), nil
	}
	if owner.clientID != "" {
		// Extensions may reuse IDs. Never reuse a client-facing response capability.
		requestID = extensions.NewUIInputRequestID()
		if event.UIInput != nil {
			event.UIInput.ID = requestID
		}
		if event.UIConfirm != nil {
			event.UIConfirm.ID = requestID
		}
		if event.UISelect != nil {
			event.UISelect.ID = requestID
		}
	}
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
		if b.pending[requestID] == responseCh {
			delete(b.pending, requestID)
		}
		b.mu.Unlock()
		if owner.clientID != "" {
			// Clear a visible prompt on takeover, expiry, or cancellation. This is
			// sent only to its original owner, never to an observing client.
			_ = owner.sink.Send(chat.ChatEvent{Kind: "ui-request-end", ConversationID: b.conversationID, UIRequestID: requestID})
		}
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
	if err := owner.sink.Send(event); err != nil {
		b.detachOwner(owner)
		return extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed, Reason: "interactive client is unavailable"}, nil
	}

	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed}, ctx.Err()
	case <-owner.ctx.Done():
		return extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed, Reason: "interactive client disconnected"}, nil
	case <-timer.C:
		return extensions.UIInputResponse{Status: extensions.UIInputStatusTimeout}, nil
	case response := <-responseCh:
		if response.Status == "" {
			response.Status = extensions.UIInputStatusDismissed
		}
		return response, nil
	}
}

func (b *webUIInputBroker) Respond(requestID string, response extensions.UIInputResponse) bool {
	return b.respondOwned("", requestID, response)
}

func (b *webUIInputBroker) respondOwned(clientID, requestID string, response extensions.UIInputResponse) bool {
	if b == nil {
		return false
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.owner == nil || b.owner.clientID != clientID || b.owner.ctx.Err() != nil {
		return false
	}
	responseCh, ok := b.pending[requestID]
	if !ok {
		return false
	}
	select {
	case responseCh <- response:
		delete(b.pending, requestID)
		return true
	default:
		return false
	}
}
