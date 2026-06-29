package messagehistory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreAppendListAndDeduplicateAdjacent(t *testing.T) {
	ctx := context.Background()
	store := NewStoreWithBasePath(t.TempDir())
	scope := t.TempDir()

	for _, text := range []string{"first", "second", "second", "third"} {
		require.NoError(t, store.Append(ctx, Entry{ScopeCWD: scope, Text: text}))
	}

	entries, err := store.List(ctx, scope, MaxEntriesPerScope)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, []string{"first", "second", "third"}, entryTexts(entries))
	assert.Equal(t, historyVersion, entries[0].Version)
	assert.Equal(t, "tui", entries[0].Source)
	assert.False(t, entries[0].CreatedAt.IsZero())
}

func TestStorePrunesToMaxEntriesPerScope(t *testing.T) {
	ctx := context.Background()
	store := NewStoreWithBasePath(t.TempDir())
	scope := t.TempDir()

	for i := 0; i < MaxEntriesPerScope+5; i++ {
		require.NoError(t, store.Append(ctx, Entry{ScopeCWD: scope, Text: fmt.Sprintf("message-%04d", i)}))
	}

	entries, err := store.List(ctx, scope, MaxEntriesPerScope+100)
	require.NoError(t, err)
	require.Len(t, entries, MaxEntriesPerScope)
	assert.Equal(t, "message-0005", entries[0].Text)
	assert.Equal(t, fmt.Sprintf("message-%04d", MaxEntriesPerScope+4), entries[len(entries)-1].Text)
}

func TestStoreUsesPrivatePermissions(t *testing.T) {
	ctx := context.Background()
	basePath := t.TempDir()
	store := NewStoreWithBasePath(basePath)
	scope := t.TempDir()

	require.NoError(t, store.Append(ctx, Entry{ScopeCWD: scope, Text: "secret-ish prompt", CreatedAt: time.Now()}))

	dirInfo, err := os.Stat(filepath.Join(basePath, "message-history", "by-cwd"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	files, err := os.ReadDir(filepath.Join(basePath, "message-history", "by-cwd"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	fileInfo, err := files[0].Info()
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

func TestStoreSkipsInvalidLines(t *testing.T) {
	basePath := t.TempDir()
	store := NewStoreWithBasePath(basePath)
	scope := t.TempDir()
	path := store.pathForScope(scope)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{
		`not-json`,
		`{"v":1,"scope_cwd":"` + scope + `","source":"tui","text":"valid"}`,
		`{"v":1,"scope_cwd":"` + scope + `","source":"tui","text":"   "}`,
	}, "\n")), 0o600))

	entries, err := store.List(context.Background(), scope, MaxEntriesPerScope)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "valid", entries[0].Text)
}

func TestResolveScopeCWDUsesGitRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repo := t.TempDir()
	require.NoError(t, runGit(repo, "init"))
	nested := filepath.Join(repo, "pkg", "tui")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	scope, err := ResolveScopeCWD(nested)
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(repo), scope)
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Run()
}

func entryTexts(entries []Entry) []string {
	texts := make([]string, 0, len(entries))
	for _, entry := range entries {
		texts = append(texts, entry.Text)
	}
	return texts
}
