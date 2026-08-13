package protocol

import (
	"crypto"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerAuthHTTPContract(t *testing.T) {
	assert.Equal(t, "/api/runner/v1/enrollment/start", EnrollmentStartPath)
	assert.Equal(t, "/api/runner/v1/enrollment/poll", EnrollmentPollPath)
	assert.Equal(t, "DPoP", DPoPHeader)
	assert.Equal(t, "DPoP", DPoPAuthorizationScheme)
	assert.Equal(t, "dpop+jwt", DPoPProofType)
	assert.Equal(t, "kltr_", RunnerAccessTokenPrefix)
}

func TestEnrollmentDTOsUseJSONWireNames(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	encodedPublicKey, err := EncodePublicKey(publicKey)
	require.NoError(t, err)
	fingerprint, err := CredentialFingerprint(publicKey)
	require.NoError(t, err)

	request := EnrollmentStartRequest{
		ProtocolVersions: []int{Version},
		PublicKey:        encodedPublicKey,
		Fingerprint:      fingerprint,
		Host:             Host{InstanceID: "host-one"},
		Workspace:        Workspace{Path: "/work/project", Name: "project"},
		DisplayName:      "Project Runner",
		KodeletVersion:   "v1.2.3",
	}
	require.NoError(t, request.Validate())
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"protocolVersions":[1],
		"publicKey":"`+encodedPublicKey+`",
		"fingerprint":"`+fingerprint+`",
		"host":{"instanceId":"host-one","hostname":"","os":"","arch":""},
		"workspace":{"path":"/work/project","name":"project"},
		"displayName":"Project Runner",
		"kodeletVersion":"v1.2.3"
	}`, string(payload))

	response := EnrollmentStartResponse{
		EnrollmentID:            "enrollment-one",
		DeviceCode:              "device-secret",
		UserCode:                "ABCD-EFGH",
		VerificationURL:         "https://kodelet.example/runner/enroll",
		VerificationURLComplete: "https://kodelet.example/runner/enroll?code=ABCD-EFGH",
		ExpiresAt:               time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC),
		PollIntervalMS:          2500,
	}
	payload, err = json.Marshal(response)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"verificationUrl"`)
	assert.Contains(t, string(payload), `"pollIntervalMs":2500`)

	require.NoError(t, (EnrollmentPollRequest{EnrollmentID: "enrollment-one", DeviceCode: "device-secret"}).Validate())
	require.ErrorContains(t, (EnrollmentPollRequest{}).Validate(), "enrollmentId")
	require.ErrorContains(t, (EnrollmentPollRequest{EnrollmentID: "enrollment-one"}).Validate(), "deviceCode")
	pollResponse := EnrollmentPollResponse{
		Status:       EnrollmentStatusApproved,
		CredentialID: "credential-one",
		AccessToken:  testRunnerAccessToken(0x41),
		TokenType:    DPoPAuthorizationScheme,
		Fingerprint:  fingerprint,
		RunnerID:     "runner-one",
	}
	payload, err = json.Marshal(pollResponse)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"accessToken":"kltr_`)
	assert.Contains(t, string(payload), `"tokenType":"DPoP"`)

	badFingerprint := request
	badFingerprint.Fingerprint = "sha256:not-the-key"
	require.ErrorContains(t, badFingerprint.Validate(), "does not match")
	badPublicKey := request
	badPublicKey.PublicKey = "not-base64url!"
	require.ErrorContains(t, badPublicKey.Validate(), "decode Ed25519 public key")
}

