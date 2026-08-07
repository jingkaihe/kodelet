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
