package protocol

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/pkg/errors"
)

const (
	// EnrollmentStartPath starts a runner device-enrollment flow.
	EnrollmentStartPath = "/api/runner/v1/enrollment/start"
	// EnrollmentPollPath polls a runner device-enrollment flow for approval.
	EnrollmentPollPath = "/api/runner/v1/enrollment/poll"
)

const (
	// DPoPHeader carries an RFC 9449 proof JWT.
	DPoPHeader = "DPoP"
	// DPoPAuthorizationScheme identifies a DPoP-bound access token.
	DPoPAuthorizationScheme = "DPoP"
	// DPoPProofType is the required typ protected-header value for a DPoP proof.
	DPoPProofType = "dpop+jwt"
	// RunnerAccessTokenPrefix distinguishes enrolled runner access tokens from other credentials.
	RunnerAccessTokenPrefix = "kltr_"
)

const (
	runnerAccessTokenBytes = 32
	dpopJTIBytes           = 16
	maxDPoPProofBytes      = 16 * 1024
)

// EnrollmentStatus is the current state of a device-enrollment flow.
type EnrollmentStatus string

const (
	EnrollmentStatusPending  EnrollmentStatus = "pending"
	EnrollmentStatusApproved EnrollmentStatus = "approved"
	EnrollmentStatusDenied   EnrollmentStatus = "denied"
	EnrollmentStatusExpired  EnrollmentStatus = "expired"
)

// EnrollmentStartRequest describes the runner and public key awaiting approval.
type EnrollmentStartRequest struct {
	ProtocolVersions []int     `json:"protocolVersions,omitempty"`
	PublicKey        string    `json:"publicKey"`
	Fingerprint      string    `json:"fingerprint"`
	Host             Host      `json:"host"`
	Workspace        Workspace `json:"workspace"`
	DisplayName      string    `json:"displayName,omitempty"`
	KodeletVersion   string    `json:"kodeletVersion,omitempty"`
}

// Validate checks the enrollment identity and Ed25519 public-key binding.
func (r EnrollmentStartRequest) Validate() error {
	publicKey, err := DecodePublicKey(r.PublicKey)
	if err != nil {
		return err
	}
	fingerprint, err := CredentialFingerprint(publicKey)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Fingerprint) != fingerprint {
		return errors.New("runner credential fingerprint does not match public key")
	}
	if strings.TrimSpace(r.Host.InstanceID) == "" {
		return errors.New("host.instanceId is required")
	}
	if strings.TrimSpace(r.Workspace.Path) == "" {
		return errors.New("workspace.path is required")
	}
	if strings.TrimSpace(r.Workspace.Name) == "" {
		return errors.New("workspace.name is required")
	}
	return nil
}

// EnrollmentStartResponse returns the device code and browser approval location.
type EnrollmentStartResponse struct {
	EnrollmentID            string    `json:"enrollmentId"`
	DeviceCode              string    `json:"deviceCode"`
	UserCode                string    `json:"userCode"`
	VerificationURL         string    `json:"verificationUrl"`
	VerificationURLComplete string    `json:"verificationUrlComplete,omitempty"`
	ExpiresAt               time.Time `json:"expiresAt"`
	PollIntervalMS          int64     `json:"pollIntervalMs"`
}

// EnrollmentPollRequest identifies one pending device-enrollment flow.
type EnrollmentPollRequest struct {
	EnrollmentID string `json:"enrollmentId"`
	DeviceCode   string `json:"deviceCode"`
}

// Validate checks the private polling identifiers returned by enrollment start.
func (r EnrollmentPollRequest) Validate() error {
	if strings.TrimSpace(r.EnrollmentID) == "" {
		return errors.New("enrollmentId is required")
	}
	if strings.TrimSpace(r.DeviceCode) == "" {
		return errors.New("deviceCode is required")
	}
	return nil
}

// EnrollmentPollResponse reports approval state and, once approved, the DPoP-bound credential.
type EnrollmentPollResponse struct {
	Status       EnrollmentStatus `json:"status"`
	CredentialID string           `json:"credentialId,omitempty"`
	AccessToken  string           `json:"accessToken,omitempty"`
	TokenType    string           `json:"tokenType,omitempty"`
	Fingerprint  string           `json:"fingerprint,omitempty"`
	RunnerID     string           `json:"runnerId,omitempty"`
	RetryAfterMS int64            `json:"retryAfterMs,omitempty"`
}

// DPoPProofOptions describes one RFC 9449 proof JWT.
type DPoPProofOptions struct {
	Method      string
	TargetURL   string
	AccessToken string
	JTI         string
	IssuedAt    time.Time
	Nonce       string
}

// DPoPVerificationOptions defines the request and credential binding expected by a resource server.
type DPoPVerificationOptions struct {
	Method      string
	TargetURL   string
	AccessToken string
	PublicKey   ed25519.PublicKey
	Now         time.Time
	MaxAge      time.Duration
	FutureSkew  time.Duration
}

