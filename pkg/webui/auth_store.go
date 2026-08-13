package webui

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

const (
	controlPlaneOwnerID          = "local"
	defaultWebSessionDuration    = 12 * time.Hour
	defaultOIDCTransactionTTL    = 10 * time.Minute
	defaultRunnerEnrollmentTTL   = 10 * time.Minute
	defaultRunnerPollInterval    = 5 * time.Second
	defaultRunnerDPoPProofMaxAge = 5 * time.Minute
	defaultRunnerDPoPFutureSkew  = 30 * time.Second
	maxPendingOIDCTransactions   = 1024
	maxPendingRunnerEnrollments  = 1024
	maxActiveRunnerDPoPReplays   = 4096
	runnerCredentialKeyAlgorithm = "ed25519"
)

var (
	errOIDCTransactionNotFound = errors.New("OIDC login transaction not found or expired")
	errTooManyOIDCTransactions = errors.New("too many OIDC login transactions are pending")
	errEnrollmentNotFound      = errors.New("runner enrollment not found")
	errEnrollmentExpired       = errors.New("runner enrollment expired")
	errEnrollmentNotPending    = errors.New("runner enrollment is not pending")
	errTooManyEnrollments      = errors.New("too many runner enrollments are pending")
	errRunnerCredentialExists  = errors.New("runner already has an active key credential")
	errRunnerCredentialInvalid = errors.New("runner credential is invalid or revoked")
	errRunnerDPoPInvalid       = errors.New("runner DPoP authentication failed")
	errRunnerDPoPReplay        = errors.New("runner DPoP proof was already used")
	errTooManyRunnerDPoPProofs = errors.New("too many runner DPoP proofs are active")
)

type authStore struct {
	db  *sqlx.DB
	now func() time.Time
}

type storedWebSession struct {
	ID        string
	Issuer    string
	Subject   string
	Name      string
	Email     string
	Roles     []string
	ExpiresAt time.Time
}

type webSessionRow struct {
	ID        string    `db:"id"`
	Issuer    string    `db:"issuer"`
	Subject   string    `db:"subject"`
	Name      string    `db:"name"`
	Email     string    `db:"email"`
	RolesJSON string    `db:"roles_json"`
	ExpiresAt time.Time `db:"expires_at"`
}

type oidcLoginTransaction struct {
	Nonce        string
	PKCEVerifier string
	ReturnTo     string
}

type runnerEnrollment struct {
	ID             string
	Status         protocol.EnrollmentStatus
	UserCode       string
	DisplayName    string
	Host           protocol.Host
	Workspace      protocol.Workspace
	KodeletVersion string
	Fingerprint    string
	PublicKey      ed25519.PublicKey
	ExpiresAt      time.Time
	RunnerID       string
	CredentialID   string
	ReplaceNeeded  bool
}

type runnerEnrollmentRow struct {
	ID                 string         `db:"id"`
	Status             string         `db:"status"`
	UserCode           string         `db:"user_code"`
	DisplayName        sql.NullString `db:"display_name"`
	HostInstanceID     string         `db:"host_instance_id"`
	Hostname           sql.NullString `db:"hostname"`
	HostOS             sql.NullString `db:"host_os"`
	HostArch           sql.NullString `db:"host_arch"`
	WorkspacePath      string         `db:"workspace_path"`
	WorkspaceName      string         `db:"workspace_name"`
	KodeletVersion     sql.NullString `db:"kodelet_version"`
	PublicKey          []byte         `db:"public_key"`
	PollInterval       int64          `db:"poll_interval_seconds"`
	LastPolledAt       sql.NullTime   `db:"last_polled_at"`
	ExpiresAt          time.Time      `db:"expires_at"`
	RunnerID           sql.NullString `db:"runner_id"`
	CredentialID       sql.NullString `db:"credential_id"`
	PublicKeySHA256    []byte         `db:"public_key_sha256"`
	EnrollmentApproved sql.NullTime   `db:"approved_at"`
}

type runnerCredentialIdentity struct {
	CredentialID   string
	RunnerID       string
	HostInstanceID string
	WorkspacePath  string
}

type enrollmentSlowDownError struct {
	RetryAfter time.Duration
}

func (e *enrollmentSlowDownError) Error() string {
	return "runner enrollment is being polled too quickly"
}

func newAuthStore(ctx context.Context, dbPath string) (*authStore, error) {
	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	return &authStore{db: sqlDB, now: time.Now}, nil
}

