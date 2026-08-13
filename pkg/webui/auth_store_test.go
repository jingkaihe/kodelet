package webui

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	stdErrors "errors"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	dbmigrations "github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type authStoreTestClock struct {
	current time.Time
}

func (c *authStoreTestClock) now() time.Time {
	return c.current
}

func newAuthStoreTest(t *testing.T) (*authStore, *authStoreTestClock) {
	t.Helper()
	database, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "auth.db"))
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), dbmigrations.All()))
	clock := &authStoreTestClock{current: time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)}
	store := &authStore{db: database, now: clock.now}
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store, clock
}

func TestAuthStoreWebSessionLifecycle(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), " https://issuer.example.com ", " subject-1 ", " Test User ", " user@example.com ", []string{" USER ", "terminal", "user"}, time.Minute)
	require.NoError(t, err)
	assert.NotEmpty(t, sessionToken)
	assert.NotEmpty(t, csrfToken)
	assert.NotEqual(t, sessionToken, csrfToken)

	var storedTokenHash, storedCSRFHash []byte
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `SELECT token_sha256, csrf_token_sha256 FROM web_auth_sessions`).Scan(&storedTokenHash, &storedCSRFHash))
	assert.Equal(t, authHash(sessionToken), storedTokenHash)
	assert.Equal(t, authHash(csrfToken), storedCSRFHash)
	assert.NotEqual(t, []byte(sessionToken), storedTokenHash)
	assert.NotEqual(t, []byte(csrfToken), storedCSRFHash)

	session, found, err := store.LoadWebSession(t.Context(), sessionToken)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "https://issuer.example.com", session.Issuer)
	assert.Equal(t, "subject-1", session.Subject)
	assert.Equal(t, "Test User", session.Name)
	assert.Equal(t, "user@example.com", session.Email)
	assert.Equal(t, []string{"user", "terminal"}, session.Roles)
	valid, err := store.WebSessionCSRFValid(t.Context(), session.ID, csrfToken)
	require.NoError(t, err)
	assert.True(t, valid)
	valid, err = store.WebSessionCSRFValid(t.Context(), session.ID, "wrong-csrf-token")
	require.NoError(t, err)
	assert.False(t, valid)
	_, found, err = store.LoadWebSession(t.Context(), "wrong-session-token")
	require.NoError(t, err)
	assert.False(t, found)

	clock.current = clock.current.Add(time.Minute)
	_, found, err = store.LoadWebSession(t.Context(), sessionToken)
	require.NoError(t, err)
	assert.False(t, found)
	valid, err = store.WebSessionCSRFValid(t.Context(), session.ID, csrfToken)
	require.NoError(t, err)
	assert.False(t, valid)

	secondToken, _, err := store.CreateWebSession(t.Context(), "issuer", "subject-2", "", "", nil, time.Minute)
	require.NoError(t, err)
	require.NoError(t, store.DeleteWebSession(t.Context(), secondToken))
	_, found, err = store.LoadWebSession(t.Context(), secondToken)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestAuthStoreOIDCTransactionIsSingleUseAndExpiryBound(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	require.NoError(t, store.CreateOIDCTransaction(t.Context(), "state-one", "nonce-one", "verifier-one", "/chat?id=1", time.Minute))

	var storedHash []byte
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `SELECT state_sha256 FROM oidc_login_transactions`).Scan(&storedHash))
	assert.Equal(t, authHash("state-one"), storedHash)
	assert.NotEqual(t, []byte("state-one"), storedHash)

	start := make(chan struct{})
	type consumeResult struct {
		transaction oidcLoginTransaction
		err         error
	}
	results := make(chan consumeResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			transaction, err := store.ConsumeOIDCTransaction(t.Context(), "state-one")
			results <- consumeResult{transaction: transaction, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	var consumed, rejected int
	for result := range results {
		switch {
		case result.err == nil:
			consumed++
			assert.Equal(t, "nonce-one", result.transaction.Nonce)
			assert.Equal(t, "verifier-one", result.transaction.PKCEVerifier)
			assert.Equal(t, "/chat?id=1", result.transaction.ReturnTo)
		case stdErrors.Is(result.err, errOIDCTransactionNotFound):
			rejected++
		default:
			t.Fatalf("unexpected OIDC consume error: %v", result.err)
		}
	}
	assert.Equal(t, 1, consumed)
	assert.Equal(t, 1, rejected)

	require.NoError(t, store.CreateOIDCTransaction(t.Context(), "state-expired", "nonce", "verifier", "/", time.Minute))
	clock.current = clock.current.Add(time.Minute)
	_, err := store.ConsumeOIDCTransaction(t.Context(), "state-expired")
	require.ErrorIs(t, err, errOIDCTransactionNotFound)
	var count int
	require.NoError(t, store.db.GetContext(t.Context(), &count, `SELECT COUNT(*) FROM oidc_login_transactions WHERE state_sha256 = ?`, authHash("state-expired")))
	assert.Zero(t, count)

	for i := range maxPendingOIDCTransactions {
		require.NoError(t, store.CreateOIDCTransaction(t.Context(), "state-"+strconv.Itoa(i), "nonce", "verifier", "/", time.Minute))
	}
	require.ErrorIs(t, store.CreateOIDCTransaction(t.Context(), "state-over-limit", "nonce", "verifier", "/", time.Minute), errTooManyOIDCTransactions)
}

func TestAuthStoreRunnerEnrollmentApprovalPollingAndReplacement(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	request := testEnrollmentRequest(t, " host-one ", " /work/project ", " project ", 0x31)
	request.DisplayName = " project runner "
	request.KodeletVersion = " v-test "
	started, err := store.StartRunnerEnrollment(t.Context(), request, "https://kodelet.example/runner/enroll?source=test")
	require.NoError(t, err)
	assert.Equal(t, "https://kodelet.example/runner/enroll?source=test", started.VerificationURL)
	assert.Contains(t, started.VerificationURLComplete, "source=test")
	assert.Contains(t, started.VerificationURLComplete, "user_code="+started.UserCode)

	pending, err := store.RunnerEnrollmentByUserCode(t.Context(), started.UserCode)
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusPending, pending.Status)
	assert.Equal(t, "project runner", pending.DisplayName)
	assert.Equal(t, "host-one", pending.Host.InstanceID)
	assert.Equal(t, "/work/project", pending.Workspace.Path)
	assert.Equal(t, "project", pending.Workspace.Name)
	assert.Equal(t, "v-test", pending.KodeletVersion)
	assert.False(t, pending.ReplaceNeeded)

	pollRequest := protocol.EnrollmentPollRequest{EnrollmentID: started.EnrollmentID, DeviceCode: started.DeviceCode}
	polled, err := store.PollRunnerEnrollment(t.Context(), pollRequest)
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusPending, polled.Status)
	assert.Equal(t, defaultRunnerPollInterval.Milliseconds(), polled.RetryAfterMS)
	_, err = store.PollRunnerEnrollment(t.Context(), pollRequest)
	var slowDown *enrollmentSlowDownError
	require.ErrorAs(t, err, &slowDown)
	assert.Equal(t, defaultRunnerPollInterval, slowDown.RetryAfter)

	insertAuthStoreTestRunner(t, store, "runner-one", "host-one", "/work/project", "project")
	approved, err := store.ApproveRunnerEnrollment(t.Context(), started.UserCode, " issuer|admin ", " runner-one ", false)
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusApproved, approved.Status)
	assert.Equal(t, "runner-one", approved.RunnerID)
	assert.NotEmpty(t, approved.CredentialID)
	firstCredentialID := approved.CredentialID

	polled, err = store.PollRunnerEnrollment(t.Context(), pollRequest)
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusApproved, polled.Status)
	assert.Equal(t, firstCredentialID, polled.CredentialID)
	assert.Equal(t, started.DeviceCode, polled.AccessToken)
	assert.Equal(t, protocol.DPoPAuthorizationScheme, polled.TokenType)
	assert.Equal(t, "runner-one", polled.RunnerID)
	assert.Equal(t, pending.Fingerprint, polled.Fingerprint)
	active, err := store.HasActiveRunnerCredential(t.Context(), "host-one", "/work/project")
	require.NoError(t, err)
	assert.True(t, active)
	active, err = store.RunnerCredentialActive(t.Context(), firstCredentialID, "runner-one", "host-one", "/work/project")
	require.NoError(t, err)
	assert.True(t, active)

	var approvedBy string
	require.NoError(t, store.db.GetContext(t.Context(), &approvedBy, `SELECT approved_by FROM runner_credentials WHERE id = ?`, firstCredentialID))
	assert.Equal(t, "issuer|admin", approvedBy)

	replacementRequest := testEnrollmentRequest(t, "host-one", "/work/project", "project", 0x32)
	replacement, err := store.StartRunnerEnrollment(t.Context(), replacementRequest, "https://kodelet.example/runner/enroll")
	require.NoError(t, err)
	replacementView, err := store.RunnerEnrollmentByUserCode(t.Context(), replacement.UserCode)
	require.NoError(t, err)
	assert.True(t, replacementView.ReplaceNeeded)
	_, err = store.ApproveRunnerEnrollment(t.Context(), replacement.UserCode, "issuer|admin", "runner-one", false)
	require.ErrorIs(t, err, errRunnerCredentialExists)
	replacementApproved, err := store.ApproveRunnerEnrollment(t.Context(), replacement.UserCode, "issuer|admin", "runner-one", true)
	require.NoError(t, err)
	assert.NotEqual(t, firstCredentialID, replacementApproved.CredentialID)

	var revokedAt time.Time
	var reason string
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `SELECT revoked_at, revoke_reason FROM runner_credentials WHERE id = ?`, firstCredentialID).Scan(&revokedAt, &reason))
	assert.Equal(t, clock.current, revokedAt.UTC())
	assert.Equal(t, "replaced by approved enrollment", reason)
	var count int
	require.NoError(t, store.db.GetContext(t.Context(), &count, `SELECT COUNT(*) FROM runner_credentials WHERE runner_id = ? AND revoked_at IS NULL`, "runner-one"))
	assert.Equal(t, 1, count)
	active, err = store.RunnerCredentialActive(t.Context(), firstCredentialID, "runner-one", "host-one", "/work/project")
	require.NoError(t, err)
	assert.False(t, active)
	active, err = store.RunnerCredentialActive(t.Context(), replacementApproved.CredentialID, "runner-one", "host-one", "/work/project")
	require.NoError(t, err)
	assert.True(t, active)
}

