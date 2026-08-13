package userauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userAuthRoundTripFunc func(*http.Request) (*http.Response, error)

func (f userAuthRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type userAuthFakeClock struct {
	mu      sync.Mutex
	current time.Time
	sleeps  []time.Duration
}

func (c *userAuthFakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *userAuthFakeClock) sleep(ctx context.Context, duration time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, duration)
	c.current = c.current.Add(duration)
	c.mu.Unlock()
	return ctx.Err()
}

func (c *userAuthFakeClock) recordedSleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

func TestLoginStartsPersistsNotifiesAndApproves(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	clock := &userAuthFakeClock{current: time.Date(2030, time.May, 6, 7, 8, 9, 0, time.UTC)}
	rawServer := "http://LOCALHOST:80/base/./"
	canonicalServer := "http://localhost/base"
	bearer := testBearerToken(0x91)
	principal := testPrincipalSnapshot()
	var startRequest DeviceStartRequest
	var callbackCalled bool
	var paths []string

	httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		assert.Empty(t, request.Header.Get("Authorization"))
		assert.Equal(t, "application/json", request.Header.Get("Accept"))
		switch request.URL.Path {
		case "/base" + DeviceStartPath:
			assert.Equal(t, http.MethodPost, request.Method)
			assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
			payload, readErr := io.ReadAll(request.Body)
			require.NoError(t, readErr)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(payload, &fields))
			assert.ElementsMatch(t, []string{"clientName", "clientOS", "clientArch", "kodeletVersion"}, mapKeys(fields))
			require.NoError(t, json.Unmarshal(payload, &startRequest))
			return userAuthJSONResponse(t, http.StatusCreated, DeviceStartResponse{
				AuthorizationID:         "authorization-new",
				DeviceCode:              "device-secret-new",
				UserCode:                "ABCD-EFGH",
				VerificationURL:         "https://kodelet.example/auth/device",
				VerificationURLComplete: "https://kodelet.example/auth/device?user_code=ABCD-EFGH",
				BearerToken:             bearer,
				ExpiresAt:               clock.now().Add(10 * time.Minute),
				PollIntervalMS:          2000,
			}, nil), nil
		case "/base" + DevicePollPath:
			assert.True(t, callbackCalled, "pending callback must run before polling")
			poll := decodeUserAuthJSON[DevicePollRequest](t, request.Body)
			assert.Equal(t, "authorization-new", poll.AuthorizationID)
			assert.Equal(t, "device-secret-new", poll.DeviceCode)
			return userAuthJSONResponse(t, http.StatusOK, DevicePollResponse{
				Status:       DeviceStatusApproved,
				CredentialID: "credential-new",
				Principal:    principal,
				ExpiresAt:    clock.now().Add(24 * time.Hour),
			}, nil), nil
		default:
			t.Fatalf("unexpected user-auth request path %q", request.URL.Path)
			return nil, io.ErrUnexpectedEOF
		}
	})}

	credential, err := login(t.Context(), LoginConfig{
		Server:     rawServer,
		Store:      store,
		HTTPClient: httpClient,
		OnPending: func(info LoginInfo) {
			pending, found, loadErr := store.LoadPendingLogin(canonicalServer)
			require.NoError(t, loadErr)
			require.True(t, found)
			assert.Equal(t, bearer, pending.BearerToken)
			assert.Equal(t, "device-secret-new", pending.DeviceCode)
			assert.Equal(t, canonicalServer, info.Server)
			assert.Equal(t, "ABCD-EFGH", info.UserCode)
			assert.Equal(t, 2*time.Second, info.PollInterval)
			assert.False(t, info.Resumed)
			encoded, marshalErr := json.Marshal(info)
			require.NoError(t, marshalErr)
			assert.NotContains(t, string(encoded), bearer)
			assert.NotContains(t, string(encoded), pending.DeviceCode)
			callbackCalled = true
		},
	}, loginDependencies{now: clock.now, sleep: clock.sleep})
	require.NoError(t, err)
	assert.Equal(t, []string{"/base" + DeviceStartPath, "/base" + DevicePollPath}, paths)
	assert.Equal(t, []time.Duration{2 * time.Second}, clock.recordedSleeps())
	assert.Equal(t, defaultUserClientName, startRequest.ClientName)
	assert.Equal(t, runtime.GOOS, startRequest.ClientOS)
	assert.Equal(t, runtime.GOARCH, startRequest.ClientArch)
	assert.Equal(t, version.Get().Version, startRequest.KodeletVersion)
	assert.Equal(t, "credential-new", credential.CredentialID)
	assert.Equal(t, bearer, credential.BearerToken)
	assert.Equal(t, principal, credential.Principal)

	storedCredential, found, err := store.LoadCredential(canonicalServer)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, credential, storedCredential)
	_, found, err = store.LoadPendingLogin(canonicalServer)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestLoginResumesUnexpiredAndReplacesExpiredPendingState(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		store, err := NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &userAuthFakeClock{current: time.Date(2030, time.June, 7, 8, 9, 10, 0, time.UTC)}
		server := "http://localhost:8383/control"
		pending := testPendingLogin(server, "authorization-resumed", 0xa1, clock.now())
		pending.PollIntervalMS = 1500
		require.NoError(t, store.SavePendingLogin(pending))
		var requests int
		var displayed LoginInfo
		httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			assert.Equal(t, "/control"+DevicePollPath, request.URL.Path)
			return userAuthJSONResponse(t, http.StatusOK, DevicePollResponse{
				Status:       DeviceStatusApproved,
				CredentialID: "credential-resumed",
				Principal:    testPrincipalSnapshot(),
				ExpiresAt:    clock.now().Add(time.Hour),
			}, nil), nil
		})}

		credential, err := login(t.Context(), LoginConfig{
			Server:     server + "/",
			Store:      store,
			HTTPClient: httpClient,
			OnPending:  func(info LoginInfo) { displayed = info },
		}, loginDependencies{now: clock.now, sleep: clock.sleep})
		require.NoError(t, err)
		assert.Equal(t, 1, requests)
		assert.Equal(t, []time.Duration{1500 * time.Millisecond}, clock.recordedSleeps())
		assert.True(t, displayed.Resumed)
		assert.Equal(t, pending.UserCode, displayed.UserCode)
		assert.Equal(t, pending.BearerToken, credential.BearerToken)
	})

	t.Run("expired starts new flow", func(t *testing.T) {
		store, err := NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &userAuthFakeClock{current: time.Date(2030, time.July, 8, 9, 10, 11, 0, time.UTC)}
		server := "http://localhost:8484"
		expired := testPendingLogin(server, "authorization-expired-local", 0xa2, clock.now().Add(-time.Hour))
		expired.ExpiresAt = clock.now().Add(-time.Minute)
		require.NoError(t, store.SavePendingLogin(expired))
		newBearer := testBearerToken(0xa3)
		var starts, polls int
		httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case DeviceStartPath:
				starts++
				return userAuthJSONResponse(t, http.StatusCreated, DeviceStartResponse{
					AuthorizationID: "authorization-replacement",
					DeviceCode:      "device-replacement",
					UserCode:        "REPL-ACE1",
					VerificationURL: "https://kodelet.example/auth/device",
					BearerToken:     newBearer,
					ExpiresAt:       clock.now().Add(time.Minute),
				}, nil), nil
			case DevicePollPath:
				polls++
				poll := decodeUserAuthJSON[DevicePollRequest](t, request.Body)
				assert.Equal(t, "authorization-replacement", poll.AuthorizationID)
				return userAuthJSONResponse(t, http.StatusOK, DevicePollResponse{Status: DeviceStatusDenied}, nil), nil
			default:
				t.Fatalf("unexpected user-auth request path %q", request.URL.Path)
				return nil, io.ErrUnexpectedEOF
			}
		})}

		_, err = login(t.Context(), LoginConfig{Server: server, Store: store, HTTPClient: httpClient}, loginDependencies{now: clock.now, sleep: clock.sleep})
		require.ErrorIs(t, err, ErrLoginDenied)
		assert.Equal(t, 1, starts)
		assert.Equal(t, 1, polls)
		_, found, loadErr := store.LoadPendingLogin(server)
		require.NoError(t, loadErr)
		assert.False(t, found)
	})
}

