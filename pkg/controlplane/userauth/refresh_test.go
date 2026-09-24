package userauth

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func refreshTestCredential(server string) Credential {
	now := time.Now().UTC()
	credential := testCredential(server, "refresh-session", 0x81, now.Add(-time.Hour))
	credential.ExpiresAt = now.Add(time.Hour)
	credential.AccessExpiresAt = now.Add(15 * time.Second)
	credential.RefreshToken = strings.Replace(testBearerToken(0x82), BearerTokenPrefix, RefreshTokenPrefix, 1)
	return credential
}

func refreshTestResponse(credential Credential) RefreshResponse {
	return RefreshResponse{
		CredentialID:    credential.CredentialID,
		BearerToken:     testBearerToken(0x83),
		RefreshToken:    strings.Replace(testBearerToken(0x84), BearerTokenPrefix, RefreshTokenPrefix, 1),
		AccessExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		ExpiresAt:       credential.ExpiresAt,
	}
}

func TestCredentialRefreshConcurrentStores(t *testing.T) {
	root := t.TempDir()
	first, err := NewStoreAt(root)
	require.NoError(t, err)
	second, err := NewStoreAt(root)
	require.NoError(t, err)
	credential := refreshTestCredential("https://kodelet.example/base")
	require.NoError(t, first.SaveCredential(credential))
	rotated := refreshTestResponse(credential)
	var calls atomic.Int32
	client := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		assert.Equal(t, "/base"+RefreshPath, request.URL.Path)
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Empty(t, request.Header.Get("Authorization"))
		refresh := decodeUserAuthJSON[RefreshRequest](t, request.Body)
		assert.Equal(t, credential.RefreshToken, refresh.RefreshToken)
		pending, readErr := readJSONFile[Credential](first.credentialPath(credential.Server))
		assert.NoError(t, readErr)
		assert.True(t, pending.RefreshPending, "rotation must be marked durable before sending")
		deadline, ok := request.Context().Deadline()
		assert.True(t, ok)
		assert.InDelta(t, refreshTimeout.Seconds(), time.Until(deadline).Seconds(), 2)
		return userAuthJSONResponse(t, http.StatusOK, rotated, nil), nil
	})}
	const count = 16
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range count {
		wait.Go(func() {
			<-start
			store := first
			if i%2 != 0 {
				store = second
			}
			// Simultaneous 401 recovery must not rotate the freshly saved pair again.
			rejected := ""
			if i%3 == 0 {
				rejected = credential.BearerToken
			}
			got, requestErr := store.CredentialForRequest(t.Context(), credential.Server, client, rejected)
			assert.NoError(t, requestErr)
			assert.Equal(t, rotated.BearerToken, got.BearerToken)
			assert.Equal(t, rotated.RefreshToken, got.RefreshToken)
			assert.False(t, got.RefreshPending)
			assert.Equal(t, credential.ExpiresAt, got.ExpiresAt)
		})
	}
	close(start)
	wait.Wait()
	assert.Equal(t, int32(1), calls.Load())
	stored, found, err := first.LoadCredential(credential.Server)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, rotated.AccessExpiresAt, stored.AccessExpiresAt)
}

func TestCredentialRefreshLocalChecks(t *testing.T) {
	for _, scenario := range []string{"fresh", "final window", "legacy", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := NewStoreAt(t.TempDir())
			require.NoError(t, err)
			credential := refreshTestCredential("https://kodelet.example")
			credential.AccessExpiresAt = time.Now().Add(5 * time.Minute)
			switch scenario {
			case "final window":
				credential.ExpiresAt = time.Now().UTC().Add(20 * time.Second)
				credential.AccessExpiresAt = credential.ExpiresAt
			case "legacy":
				credential.RefreshToken = ""
				credential.AccessExpiresAt = time.Time{}
			case "expired":
				credential.ExpiresAt = time.Now().Add(-time.Minute)
				credential.AccessExpiresAt = credential.ExpiresAt
			}
			require.NoError(t, store.SaveCredential(credential))
			client := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("local check must not perform a network request")
				return nil, io.ErrUnexpectedEOF
			})}
			got, err := store.CredentialForRequest(t.Context(), credential.Server, client, "")
			if scenario == "expired" {
				require.ErrorIs(t, err, ErrLoginRequired)
				assert.Contains(t, err.Error(), "kodelet auth login --server")
			} else {
				require.NoError(t, err)
				assert.Equal(t, credential.BearerToken, got.BearerToken)
			}
		})
	}
}