func TestAuthStoreApproveRunnerEnrollmentReturnsCommittedResultWithoutReread(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	request := testEnrollmentRequest(t, "host-no-reread", "/work/no-reread", "no-reread", 0x33)
	started, err := store.StartRunnerEnrollment(t.Context(), request, "https://kodelet.example/runner/enroll")
	require.NoError(t, err)
	insertAuthStoreTestRunner(t, store, "runner-no-reread", "host-no-reread", "/work/no-reread", "no-reread")
	_, err = store.db.Exec(`
		CREATE TRIGGER mutate_approved_enrollment_user_code
		AFTER UPDATE OF status ON runner_enrollments
		WHEN NEW.status = 'approved'
		BEGIN
			UPDATE runner_enrollments SET user_code = 'ZZZZ-ZZZZ' WHERE id = NEW.id;
		END
	`)
	require.NoError(t, err)

	approved, err := store.ApproveRunnerEnrollment(t.Context(), started.UserCode, "issuer|admin", "runner-no-reread", false)
	require.NoError(t, err)
	assert.Equal(t, started.EnrollmentID, approved.ID)
	assert.Equal(t, protocol.EnrollmentStatusApproved, approved.Status)
	assert.Equal(t, started.UserCode, approved.UserCode)
	assert.Equal(t, "runner-no-reread", approved.RunnerID)
	assert.NotEmpty(t, approved.CredentialID)

	var storedStatus, storedUserCode string
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `SELECT status, user_code FROM runner_enrollments WHERE id = ?`, started.EnrollmentID).Scan(&storedStatus, &storedUserCode))
	assert.Equal(t, string(protocol.EnrollmentStatusApproved), storedStatus)
	assert.Equal(t, "ZZZZ-ZZZZ", storedUserCode)
}