func (s *authStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *authStore) CreateWebSession(ctx context.Context, issuer, subject, name, email string, roles []string, duration time.Duration) (string, string, error) {
	if s == nil || s.db == nil {
		return "", "", errors.New("authentication store is closed")
	}
	issuer = strings.TrimSpace(issuer)
	subject = strings.TrimSpace(subject)
	if issuer == "" || subject == "" {
		return "", "", errors.New("OIDC issuer and subject are required")
	}
	if duration <= 0 {
		duration = defaultWebSessionDuration
	}
	sessionID, err := newAuthID("session")
	if err != nil {
		return "", "", err
	}
	token, tokenHash, err := newAuthSecret()
	if err != nil {
		return "", "", err
	}
	csrfToken, csrfHash, err := newAuthSecret()
	if err != nil {
		return "", "", err
	}
	rolesJSON, err := json.Marshal(normalizeRoleNames(roles))
	if err != nil {
		return "", "", errors.Wrap(err, "failed to encode web session roles")
	}
	now := s.now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO web_auth_sessions (
			id, token_sha256, csrf_token_sha256, issuer, subject, name, email,
			roles_json, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sessionID, tokenHash, csrfHash, issuer, subject, nullAuthString(name), nullAuthString(email), string(rolesJSON), now, now.Add(duration)); err != nil {
		return "", "", errors.Wrap(err, "failed to create web authentication session")
	}
	_, _ = s.db.ExecContext(ctx, "DELETE FROM web_auth_sessions WHERE expires_at <= ?", now)
	return token, csrfToken, nil
}

func (s *authStore) LoadWebSession(ctx context.Context, token string) (storedWebSession, bool, error) {
	if s == nil || s.db == nil {
		return storedWebSession{}, false, errors.New("authentication store is closed")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return storedWebSession{}, false, nil
	}
	var row webSessionRow
	err := s.db.GetContext(ctx, &row, `
		SELECT id, issuer, subject, COALESCE(name, '') AS name, COALESCE(email, '') AS email,
			roles_json, expires_at
		FROM web_auth_sessions
		WHERE token_sha256 = ? AND expires_at > ?
	`, authHash(token), s.now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return storedWebSession{}, false, nil
	}
	if err != nil {
		return storedWebSession{}, false, errors.Wrap(err, "failed to load web authentication session")
	}
	var roles []string
	if err := json.Unmarshal([]byte(row.RolesJSON), &roles); err != nil {
		return storedWebSession{}, false, errors.Wrap(err, "failed to decode web authentication session roles")
	}
	return storedWebSession{
		ID:        row.ID,
		Issuer:    row.Issuer,
		Subject:   row.Subject,
		Name:      row.Name,
		Email:     row.Email,
		Roles:     normalizeRoleNames(roles),
		ExpiresAt: row.ExpiresAt,
	}, true, nil
}

