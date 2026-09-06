package controlplane

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

type (
	turnRunIDKey          struct{}
	turnConversationIDKey struct{}
)

// Only admitted ordinary turns require a durable input/affinity checkpoint before
// extension startup. Discovery and delegated utilities retain their own lifetimes.
type admittedTurnController struct{ *runnerregistry.Registry }

func (c admittedTurnController) OpenRun(ctx context.Context, runnerID string, params protocol.RunOpenParams) (runnerpayload.Manifest, error) {
	manifest, err := c.OpenRunWithCheckpoint(ctx, runnerID, params, agentenv.RunCheckpointFromContext(ctx))
	if err != nil {
		return runnerpayload.Manifest{}, err
	}
	if err := c.CommitConversationAffinity(ctx, params.ConversationID); err != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = c.CloseRun(closeCtx, params.RunID, protocol.RunStatusFailed, err)
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to save the conversation's runner and working directory")
	}
	return manifest, nil
}

func validReceiptID(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 128 && !strings.ContainsAny(id, "/\\") && strings.IndexFunc(id, func(r rune) bool { return r <= ' ' || r == 127 }) == -1
}

func (s *Server) handleGetTurnReceipt(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	if !validReceiptID(vars["id"]) || !validReceiptID(vars["turnId"]) {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid conversation or turn ID", nil)
		return
	}
	if s.turns == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "saved turn status is unavailable", nil)
		return
	}
	receipt, err := s.turns.get(r.Context(), vars["id"], vars["turnId"])
	if errors.Is(err, sql.ErrNoRows) {
		s.writeErrorResponse(w, http.StatusNotFound, "turn not found; check the conversation and turn IDs", nil)
		return
	}
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read the turn status", err)
		return
	}
	s.writeJSONResponse(w, receipt)
}

// Unknown exact stops create durable cancellation fences, not expiring promises
// that a delayed request could execute after the client was told it was stopped.
func (s *Server) handleDurableTurnStop(w http.ResponseWriter, r *http.Request) bool {
	turnID := strings.TrimSpace(r.URL.Query().Get("turnId"))
	if s.turns == nil || !r.URL.Query().Has("turnId") {
		return false
	}
	waitCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	conversationID := strings.TrimSpace(mux.Vars(r)["id"])
	if !validReceiptID(conversationID) || !validReceiptID(turnID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid conversation or turn ID", nil)
		return true
	}
	receipt, err := s.turns.stop(waitCtx, conversationID, turnID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to record turn cancellation", err)
		return true
	}
	if !receipt.Terminal() || receipt.Status == "cancelled" {
		run, _ := s.requestActiveChatStop(conversationID, turnID)
		if run == nil && s.runnerRegistry != nil && receipt.RunID != "" {
			if _, err := s.runnerRegistry.CancelChildTurn(waitCtx, conversationID, receipt.RunID); err != nil {
				s.writeErrorResponse(w, http.StatusRequestTimeout, "cancellation was requested, but the child task has not yet confirmed that it stopped", err)
				return true
			}
		}
		if run != nil && run.done != nil {
			select {
			case <-run.done:
			case <-waitCtx.Done():
				s.writeErrorResponse(w, http.StatusRequestTimeout, "cancellation was requested, but the turn may still be running", waitCtx.Err())
				return true
			}
		}
		receipt, err = s.turns.get(r.Context(), conversationID, turnID)
		if err != nil {
			s.writeErrorResponse(w, http.StatusInternalServerError, "failed to update the turn status cancellation", err)
			return true
		}
	}
	if !receipt.Terminal() {
		s.writeErrorResponse(w, http.StatusRequestTimeout, "cancellation was requested, but the turn may still be running; check its saved status", nil)
		return true
	}
	s.writeJSONResponse(w, struct {
		Success bool             `json:"success"`
		Stopped bool             `json:"stopped"`
		Receipt chat.TurnReceipt `json:"receipt"`
	}{true, receipt.Status == "cancelled", receipt})
	return true
}

func (s *Server) replyTurnReceipt(w http.ResponseWriter, sink chat.ChatEventSink, receipt chat.TurnReceipt) {
	w.Header().Set("X-Kodelet-Conversation-ID", receipt.ConversationID)
	w.Header().Set("X-Kodelet-Turn-ID", receipt.TurnID)
	if !receipt.Terminal() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		s.writeJSONResponse(w, receipt)
		return
	}
	_ = sink.Send(chat.ChatEvent{Kind: "conversation", ConversationID: receipt.ConversationID})
	if receipt.Status == "succeeded" {
		if receipt.Result != nil {
			_ = sink.Send(chat.ChatEvent{Kind: "result", ConversationID: receipt.ConversationID, Result: receipt.Result})
		}
	} else if receipt.Status != "cancelled" {
		_ = sink.Send(chat.ChatEvent{Kind: "error", ConversationID: receipt.ConversationID, Error: receipt.Error})
	}
	_ = sink.Send(chat.ChatEvent{Kind: "done", ConversationID: receipt.ConversationID, Cancelled: receipt.Status == "cancelled"})
}

type turnEventSink struct {
	chat.ChatEventSink
	mu     sync.Mutex
	result *string
}

func (s *turnEventSink) Send(event chat.ChatEvent) error {
	if event.Result != nil {
		s.mu.Lock()
		s.result = new(*event.Result)
		s.mu.Unlock()
	}
	return s.ChatEventSink.Send(event)
}

func (s *Server) finishTurn(ctx context.Context, conversationID, turnID string, sink *turnEventSink, runErr error) error {
	if s.turns == nil {
		return runErr
	}
	status := "succeeded"
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) {
		status = "cancelled"
	} else if runErr != nil {
		status = "failed"
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	sink.mu.Lock()
	result := sink.result
	sink.mu.Unlock()
	if err := s.turns.finish(persistCtx, conversationID, turnID, status, result, runErr); err != nil {
		return err
	}
	receipt, err := s.turns.get(persistCtx, conversationID, turnID)
	if err != nil {
		return errors.Wrap(err, "failed to save the turn's final status")
	}
	if receipt.Status == "cancelled" {
		return context.Canceled
	}
	return runErr
}
