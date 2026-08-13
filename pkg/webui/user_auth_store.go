package webui

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/pkg/errors"
)

const (
	defaultUserLoginTTL     = 10 * time.Minute
	defaultUserPollInterval = 5 * time.Second
	maxPendingUserLogins    = 1024
)

var (
	errUserAuthorizationNotFound   = errors.New("user login authorization not found")
	errUserAuthorizationExpired    = errors.New("user login authorization expired")
	errUserAuthorizationNotPending = errors.New("user login authorization is not pending")
	errTooManyUserLogins           = errors.New("too many user logins are pending")
	errUserCredentialInvalid       = errors.New("user credential is invalid, expired, or revoked")
)

type userLoginSlowDownError struct {
	RetryAfter time.Duration
}

func (e *userLoginSlowDownError) Error() string {
	return "user login authorization is being polled too quickly"
}

type userLoginAuthorization struct {
	ID                  string
	Status              userauth.DeviceStatus
	UserCode            string
	ClientName          string
	ClientOS            string
	ClientArch          string
	KodeletVersion      string
	ExpiresAt           time.Time
	CredentialID        string
	Principal           userauth.PrincipalSnapshot
	CredentialExpiresAt time.Time
}

type userCredentialIdentity struct {
	CredentialID string
	Principal    userauth.PrincipalSnapshot
}

type userLoginAuthorizationRow struct {
	ID                  string       `db:"id"`
	Status              string       `db:"status"`
	UserCode            string       `db:"user_code"`
	TokenSHA256         []byte       `db:"token_sha256"`
	ClientName          string       `db:"client_name"`
	ClientOS            string       `db:"client_os"`
	ClientArch          string       `db:"client_arch"`
	KodeletVersion      string       `db:"kodelet_version"`
	PollIntervalSeconds int64        `db:"poll_interval_seconds"`
	LastPolledAt        sql.NullTime `db:"last_polled_at"`
	ExpiresAt           time.Time    `db:"authorization_expires_at"`
	CredentialID        string       `db:"credential_id"`
	CredentialIssuer    string       `db:"credential_issuer"`
	CredentialSubject   string       `db:"credential_subject"`
	CredentialName      string       `db:"credential_name"`
	CredentialEmail     string       `db:"credential_email"`
	CredentialRolesJSON string       `db:"credential_roles_json"`
	CredentialExpiresAt sql.NullTime `db:"credential_expires_at"`
}

type userCredentialRow struct {
	CredentialID string `db:"credential_id"`
	Issuer       string `db:"issuer"`
	Subject      string `db:"subject"`
	Name         string `db:"name"`
	Email        string `db:"email"`
	RolesJSON    string `db:"roles_json"`
}

