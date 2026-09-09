package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

func (s *Server) handleMessageHistory(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("options") {
		s.writeErrorResponse(w, http.StatusBadRequest, "message history does not accept execution options", nil)
		return
	}
	var params protocol.WorkspaceMessageHistoryParams
	if r.Method == http.MethodPost {
		var entry messagehistory.Entry
		if err := decodeUserAuthJSON(w, r, 1<<20, &entry); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid message history entry", err)
			return
		}
		if strings.TrimSpace(entry.Text) == "" {
			s.writeErrorResponse(w, http.StatusBadRequest, "message history requires nonempty text", nil)
			return
		}
		if conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId")); conversationID != "" {
			if entry.ConversationID != "" && entry.ConversationID != conversationID {
				s.writeErrorResponse(w, http.StatusBadRequest, "message history entry differs from the selected conversation", nil)
				return
			}
			entry.ConversationID = conversationID
		}
		// Only the runner can resolve the Git-root scope. The entry's conversation
		// ID is metadata and may refer to a not-yet-submitted first turn.
		entry.ScopeCWD, entry.Source = "", "tui"
		params.Entry = &entry
	}
	target, targetErr := s.resolveRunnerTarget(r)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	params.CWD = target.CWD
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result protocol.WorkspaceMessageHistoryResult
	if err := s.runnerRegistry.CallRunner(ctx, target.Runner.ID, target.Runner.Generation, protocol.MethodWorkspaceMessageHistory, params, &result); err != nil {
		if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			s.writeErrorResponse(w, http.StatusNotImplemented, "message history requires a newer runner; upgrade and restart the selected runner", nil)
			return
		}
		s.writeErrorResponse(w, http.StatusBadGateway, "could not access message history on the runner", err)
		return
	}
	s.writeJSONResponse(w, result)
}
