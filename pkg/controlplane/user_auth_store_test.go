package controlplane

import (
	"bytes"
	stdErrors "errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthStoreUserLoginLifecycleAndSecretStorage(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	request := testUserLoginStartRequest()
	started, err := store.StartUserLogin(t.Context(), request, "https://kodelet.example/auth/device?source=test")
	require.NoError(t, err)
	require.NoError(t, started.ValidateAt(clock.current))
	assert.NotEmpty(t, started.AuthorizationID)
	assert.NotEmpty(t, started.DeviceCode)
	assert.NotEmpty(t, started.BearerToken)
	assert.Equal(t, clock.current.Add(defaultUserLoginTTL), started.ExpiresAt)
	assert.Equal(t, defaultUserPollInterval.Milliseconds(), started.PollIntervalMS)
	assert.Equal(t, "https://kodelet.example/auth/device?source=test", started.VerificationURL)
	assert.Empty(t, started.VerificationURLComplete)

	var storedDeviceHash, storedTokenHash []byte
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `
		SELECT device_code_sha256, token_sha256
		FROM user_login_authorizations WHERE id = ?
	`, started.AuthorizationID).Scan(&storedDeviceHash, &storedTokenHash))
	assert.Equal(t, authHash(started.DeviceCode), storedDeviceHash)
	assert.Equal(t, authHash(started.BearerToken), storedTokenHash)
	assert.Len(t, storedDeviceHash, 32)
	assert.Len(t, storedTokenHash, 32)
	assert.NotEqual(t, []byte(started.DeviceCode), storedDeviceHash)
	assert.NotEqual(t, []byte(started.BearerToken), storedTokenHash)
	assertUserAuthSecretsAbsentFromDatabaseFiles(t, store, started.DeviceCode, started.BearerToken)

	enteredCode := strings.ToLower(strings.ReplaceAll(started.UserCode, "-", " "))
	pendingView, err := store.UserLoginByUserCode(t.Context(), enteredCode)
	require.NoError(t, err)
	assert.Equal(t, userLoginAuthorization{
		ID:             started.AuthorizationID,
		Status:         userauth.DeviceStatusPending,
		UserCode:       started.UserCode,
		ClientName:     request.ClientName,
		ClientOS:       request.ClientOS,
		ClientArch:     request.ClientArch,
		KodeletVersion: request.KodeletVersion,
		ExpiresAt:      started.ExpiresAt,
	}, pendingView)

	pollRequest := userauth.DevicePollRequest{AuthorizationID: started.AuthorizationID, DeviceCode: started.DeviceCode}
	pendingPoll, err := store.PollUserLogin(t.Context(), pollRequest)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusPending, pendingPoll.Status)
	assert.Equal(t, defaultUserPollInterval.Milliseconds(), pendingPoll.RetryAfterMS)

	principal := testUserLoginPrincipal()
	credentialDuration := 2 * time.Hour
	approved, err := store.ApproveUserLogin(t.Context(), enteredCode, principal, credentialDuration)
	require.NoError(t, err)
	expectedPrincipal := userauth.PrincipalSnapshot{
		ID:      "https://issuer.example|subject-1",
		Issuer:  "https://issuer.example",
		Subject: "subject-1",
		Name:    "Test User",
		Email:   "user@example.com",
		Roles:   []string{"terminal", "user"},
	}
	assert.Equal(t, userauth.DeviceStatusApproved, approved.Status)
	assert.Equal(t, started.AuthorizationID, approved.ID)
	assert.Equal(t, started.UserCode, approved.UserCode)
	assert.Equal(t, request.ClientName, approved.ClientName)
	assert.NotEmpty(t, approved.CredentialID)
	assert.Equal(t, expectedPrincipal, approved.Principal)
	assert.Equal(t, clock.current.Add(credentialDuration), approved.CredentialExpiresAt)
	principal.Roles[0] = "admin"

	approvedByCode, err := store.UserLoginByUserCode(t.Context(), started.UserCode)
	require.NoError(t, err)
	assert.Equal(t, approved, approvedByCode)
	approvedPoll, err := store.PollUserLogin(t.Context(), pollRequest)
	require.NoError(t, err)
	require.NoError(t, approvedPoll.ValidateAt(clock.current))
	assert.Equal(t, userauth.DeviceStatusApproved, approvedPoll.Status)
	assert.Equal(t, approved.CredentialID, approvedPoll.CredentialID)
	assert.Equal(t, expectedPrincipal, approvedPoll.Principal)
	assert.Equal(t, approved.CredentialExpiresAt, approvedPoll.ExpiresAt)

	var credentialTokenHash []byte
	var issuer, subject, name, email, rolesJSON, approvedBy string
	var deliveredAt time.Time
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `
		SELECT c.token_sha256, c.issuer, c.subject, COALESCE(c.name, ''),
			COALESCE(c.email, ''), c.roles_json, a.approved_by, a.delivered_at
		FROM user_api_credentials c
		JOIN user_login_authorizations a ON a.credential_id = c.id
		WHERE c.id = ?
	`, approved.CredentialID).Scan(
		&credentialTokenHash, &issuer, &subject, &name, &email, &rolesJSON, &approvedBy, &deliveredAt,
	))
	assert.Equal(t, storedTokenHash, credentialTokenHash)
	assert.Equal(t, authHash(started.BearerToken), credentialTokenHash)
	assert.NotEqual(t, []byte(started.BearerToken), credentialTokenHash)
	assert.Equal(t, expectedPrincipal.Issuer, issuer)
	assert.Equal(t, expectedPrincipal.Subject, subject)
	assert.Equal(t, expectedPrincipal.Name, name)
	assert.Equal(t, expectedPrincipal.Email, email)
	assert.JSONEq(t, `["terminal","user"]`, rolesJSON)
	assert.Equal(t, expectedPrincipal.ID, approvedBy)
	assert.Equal(t, clock.current, deliveredAt.UTC())
	assertUserAuthSecretsAbsentFromDatabaseFiles(t, store, started.DeviceCode, started.BearerToken)

	identity, err := store.LoadUserCredential(t.Context(), started.BearerToken)
	require.NoError(t, err)
	assert.Equal(t, userCredentialIdentity{CredentialID: approved.CredentialID, Principal: expectedPrincipal}, identity)
	var lastUsedAt time.Time
	require.NoError(t, store.db.GetContext(t.Context(), &lastUsedAt, `SELECT last_used_at FROM user_api_credentials WHERE id = ?`, approved.CredentialID))
	assert.Equal(t, clock.current, lastUsedAt.UTC())

	require.NoError(t, store.RevokeUserCredential(t.Context(), approved.CredentialID, " test revocation "))
	var revokedAt time.Time
	var revokeReason string
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `SELECT revoked_at, revoke_reason FROM user_api_credentials WHERE id = ?`, approved.CredentialID).Scan(&revokedAt, &revokeReason))
	assert.Equal(t, clock.current, revokedAt.UTC())
	assert.Equal(t, "test revocation", revokeReason)
	_, err = store.LoadUserCredential(t.Context(), started.BearerToken)
	require.ErrorIs(t, err, errUserCredentialInvalid)
	require.ErrorIs(t, store.RevokeUserCredential(t.Context(), approved.CredentialID, "again"), errUserCredentialInvalid)
	require.ErrorIs(t, store.RevokeUserCredential(t.Context(), "credential-missing", "missing"), errUserCredentialInvalid)
}