func (s *authStore) WebSessionCSRFValid(ctx context.Context, sessionID, csrfToken string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("authentication store is closed")
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(csrfToken) == "" {
		return false, nil
	}
	var count int
	if err := s.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM web_auth_sessions
		WHERE id = ? AND csrf_token_sha256 = ? AND expires_at > ?
	`, sessionID, authHash(csrfToken), s.now().UTC()); err != nil {
		return false, errors.Wrap(err, "failed to validate web session CSRF token")
	}
	return count == 1, nil
}

func (s *authStore) DeleteWebSession(ctx context.Context, token string) error {
	if s == nil || s.db == nil || strings.TrimSpace(token) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM web_auth_sessions WHERE token_sha256 = ?", authHash(strings.TrimSpace(token)))
	return errors.Wrap(err, "failed to delete web authentication session")
}

func (s *authStore) CreateOIDCTransaction(ctx context.Context, state, nonce, verifier, returnTo string, ttl time.Duration) error {
	if s == nil || s.db == nil {
		return errors.New("authentication store is closed")
	}
	if strings.TrimSpace(state) == "" || strings.TrimSpace(nonce) == "" || strings.TrimSpace(verifier) == "" {
		return errors.New("OIDC state, nonce, and PKCE verifier are required")
	}
	if ttl <= 0 {
		ttl = defaultOIDCTransactionTTL
	}
	id, err := newAuthID("oidc")
	if err != nil {
		return err
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin OIDC login transaction")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM oidc_login_transactions WHERE expires_at <= ?", now); err != nil {
		return errors.Wrap(err, "failed to clean up expired OIDC login transactions")
	}
	var pendingCount int
	if err := tx.GetContext(ctx, &pendingCount, "SELECT COUNT(*) FROM oidc_login_transactions WHERE expires_at > ?", now); err != nil {
		return errors.Wrap(err, "failed to count pending OIDC login transactions")
	}
	if pendingCount >= maxPendingOIDCTransactions {
		return errTooManyOIDCTransactions
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO oidc_login_transactions (
			id, state_sha256, nonce, pkce_verifier, return_to, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, authHash(state), nonce, verifier, returnTo, now, now.Add(ttl)); err != nil {
		return errors.Wrap(err, "failed to create OIDC login transaction")
	}
	return errors.Wrap(tx.Commit(), "failed to commit OIDC login transaction")
}

func (s *authStore) ConsumeOIDCTransaction(ctx context.Context, state string) (oidcLoginTransaction, error) {
	if s == nil || s.db == nil {
		return oidcLoginTransaction{}, errors.New("authentication store is closed")
	}
	state = strings.TrimSpace(state)
	if state == "" {
		return oidcLoginTransaction{}, errOIDCTransactionNotFound
	}
	var row struct {
		Nonce        string `db:"nonce"`
		PKCEVerifier string `db:"pkce_verifier"`
		ReturnTo     string `db:"return_to"`
	}
	now := s.now().UTC()
	if err := s.db.GetContext(ctx, &row, `
		DELETE FROM oidc_login_transactions
		WHERE state_sha256 = ? AND expires_at > ?
		RETURNING nonce, pkce_verifier, return_to
	`, authHash(state), now); errors.Is(err, sql.ErrNoRows) {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM oidc_login_transactions WHERE state_sha256 = ? AND expires_at <= ?", authHash(state), now)
		return oidcLoginTransaction{}, errOIDCTransactionNotFound
	} else if err != nil {
		return oidcLoginTransaction{}, errors.Wrap(err, "failed to consume OIDC login transaction")
	}
	return oidcLoginTransaction{Nonce: row.Nonce, PKCEVerifier: row.PKCEVerifier, ReturnTo: row.ReturnTo}, nil
}

func (s *authStore) StartRunnerEnrollment(ctx context.Context, request protocol.EnrollmentStartRequest, verificationURL string) (protocol.EnrollmentStartResponse, error) {
	if s == nil || s.db == nil {
		return protocol.EnrollmentStartResponse{}, errors.New("authentication store is closed")
	}
	if err := request.Validate(); err != nil {
		return protocol.EnrollmentStartResponse{}, err
	}
	if !protocol.SupportsVersion(request.ProtocolVersions, protocol.Version) {
		return protocol.EnrollmentStartResponse{}, errors.Errorf("runner does not support protocol version %d", protocol.Version)
	}
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Host.InstanceID = strings.TrimSpace(request.Host.InstanceID)
	request.Host.Hostname = strings.TrimSpace(request.Host.Hostname)
	request.Host.OS = strings.TrimSpace(request.Host.OS)
	request.Host.Arch = strings.TrimSpace(request.Host.Arch)
	request.Workspace.Path = strings.TrimSpace(request.Workspace.Path)
	request.Workspace.Name = strings.TrimSpace(request.Workspace.Name)
	request.KodeletVersion = strings.TrimSpace(request.KodeletVersion)
	verificationURL = strings.TrimSpace(verificationURL)
	verification, err := url.Parse(verificationURL)
	if err != nil || !verification.IsAbs() || verification.Host == "" || verification.User != nil || (verification.Scheme != "http" && verification.Scheme != "https") || verification.Fragment != "" {
		return protocol.EnrollmentStartResponse{}, errors.New("runner enrollment verification URL must be an absolute http:// or https:// URL without user information or a fragment")
	}
	publicKey, err := protocol.DecodePublicKey(request.PublicKey)
	if err != nil {
		return protocol.EnrollmentStartResponse{}, err
	}
	fingerprintHash := sha256.Sum256(publicKey)
	now := s.now().UTC()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return protocol.EnrollmentStartResponse{}, errors.Wrap(err, "failed to begin runner enrollment")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM runner_enrollments
		WHERE status = ? OR (status = ? AND expires_at <= ?)
	`, protocol.EnrollmentStatusExpired, protocol.EnrollmentStatusPending, now); err != nil {
		return protocol.EnrollmentStartResponse{}, errors.Wrap(err, "failed to clean up expired runner enrollments")
	}
	var pendingCount int
	if err := tx.GetContext(ctx, &pendingCount, `SELECT COUNT(*) FROM runner_enrollments WHERE status = ? AND expires_at > ?`, protocol.EnrollmentStatusPending, now); err != nil {
		return protocol.EnrollmentStartResponse{}, errors.Wrap(err, "failed to count pending runner enrollments")
	}
	if pendingCount >= maxPendingRunnerEnrollments {
		return protocol.EnrollmentStartResponse{}, errTooManyEnrollments
	}

	enrollmentID, err := newAuthID("enrollment")
	if err != nil {
		return protocol.EnrollmentStartResponse{}, err
	}
	deviceCode, err := protocol.NewRunnerAccessToken()
	if err != nil {
		return protocol.EnrollmentStartResponse{}, err
	}
	deviceHash := authHash(deviceCode)
	expiresAt := now.Add(defaultRunnerEnrollmentTTL)
	for range 8 {
		userCode, codeErr := newRunnerUserCode()
		if codeErr != nil {
			return protocol.EnrollmentStartResponse{}, codeErr
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO runner_enrollments (
				id, device_code_sha256, user_code, owner_id, status, protocol_version,
				key_algorithm, public_key, public_key_sha256, display_name,
				host_instance_id, hostname, host_os, host_arch, workspace_path,
				workspace_name, kodelet_version, poll_interval_seconds, created_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, enrollmentID, deviceHash, userCode, controlPlaneOwnerID, protocol.EnrollmentStatusPending, protocol.Version,
			runnerCredentialKeyAlgorithm, []byte(publicKey), fingerprintHash[:], nullAuthString(request.DisplayName),
			request.Host.InstanceID, nullAuthString(request.Host.Hostname), nullAuthString(request.Host.OS), nullAuthString(request.Host.Arch), request.Workspace.Path,
			request.Workspace.Name, nullAuthString(request.KodeletVersion), int64(defaultRunnerPollInterval/time.Second), now, expiresAt)
		if err == nil {
			if err := tx.Commit(); err != nil {
				return protocol.EnrollmentStartResponse{}, errors.Wrap(err, "failed to commit runner enrollment")
			}
			complete := *verification
			query := complete.Query()
			query.Set("user_code", userCode)
			complete.RawQuery = query.Encode()
			return protocol.EnrollmentStartResponse{
				EnrollmentID:            enrollmentID,
				DeviceCode:              deviceCode,
				UserCode:                userCode,
				VerificationURL:         verification.String(),
				VerificationURLComplete: complete.String(),
				ExpiresAt:               expiresAt,
				PollIntervalMS:          defaultRunnerPollInterval.Milliseconds(),
			}, nil
		}
	}
	return protocol.EnrollmentStartResponse{}, errors.Wrap(err, "failed to create runner enrollment")
}

