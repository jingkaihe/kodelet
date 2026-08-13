package localstate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
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
		filepath.Join(store.Root(), "credentials"),
		filepath.Join(store.Root(), "enrollments"),
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
	_, err = store.WorkspaceLockHeld(" ")
	require.ErrorContains(t, err, "runner workspace is required")
	assert.Empty(t, (*Store)(nil).WorkspaceLockPath(workspace))

	lock, err := store.AcquireWorkspaceLock(workspace, LockMetadata{PID: 123, Hostname: "host"})
	require.NoError(t, err)
	held, err := store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	assert.True(t, held)
	metadata, found, err := store.ReadWorkspaceLockMetadata(workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 123, metadata.PID)
	assert.Equal(t, workspace, metadata.Workspace)
	assert.Nil(t, metadata.StoppedAt)

	require.NoError(t, lock.Close())
	require.NoError(t, lock.Close())
	held, err = store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	assert.False(t, held)
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

func TestWorkspaceLockHeldProbesDoNotBlock(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := "/work/project"
	lock, err := store.AcquireWorkspaceLock(workspace, LockMetadata{PID: os.Getpid()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })

	startedAt := time.Now()
	for range 16 {
		held, probeErr := store.WorkspaceLockHeld(workspace)
		require.NoError(t, probeErr)
		assert.True(t, held)
	}
	assert.Less(t, time.Since(startedAt), 2*time.Second)
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

func TestStorePersistsKeyBoundCredentialSecurely(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	workspaceLink := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.Symlink(workspace, workspaceLink))
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 1)
	accessToken := testRunnerAccessToken(t)

	require.NoError(t, store.SaveCredential(Credential{
		Server:       "HTTPS://EXAMPLE.COM:443/base/./",
		Workspace:    workspaceLink,
		CredentialID: "credential-one",
		AccessToken:  accessToken,
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}))

	credential, found, err := store.LoadCredential("https://example.com/base", workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, stateVersion, credential.Version)
	assert.Equal(t, "https://example.com/base", credential.Server)
	assert.Equal(t, workspace, credential.Workspace)
	assert.Equal(t, "credential-one", credential.CredentialID)
	assert.Equal(t, accessToken, credential.AccessToken)
	assert.Equal(t, fingerprint, credential.Fingerprint)
	assert.Equal(t, publicKey, credential.PublicKey)
	assert.Equal(t, privateKey, credential.PrivateKey)
	assert.False(t, credential.CreatedAt.IsZero())
	assert.False(t, credential.UpdatedAt.IsZero())

	credentialPath := onlyJSONStateFile(t, filepath.Join(store.Root(), "credentials"))
	info, err := os.Stat(credentialPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	payload, err := os.ReadFile(credentialPath)
	require.NoError(t, err)
	var stored credentialFile
	require.NoError(t, json.Unmarshal(payload, &stored))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(publicKey), stored.PublicKey)
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(privateKey), stored.PrivateKey)
	assert.NotContains(t, stored.PublicKey, "=")
	assert.NotContains(t, stored.PrivateKey, "=")
	assertStateLockFilesSecure(t, filepath.Join(store.Root(), "credentials"))

	credential.CredentialID = "credential-two"
	require.NoError(t, store.SaveCredential(credential))
	reloaded, found, err := store.LoadCredential("https://EXAMPLE.com:443/base/", workspaceLink)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-two", reloaded.CredentialID)
	assert.Equal(t, credentialPath, onlyJSONStateFile(t, filepath.Join(store.Root(), "credentials")))

	removed, err := store.DeleteCredential("https://example.com/base", workspaceLink)
	require.NoError(t, err)
	assert.True(t, removed)
	_, found, err = store.LoadCredential("https://example.com/base", workspace)
	require.NoError(t, err)
	assert.False(t, found)
	removed, err = store.DeleteCredential("https://example.com/base", workspace)
	require.NoError(t, err)
	assert.False(t, removed)
}

