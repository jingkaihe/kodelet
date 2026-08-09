package localstate

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorePersistsHostIdentityAndRegistration(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	first, err := store.LoadOrCreateHostIdentity()
	require.NoError(t, err)
	second, err := store.LoadOrCreateHostIdentity()
	require.NoError(t, err)
	assert.Equal(t, first.InstanceID, second.InstanceID)
	assert.NotEmpty(t, first.InstanceID)

	registration := Registration{
		Server:      "https://kodelet.example",
		Workspace:   "/work/project",
		RunnerID:    "runner-1",
		DisplayName: "project",
	}
	require.NoError(t, store.SaveRegistration(registration))
	loaded, ok, err := store.LoadRegistration(registration.Server, registration.Workspace)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, registration.RunnerID, loaded.RunnerID)
	assert.Equal(t, registration.DisplayName, loaded.DisplayName)

	registrations, err := store.Registrations()
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	assert.Equal(t, registration.RunnerID, registrations[0].RunnerID)

	removed, err := store.DeleteRegistration(registration.Server, registration.Workspace, registration.RunnerID)
	require.NoError(t, err)
	assert.True(t, removed)
	_, ok, err = store.LoadRegistration(registration.Server, registration.Workspace)
	require.NoError(t, err)
	assert.False(t, ok)
	removed, err = store.DeleteRegistration(registration.Server, registration.Workspace, registration.RunnerID)
	require.NoError(t, err)
	assert.False(t, removed)

	newer := registration
	newer.RunnerID = "runner-2"
	require.NoError(t, store.SaveRegistration(newer))
	removed, err = store.DeleteRegistration(registration.Server, registration.Workspace, registration.RunnerID)
	require.NoError(t, err)
	assert.False(t, removed)
	loaded, ok, err = store.LoadRegistration(registration.Server, registration.Workspace)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, newer.RunnerID, loaded.RunnerID)
}

func TestRegistrationCacheUsesCanonicalServerIdentity(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	registration := Registration{
		Server:    "HTTPS://EXAMPLE.COM:443/base/./",
		Workspace: "/work/project",
		RunnerID:  "runner-1",
	}
	require.NoError(t, store.SaveRegistration(registration))

	loaded, ok, err := store.LoadRegistration("https://example.com/base", registration.Workspace)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "https://example.com/base", loaded.Server)
	assert.Equal(t, registration.RunnerID, loaded.RunnerID)

	entries, err := os.ReadDir(filepath.Join(store.Root(), "registrations"))
	require.NoError(t, err)
	var jsonFiles int
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			jsonFiles++
		}
	}
	assert.Equal(t, 1, jsonFiles)

	removed, err := store.DeleteRegistration("https://EXAMPLE.com:443/base/", registration.Workspace, registration.RunnerID)
	require.NoError(t, err)
	assert.True(t, removed)
}

func TestWorkspaceLockPreventsDuplicateRunnerAndRecordsPID(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	metadata := LockMetadata{
		PID:       os.Getpid(),
		Hostname:  "host-one",
		Workspace: "/work/project",
		Server:    "https://kodelet.example",
		RunnerID:  "runner-1",
		StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	lockPath := store.WorkspaceLockPath(metadata.Workspace)
	require.NoError(t, os.WriteFile(lockPath, []byte("stale"), 0o644))
	require.NoError(t, os.Chmod(lockPath, 0o644))
	lock, err := store.AcquireWorkspaceLock(metadata.Workspace, metadata)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	assert.FileExists(t, lock.Path())
	info, err := os.Stat(lock.Path())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	_, err = store.AcquireWorkspaceLock(metadata.Workspace, metadata)
	var held *LockHeldError
	require.ErrorAs(t, err, &held)
	assert.Equal(t, os.Getpid(), held.Metadata.PID)
	assert.Equal(t, lock.Path(), held.Path)
	assert.Contains(t, held.Error(), `workspace "/work/project"`)
	assert.Contains(t, held.Error(), fmt.Sprintf("pid %d", os.Getpid()))
	assert.Contains(t, held.Error(), `runner "runner-1"`)
	assert.Contains(t, held.Error(), `server "https://kodelet.example"`)
	assert.Contains(t, held.Error(), metadata.StartedAt.Format(time.RFC3339))
	assert.Contains(t, held.Error(), fmt.Sprintf(`lock %q`, lock.Path()))

	updated := lock.Metadata()
	updated.RunnerID = "runner-1"
	require.NoError(t, lock.WriteMetadata(updated))
	payload, err := os.ReadFile(lock.Path())
	require.NoError(t, err)
	assert.Contains(t, string(payload), "runner-1")
}

func TestCanonicalWorkspaceResolvesSymlinks(t *testing.T) {
	workspace := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.Symlink(workspace, link))
	canonical, err := CanonicalWorkspace(link)
	require.NoError(t, err)
	assert.Equal(t, workspace, canonical)
}

func TestNewStoreUsesDefaultBasePathAndSecuresDirectories(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	store, err := NewStore()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(basePath, "runners"), store.Root())
	for _, path := range []string{
		store.Root(),
		filepath.Join(store.Root(), "registrations"),
		filepath.Join(store.Root(), "locks"),
	} {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		assert.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}

	_, err = NewStoreAt("   ")
	require.ErrorContains(t, err, "runner state directory is required")
	assert.Empty(t, (*Store)(nil).Root())
}