func TestLoginRespectsRetryHintsAndPerformsFinalPollAtExpiry(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	require.NoError(t, err)
	clock := &userAuthFakeClock{current: time.Date(2030, time.August, 9, 10, 11, 12, 0, time.UTC)}
	server := "http://localhost:8585"
	pending := testPendingLogin(server, "authorization-retries", 0xb1, clock.now())
	pending.PollIntervalMS = 2000
	pending.ExpiresAt = clock.now().Add(10 * time.Second)
	require.NoError(t, store.SavePendingLogin(pending))
	var polls int
	httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		polls++
		switch polls {
		case 1:
			return userAuthJSONResponse(t, http.StatusOK, DevicePollResponse{
				Status:       DeviceStatusPending,
				ExpiresAt:    pending.ExpiresAt,
				RetryAfterMS: 3000,
			}, http.Header{"Retry-After": []string{"4"}}), nil
		case 2:
			return userAuthJSONResponse(t, http.StatusTooManyRequests, map[string]any{
				"error":        "slow_down",
				"retryAfterMs": 5000,
			}, http.Header{"Retry-After": []string{"6"}}), nil
		case 3:
			assert.Equal(t, pending.ExpiresAt, clock.now(), "third request must be the final poll at expiry")
			return userAuthJSONResponse(t, http.StatusOK, DevicePollResponse{
				Status:       DeviceStatusApproved,
				CredentialID: "credential-final",
				Principal:    testPrincipalSnapshot(),
				ExpiresAt:    clock.now().Add(time.Hour),
			}, nil), nil
		default:
			t.Fatalf("unexpected poll attempt %d", polls)
			return nil, io.ErrUnexpectedEOF
		}
	})}

	credential, err := login(t.Context(), LoginConfig{Server: server, Store: store, HTTPClient: httpClient}, loginDependencies{now: clock.now, sleep: clock.sleep})
	require.NoError(t, err)
	assert.Equal(t, "credential-final", credential.CredentialID)
	assert.Equal(t, 3, polls)
	assert.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 4 * time.Second}, clock.recordedSleeps())
}

