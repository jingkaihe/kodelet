package controlplane

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
)

type runnerListResponse struct {
	Runners []runnerregistry.Runner `json:"runners"`
}

func (s *Server) handleListRunners(w http.ResponseWriter, _ *http.Request) {
	if s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner registry is unavailable", nil)
		return
	}
	s.writeJSONResponse(w, runnerListResponse{Runners: s.runnerRegistry.Runners()})
}

func (s *Server) handleGetRunner(w http.ResponseWriter, r *http.Request) {
	if s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner registry is unavailable", nil)
		return
	}
	runner, ok := s.runnerRegistry.Runner(strings.TrimSpace(mux.Vars(r)["id"]))
	if !ok {
		s.writeErrorResponse(w, http.StatusNotFound, "runner not found", nil)
		return
	}
	s.writeJSONResponse(w, runner)
}

func (s *Server) handleDeleteRunner(w http.ResponseWriter, r *http.Request) {
	if s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner registry is unavailable", nil)
		return
	}
	runnerID := strings.TrimSpace(mux.Vars(r)["id"])
	if runnerID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "runner ID is required", nil)
		return
	}
	force := false
	if raw := strings.TrimSpace(r.URL.Query().Get("force")); raw != "" {
		var err error
		force, err = strconv.ParseBool(raw)
		if err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "force must be a boolean", nil)
			return
		}
	}

	result, err := s.runnerRegistry.RemoveRunner(r.Context(), runnerID, force)
	if err != nil {
		switch {
		case stdErrors.Is(err, runnerregistry.ErrRunnerNotFound):
			s.writeErrorResponse(w, http.StatusNotFound, err.Error(), nil)
		case stdErrors.Is(err, runnerregistry.ErrRunnerConnected), stdErrors.Is(err, runnerregistry.ErrRunnerActiveRun):
			s.writeErrorResponse(w, http.StatusConflict, err.Error(), nil)
		default:
			s.writeErrorResponse(w, http.StatusInternalServerError, "failed to remove runner", err)
		}
		return
	}
	s.writeJSONResponse(w, result)
}

var runnerUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	Subprotocols:    []string{protocol.Subprotocol},
	CheckOrigin:     terminalOriginAllowed,
}

func (s *Server) runnerUpgrader() websocket.Upgrader {
	upgrader := runnerUpgrader
	upgrader.CheckOrigin = s.terminalOriginAllowed
	return upgrader
}

func (s *Server) handleRunnerWebsocket(w http.ResponseWriter, r *http.Request) {
	if !supportsRunnerSubprotocol(r) {
		s.writeErrorResponse(w, http.StatusBadRequest, "runner websocket subprotocol is required", nil)
		return
	}
	if s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner registry is unavailable", nil)
		return
	}
	principal, ok := runnerPrincipalFromContext(r.Context())
	if !ok {
		if s.config != nil && s.config.resolvedRunnerAuthMode() != RunnerAuthModeNone {
			s.writeAuthError(w, r, http.StatusUnauthorized, "runner authentication required")
			return
		}
		principal = runnerregistry.RegistrationPrincipal{Mode: runnerregistry.RegistrationAuthLegacy}
	}

	upgrader := s.runnerUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.G(r.Context()).WithError(err).Warn("failed to upgrade runner websocket")
		return
	}
	if conn.Subprotocol() != protocol.Subprotocol {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseProtocolError, "runner subprotocol negotiation failed"), time.Now().Add(5*time.Second))
		_ = conn.Close()
		return
	}

	session := runnerregistry.NewAuthenticatedSession(s.runnerRegistry, s, principal)
	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{
		RequestPrefix: "server",
		Handler:       session,
		Notifications: session,
	})
	if err != nil {
		_ = conn.Close()
		logger.G(r.Context()).WithError(err).Warn("failed to create runner rpc peer")
		return
	}
	session.Attach(peer)
	ctx := s.chatExecutionContext(r.Context())
	if err := peer.Start(ctx); err != nil {
		_ = peer.Close()
		logger.G(r.Context()).WithError(err).Warn("failed to start runner rpc peer")
		return
	}
	<-peer.TransportDone()
	session.Detach(peer.Err())
}

func supportsRunnerSubprotocol(r *http.Request) bool {
	for _, subprotocol := range websocket.Subprotocols(r) {
		if strings.TrimSpace(subprotocol) == protocol.Subprotocol {
			return true
		}
	}
	return false
}