func TestAuthStoreRunnerEnrollmentDenialAndExpiry(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	deniedRequest := testEnrollmentRequest(t, "host-denied", "/work/denied", "denied", 0x41)
	denied, err := store.StartRunnerEnrollment(t.Context(), deniedRequest, "https://kodelet.example/runner/enroll")
	require.NoError(t, err)
	require.NoError(t, store.DenyRunnerEnrollment(t.Context(), denied.UserCode, "issuer|admin"))
	view, err := store.RunnerEnrollmentByUserCode(t.Context(), denied.UserCode)
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusDenied, view.Status)
	polled, err := store.PollRunnerEnrollment(t.Context(), protocol.EnrollmentPollRequest{EnrollmentID: denied.EnrollmentID, DeviceCode: denied.DeviceCode})
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusDenied, polled.Status)
	require.ErrorIs(t, store.DenyRunnerEnrollment(t.Context(), denied.UserCode, "issuer|admin"), errEnrollmentNotPending)

	expiredRequest := testEnrollmentRequest(t, "host-expired", "/work/expired", "expired", 0x42)
	expired, err := store.StartRunnerEnrollment(t.Context(), expiredRequest, "https://kodelet.example/runner/enroll")
	require.NoError(t, err)
	clock.current = expired.ExpiresAt
	view, err = store.RunnerEnrollmentByUserCode(t.Context(), expired.UserCode)
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusExpired, view.Status)
	_, err = store.ApproveRunnerEnrollment(t.Context(), expired.UserCode, "issuer|admin", "runner-missing", false)
	require.ErrorIs(t, err, errEnrollmentExpired)
	require.ErrorIs(t, store.DenyRunnerEnrollment(t.Context(), expired.UserCode, "issuer|admin"), errEnrollmentExpired)
	polled, err = store.PollRunnerEnrollment(t.Context(), protocol.EnrollmentPollRequest{EnrollmentID: expired.EnrollmentID, DeviceCode: expired.DeviceCode})
	require.NoError(t, err)
	assert.Equal(t, protocol.EnrollmentStatusExpired, polled.Status)
}

