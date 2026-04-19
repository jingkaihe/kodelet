package webui

import (
	"bytes"
	"context"
	"net/http"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
)

type gitDiffResponse struct {
	CWD      string `json:"cwd"`
	Diff     string `json:"diff"`
	HasDiff  bool   `json:"has_diff"`
	GitRoot  string `json:"git_root,omitempty"`
	ExitCode int    `json:"exit_code"`
}

func (s *Server) handleGetGitDiff(w http.ResponseWriter, r *http.Request) {
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

	return root, nil
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