func TestLoginDeletesOnlyTerminalPendingState(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   DevicePollResponse
		wantError  error
	}{
		{name: "denied", statusCode: http.StatusOK, response: DevicePollResponse{Status: DeviceStatusDenied}, wantError: ErrLoginDenied},
		{name: "expired", statusCode: http.StatusOK, response: DevicePollResponse{Status: DeviceStatusExpired}, wantError: ErrLoginExpired},
		{name: "not found", statusCode: http.StatusNotFound, wantError: ErrLoginExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStoreAt(t.TempDir())
			require.NoError(t, err)
			clock := &userAuthFakeClock{current: time.Date(2030, time.September, 10, 11, 12, 13, 0, time.UTC)}
			server := "http://localhost:8686"
			pending := testPendingLogin(server, "authorization-terminal", 0xc1, clock.now())
			pending.PollIntervalMS = 0
			require.NoError(t, store.SavePendingLogin(pending))
			httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
				if test.statusCode == http.StatusNotFound {
					return userAuthJSONResponse(t, test.statusCode, map[string]string{"error": "not found"}, nil), nil
				}
				return userAuthJSONResponse(t, test.statusCode, test.response, nil), nil
			})}

			_, err = login(t.Context(), LoginConfig{Server: server, Store: store, HTTPClient: httpClient}, loginDependencies{now: clock.now, sleep: clock.sleep})
			require.ErrorIs(t, err, test.wantError)
			_, found, loadErr := store.LoadPendingLogin(server)
			require.NoError(t, loadErr)
			assert.False(t, found)
		})
	}
}

