package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// An attachment is a live, authenticated capability, not conversation metadata.
// Its connection, owner, conversation, and runner generation are immutable.
type sessionExtensionAttachment struct {
	server         *Server
	peer           *protocol.Peer
	ctx            context.Context
	cancel         context.CancelFunc
	principalID    string
	credentialID   string
	clientID       string
	conversationID string
	identity       runnerregistry.UIRequestIdentity
	descriptor     protocol.SessionExtensions
	channels       map[sessionExtensionChannel]bool // true once permanently closed
	closed         bool
}

type sessionExtensionChannel struct {
	runID       string
	extensionID string
}

type sessionExtensionContextKey struct{}

func (s *Server) handleSessionExtensionsWebsocket(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	clientID := strings.TrimSpace(r.Header.Get(chat.ClientIDHeader))
	if !ok || !validUIClientID(clientID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "session extensions require an authenticated client identity", nil)
		return
	}
	if !slices.Contains(websocket.Subprotocols(r), protocol.SessionExtensionsSubprotocol) {
		s.writeErrorResponse(w, http.StatusBadRequest, "session extension websocket subprotocol is required", nil)
		return
	}
	if s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner registry is unavailable", nil)
		return
	}
	upgrader := s.runnerUpgrader()
	upgrader.Subprotocols = []string{protocol.SessionExtensionsSubprotocol}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	attachment := &sessionExtensionAttachment{
		server: s, ctx: ctx, cancel: cancel, principalID: principal.ID,
		credentialID: principal.CredentialID, clientID: clientID,
		channels: make(map[sessionExtensionChannel]bool),
	}
	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{RequestPrefix: "session-server", Handler: attachment})
	if err != nil {
		cancel()
		_ = conn.Close()
		return
	}
	attachment.peer = peer
	if err := peer.Start(ctx); err != nil {
		attachment.close()
		return
	}
	// HTTP Shutdown does not close hijacked connections.
	stop := context.AfterFunc(s.chatExecutionContext(r.Context()), cancel)
	defer stop()
	<-peer.TransportDone()
	attachment.close()
}