// VerifiedDPoPProof contains replay and key-binding information from a verified proof.
type VerifiedDPoPProof struct {
	JTI           string
	IssuedAt      time.Time
	JWKThumbprint string
}

type dpopProofClaims struct {
	JTI   string `json:"jti"`
	HTM   string `json:"htm"`
	HTU   string `json:"htu"`
	IAT   int64  `json:"iat"`
	ATH   string `json:"ath"`
	Nonce string `json:"nonce,omitempty"`
}

// EncodePublicKey returns the canonical unpadded base64url representation of an Ed25519 public key.
func EncodePublicKey(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", errors.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	return base64.RawURLEncoding.EncodeToString(publicKey), nil
}

// DecodePublicKey parses a canonical unpadded base64url Ed25519 public key.
func DecodePublicKey(encoded string) (ed25519.PublicKey, error) {
	decoded, err := decodeBase64URL("Ed25519 public key", encoded, ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(decoded), nil
}

// CredentialFingerprint returns the stable SHA-256 fingerprint for an Ed25519 public key.
func CredentialFingerprint(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", errors.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	digest := sha256.Sum256(publicKey)
	return "sha256:" + base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

// NewRunnerAccessToken returns a cryptographically random opaque token suitable for DPoP binding.
func NewRunnerAccessToken() (string, error) {
	value := make([]byte, runnerAccessTokenBytes)
	if _, err := rand.Read(value); err != nil {
		return "", errors.Wrap(err, "failed to generate runner access token")
	}
	return RunnerAccessTokenPrefix + base64.RawURLEncoding.EncodeToString(value), nil
}

// ValidateRunnerAccessToken checks the canonical runner access-token representation.
func ValidateRunnerAccessToken(token string) error {
	if strings.TrimSpace(token) != token || !strings.HasPrefix(token, RunnerAccessTokenPrefix) {
		return errors.New("runner access token is invalid")
	}
	_, err := decodeBase64URL("runner access token", strings.TrimPrefix(token, RunnerAccessTokenPrefix), runnerAccessTokenBytes)
	return err
}

// DPoPAccessTokenHash returns the RFC 9449 ath value for an access token.
func DPoPAccessTokenHash(accessToken string) (string, error) {
	if err := ValidateRunnerAccessToken(accessToken); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(accessToken))
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

// NormalizeDPoPHTU returns the query- and fragment-free HTTP target URI used by RFC 9449.
// WebSocket ws/wss URLs are mapped to their HTTP handshake schemes.
func NormalizeDPoPHTU(raw string) (string, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return "", errors.New("DPoP target URL is required without surrounding whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return "", errors.New("DPoP target URL must be an absolute HTTP URL without user information")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	case "http", "https":
	default:
		return "", errors.New("DPoP target URL must use http, https, ws, or wss")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", errors.New("DPoP target URL host is required")
	}
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

// SignDPoPProof creates an EdDSA-signed RFC 9449 proof JWT with an embedded public JWK.
func SignDPoPProof(privateKey ed25519.PrivateKey, options DPoPProofOptions) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.Errorf("Ed25519 private key must be %d bytes", ed25519.PrivateKeySize)
	}
	method, err := normalizeDPoPMethod(options.Method)
	if err != nil {
		return "", err
	}
	targetURL, err := NormalizeDPoPHTU(options.TargetURL)
	if err != nil {
		return "", err
	}
	ath, err := DPoPAccessTokenHash(options.AccessToken)
	if err != nil {
		return "", err
	}
	jti := options.JTI
	if jti == "" {
		value := make([]byte, dpopJTIBytes)
		if _, err := rand.Read(value); err != nil {
			return "", errors.Wrap(err, "failed to generate DPoP proof identifier")
		}
		jti = base64.RawURLEncoding.EncodeToString(value)
	}
	if err := validateDPoPIdentifier("jti", jti); err != nil {
		return "", err
	}
	if options.Nonce != "" {
		if err := validateDPoPIdentifier("nonce", options.Nonce); err != nil {
			return "", err
		}
	}
	issuedAt := options.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now()
	}
	claims, err := json.Marshal(dpopProofClaims{
		JTI:   jti,
		HTM:   method,
		HTU:   targetURL,
		IAT:   issuedAt.Unix(),
		ATH:   ath,
		Nonce: options.Nonce,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to encode DPoP proof claims")
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: privateKey}, (&jose.SignerOptions{EmbedJWK: true}).WithType(jose.ContentType(DPoPProofType)))
	if err != nil {
		return "", errors.Wrap(err, "failed to create DPoP proof signer")
	}
	signed, err := signer.Sign(claims)
	if err != nil {
		return "", errors.Wrap(err, "failed to sign DPoP proof")
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		return "", errors.Wrap(err, "failed to serialize DPoP proof")
	}
	return compact, nil
}