func (s *authStore) StartUserLogin(ctx context.Context, request userauth.DeviceStartRequest, verificationURL string) (userauth.DeviceStartResponse, error) {
	if s == nil || s.db == nil {
		return userauth.DeviceStartResponse{}, errors.New("authentication store is closed")
	}
	if err := request.Validate(); err != nil {
		return userauth.DeviceStartResponse{}, err
	}
	verification, err := parseUserLoginVerificationURL(verificationURL)
	if err != nil {
		return userauth.DeviceStartResponse{}, err
	}

	now := s.now().UTC()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return userauth.DeviceStartResponse{}, errors.Wrap(err, "failed to begin user login authorization")
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM user_login_authorizations
		WHERE status = ? OR (status = ? AND expires_at <= ?)
	`, userauth.DeviceStatusExpired, userauth.DeviceStatusPending, now); err != nil {
		return userauth.DeviceStartResponse{}, errors.Wrap(err, "failed to clean up expired user login authorizations")
	}
	var pendingCount int
	if err := tx.GetContext(ctx, &pendingCount, `
		SELECT COUNT(*) FROM user_login_authorizations
		WHERE status = ? AND expires_at > ?
	`, userauth.DeviceStatusPending, now); err != nil {
		return userauth.DeviceStartResponse{}, errors.Wrap(err, "failed to count pending user login authorizations")
	}
	if pendingCount >= maxPendingUserLogins {
		return userauth.DeviceStartResponse{}, errTooManyUserLogins
	}

	authorizationID, err := newAuthID("authorization")
	if err != nil {
		return userauth.DeviceStartResponse{}, err
	}
	deviceCode, deviceCodeHash, err := newAuthSecret()
	if err != nil {
		return userauth.DeviceStartResponse{}, err
	}
	bearerToken, err := userauth.GenerateBearerToken()
	if err != nil {
		return userauth.DeviceStartResponse{}, err
	}
	tokenHash := authHash(bearerToken)
	expiresAt := now.Add(defaultUserLoginTTL)

	var insertErr error
	for range 8 {
		userCode, codeErr := newUserCode()
		if codeErr != nil {
			return userauth.DeviceStartResponse{}, codeErr
		}
		_, insertErr = tx.ExecContext(ctx, `
			INSERT INTO user_login_authorizations (
				id, device_code_sha256, user_code, token_sha256, status,
				client_name, client_os, client_arch, kodelet_version,
				poll_interval_seconds, created_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, authorizationID, deviceCodeHash, userCode, tokenHash, userauth.DeviceStatusPending,
			request.ClientName, request.ClientOS, request.ClientArch, request.KodeletVersion,
			int64(defaultUserPollInterval/time.Second), now, expiresAt)
		if insertErr != nil {
			continue
		}
		if err := tx.Commit(); err != nil {
			return userauth.DeviceStartResponse{}, errors.Wrap(err, "failed to commit user login authorization")
		}
		complete := *verification
		query := complete.Query()
		query.Set("user_code", userCode)
		complete.RawQuery = query.Encode()
		return userauth.DeviceStartResponse{
			AuthorizationID:         authorizationID,
			DeviceCode:              deviceCode,
			UserCode:                userCode,
			VerificationURL:         verification.String(),
			VerificationURLComplete: complete.String(),
			BearerToken:             bearerToken,
			ExpiresAt:               expiresAt,
			PollIntervalMS:          defaultUserPollInterval.Milliseconds(),
		}, nil
	}
	return userauth.DeviceStartResponse{}, errors.Wrap(insertErr, "failed to create user login authorization")
}