func TestLoginPreservesPendingStateAndRedactsSecretsOnNonterminalFailures(t *testing.T) {
	t.Run("context cancellation", func(t *testing.T) {
		store, err := NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &userAuthFakeClock{current: time.Date(2030, time.October, 11, 12, 13, 14, 0, time.UTC)}
		server := "http://localhost:8787"
		pending := testPendingLogin(server, "authorization-cancel", 0xd1, clock.now())
		pending.PollIntervalMS = 5000
		require.NoError(t, store.SavePendingLogin(pending))
		ctx, cancel := context.WithCancel(t.Context())
		requests := 0
		deps := loginDependencies{now: clock.now, sleep: func(ctx context.Context, duration time.Duration) error {
			clock.mu.Lock()
			clock.sleeps = append(clock.sleeps, duration)
			clock.mu.Unlock()
			cancel()
			return ctx.Err()
		}}
		_, err = login(ctx, LoginConfig{Server: server, Store: store, HTTPClient: &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, io.ErrUnexpectedEOF
		})}}, deps)
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, requests)
		assert.Equal(t, []time.Duration{5 * time.Second}, clock.recordedSleeps())
		assertPendingLoginExists(t, store, server, pending.AuthorizationID)
	})

	t.Run("network error", func(t *testing.T) {
		store, err := NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &userAuthFakeClock{current: time.Date(2030, time.November, 12, 13, 14, 15, 0, time.UTC)}
		server := "http://localhost:8888"
		pending := testPendingLogin(server, "authorization-network", 0xd2, clock.now())
		pending.PollIntervalMS = 0
		require.NoError(t, store.SavePendingLogin(pending))
		httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("transport echoed %s and %s", pending.DeviceCode, pending.BearerToken)
		})}
		_, err = login(t.Context(), LoginConfig{Server: server, Store: store, HTTPClient: httpClient}, loginDependencies{now: clock.now, sleep: clock.sleep})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), pending.DeviceCode)
		assert.NotContains(t, err.Error(), pending.BearerToken)
		assert.Equal(t, 2, bytes.Count([]byte(err.Error()), []byte("[REDACTED]")))
		assertPendingLoginExists(t, store, server, pending.AuthorizationID)
	})

	t.Run("server error", func(t *testing.T) {
		store, err := NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &userAuthFakeClock{current: time.Date(2030, time.December, 13, 14, 15, 16, 0, time.UTC)}
		server := "http://localhost:8989"
		pending := testPendingLogin(server, "authorization-server", 0xd3, clock.now())
		pending.PollIntervalMS = 0
		require.NoError(t, store.SavePendingLogin(pending))
		message := "server echoed " + pending.DeviceCode + " and " + pending.BearerToken
		httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return userAuthJSONResponse(t, http.StatusServiceUnavailable, map[string]string{"error": message}, nil), nil
		})}
		_, err = login(t.Context(), LoginConfig{Server: server, Store: store, HTTPClient: httpClient}, loginDependencies{now: clock.now, sleep: clock.sleep})
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
		assert.NotContains(t, err.Error(), pending.DeviceCode)
		assert.NotContains(t, err.Error(), pending.BearerToken)
		assertPendingLoginExists(t, store, server, pending.AuthorizationID)
	})
}

