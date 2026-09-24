package userauth

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/pkg/errors"
)

const (
	accessRefreshSkew = 30 * time.Second
	refreshTimeout    = 30 * time.Second
)

// CredentialForRequest reads the saved credential under the per-server process
// lock, refreshing only when needed. rejectedToken is set only after an explicit
// invalid_token response, so concurrent callers can reuse an already rotated pair.
func (s *Store) CredentialForRequest(ctx context.Context, server string, client *http.Client, rejectedToken string) (Credential, error) {
	return s.credentialForRequest(ctx, server, client, rejectedToken, "")
}

func (s *Store) credentialForRequest(ctx context.Context, server string, client *http.Client, rejectedToken, expectedCredentialID string) (Credential, error) {
	if ctx == nil {
		return Credential{}, errors.New("credential request context is required")
	}
	server, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return Credential{}, err
	}
	var credential Credential
	err = s.withStateLock("credentials", server, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		stored, found, err := s.loadCredentialUnlocked(server)
		if err != nil {
			return err
		}
		if !found {
			return loginRequired(server, "no saved sign-in")
		}
		if expectedCredentialID != "" && stored.CredentialID != expectedCredentialID {
			return loginRequired(server, "the saved sign-in changed; reconnect before sending more requests")
		}
		now := time.Now().UTC()
		if !stored.ExpiresAt.After(now) {
			return loginRequired(server, "the saved sign-in has expired")
		}
		if stored.RefreshPending {
			return loginRequired(server, "a previous token refresh was interrupted; its result is unknown")
		}
		rejected := rejectedToken != "" && constantTimeEqual(rejectedToken, stored.BearerToken)
		if stored.RefreshToken == "" {
			if rejected {
				return loginRequired(server, "the saved sign-in was rejected")
			}
			credential = stored
			return nil
		}
		// A token capped at the hard deadline cannot be extended. Avoid rotating
		// on every request during the final skew window of a session.
		nearExpiry := !stored.AccessExpiresAt.After(now.Add(accessRefreshSkew)) && stored.AccessExpiresAt.Before(stored.ExpiresAt)
		if !rejected && stored.AccessExpiresAt.After(now) && !nearExpiry {
			credential = stored
			return nil
		}
		// Persist before sending: a lost response or process crash must never
		// cause replay of a possibly consumed, single-use refresh token.
		stored.RefreshPending = true
		stored.UpdatedAt = now
		if err := writeJSONAtomic(s.credentialPath(server), stored); err != nil {
			return errors.Wrap(err, "failed to record pending token refresh")
		}
		rotated, err := refreshCredential(ctx, stored, client)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
				// Only the explicit pre-consumption slow_down response reaches
				// here as an APIError. The same token is safe for a later request.
				stored.RefreshPending = false
				stored.UpdatedAt = time.Now().UTC()
				if err := writeJSONAtomic(s.credentialPath(server), stored); err != nil {
					return loginRequired(server, "the unconsumed refresh credential could not be saved")
				}
			}
			return err
		}
		if err := writeJSONAtomic(s.credentialPath(server), rotated); err != nil {
			return loginRequired(server, "the rotated credential could not be saved")
		}
		credential = rotated
		return nil
	})
	return credential, err
}

func loginRequired(server, reason string) error {
	return errors.Wrapf(ErrLoginRequired, "%s; run `kodelet auth login --server %s`", reason, server)
}

func refreshCredential(ctx context.Context, credential Credential, client *http.Client) (Credential, error) {
	endpoint, err := endpointFromPath(credential.Server, RefreshPath)
	if err != nil {
		return Credential{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	client = unwrappedClient(client)
	response, err := postJSON(ctx, client, endpoint, RefreshRequest{RefreshToken: credential.RefreshToken}, "refresh", credential.RefreshToken, credential.BearerToken)
	if err != nil {
		return Credential{}, loginRequired(credential.Server, "token refresh did not complete; its result is unknown")
	}
	if response.statusCode == http.StatusTooManyRequests {
		var slowDown struct {
			Error        string `json:"error"`
			RetryAfterMS int64  `json:"retryAfterMs"`
		}
		if err := decodeStrictJSON(response.body, &slowDown); err == nil && slowDown.Error == "slow_down" {
			bodyDelay, bodyErr := durationFromMilliseconds(slowDown.RetryAfterMS)
			headerDelay, headerOK := parseRetryAfter(response.header.Get("Retry-After"), time.Now().UTC())
			if bodyErr == nil && bodyDelay > 0 && headerOK {
				return Credential{}, &APIError{
					Operation:  "refresh",
					StatusCode: http.StatusTooManyRequests,
					Message:    "refresh was rate limited before token consumption",
					RetryAfter: max(bodyDelay, headerDelay),
				}
			}
		}
	}
	if response.statusCode != http.StatusOK {
		return Credential{}, loginRequired(credential.Server, "token refresh was rejected or did not complete")
	}
	var rotated RefreshResponse
	if err := decodeStrictJSON(response.body, &rotated); err != nil {
		return Credential{}, loginRequired(credential.Server, "the server returned an invalid token refresh response")
	}
	now := time.Now().UTC()
	if err := rotated.ValidateAt(now); err != nil || rotated.CredentialID != credential.CredentialID || !rotated.ExpiresAt.Equal(credential.ExpiresAt) || constantTimeEqual(rotated.BearerToken, credential.BearerToken) || constantTimeEqual(rotated.RefreshToken, credential.RefreshToken) {
		return Credential{}, loginRequired(credential.Server, "the server returned an invalid rotated credential")
	}
	credential.BearerToken = rotated.BearerToken
	credential.RefreshToken = rotated.RefreshToken
	credential.AccessExpiresAt = rotated.AccessExpiresAt.UTC()
	credential.RefreshPending = false
	credential.UpdatedAt = now
	return credential, nil
}

// NewAuthenticatedClient clones an HTTP client and pins authentication to the
// currently saved session. Rotation is allowed, but a different login requires
// a new client. It refuses redirects and requests outside the server base URL.
// Callers using explicit or local daemon tokens should keep their existing client.
// An optional expectedCredentialID pins a session the caller has already loaded.
func NewAuthenticatedClient(server string, store *Store, client *http.Client, expectedCredentialID ...string) (*http.Client, error) {
	server, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return nil, err
	}
	if len(expectedCredentialID) > 1 {
		return nil, errors.New("at most one expected credential id may be supplied")
	}
	if len(expectedCredentialID) == 1 {
		if err := validateText("expected credential id", expectedCredentialID[0], true); err != nil {
			return nil, err
		}
	}
	if store == nil {
		store, err = NewStore()
		if err != nil {
			return nil, err
		}
	}
	credential, found, err := store.LoadCredential(server)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, loginRequired(server, "no saved sign-in")
	}
	if len(expectedCredentialID) == 1 && credential.CredentialID != expectedCredentialID[0] {
		return nil, loginRequired(server, "the saved sign-in changed; reconnect before sending more requests")
	}
	base, err := url.Parse(server)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse authentication server")
	}
	raw := unwrappedClient(client)
	result := *raw
	result.Transport = &authenticatedTransport{
		server: server, base: base, store: store, client: raw,
		credentialID: credential.CredentialID,
	}
	return &result, nil
}

func unwrappedClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	if transport, ok := cloned.Transport.(*authenticatedTransport); ok {
		cloned.Transport = transport.client.Transport
	}
	if cloned.Transport == nil {
		cloned.Transport = http.DefaultTransport
	}
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cloned
}

type authenticatedTransport struct {
	server       string
	base         *url.URL
	store        *Store
	client       *http.Client
	credentialID string
}

// AuthorizeRequest provides the same scoped preflight check to WebSocket
// handshakes, whose dialer does not use this HTTP transport.
func (t *authenticatedTransport) AuthorizeRequest(request *http.Request) error {
	if request == nil || !t.inScope(request) {
		return errors.New("refusing to send saved credentials outside the server base URL")
	}
	credential, err := t.store.credentialForRequest(request.Context(), t.server, t.client, "", t.credentialID)
	if err != nil {
		return err
	}
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Authorization", "Bearer "+credential.BearerToken)
	return nil
}

func (t *authenticatedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !t.inScope(request) {
		closeRequestBody(request)
		return nil, errors.New("refusing to send saved credentials outside the server base URL")
	}
	credential, err := t.store.credentialForRequest(request.Context(), t.server, t.client, "", t.credentialID)
	if err != nil {
		closeRequestBody(request)
		return nil, err
	}
	authorized := request.Clone(request.Context())
	authorized.Header.Set("Authorization", "Bearer "+credential.BearerToken)
	response, err := t.roundTrip(authorized, credential)
	if err != nil {
		return nil, err
	}
	// Only this precise authentication-middleware response guarantees that an
	// application handler did not run. Never retry network failures or generic 401s.
	if response.StatusCode != http.StatusUnauthorized || len(response.Header.Values("WWW-Authenticate")) != 1 || response.Header.Get("WWW-Authenticate") != `Bearer error="invalid_token"` || (request.Body != nil && request.Body != http.NoBody && request.GetBody == nil) {
		return response, nil
	}
	var body io.ReadCloser
	if request.Body != nil && request.Body != http.NoBody {
		body, err = request.GetBody()
		if err != nil {
			_ = response.Body.Close()
			return nil, errors.New("failed to recreate request body after authentication rejection")
		}
	}
	_ = response.Body.Close()
	credential, err = t.store.credentialForRequest(request.Context(), t.server, t.client, credential.BearerToken, t.credentialID)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return nil, err
	}
	retry := request.Clone(request.Context())
	retry.Body = body
	retry.Header.Set("Authorization", "Bearer "+credential.BearerToken)
	return t.roundTrip(retry, credential)
}

func (t *authenticatedTransport) roundTrip(request *http.Request, credential Credential) (*http.Response, error) {
	response, err := t.client.Transport.RoundTrip(request)
	if err != nil {
		if ctxErr := request.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New(redactSecrets(err.Error(), credential.BearerToken, credential.RefreshToken))
	}
	return response, nil
}

func (t *authenticatedTransport) inScope(request *http.Request) bool {
	if request.URL == nil || request.URL.User != nil || (request.Host != "" && request.Host != request.URL.Host) {
		return false
	}
	target := *request.URL
	target.RawQuery, target.Fragment, target.RawFragment = "", "", ""
	target.ForceQuery = false
	canonical, err := controlplaneurl.NormalizeBase(target.String())
	if err != nil {
		return false
	}
	normalized, err := url.Parse(canonical)
	if err != nil || normalized.Scheme != t.base.Scheme || normalized.Host != t.base.Host {
		return false
	}
	basePath := strings.TrimSuffix(t.base.Path, "/")
	targetPath := path.Clean(target.Path)
	return basePath == "" || targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}

func closeRequestBody(request *http.Request) {
	if request.Body != nil {
		_ = request.Body.Close()
	}
}