func (s *authStore) UserLoginByUserCode(ctx context.Context, userCode string) (userLoginAuthorization, error) {
	if s == nil || s.db == nil {
		return userLoginAuthorization{}, errors.New("authentication store is closed")
	}
	normalized, err := normalizeUserCode(userCode)
	if err != nil {
		return userLoginAuthorization{}, err
	}
	now := s.now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE user_login_authorizations SET status = ?
		WHERE user_code = ? AND status = ? AND expires_at <= ?
	`, userauth.DeviceStatusExpired, normalized, userauth.DeviceStatusPending, now); err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to expire user login authorization")
	}
	var row userLoginAuthorizationRow
	if err := s.db.GetContext(ctx, &row, userLoginAuthorizationSelect+" WHERE a.user_code = ?", normalized); errors.Is(err, sql.ErrNoRows) {
		return userLoginAuthorization{}, errUserAuthorizationNotFound
	} else if err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to load user login authorization")
	}
	return userLoginAuthorizationFromRow(row)
}

func (s *authStore) ApproveUserLogin(ctx context.Context, userCode string, principal Principal, duration time.Duration) (userLoginAuthorization, error) {
	if s == nil || s.db == nil {
		return userLoginAuthorization{}, errors.New("authentication store is closed")
	}
	normalized, err := normalizeUserCode(userCode)
	if err != nil {
		return userLoginAuthorization{}, err
	}
	principalSnapshot, rolesJSON, err := userPrincipalSnapshot(principal)
	if err != nil {
		return userLoginAuthorization{}, err
	}
	if duration <= 0 {
		duration = defaultWebSessionDuration
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to begin user login approval")
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_login_authorizations SET status = ?
		WHERE user_code = ? AND status = ? AND expires_at <= ?
	`, userauth.DeviceStatusExpired, normalized, userauth.DeviceStatusPending, now); err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to expire user login authorization before approval")
	}
	var row userLoginAuthorizationRow
	if err := tx.GetContext(ctx, &row, userLoginAuthorizationSelect+" WHERE a.user_code = ?", normalized); errors.Is(err, sql.ErrNoRows) {
		return userLoginAuthorization{}, errUserAuthorizationNotFound
	} else if err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to load user login authorization for approval")
	}
	status := userauth.DeviceStatus(row.Status)
	if status == userauth.DeviceStatusExpired {
		if err := tx.Commit(); err != nil {
			return userLoginAuthorization{}, errors.Wrap(err, "failed to commit expired user login authorization")
		}
		return userLoginAuthorization{}, errUserAuthorizationExpired
	}
	if status != userauth.DeviceStatusPending {
		return userLoginAuthorization{}, errUserAuthorizationNotPending
	}

	credentialID, err := newAuthID("credential")
	if err != nil {
		return userLoginAuthorization{}, err
	}
	credentialExpiresAt := now.Add(duration)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_api_credentials (
			id, token_sha256, issuer, subject, name, email, roles_json,
			created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, credentialID, row.TokenSHA256, principalSnapshot.Issuer, principalSnapshot.Subject,
		nullAuthString(principalSnapshot.Name), nullAuthString(principalSnapshot.Email), string(rolesJSON),
		now, credentialExpiresAt); err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to issue user API credential")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE user_login_authorizations
		SET status = ?, approved_at = ?, approved_by = ?, credential_id = ?
		WHERE id = ? AND status = ? AND expires_at > ?
	`, userauth.DeviceStatusApproved, now, principalSnapshot.ID, credentialID,
		row.ID, userauth.DeviceStatusPending, now)
	if err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to approve user login authorization")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to inspect user login approval")
	}
	if affected != 1 {
		return userLoginAuthorization{}, errUserAuthorizationNotPending
	}

	row.Status = string(userauth.DeviceStatusApproved)
	row.CredentialID = credentialID
	row.CredentialIssuer = principalSnapshot.Issuer
	row.CredentialSubject = principalSnapshot.Subject
	row.CredentialName = principalSnapshot.Name
	row.CredentialEmail = principalSnapshot.Email
	row.CredentialRolesJSON = string(rolesJSON)
	row.CredentialExpiresAt = sql.NullTime{Time: credentialExpiresAt, Valid: true}
	approved, err := userLoginAuthorizationFromRow(row)
	if err != nil {
		return userLoginAuthorization{}, err
	}
	if err := tx.Commit(); err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "failed to commit user login approval")
	}
	return approved, nil
}

func (s *authStore) DenyUserLogin(ctx context.Context, userCode, deniedBy string) error {
	if s == nil || s.db == nil {
		return errors.New("authentication store is closed")
	}
	normalized, err := normalizeUserCode(userCode)
	if err != nil {
		return err
	}
	deniedBy = strings.TrimSpace(deniedBy)
	if deniedBy == "" {
		return errors.New("user login denier is required")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin user login denial")
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_login_authorizations SET status = ?
		WHERE user_code = ? AND status = ? AND expires_at <= ?
	`, userauth.DeviceStatusExpired, normalized, userauth.DeviceStatusPending, now); err != nil {
		return errors.Wrap(err, "failed to expire user login authorization before denial")
	}
	var status string
	if err := tx.GetContext(ctx, &status, "SELECT status FROM user_login_authorizations WHERE user_code = ?", normalized); errors.Is(err, sql.ErrNoRows) {
		return errUserAuthorizationNotFound
	} else if err != nil {
		return errors.Wrap(err, "failed to load user login authorization for denial")
	}
	if userauth.DeviceStatus(status) == userauth.DeviceStatusExpired {
		if err := tx.Commit(); err != nil {
			return errors.Wrap(err, "failed to commit expired user login authorization")
		}
		return errUserAuthorizationExpired
	}
	if userauth.DeviceStatus(status) != userauth.DeviceStatusPending {
		return errUserAuthorizationNotPending
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE user_login_authorizations
		SET status = ?, denied_at = ?, denied_by = ?
		WHERE user_code = ? AND status = ? AND expires_at > ?
	`, userauth.DeviceStatusDenied, now, deniedBy, normalized, userauth.DeviceStatusPending, now)
	if err != nil {
		return errors.Wrap(err, "failed to deny user login authorization")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to inspect user login denial")
	}
	if affected != 1 {
		return errUserAuthorizationNotPending
	}
	return errors.Wrap(tx.Commit(), "failed to commit user login denial")
}

func (s *authStore) PollUserLogin(ctx context.Context, request userauth.DevicePollRequest) (userauth.DevicePollResponse, error) {
	if s == nil || s.db == nil {
		return userauth.DevicePollResponse{}, errors.New("authentication store is closed")
	}
	if err := request.Validate(); err != nil {
		return userauth.DevicePollResponse{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to begin user login poll")
	}
	defer tx.Rollback()
	now := s.now().UTC()
	deviceCodeHash := authHash(request.DeviceCode)
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_login_authorizations SET status = ?
		WHERE id = ? AND device_code_sha256 = ? AND status = ? AND expires_at <= ?
	`, userauth.DeviceStatusExpired, request.AuthorizationID, deviceCodeHash, userauth.DeviceStatusPending, now); err != nil {
		return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to expire user login authorization before polling")
	}
	var row userLoginAuthorizationRow
	if err := tx.GetContext(ctx, &row, userLoginAuthorizationSelect+" WHERE a.id = ? AND a.device_code_sha256 = ?", request.AuthorizationID, deviceCodeHash); errors.Is(err, sql.ErrNoRows) {
		return userauth.DevicePollResponse{}, errUserAuthorizationNotFound
	} else if err != nil {
		return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to poll user login authorization")
	}
	status := userauth.DeviceStatus(row.Status)
	if status == userauth.DeviceStatusPending {
		interval := time.Duration(row.PollIntervalSeconds) * time.Second
		if interval <= 0 {
			return userauth.DevicePollResponse{}, errors.New("stored user login poll interval is invalid")
		}
		if row.LastPolledAt.Valid {
			elapsed := now.Sub(row.LastPolledAt.Time.UTC())
			if elapsed < interval {
				return userauth.DevicePollResponse{}, &userLoginSlowDownError{RetryAfter: interval - elapsed}
			}
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE user_login_authorizations SET last_polled_at = ?
			WHERE id = ? AND status = ? AND expires_at > ?
		`, now, row.ID, userauth.DeviceStatusPending, now)
		if err != nil {
			return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to record user login poll")
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to inspect user login poll")
		}
		if affected != 1 {
			return userauth.DevicePollResponse{}, errUserAuthorizationNotPending
		}
	}

	response := userauth.DevicePollResponse{Status: status}
	switch status {
	case userauth.DeviceStatusPending:
		response.RetryAfterMS = (time.Duration(row.PollIntervalSeconds) * time.Second).Milliseconds()
	case userauth.DeviceStatusApproved:
		view, err := userLoginAuthorizationFromRow(row)
		if err != nil {
			return userauth.DevicePollResponse{}, err
		}
		response.CredentialID = view.CredentialID
		response.Principal = view.Principal
		response.ExpiresAt = view.CredentialExpiresAt
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_login_authorizations
			SET delivered_at = COALESCE(delivered_at, ?)
			WHERE id = ?
		`, now, row.ID); err != nil {
			return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to record user credential delivery")
		}
	case userauth.DeviceStatusExpired:
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM user_login_authorizations
			WHERE id = ? AND status = ?
		`, row.ID, userauth.DeviceStatusExpired); err != nil {
			return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to delete expired user login authorization")
		}
	case userauth.DeviceStatusDenied:
	default:
		return userauth.DevicePollResponse{}, errors.Errorf("stored user login authorization has unknown status %q", status)
	}
	if err := tx.Commit(); err != nil {
		return userauth.DevicePollResponse{}, errors.Wrap(err, "failed to commit user login poll")
	}
	return response, nil
}

func (s *authStore) LoadUserCredential(ctx context.Context, bearerToken string) (userCredentialIdentity, error) {
	if s == nil || s.db == nil {
		return userCredentialIdentity{}, errors.New("authentication store is closed")
	}
	if err := userauth.ValidateBearerToken(bearerToken); err != nil {
		return userCredentialIdentity{}, errUserCredentialInvalid
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return userCredentialIdentity{}, errors.Wrap(err, "failed to begin user credential load")
	}
	defer tx.Rollback()
	now := s.now().UTC()
	var row userCredentialRow
	if err := tx.GetContext(ctx, &row, `
		UPDATE user_api_credentials
		SET last_used_at = ?
		WHERE token_sha256 = ? AND revoked_at IS NULL AND expires_at > ?
		RETURNING id AS credential_id, issuer, subject,
			COALESCE(name, '') AS name, COALESCE(email, '') AS email, roles_json
	`, now, authHash(bearerToken), now); errors.Is(err, sql.ErrNoRows) {
		return userCredentialIdentity{}, errUserCredentialInvalid
	} else if err != nil {
		return userCredentialIdentity{}, errors.Wrap(err, "failed to load user credential")
	}
	principal, err := storedUserPrincipalSnapshot(row.Issuer, row.Subject, row.Name, row.Email, row.RolesJSON)
	if err != nil {
		return userCredentialIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return userCredentialIdentity{}, errors.Wrap(err, "failed to commit user credential usage")
	}
	return userCredentialIdentity{CredentialID: row.CredentialID, Principal: principal}, nil
}

func (s *authStore) RevokeUserCredential(ctx context.Context, credentialID, reason string) error {
	if s == nil || s.db == nil {
		return errors.New("authentication store is closed")
	}
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		return errUserCredentialInvalid
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE user_api_credentials
		SET revoked_at = ?, revoke_reason = ?
		WHERE id = ? AND revoked_at IS NULL AND expires_at > ?
	`, now, nullAuthString(reason), credentialID, now)
	if err != nil {
		return errors.Wrap(err, "failed to revoke user credential")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to inspect user credential revocation")
	}
	if affected != 1 {
		return errUserCredentialInvalid
	}
	return nil
}

