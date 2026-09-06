package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

func (s *Server) handleWorkspaceCommit(w http.ResponseWriter, r *http.Request) {
	var approval protocol.WorkspaceGitCommitParams
	if r.Method == http.MethodPost {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 80*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&approval); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid commit approval", err)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			s.writeErrorResponse(w, http.StatusBadRequest, "commit approval requires one JSON object", err)
			return
		}
		if err := approval.Validate(); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid commit approval", err)
			return
		}
	}
	target, targetErr := s.resolveRunnerTarget(r)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	if target == nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "commit requires a runner or conversation target", nil)
		return
	}
	method, timeout := protocol.MethodWorkspaceGitPrepare, 30*time.Second
	var params any = protocol.WorkspaceGitDiffParams{CWD: target.CWD}
	var snapshot protocol.WorkspaceGitCommitSnapshot
	var result any = &snapshot
	if r.Method == http.MethodPost {
		if approval.Generation != target.Runner.Generation || (target.CWD != "" && target.CWD != approval.CWD) {
			s.writeErrorResponse(w, http.StatusConflict, "runner generation or directory changed; prepare and review the commit again", nil)
			return
		}
		method, timeout, params = protocol.MethodWorkspaceGitCommit, 2*time.Minute, approval
		result = &protocol.WorkspaceGitCommitResult{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	if err := s.runnerRegistry.CallRunner(ctx, target.Runner.ID, target.Runner.Generation, method, params, result); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			status = http.StatusNotImplemented
		}
		message := "runner commit preparation failed"
		if r.Method == http.MethodPost {
			message = "runner commit was not acknowledged; inspect Git history before retrying"
		}
		// Runner operation errors are actionable to the workspace's caller. Keep
		// daemon/transport internals private and bound potentially verbose hooks.
		var rpcErr *protocol.RPCError
		if errors.As(err, &rpcErr) {
			detail := strings.TrimSpace(rpcErr.Message)
			if len(detail) > 4096 {
				detail = strings.ToValidUTF8(detail[:4096], "") + " [truncated]"
			}
			if detail != "" {
				message += ": " + detail
			}
		} else if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			message += ": runner does not support workspace commits; upgrade the runner"
		}
		s.writeErrorResponse(w, status, message, err)
		return
	}
	if r.Method == http.MethodGet {
		snapshot.RunnerID, snapshot.Generation = target.Runner.ID, target.Runner.Generation
	}
	s.writeJSONResponse(w, result)
}
