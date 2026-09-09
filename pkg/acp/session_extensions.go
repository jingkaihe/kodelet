package acp

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

const sessionExtensionFrameMethod = "kodelet/extensionFrame"

type sessionExtensionsClient interface {
	AttachSessionExtensions(context.Context, string, string, []string, func(context.Context, protocol.ExtensionFrame) error) (chat.SessionExtensionRelay, error)
}

type extensionChannel struct {
	runID       string
	extensionID string
}

type sessionExtensionFrame struct {
	SessionID   acptypes.SessionID `json:"sessionId"`
	RunID       string             `json:"runId"`
	ExtensionID string             `json:"extensionId"`
	Message     json.RawMessage    `json:"message,omitempty"`
	Close       bool               `json:"close,omitempty"`
}

func (s *Server) requestedSessionExtensions(meta map[string]any) ([]string, error) {
	value, present := meta["sessionExtensions"]
	if !present {
		return nil, nil
	}
	if s.remoteSessions == nil {
		return nil, errors.New("session extensions require a daemon-backed ACP session")
	}
	capabilities := s.GetClientCapabilities()
	var capability struct {
		Version int `json:"version"`
	}
	if capabilities != nil {
		data, _ := json.Marshal(capabilities.Meta["sessionExtensions"])
		_ = json.Unmarshal(data, &capability)
	}
	if capability.Version != 1 {
		return nil, errors.New("session extensions require clientCapabilities._meta.sessionExtensions.version 1")
	}
	var request struct {
		Version      int      `json:"version"`
		ExtensionIDs []string `json:"extensionIds"`
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, errors.Wrap(err, "invalid session extensions")
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, errors.Wrap(err, "invalid session extensions")
	}
	if request.Version != 1 {
		return nil, errors.New("session extensions require version 1")
	}
	// The server assigns the real attachment ID after channel validation.
	if err := (protocol.SessionExtensions{ID: "pending", ExtensionIDs: request.ExtensionIDs}).Validate(); err != nil {
		return nil, err
	}
	return request.ExtensionIDs, nil
}

func (s *Server) attachSessionExtensions(session *remoteSession, meta map[string]any) error {
	ids, err := s.requestedSessionExtensions(meta)
	if err != nil || len(ids) == 0 {
		return err
	}
	client, ok := session.client.(sessionExtensionsClient)
	if !ok {
		return errors.New("the server client does not support session extensions; update kodelet to use inline callbacks")
	}
	relay, err := client.AttachSessionExtensions(s.clientCtx, string(session.id), session.runnerID, ids, func(ctx context.Context, frame protocol.ExtensionFrame) error {
		return s.deliverSessionExtensionFrame(ctx, session, frame)
	})
	if err != nil {
		return err
	}
	s.remoteSessions.mu.Lock()
	session.extensionRelay = relay
	session.extensionChannels = make(map[extensionChannel]struct{})
	s.remoteSessions.mu.Unlock()
	go func() {
		<-relay.Done()
		s.remoteSessions.mu.Lock()
		if s.remoteSessions.sessions[session.id] != session {
			s.remoteSessions.mu.Unlock()
			return
		}
		session.extensionErr = errors.New("session extension relay closed")
		if err := relay.Err(); err != nil {
			session.extensionErr = err
		}
		channels := session.extensionChannels
		session.extensionChannels = make(map[extensionChannel]struct{})
		s.remoteSessions.mu.Unlock()
		s.cancelRemotePrompt(session.id)
		ctx, cancel := context.WithTimeout(s.clientCtx, 5*time.Second)
		defer cancel()
		for channel := range channels {
			_, _ = s.CallClient(ctx, sessionExtensionFrameMethod, sessionExtensionFrame{SessionID: session.id, RunID: channel.runID, ExtensionID: channel.extensionID, Close: true})
		}
	}()
	return nil
}

func (s *Server) deliverSessionExtensionFrame(ctx context.Context, session *remoteSession, frame protocol.ExtensionFrame) error {
	s.remoteSessions.mu.Lock()
	if s.remoteSessions.sessions[session.id] != session || session.extensionRelay == nil || session.extensionErr != nil {
		s.remoteSessions.mu.Unlock()
		return errors.New("session extension attachment is no longer active")
	}
	channel := extensionChannel{runID: frame.RunID, extensionID: frame.ExtensionID}
	if frame.Close {
		delete(session.extensionChannels, channel)
	} else {
		session.extensionChannels[channel] = struct{}{}
	}
	s.remoteSessions.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := s.CallClient(ctx, sessionExtensionFrameMethod, sessionExtensionFrame{
		SessionID: session.id, RunID: frame.RunID, ExtensionID: frame.ExtensionID, Message: frame.Message, Close: frame.Close,
	})
	return err
}

func (s *Server) handleSessionExtensionFrame(req *acptypes.Request) error {
	if !s.initialized.Load() || s.remoteSessions == nil {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "session extensions are not available", nil)
	}
	var frame sessionExtensionFrame
	if err := json.Unmarshal(req.Params, &frame); err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "invalid extension frame", err.Error())
	}
	s.remoteSessions.mu.Lock()
	session := s.remoteSessions.sessions[frame.SessionID]
	if session == nil || session.extensionRelay == nil || session.extensionErr != nil {
		s.remoteSessions.mu.Unlock()
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "session extension attachment not found", nil)
	}
	relay := session.extensionRelay
	_, channelKnown := session.extensionChannels[extensionChannel{runID: frame.RunID, extensionID: frame.ExtensionID}]
	s.remoteSessions.mu.Unlock()
	attachment := relay.Attachment()
	if !channelKnown || !slices.Contains(attachment.ExtensionIDs, frame.ExtensionID) {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "extension frame does not belong to this session", nil)
	}
	if err := relay.SendFrame(s.clientCtx, protocol.ExtensionFrame{
		AttachmentID: attachment.ID, RunID: frame.RunID, ExtensionID: frame.ExtensionID, Message: frame.Message, Close: frame.Close,
	}); err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
	}
	return s.sendResult(req.ID, struct{}{})
}

func (s *Server) sessionExtensionPromptError(sessionID acptypes.SessionID) error {
	if s.clientCtx.Err() != nil {
		return nil // EOF has no connected SDK to report a callback transport failure to.
	}
	s.remoteSessions.mu.Lock()
	defer s.remoteSessions.mu.Unlock()
	session := s.remoteSessions.sessions[sessionID]
	if session == nil {
		return nil
	}
	err := session.extensionErr
	if err == nil && session.extensionRelay != nil {
		select {
		case <-session.extensionRelay.Done():
			// The disconnect watcher may not have recorded extensionErr yet.
			err = errors.New("session extension relay closed")
		default:
		}
	}
	if err == nil {
		return nil
	}
	return errors.Wrap(err, "inline extension relay lost; callback side effects may be uncertain; do not retry or reattach automatically; inspect the conversation before explicitly reattaching callbacks")
}

func (s *Server) closeSessionExtensions() {
	s.closeClient()
	if s.remoteSessions == nil {
		return
	}
	s.remoteSessions.mu.Lock()
	s.remoteSessions.closed = true
	sessions := make([]*remoteSession, 0, len(s.remoteSessions.sessions))
	for _, session := range s.remoteSessions.sessions {
		if session.extensionRelay != nil {
			sessions = append(sessions, session)
		}
	}
	s.remoteSessions.mu.Unlock()
	for _, session := range sessions {
		s.cancelRemotePrompt(session.id)
		_ = session.extensionRelay.Close()
	}
}
