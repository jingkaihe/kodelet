package controlplane

import (
	"context"
	"net/http"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

func (s *Server) handleWorkspaceInspection(w http.ResponseWriter, r *http.Request) {
	var params protocol.WorkspaceInspectParams
	if err := decodeUserAuthJSON(w, r, 64<<10, &params); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid workspace inspection request", err)
		return
	}
	if err := params.Validate(); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid workspace inspection request", err)
		return
	}
	if params.CWD != "" || params.Profile != "" || params.EnvironmentProfile != "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "specify the workspace target in query parameters", nil)
		return
	}
	if r.URL.Query().Has("conversationId") || r.URL.Query().Has("options") {
		s.writeErrorResponse(w, http.StatusBadRequest, "inspection accepts a runner, directory and profiles, not a conversation or execution options", nil)
		return
	}
	target, targetErr := s.resolveRunnerTarget(r)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	params.CWD, params.Profile, params.EnvironmentProfile = target.CWD, target.Profile, target.EnvironmentProfile
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result protocol.WorkspaceInspectResult
	err := s.runnerRegistry.CallRunner(ctx, target.Runner.ID, target.Runner.Generation, protocol.MethodWorkspaceInspect, params, &result)
	if err != nil {
		if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			s.writeErrorResponse(w, http.StatusNotImplemented, "workspace inspection requires a newer runner; upgrade and restart the selected runner", nil)
			return
		}
		s.writeErrorResponse(w, http.StatusBadGateway, "could not inspect the workspace", err)
		return
	}
	s.writeJSONResponse(w, result)
}