func (s *authStore) RunnerEnrollmentByUserCode(ctx context.Context, userCode string) (runnerEnrollment, error) {
	if s == nil || s.db == nil {
		return runnerEnrollment{}, errors.New("authentication store is closed")
	}
	normalized, err := normalizeRunnerUserCode(userCode)
	if err != nil {
		return runnerEnrollment{}, err
	}
	now := s.now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE runner_enrollments SET status = ?
		WHERE user_code = ? AND status = ? AND expires_at <= ?
	`, protocol.EnrollmentStatusExpired, normalized, protocol.EnrollmentStatusPending, now); err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to expire runner enrollment")
	}
	var row runnerEnrollmentRow
	if err := s.db.GetContext(ctx, &row, runnerEnrollmentSelect+" WHERE e.user_code = ?", normalized); errors.Is(err, sql.ErrNoRows) {
		return runnerEnrollment{}, errEnrollmentNotFound
	} else if err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to load runner enrollment")
	}
	var activeCount int
	if err := s.db.GetContext(ctx, &activeCount, `
		SELECT COUNT(*)
		FROM runner_credentials c
		JOIN runner_registrations r ON r.id = c.runner_id
		WHERE r.owner_id = ? AND r.host_instance_id = ? AND r.workspace_path = ?
			AND c.revoked_at IS NULL
	`, controlPlaneOwnerID, row.HostInstanceID, row.WorkspacePath); err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to inspect runner enrollment replacement state")
	}
	return runnerEnrollmentFromRow(row, activeCount > 0)
}

func (s *authStore) ApproveRunnerEnrollment(ctx context.Context, userCode, approvedBy, runnerID string, replace bool) (runnerEnrollment, error) {
	if s == nil || s.db == nil {
		return runnerEnrollment{}, errors.New("authentication store is closed")
	}
	normalized, err := normalizeRunnerUserCode(userCode)
	if err != nil {
		return runnerEnrollment{}, err
	}
	approvedBy = strings.TrimSpace(approvedBy)
	runnerID = strings.TrimSpace(runnerID)
	if approvedBy == "" {
		return runnerEnrollment{}, errors.New("runner enrollment approver is required")
	}
	if runnerID == "" {
		return runnerEnrollment{}, errors.New("runner registration id is required")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to begin runner enrollment approval")
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE runner_enrollments SET status = ?
		WHERE user_code = ? AND status = ? AND expires_at <= ?
	`, protocol.EnrollmentStatusExpired, normalized, protocol.EnrollmentStatusPending, now); err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to expire runner enrollment before approval")
	}
	var row runnerEnrollmentRow
	if err := tx.GetContext(ctx, &row, runnerEnrollmentSelect+" WHERE e.user_code = ?", normalized); errors.Is(err, sql.ErrNoRows) {
		return runnerEnrollment{}, errEnrollmentNotFound
	} else if err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to load runner enrollment for approval")
	}
	status := protocol.EnrollmentStatus(row.Status)
	if status == protocol.EnrollmentStatusExpired {
		if err := tx.Commit(); err != nil {
			return runnerEnrollment{}, errors.Wrap(err, "failed to commit expired runner enrollment")
		}
		return runnerEnrollment{}, errEnrollmentExpired
	}
	if status != protocol.EnrollmentStatusPending {
		return runnerEnrollment{}, errEnrollmentNotPending
	}
	approvedEnrollment, err := runnerEnrollmentFromRow(row, true)
	if err != nil {
		return runnerEnrollment{}, err
	}
	var runnerMatches int
	if err := tx.GetContext(ctx, &runnerMatches, `
		SELECT COUNT(*) FROM runner_registrations
		WHERE id = ? AND owner_id = ? AND host_instance_id = ? AND workspace_path = ?
	`, runnerID, controlPlaneOwnerID, row.HostInstanceID, row.WorkspacePath); err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to validate runner registration for enrollment")
	}
	if runnerMatches != 1 {
		return runnerEnrollment{}, errors.New("runner registration does not match the enrollment identity")
	}
	var activeCredentialID string
	err = tx.GetContext(ctx, &activeCredentialID, `SELECT id FROM runner_credentials WHERE runner_id = ? AND revoked_at IS NULL`, runnerID)
	switch {
	case err == nil && !replace:
		return runnerEnrollment{}, errRunnerCredentialExists
	case err == nil:
		result, err := tx.ExecContext(ctx, `UPDATE runner_credentials SET revoked_at = ?, revoke_reason = ? WHERE id = ? AND revoked_at IS NULL`, now, "replaced by approved enrollment", activeCredentialID)
		if err != nil {
			return runnerEnrollment{}, errors.Wrap(err, "failed to revoke previous runner credential")
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return runnerEnrollment{}, errors.Wrap(err, "failed to inspect previous runner credential revocation")
		}
		if affected != 1 {
			return runnerEnrollment{}, errRunnerCredentialExists
		}
	case !errors.Is(err, sql.ErrNoRows):
		return runnerEnrollment{}, errors.Wrap(err, "failed to inspect active runner credential")
	}
	credentialID, err := newAuthID("credential")
	if err != nil {
		return runnerEnrollment{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO runner_credentials (
			id, runner_id, key_algorithm, public_key, public_key_sha256,
			approved_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, credentialID, runnerID, runnerCredentialKeyAlgorithm, row.PublicKey, row.PublicKeySHA256, approvedBy, now); err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to issue runner credential")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE runner_enrollments
		SET status = ?, approved_at = ?, approved_by = ?, runner_id = ?, credential_id = ?
		WHERE id = ? AND status = ? AND expires_at > ?
	`, protocol.EnrollmentStatusApproved, now, approvedBy, runnerID, credentialID, row.ID, protocol.EnrollmentStatusPending, now)
	if err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to approve runner enrollment")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to inspect runner enrollment approval")
	}
	if affected != 1 {
		return runnerEnrollment{}, errEnrollmentNotPending
	}
	approvedEnrollment.Status = protocol.EnrollmentStatusApproved
	approvedEnrollment.RunnerID = runnerID
	approvedEnrollment.CredentialID = credentialID
	if err := tx.Commit(); err != nil {
		return runnerEnrollment{}, errors.Wrap(err, "failed to commit runner enrollment approval")
	}
	return approvedEnrollment, nil
}

func (s *authStore) DenyRunnerEnrollment(ctx context.Context, userCode, deniedBy string) error {
	if s == nil || s.db == nil {
		return errors.New("authentication store is closed")
	}
	normalized, err := normalizeRunnerUserCode(userCode)
	if err != nil {
		return err
	}
	deniedBy = strings.TrimSpace(deniedBy)
	if deniedBy == "" {
		return errors.New("runner enrollment denier is required")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin runner enrollment denial")
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE runner_enrollments SET status = ?
		WHERE user_code = ? AND status = ? AND expires_at <= ?
	`, protocol.EnrollmentStatusExpired, normalized, protocol.EnrollmentStatusPending, now); err != nil {
		return errors.Wrap(err, "failed to expire runner enrollment before denial")
	}
	var status string
	if err := tx.GetContext(ctx, &status, "SELECT status FROM runner_enrollments WHERE user_code = ?", normalized); errors.Is(err, sql.ErrNoRows) {
		return errEnrollmentNotFound
	} else if err != nil {
		return errors.Wrap(err, "failed to load runner enrollment for denial")
	}
	if protocol.EnrollmentStatus(status) == protocol.EnrollmentStatusExpired {
		if err := tx.Commit(); err != nil {
			return errors.Wrap(err, "failed to commit expired runner enrollment")
		}
		return errEnrollmentExpired
	}
	if protocol.EnrollmentStatus(status) != protocol.EnrollmentStatusPending {
		return errEnrollmentNotPending
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE runner_enrollments
		SET status = ?, denied_at = ?, denied_by = ?
		WHERE user_code = ? AND status = ? AND expires_at > ?
	`, protocol.EnrollmentStatusDenied, now, deniedBy, normalized, protocol.EnrollmentStatusPending, now)
	if err != nil {
		return errors.Wrap(err, "failed to deny runner enrollment")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to inspect runner enrollment denial")
	}
	if affected != 1 {
		return errEnrollmentNotPending
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "failed to commit runner enrollment denial")
	}
	return nil
}

func (s *authStore) PollRunnerEnrollment(ctx context.Context, request protocol.EnrollmentPollRequest) (protocol.EnrollmentPollResponse, error) {
	if s == nil || s.db == nil {
		return protocol.EnrollmentPollResponse{}, errors.New("authentication store is closed")
	}
	if err := request.Validate(); err != nil {
		return protocol.EnrollmentPollResponse{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to begin runner enrollment poll")
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE runner_enrollments SET status = ?
		WHERE id = ? AND device_code_sha256 = ? AND status = ? AND expires_at <= ?
	`, protocol.EnrollmentStatusExpired, request.EnrollmentID, authHash(request.DeviceCode), protocol.EnrollmentStatusPending, now); err != nil {
		return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to expire runner enrollment before polling")
	}
	var row runnerEnrollmentRow
	if err := tx.GetContext(ctx, &row, runnerEnrollmentSelect+" WHERE e.id = ? AND e.device_code_sha256 = ?", request.EnrollmentID, authHash(request.DeviceCode)); errors.Is(err, sql.ErrNoRows) {
		return protocol.EnrollmentPollResponse{}, errEnrollmentNotFound
	} else if err != nil {
		return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to poll runner enrollment")
	}
	status := protocol.EnrollmentStatus(row.Status)
	if status == protocol.EnrollmentStatusPending {
		interval := time.Duration(row.PollInterval) * time.Second
		if row.LastPolledAt.Valid && now.Sub(row.LastPolledAt.Time) < interval {
			return protocol.EnrollmentPollResponse{}, &enrollmentSlowDownError{RetryAfter: interval - now.Sub(row.LastPolledAt.Time)}
		}
		result, err := tx.ExecContext(ctx, "UPDATE runner_enrollments SET last_polled_at = ? WHERE id = ? AND status = ?", now, row.ID, protocol.EnrollmentStatusPending)
		if err != nil {
			return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to record runner enrollment poll")
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to inspect runner enrollment poll")
		}
		if affected != 1 {
			return protocol.EnrollmentPollResponse{}, errEnrollmentNotPending
		}
	}
	response := protocol.EnrollmentPollResponse{Status: status}
	if status == protocol.EnrollmentStatusPending {
		response.RetryAfterMS = time.Duration(row.PollInterval * int64(time.Second)).Milliseconds()
	}
	if status == protocol.EnrollmentStatusApproved {
		if err := protocol.ValidateRunnerAccessToken(request.DeviceCode); err != nil {
			return protocol.EnrollmentPollResponse{}, errEnrollmentNotFound
		}
		response.CredentialID = row.CredentialID.String
		response.AccessToken = request.DeviceCode
		response.TokenType = protocol.DPoPAuthorizationScheme
		response.RunnerID = row.RunnerID.String
		response.Fingerprint, err = protocol.CredentialFingerprint(ed25519.PublicKey(row.PublicKey))
		if err != nil {
			return protocol.EnrollmentPollResponse{}, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runner_enrollments SET delivered_at = COALESCE(delivered_at, ?) WHERE id = ?", now, row.ID); err != nil {
			return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to record runner credential delivery")
		}
	}
	if status == protocol.EnrollmentStatusExpired {
		if _, err := tx.ExecContext(ctx, "DELETE FROM runner_enrollments WHERE id = ? AND status = ?", row.ID, protocol.EnrollmentStatusExpired); err != nil {
			return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to delete expired runner enrollment")
		}
	}
	if err := tx.Commit(); err != nil {
		return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to commit runner enrollment poll")
	}
	return response, nil
}

