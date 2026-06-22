package webui

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"io"
	"net/http"
	"strings"
	"sync"

	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

type (
	ChatRequest              = chat.ChatRequest
	ChatContentBlock         = chat.ChatContentBlock
	ChatImageSource          = chat.ChatImageSource
	ChatImageURLSource       = chat.ChatImageURLSource
	ChatEvent                = chat.ChatEvent
	UIInputEvent             = chat.UIInputEvent
	UIConfirmEvent           = chat.UIConfirmEvent
	UISelectEvent            = chat.UISelectEvent
	UINotifyEvent            = chat.UINotifyEvent
	ChatEventSink            = chat.ChatEventSink
	ChatRunner               = chat.ChatRunner
	extensionRuntimeProvider = chat.ExtensionRuntimeProvider
	DefaultChatRunner        = chat.DefaultChatRunner
)

var NewDefaultChatRunner = chat.NewDefaultChatRunner

type webUIChatRunner struct {
	defaultCWD        string
	extensionRuntimes extensionRuntimeProvider
	server            *Server
}

func (r *webUIChatRunner) Run(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
	conversationID := strings.TrimSpace(req.ConversationID)
	if r != nil && r.server != nil && conversationID != "" {
		if broker := r.server.uiInputBrokerForRun(conversationID); broker != nil {
			ctx = extensions.ContextWithUIInputBroker(ctx, broker)
		}
	}
	if r == nil {
		return chat.RunDefaultChat(ctx, req, sink, "", nil)
	}
	return chat.RunDefaultChat(ctx, req, sink, r.defaultCWD, r.extensionRuntimes)
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

	message, imageInputs, err := chat.NormalizeRequest(req)
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
	run.uiInput = newWebUIInputBroker(conversationID, broadcastingSink)

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