func TestLoginStrictAndBoundedResponses(t *testing.T) {
	t.Run("unknown start field", func(t *testing.T) {
		store, err := NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &userAuthFakeClock{current: time.Date(2031, time.January, 2, 3, 4, 5, 0, time.UTC)}
		bearer := testBearerToken(0xe1)
		body := fmt.Sprintf(`{"authorizationId":"authorization-1","deviceCode":"device-1","userCode":"ABCD-EFGH","verificationUrl":"https://kodelet.example/auth/device","bearerToken":"%s","expiresAt":"%s","pollIntervalMs":0,"unexpected":true}`, bearer, clock.now().Add(time.Minute).Format(time.RFC3339))
		httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return userAuthRawResponse(http.StatusCreated, []byte(body), nil), nil
		})}
		_, err = login(t.Context(), LoginConfig{Server: "http://localhost:9090", Store: store, HTTPClient: httpClient}, loginDependencies{now: clock.now, sleep: clock.sleep})
		require.ErrorContains(t, err, "unknown field")
		assert.NotContains(t, err.Error(), bearer)
	})

	t.Run("oversized response", func(t *testing.T) {
		store, err := NewStoreAt(t.TempDir())
		require.NoError(t, err)
		httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return userAuthRawResponse(http.StatusServiceUnavailable, bytes.Repeat([]byte("x"), maxHTTPResponseBytes+1), nil), nil
		})}
		_, err = Login(t.Context(), LoginConfig{Server: "http://localhost:9191", Store: store, HTTPClient: httpClient})
		require.ErrorContains(t, err, "exceeds")
	})
}

func TestValidateAndRevokeCredentialUseBearerOnlyOnAuthenticatedEndpoints(t *testing.T) {
	bearer := testBearerToken(0xf1)
	principal := testPrincipalSnapshot()
	var calls int
	httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "Bearer "+bearer, request.Header.Get("Authorization"))
		assert.Equal(t, "application/json", request.Header.Get("Accept"))
		switch calls {
		case 1:
			assert.Equal(t, http.MethodGet, request.Method)
			assert.Equal(t, "/base"+MePath, request.URL.Path)
			return userAuthJSONResponse(t, http.StatusOK, principal, nil), nil
		case 2:
			assert.Equal(t, http.MethodDelete, request.Method)
			assert.Equal(t, "/base"+CurrentCredentialPath, request.URL.Path)
			return userAuthRawResponse(http.StatusNoContent, nil, nil), nil
		default:
			t.Fatalf("unexpected authenticated request %d", calls)
			return nil, io.ErrUnexpectedEOF
		}
	})}

	validated, err := ValidateCredential(t.Context(), "http://LOCALHOST:80/base/./", bearer, httpClient)
	require.NoError(t, err)
	assert.Equal(t, principal, validated)
	require.NoError(t, RevokeCredential(t.Context(), "http://localhost/base", bearer, httpClient))
	assert.Equal(t, 2, calls)

	errorClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return userAuthJSONResponse(t, http.StatusUnauthorized, map[string]string{"error": "invalid bearer " + bearer}, nil), nil
	})}
	_, err = ValidateCredential(t.Context(), "http://localhost:9292", bearer, errorClient)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.NotContains(t, err.Error(), bearer)
	assert.Contains(t, err.Error(), "[REDACTED]")

	transportClient := &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("header contained %s", bearer)
	})}
	err = RevokeCredential(t.Context(), "http://localhost:9393", bearer, transportClient)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), bearer)

	requests := 0
	_, err = ValidateCredential(t.Context(), "http://localhost:9494", "invalid", &http.Client{Transport: userAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, io.ErrUnexpectedEOF
	})})
	require.Error(t, err)
	assert.Zero(t, requests)
}

