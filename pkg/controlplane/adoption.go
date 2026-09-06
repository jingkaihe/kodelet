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
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

func (s *Server) handleAdoptConversation(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var params chat.ConversationAdoptionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid adoption request", err)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF || strings.TrimSpace(params.RunnerID) == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "adoption requires one request and an explicit runner ID; no default runner selection", nil)
		return
	}
	params.RunnerID = strings.TrimSpace(params.RunnerID)
	id := strings.TrimSpace(mux.Vars(r)["id"])
	if s.runnerRegistry == nil || s.conversationService == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "conversation adoption is unavailable", nil)
		return
	}
	if s.isActiveChat(id) {
		s.writeErrorResponse(w, http.StatusConflict, "conversation is actively running", nil)
		return
	}
	if s.turns != nil {
		var active bool
		if err := s.turns.db.GetContext(ctx, &active, `SELECT EXISTS(SELECT 1 FROM chat_turns WHERE conversation_id = ? AND status IN ('accepted','running'))`, id); err != nil {
			s.writeErrorResponse(w, http.StatusServiceUnavailable, "conversation admission state is unavailable", err)
			return
		}
		if active {
			s.writeErrorResponse(w, http.StatusConflict, "conversation already has an admitted turn", nil)
			return
		}
	}
	_, bound, err := s.runnerRegistry.ResolveConversationAffinity(ctx, id)
	if err != nil || bound {
		s.writeErrorResponse(w, http.StatusConflict, "conversation is already bound or affinity is unavailable; adoption cannot rebind it", err)
		return
	}
	response, err := s.conversationService.GetConversation(ctx, id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, convtypes.ErrConversationNotFound) {
			status = http.StatusNotFound
		}
		s.writeErrorResponse(w, status, "could not load legacy conversation", err)
		return
	}
	if runnerID, exists := response.Metadata[convtypes.RunnerIDMetadataKey]; exists && runnerID != "" {
		s.writeErrorResponse(w, http.StatusConflict, "conversation already has runner provenance; adoption cannot replace it", nil)
		return
	}
	if strings.TrimSpace(response.CWD) == "" {
		s.writeErrorResponse(w, http.StatusConflict, "legacy conversation has no saved directory; keep its history and start a new conversation in the chosen workspace", nil)
		return
	}
	config, err := chat.ValidateConversationAdoptionConfig(response)
	if err != nil {
		s.writeErrorResponse(w, http.StatusConflict, "stored model configuration is incompatible: "+err.Error(), nil)
		return
	}
	profile := chat.NormalizeEnvironmentProfile(params.EnvironmentProfile)
	if stored, exists := response.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey]; exists {
		value, ok := stored.(string)
		if !ok || (params.EnvironmentProfile != "" && profile != chat.NormalizeEnvironmentProfile(value)) {
			s.writeErrorResponse(w, http.StatusConflict, "stored environment profile cannot be replaced during adoption", nil)
			return
		}
		profile = chat.NormalizeEnvironmentProfile(value)
	}
	runner, found := s.runnerRegistry.Runner(params.RunnerID)
	if !found {
		s.writeErrorResponse(w, http.StatusNotFound, "selected runner not found", nil)
		return
	}
	modelProfile := strings.TrimSpace(config.Profile)
	if modelProfile == "" {
		modelProfile = "default" // Config is resolved; do not inherit the active profile again.
	}
	var discovered protocol.WorkspaceDiscoverResult
	err = s.runnerRegistry.CallRunner(ctx, runner.ID, runner.Generation, protocol.MethodWorkspaceDiscover,
		protocol.WorkspaceDiscoverParams{CWD: response.CWD, Profile: modelProfile, EnvironmentProfile: profile}, &discovered)
	if err != nil {
		s.writeErrorResponse(w, http.StatusConflict, "runner workspace validation failed: "+err.Error(), nil)
		return
	}
	if discovered.CWD != response.CWD || chat.NormalizeEnvironmentProfile(discovered.EnvironmentProfile) != profile {
		s.writeErrorResponse(w, http.StatusConflict, "runner canonical directory/profile does not match saved history; start a new conversation rather than silently relocating it", nil)
		return
	}
	result := chat.ConversationAdoptionResult{
		ConversationID: id, RunnerID: runner.ID, RunnerName: runner.DisplayName,
		HostInstanceID: runner.Host.InstanceID, Hostname: runner.Host.Hostname, Generation: runner.Generation,
		CWD: discovered.CWD, EnvironmentProfile: profile, ModelProfile: config.Profile, Provider: config.Provider, Model: config.Model,
	}
	snapshot, err := llmtypes.NewConversationConfigSnapshot(config)
	if err != nil {
		s.writeErrorResponse(w, http.StatusConflict, "stored model configuration cannot be adopted: "+err.Error(), nil)
		return
	}
	// A confirmation is a content/version fence, not a bearer credential. Each
	// request is authenticated and fully revalidated; changing hosts, connection
	// generations, history, model policy or the manifest requires a fresh preview.
	data, err := json.Marshal([]any{response, result, snapshot, discovered.Digest})
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to prepare adoption preview", err)
		return
	}
	digest := sha256.Sum256(data)
	result.Confirmation = hex.EncodeToString(digest[:])
	if params.Confirmation != "" {
		if params.Confirmation != result.Confirmation {
			s.writeErrorResponse(w, http.StatusConflict, "adoption preview changed; inspect the resolved host, directory and configuration and confirm a fresh preview", nil)
			return
		}
		record := convtypes.ConversationRecord{ID: id, CWD: response.CWD, UpdatedAt: response.UpdatedAt, Metadata: response.Metadata}
		if err := s.runnerRegistry.AdoptConversation(ctx, record, runner.ID, runner.Generation, profile); err != nil {
			s.writeErrorResponse(w, http.StatusConflict, "adoption not committed: "+err.Error(), nil)
			return
		}
		result.Adopted = true
	}
	s.writeJSONResponse(w, result)
}