func TestCredentialRefreshFailureRecovery(t *testing.T) {
	for _, scenario := range []string{"lost response", "extended expiry", "slow_down", "generic 429", "malformed slow_down"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := NewStoreAt(t.TempDir())
			require.NoError(t, err)
			credential := refreshTestCredential("https://kodelet.example")
			require.NoError(t, store.SaveCredential(credential))
			rotated := refreshTestResponse(credential)
			calls := 0
			client := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, RefreshPath, request.URL.Path)
				assert.Equal(t, credential.RefreshToken, decodeUserAuthJSON[RefreshRequest](t, request.Body).RefreshToken)
				if calls > 1 {
					return userAuthJSONResponse(t, http.StatusOK, rotated, nil), nil
				}
				switch scenario {
				case "lost response":
					return nil, errors.New("lost response " + credential.RefreshToken)
				case "extended expiry":
					rotated.ExpiresAt = rotated.ExpiresAt.Add(time.Hour)
				case "slow_down":
					return userAuthJSONResponse(t, http.StatusTooManyRequests, map[string]any{"error": "slow_down", "retryAfterMs": 1500}, http.Header{"Retry-After": {"2"}}), nil
				case "generic 429":
					return userAuthJSONResponse(t, http.StatusTooManyRequests, map[string]string{"error": credential.RefreshToken}, nil), nil
				case "malformed slow_down":
					return userAuthJSONResponse(t, http.StatusTooManyRequests, map[string]string{"error": "slow_down"}, http.Header{"Retry-After": {"2"}}), nil
				}
				return userAuthJSONResponse(t, http.StatusOK, rotated, nil), nil
			})}
			authenticated, err := NewAuthenticatedClient(credential.Server, store, client)
			require.NoError(t, err)
			_, err = ValidateCredential(t.Context(), credential.Server, credential.BearerToken, authenticated)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), credential.RefreshToken)
			assert.NotContains(t, err.Error(), credential.BearerToken)
			assert.Equal(t, 1, calls, "no automatic retry")
			if scenario == "slow_down" {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr, "auth helpers must preserve temporary failures")
				assert.NotErrorIs(t, err, ErrLoginRequired)
				assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
				assert.Equal(t, 2*time.Second, apiErr.RetryAfter)
				assert.Contains(t, err.Error(), "retry after 2s")
			} else {
				require.ErrorIs(t, err, ErrLoginRequired)
			}
			// A later CLI process can reuse only a known-unconsumed token.
			nextStore, err := NewStoreAt(store.Root())
			require.NoError(t, err)
			got, err := nextStore.CredentialForRequest(t.Context(), credential.Server, client, "")
			if scenario == "slow_down" {
				require.NoError(t, err)
				assert.Equal(t, rotated.RefreshToken, got.RefreshToken)
				assert.False(t, got.RefreshPending)
				assert.Equal(t, 2, calls)
			} else {
				require.ErrorIs(t, err, ErrLoginRequired)
				assert.Contains(t, err.Error(), "previous token refresh was interrupted")
				assert.Equal(t, 1, calls)
			}
		})
	}
}

type refreshTrackedBody struct {
	io.Reader
	closed bool
}

func (b *refreshTrackedBody) Close() error {
	b.closed = true
	return nil
}

func TestAuthenticatedClientRetriesOnlyRejectedReplayableRequests(t *testing.T) {
	for _, scenario := range []string{"retry", "retry rejected again", "generic 401", "403", "network", "unreplayable"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := NewStoreAt(t.TempDir())
			require.NoError(t, err)
			credential := refreshTestCredential("https://kodelet.example/base")
			credential.AccessExpiresAt = time.Now().Add(5 * time.Minute)
			require.NoError(t, store.SaveCredential(credential))
			rotated := refreshTestResponse(credential)
			apiCalls, refreshCalls := 0, 0
			firstBody := &refreshTrackedBody{Reader: strings.NewReader("rejected")}
			transport := userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/base"+RefreshPath {
					refreshCalls++
					assert.Empty(t, request.Header.Get("Authorization"))
					return userAuthJSONResponse(t, http.StatusOK, rotated, nil), nil
				}
				apiCalls++
				assert.Equal(t, "preserved", request.Header.Get("X-Test"))
				body, readErr := io.ReadAll(request.Body)
				assert.NoError(t, readErr)
				_ = request.Body.Close()
				assert.Equal(t, "chat body", string(body))
				if apiCalls > 1 {
					assert.True(t, firstBody.closed)
					assert.Equal(t, "Bearer "+rotated.BearerToken, request.Header.Get("Authorization"))
					if scenario != "retry rejected again" {
						return userAuthJSONResponse(t, http.StatusOK, nil, nil), nil
					}
				} else {
					assert.Equal(t, "Bearer "+credential.BearerToken, request.Header.Get("Authorization"))
				}
				status := http.StatusUnauthorized
				headers := http.Header{}
				headers.Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				switch scenario {
				case "generic 401":
					headers.Del("WWW-Authenticate")
				case "403":
					status = http.StatusForbidden
				case "network":
					return nil, errors.New("failure " + credential.BearerToken + " " + credential.RefreshToken)
				}
				return &http.Response{StatusCode: status, Header: headers, Body: firstBody}, nil
			})
			client, err := NewAuthenticatedClient(credential.Server, store, &http.Client{Transport: transport})
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, credential.Server+"/api/chat", strings.NewReader("chat body"))
			require.NoError(t, err)
			request.Header.Set("X-Test", "preserved")
			request.Header.Set("Authorization", "unchanged original")
			if scenario == "unreplayable" {
				request.GetBody = nil
			}
			response, err := client.Do(request)
			if scenario == "network" {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), credential.BearerToken)
				assert.NotContains(t, err.Error(), credential.RefreshToken)
			} else {
				require.NoError(t, err)
				_ = response.Body.Close()
			}
			assert.Equal(t, "unchanged original", request.Header.Get("Authorization"))
			if strings.HasPrefix(scenario, "retry") {
				assert.Equal(t, 2, apiCalls)
				assert.Equal(t, 1, refreshCalls)
			} else {
				assert.Equal(t, 1, apiCalls)
				assert.Zero(t, refreshCalls)
			}
		})
	}
}

