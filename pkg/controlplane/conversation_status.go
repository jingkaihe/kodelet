package controlplane

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/logger"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// conversationSummaryReader is implemented by conversation services that can
// read a summary without loading messages.
type conversationSummaryReader interface {
	GetConversationSummary(ctx context.Context, id string) (conversationtypes.ConversationSummary, error)
}

// handleGetConversationStatus handles GET /api/conversations/{id}/status. It
// reads the conversation summary, the latest turn receipt, and live run
// state, never the message history, so clients can poll it.
func (s *Server) handleGetConversationStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := mux.Vars(r)["id"]
	if !validReceiptID(id) {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid conversation ID", nil)
		return
	}
	summary, found, err := s.conversationSummary(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read the conversation", err)
		return
	}
	var latest *chat.TurnReceipt
	if s.turns != nil {
		receipt, err := s.turns.latest(ctx, id)
		switch {
		case err == nil:
			latest = &receipt
		case !errors.Is(err, sql.ErrNoRows):
			s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read the conversation's turns", err)
			return
		}
	}
	if !found && latest == nil {
		s.writeErrorResponse(w, http.StatusNotFound, "conversation not found", nil)
		return
	}

	status := chat.ConversationStatus{ID: id, Running: s.isActiveChat(id)}
	if found {
		status.CWD = summary.CWD
		status.Profile = resolveConversationProfile(summary.Metadata)
		status.Model = resolveConversationModel(summary.Metadata)
		status.Summary = summary.Summary
		status.MessageCount = summary.MessageCount
		status.CreatedAt = &summary.CreatedAt
		status.UpdatedAt = &summary.UpdatedAt
		status.Usage = chat.NewConversationUsage(summary.Usage)
	}
	if latest != nil {
		turn := chat.NewConversationTurn(*latest)
		status.LatestTurn = &turn
		status.Running = status.Running || !latest.Terminal()
	}
	if s.runnerRegistry != nil {
		affinity, ok, err := s.runnerRegistry.ResolveConversationAffinity(ctx, id)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("conversation_id", id).Warn("failed to read the conversation's runner")
		} else if ok {
			status.RunnerID = affinity.RunnerID
		}
	}
	s.writeJSONResponse(w, status)
}

// conversationSummary reads a summary, without messages when the service
// supports it. found is false when the conversation has not been saved.
func (s *Server) conversationSummary(ctx context.Context, id string) (summary conversationtypes.ConversationSummary, found bool, err error) {
	if s.conversationService == nil {
		return summary, false, nil
	}
	if reader, ok := s.conversationService.(conversationSummaryReader); ok {
		summary, err = reader.GetConversationSummary(ctx, id)
	} else {
		var response *conversations.GetConversationResponse
		response, err = s.conversationService.GetConversation(ctx, id)
		if err == nil {
			summary = conversationtypes.ConversationSummary{
				ID:           response.ID,
				CWD:          response.CWD,
				MessageCount: response.MessageCount,
				Summary:      response.Summary,
				Provider:     response.Provider,
				Metadata:     response.Metadata,
				Usage:        response.Usage,
				CreatedAt:    response.CreatedAt,
				UpdatedAt:    response.UpdatedAt,
			}
		}
	}
	if errors.Is(err, conversationtypes.ErrConversationNotFound) {
		return conversationtypes.ConversationSummary{}, false, nil
	}
	if err != nil {
		return conversationtypes.ConversationSummary{}, false, err
	}
	return summary, true, nil
}
