package acp

import (
	"encoding/json"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// handleConversationStatus answers _conversation/status from the server
// without opening a session.
func (s *Server) handleConversationStatus(req *acptypes.Request) error {
	var params acptypes.ConversationStatusRequest
	client, ok, err := s.conversationRequest(req, &params, &params.ConversationID)
	if !ok {
		return err
	}
	status, err := client.GetConversationStatus(s.clientCtx, params.ConversationID)
	if err != nil {
		return s.sendConversationError(req, err)
	}
	return s.sendResult(req.ID, status)
}

// handleConversationStop answers _conversation/stop. It returns once the
// server has accepted the stop; poll _conversation/status for the outcome.
func (s *Server) handleConversationStop(req *acptypes.Request) error {
	var params acptypes.ConversationStopRequest
	client, ok, err := s.conversationRequest(req, &params, &params.ConversationID)
	if !ok {
		return err
	}
	if err := client.StopConversationTurn(s.clientCtx, params.ConversationID, strings.TrimSpace(params.TurnID)); err != nil {
		return s.sendConversationError(req, err)
	}
	return s.sendResult(req.ID, struct{}{})
}

// conversationRequest decodes a conversation method's params and returns the
// server client. When ok is false the error response has been sent and err
// is the result of sending it.
func (s *Server) conversationRequest(req *acptypes.Request, params any, conversationID *string) (client RemoteChatClient, ok bool, err error) {
	if s.remoteSessions == nil {
		return nil, false, s.sendError(req.ID, acptypes.ErrCodeMethodNotFound, "Method not found", nil)
	}
	if !s.initialized.Load() {
		return nil, false, s.sendError(req.ID, acptypes.ErrCodeInternalError, "Not initialized", nil)
	}
	if err := json.Unmarshal(req.Params, params); err != nil {
		return nil, false, s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "Invalid params", err.Error())
	}
	*conversationID = strings.TrimSpace(*conversationID)
	if *conversationID == "" {
		return nil, false, s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "conversation ID is required", nil)
	}
	client, _, err = s.remoteSessions.waitForClient(s.clientCtx)
	if err != nil {
		return nil, false, s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
	}
	return client, true, nil
}

func (s *Server) sendConversationError(req *acptypes.Request, err error) error {
	if errors.Is(err, convtypes.ErrConversationNotFound) {
		return s.sendError(req.ID, acptypes.ErrCodeResourceNotFound, "conversation not found", nil)
	}
	return s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
}