func TestAuthStoreRunnerDPoPProofIsBoundOneUseAndRevocable(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	publicKey, privateKey := testEd25519KeyPair(0x51)
	insertAuthStoreTestRunner(t, store, "runner-proof", "host-proof", "/work/proof", "proof")
	accessToken := insertAuthStoreTestCredential(t, store, "credential-proof", "runner-proof", publicKey, clock.current)
	targetURL := "https://kodelet.example/api/runner/v1/connect"
	proof, err := protocol.SignDPoPProof(privateKey, protocol.DPoPProofOptions{
		Method:      http.MethodGet,
		TargetURL:   targetURL,
		AccessToken: accessToken,
		JTI:         "proof-one",
		IssuedAt:    clock.current,
	})
	require.NoError(t, err)
	identity, err := store.VerifyRunnerDPoP(t.Context(), accessToken, proof, http.MethodGet, targetURL)
	require.NoError(t, err)
	assert.Equal(t, runnerCredentialIdentity{
		CredentialID:   "credential-proof",
		RunnerID:       "runner-proof",
		HostInstanceID: "host-proof",
		WorkspacePath:  "/work/proof",
	}, identity)
	var storedHash []byte
	require.NoError(t, store.db.GetContext(t.Context(), &storedHash, `SELECT jti_sha256 FROM runner_dpop_replays WHERE credential_id = ?`, "credential-proof"))
	assert.Equal(t, authHash("proof-one"), storedHash)
	assert.NotEqual(t, []byte("proof-one"), storedHash)
	_, err = store.VerifyRunnerDPoP(t.Context(), accessToken, proof, http.MethodGet, targetURL)
	require.ErrorIs(t, err, errRunnerDPoPReplay)

	wrongPublicKey, wrongPrivateKey := testEd25519KeyPair(0x52)
	assert.NotEqual(t, publicKey, wrongPublicKey)
	wrongProof, err := protocol.SignDPoPProof(wrongPrivateKey, protocol.DPoPProofOptions{Method: http.MethodGet, TargetURL: targetURL, AccessToken: accessToken, JTI: "wrong-key", IssuedAt: clock.current})
	require.NoError(t, err)
	_, err = store.VerifyRunnerDPoP(t.Context(), accessToken, wrongProof, http.MethodGet, targetURL)
	require.ErrorIs(t, err, errRunnerDPoPInvalid)
	wrongTargetProof, err := protocol.SignDPoPProof(privateKey, protocol.DPoPProofOptions{Method: http.MethodGet, TargetURL: targetURL + "/other", AccessToken: accessToken, JTI: "wrong-target", IssuedAt: clock.current})
	require.NoError(t, err)
	_, err = store.VerifyRunnerDPoP(t.Context(), accessToken, wrongTargetProof, http.MethodGet, targetURL)
	require.ErrorIs(t, err, errRunnerDPoPInvalid)
	expiredProof, err := protocol.SignDPoPProof(privateKey, protocol.DPoPProofOptions{Method: http.MethodGet, TargetURL: targetURL, AccessToken: accessToken, JTI: "expired", IssuedAt: clock.current.Add(-defaultRunnerDPoPProofMaxAge - time.Second)})
	require.NoError(t, err)
	_, err = store.VerifyRunnerDPoP(t.Context(), accessToken, expiredProof, http.MethodGet, targetURL)
	require.ErrorIs(t, err, errRunnerDPoPInvalid)

	revokedProof, err := protocol.SignDPoPProof(privateKey, protocol.DPoPProofOptions{Method: http.MethodGet, TargetURL: targetURL, AccessToken: accessToken, JTI: "revoked", IssuedAt: clock.current})
	require.NoError(t, err)
	_, err = store.db.ExecContext(t.Context(), `UPDATE runner_credentials SET revoked_at = ?, revoke_reason = ? WHERE id = ?`, clock.current, "test revocation", "credential-proof")
	require.NoError(t, err)
	_, err = store.VerifyRunnerDPoP(t.Context(), accessToken, revokedProof, http.MethodGet, targetURL)
	require.ErrorIs(t, err, errRunnerCredentialInvalid)
}