func userLoginAuthorizationFromRow(row userLoginAuthorizationRow) (userLoginAuthorization, error) {
	status := userauth.DeviceStatus(row.Status)
	if err := status.Validate(); err != nil {
		return userLoginAuthorization{}, errors.Wrap(err, "stored user login authorization is invalid")
	}
	view := userLoginAuthorization{
		ID:             row.ID,
		Status:         status,
		UserCode:       row.UserCode,
		ClientName:     row.ClientName,
		ClientOS:       row.ClientOS,
		ClientArch:     row.ClientArch,
		KodeletVersion: row.KodeletVersion,
		ExpiresAt:      row.ExpiresAt.UTC(),
		CredentialID:   row.CredentialID,
	}
	if row.CredentialID == "" {
		if status == userauth.DeviceStatusApproved {
			return userLoginAuthorization{}, errors.New("approved user login authorization has no credential")
		}
		return view, nil
	}
	if !row.CredentialExpiresAt.Valid {
		return userLoginAuthorization{}, errors.New("user login authorization credential has no expiry")
	}
	principal, err := storedUserPrincipalSnapshot(
		row.CredentialIssuer,
		row.CredentialSubject,
		row.CredentialName,
		row.CredentialEmail,
		row.CredentialRolesJSON,
	)
	if err != nil {
		return userLoginAuthorization{}, err
	}
	view.Principal = principal
	view.CredentialExpiresAt = row.CredentialExpiresAt.Time.UTC()
	return view, nil
}