func TestSignAndVerifyRunnerDPoPProof(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	accessToken := testRunnerAccessToken(0x42)
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	targetURL := "wss://KODELET.example:443/api/runner/v1/connect?ignored=true#ignored"

	proof, err := SignDPoPProof(privateKey, DPoPProofOptions{
		Method:      "GET",
		TargetURL:   targetURL,
		AccessToken: accessToken,
		JTI:         "proof-one",
		IssuedAt:    now,
	})
	require.NoError(t, err)
	signed, err := jose.ParseSignedCompact(proof, []jose.SignatureAlgorithm{jose.EdDSA})
	require.NoError(t, err)
	require.Len(t, signed.Signatures, 1)
	header := signed.Signatures[0].Protected
	assert.Equal(t, string(jose.EdDSA), header.Algorithm)
	assert.Equal(t, DPoPProofType, header.ExtraHeaders[jose.HeaderType])
	require.NotNil(t, header.JSONWebKey)
	assert.True(t, header.JSONWebKey.IsPublic())
	assert.Equal(t, publicKey, header.JSONWebKey.Key)
	payload, err := signed.Verify(publicKey)
	require.NoError(t, err)
	var claims dpopProofClaims
	require.NoError(t, json.Unmarshal(payload, &claims))
	assert.Equal(t, "proof-one", claims.JTI)
	assert.Equal(t, "GET", claims.HTM)
	assert.Equal(t, "https://kodelet.example/api/runner/v1/connect", claims.HTU)
	assert.Equal(t, now.Unix(), claims.IAT)
	wantATH, err := DPoPAccessTokenHash(accessToken)
	require.NoError(t, err)
	assert.Equal(t, wantATH, claims.ATH)

	verified, err := VerifyDPoPProof(proof, DPoPVerificationOptions{
		Method:      "GET",
		TargetURL:   "https://kodelet.example/api/runner/v1/connect",
		AccessToken: accessToken,
		PublicKey:   publicKey,
		Now:         now.Add(time.Minute),
		MaxAge:      5 * time.Minute,
		FutureSkew:  30 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, "proof-one", verified.JTI)
	assert.Equal(t, now, verified.IssuedAt)
	thumbprint, err := (&jose.JSONWebKey{Key: publicKey}).Thumbprint(crypto.SHA256)
	require.NoError(t, err)
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(thumbprint), verified.JWKThumbprint)

	wrongKey, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	baseOptions := DPoPVerificationOptions{Method: "GET", TargetURL: targetURL, AccessToken: accessToken, PublicKey: publicKey, Now: now, MaxAge: 5 * time.Minute, FutureSkew: 30 * time.Second}
	wrongMethod := baseOptions
	wrongMethod.Method = "POST"
	_, err = VerifyDPoPProof(proof, wrongMethod)
	require.ErrorContains(t, err, "htm")
	wrongTarget := baseOptions
	wrongTarget.TargetURL = "https://kodelet.example/api/runner/v1/other"
	_, err = VerifyDPoPProof(proof, wrongTarget)
	require.ErrorContains(t, err, "htu")
	wrongToken := baseOptions
	wrongToken.AccessToken = testRunnerAccessToken(0x43)
	_, err = VerifyDPoPProof(proof, wrongToken)
	require.ErrorContains(t, err, "ath")
	wrongKeyOptions := baseOptions
	wrongKeyOptions.PublicKey = wrongKey
	_, err = VerifyDPoPProof(proof, wrongKeyOptions)
	require.ErrorContains(t, err, "key")
	stale := baseOptions
	stale.Now = now.Add(5*time.Minute + time.Second)
	_, err = VerifyDPoPProof(proof, stale)
	require.ErrorContains(t, err, "time window")
	future := baseOptions
	future.Now = now.Add(-31 * time.Second)
	_, err = VerifyDPoPProof(proof, future)
	require.ErrorContains(t, err, "time window")
	_, err = VerifyDPoPProof(" "+proof, baseOptions)
	require.ErrorContains(t, err, "compact JWT")

	_, err = SignDPoPProof(ed25519.PrivateKey("short"), DPoPProofOptions{})
	require.ErrorContains(t, err, "private key")
	_, err = SignDPoPProof(privateKey, DPoPProofOptions{Method: "GET", TargetURL: targetURL, AccessToken: accessToken, JTI: " "})
	require.ErrorContains(t, err, "jti")
	invalidWindow := baseOptions
	invalidWindow.MaxAge = 0
	_, err = VerifyDPoPProof(proof, invalidWindow)
	require.ErrorContains(t, err, "window")
}

func TestRunnerAccessTokenAndDPoPTargetValidation(t *testing.T) {
	token, err := NewRunnerAccessToken()
	require.NoError(t, err)
	require.NoError(t, ValidateRunnerAccessToken(token))
	assert.Regexp(t, `^kltr_[A-Za-z0-9_-]{43}$`, token)
	ath, err := DPoPAccessTokenHash(token)
	require.NoError(t, err)
	assert.Regexp(t, `^[A-Za-z0-9_-]{43}$`, ath)

	for _, invalid := range []string{"", " token", "token", RunnerAccessTokenPrefix + "bad=", RunnerAccessTokenPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 31))} {
		require.Error(t, ValidateRunnerAccessToken(invalid), invalid)
	}

	normalized, err := NormalizeDPoPHTU("WSS://EXAMPLE.COM:443/api/runner/v1/connect?query=ignored#fragment")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/api/runner/v1/connect", normalized)
	normalized, err = NormalizeDPoPHTU("ws://[::1]:80")
	require.NoError(t, err)
	assert.Equal(t, "http://[::1]/", normalized)
	_, err = NormalizeDPoPHTU("relative/path")
	require.Error(t, err)
	_, err = NormalizeDPoPHTU("ftp://example.com/path")
	require.Error(t, err)
}

func TestCredentialFingerprintAndPublicKeyEncoding(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	publicKey := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)

	encoded, err := EncodePublicKey(publicKey)
	require.NoError(t, err)
	assert.NotContains(t, encoded, "=")
	decoded, err := DecodePublicKey(encoded)
	require.NoError(t, err)
	assert.Equal(t, publicKey, decoded)

	fingerprint, err := CredentialFingerprint(publicKey)
	require.NoError(t, err)
	assert.Regexp(t, `^sha256:[A-Za-z0-9_-]{43}$`, fingerprint)
	second, err := CredentialFingerprint(publicKey)
	require.NoError(t, err)
	assert.Equal(t, fingerprint, second)

	_, err = EncodePublicKey(ed25519.PublicKey("short"))
	require.ErrorContains(t, err, "public key")
	_, err = DecodePublicKey(encoded + "=")
	require.Error(t, err)
	_, err = CredentialFingerprint(ed25519.PublicKey("short"))
	require.ErrorContains(t, err, "public key")
}

func testRunnerAccessToken(fill byte) string {
	return RunnerAccessTokenPrefix + base64.RawURLEncoding.EncodeToString(bytesOfSize(fill, runnerAccessTokenBytes))
}

func bytesOfSize(fill byte, size int) []byte {
	value := make([]byte, size)
	for index := range value {
		value[index] = fill
	}
	return value
}
