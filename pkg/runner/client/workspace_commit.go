package client

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

func (s *Service) prepareWorkspaceCommit(ctx context.Context, cwd string) (protocol.WorkspaceGitCommitSnapshot, error) {
	var result protocol.WorkspaceGitCommitSnapshot
	s.mu.Lock()
	closed := s.closed
	result.RunnerID, result.Generation = s.runnerID, s.generation
	s.mu.Unlock()
	if closed {
		return result, errors.New("runner service is closed")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	workspace, err := s.instanceProvider.ResolveWorkingDirectory(ctx, cwd)
	if err != nil {
		return result, err
	}
	root, err := resolveWorkspaceGitRoot(ctx, workspace)
	if err != nil {
		return result, err
	}
	result.CWD, result.GitRoot = workspace, root
	result.Head, result.HeadRef, err = workspaceCommitHead(ctx, root)
	if err != nil {
		return result, err
	}
	result.Tree, err = runCommitGit(ctx, root, "", "", "write-tree")
	if err != nil {
		return result, err
	}
	base := result.Head
	if base == "" {
		base, err = runCommitGit(ctx, root, "", "", "mktree")
		if err != nil {
			return result, err
		}
	}
	// Diff immutable objects, not a second read of the mutable staging area.
	// Large patches are previews only; approval still identifies the full tree.
	result.Diff, result.Truncated, err = readCommitGit(ctx, root, "", "", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", base, result.Tree, "--")
	if err != nil {
		return result, err
	}
	if result.Diff == "" {
		return result, errors.New("no staged changes; stage changes on the selected runner first")
	}
	if result.Truncated {
		// Bound both line width and file count, but retain Git's overall totals.
		// Use the same immutable objects so staging cannot race the summary.
		result.DiffStat, err = runCommitGit(ctx, root, "", "", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--stat=160,120,200", base, result.Tree, "--")
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Service) commitWorkspace(ctx context.Context, params protocol.WorkspaceGitCommitParams) (protocol.WorkspaceGitCommitResult, error) {
	var result protocol.WorkspaceGitCommitResult
	if err := params.Validate(); err != nil {
		return result, err
	}
	s.mu.Lock()
	available := !s.closed && s.generation == params.Generation
	s.mu.Unlock()
	if !available {
		return result, errors.New("the runner reconnected; generate and review the commit again")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	workspace, err := s.instanceProvider.ResolveWorkingDirectory(ctx, params.CWD)
	if err != nil {
		return result, err
	}
	root, err := resolveWorkspaceGitRoot(ctx, workspace)
	if err != nil {
		return result, err
	}
	indexPath, err := runCommitGit(ctx, root, "", "", "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return result, err
	}
	// Reserve Git's ordinary index lock before checking approval. Normal staging,
	// checkout and competing commits cannot replace the approved index meanwhile.
	lock, err := os.OpenFile(indexPath+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return result, errors.Wrap(err, "cannot lock runner Git index; another Git operation may be active")
	}
	_ = lock.Close()
	defer os.Remove(indexPath + ".lock")
	index, err := os.Open(indexPath)
	if err != nil {
		return result, errors.Wrap(err, "cannot read staged Git index")
	}
	defer index.Close()
	copyIndex, err := os.CreateTemp(filepath.Dir(indexPath), "kodelet-commit-index-*")
	if err != nil {
		return result, errors.Wrap(err, "cannot prepare isolated Git index")
	}
	defer os.Remove(copyIndex.Name())
	defer os.Remove(copyIndex.Name() + ".lock")
	info, err := index.Stat()
	if err != nil {
		_ = copyIndex.Close()
		return result, errors.Wrap(err, "cannot inspect staged Git index")
	} else if err := copyIndex.Chmod(info.Mode().Perm()); err != nil {
		_ = copyIndex.Close()
		return result, errors.Wrap(err, "cannot preserve staged Git index permissions")
	}
	_, copyErr := io.Copy(copyIndex, index)
	closeErr := copyIndex.Close()
	if copyErr != nil {
		return result, errors.Wrap(copyErr, "cannot copy staged Git index")
	}
	if closeErr != nil {
		return result, errors.Wrap(closeErr, "cannot close staged Git index copy")
	}
	// Git uses the index mtime to detect same-size worktree edits with matching
	// cached timestamps. A fresh copy timestamp would hide these racy entries.
	if err := os.Chtimes(copyIndex.Name(), info.ModTime(), info.ModTime()); err != nil {
		return result, errors.Wrap(err, "cannot preserve staged Git index timestamp")
	}
	tree, err := runCommitGit(ctx, root, copyIndex.Name(), "", "write-tree")
	if err != nil {
		return result, err
	}
	head, ref, err := workspaceCommitHead(ctx, root)
	if err != nil {
		return result, err
	}
	if tree != params.Tree || head != params.Head || ref != params.HeadRef {
		return result, errors.New("staged changes or HEAD changed after preparation; review a new commit message before retrying")
	}
	args := []string{"commit", "--file=-"}
	if params.SignOff {
		args = append(args, "--signoff")
	}
	// Keep ordinary Git hooks, signing configuration and identity. They receive
	// the private index; publish it only after Git successfully commits it.
	result.Output, err = runCommitGit(ctx, root, copyIndex.Name(), params.Message, args...)
	if err != nil {
		return result, errors.Wrap(err, "could not confirm whether the commit was created; check 'git log' in the repository before trying again")
	}
	if err := os.Rename(copyIndex.Name(), indexPath); err != nil {
		return result, errors.Wrap(err, "the commit was created, but the staging area could not be updated; check 'git status' and 'git log' in the repository")
	}
	result.Commit, err = runCommitGit(ctx, root, "", "", "rev-parse", "HEAD")
	if err != nil {
		return result, errors.Wrap(err, "the commit was created, but its ID could not be read; check 'git log' in the repository")
	}
	return result, nil
}

func workspaceCommitHead(ctx context.Context, root string) (head, ref string, err error) {
	optional := func(args ...string) (string, error) {
		value, err := runCommitGit(ctx, root, "", "", args...)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil // Unborn branch or detached HEAD, respectively.
		}
		return value, err
	}
	head, err = optional("rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", "", err
	}
	ref, err = optional("symbolic-ref", "--quiet", "HEAD")
	return head, ref, err
}

func runCommitGit(ctx context.Context, root, index, input string, args ...string) (string, error) {
	output, truncated, err := readCommitGit(ctx, root, index, input, args...)
	if err != nil {
		return "", err
	}
	if truncated {
		return "", errors.New("runner Git output exceeds the supported limit; split the staged changes or inspect the repository directly")
	}
	return output, nil
}

// readCommitGit permits bounded previews; callers that require complete output
// (object IDs, commit acknowledgements) must use runCommitGit instead.
func readCommitGit(ctx context.Context, root, index, input string, args ...string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	if index != "" {
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
	}
	cmd.Stdin = strings.NewReader(input)
	stdout, stderr := &cappedBuffer{limit: workspaceGitDiffLimit}, &cappedBuffer{limit: workspaceGitErrorLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		return "", false, errors.Wrapf(err, "runner Git %s failed: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	if stderr.truncated {
		return "", false, errors.New("runner Git diagnostics exceed the supported limit; inspect the repository directly")
	}
	return strings.TrimSpace(stdout.String()), stdout.truncated, nil
}