func userPrincipalSnapshot(principal Principal) (userauth.PrincipalSnapshot, []byte, error) {
	issuer := strings.TrimSpace(principal.Issuer)
	subject := strings.TrimSpace(principal.Subject)
	if issuer == "" || subject == "" {
		return userauth.PrincipalSnapshot{}, nil, errors.New("user login approval requires a stable OIDC issuer and subject")
	}
	principalID := issuer + "|" + subject
	if strings.TrimSpace(principal.ID) != principalID {
		return userauth.PrincipalSnapshot{}, nil, errors.New("user login approval principal does not match its OIDC issuer and subject")
	}
	snapshot := userauth.PrincipalSnapshot{
		ID:      principalID,
		Issuer:  issuer,
		Subject: subject,
		Name:    strings.TrimSpace(principal.Name),
		Email:   strings.TrimSpace(principal.Email),
		Roles:   normalizeRoleNames(principal.Roles),
	}
	if err := snapshot.Validate(); err != nil {
		return userauth.PrincipalSnapshot{}, nil, errors.Wrap(err, "user login approval principal is invalid")
	}
	rolesJSON, err := json.Marshal(snapshot.Roles)
	if err != nil {
		return userauth.PrincipalSnapshot{}, nil, errors.Wrap(err, "failed to encode user credential roles")
	}
	return snapshot, rolesJSON, nil
}