func TestAuthStoreUserLoginDenialAndExpiry(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	principal := testUserLoginPrincipal()

	denied, err := store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example/auth/device")
	require.NoError(t, err)
	require.NoError(t, store.DenyUserLogin(t.Context(), strings.ToLower(denied.UserCode), " issuer|admin "))
	deniedView, err := store.UserLoginByUserCode(t.Context(), denied.UserCode)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusDenied, deniedView.Status)
	deniedPoll, err := store.PollUserLogin(t.Context(), userauth.DevicePollRequest{AuthorizationID: denied.AuthorizationID, DeviceCode: denied.DeviceCode})
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusDenied, deniedPoll.Status)
	require.ErrorIs(t, store.DenyUserLogin(t.Context(), denied.UserCode, "issuer|admin"), errUserAuthorizationNotPending)
	_, err = store.ApproveUserLogin(t.Context(), denied.UserCode, principal, time.Hour)
	require.ErrorIs(t, err, errUserAuthorizationNotPending)
	var deniedBy string
	require.NoError(t, store.db.GetContext(t.Context(), &deniedBy, `SELECT denied_by FROM user_login_authorizations WHERE id = ?`, denied.AuthorizationID))
	assert.Equal(t, "issuer|admin", deniedBy)

	expired, err := store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example/auth/device")
	require.NoError(t, err)
	clock.current = expired.ExpiresAt
	expiredView, err := store.UserLoginByUserCode(t.Context(), expired.UserCode)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusExpired, expiredView.Status)
	_, err = store.ApproveUserLogin(t.Context(), expired.UserCode, principal, time.Hour)
	require.ErrorIs(t, err, errUserAuthorizationExpired)
	require.ErrorIs(t, store.DenyUserLogin(t.Context(), expired.UserCode, "issuer|admin"), errUserAuthorizationExpired)
	expiredPoll, err := store.PollUserLogin(t.Context(), userauth.DevicePollRequest{AuthorizationID: expired.AuthorizationID, DeviceCode: expired.DeviceCode})
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusExpired, expiredPoll.Status)
	var count int
	require.NoError(t, store.db.GetContext(t.Context(), &count, `SELECT COUNT(*) FROM user_login_authorizations WHERE id = ?`, expired.AuthorizationID))
	assert.Zero(t, count)
	_, err = store.PollUserLogin(t.Context(), userauth.DevicePollRequest{AuthorizationID: expired.AuthorizationID, DeviceCode: expired.DeviceCode})
	require.ErrorIs(t, err, errUserAuthorizationNotFound)
}