func TestStorePersistsPendingEnrollmentSecurely(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 2)
	accessToken := testRunnerAccessToken(t)
	expiresAt := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)

	require.NoError(t, store.SavePendingEnrollment(PendingEnrollment{
		Server:                  "https://kodelet.example/base/",
		Workspace:               workspace,
		EnrollmentID:            "enrollment-one",
		DeviceCode:              accessToken,
		UserCode:                "ABCD-EFGH",
		VerificationURL:         "https://kodelet.example/runner/enroll",
		VerificationURLComplete: "https://kodelet.example/runner/enroll?code=ABCD-EFGH",
		Fingerprint:             fingerprint,
		PublicKey:               publicKey,
		PrivateKey:              privateKey,
		ExpiresAt:               expiresAt,
		PollIntervalMS:          2500,
	}))

	enrollment, found, err := store.LoadPendingEnrollment("https://KODELET.example:443/base", workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "https://kodelet.example/base", enrollment.Server)
	assert.Equal(t, workspace, enrollment.Workspace)
	assert.Equal(t, "enrollment-one", enrollment.EnrollmentID)
	assert.Equal(t, accessToken, enrollment.DeviceCode)
	assert.Equal(t, "ABCD-EFGH", enrollment.UserCode)
	assert.Equal(t, expiresAt, enrollment.ExpiresAt)
	assert.Equal(t, int64(2500), enrollment.PollIntervalMS)
	assert.Equal(t, publicKey, enrollment.PublicKey)
	assert.Equal(t, privateKey, enrollment.PrivateKey)

	enrollmentPath := onlyJSONStateFile(t, filepath.Join(store.Root(), "enrollments"))
	info, err := os.Stat(enrollmentPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	payload, err := os.ReadFile(enrollmentPath)
	require.NoError(t, err)
	var stored enrollmentFile
	require.NoError(t, json.Unmarshal(payload, &stored))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(publicKey), stored.PublicKey)
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(privateKey), stored.PrivateKey)
	assert.NotContains(t, stored.PublicKey, "=")
	assert.NotContains(t, stored.PrivateKey, "=")
	assertStateLockFilesSecure(t, filepath.Join(store.Root(), "enrollments"))

	removed, err := store.DeletePendingEnrollment("https://kodelet.example/base", workspace)
	require.NoError(t, err)
	assert.True(t, removed)
	_, found, err = store.LoadPendingEnrollment("https://kodelet.example/base", workspace)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestLoadOrCreatePendingEnrollmentSerializesAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	server := "https://kodelet.example"
	first, err := NewStoreAt(root)
	require.NoError(t, err)
	second, err := NewStoreAt(root)
	require.NoError(t, err)
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 0x61)
	pending := PendingEnrollment{
		EnrollmentID: "enrollment-shared",
		DeviceCode:   testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		ExpiresAt:    time.Now().Add(time.Minute),
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var callsMu sync.Mutex
	var calls int
	type result struct {
		pending PendingEnrollment
		resumed bool
		err     error
	}
	results := make(chan result, 2)
	create := func(active bool) (PendingEnrollment, error) {
		if active {
			return PendingEnrollment{}, fmt.Errorf("unexpected active credential")
		}
		callsMu.Lock()
		calls++
		if calls == 1 {
			close(entered)
		}
		callsMu.Unlock()
		<-release
		return pending, nil
	}
	go func() {
		loaded, resumed, loadErr := first.LoadOrCreatePendingEnrollment(server, workspace, create)
		results <- result{pending: loaded, resumed: resumed, err: loadErr}
	}()
	<-entered
	go func() {
		loaded, resumed, loadErr := second.LoadOrCreatePendingEnrollment(server, workspace, create)
		results <- result{pending: loaded, resumed: resumed, err: loadErr}
	}()
	close(release)
	firstResult := <-results
	secondResult := <-results
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)
	assert.Equal(t, pending.EnrollmentID, firstResult.pending.EnrollmentID)
	assert.Equal(t, pending.EnrollmentID, secondResult.pending.EnrollmentID)
	assert.NotEqual(t, firstResult.resumed, secondResult.resumed)
	callsMu.Lock()
	assert.Equal(t, 1, calls)
	callsMu.Unlock()
}

