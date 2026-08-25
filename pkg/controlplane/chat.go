package controlplane

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

type serverChatRunner struct {
	runner *chat.DefaultChatRunner
	server *Server
}

func (r *serverChatRunner) Run(ctx context.Context, req chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
	conversationID := strings.TrimSpace(req.ConversationID)
	hasRunnerAffinity := false
	if r != nil && r.server != nil && r.server.runnerRegistry != nil && conversationID != "" {
		affinity, ok, err := r.server.runnerRegistry.ResolveConversationAffinity(ctx, conversationID)
		if err != nil {
			return conversationID, err
		}
		hasRunnerAffinity = ok
		if ok {
			if strings.TrimSpace(req.RunnerID) == "" {
				req.RunnerID = affinity.RunnerID
			}
			if strings.TrimSpace(req.EnvironmentProfile) == "" {
				req.EnvironmentProfile = affinity.EnvironmentProfile
			}
		}
	}
	if r != nil && r.server != nil && !r.server.controlPlaneWorkspaceEnabled() {
		if strings.TrimSpace(req.RunnerID) == "" {
			return conversationID, errors.New(controlPlaneWorkspaceDisabledMessage + "; select a workspace runner")
		}
		if conversationID != "" && !hasRunnerAffinity {
			if r.server.conversationService == nil {
				return conversationID, errors.New("conversation service is unavailable")
			}
			_, err := r.server.conversationService.GetConversation(ctx, conversationID)
			switch {
			case err == nil:
				return conversationID, errors.New(controlPlaneWorkspaceDisabledMessage + "; existing local conversations are read-only")
			case stdErrors.Is(err, convtypes.ErrConversationNotFound):
				// A client may allocate the conversation ID before the first turn.
			default:
				return conversationID, errors.Wrap(err, "failed to inspect conversation before selecting a runner")
			}
		}
	}
	if r != nil && r.server != nil && conversationID != "" && chatSupportsInteractiveUI(req) {
		if broker := r.server.uiInputBrokerForRun(conversationID); broker != nil {
			ctx = extensions.ContextWithUIInputBroker(ctx, broker)
		}
	}
	var resultConversationID string
	var runErr error
	if r == nil || r.runner == nil {
		resultConversationID, runErr = chat.RunDefaultChat(ctx, req, sink, "", nil)
	} else {
		resultConversationID, runErr = r.runner.Run(ctx, req, sink)
	}
	if strings.TrimSpace(resultConversationID) == "" {
		resultConversationID = conversationID
	}
	if strings.TrimSpace(req.RunnerID) != "" && r != nil && r.server != nil {
		if err := r.server.commitRunnerAffinity(context.WithoutCancel(ctx), resultConversationID); err != nil && runErr == nil {
			runErr = err
		}
	}
	return resultConversationID, runErr
}

func (r *serverChatRunner) ResolveEnvironment(ctx context.Context, req chat.ChatRequest, conversationID string, _ llmtypes.Config, _ string) (agentenv.Environment, error) {
	runnerID := strings.TrimSpace(req.RunnerID)
	if runnerID == "" {
		return nil, errors.New("runner id is required")
	}
	if r == nil || r.server == nil || r.server.runnerRegistry == nil {
		return nil, errors.New("runner registry is unavailable")
	}
	runner, ok := r.server.runnerRegistry.Runner(runnerID)
	if !ok {
		return nil, errors.New("runner not found")
	}
	if runner.Status == runnerregistry.RunnerStatusIncompatible {
		if runner.CompatibilityError != "" {
			return nil, errors.New(runner.CompatibilityError)
		}
		return nil, errors.New("runner is incompatible with this control plane")
	}
	if !runner.Connected {
		return nil, errors.New("runner is offline")
	}
	capabilities := protocol.ClientCapabilities{}
	if req.ClientCapabilities != nil {
		capabilities.InteractiveUI = req.ClientCapabilities.InteractiveUI
		capabilities.PersistentSurfaces = req.ClientCapabilities.PersistentSurfaces
	}
	return agentenv.NewRemoteEnvironment(
		r.server.runnerRegistry,
		runnerID,
		agentenv.WithRemoteClientCapabilities(capabilities),
	), nil
}

func (s *Server) commitRunnerAffinity(ctx context.Context, conversationID string) error {
	if s == nil || s.runnerRegistry == nil || s.conversationService == nil || strings.TrimSpace(conversationID) == "" {
		return nil
	}
	if _, err := s.conversationService.GetConversation(ctx, conversationID); err != nil {
		if stdErrors.Is(err, convtypes.ErrConversationNotFound) {
			s.runnerRegistry.ReleasePendingConversationAffinity(conversationID)
		}
		return err
	}
	return s.runnerRegistry.CommitConversationAffinity(ctx, conversationID)
}

