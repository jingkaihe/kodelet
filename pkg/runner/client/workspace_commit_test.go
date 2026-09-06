package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func commitRepository(t *testing.T) (string, func(...string) string) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		return strings.TrimSpace(string(output))
	}
	git("init")
	git("config", "user.name", "Runner User")
	git("config", "user.email", "runner@example.com")
	git("config", "commit.gpgsign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("approved\n"), 0o600))
	git("add", "file.txt")
	return root, git
}

func commitParams(snapshot protocol.WorkspaceGitCommitSnapshot) protocol.WorkspaceGitCommitParams {
	return protocol.WorkspaceGitCommitParams{CWD: snapshot.CWD, Head: snapshot.Head, HeadRef: snapshot.HeadRef, Tree: snapshot.Tree, Generation: snapshot.Generation, Message: "feat: approved runner changes", SignOff: true}
}

func TestWorkspaceCommitSnapshotAndApprovedMutation(t *testing.T) {
	root, git := commitRepository(t)
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.runnerID, service.generation = "runner", 7
	snapshot := callService[protocol.WorkspaceGitCommitSnapshot](t, service, protocol.MethodWorkspaceGitPrepare, protocol.WorkspaceGitDiffParams{CWD: root})
	assert.Equal(t, root, snapshot.CWD)
	assert.Equal(t, root, snapshot.GitRoot)
	assert.Empty(t, snapshot.Head)
	assert.NotEmpty(t, snapshot.HeadRef)
	assert.Contains(t, snapshot.Diff, "+approved")
	assert.False(t, snapshot.Truncated)
	assert.Empty(t, snapshot.DiffStat)
	assert.Equal(t, int64(7), snapshot.Generation)
	assert.Equal(t, "runner", snapshot.RunnerID)
	// The unstaged working file is not silently included in the commit.
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("unstaged\n"), 0o600))
	hooks := filepath.Join(root, ".git", "hooks")
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "commit-msg"), []byte("#!/bin/sh\nprintf '\nHook checked\n' >> \"$1\"\n"), 0o700))
	result := callService[protocol.WorkspaceGitCommitResult](t, service, protocol.MethodWorkspaceGitCommit, commitParams(snapshot))
	assert.Equal(t, git("rev-parse", "HEAD"), result.Commit)
	assert.Equal(t, snapshot.Tree, git("rev-parse", "HEAD^{tree}"))
	assert.Equal(t, "approved", git("show", "HEAD:file.txt"))
	assert.Contains(t, git("log", "-1", "--format=%B"), "Signed-off-by: Runner User <runner@example.com>")
	assert.Contains(t, git("log", "-1", "--format=%B"), "Hook checked")
	assert.Empty(t, git("diff", "--cached"))
	assert.Contains(t, git("diff"), "+unstaged")
	assert.NoFileExists(t, filepath.Join(root, ".git", "index.lock"))
	_, err = service.prepareWorkspaceCommit(t.Context(), root)
	require.ErrorContains(t, err, "no staged changes")
	// A replay cannot produce a second mutation even if the HTTP result was lost.
	_, err = service.commitWorkspace(t.Context(), commitParams(snapshot))
	require.ErrorContains(t, err, "changed after preparation")
	assert.Equal(t, "1", git("rev-list", "--count", "HEAD"))
}

func TestWorkspaceCommitLargeDiffPreservesEntireStagedTree(t *testing.T) {
	for _, initialCommit := range []bool{false, true} {
		name := "unborn"
		if initialCommit {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			root, git := commitRepository(t)
			if initialCommit {
				git("commit", "-m", "initial")
			}
			largeChange := strings.Repeat("large staged change\n", workspaceGitDiffLimit/len("large staged change\n")+1)
			require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte(largeChange), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "z-summary-only.txt"), []byte("outside the patch preview\n"), 0o600))
			git("add", ".")
			service, err := NewService(t.Context(), root, ServiceOptions{})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			service.generation = 1
			snapshot, err := service.prepareWorkspaceCommit(t.Context(), root)
			require.NoError(t, err)
			assert.True(t, snapshot.Truncated)
			assert.LessOrEqual(t, len(snapshot.Diff), workspaceGitDiffLimit)
			assert.NotContains(t, snapshot.Diff, "z-summary-only.txt")
			assert.Contains(t, snapshot.DiffStat, "z-summary-only.txt")
			assert.Contains(t, snapshot.DiffStat, "2 files changed")
			assert.Equal(t, git("write-tree"), snapshot.Tree)

			// Neither truncation nor unstaged edits may change the approved tree.
			require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("unstaged\n"), 0o600))
			result, err := service.commitWorkspace(t.Context(), commitParams(snapshot))
			require.NoError(t, err)
			assert.Equal(t, git("rev-parse", "HEAD"), result.Commit)
			assert.Equal(t, snapshot.Tree, git("rev-parse", "HEAD^{tree}"))
			assert.Equal(t, strings.TrimSpace(largeChange), git("show", "HEAD:file.txt"))
			assert.Equal(t, "outside the patch preview", git("show", "HEAD:z-summary-only.txt"))
			assert.Empty(t, git("diff", "--cached"))
			assert.Contains(t, git("diff"), "+unstaged")
		})
	}
}