func TestConcurrentLoginsStartOneFlowPerCanonicalServer(t *testing.T) {
	root := t.TempDir()
	firstStore, err := NewStoreAt(root)
	require.NoError(t, err)
	secondStore, err := NewStoreAt(root)
	require.NoError(t, err)
	clock := &userAuthFakeClock{current: time.Date(2031, time.February, 3, 4, 5, 6, 0, time.UTC)}
	server := "http://localhost:9595"
	bearer := testBearerToken(0xf2)
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	resumedSeen := make(chan struct{})
	var resumedOnce sync.Once
	var startCalls atomic.Int32
	var pollCalls atomic.Int32
	httpClient := &http.Client{Transport: userAuthRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case DeviceStartPath:
			if startCalls.Add(1) == 1 {
				close(startEntered)
			}
			<-releaseStart
			return userAuthJSONResponse(t, http.StatusCreated, DeviceStartResponse{
				AuthorizationID: "authorization-shared",
				DeviceCode:      "device-shared",
				UserCode:        "SHAR-ED01",
				VerificationURL: "https://kodelet.example/auth/device",
				BearerToken:     bearer,
				ExpiresAt:       clock.now().Add(time.Minute),
			}, nil), nil
		case DevicePollPath:
			pollCalls.Add(1)
			select {
			case <-resumedSeen:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
			return userAuthJSONResponse(t, http.StatusOK, DevicePollResponse{
				Status:       DeviceStatusApproved,
				CredentialID: "credential-shared",
				Principal:    testPrincipalSnapshot(),
				ExpiresAt:    clock.now().Add(time.Hour),
			}, nil), nil
		default:
			t.Fatalf("unexpected user-auth request path %q", request.URL.Path)
			return nil, io.ErrUnexpectedEOF
		}
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	results := make(chan error, 2)
	config := func(store *Store) LoginConfig {
		return LoginConfig{
			Server:     server,
			Store:      store,
			HTTPClient: httpClient,
			OnPending: func(info LoginInfo) {
				if info.Resumed {
					resumedOnce.Do(func() { close(resumedSeen) })
				}
			},
		}
	}
	go func() {
		_, loginErr := login(ctx, config(firstStore), loginDependencies{now: clock.now, sleep: clock.sleep})
		results <- loginErr
	}()
	select {
	case <-startEntered:
	case <-ctx.Done():
		t.Fatal("first login did not start")
	}
	go func() {
		_, loginErr := login(ctx, config(secondStore), loginDependencies{now: clock.now, sleep: clock.sleep})
		results <- loginErr
	}()
	close(releaseStart)
	for range 2 {
		require.NoError(t, <-results)
	}
	assert.Equal(t, int32(1), startCalls.Load())
	assert.Equal(t, int32(2), pollCalls.Load())
	credential, found, err := firstStore.LoadCredential(server)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-shared", credential.CredentialID)
}

func assertPendingLoginExists(t *testing.T, store *Store, server, authorizationID string) {
	t.Helper()
	pending, found, err := store.LoadPendingLogin(server)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, authorizationID, pending.AuthorizationID)
}

func decodeUserAuthJSON[T any](t *testing.T, reader io.Reader) T {
	t.Helper()
	var value T
	require.NoError(t, json.NewDecoder(reader).Decode(&value))
	return value
}

func userAuthJSONResponse(t *testing.T, statusCode int, value any, header http.Header) *http.Response {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return userAuthRawResponse(statusCode, payload, header)
}

func userAuthRawResponse(statusCode int, body []byte, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: statusCode, Header: header, Body: io.NopCloser(bytes.NewReader(body))}
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2031, time.March, 4, 5, 6, 7, 0, time.UTC)
	duration, ok := parseRetryAfter("7", now)
	assert.True(t, ok)
	assert.Equal(t, 7*time.Second, duration)
	duration, ok = parseRetryAfter(now.Add(4*time.Second).Format(http.TimeFormat), now)
	assert.True(t, ok)
	assert.Equal(t, 4*time.Second, duration)
	duration, ok = parseRetryAfter(now.Add(-time.Second).Format(http.TimeFormat), now)
	assert.True(t, ok)
	assert.Zero(t, duration)
	_, ok = parseRetryAfter("not-a-retry-time", now)
	assert.False(t, ok)
}
