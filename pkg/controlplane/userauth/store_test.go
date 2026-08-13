package userauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStoreUsesSecureDirectoriesAndDefaultRoot(t *testing.T) {
	base := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", base)
	store, err := NewStore()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "control-plane-auth"), store.Root())
	if runtime.GOOS == "windows" {
		return
	}
	for _, directory := range []string{store.Root(), filepath.Join(store.Root(), "credentials"), filepath.Join(store.Root(), "logins")} {
		info, statErr := os.Stat(directory)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), directory)
	}
}

func TestStorePersistsCanonicalServerStateWithSecureFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "auth-state")
	store, err := NewStoreAt(root)
	require.NoError(t, err)
	now := time.Date(2030, time.February, 3, 4, 5, 6, 0, time.UTC)
	rawServer := "http://LOCALHOST:80/base/./"
	canonicalServer := "http://localhost/base"
	credential := testCredential(rawServer, "credential-1", 0x51, now)
	require.NoError(t, store.SaveCredential(credential))
	pending := testPendingLogin(rawServer, "authorization-1", 0x52, now)
	require.NoError(t, store.SavePendingLogin(pending))

	loadedCredential, found, err := store.LoadCredential(canonicalServer + "/")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, stateVersion, loadedCredential.Version)
	assert.Equal(t, canonicalServer, loadedCredential.Server)
	assert.Equal(t, credential.CredentialID, loadedCredential.CredentialID)
	assert.Equal(t, credential.BearerToken, loadedCredential.BearerToken)

	loadedPending, found, err := store.LoadPendingLogin(canonicalServer)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, stateVersion, loadedPending.Version)
	assert.Equal(t, canonicalServer, loadedPending.Server)
	assert.Equal(t, pending.AuthorizationID, loadedPending.AuthorizationID)
	assert.Equal(t, pending.BearerToken, loadedPending.BearerToken)

	credentialPath := filepath.Join(root, "credentials", stateKey(canonicalServer)+".json")
	pendingPath := filepath.Join(root, "logins", stateKey(canonicalServer)+".json")
	for _, path := range []string{
		credentialPath,
		pendingPath,
		filepath.Join(root, "credentials", stateKey(canonicalServer)+".lock"),
		filepath.Join(root, "logins", stateKey(canonicalServer)+".lock"),
	} {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		if runtime.GOOS != "windows" {
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), path)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "credentials"))
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), canonicalServer)
	}
}

func TestStoreExpectedIDDeletesDoNotRemoveReplacementState(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	now := time.Date(2030, time.March, 4, 5, 6, 7, 0, time.UTC)
	server := "http://localhost:8080"

	require.NoError(t, store.SaveCredential(testCredential(server, "credential-old", 0x61, now)))
	require.NoError(t, store.SaveCredential(testCredential(server, "credential-new", 0x62, now)))
	removed, err := store.DeleteCredential(server, "credential-old")
	require.NoError(t, err)
	assert.False(t, removed)
	credential, found, err := store.LoadCredential(server)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-new", credential.CredentialID)
	removed, err = store.DeleteCredential(server, "credential-new")
	require.NoError(t, err)
	assert.True(t, removed)

	require.NoError(t, store.SavePendingLogin(testPendingLogin(server, "authorization-old", 0x63, now)))
	require.NoError(t, store.SavePendingLogin(testPendingLogin(server, "authorization-new", 0x64, now)))
	removed, err = store.DeletePendingLogin(server, "authorization-old")
	require.NoError(t, err)
	assert.False(t, removed)
	pending, found, err := store.LoadPendingLogin(server)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "authorization-new", pending.AuthorizationID)
	removed, err = store.DeletePendingLogin(server, "authorization-new")
	require.NoError(t, err)
	assert.True(t, removed)
}

func TestStoreLoadsExpiredStateButRejectsTampering(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	server := "http://localhost:8181"
	createdAt := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiredCredential := testCredential(server, "credential-expired", 0x71, createdAt)
	expiredCredential.ExpiresAt = createdAt.Add(time.Hour)
	require.NoError(t, store.SaveCredential(expiredCredential))
	loaded, found, err := store.LoadCredential(server)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, expiredCredential.ExpiresAt, loaded.ExpiresAt)

	path := store.credentialPath(server)
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(payload, &stored))
	stored["server"] = "http://localhost:9191"
	payload, err = json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	_, _, err = store.LoadCredential(server)
	require.ErrorContains(t, err, "stored server")

	require.NoError(t, store.SaveCredential(expiredCredential))
	payload, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(payload, &stored))
	stored["bearerToken"] = "kltu_not-canonical"
	stored["unexpected"] = true
	payload, err = json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	_, _, err = store.LoadCredential(server)
	require.ErrorContains(t, err, "unknown field")
}

func TestStoreConcurrentWritesRemainValid(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	server := "http://localhost:8282"
	now := time.Date(2030, time.April, 5, 6, 7, 8, 0, time.UTC)
	const writers = 16
	start := make(chan struct{})
	errorsByWriter := make(chan error, writers)
	var wait sync.WaitGroup
	for i := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			credential := testCredential(server, "credential-"+string(rune('a'+i)), byte(0x80+i), now)
			errorsByWriter <- store.SaveCredential(credential)
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByWriter)
	for writeErr := range errorsByWriter {
		require.NoError(t, writeErr)
	}
	credential, found, err := store.LoadCredential(server)
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, validateStoredCredential(credential, server))
}

func testCredential(server, credentialID string, discriminator byte, createdAt time.Time) Credential {
	return Credential{
		Server:       server,
		CredentialID: credentialID,
		BearerToken:  testBearerToken(discriminator),
		Principal:    testPrincipalSnapshot(),
		CreatedAt:    createdAt,
		ExpiresAt:    createdAt.Add(24 * time.Hour),
	}
}

func testPendingLogin(server, authorizationID string, discriminator byte, createdAt time.Time) PendingLogin {
	return PendingLogin{
		Server:                  server,
		AuthorizationID:         authorizationID,
		DeviceCode:              "device-secret-" + authorizationID,
		UserCode:                "ABCD-EFGH",
		VerificationURL:         "https://kodelet.example/auth/device",
		VerificationURLComplete: "https://kodelet.example/auth/device?user_code=ABCD-EFGH",
		BearerToken:             testBearerToken(discriminator),
		ExpiresAt:               createdAt.Add(10 * time.Minute),
		PollIntervalMS:          2000,
		CreatedAt:               createdAt,
	}
}