func TestStoreRejectsInvalidStateAndArguments(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)

	_, err = (*Store)(nil).LoadOrCreateHostIdentity()
	require.ErrorContains(t, err, "runner state store is required")
	_, _, err = (*Store)(nil).LoadRegistration("https://example.com", "/work")
	require.ErrorContains(t, err, "runner state store is required")
	require.ErrorContains(t, (*Store)(nil).SaveRegistration(Registration{}), "runner state store is required")
	_, err = (*Store)(nil).DeleteRegistration("https://example.com", "/work", "runner")
	require.ErrorContains(t, err, "runner state store is required")
	_, err = (*Store)(nil).Registrations()
	require.ErrorContains(t, err, "runner state store is required")

	hostPath := filepath.Join(store.Root(), "host.json")
	require.NoError(t, os.WriteFile(hostPath, []byte(`{"version":1}`), 0o600))
	_, err = store.LoadOrCreateHostIdentity()
	require.ErrorContains(t, err, "runner host identity is invalid")
	require.NoError(t, os.WriteFile(hostPath, []byte(`not-json`), 0o600))
	_, err = store.LoadOrCreateHostIdentity()
	require.ErrorContains(t, err, "failed to read runner host identity")

	require.ErrorContains(t, store.SaveRegistration(Registration{}), "server, workspace, and runner id are required")
	_, _, err = store.LoadRegistration("://bad", "/work")
	require.Error(t, err)
	_, err = store.DeleteRegistration("", "/work", "runner")
	require.ErrorContains(t, err, "server, workspace, and expected runner id are required")
}

func TestStoreRegistrationListingSortsAndRejectsCorruptEntries(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	for _, registration := range []Registration{
		{Server: "https://z.example", Workspace: "/work/b", RunnerID: "runner-z"},
		{Server: "https://a.example", Workspace: "/work/c", RunnerID: "runner-c"},
		{Server: "https://a.example", Workspace: "/work/a", RunnerID: "runner-a"},
	} {
		require.NoError(t, store.SaveRegistration(registration))
	}
	require.NoError(t, os.WriteFile(filepath.Join(store.Root(), "registrations", "ignored.txt"), []byte("ignored"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(store.Root(), "registrations", "ignored.json"), 0o700))

	registrations, err := store.Registrations()
	require.NoError(t, err)
	require.Len(t, registrations, 3)
	assert.Equal(t, []string{"runner-a", "runner-c", "runner-z"}, []string{
		registrations[0].RunnerID,
		registrations[1].RunnerID,
		registrations[2].RunnerID,
	})

	require.NoError(t, os.WriteFile(filepath.Join(store.Root(), "registrations", "corrupt.json"), []byte(`not-json`), 0o600))
	_, err = store.Registrations()
	require.ErrorContains(t, err, "failed to read cached runner registration corrupt.json")
}

func TestWorkspaceLockMetadataCanBeReadAcrossLifecycle(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := "/work/project"

	_, found, err := store.ReadWorkspaceLockMetadata(workspace)
	require.NoError(t, err)
	assert.False(t, found)
	_, _, err = store.ReadWorkspaceLockMetadata(" ")
	require.ErrorContains(t, err, "runner workspace is required")
	assert.Empty(t, (*Store)(nil).WorkspaceLockPath(workspace))

	lock, err := store.AcquireWorkspaceLock(workspace, LockMetadata{PID: 123, Hostname: "host"})
	require.NoError(t, err)
	metadata, found, err := store.ReadWorkspaceLockMetadata(workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 123, metadata.PID)
	assert.Equal(t, workspace, metadata.Workspace)
	assert.Nil(t, metadata.StoppedAt)

	require.NoError(t, lock.Close())
	require.NoError(t, lock.Close())
	metadata, found, err = store.ReadWorkspaceLockMetadata(workspace)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, metadata.StoppedAt)
	require.ErrorContains(t, lock.WriteMetadata(metadata), "runner workspace lock is closed")

	require.NoError(t, os.WriteFile(store.WorkspaceLockPath(workspace), []byte(`not-json`), 0o600))
	_, _, err = store.ReadWorkspaceLockMetadata(workspace)
	require.Error(t, err)
	assert.Empty(t, (*WorkspaceLock)(nil).Path())
	assert.Equal(t, LockMetadata{}, (*WorkspaceLock)(nil).Metadata())
	require.NoError(t, (*WorkspaceLock)(nil).Close())
	require.ErrorContains(t, (*WorkspaceLock)(nil).WriteMetadata(LockMetadata{}), "runner workspace lock is required")
}

func TestCanonicalWorkspaceRejectsInvalidPaths(t *testing.T) {
	_, err := CanonicalWorkspace(" ")
	require.ErrorContains(t, err, "workspace path is required")
	_, err = CanonicalWorkspace(filepath.Join(t.TempDir(), "missing"))
	require.ErrorContains(t, err, "failed to resolve workspace symlinks")

	file := filepath.Join(t.TempDir(), "workspace.txt")
	require.NoError(t, os.WriteFile(file, []byte("not a directory"), 0o600))
	_, err = CanonicalWorkspace(file)
	require.ErrorContains(t, err, "runner workspace must be a directory")
}
