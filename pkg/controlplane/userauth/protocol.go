// Package userauth implements Kodelet-issued credentials for non-browser users.
package userauth

import (
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/pkg/errors"
)

const (
	DeviceStartPath         = "/api/auth/v1/device/start"
	DevicePollPath          = "/api/auth/v1/device/poll"
	DeviceVerificationPath  = "/auth/device"
	CurrentCredentialPath   = "/api/auth/v1/credentials/current"
	MePath                  = "/api/auth/me"
	BearerTokenPrefix       = "kltu_"
	bearerTokenPayloadBytes = 32
)

var (
	// ErrLoginDenied indicates that the user denied the pending device login.
	ErrLoginDenied = errors.New("user login was denied")
	// ErrLoginExpired indicates that the pending device login expired before approval.
	ErrLoginExpired = errors.New("user login expired")
)

// DeviceStatus is the current state of a device authorization.
type DeviceStatus string

const (
	DeviceStatusPending  DeviceStatus = "pending"
	DeviceStatusApproved DeviceStatus = "approved"
	DeviceStatusDenied   DeviceStatus = "denied"
	DeviceStatusExpired  DeviceStatus = "expired"
)

// Status is an alias for DeviceStatus.
type Status = DeviceStatus

const (
	StatusPending  = DeviceStatusPending
	StatusApproved = DeviceStatusApproved
	StatusDenied   = DeviceStatusDenied
	StatusExpired  = DeviceStatusExpired
)

// DeviceStartRequest describes the Kodelet client requesting user authorization.
type DeviceStartRequest struct {
	ClientName     string `json:"clientName"`
	ClientOS       string `json:"clientOS"`
	ClientArch     string `json:"clientArch"`
	KodeletVersion string `json:"kodeletVersion"`
}

// Validate checks that all device-start metadata is present and canonical.
func (r DeviceStartRequest) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "clientName", value: r.ClientName},
		{name: "clientOS", value: r.ClientOS},
		{name: "clientArch", value: r.ClientArch},
		{name: "kodeletVersion", value: r.KodeletVersion},
	} {
		if err := validateText(field.name, field.value, true); err != nil {
			return err
		}
	}
	return nil
}

// DeviceStartResponse returns the private polling values and bearer issued at flow start.
type DeviceStartResponse struct {
	AuthorizationID         string    `json:"authorizationId"`
	DeviceCode              string    `json:"deviceCode"`
	UserCode                string    `json:"userCode"`
	VerificationURL         string    `json:"verificationUrl"`
	VerificationURLComplete string    `json:"verificationUrlComplete,omitempty"`
	BearerToken             string    `json:"bearerToken"`
	ExpiresAt               time.Time `json:"expiresAt"`
	PollIntervalMS          int64     `json:"pollIntervalMs"`
}

// Validate checks a device-start response against the current time.
func (r DeviceStartResponse) Validate() error {
	return r.ValidateAt(time.Now().UTC())
}

// ValidateAt checks a device-start response against an explicit current time.
func (r DeviceStartResponse) ValidateAt(now time.Time) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "authorizationId", value: r.AuthorizationID},
		{name: "deviceCode", value: r.DeviceCode},
		{name: "userCode", value: r.UserCode},
	} {
		if err := validateText(field.name, field.value, true); err != nil {
			return err
		}
	}
	if err := validateVerificationURL("verificationUrl", r.VerificationURL, true); err != nil {
		return err
	}
	if err := validateVerificationURL("verificationUrlComplete", r.VerificationURLComplete, false); err != nil {
		return err
	}
	if err := ValidateBearerToken(r.BearerToken); err != nil {
		return err
	}
	if r.ExpiresAt.IsZero() {
		return errors.New("expiresAt is required")
	}
	if !r.ExpiresAt.After(now) {
		return ErrLoginExpired
	}
	_, err := durationFromMilliseconds(r.PollIntervalMS)
	return err
}

// DevicePollRequest identifies one pending device authorization.
type DevicePollRequest struct {
	AuthorizationID string `json:"authorizationId"`
	DeviceCode      string `json:"deviceCode"`
}

// Validate checks the private polling identifiers returned by device start.
func (r DevicePollRequest) Validate() error {
	if err := validateText("authorizationId", r.AuthorizationID, true); err != nil {
		return err
	}
	return validateText("deviceCode", r.DeviceCode, true)
}

// DevicePollResponse reports the authorization state and approved credential metadata.
type DevicePollResponse struct {
	Status       DeviceStatus      `json:"status"`
	CredentialID string            `json:"credentialId,omitempty"`
	Principal    PrincipalSnapshot `json:"principal,omitempty"`
	ExpiresAt    time.Time         `json:"expiresAt,omitempty"`
	RetryAfterMS int64             `json:"retryAfterMs,omitempty"`
}

// Validate checks a device-poll response against the current time.
func (r DevicePollResponse) Validate() error {
	return r.ValidateAt(time.Now().UTC())
}