// HandleRunnerUIRequest routes runner-owned extension UI through the client attached to the run.
func (s *Server) HandleRunnerUIRequest(ctx context.Context, runnerID, method string, params json.RawMessage) (any, *protocol.RPCError) {
	switch method {
	case protocol.MethodUIInput:
		value, rpcErr := decodeRunnerUIParams[runnerpayload.UIInputParams](s, runnerID, params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		broker := s.runnerUIBroker(value.RunID)
		if broker == nil {
			return unavailableUIResponse("web ui input is not available"), nil
		}
		response, err := broker.Input(ctx, value.Request)
		return runnerUIResponse(response, err)
	case protocol.MethodUIConfirm:
		value, rpcErr := decodeRunnerUIParams[runnerpayload.UIConfirmParams](s, runnerID, params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		broker := s.runnerUIBroker(value.RunID)
		if broker == nil {
			return unavailableUIResponse("web ui confirmation is not available"), nil
		}
		response, err := broker.Confirm(ctx, value.Request)
		return runnerUIResponse(response, err)
	case protocol.MethodUISelect:
		value, rpcErr := decodeRunnerUIParams[runnerpayload.UISelectParams](s, runnerID, params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		broker := s.runnerUIBroker(value.RunID)
		if broker == nil {
			return unavailableUIResponse("web ui selection is not available"), nil
		}
		response, err := broker.Select(ctx, value.Request)
		return runnerUIResponse(response, err)
	case protocol.MethodUINotify:
		value, rpcErr := decodeRunnerUIParams[runnerpayload.UINotifyParams](s, runnerID, params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		broker := s.runnerUIBroker(value.RunID)
		if broker == nil {
			return unavailableUIResponse("web ui notification is not available"), nil
		}
		response, err := broker.Notify(ctx, value.Request)
		return runnerUIResponse(response, err)
	case protocol.MethodUITranscriptAppend:
		value, rpcErr := decodeRunnerUIParams[runnerpayload.UITranscriptAppendParams](s, runnerID, params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		_ = value
		return extensions.UITranscriptAppendResponse{Reason: "web ui extension transcript proxy is not available"}, nil
	case protocol.MethodUIWidgetSet,
		protocol.MethodUIWidgetFrame,
		protocol.MethodUIWidgetRemove,
		protocol.MethodUISurfaceOpen,
		protocol.MethodUISurfaceFrame,
		protocol.MethodUISurfaceClose:
		if rpcErr := validateRunnerUIRun(s, runnerID, params); rpcErr != nil {
			return nil, rpcErr
		}
		return extensions.UIFrameResponse{Reason: "web ui persistent extension surfaces are not available"}, nil
	default:
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeMethodNotFound, Message: "runner UI method not found"}
	}
}

func decodeRunnerUIParams[T any](s *Server, runnerID string, params json.RawMessage) (T, *protocol.RPCError) {
	var value T
	if err := json.Unmarshal(params, &value); err != nil {
		return value, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value, &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: err.Error()}
	}
	if rpcErr := validateRunnerUIRun(s, runnerID, payload); rpcErr != nil {
		return value, rpcErr
	}
	return value, nil
}

func validateRunnerUIRun(s *Server, runnerID string, params json.RawMessage) *protocol.RPCError {
	var envelope struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	envelope.RunID = strings.TrimSpace(envelope.RunID)
	if envelope.RunID == "" {
		return &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "runId is required"}
	}
	if s == nil || s.runnerRegistry == nil {
		return &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "runner registry is unavailable"}
	}
	run, ok := s.runnerRegistry.Run(envelope.RunID)
	if !ok || run.RunnerID != runnerID || (run.Status != runnerregistry.RunStatusOpening && run.Status != runnerregistry.RunStatusRunning) {
		return &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: "runner UI request belongs to an inactive run"}
	}
	return nil
}

func (s *Server) runnerUIBroker(runID string) *webUIInputBroker {
	if s == nil || s.runnerRegistry == nil {
		return nil
	}
	run, ok := s.runnerRegistry.Run(strings.TrimSpace(runID))
	if !ok {
		return nil
	}
	return s.uiInputBrokerForRun(run.ConversationID)
}

func runnerUIResponse(response extensions.UIInputResponse, err error) (any, *protocol.RPCError) {
	if err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: err.Error()}
	}
	if response.Status == "" {
		response.Status = extensions.UIInputStatusDismissed
	}
	return response, nil
}

func unavailableUIResponse(reason string) extensions.UIInputResponse {
	return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: reason}
}