func chatSupportsInteractiveUI(req chat.ChatRequest) bool {
	return req.ClientCapabilities != nil && req.ClientCapabilities.InteractiveUI
}

func (r *serverChatRunner) Close() error {
	if r == nil || r.runner == nil {
		return nil
	}
	return r.runner.Close()
}

func (r *serverChatRunner) CloseConversation(conversationID string) error {
	if r == nil || r.runner == nil {
		return nil
	}
	return r.runner.CloseConversation(conversationID)
}

type ndjsonEventSink struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

type subscriberEventSink struct {
	ch     chan chat.ChatEvent
	mu     sync.RWMutex
	closed bool
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

func (s *ndjsonEventSink) Send(event chat.ChatEvent) error {
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

func (s *ndjsonEventSink) KeepAlive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write([]byte("\n")); err != nil {
		return errors.Wrap(err, "failed to write chat stream keepalive")
	}
	s.flusher.Flush()
	return nil
}

func newSubscriberEventSink() *subscriberEventSink {
	return &subscriberEventSink{ch: make(chan chat.ChatEvent, 128)}
}

func (s *subscriberEventSink) Send(event chat.ChatEvent) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return errors.New("subscriber is closed")
	}
	select {
	case s.ch <- event:
		return nil
	default:
		return errors.New("subscriber buffer full")
	}
}

func (s *subscriberEventSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	requestCtx := r.Context()

	var req chat.ChatRequest
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
	registeredConversationID := conversationID

	ctx, cancel := context.WithCancel(s.chatExecutionContext(requestCtx))
	run := newActiveChatRun(cancel)
	run.turnID = strings.TrimSpace(req.TurnID)
	if !s.registerActiveChat(conversationID, run) {
		cancel()
		s.writeErrorResponse(w, http.StatusConflict, "conversation already has an active run", nil)
		return
	}
	defer s.unregisterActiveChat(registeredConversationID, run)
	defer cancel()

	broadcastingSink := &broadcastingEventSink{
		primary:        sink,
		broadcast:      s.broadcastChatEvent,
		conversationID: conversationID,
	}
	run.uiInput = newWebUIInputBroker(conversationID, broadcastingSink)

	s.broadcastChatEvent(conversationID, chat.ChatEvent{
		Kind:           "conversation",
		ConversationID: conversationID,
		Role:           "assistant",
	})
	var userContent any = message
	if contentBlocks := chat.ContentBlocksForUserInput(message, imageInputs); len(contentBlocks) > 0 {
		userContent = contentBlocks
	}
	s.broadcastChatEvent(conversationID, chat.ChatEvent{
		Kind:           "user-message",
		ConversationID: conversationID,
		Role:           "user",
		Content:        userContent,
	})

	conversationID, runErr := s.chatRunner.Run(ctx, req, broadcastingSink)
	if strings.TrimSpace(conversationID) == "" {
		conversationID = registeredConversationID
	}
	if runErr != nil {
		if stdErrors.Is(runErr, io.ErrClosedPipe) || stdErrors.Is(runErr, context.Canceled) {
			s.unregisterActiveChat(registeredConversationID, run)
			s.broadcastChatEvent(conversationID, chat.ChatEvent{
				Kind:           "done",
				ConversationID: conversationID,
				Role:           "assistant",
			})
			logger.G(requestCtx).WithError(runErr).Debug("chat stream disconnected")
			return
		}

		logger.G(ctx).WithError(runErr).Error("chat request failed")
		s.unregisterActiveChat(registeredConversationID, run)
		s.broadcastChatEvent(conversationID, chat.ChatEvent{
			Kind:           "error",
			ConversationID: conversationID,
			Role:           "assistant",
			Error:          runErr.Error(),
		})
		_ = sink.Send(chat.ChatEvent{
			Kind:           "error",
			ConversationID: conversationID,
			Role:           "assistant",
			Error:          runErr.Error(),
		})
		return
	}

	s.unregisterActiveChat(registeredConversationID, run)
	s.broadcastChatEvent(conversationID, chat.ChatEvent{
		Kind:           "done",
		ConversationID: conversationID,
		Role:           "assistant",
	})
	_ = sink.Send(chat.ChatEvent{
		Kind:           "done",
		ConversationID: conversationID,
		Role:           "assistant",
	})
}

type broadcastingEventSink struct {
	primary        chat.ChatEventSink
	broadcast      func(string, chat.ChatEvent)
	conversationID string
}

func (s *broadcastingEventSink) Send(event chat.ChatEvent) error {
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