func TestAuthenticatedClientScopeRedirectAndClosedBody(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	store, err := NewStore()
	require.NoError(t, err)
	credential := refreshTestCredential("https://kodelet.example/base")
	credential.AccessExpiresAt = time.Now().Add(5 * time.Minute)
	require.NoError(t, store.SaveCredential(credential))
	calls := 0
	base := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return userAuthJSONResponse(t, http.StatusTemporaryRedirect, nil, http.Header{"Location": {"https://kodelet.example/base/redirected"}}), nil
	})}
	client, err := NewAuthenticatedClient(credential.Server, nil, base)
	require.NoError(t, err)
	for _, target := range []string{
		"https://other.example/base/api/chat", "https://kodelet.example/base-other",
		"https://kodelet.example/base%2f..%2fother",
	} {
		body := &refreshTrackedBody{Reader: strings.NewReader("private")}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, body)
		require.NoError(t, err)
		_, err = client.Do(request)
		require.ErrorContains(t, err, "outside the server base URL")
		assert.True(t, body.closed)
	}
	assert.Zero(t, calls)
	response, err := client.Get(credential.Server + "/api/chat?query=ok")
	require.NoError(t, err)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
	assert.Equal(t, 1, calls, "no redirects, even inside the permitted base URL")

	credential.RefreshPending = true
	require.NoError(t, store.SaveCredential(credential))
	body := &refreshTrackedBody{Reader: strings.NewReader("private")}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, credential.Server+"/api/chat", body)
	require.NoError(t, err)
	_, err = client.Do(request)
	require.ErrorIs(t, err, ErrLoginRequired)
	assert.True(t, body.closed, "preflight rejection must close the unsent body")
	assert.Equal(t, 1, calls)
}

func TestAuthenticatedClientNeverSwitchesSavedSession(t *testing.T) {
	for _, scenario := range []string{"before construction", "before request", "during rejection", "handshake"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := NewStoreAt(t.TempDir())
			require.NoError(t, err)
			original := refreshTestCredential("https://kodelet.example")
			original.AccessExpiresAt = time.Now().Add(5 * time.Minute)
			require.NoError(t, store.SaveCredential(original))
			replacement := refreshTestCredential(original.Server)
			replacement.CredentialID = "new-sign-in"
			replacement.Principal.ID = "new-principal"
			replacement.BearerToken = testBearerToken(0x95)
			replacement.RefreshToken = strings.Replace(testBearerToken(0x96), BearerTokenPrefix, RefreshTokenPrefix, 1)
			if scenario == "before construction" {
				require.NoError(t, store.SaveCredential(replacement))
			}
			calls := 0
			rejectedBody := &refreshTrackedBody{Reader: strings.NewReader("rejected")}
			client, err := NewAuthenticatedClient(original.Server, store, &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "during rejection", scenario)
				assert.Equal(t, CurrentCredentialPath, request.URL.Path)
				assert.Equal(t, http.MethodDelete, request.Method)
				assert.Equal(t, "Bearer "+original.BearerToken, request.Header.Get("Authorization"))
				assert.NoError(t, store.SaveCredential(replacement))
				headers := http.Header{}
				headers.Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				return &http.Response{StatusCode: http.StatusUnauthorized, Header: headers, Body: rejectedBody}, nil
			})}, original.CredentialID)
			if scenario != "before construction" {
				require.NoError(t, err)
				if scenario != "during rejection" {
					require.NoError(t, store.SaveCredential(replacement))
				}
				if scenario == "handshake" {
					request, requestErr := http.NewRequestWithContext(t.Context(), http.MethodGet, original.Server+"/api/relay", nil)
					require.NoError(t, requestErr)
					err = client.Transport.(interface{ AuthorizeRequest(*http.Request) error }).AuthorizeRequest(request)
					assert.Empty(t, request.Header.Get("Authorization"))
				} else {
					err = RevokeCredential(t.Context(), original.Server, original.BearerToken, client)
				}
			}
			require.ErrorIs(t, err, ErrLoginRequired)
			assert.Contains(t, err.Error(), "sign-in changed; reconnect")
			if scenario == "during rejection" {
				assert.Equal(t, 1, calls, "never refresh or replay revocation under the new principal")
				assert.True(t, rejectedBody.closed)
			} else {
				assert.Zero(t, calls, "never send or refresh with a replaced sign-in")
			}
			stored, found, err := store.LoadCredential(original.Server)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, replacement.CredentialID, stored.CredentialID)
			assert.Equal(t, replacement.BearerToken, stored.BearerToken)
			assert.Equal(t, replacement.RefreshToken, stored.RefreshToken)
			assert.False(t, stored.RefreshPending)
		})
	}
}