func TestRunnerAuthenticationStateMutationsAreFencedByExpectedIDs(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	server := "https://kodelet.example"
	workspace := t.TempDir()
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 0x62)
	pending := PendingEnrollment{
		Server:       server,
		Workspace:    workspace,
		EnrollmentID: "enrollment-new",
		DeviceCode:   testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		ExpiresAt:    time.Now().Add(time.Minute),
	}
	require.NoError(t, store.SavePendingEnrollment(pending))
	credential := Credential{
		Server:       server,
		Workspace:    workspace,
		CredentialID: "credential-new",
		AccessToken:  testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}
	require.NoError(t, store.SaveCredential(credential))
	require.NoError(t, store.SaveRegistration(Registration{Server: server, Workspace: workspace, RunnerID: "runner-new"}))

	committed, err := store.CommitApprovedEnrollment("enrollment-old", Credential{
		Server:       server,
		Workspace:    workspace,
		CredentialID: "credential-old",
		AccessToken:  pending.DeviceCode,
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}, Registration{Server: server, Workspace: workspace, RunnerID: "runner-old"})
	require.NoError(t, err)
	assert.False(t, committed)
	removed, err := store.DeletePendingEnrollmentIfID(server, workspace, "enrollment-old")
	require.NoError(t, err)
	assert.False(t, removed)
	removed, err = store.DeleteAuthenticationStateForRegistration(server, workspace, "runner-old")
	require.NoError(t, err)
	assert.False(t, removed)

	loadedPending, found, err := store.LoadPendingEnrollment(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "enrollment-new", loadedPending.EnrollmentID)
	loadedCredential, found, err := store.LoadCredential(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-new", loadedCredential.CredentialID)
	loadedRegistration, found, err := store.LoadRegistration(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "runner-new", loadedRegistration.RunnerID)
}

func TestStoreDeletesRunnerAuthenticationStateAfterWorkspaceRemoval(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	server := "https://kodelet.example"
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 9)
	require.NoError(t, store.SaveRegistration(Registration{
		Server:    server,
		Workspace: workspace,
		RunnerID:  "runner-removed-workspace",
	}))
	require.NoError(t, store.SaveCredential(Credential{
		Server:       server,
		Workspace:    workspace,
		CredentialID: "credential-removed-workspace",
		AccessToken:  testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}))
	require.NoError(t, store.SavePendingEnrollment(PendingEnrollment{
		Server:       server,
		Workspace:    workspace,
		EnrollmentID: "enrollment-removed-workspace",
		DeviceCode:   testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		ExpiresAt:    time.Now().Add(time.Minute),
	}))
	require.NoError(t, os.RemoveAll(workspace))

	removed, err := store.DeleteCredential(server, workspace)
	require.NoError(t, err)
	assert.True(t, removed)
	removed, err = store.DeletePendingEnrollment(server, workspace)
	require.NoError(t, err)
	assert.True(t, removed)
	removed, err = store.DeleteRegistration(server, workspace, "runner-removed-workspace")
	require.NoError(t, err)
	assert.True(t, removed)
}

func TestCredentialStateRejectsMismatchedKeysAndFingerprints(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 3)
	otherPublicKey, _, _ := testCredentialKeyPair(t, 4)
	valid := Credential{
		Server:       "https://kodelet.example",
		Workspace:    workspace,
		CredentialID: "credential-one",
		AccessToken:  testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}

	mismatchedPublic := valid
	mismatchedPublic.PublicKey = otherPublicKey
	require.ErrorContains(t, store.SaveCredential(mismatchedPublic), "does not match private key")
	mismatchedFingerprint := valid
	mismatchedFingerprint.Fingerprint = "sha256:not-the-key"
	require.ErrorContains(t, store.SaveCredential(mismatchedFingerprint), "fingerprint does not match")
	corruptPrivate := valid
	corruptPrivate.PrivateKey = append(ed25519.PrivateKey(nil), privateKey...)
	corruptPrivate.PrivateKey[len(corruptPrivate.PrivateKey)-1] ^= 0xff
	require.ErrorContains(t, store.SaveCredential(corruptPrivate), "derived public key")

	require.NoError(t, store.SaveCredential(valid))
	path := onlyJSONStateFile(t, filepath.Join(store.Root(), "credentials"))
	stored, err := readJSONFile[credentialFile](path)
	require.NoError(t, err)
	stored.PublicKey = base64.RawURLEncoding.EncodeToString(otherPublicKey)
	require.NoError(t, writeJSONAtomic(path, stored))
	_, _, err = store.LoadCredential(valid.Server, workspace)
	require.ErrorContains(t, err, "public key does not match private key")

	pending := PendingEnrollment{
		Server:       valid.Server,
		Workspace:    workspace,
		EnrollmentID: "enrollment-one",
		DeviceCode:   testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		ExpiresAt:    time.Now().UTC().Add(time.Minute),
	}
	pending.Fingerprint = ""
	require.ErrorContains(t, store.SavePendingEnrollment(pending), "fingerprint is required")
}

