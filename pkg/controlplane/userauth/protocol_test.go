package userauth

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtocolConstantsAndBearerFormat(t *testing.T) {
	assert.Equal(t, "/api/auth/v1/device/start", DeviceStartPath)
	assert.Equal(t, "/api/auth/v1/device/poll", DevicePollPath)
	assert.Equal(t, "/auth/device", DeviceVerificationPath)
	assert.Equal(t, "/api/auth/v1/credentials/current", CurrentCredentialPath)
	assert.Equal(t, "/api/auth/me", MePath)

	generated, err := NewBearerToken()
	require.NoError(t, err)
	require.NoError(t, ValidateBearerToken(generated))
	assert.True(t, bytes.HasPrefix([]byte(generated), []byte(BearerTokenPrefix)))
	decoded, err := base64.RawURLEncoding.DecodeString(generated[len(BearerTokenPrefix):])
	require.NoError(t, err)
	assert.Len(t, decoded, bearerTokenPayloadBytes)

	valid := testBearerToken(0x31)
	require.NoError(t, ValidateBearerToken(valid))
	for _, invalid := range []string{
		"",
		"Bearer " + valid,
		" " + valid,
		valid + "=",
		BearerTokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, bearerTokenPayloadBytes-1)),
		BearerTokenPrefix + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, bearerTokenPayloadBytes)),
	} {
		assert.Error(t, ValidateBearerToken(invalid), invalid)
	}
}

func TestProtocolValidationIsStatusDependentAndStrict(t *testing.T) {
	now := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	start := DeviceStartResponse{
		AuthorizationID:         "authorization-1",
		DeviceCode:              "device-secret-1",
		UserCode:                "ABCD-EFGH",
		VerificationURL:         "https://kodelet.example/auth/device",
		VerificationURLComplete: "https://kodelet.example/auth/device?user_code=ABCD-EFGH",
		BearerToken:             testBearerToken(0x41),
		ExpiresAt:               now.Add(10 * time.Minute),
		PollIntervalMS:          2500,
	}
	require.NoError(t, start.ValidateAt(now))

	invalidStart := start
	invalidStart.AuthorizationID = " authorization-1"
	require.ErrorContains(t, invalidStart.ValidateAt(now), "whitespace")
	invalidStart = start
	invalidStart.VerificationURL = "https://user@kodelet.example/auth/device"
	require.ErrorContains(t, invalidStart.ValidateAt(now), "without user information")
	invalidStart = start
	invalidStart.ExpiresAt = now
	require.ErrorIs(t, invalidStart.ValidateAt(now), ErrLoginExpired)

	pending := DevicePollResponse{Status: DeviceStatusPending, ExpiresAt: now.Add(time.Minute), RetryAfterMS: 1000}
	require.NoError(t, pending.ValidateAt(now))
	approved := DevicePollResponse{
		Status:       DeviceStatusApproved,
		CredentialID: "credential-1",
		Principal:    testPrincipalSnapshot(),
		ExpiresAt:    now.Add(time.Hour),
	}
	require.NoError(t, approved.ValidateAt(now))
	approved.Principal.ID = ""
	require.ErrorContains(t, approved.ValidateAt(now), "principal")
	require.NoError(t, (DevicePollResponse{Status: DeviceStatusDenied}).ValidateAt(now))
	require.NoError(t, (DevicePollResponse{Status: DeviceStatusExpired}).ValidateAt(now))
	require.Error(t, (DevicePollResponse{Status: "unknown"}).ValidateAt(now))
	require.Error(t, (DevicePollResponse{Status: DeviceStatusPending, CredentialID: "credential-unexpected"}).ValidateAt(now))
	require.Error(t, (DevicePollResponse{Status: DeviceStatusPending, RetryAfterMS: -1}).ValidateAt(now))

	principal := testPrincipalSnapshot()
	require.NoError(t, principal.Validate())
	principal.Roles = append(principal.Roles, principal.Roles[0])
	require.ErrorContains(t, principal.Validate(), "duplicated")
}

func testBearerToken(discriminator byte) string {
	return BearerTokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{discriminator}, bearerTokenPayloadBytes))
}

func testPrincipalSnapshot() PrincipalSnapshot {
	return PrincipalSnapshot{
		ID:      "https://issuer.example|subject-1",
		Issuer:  "https://issuer.example",
		Subject: "subject-1",
		Name:    "Test User",
		Email:   "user@example.com",
		Roles:   []string{"user", "terminal"},
	}
}