// ValidateAt checks status-dependent device-poll response fields.
func (r DevicePollResponse) ValidateAt(now time.Time) error {
	if err := r.Status.Validate(); err != nil {
		return err
	}
	if _, err := durationFromMilliseconds(r.RetryAfterMS); err != nil {
		return errors.Wrap(err, "retryAfterMs is invalid")
	}
	switch r.Status {
	case DeviceStatusPending:
		if err := validateAbsentApproval(r); err != nil {
			return err
		}
	case DeviceStatusApproved:
		if err := validateText("credentialId", r.CredentialID, true); err != nil {
			return err
		}
		if err := r.Principal.Validate(); err != nil {
			return errors.Wrap(err, "principal is invalid")
		}
		if r.ExpiresAt.IsZero() {
			return errors.New("expiresAt is required for approved authorization")
		}
		if !r.ExpiresAt.After(now) {
			return errors.New("approved credential is expired")
		}
	case DeviceStatusDenied, DeviceStatusExpired:
		if err := validateAbsentApproval(r); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks that a device status is one of the protocol-defined values.
func (s DeviceStatus) Validate() error {
	switch s {
	case DeviceStatusPending, DeviceStatusApproved, DeviceStatusDenied, DeviceStatusExpired:
		return nil
	default:
		return errors.Errorf("unknown device authorization status %q", s)
	}
}

// PrincipalSnapshot is the approved principal captured when a credential is issued.
type PrincipalSnapshot struct {
	ID      string   `json:"id"`
	Issuer  string   `json:"issuer,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Name    string   `json:"name,omitempty"`
	Email   string   `json:"email,omitempty"`
	Roles   []string `json:"roles"`
}

// Validate checks the stable principal identity and normalized role set.
func (p PrincipalSnapshot) Validate() error {
	if err := validateText("id", p.ID, true); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "issuer", value: p.Issuer},
		{name: "subject", value: p.Subject},
		{name: "name", value: p.Name},
		{name: "email", value: p.Email},
	} {
		if err := validateText(field.name, field.value, false); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{}, len(p.Roles))
	for _, role := range p.Roles {
		if err := validateText("role", role, true); err != nil {
			return err
		}
		if _, ok := seen[role]; ok {
			return errors.Errorf("role %q is duplicated", role)
		}
		seen[role] = struct{}{}
	}
	return nil
}

// GenerateBearerToken creates a Kodelet user bearer from 32 cryptographically random bytes.
func GenerateBearerToken() (string, error) {
	payload := make([]byte, bearerTokenPayloadBytes)
	if _, err := rand.Read(payload); err != nil {
		return "", errors.Wrap(err, "failed to generate user bearer token")
	}
	return BearerTokenPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// NewBearerToken creates a Kodelet user bearer token.
func NewBearerToken() (string, error) {
	return GenerateBearerToken()
}

// ValidateBearerToken checks the exact kltu_ plus canonical 32-byte base64url format.
func ValidateBearerToken(token string) error {
	if token == "" {
		return errors.New("bearer token is required")
	}
	if strings.TrimSpace(token) != token {
		return errors.New("bearer token must not contain leading or trailing whitespace")
	}
	if !strings.HasPrefix(token, BearerTokenPrefix) {
		return errors.Errorf("bearer token must use the %s prefix", BearerTokenPrefix)
	}
	encoded := strings.TrimPrefix(token, BearerTokenPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return errors.New("bearer token must use canonical unpadded base64url")
	}
	if len(decoded) != bearerTokenPayloadBytes {
		return errors.Errorf("bearer token must encode exactly %d bytes", bearerTokenPayloadBytes)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return errors.New("bearer token must use canonical unpadded base64url")
	}
	return nil
}

func validateAbsentApproval(response DevicePollResponse) error {
	if response.CredentialID != "" || !response.Principal.isZero() {
		return errors.New("credential approval fields are only valid for approved authorization")
	}
	return nil
}

func (p PrincipalSnapshot) isZero() bool {
	return p.ID == "" && p.Issuer == "" && p.Subject == "" && p.Name == "" && p.Email == "" && len(p.Roles) == 0
}

func validateText(name, value string, required bool) error {
	if value == "" {
		if required {
			return errors.Errorf("%s is required", name)
		}
		return nil
	}
	if strings.TrimSpace(value) != value {
		return errors.Errorf("%s must not contain leading or trailing whitespace", name)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.Errorf("%s must not contain control characters", name)
	}
	return nil
}

func validateVerificationURL(name, value string, required bool) error {
	if value == "" {
		if required {
			return errors.Errorf("%s is required", name)
		}
		return nil
	}
	if strings.TrimSpace(value) != value {
		return errors.Errorf("%s must not contain leading or trailing whitespace", name)
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.Errorf("%s must be an absolute http or https URL without user information or a fragment", name)
	}
	return nil
}

func durationFromMilliseconds(milliseconds int64) (time.Duration, error) {
	if milliseconds < 0 {
		return 0, errors.New("interval must not be negative")
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if milliseconds > int64(maxDuration/time.Millisecond) {
		return 0, errors.New("interval is too large")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}