func (s *authStore) VerifyRunnerDPoP(ctx context.Context, accessToken, proof, method, targetURL string) (runnerCredentialIdentity, error) {
	if s == nil || s.db == nil {
		return runnerCredentialIdentity{}, errors.New("authentication store is closed")
	}
	if err := protocol.ValidateRunnerAccessToken(accessToken); err != nil {
		return runnerCredentialIdentity{}, errRunnerCredentialInvalid
	}
	var row struct {
		PublicKey      []byte `db:"public_key"`
		CredentialID   string `db:"credential_id"`
		RunnerID       string `db:"runner_id"`
		HostInstanceID string `db:"host_instance_id"`
		WorkspacePath  string `db:"workspace_path"`
	}
	if err := s.db.GetContext(ctx, &row, `
		SELECT c.public_key, c.id AS credential_id, c.runner_id,
			r.host_instance_id, r.workspace_path
		FROM runner_enrollments e
		JOIN runner_credentials c ON c.id = e.credential_id
		JOIN runner_registrations r ON r.id = c.runner_id
		WHERE e.device_code_sha256 = ? AND e.status = ? AND e.delivered_at IS NOT NULL
			AND c.revoked_at IS NULL
	`, authHash(accessToken), protocol.EnrollmentStatusApproved); errors.Is(err, sql.ErrNoRows) {
		return runnerCredentialIdentity{}, errRunnerCredentialInvalid
	} else if err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to load DPoP-bound runner credential")
	}
	now := s.now().UTC()
	verified, err := protocol.VerifyDPoPProof(proof, protocol.DPoPVerificationOptions{
		Method:      method,
		TargetURL:   targetURL,
		AccessToken: accessToken,
		PublicKey:   ed25519.PublicKey(row.PublicKey),
		Now:         now,
		MaxAge:      defaultRunnerDPoPProofMaxAge,
		FutureSkew:  defaultRunnerDPoPFutureSkew,
	})
	if err != nil {
		return runnerCredentialIdentity{}, errRunnerDPoPInvalid
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to begin runner DPoP verification")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM runner_dpop_replays WHERE expires_at <= ?", now); err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to clean up runner DPoP replay state")
	}
	var activeProofs int
	if err := tx.GetContext(ctx, &activeProofs, `
		SELECT COUNT(*) FROM runner_dpop_replays
		WHERE credential_id = ? AND expires_at > ?
	`, row.CredentialID, now); err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to count active runner DPoP proofs")
	}
	if activeProofs >= maxActiveRunnerDPoPReplays {
		return runnerCredentialIdentity{}, errTooManyRunnerDPoPProofs
	}
	expiresAt := verified.IssuedAt.Add(defaultRunnerDPoPProofMaxAge + defaultRunnerDPoPFutureSkew)
	result, err := tx.ExecContext(ctx, `
		INSERT INTO runner_dpop_replays (credential_id, jti_sha256, created_at, expires_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(credential_id, jti_sha256) DO NOTHING
	`, row.CredentialID, authHash(verified.JTI), now, expiresAt)
	if err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to record runner DPoP proof")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to inspect runner DPoP replay insertion")
	}
	if affected != 1 {
		return runnerCredentialIdentity{}, errRunnerDPoPReplay
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE runner_credentials
		SET last_used_at = ?
		WHERE id = ? AND revoked_at IS NULL
			AND EXISTS (
				SELECT 1 FROM runner_enrollments
				WHERE credential_id = ? AND device_code_sha256 = ?
					AND status = ? AND delivered_at IS NOT NULL
			)
	`, now, row.CredentialID, row.CredentialID, authHash(accessToken), protocol.EnrollmentStatusApproved)
	if err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to update runner credential usage")
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to inspect runner credential usage update")
	}
	if affected != 1 {
		return runnerCredentialIdentity{}, errRunnerCredentialInvalid
	}
	if err := tx.Commit(); err != nil {
		return runnerCredentialIdentity{}, errors.Wrap(err, "failed to commit runner DPoP verification")
	}
	return runnerCredentialIdentity{
		CredentialID:   row.CredentialID,
		RunnerID:       row.RunnerID,
		HostInstanceID: row.HostInstanceID,
		WorkspacePath:  row.WorkspacePath,
	}, nil
}

func (s *authStore) HasActiveRunnerCredential(ctx context.Context, hostInstanceID, workspacePath string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("authentication store is closed")
	}
	var count int
	if err := s.db.GetContext(ctx, &count, `
		SELECT COUNT(*)
		FROM runner_credentials c
		JOIN runner_registrations r ON r.id = c.runner_id
		WHERE r.owner_id = ? AND r.host_instance_id = ? AND r.workspace_path = ?
			AND c.revoked_at IS NULL
	`, controlPlaneOwnerID, strings.TrimSpace(hostInstanceID), strings.TrimSpace(workspacePath)); err != nil {
		return false, errors.Wrap(err, "failed to inspect runner credential binding")
	}
	return count > 0, nil
}

func (s *authStore) RunnerCredentialActive(ctx context.Context, credentialID, runnerID, hostInstanceID, workspacePath string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("authentication store is closed")
	}
	var count int
	if err := s.db.GetContext(ctx, &count, `
		SELECT COUNT(*)
		FROM runner_credentials c
		JOIN runner_registrations r ON r.id = c.runner_id
		WHERE c.id = ? AND c.runner_id = ? AND c.revoked_at IS NULL
			AND r.owner_id = ? AND r.host_instance_id = ? AND r.workspace_path = ?
	`, strings.TrimSpace(credentialID), strings.TrimSpace(runnerID), controlPlaneOwnerID, strings.TrimSpace(hostInstanceID), strings.TrimSpace(workspacePath)); err != nil {
		return false, errors.Wrap(err, "failed to validate active runner credential")
	}
	return count == 1, nil
}

func runnerEnrollmentFromRow(row runnerEnrollmentRow, replaceNeeded bool) (runnerEnrollment, error) {
	publicKey := ed25519.PublicKey(row.PublicKey)
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	if err != nil {
		return runnerEnrollment{}, err
	}
	return runnerEnrollment{
		ID:             row.ID,
		Status:         protocol.EnrollmentStatus(row.Status),
		UserCode:       row.UserCode,
		DisplayName:    row.DisplayName.String,
		Host:           protocol.Host{InstanceID: row.HostInstanceID, Hostname: row.Hostname.String, OS: row.HostOS.String, Arch: row.HostArch.String},
		Workspace:      protocol.Workspace{Path: row.WorkspacePath, Name: row.WorkspaceName},
		KodeletVersion: row.KodeletVersion.String,
		Fingerprint:    fingerprint,
		PublicKey:      publicKey,
		ExpiresAt:      row.ExpiresAt,
		RunnerID:       row.RunnerID.String,
		CredentialID:   row.CredentialID.String,
		ReplaceNeeded:  replaceNeeded,
	}, nil
}

const runnerEnrollmentSelect = `
	SELECT e.id, e.status, e.user_code, e.display_name, e.host_instance_id,
		e.hostname, e.host_os, e.host_arch, e.workspace_path, e.workspace_name,
		e.kodelet_version, e.public_key, e.public_key_sha256,
		e.poll_interval_seconds, e.last_polled_at, e.expires_at,
		e.runner_id, e.credential_id, e.approved_at
	FROM runner_enrollments e`

func normalizeRoleNames(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	return result
}

func normalizeRunnerUserCode(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "", " ", "").Replace(value)
	if len(value) != 8 {
		return "", errors.New("runner enrollment code must contain eight characters")
	}
	for _, character := range value {
		if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", character) {
			return "", errors.New("runner enrollment code contains an invalid character")
		}
	}
	return value[:4] + "-" + value[4:], nil
}

func newRunnerUserCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", errors.Wrap(err, "failed to generate runner enrollment code")
	}
	for index := range random {
		random[index] = alphabet[int(random[index])%len(alphabet)]
	}
	return string(random[:4]) + "-" + string(random[4:]), nil
}

func newAuthID(prefix string) (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", errors.Wrap(err, "failed to generate authentication id")
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value), nil
}

func newAuthSecret() (string, []byte, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", nil, errors.Wrap(err, "failed to generate authentication secret")
	}
	encoded := base64.RawURLEncoding.EncodeToString(value)
	return encoded, authHash(encoded), nil
}

func authHash(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

func nullAuthString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