func TestAuthStoreUserLoginPollSlowDown(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	started, err := store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example/auth/device")
	require.NoError(t, err)
	pollRequest := userauth.DevicePollRequest{AuthorizationID: started.AuthorizationID, DeviceCode: started.DeviceCode}

	first, err := store.PollUserLogin(t.Context(), pollRequest)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusPending, first.Status)
	clock.current = clock.current.Add(2 * time.Second)
	_, err = store.PollUserLogin(t.Context(), pollRequest)
	var slowDown *userLoginSlowDownError
	require.ErrorAs(t, err, &slowDown)
	assert.Equal(t, 3*time.Second, slowDown.RetryAfter)

	var lastPolledAt time.Time
	require.NoError(t, store.db.GetContext(t.Context(), &lastPolledAt, `SELECT last_polled_at FROM user_login_authorizations WHERE id = ?`, started.AuthorizationID))
	assert.Equal(t, clock.current.Add(-2*time.Second), lastPolledAt.UTC())
	clock.current = clock.current.Add(3 * time.Second)
	polled, err := store.PollUserLogin(t.Context(), pollRequest)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusPending, polled.Status)
	assert.Equal(t, defaultUserPollInterval.Milliseconds(), polled.RetryAfterMS)
}

func TestAuthStoreUserLoginPendingLimitAndCleanup(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	tx, err := store.db.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	for index := range maxPendingUserLogins {
		_, err := tx.ExecContext(t.Context(), `
			INSERT INTO user_login_authorizations (
				id, device_code_sha256, user_code, token_sha256, status,
				client_name, client_os, client_arch, kodelet_version,
				poll_interval_seconds, created_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, fmt.Sprintf("authorization-limit-%d", index), authHash(fmt.Sprintf("device-limit-%d", index)), testUserCodeForIndex(index),
			authHash(fmt.Sprintf("token-limit-%d", index)), userauth.DeviceStatusPending,
			"kodelet", "linux", "amd64", "v-test", int64(defaultUserPollInterval/time.Second), clock.current, clock.current.Add(time.Hour))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	_, err = store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example/auth/device")
	require.ErrorIs(t, err, errTooManyUserLogins)
	_, err = store.db.ExecContext(t.Context(), `UPDATE user_login_authorizations SET expires_at = ? WHERE id = ?`, clock.current, "authorization-limit-0")
	require.NoError(t, err)
	started, err := store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example/auth/device")
	require.NoError(t, err)
	assert.NotEmpty(t, started.AuthorizationID)
	var pendingCount, expiredSeedCount int
	require.NoError(t, store.db.GetContext(t.Context(), &pendingCount, `SELECT COUNT(*) FROM user_login_authorizations WHERE status = ?`, userauth.DeviceStatusPending))
	require.NoError(t, store.db.GetContext(t.Context(), &expiredSeedCount, `SELECT COUNT(*) FROM user_login_authorizations WHERE id = ?`, "authorization-limit-0"))
	assert.Equal(t, maxPendingUserLogins, pendingCount)
	assert.Zero(t, expiredSeedCount)
}

func TestAuthStoreConcurrentUserLoginApprovalHasSingleWinner(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	started, err := store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example/auth/device")
	require.NoError(t, err)
	principal := testUserLoginPrincipal()
	start := make(chan struct{})
	type approvalResult struct {
		view userLoginAuthorization
		err  error
	}
	results := make(chan approvalResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			view, approveErr := store.ApproveUserLogin(t.Context(), started.UserCode, principal, time.Hour)
			results <- approvalResult{view: view, err: approveErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	var approved, rejected int
	for result := range results {
		switch {
		case result.err == nil:
			approved++
			assert.Equal(t, userauth.DeviceStatusApproved, result.view.Status)
			assert.NotEmpty(t, result.view.CredentialID)
		case stdErrors.Is(result.err, errUserAuthorizationNotPending):
			rejected++
		default:
			t.Fatalf("unexpected user login approval error: %v", result.err)
		}
	}
	assert.Equal(t, 1, approved)
	assert.Equal(t, 1, rejected)
	var credentialCount int
	require.NoError(t, store.db.GetContext(t.Context(), &credentialCount, `SELECT COUNT(*) FROM user_api_credentials`))
	assert.Equal(t, 1, credentialCount)
}

func TestAuthStoreUserLoginValidationAndCredentialDefaults(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	validRequest := testUserLoginStartRequest()
	invalidStarts := []struct {
		name    string
		request userauth.DeviceStartRequest
		url     string
	}{
		{name: "missing client name", request: userauth.DeviceStartRequest{ClientOS: "linux", ClientArch: "amd64", KodeletVersion: "v-test"}, url: "https://kodelet.example/auth/device"},
		{name: "request whitespace", request: userauth.DeviceStartRequest{ClientName: " kodelet", ClientOS: "linux", ClientArch: "amd64", KodeletVersion: "v-test"}, url: "https://kodelet.example/auth/device"},
		{name: "request control character", request: userauth.DeviceStartRequest{ClientName: "kodelet", ClientOS: "linux", ClientArch: "amd64", KodeletVersion: "v\ntest"}, url: "https://kodelet.example/auth/device"},
		{name: "empty URL", request: validRequest, url: ""},
		{name: "URL whitespace", request: validRequest, url: " https://kodelet.example/auth/device"},
		{name: "relative URL", request: validRequest, url: "/auth/device"},
		{name: "wrong scheme", request: validRequest, url: "ftp://kodelet.example/auth/device"},
		{name: "URL user info", request: validRequest, url: "https://user@kodelet.example/auth/device"},
		{name: "URL fragment", request: validRequest, url: "https://kodelet.example/auth/device#approval"},
	}
	for _, test := range invalidStarts {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.StartUserLogin(t.Context(), test.request, test.url)
			assert.Error(t, err)
		})
	}
	var authorizationCount int
	require.NoError(t, store.db.GetContext(t.Context(), &authorizationCount, `SELECT COUNT(*) FROM user_login_authorizations`))
	assert.Zero(t, authorizationCount)

	normalized, err := normalizeUserCode(" abcd - efgh ")
	require.NoError(t, err)
	assert.Equal(t, "ABCD-EFGH", normalized)
	for _, invalidCode := range []string{"", "ABCD-EFG", "ABCI-EFGH", "ABCD_EFGH", "ABCD-EFGH-X"} {
		_, err := normalizeUserCode(invalidCode)
		assert.Error(t, err, invalidCode)
	}

	started, err := store.StartUserLogin(t.Context(), validRequest, "http://kodelet.example/auth/device")
	require.NoError(t, err)
	_, err = store.PollUserLogin(t.Context(), userauth.DevicePollRequest{})
	assert.Error(t, err)
	_, err = store.PollUserLogin(t.Context(), userauth.DevicePollRequest{AuthorizationID: " " + started.AuthorizationID, DeviceCode: started.DeviceCode})
	assert.Error(t, err)
	_, err = store.PollUserLogin(t.Context(), userauth.DevicePollRequest{AuthorizationID: started.AuthorizationID, DeviceCode: "wrong-device-code"})
	require.ErrorIs(t, err, errUserAuthorizationNotFound)
	_, err = store.UserLoginByUserCode(t.Context(), "ZZZZ-ZZZZ")
	require.ErrorIs(t, err, errUserAuthorizationNotFound)

	invalidPrincipals := []Principal{
		{ID: "token", Roles: []string{"admin"}},
		{ID: "issuer|subject", Issuer: "issuer", Roles: []string{"user"}},
		{ID: "other", Issuer: "issuer", Subject: "subject", Roles: []string{"user"}},
		{ID: "issuer|subject", Issuer: "issuer", Subject: "subject", Roles: []string{"bad\nrole"}},
	}
	for _, principal := range invalidPrincipals {
		_, err := store.ApproveUserLogin(t.Context(), started.UserCode, principal, time.Hour)
		assert.Error(t, err)
	}
	require.Error(t, store.DenyUserLogin(t.Context(), started.UserCode, " "))
	var credentialCount int
	require.NoError(t, store.db.GetContext(t.Context(), &credentialCount, `SELECT COUNT(*) FROM user_api_credentials`))
	assert.Zero(t, credentialCount)

	approved, err := store.ApproveUserLogin(t.Context(), started.UserCode, testUserLoginPrincipal(), 0)
	require.NoError(t, err)
	assert.Equal(t, clock.current.Add(defaultWebSessionDuration), approved.CredentialExpiresAt)
	for _, invalidBearer := range []string{"", " " + started.BearerToken, "Bearer " + started.BearerToken, "kltu_bad"} {
		_, err := store.LoadUserCredential(t.Context(), invalidBearer)
		require.ErrorIs(t, err, errUserCredentialInvalid, invalidBearer)
	}
	unknownBearer, err := userauth.GenerateBearerToken()
	require.NoError(t, err)
	_, err = store.LoadUserCredential(t.Context(), unknownBearer)
	require.ErrorIs(t, err, errUserCredentialInvalid)
	require.ErrorIs(t, store.RevokeUserCredential(t.Context(), "", "test"), errUserCredentialInvalid)

	clock.current = approved.CredentialExpiresAt
	_, err = store.LoadUserCredential(t.Context(), started.BearerToken)
	require.ErrorIs(t, err, errUserCredentialInvalid)
	require.ErrorIs(t, store.RevokeUserCredential(t.Context(), approved.CredentialID, "expired"), errUserCredentialInvalid)
}

func TestAuthStoreApproveUserLoginReturnsCommittedResultWithoutReread(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	started, err := store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example/auth/device")
	require.NoError(t, err)
	_, err = store.db.Exec(`
		CREATE TRIGGER mutate_approved_user_login
		AFTER UPDATE OF status ON user_login_authorizations
		WHEN NEW.status = 'approved'
		BEGIN
			UPDATE user_login_authorizations
			SET user_code = 'ZZZZ-ZZZZ', client_name = 'mutated client'
			WHERE id = NEW.id;
			UPDATE user_api_credentials
			SET name = 'Mutated User', roles_json = '["mutated"]'
			WHERE id = NEW.credential_id;
		END
	`)
	require.NoError(t, err)

	approved, err := store.ApproveUserLogin(t.Context(), started.UserCode, testUserLoginPrincipal(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, started.AuthorizationID, approved.ID)
	assert.Equal(t, started.UserCode, approved.UserCode)
	assert.Equal(t, "kodelet", approved.ClientName)
	assert.Equal(t, "Test User", approved.Principal.Name)
	assert.Equal(t, []string{"terminal", "user"}, approved.Principal.Roles)
	assert.NotEmpty(t, approved.CredentialID)

	var storedUserCode, storedClientName, storedName, storedRoles string
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `
		SELECT a.user_code, a.client_name, c.name, c.roles_json
		FROM user_login_authorizations a
		JOIN user_api_credentials c ON c.id = a.credential_id
		WHERE a.id = ?
	`, started.AuthorizationID).Scan(&storedUserCode, &storedClientName, &storedName, &storedRoles))
	assert.Equal(t, "ZZZZ-ZZZZ", storedUserCode)
	assert.Equal(t, "mutated client", storedClientName)
	assert.Equal(t, "Mutated User", storedName)
	assert.JSONEq(t, `["mutated"]`, storedRoles)
}

func testUserLoginStartRequest() userauth.DeviceStartRequest {
	return userauth.DeviceStartRequest{
		ClientName:     "kodelet",
		ClientOS:       "linux",
		ClientArch:     "amd64",
		KodeletVersion: "v-test",
	}
}

func testUserLoginPrincipal() Principal {
	return Principal{
		ID:      "https://issuer.example|subject-1",
		Issuer:  " https://issuer.example ",
		Subject: " subject-1 ",
		Name:    " Test User ",
		Email:   " user@example.com ",
		Roles:   []string{" TERMINAL ", "user", "terminal", ""},
	}
}

func testUserCodeForIndex(index int) string {
	code := make([]byte, 8)
	for position := len(code) - 1; position >= 0; position-- {
		code[position] = userCodeAlphabet[index%len(userCodeAlphabet)]
		index /= len(userCodeAlphabet)
	}
	return string(code[:4]) + "-" + string(code[4:])
}

func assertUserAuthSecretsAbsentFromDatabaseFiles(t *testing.T, store *authStore, secrets ...string) {
	t.Helper()
	var sequence int
	var name, databasePath string
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &databasePath))
	for _, path := range []string{databasePath, databasePath + "-wal"} {
		contents, err := os.ReadFile(path)
		if stdErrors.Is(err, os.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		for _, secret := range secrets {
			assert.False(t, bytes.Contains(contents, []byte(secret)), "%s contains a plaintext secret", path)
		}
	}
}
