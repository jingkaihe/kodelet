package controlplane

import (
	"net/http"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

type gitDiffResponse struct {
	CWD       string `json:"cwd"`
	Diff      string `json:"diff"`
	HasDiff   bool   `json:"has_diff"`
	GitRoot   string `json:"git_root,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (s *Server) handleGetGitDiff(w http.ResponseWriter, r *http.Request) {
	target, targetErr := s.resolveWorkspaceRunnerTarget(r)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	var result protocol.WorkspaceGitDiffResult
	if err := s.runnerRegistry.CallRunner(r.Context(), target.Runner.ID, target.Runner.Generation, protocol.MethodWorkspaceGitDiff, protocol.WorkspaceGitDiffParams{CWD: target.CWD}, &result); err != nil {
		if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			s.writeErrorResponse(w, http.StatusNotImplemented, "runner does not support workspace git diff", nil)
			return
		}
		s.writeErrorResponse(w, http.StatusBadGateway, "failed to read runner git diff", err)
		return
	}
	s.writeJSONResponse(w, gitDiffResponse{
		CWD:       result.CWD,
		Diff:      result.Diff,
		HasDiff:   result.HasDiff,
		GitRoot:   result.GitRoot,
		ExitCode:  result.ExitCode,
		Truncated: result.Truncated,
	})
}