func TestAuthStoreLimitsActiveRunnerDPoPProofs(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	publicKey, privateKey := testEd25519KeyPair(0x53)
	insertAuthStoreTestRunner(t, store, "runner-dpop-limit", "host-limit", "/work/limit", "limit")
	accessToken := insertAuthStoreTestCredential(t, store, "credential-dpop-limit", "runner-dpop-limit", publicKey, clock.current)
	_, err := store.db.ExecContext(t.Context(), `
		WITH RECURSIVE proof_count(value) AS (
			VALUES(1)
			UNION ALL
			SELECT value + 1 FROM proof_count WHERE value < ?
		)
		INSERT INTO runner_dpop_replays (credential_id, jti_sha256, created_at, expires_at)
		SELECT ?, randomblob(32), ?, ? FROM proof_count
	`, maxActiveRunnerDPoPReplays, "credential-dpop-limit", clock.current, clock.current.Add(time.Minute))
	require.NoError(t, err)
	targetURL := "https://kodelet.example/api/runner/v1/connect"
	proof, err := protocol.SignDPoPProof(privateKey, protocol.DPoPProofOptions{Method: http.MethodGet, TargetURL: targetURL, AccessToken: accessToken, JTI: "over-limit", IssuedAt: clock.current})
	require.NoError(t, err)
	_, err = store.VerifyRunnerDPoP(t.Context(), accessToken, proof, http.MethodGet, targetURL)
	require.ErrorIs(t, err, errTooManyRunnerDPoPProofs)

	clock.current = clock.current.Add(time.Minute)
	proof, err = protocol.SignDPoPProof(privateKey, protocol.DPoPProofOptions{Method: http.MethodGet, TargetURL: targetURL, AccessToken: accessToken, JTI: "after-expiry", IssuedAt: clock.current})
	require.NoError(t, err)
	_, err = store.VerifyRunnerDPoP(t.Context(), accessToken, proof, http.MethodGet, targetURL)
	require.NoError(t, err)
}