func (a *sessionExtensionAttachment) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, *protocol.RPCError) {
	if method == protocol.MethodSessionExtensionsAttach {
		var request struct {
			ConversationID string   `json:"conversationId"`
			RunnerID       string   `json:"runnerId"`
			ExtensionIDs   []string `json:"extensionIds"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, sessionExtensionRPCError(err)
		}
		if !validReceiptID(request.ConversationID) || request.RunnerID == "" {
			return nil, sessionExtensionRPCError(errors.New("a valid conversation and runner are required"))
		}
		descriptor := protocol.SessionExtensions{ID: convtypes.GenerateID(), ExtensionIDs: request.ExtensionIDs}
		if err := descriptor.Validate(); err != nil {
			return nil, sessionExtensionRPCError(err)
		}
		runner, ok := a.server.runnerRegistry.Runner(request.RunnerID)
		if !ok || !runner.Connected || !runner.SessionExtensions {
			return nil, sessionExtensionRPCError(errors.New("the selected runner does not support session extensions or is offline; update and reconnect the runner"))
		}
		if affinity, exists, err := a.server.runnerRegistry.ResolveConversationAffinity(ctx, request.ConversationID); err != nil {
			return nil, sessionExtensionRPCError(err)
		} else if exists && affinity.RunnerID != runner.ID {
			return nil, sessionExtensionRPCError(errors.New("conversation is bound to another runner"))
		}
		if err := a.server.validateSessionExtensionRequirements(request.ConversationID, descriptor.ExtensionIDs); err != nil {
			return nil, sessionExtensionRPCError(err)
		}
		a.server.sessionExtensionsMu.Lock()
		defer a.server.sessionExtensionsMu.Unlock()
		if a.closed || a.descriptor.ID != "" {
			return nil, sessionExtensionRPCError(errors.New("this connection has already attached or closed"))
		}
		for _, existing := range a.server.sessionExtensions {
			if existing.conversationID == request.ConversationID {
				return nil, sessionExtensionRPCError(errors.New("conversation already has a live session extension attachment"))
			}
		}
		a.descriptor = descriptor
		a.conversationID = request.ConversationID
		a.identity = runnerregistry.UIRequestIdentity{RunnerID: runner.ID, ConnectionID: runner.ConnectionID, Generation: runner.Generation}
		if a.server.sessionExtensions == nil {
			a.server.sessionExtensions = make(map[string]*sessionExtensionAttachment)
		}
		a.server.sessionExtensions[descriptor.ID] = a
		return descriptor, nil
	}
	if method != protocol.MethodSessionExtensionFrame {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeMethodNotFound, Message: "unknown session extension method"}
	}
	var frame protocol.ExtensionFrame
	if err := json.Unmarshal(params, &frame); err != nil {
		return nil, sessionExtensionRPCError(err)
	}
	if err := frame.Validate(); err != nil {
		return nil, sessionExtensionRPCError(err)
	}
	key := sessionExtensionChannel{frame.RunID, frame.ExtensionID}
	a.server.sessionExtensionsMu.Lock()
	closed, exists := a.channels[key]
	valid := !a.closed && frame.AttachmentID == a.descriptor.ID && exists && !closed
	identity := a.identity
	a.server.sessionExtensionsMu.Unlock()
	if !valid {
		return nil, sessionExtensionRPCError(errors.New("session extension channel is not active on this connection"))
	}
	if err := a.server.runnerRegistry.DeliverSessionExtensionFrame(ctx, identity, frame); err != nil {
		return nil, sessionExtensionRPCError(err)
	}
	if frame.Close {
		a.server.sessionExtensionsMu.Lock()
		a.channels[key] = true
		a.server.sessionExtensionsMu.Unlock()
	}
	return struct{}{}, nil
}

// HandleRunnerExtensionFrame routes only frames authorized by the pinned run
// descriptor and the authenticated runner generation; IDs alone are not authority.
func (s *Server) HandleRunnerExtensionFrame(ctx context.Context, identity runnerregistry.UIRequestIdentity, params json.RawMessage) (any, *protocol.RPCError) {
	var frame protocol.ExtensionFrame
	if err := json.Unmarshal(params, &frame); err != nil {
		return nil, sessionExtensionRPCError(err)
	}
	if err := frame.Validate(); err != nil {
		return nil, sessionExtensionRPCError(err)
	}
	run, err := s.runnerRegistry.ValidateSessionExtensionFrame(identity, frame)
	if err != nil {
		return nil, sessionExtensionRPCError(err)
	}
	key := sessionExtensionChannel{frame.RunID, frame.ExtensionID}
	s.sessionExtensionsMu.Lock()
	a := s.sessionExtensions[frame.AttachmentID]
	if a == nil || a.closed || a.identity != identity || a.conversationID != run.ConversationID {
		s.sessionExtensionsMu.Unlock()
		return nil, sessionExtensionRPCError(errors.New("session extension owner disconnected or belongs to another session"))
	}
	closed, exists := a.channels[key]
	if frame.Close && (!exists || closed) {
		s.sessionExtensionsMu.Unlock()
		return struct{}{}, nil
	}
	if closed {
		s.sessionExtensionsMu.Unlock()
		return nil, sessionExtensionRPCError(errors.New("session extension channels cannot be reopened"))
	}
	if !exists {
		var message struct {
			Method string `json:"method"`
		}
		if frame.Close || json.Unmarshal(frame.Message, &message) != nil || message.Method != "extension.initialize" {
			s.sessionExtensionsMu.Unlock()
			return nil, sessionExtensionRPCError(errors.New("a session extension channel must begin with initialize"))
		}
		// Discard tombstones only after the corresponding run lease has ended.
		for channel, ended := range a.channels {
			if ended && channel.runID != frame.RunID {
				delete(a.channels, channel)
			}
		}
		a.channels[key] = false
	}
	s.sessionExtensionsMu.Unlock()
	if err := a.peer.Call(ctx, protocol.MethodSessionExtensionFrame, frame, new(struct{})); err != nil {
		// An uncertain delivery is terminal, never retry an inner tool invocation.
		a.cancel()
		return nil, sessionExtensionRPCError(errors.Wrap(err, "session extension delivery failed; callback side effects may be uncertain"))
	}
	if frame.Close {
		s.sessionExtensionsMu.Lock()
		a.channels[key] = true
		s.sessionExtensionsMu.Unlock()
	}
	return struct{}{}, nil
}

func sessionExtensionRPCError(err error) *protocol.RPCError {
	return &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: err.Error()}
}

func (a *sessionExtensionAttachment) close() {
	a.cancel()
	s := a.server
	s.sessionExtensionsMu.Lock()
	if a.closed {
		s.sessionExtensionsMu.Unlock()
		return
	}
	a.closed = true
	delete(s.sessionExtensions, a.descriptor.ID)
	channels := make([]sessionExtensionChannel, 0, len(a.channels))
	for channel, closed := range a.channels {
		if !closed {
			channels = append(channels, channel)
		}
	}
	s.sessionExtensionsMu.Unlock()
	_ = a.peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, channel := range channels {
		_ = s.runnerRegistry.DeliverSessionExtensionFrame(ctx, a.identity, protocol.ExtensionFrame{
			AttachmentID: a.descriptor.ID, RunID: channel.runID, ExtensionID: channel.extensionID, Close: true,
		})
	}
}

// RunnerExtensionsDetached invalidates client attachments rather than silently
// transferring callback authority to the next runner connection generation.
func (s *Server) RunnerExtensionsDetached(identity runnerregistry.UIRequestIdentity) {
	s.sessionExtensionsMu.Lock()
	defer s.sessionExtensionsMu.Unlock()
	for _, attachment := range s.sessionExtensions {
		if attachment.identity == identity {
			attachment.cancel()
		}
	}
}

func (s *Server) validateSessionExtensionRequirements(conversationID string, ids []string) error {
	required, err := s.runnerRegistry.RequiredSessionExtensions(conversationID)
	if err != nil {
		return err
	}
	if len(required) > 0 && !slices.Equal(required, ids) {
		return errors.New("this conversation requires its session extensions; explicitly reattach the same extensions when resuming, or start a new session")
	}
	return nil
}

func (s *Server) sessionExtensionsForRequest(r *http.Request, req chat.ChatRequest) (*sessionExtensionAttachment, error) {
	if req.SessionExtensionsID == "" {
		return nil, nil //nolint:nilnil // Ordinary sessions do not have a callback attachment.
	}
	principal, ok := principalFromContext(r.Context())
	clientID := strings.TrimSpace(r.Header.Get(chat.ClientIDHeader))
	s.sessionExtensionsMu.Lock()
	defer s.sessionExtensionsMu.Unlock()
	a := s.sessionExtensions[req.SessionExtensionsID]
	if !ok || a == nil || a.closed || a.ctx.Err() != nil || a.principalID != principal.ID || a.credentialID != principal.CredentialID || a.clientID != clientID || a.conversationID != req.ConversationID || (req.RunnerID != "" && a.identity.RunnerID != req.RunnerID) {
		return nil, errors.New("session extension attachment is not available to this client, conversation, or runner")
	}
	return a, nil
}

func (s *Server) sessionExtensionEnvironmentOption(ctx context.Context, req chat.ChatRequest, conversationID, runnerID string) (agentenv.RemoteEnvironmentOption, error) {
	var ids []string
	var descriptor *protocol.SessionExtensions
	if req.SessionExtensionsID != "" {
		a, _ := ctx.Value(sessionExtensionContextKey{}).(*sessionExtensionAttachment)
		if a == nil || a.ctx.Err() != nil || a.descriptor.ID != req.SessionExtensionsID || a.conversationID != conversationID || a.identity.RunnerID != runnerID {
			return nil, errors.New("session extensions require their original authenticated client connection")
		}
		descriptor = &a.descriptor
		ids = descriptor.ExtensionIDs
	}
	if err := s.validateSessionExtensionRequirements(conversationID, ids); err != nil {
		return nil, err
	}
	return agentenv.WithRemoteSessionExtensions(descriptor), nil
}