func TestCredentialStateDoesNotLeakThroughRegistrations(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 5)
	accessToken := testRunnerAccessToken(t)
	deviceCode := testRunnerAccessToken(t)
	require.NoError(t, store.SaveRegistration(Registration{
		Server:    "https://kodelet.example",
		Workspace: workspace,
		RunnerID:  "runner-one",
	}))
	require.NoError(t, store.SaveCredential(Credential{
		Server:       "https://kodelet.example",
		Workspace:    workspace,
		CredentialID: "credential-secret",
		AccessToken:  accessToken,
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}))
	require.NoError(t, store.SavePendingEnrollment(PendingEnrollment{
		Server:       "https://kodelet.example",
		Workspace:    workspace,
		EnrollmentID: "enrollment-one",
		DeviceCode:   deviceCode,
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		ExpiresAt:    time.Now().UTC().Add(time.Minute),
	}))

	registrations, err := store.Registrations()
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	payload, err := json.Marshal(registrations)
	require.NoError(t, err)
	serialized := string(payload)
	assert.NotContains(t, serialized, "credential-secret")
	assert.NotContains(t, serialized, accessToken)
	assert.NotContains(t, serialized, deviceCode)
	assert.NotContains(t, serialized, base64.RawURLEncoding.EncodeToString(privateKey))
	assert.NotContains(t, serialized, fingerprint)
}

func TestCredentialStateRejectsInvalidArguments(t *testing.T) {
	workspace := t.TempDir()
	publicKey, privateKey, fingerprint := testCredentialKeyPair(t, 6)
	validCredential := Credential{
		Server:       "https://kodelet.example",
		Workspace:    workspace,
		CredentialID: "credential-one",
		AccessToken:  testRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}

	_, _, err := (*Store)(nil).LoadCredential(validCredential.Server, workspace)
	require.ErrorContains(t, err, "runner state store is required")
	require.ErrorContains(t, (*Store)(nil).SaveCredential(validCredential), "runner state store is required")
	_, err = (*Store)(nil).DeleteCredential(validCredential.Server, workspace)
	require.ErrorContains(t, err, "runner state store is required")
	_, _, err = (*Store)(nil).LoadPendingEnrollment(validCredential.Server, workspace)
	require.ErrorContains(t, err, "runner state store is required")
	require.ErrorContains(t, (*Store)(nil).SavePendingEnrollment(PendingEnrollment{}), "runner state store is required")
	_, err = (*Store)(nil).DeletePendingEnrollment(validCredential.Server, workspace)
	require.ErrorContains(t, err, "runner state store is required")

	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	missingID := validCredential
	missingID.CredentialID = ""
	require.ErrorContains(t, store.SaveCredential(missingID), "credential id is required")
	_, _, err = store.LoadCredential("://bad", workspace)
	require.Error(t, err)
	_, _, err = store.LoadCredential(validCredential.Server, filepath.Join(workspace, "missing"))
	require.ErrorContains(t, err, "resolve workspace symlinks")

	pending := PendingEnrollment{
		Server:      validCredential.Server,
		Workspace:   workspace,
		Fingerprint: fingerprint,
		PublicKey:   publicKey,
		PrivateKey:  privateKey,
		ExpiresAt:   time.Now().UTC().Add(time.Minute),
	}
	require.ErrorContains(t, store.SavePendingEnrollment(pending), "enrollment id and device code")
	pending.EnrollmentID = "enrollment-one"
	pending.DeviceCode = testRunnerAccessToken(t)
	pending.ExpiresAt = time.Time{}
	require.ErrorContains(t, store.SavePendingEnrollment(pending), "expiration")
	pending.ExpiresAt = time.Now().UTC().Add(time.Minute)
	pending.PollIntervalMS = -1
	require.ErrorContains(t, store.SavePendingEnrollment(pending), "poll interval")
}

func testRunnerAccessToken(t *testing.T) string {
	t.Helper()
	token, err := protocol.NewRunnerAccessToken()
	require.NoError(t, err)
	return token
}

func testCredentialKeyPair(t *testing.T, discriminator byte) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i) + discriminator
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	require.NoError(t, err)
	return publicKey, privateKey, fingerprint
}

func onlyJSONStateFile(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	require.Len(t, paths, 1)
	return paths[0]
}

func assertStateLockFilesSecure(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	var found bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		found = true
		info, err := entry.Info()
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	assert.True(t, found)
}