func testEnrollmentRequest(t *testing.T, hostInstanceID, workspacePath, workspaceName string, discriminator byte) protocol.EnrollmentStartRequest {
	t.Helper()
	publicKey, _ := testEd25519KeyPair(discriminator)
	encodedPublicKey, err := protocol.EncodePublicKey(publicKey)
	require.NoError(t, err)
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	require.NoError(t, err)
	return protocol.EnrollmentStartRequest{
		ProtocolVersions: []int{protocol.Version},
		PublicKey:        encodedPublicKey,
		Fingerprint:      fingerprint,
		Host: protocol.Host{
			InstanceID: hostInstanceID,
			Hostname:   "host.example.test",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace:      protocol.Workspace{Path: workspacePath, Name: workspaceName},
		DisplayName:    workspaceName,
		KodeletVersion: "v-test",
	}
}

func testEd25519KeyPair(discriminator byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := bytes.Repeat([]byte{discriminator}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	return privateKey.Public().(ed25519.PublicKey), privateKey
}

func insertAuthStoreTestRunner(t *testing.T, store *authStore, runnerID, hostInstanceID, workspacePath, workspaceName string) {
	t.Helper()
	now := store.now().UTC()
	_, err := store.db.ExecContext(t.Context(), `
		INSERT INTO runner_registrations (
			id, owner_id, host_instance_id, workspace_path, workspace_name,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, runnerID, controlPlaneOwnerID, hostInstanceID, workspacePath, workspaceName, "offline", now, now)
	require.NoError(t, err)
}

func insertAuthStoreTestCredential(t *testing.T, store *authStore, credentialID, runnerID string, publicKey ed25519.PublicKey, createdAt time.Time) string {
	t.Helper()
	accessToken, err := protocol.NewRunnerAccessToken()
	require.NoError(t, err)
	fingerprintHash := sha256.Sum256(publicKey)
	_, err = store.db.ExecContext(t.Context(), `
		INSERT INTO runner_credentials (
			id, runner_id, key_algorithm, public_key, public_key_sha256,
			approved_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, credentialID, runnerID, runnerCredentialKeyAlgorithm, []byte(publicKey), fingerprintHash[:], "issuer|admin", createdAt)
	require.NoError(t, err)
	_, err = store.db.ExecContext(t.Context(), `
		INSERT INTO runner_enrollments (
			id, device_code_sha256, user_code, owner_id, status, protocol_version,
			key_algorithm, public_key, public_key_sha256, host_instance_id,
			workspace_path, workspace_name, poll_interval_seconds, created_at,
			expires_at, approved_at, approved_by, delivered_at, runner_id, credential_id
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, host_instance_id,
			workspace_path, workspace_name, ?, ?, ?, ?, ?, ?, id, ?
		FROM runner_registrations WHERE id = ?
	`, "enrollment-"+credentialID, authHash(accessToken), "CODE-"+credentialID, controlPlaneOwnerID, protocol.EnrollmentStatusApproved,
		protocol.Version, runnerCredentialKeyAlgorithm, []byte(publicKey), fingerprintHash[:], int64(defaultRunnerPollInterval/time.Second),
		createdAt, createdAt.Add(defaultRunnerEnrollmentTTL), createdAt, "issuer|admin", createdAt, credentialID, runnerID)
	require.NoError(t, err)
	return accessToken
}