func TestWorkspaceCommitLargeDiffSummaryIsBounded(t *testing.T) {
	root, git := commitRepository(t)
	for i := range 250 {
		name := fmt.Sprintf("change-%03d-%s.txt", i, strings.Repeat("long-name", 20))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat("changed line\n", 200)), 0o600))
	}
	git("add", ".")
	git("config", "color.ui", "always")
	service, err := NewService(t.Context(), root, ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	snapshot, err := service.prepareWorkspaceCommit(t.Context(), root)
	require.NoError(t, err)
	assert.True(t, snapshot.Truncated)
	assert.LessOrEqual(t, len(snapshot.Diff), workspaceGitDiffLimit)
	assert.LessOrEqual(t, len(snapshot.DiffStat), workspaceGitErrorLimit)
	assert.LessOrEqual(t, len(strings.Split(snapshot.DiffStat, "\n")), 202)
	assert.Contains(t, snapshot.DiffStat, "251 files changed")
	assert.NotContains(t, snapshot.Diff, "\x1b[")
	assert.NotContains(t, snapshot.DiffStat, "\x1b[")
}

func TestRunCommitGitRequiresCompleteOutput(t *testing.T) {
	root, _ := commitRepository(t)
	input := strings.Repeat("x", workspaceGitDiffLimit+1)
	output, truncated, err := readCommitGit(t.Context(), root, "", input, "-c", "alias.emit=!cat", "emit")
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Len(t, output, workspaceGitDiffLimit)
	_, err = runCommitGit(t.Context(), root, "", input, "-c", "alias.emit=!cat", "emit")
	require.ErrorContains(t, err, "output exceeds the supported limit")
	_, _, err = readCommitGit(t.Context(), root, "", input, "-c", "alias.emit=!cat >&2", "emit")
	require.ErrorContains(t, err, "diagnostics exceed the supported limit")
}

func TestWorkspaceCommitRejectsStaleApprovalAndPreservesLocks(t *testing.T) {
	for _, scenario := range []string{"staging", "head", "branch", "generation", "message", "lock"} {
		t.Run(scenario, func(t *testing.T) {
			root, git := commitRepository(t)
			git("commit", "-m", "initial")
			require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("new staged\n"), 0o600))
			git("add", "file.txt")
			service, err := NewService(t.Context(), root, ServiceOptions{})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			service.generation = 1
			snapshot, err := service.prepareWorkspaceCommit(t.Context(), root)
			require.NoError(t, err)
			params := commitParams(snapshot)
			expected := "changed after preparation"
			switch scenario {
			case "staging":
				require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("unapproved\n"), 0o600))
				git("add", "file.txt")
			case "head":
				git("commit", "--amend", "--only", "-m", "rewritten parent")
			case "branch":
				git("switch", "-c", "other")
			case "generation":
				service.generation++
				expected = "generation changed"
			case "message":
				params.Message = "\x00"
				expected = "commit message"
			case "lock":
				require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "index.lock"), []byte("other writer"), 0o600))
				expected = "cannot lock"
			}
			head := git("rev-parse", "HEAD")
			_, err = service.commitWorkspace(t.Context(), params)
			require.ErrorContains(t, err, expected)
			assert.Equal(t, head, git("rev-parse", "HEAD"))
			if scenario == "lock" {
				data, err := os.ReadFile(filepath.Join(root, ".git", "index.lock"))
				require.NoError(t, err)
				assert.Equal(t, "other writer", string(data))
			} else {
				assert.NoFileExists(t, filepath.Join(root, ".git", "index.lock"))
			}
		})
	}
}

func TestWorkspaceCommitCancellationReleasesIndexAndStopsHook(t *testing.T) {
	root, git := commitRepository(t)
	marker := filepath.Join(root, "started")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\ntouch started\nsleep 60\ntouch should-not-exist\n"), 0o700))
	service, err := NewService(t.Context(), root, ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.generation = 1
	snapshot, err := service.prepareWorkspaceCommit(t.Context(), root)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := service.commitWorkspace(ctx, commitParams(snapshot)); done <- err }()
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 2*time.Second, 10*time.Millisecond)
	// A normal concurrent staging operation cannot change the reserved index.
	cmd := exec.Command("git", "add", "file.txt")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	require.Error(t, err, string(output))
	assert.Contains(t, string(output), "index.lock")
	cancel()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "did not acknowledge success")
	case <-time.After(5 * time.Second):
		t.Fatal("canceled commit hook did not terminate")
	}
	assert.NoFileExists(t, filepath.Join(root, "should-not-exist"))
	assert.NoFileExists(t, filepath.Join(root, ".git", "index.lock"))
	assert.Equal(t, snapshot.Tree, git("write-tree"))
	files, err := filepath.Glob(filepath.Join(root, ".git", "kodelet-commit-index-*"))
	require.NoError(t, err)
	assert.Empty(t, files)
}
