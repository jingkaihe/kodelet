package registry

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

// UIRequestRouter handles runner-originated extension UI requests in the control plane.
type UIRequestRouter interface {
	HandleRunnerUIRequest(ctx context.Context, runnerID string, method string, params json.RawMessage) (any, *protocol.RPCError)
}

// Session binds one WebSocket peer to a generation-fenced runner registration.
type Session struct {
	registry     *Registry
	ui           UIRequestRouter
	mu           sync.RWMutex
	peer         Link
	runnerID     string
	connectionID string
	generation   int64
	registered   bool
}

// NewSession creates a connection handler. Attach must be called before the peer starts.
func NewSession(registry *Registry, ui UIRequestRouter) *Session {
	return &Session{registry: registry, ui: ui}
}

// Attach binds the symmetric peer used for registration and reverse calls.
func (s *Session) Attach(peer Link) {
	s.mu.Lock()
	s.peer = peer
	s.mu.Unlock()
}

// HandleRequest implements protocol.RequestHandler.
func (s *Session) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, *protocol.RPCError) {
	if method == protocol.MethodRunnerRegister {
		return s.handleRegister(params)
	}
	runnerID, registered := s.identity()
	if !registered {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidRequest, Message: "runner.register must be the first request"}
	}
	if isUIRequest(method) {
		if s.ui == nil {
			return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "runner UI routing is unavailable"}
		}
		return s.ui.HandleRunnerUIRequest(ctx, runnerID, method, params)
	}
	return nil, &protocol.RPCError{Code: protocol.ErrorCodeMethodNotFound, Message: "runner request method not found"}
}

// HandleNotification implements protocol.NotificationHandler.
func (s *Session) HandleNotification(ctx context.Context, method string, params json.RawMessage) {
	runnerID, connectionID, generation, registered := s.connectionIdentity()
	if !registered || s.registry == nil {
		return
	}
	switch method {
	case protocol.MethodRunnerHeartbeat:
		value, err := decodeParams[protocol.HeartbeatParams](params)
		if err == nil {
			err = s.registry.Heartbeat(runnerID, connectionID, generation, value)
		}
		if err != nil {
			logger.G(ctx).
				WithField("runner_id", runnerID).
				WithField("generation", generation).
				WithError(err).
				Warn("failed to apply runner heartbeat")
		}
	case protocol.MethodRunnerManifestChanged:
		if value, err := decodeParams[protocol.ManifestChangedParams](params); err == nil {
			_ = s.registry.ManifestChanged(runnerID, connectionID, generation, value)
		}
	case protocol.MethodRunnerGoodbye:
		s.registry.Detach(runnerID, connectionID, generation, nil)
	case protocol.MethodRunEnvironmentError:
		if value, err := decodeParams[protocol.EnvironmentErrorParams](params); err == nil {
			_ = s.registry.EnvironmentError(runnerID, connectionID, generation, value)
		}
	case protocol.MethodToolUpdate:
		if value, err := decodeParams[runnerpayload.ToolUpdateParams](params); err == nil {
			_ = s.registry.DeliverToolUpdate(runnerID, connectionID, generation, value)
		}
	}
}

// Detach removes this session only when it remains the current runner generation.
func (s *Session) Detach(cause error) {
	runnerID, connectionID, generation, registered := s.connectionIdentity()
	if registered && s.registry != nil {
		s.registry.Detach(runnerID, connectionID, generation, cause)
	}
}

func (s *Session) handleRegister(params json.RawMessage) (any, *protocol.RPCError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registered {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidRequest, Message: "runner is already registered on this connection"}
	}
	if s.registry == nil || s.peer == nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: "runner session is not initialized"}
	}
	value, err := decodeParams[protocol.RegisterParams](params)
	if err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	result, err := s.registry.Register(value, s.peer)
	if err != nil {
		return nil, rpcErrorFor(err)
	}
	s.runnerID = result.RunnerID
	s.connectionID = result.ConnectionID
	s.generation = result.Generation
	s.registered = true
	return result, nil
}

func (s *Session) identity() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runnerID, s.registered
}

func (s *Session) connectionIdentity() (string, string, int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runnerID, s.connectionID, s.generation, s.registered
}

func isUIRequest(method string) bool {
	switch strings.TrimSpace(method) {
	case protocol.MethodUIInput,
		protocol.MethodUIConfirm,
		protocol.MethodUISelect,
		protocol.MethodUINotify,
		protocol.MethodUIWidgetSet,
		protocol.MethodUIWidgetFrame,
		protocol.MethodUIWidgetRemove,
		protocol.MethodUITranscriptAppend,
		protocol.MethodUISurfaceOpen,
		protocol.MethodUISurfaceFrame,
		protocol.MethodUISurfaceClose:
		return true
	default:
		return false
	}
}

func rpcErrorFor(err error) *protocol.RPCError {
	if err == nil {
		return nil
	}
	message := err.Error()
	code := protocol.ErrorCodeInvalidParams
	switch {
	case errors.Is(err, ErrRunnerNotFound):
		code = protocol.ErrorCodeStale
	case strings.Contains(message, "busy"), strings.Contains(message, "active run"):
		code = protocol.ErrorCodeBusy
	case strings.Contains(message, "stale"), strings.Contains(message, "generation"):
		code = protocol.ErrorCodeStale
	case strings.Contains(message, "another") || strings.Contains(message, "already"):
		code = protocol.ErrorCodeConflict
	}
	rpcErr := &protocol.RPCError{Code: code, Message: message}
	if errors.Is(err, ErrRunnerNotFound) {
		rpcErr.Data = protocol.RPCErrorData{Reason: protocol.ErrorReasonRunnerNotFound}
	}
	return rpcErr
}
