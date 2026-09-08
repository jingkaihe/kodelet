package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

func (s *Server) handleMoveConversation(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var params chat.ConversationMoveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid move request", err)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF || strings.TrimSpace(params.RunnerID) == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "provide one move request with the destination runnerId", nil)
		return
	}
	if params.CWD != "" && strings.TrimSpace(params.CWD) == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "destination directory must not be blank", nil)
		return
	}
	params.RunnerID = strings.TrimSpace(params.RunnerID)
	id := strings.TrimSpace(mux.Vars(r)["id"])
	if s.runnerRegistry == nil || s.conversationService == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "conversation move is unavailable", nil)
		return
	}
	if s.isActiveChat(id) {
		s.writeErrorResponse(w, http.StatusConflict, "conversation is actively running", nil)
		return
	}
	if s.turns != nil {
		var active bool
		if err := s.turns.db.GetContext(ctx, &active, `SELECT EXISTS(SELECT 1 FROM chat_turns WHERE conversation_id = ? AND status IN ('accepted','running'))`, id); err != nil {
			s.writeErrorResponse(w, http.StatusServiceUnavailable, "could not check whether the conversation is running", err)
			return
		}
		if active {
			s.writeErrorResponse(w, http.StatusConflict, "this conversation already has a turn in progress; wait for it to finish or stop it first", nil)
			return
		}
	}
	source, bound, err := s.runnerRegistry.ResolveConversationAffinity(ctx, id)
	if err != nil {
		s.writeErrorResponse(w, http.StatusConflict, "could not read the conversation's saved runner assignment", err)
		return
	}
	response, err := s.conversationService.GetConversation(ctx, id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, convtypes.ErrConversationNotFound) {
			status = http.StatusNotFound
		}
		s.writeErrorResponse(w, status, "could not load conversation", err)
		return
	}
	sourceRunnerID, _ := response.Metadata[convtypes.RunnerIDMetadataKey].(string)
	profile, _ := response.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey].(string)
	if bound {
		sourceRunnerID, profile = source.RunnerID, source.EnvironmentProfile
	}
	runner, found := s.runnerRegistry.Runner(params.RunnerID)
	if !found {
		s.writeErrorResponse(w, http.StatusNotFound, "selected runner not found", nil)
		return
	}
	cwd := params.CWD
	if cwd == "" {
		cwd = response.CWD
	}
	result := chat.ConversationMoveResult{
		ConversationID: id, RunnerID: runner.ID, RunnerName: runner.DisplayName,
		SourceRunnerID: sourceRunnerID, SourceCWD: response.CWD,
		CWD: cwd, EnvironmentProfile: chat.NormalizeEnvironmentProfile(profile),
	}
	// A confirmation is a content/version fence, not a bearer credential. Each
	// request is authenticated. Only saved history and the selected destination
	// matter: runner availability, connection generations and policy are not probed.
	data, err := json.Marshal([]any{response, source, result})
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to prepare move plan", err)
		return
	}
	digest := sha256.Sum256(data)
	result.Confirmation = hex.EncodeToString(digest[:])
	if params.Confirmation != "" {
		if params.Confirmation != result.Confirmation {
			s.writeErrorResponse(w, http.StatusConflict, "the move details have changed; review the source and destination again before confirming", nil)
			return
		}
		record := convtypes.ConversationRecord{ID: id, CWD: response.CWD, UpdatedAt: response.UpdatedAt, Metadata: response.Metadata}
		if err := s.runnerRegistry.MoveConversation(ctx, record, source, runner.ID, cwd); err != nil {
			s.writeErrorResponse(w, http.StatusConflict, "could not save the conversation's runner assignment: "+err.Error(), nil)
			return
		}
		result.Moved = true
	}
	s.writeJSONResponse(w, result)
}
