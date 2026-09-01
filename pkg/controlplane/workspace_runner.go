package controlplane

import (
	"net/http"
	"strings"

	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

type workspaceRunnerTarget struct {
	Runner runnerregistry.Runner
}

type workspaceRunnerTargetError struct {
	status  int
	message string
	err     error
}

func (s *Server) resolveWorkspaceRunnerTarget(r *http.Request) (*workspaceRunnerTarget, *workspaceRunnerTargetError) {
	if r == nil {
		return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "invalid workspace target", err: errors.New("request is required")}
	}
	runnerID := strings.TrimSpace(r.URL.Query().Get("runnerId"))
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	if runnerID == "" && conversationID == "" {
		return nil, nil
	}
	if strings.TrimSpace(r.URL.Query().Get("cwd")) != "" {
		return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "invalid workspace target", err: errors.New("cwd is not accepted for runner-wide workspace tools")}
	}
	if s == nil || s.runnerRegistry == nil {
		return nil, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "runner registry is unavailable"}
	}

	if conversationID != "" {
		affinity, found, err := s.runnerRegistry.ResolveConversationAffinity(r.Context(), conversationID)
		if err != nil {
			return nil, &workspaceRunnerTargetError{status: http.StatusInternalServerError, message: "failed to resolve conversation runner", err: err}
		}
		if found {
			if runnerID != "" && runnerID != affinity.RunnerID {
				return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "invalid workspace target", err: errors.New("runner does not match conversation affinity")}
			}
			runnerID = affinity.RunnerID
		} else {
			return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "conversation has no remote runner workspace"}
		}
	}

	runner, found := s.runnerRegistry.Runner(runnerID)
	if !found {
		return nil, &workspaceRunnerTargetError{status: http.StatusNotFound, message: "runner not found"}
	}
	if !runner.Connected {
		return nil, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "runner is offline"}
	}
	return &workspaceRunnerTarget{Runner: runner}, nil
}

func (s *Server) writeWorkspaceRunnerTargetError(w http.ResponseWriter, targetErr *workspaceRunnerTargetError) {
	if targetErr == nil {
		return
	}
	s.writeErrorResponse(w, targetErr.status, targetErr.message, targetErr.err)
}
