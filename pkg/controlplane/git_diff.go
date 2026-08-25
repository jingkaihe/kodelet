package controlplane

import (
	"bytes"
	"context"
	"net/http"
	"os/exec"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/osutil"
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
	if target != nil {
		var result protocol.WorkspaceGitDiffResult
		if err := s.runnerRegistry.CallRunner(r.Context(), target.Runner.ID, target.Runner.Generation, protocol.MethodWorkspaceGitDiff, protocol.WorkspaceGitDiffParams{}, &result); err != nil {
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
		return
	}
	if !s.requireControlPlaneWorkspace(w) {
		return
	}
	resolvedCWD, err := s.resolveRequestedCWD(r.URL.Query().Get("cwd"))
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid cwd", err)
		return
	}

	gitRoot, err := resolveGitRoot(r.Context(), resolvedCWD)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "failed to resolve git repository", err)
		return
	}

	diff, exitCode, err := gitDiff(r.Context(), gitRoot)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read git diff", err)
		return
	}

	s.writeJSONResponse(w, gitDiffResponse{
		CWD:      resolvedCWD,
		Diff:     diff,
		HasDiff:  strings.TrimSpace(diff) != "",
		GitRoot:  gitRoot,
		ExitCode: exitCode,
	})
}

func resolveGitRoot(ctx context.Context, cwd string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.Wrap(err, message)
	}

	root := strings.TrimSpace(stdout.String())
	if root == "" {
		return "", errors.New("git root is empty")
	}

	return osutil.CanonicalizePath(root), nil
}

func gitDiff(ctx context.Context, cwd string) (string, int, error) {
	cmd := exec.CommandContext(
		ctx,
		"git",
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"--submodule=diff",
		"--src-prefix=a/",
		"--dst-prefix=b/",
	)
	cmd.Dir = cwd

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return "", exitCode, errors.Wrap(err, "failed to execute git diff")
		}
	}

	if exitCode != 0 {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = "git diff exited with non-zero status"
		}
		return "", exitCode, errors.New(message)
	}

	return stdout.String(), exitCode, nil
}