func storedUserPrincipalSnapshot(issuer, subject, name, email, rolesJSON string) (userauth.PrincipalSnapshot, error) {
	var roles []string
	if err := json.Unmarshal([]byte(rolesJSON), &roles); err != nil {
		return userauth.PrincipalSnapshot{}, errors.Wrap(err, "failed to decode user credential roles")
	}
	snapshot := userauth.PrincipalSnapshot{
		ID:      issuer + "|" + subject,
		Issuer:  issuer,
		Subject: subject,
		Name:    name,
		Email:   email,
		Roles:   normalizeRoleNames(roles),
	}
	if err := snapshot.Validate(); err != nil {
		return userauth.PrincipalSnapshot{}, errors.Wrap(err, "stored user credential principal is invalid")
	}
	return snapshot, nil
}

func parseUserLoginVerificationURL(value string) (*url.URL, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return nil, errors.New("user login verification URL must be an absolute http:// or https:// URL without user information or a fragment")
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("user login verification URL must be an absolute http:// or https:// URL without user information or a fragment")
	}
	return parsed, nil
}

func normalizeUserCode(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	var normalized strings.Builder
	normalized.Grow(8)
	for _, character := range value {
		if character == '-' || unicode.IsSpace(character) {
			continue
		}
		if !strings.ContainsRune(userCodeAlphabet, character) {
			return "", errors.New("user login code contains an invalid character")
		}
		normalized.WriteRune(character)
	}
	code := normalized.String()
	if len(code) != 8 {
		return "", errors.New("user login code must contain eight characters")
	}
	return code[:4] + "-" + code[4:], nil
}

func newUserCode() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", errors.Wrap(err, "failed to generate user login code")
	}
	for index := range random {
		random[index] = userCodeAlphabet[int(random[index])%len(userCodeAlphabet)]
	}
	return string(random[:4]) + "-" + string(random[4:]), nil
}

const userCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const userLoginAuthorizationSelect = `
	SELECT a.id, a.status, a.user_code, a.token_sha256,
		a.client_name, a.client_os, a.client_arch, a.kodelet_version,
		a.poll_interval_seconds, a.last_polled_at,
		a.expires_at AS authorization_expires_at,
		COALESCE(a.credential_id, '') AS credential_id,
		COALESCE(c.issuer, '') AS credential_issuer,
		COALESCE(c.subject, '') AS credential_subject,
		COALESCE(c.name, '') AS credential_name,
		COALESCE(c.email, '') AS credential_email,
		COALESCE(c.roles_json, '[]') AS credential_roles_json,
		c.expires_at AS credential_expires_at
	FROM user_login_authorizations a
	LEFT JOIN user_api_credentials c ON c.id = a.credential_id`