// VerifyDPoPProof verifies an RFC 9449 proof and its request, token, time, and key bindings.
// Replay detection remains the resource server's responsibility using the returned JTI.
func VerifyDPoPProof(proof string, options DPoPVerificationOptions) (VerifiedDPoPProof, error) {
	if proof == "" || strings.TrimSpace(proof) != proof || len(proof) > maxDPoPProofBytes || strings.Count(proof, ".") != 2 {
		return VerifiedDPoPProof{}, errors.New("DPoP proof must be one compact JWT")
	}
	if len(options.PublicKey) != ed25519.PublicKeySize {
		return VerifiedDPoPProof{}, errors.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	method, err := normalizeDPoPMethod(options.Method)
	if err != nil {
		return VerifiedDPoPProof{}, err
	}
	targetURL, err := NormalizeDPoPHTU(options.TargetURL)
	if err != nil {
		return VerifiedDPoPProof{}, err
	}
	ath, err := DPoPAccessTokenHash(options.AccessToken)
	if err != nil {
		return VerifiedDPoPProof{}, err
	}
	signed, err := jose.ParseSignedCompact(proof, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(signed.Signatures) != 1 {
		return VerifiedDPoPProof{}, errors.New("DPoP proof JWT is invalid")
	}
	header := signed.Signatures[0].Protected
	if header.Algorithm != string(jose.EdDSA) || header.JSONWebKey == nil || !header.JSONWebKey.IsPublic() {
		return VerifiedDPoPProof{}, errors.New("DPoP proof must use an embedded public Ed25519 JWK")
	}
	typ, ok := header.ExtraHeaders[jose.HeaderType].(string)
	if !ok || typ != DPoPProofType {
		return VerifiedDPoPProof{}, errors.New("DPoP proof typ header is invalid")
	}
	proofPublicKey, ok := header.JSONWebKey.Key.(ed25519.PublicKey)
	if !ok || len(proofPublicKey) != ed25519.PublicKeySize {
		return VerifiedDPoPProof{}, errors.New("DPoP proof JWK is not an Ed25519 public key")
	}
	if subtle.ConstantTimeCompare(proofPublicKey, options.PublicKey) != 1 {
		return VerifiedDPoPProof{}, errors.New("DPoP proof key does not match the access token binding")
	}
	payload, err := signed.Verify(proofPublicKey)
	if err != nil {
		return VerifiedDPoPProof{}, errors.New("DPoP proof signature is invalid")
	}
	var claims dpopProofClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return VerifiedDPoPProof{}, errors.New("DPoP proof claims are invalid")
	}
	if err := validateDPoPIdentifier("jti", claims.JTI); err != nil {
		return VerifiedDPoPProof{}, err
	}
	if claims.HTM != method {
		return VerifiedDPoPProof{}, errors.New("DPoP proof htm claim does not match the request method")
	}
	claimTargetURL, err := NormalizeDPoPHTU(claims.HTU)
	if err != nil || claimTargetURL != targetURL {
		return VerifiedDPoPProof{}, errors.New("DPoP proof htu claim does not match the request target")
	}
	if subtle.ConstantTimeCompare([]byte(claims.ATH), []byte(ath)) != 1 {
		return VerifiedDPoPProof{}, errors.New("DPoP proof ath claim does not match the access token")
	}
	if claims.IAT <= 0 {
		return VerifiedDPoPProof{}, errors.New("DPoP proof iat claim is invalid")
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	issuedAt := time.Unix(claims.IAT, 0).UTC()
	if options.MaxAge <= 0 || options.FutureSkew < 0 {
		return VerifiedDPoPProof{}, errors.New("DPoP proof verification window is invalid")
	}
	if issuedAt.Before(now.Add(-options.MaxAge)) || issuedAt.After(now.Add(options.FutureSkew)) {
		return VerifiedDPoPProof{}, errors.New("DPoP proof iat claim is outside the accepted time window")
	}
	thumbprint, err := header.JSONWebKey.Thumbprint(crypto.SHA256)
	if err != nil {
		return VerifiedDPoPProof{}, errors.Wrap(err, "failed to calculate DPoP JWK thumbprint")
	}
	return VerifiedDPoPProof{
		JTI:           claims.JTI,
		IssuedAt:      issuedAt,
		JWKThumbprint: base64.RawURLEncoding.EncodeToString(thumbprint),
	}, nil
}

func normalizeDPoPMethod(method string) (string, error) {
	if strings.TrimSpace(method) != method || method == "" || strings.ContainsAny(method, " \t\r\n") {
		return "", errors.New("DPoP HTTP method is invalid")
	}
	return strings.ToUpper(method), nil
}

func validateDPoPIdentifier(name, value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
		return errors.Errorf("DPoP proof %s claim is invalid", name)
	}
	return nil
}

func decodeBase64URL(name, encoded string, size int) ([]byte, error) {
	if encoded == "" {
		return nil, errors.Errorf("%s is required", name)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode %s as unpadded base64url", name)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, errors.Errorf("%s must use canonical unpadded base64url", name)
	}
	if len(decoded) != size {
		return nil, errors.Errorf("%s must decode to %d bytes", name, size)
	}
	return decoded, nil
}
