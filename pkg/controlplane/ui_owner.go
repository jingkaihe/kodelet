package controlplane

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
)

func validUIClientID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, char := range id {
		if char != '-' && char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

// A viewer must explicitly take ownership. Merely attaching never selects a
// browser or TUI to answer another client's prompt.
func (s *Server) handleTakeUIOwnership(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(mux.Vars(r)["id"])
	clientID := strings.TrimSpace(r.Header.Get(chat.ClientIDHeader))
	if conversationID == "" || !validUIClientID(clientID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "conversation and client identity are required", nil)
		return
	}
	broker := s.uiInputBrokerForRun(conversationID)
	if broker == nil {
		s.writeErrorResponse(w, http.StatusConflict, "conversation has no active execution", nil)
		return
	}
	s.chatSubscribersMu.Lock()
	var owner *subscriberEventSink
	for subscriber := range s.chatSubscribers[conversationID] {
		if subscriber.clientID == clientID && subscriber.interactive && subscriber.ctx.Err() == nil {
			if owner != nil {
				s.chatSubscribersMu.Unlock()
				s.writeErrorResponse(w, http.StatusConflict, "client has multiple attached streams", nil)
				return
			}
			owner = subscriber
		}
	}
	s.chatSubscribersMu.Unlock()
	if owner == nil {
		s.writeErrorResponse(w, http.StatusConflict, "attach a capable conversation stream before taking control", nil)
		return
	}
	capabilities := owner.capabilities
	capabilities.InteractiveUI = owner.interactive
	broker.transferMu.Lock()
	defer broker.transferMu.Unlock()
	if err := s.updateRunnerUICapabilities(r.Context(), conversationID, capabilities); err != nil {
		s.writeErrorResponse(w, http.StatusConflict, "failed to update runner UI capabilities", err)
		return
	}
	broker.setOwner(withNativeCapabilities(owner.ctx, &capabilities), clientID, owner)
	s.writeJSONResponse(w, map[string]bool{"success": true})
}
