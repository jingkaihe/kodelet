package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type enrollmentRoundTripFunc func(*http.Request) (*http.Response, error)

func (f enrollmentRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type enrollmentFakeClock struct {
	current time.Time
	sleeps  []time.Duration
}

func (c *enrollmentFakeClock) now() time.Time {
	return c.current
}

func (c *enrollmentFakeClock) sleep(ctx context.Context, duration time.Duration) error {
	c.sleeps = append(c.sleeps, duration)
	if err := ctx.Err(); err != nil {
		return err
	}
	c.current = c.current.Add(duration)
	return nil
}

type failingEnrollmentReader struct{}

func (failingEnrollmentReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestEnrollRunnerStartsPersistsAndApproves(t *testing.T) {
	workspace := t.TempDir()
	canonicalWorkspace, err := localstate.CanonicalWorkspace(workspace)
	require.NoError(t, err)
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	clock := &enrollmentFakeClock{current: time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)}
	server := "http://LOCALHOST:80/base/./"
	canonicalServer := "http://localhost/base"
	accessToken := testEnrollmentAccessToken(t)
	var startRequest protocol.EnrollmentStartRequest
	var callbackCalled bool
	var requestPaths []string

	httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestPaths = append(requestPaths, request.URL.Path)
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "application/json", request.Header.Get("Accept"))
		assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
		switch request.URL.Path {
		case "/base" + protocol.EnrollmentStartPath:
			startRequest = decodeEnrollmentJSON[protocol.EnrollmentStartRequest](t, request.Body)
			return enrollmentJSONResponse(t, http.StatusCreated, protocol.EnrollmentStartResponse{
				EnrollmentID:            "enrollment-new",
				DeviceCode:              accessToken,
				UserCode:                "ABCD-EFGH",
				VerificationURL:         "https://control.example/runner/enroll",
				VerificationURLComplete: "https://control.example/runner/enroll?user_code=ABCD-EFGH",
				ExpiresAt:               clock.current.Add(10 * time.Minute),
				PollIntervalMS:          2000,
			}, nil), nil
		case "/base" + protocol.EnrollmentPollPath:
			assert.True(t, callbackCalled, "pending callback must run before the first poll")
			pollRequest := decodeEnrollmentJSON[protocol.EnrollmentPollRequest](t, request.Body)
			assert.Equal(t, "enrollment-new", pollRequest.EnrollmentID)
			assert.Equal(t, accessToken, pollRequest.DeviceCode)
			return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{
				Status:       protocol.EnrollmentStatusApproved,
				CredentialID: "credential-new",
				AccessToken:  accessToken,
				TokenType:    protocol.DPoPAuthorizationScheme,
				RunnerID:     "runner-new",
				Fingerprint:  startRequest.Fingerprint,
			}, nil), nil
		default:
			t.Fatalf("unexpected enrollment request path %q", request.URL.Path)
			return nil, io.ErrUnexpectedEOF
		}
	})}

	result, err := enrollRunner(t.Context(), EnrollmentConfig{
		Server:      server,
		Workspace:   workspace + string(filepath.Separator) + ".",
		DisplayName: "  project runner  ",
		Store:       store,
		HTTPClient:  httpClient,
		OnPending: func(info EnrollmentInfo) {
			pending, found, loadErr := store.LoadPendingEnrollment(canonicalServer, canonicalWorkspace)
			require.NoError(t, loadErr)
			require.True(t, found)
			assert.Equal(t, accessToken, pending.DeviceCode)
			assert.Equal(t, "ABCD-EFGH", info.UserCode)
			assert.Equal(t, canonicalServer, info.Server)
			assert.Equal(t, canonicalWorkspace, info.Workspace)
			assert.Equal(t, 2*time.Second, info.PollInterval)
			assert.False(t, info.Resumed)
			serialized, marshalErr := json.Marshal(info)
			require.NoError(t, marshalErr)
			assert.NotContains(t, string(serialized), pending.DeviceCode)
			assert.NotContains(t, string(serialized), base64.RawURLEncoding.EncodeToString(pending.PrivateKey))
			callbackCalled = true
		},
	}, testEnrollmentDependencies(clock, 0x31))
	require.NoError(t, err)
	assert.Equal(t, []string{"/base" + protocol.EnrollmentStartPath, "/base" + protocol.EnrollmentPollPath}, requestPaths)
	assert.Equal(t, []time.Duration{2 * time.Second}, clock.sleeps)
	assert.Equal(t, "credential-new", result.CredentialID)
	assert.Equal(t, "runner-new", result.RunnerID)
	assert.Equal(t, startRequest.Fingerprint, result.Fingerprint)
	assert.False(t, result.Info.Resumed)

	require.NoError(t, startRequest.Validate())
	assert.Equal(t, []int{protocol.Version}, startRequest.ProtocolVersions)
	assert.Equal(t, "project runner", startRequest.DisplayName)
	assert.Equal(t, "host.example.test", startRequest.Host.Hostname)
	assert.Equal(t, runtime.GOOS, startRequest.Host.OS)
	assert.Equal(t, runtime.GOARCH, startRequest.Host.Arch)
	assert.NotEmpty(t, startRequest.Host.InstanceID)
	assert.Equal(t, canonicalWorkspace, startRequest.Workspace.Path)
	assert.Equal(t, filepath.Base(canonicalWorkspace), startRequest.Workspace.Name)
	assert.Equal(t, "v-test", startRequest.KodeletVersion)
	identity, err := store.LoadOrCreateHostIdentity()
	require.NoError(t, err)
	assert.Equal(t, identity.InstanceID, startRequest.Host.InstanceID)

	credential, found, err := store.LoadCredential(canonicalServer, canonicalWorkspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-new", credential.CredentialID)
	assert.Equal(t, accessToken, credential.AccessToken)
	assert.Equal(t, startRequest.Fingerprint, credential.Fingerprint)
	publicKey, err := protocol.DecodePublicKey(startRequest.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, publicKey, credential.PublicKey)
	assert.Equal(t, publicKey, credential.PrivateKey.Public().(ed25519.PublicKey))

	registration, found, err := store.LoadRegistration(canonicalServer, canonicalWorkspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "runner-new", registration.RunnerID)
	assert.Equal(t, "project runner", registration.DisplayName)
	_, found, err = store.LoadPendingEnrollment(canonicalServer, canonicalWorkspace)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestEnrollRunnerResumesPendingEnrollmentAfterRestart(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	server := "http://localhost:8080/control"
	clock := &enrollmentFakeClock{current: time.Date(2030, time.February, 3, 4, 5, 6, 0, time.UTC)}
	firstStore, err := localstate.NewStoreAt(root)
	require.NoError(t, err)
	pending := saveEnrollmentTestPending(t, firstStore, server, workspace, clock.current, 1500, 0x41)
	restartedStore, err := localstate.NewStoreAt(root)
	require.NoError(t, err)
	var requests int

	httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		assert.Equal(t, "/control"+protocol.EnrollmentPollPath, request.URL.Path)
		pollRequest := decodeEnrollmentJSON[protocol.EnrollmentPollRequest](t, request.Body)
		assert.Equal(t, pending.EnrollmentID, pollRequest.EnrollmentID)
		assert.Equal(t, pending.DeviceCode, pollRequest.DeviceCode)
		return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{
			Status:       protocol.EnrollmentStatusApproved,
			CredentialID: "credential-resumed",
			AccessToken:  pending.DeviceCode,
			TokenType:    protocol.DPoPAuthorizationScheme,
			RunnerID:     "runner-resumed",
			Fingerprint:  pending.Fingerprint,
		}, nil), nil
	})}

	var displayed EnrollmentInfo
	result, err := enrollRunner(t.Context(), EnrollmentConfig{
		Server:      server + "/",
		Workspace:   workspace,
		DisplayName: "resumed runner",
		Store:       restartedStore,
		HTTPClient:  httpClient,
		OnPending: func(info EnrollmentInfo) {
			displayed = info
		},
	}, enrollmentDependencies{
		now:            clock.now,
		sleep:          clock.sleep,
		random:         failingEnrollmentReader{},
		hostname:       func() (string, error) { return "unused", nil },
		kodeletVersion: func() string { return "unused" },
	})
	require.NoError(t, err)
	assert.Equal(t, 1, requests)
	assert.Equal(t, []time.Duration{1500 * time.Millisecond}, clock.sleeps)
	assert.True(t, displayed.Resumed)
	assert.Equal(t, pending.UserCode, displayed.UserCode)
	assert.Equal(t, pending.VerificationURLComplete, displayed.VerificationURLComplete)
	assert.Equal(t, "credential-resumed", result.CredentialID)
	assert.Equal(t, "runner-resumed", result.RunnerID)

	credential, found, err := restartedStore.LoadCredential(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, pending.PublicKey, credential.PublicKey)
	assert.Equal(t, pending.PrivateKey, credential.PrivateKey)
	registration, found, err := restartedStore.LoadRegistration(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "runner-resumed", registration.RunnerID)
	assert.Equal(t, "resumed runner", registration.DisplayName)
}

func TestConcurrentEnrollmentsStartOneFlowPerWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	server := "http://localhost:8182"
	firstStore, err := localstate.NewStoreAt(root)
	require.NoError(t, err)
	secondStore, err := localstate.NewStoreAt(root)
	require.NoError(t, err)
	clock := &enrollmentFakeClock{current: time.Date(2030, time.February, 3, 4, 5, 6, 0, time.UTC)}
	accessToken := testEnrollmentAccessToken(t)
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	resumedSeen := make(chan struct{})
	var resumedOnce sync.Once
	var startCalls atomic.Int32
	var pollCalls atomic.Int32
	var fingerprintMu sync.Mutex
	var fingerprint string
	httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case protocol.EnrollmentStartPath:
			started := decodeEnrollmentJSON[protocol.EnrollmentStartRequest](t, request.Body)
			fingerprintMu.Lock()
			fingerprint = started.Fingerprint
			fingerprintMu.Unlock()
			if startCalls.Add(1) == 1 {
				close(startEntered)
			}
			<-releaseStart
			return enrollmentJSONResponse(t, http.StatusCreated, protocol.EnrollmentStartResponse{
				EnrollmentID:    "enrollment-shared",
				DeviceCode:      accessToken,
				UserCode:        "SHAR-ED01",
				VerificationURL: "https://kodelet.example/runner/enroll",
				ExpiresAt:       clock.current.Add(time.Minute),
			}, nil), nil
		case protocol.EnrollmentPollPath:
			pollCalls.Add(1)
			select {
			case <-resumedSeen:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
			fingerprintMu.Lock()
			approvedFingerprint := fingerprint
			fingerprintMu.Unlock()
			return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{
				Status:       protocol.EnrollmentStatusApproved,
				CredentialID: "credential-shared",
				AccessToken:  accessToken,
				TokenType:    protocol.DPoPAuthorizationScheme,
				RunnerID:     "runner-shared",
				Fingerprint:  approvedFingerprint,
			}, nil), nil
		default:
			t.Fatalf("unexpected enrollment request path %q", request.URL.Path)
			return nil, io.ErrUnexpectedEOF
		}
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	results := make(chan error, 2)
	config := func(store *localstate.Store) EnrollmentConfig {
		return EnrollmentConfig{
			Server:     server,
			Workspace:  workspace,
			Store:      store,
			HTTPClient: httpClient,
			OnPending: func(info EnrollmentInfo) {
				if info.Resumed {
					resumedOnce.Do(func() { close(resumedSeen) })
				}
			},
		}
	}
	go func() {
		_, enrollErr := enrollRunner(ctx, config(firstStore), testEnrollmentDependencies(clock, 0x42))
		results <- enrollErr
	}()
	select {
	case <-startEntered:
	case <-ctx.Done():
		t.Fatal("first enrollment did not start")
	}
	go func() {
		_, enrollErr := enrollRunner(ctx, config(secondStore), testEnrollmentDependencies(clock, 0x43))
		results <- enrollErr
	}()
	close(releaseStart)
	for range 2 {
		require.NoError(t, <-results)
	}
	assert.Equal(t, int32(1), startCalls.Load())
	assert.Equal(t, int32(2), pollCalls.Load())
	credential, found, err := firstStore.LoadCredential(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-shared", credential.CredentialID)
}

func TestEnrollRunnerRequiresExplicitLocalCredentialReplacement(t *testing.T) {
	workspace := t.TempDir()
	server := "http://localhost:9090"
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	oldCredential := saveEnrollmentTestCredential(t, store, server, workspace, "credential-old", 0x51)
	var requests int
	rejectingClient := &http.Client{Transport: enrollmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		t.Fatal("enrollment HTTP request must not be sent without replacement opt-in")
		return nil, io.ErrUnexpectedEOF
	})}

	_, err = enrollRunner(t.Context(), EnrollmentConfig{
		Server:     server,
		Workspace:  workspace,
		Store:      store,
		HTTPClient: rejectingClient,
	}, testEnrollmentDependencies(&enrollmentFakeClock{current: time.Date(2030, time.March, 4, 5, 6, 7, 0, time.UTC)}, 0x52))
	require.ErrorIs(t, err, ErrActiveLocalCredential)
	assert.Zero(t, requests)

	clock := &enrollmentFakeClock{current: time.Date(2030, time.March, 4, 5, 6, 7, 0, time.UTC)}
	replacementAccessToken := testEnrollmentAccessToken(t)
	var newFingerprint string
	replacementClient := &http.Client{Transport: enrollmentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch request.URL.Path {
		case protocol.EnrollmentStartPath:
			start := decodeEnrollmentJSON[protocol.EnrollmentStartRequest](t, request.Body)
			newFingerprint = start.Fingerprint
			return enrollmentJSONResponse(t, http.StatusCreated, protocol.EnrollmentStartResponse{
				EnrollmentID:    "enrollment-replacement",
				DeviceCode:      replacementAccessToken,
				UserCode:        "REPL-ACE1",
				VerificationURL: "https://control.example/runner/enroll",
				ExpiresAt:       clock.current.Add(10 * time.Minute),
			}, nil), nil
		case protocol.EnrollmentPollPath:
			current, found, loadErr := store.LoadCredential(server, workspace)
			require.NoError(t, loadErr)
			require.True(t, found)
			assert.Equal(t, oldCredential.CredentialID, current.CredentialID, "old credential must remain active until approval")
			return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{
				Status:       protocol.EnrollmentStatusApproved,
				CredentialID: "credential-replacement",
				AccessToken:  replacementAccessToken,
				TokenType:    protocol.DPoPAuthorizationScheme,
				RunnerID:     "runner-replacement",
				Fingerprint:  newFingerprint,
			}, nil), nil
		default:
			t.Fatalf("unexpected enrollment request path %q", request.URL.Path)
			return nil, io.ErrUnexpectedEOF
		}
	})}

	result, err := enrollRunner(t.Context(), EnrollmentConfig{
		Server:                 server,
		Workspace:              workspace,
		Store:                  store,
		HTTPClient:             replacementClient,
		ReplaceLocalCredential: true,
	}, testEnrollmentDependencies(clock, 0x53))
	require.NoError(t, err)
	assert.Equal(t, "credential-replacement", result.CredentialID)
	replaced, found, err := store.LoadCredential(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-replacement", replaced.CredentialID)
	assert.NotEqual(t, oldCredential.Fingerprint, replaced.Fingerprint)
}

func TestEnrollRunnerObeysPollAndSlowDownIntervals(t *testing.T) {
	workspace := t.TempDir()
	server := "http://localhost:9191"
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	clock := &enrollmentFakeClock{current: time.Date(2030, time.April, 5, 6, 7, 8, 0, time.UTC)}
	pending := saveEnrollmentTestPending(t, store, server, workspace, clock.current, 2000, 0x61)
	var polls int
	httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, protocol.EnrollmentPollPath, request.URL.Path)
		polls++
		switch polls {
		case 1:
			return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{
				Status:       protocol.EnrollmentStatusPending,
				RetryAfterMS: 3000,
			}, nil), nil
		case 2:
			return enrollmentJSONResponse(t, http.StatusTooManyRequests, map[string]any{
				"error":        "slow_down",
				"retryAfterMs": 5000,
			}, http.Header{"Retry-After": []string{"4"}}), nil
		case 3:
			return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{
				Status:       protocol.EnrollmentStatusApproved,
				CredentialID: "credential-intervals",
				AccessToken:  pending.DeviceCode,
				TokenType:    protocol.DPoPAuthorizationScheme,
				RunnerID:     "runner-intervals",
				Fingerprint:  pending.Fingerprint,
			}, nil), nil
		default:
			t.Fatalf("unexpected poll attempt %d", polls)
			return nil, io.ErrUnexpectedEOF
		}
	})}

	_, err = enrollRunner(t.Context(), EnrollmentConfig{
		Server:     server,
		Workspace:  workspace,
		Store:      store,
		HTTPClient: httpClient,
	}, testEnrollmentDependencies(clock, 0x62))
	require.NoError(t, err)
	assert.Equal(t, 3, polls)
	assert.Equal(t, []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second}, clock.sleeps)
}

func TestEnrollRunnerDeletesTerminalPendingState(t *testing.T) {
	tests := []struct {
		name      string
		status    protocol.EnrollmentStatus
		wantError error
	}{
		{name: "denied", status: protocol.EnrollmentStatusDenied, wantError: ErrEnrollmentDenied},
		{name: "expired", status: protocol.EnrollmentStatusExpired, wantError: ErrEnrollmentExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			server := "http://localhost:9292"
			store, err := localstate.NewStoreAt(t.TempDir())
			require.NoError(t, err)
			clock := &enrollmentFakeClock{current: time.Date(2030, time.May, 6, 7, 8, 9, 0, time.UTC)}
			saveEnrollmentTestPending(t, store, server, workspace, clock.current, 0, 0x71)
			httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{Status: test.status}, nil), nil
			})}

			result, err := enrollRunner(t.Context(), EnrollmentConfig{
				Server:     server,
				Workspace:  workspace,
				Store:      store,
				HTTPClient: httpClient,
			}, testEnrollmentDependencies(clock, 0x72))
			require.ErrorIs(t, err, test.wantError)
			assert.NotEmpty(t, result.Info.UserCode)
			_, found, loadErr := store.LoadPendingEnrollment(server, workspace)
			require.NoError(t, loadErr)
			assert.False(t, found)
		})
	}
}

func TestEnrollRunnerPerformsFinalPollAtLocalExpiry(t *testing.T) {
	workspace := t.TempDir()
	server := "http://localhost:9393"
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	clock := &enrollmentFakeClock{current: time.Date(2030, time.June, 7, 8, 9, 10, 0, time.UTC)}
	pending := saveEnrollmentTestPending(t, store, server, workspace, clock.current, 5000, 0x73)
	pending.ExpiresAt = clock.current.Add(2 * time.Second)
	require.NoError(t, store.SavePendingEnrollment(pending))
	var requests int
	httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		assert.Equal(t, protocol.EnrollmentPollPath, request.URL.Path)
		return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{
			Status:       protocol.EnrollmentStatusApproved,
			CredentialID: "credential-final-poll",
			AccessToken:  pending.DeviceCode,
			TokenType:    protocol.DPoPAuthorizationScheme,
			RunnerID:     "runner-final-poll",
			Fingerprint:  pending.Fingerprint,
		}, nil), nil
	})}

	result, err := enrollRunner(t.Context(), EnrollmentConfig{
		Server:     server,
		Workspace:  workspace,
		Store:      store,
		HTTPClient: httpClient,
	}, testEnrollmentDependencies(clock, 0x74))
	require.NoError(t, err)
	assert.Equal(t, "credential-final-poll", result.CredentialID)
	assert.Equal(t, 1, requests)
	assert.Equal(t, []time.Duration{2 * time.Second}, clock.sleeps)
	_, found, err := store.LoadPendingEnrollment(server, workspace)
	require.NoError(t, err)
	assert.False(t, found)
	credential, found, err := store.LoadCredential(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, pending.PrivateKey, credential.PrivateKey)
}

func TestEnrollRunnerKeepsPendingStateWhenServerStillPendingAtLocalExpiry(t *testing.T) {
	workspace := t.TempDir()
	server := "http://localhost:9394"
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	clock := &enrollmentFakeClock{current: time.Date(2030, time.June, 7, 8, 9, 10, 0, time.UTC)}
	pending := saveEnrollmentTestPending(t, store, server, workspace, clock.current, 5000, 0x75)
	pending.ExpiresAt = clock.current.Add(2 * time.Second)
	require.NoError(t, store.SavePendingEnrollment(pending))
	var requests int
	httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return enrollmentJSONResponse(t, http.StatusOK, protocol.EnrollmentPollResponse{Status: protocol.EnrollmentStatusPending, RetryAfterMS: 5000}, nil), nil
	})}

	_, err = enrollRunner(t.Context(), EnrollmentConfig{Server: server, Workspace: workspace, Store: store, HTTPClient: httpClient}, testEnrollmentDependencies(clock, 0x76))
	require.ErrorIs(t, err, ErrEnrollmentExpired)
	assert.Equal(t, 1, requests)
	_, found, err := store.LoadPendingEnrollment(server, workspace)
	require.NoError(t, err)
	assert.True(t, found)
}

func TestEnrollRunnerContextCancellationKeepsPendingState(t *testing.T) {
	workspace := t.TempDir()
	server := "http://localhost:9494"
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	clock := &enrollmentFakeClock{current: time.Date(2030, time.July, 8, 9, 10, 11, 0, time.UTC)}
	saveEnrollmentTestPending(t, store, server, workspace, clock.current, 5000, 0x75)
	ctx, cancel := context.WithCancel(t.Context())
	deps := testEnrollmentDependencies(clock, 0x76)
	deps.sleep = func(ctx context.Context, duration time.Duration) error {
		clock.sleeps = append(clock.sleeps, duration)
		cancel()
		return ctx.Err()
	}
	var requests int

	_, err = enrollRunner(ctx, EnrollmentConfig{
		Server:    server,
		Workspace: workspace,
		Store:     store,
		HTTPClient: &http.Client{Transport: enrollmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, io.ErrUnexpectedEOF
		})},
	}, deps)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, requests)
	assert.Equal(t, []time.Duration{5 * time.Second}, clock.sleeps)
	_, found, err := store.LoadPendingEnrollment(server, workspace)
	require.NoError(t, err)
	assert.True(t, found)
}

func TestEnrollRunnerRejectsInvalidApprovalIdentity(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*protocol.EnrollmentPollResponse)
		wantError string
	}{
		{name: "missing credential id", mutate: func(response *protocol.EnrollmentPollResponse) { response.CredentialID = "" }, wantError: "credential id"},
		{name: "missing runner id", mutate: func(response *protocol.EnrollmentPollResponse) { response.RunnerID = "" }, wantError: "runner id"},
		{name: "mismatched fingerprint", mutate: func(response *protocol.EnrollmentPollResponse) { response.Fingerprint = "sha256:not-the-generated-key" }, wantError: "does not match"},
		{name: "missing access token", mutate: func(response *protocol.EnrollmentPollResponse) { response.AccessToken = "" }, wantError: "access token"},
		{name: "mismatched access token", mutate: func(response *protocol.EnrollmentPollResponse) { response.AccessToken = testEnrollmentAccessToken(t) }, wantError: "does not match"},
		{name: "wrong token type", mutate: func(response *protocol.EnrollmentPollResponse) { response.TokenType = "Bearer" }, wantError: "token type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			server := "http://localhost:9595"
			store, err := localstate.NewStoreAt(t.TempDir())
			require.NoError(t, err)
			clock := &enrollmentFakeClock{current: time.Date(2030, time.August, 9, 10, 11, 12, 0, time.UTC)}
			pending := saveEnrollmentTestPending(t, store, server, workspace, clock.current, 0, 0x81)
			response := protocol.EnrollmentPollResponse{
				Status:       protocol.EnrollmentStatusApproved,
				CredentialID: "credential-valid",
				AccessToken:  pending.DeviceCode,
				TokenType:    protocol.DPoPAuthorizationScheme,
				RunnerID:     "runner-valid",
				Fingerprint:  pending.Fingerprint,
			}
			test.mutate(&response)
			httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return enrollmentJSONResponse(t, http.StatusOK, response, nil), nil
			})}

			_, err = enrollRunner(t.Context(), EnrollmentConfig{
				Server:     server,
				Workspace:  workspace,
				Store:      store,
				HTTPClient: httpClient,
			}, testEnrollmentDependencies(clock, 0x82))
			require.ErrorContains(t, err, test.wantError)
			_, found, loadErr := store.LoadCredential(server, workspace)
			require.NoError(t, loadErr)
			assert.False(t, found)
			_, found, loadErr = store.LoadRegistration(server, workspace)
			require.NoError(t, loadErr)
			assert.False(t, found)
			_, found, loadErr = store.LoadPendingEnrollment(server, workspace)
			require.NoError(t, loadErr)
			assert.True(t, found)
		})
	}
}

func TestEnrollmentAPIErrorsAreUsefulAndRedacted(t *testing.T) {
	t.Run("start redacts private key", func(t *testing.T) {
		workspace := t.TempDir()
		server := "http://localhost:9696"
		store, err := localstate.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &enrollmentFakeClock{current: time.Date(2030, time.September, 10, 11, 12, 13, 0, time.UTC)}
		seed := bytes.Repeat([]byte{0x92}, ed25519.SeedSize)
		privateKeySecret := base64.RawURLEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed))
		body, err := json.Marshal(map[string]string{"error": "temporarily unavailable; echoed private key " + privateKeySecret})
		require.NoError(t, err)
		var requests int
		httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			assert.Equal(t, protocol.EnrollmentStartPath, request.URL.Path)
			return enrollmentRawResponse(http.StatusServiceUnavailable, body, nil), nil
		})}

		_, err = enrollRunner(t.Context(), EnrollmentConfig{
			Server:     server,
			Workspace:  workspace,
			Store:      store,
			HTTPClient: httpClient,
		}, testEnrollmentDependencies(clock, 0x92))
		var apiErr *EnrollmentAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
		assert.Equal(t, "start", apiErr.Operation)
		assert.Contains(t, apiErr.Message, "temporarily unavailable")
		assert.Contains(t, apiErr.Message, "[REDACTED]")
		assert.NotContains(t, err.Error(), privateKeySecret)
		assert.Equal(t, 1, requests)
		_, found, loadErr := store.LoadPendingEnrollment(server, workspace)
		require.NoError(t, loadErr)
		assert.False(t, found)
	})

	t.Run("poll redacts device code and private key", func(t *testing.T) {
		workspace := t.TempDir()
		server := "http://localhost:9797"
		store, err := localstate.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		clock := &enrollmentFakeClock{current: time.Date(2030, time.October, 11, 12, 13, 14, 0, time.UTC)}
		pending := saveEnrollmentTestPending(t, store, server, workspace, clock.current, 0, 0x93)
		privateKeySecret := base64.RawURLEncoding.EncodeToString(pending.PrivateKey)
		body, err := json.Marshal(map[string]string{
			"error": "invalid enrollment for device " + pending.DeviceCode + " and private key " + privateKeySecret,
		})
		require.NoError(t, err)
		httpClient := &http.Client{Transport: enrollmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return enrollmentRawResponse(http.StatusBadRequest, body, nil), nil
		})}

		_, err = enrollRunner(t.Context(), EnrollmentConfig{
			Server:     server,
			Workspace:  workspace,
			Store:      store,
			HTTPClient: httpClient,
		}, testEnrollmentDependencies(clock, 0x94))
		var apiErr *EnrollmentAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, "poll", apiErr.Operation)
		assert.Contains(t, apiErr.Message, "invalid enrollment")
		assert.NotContains(t, err.Error(), pending.DeviceCode)
		assert.NotContains(t, err.Error(), privateKeySecret)
		assert.Equal(t, 2, strings.Count(apiErr.Message, "[REDACTED]"))
		_, found, loadErr := store.LoadPendingEnrollment(server, workspace)
		require.NoError(t, loadErr)
		assert.True(t, found)
	})
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2030, time.November, 12, 13, 14, 15, 0, time.UTC)
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

func testEnrollmentDependencies(clock *enrollmentFakeClock, randomByte byte) enrollmentDependencies {
	return enrollmentDependencies{
		now:            clock.now,
		sleep:          clock.sleep,
		random:         bytes.NewReader(bytes.Repeat([]byte{randomByte}, 128)),
		hostname:       func() (string, error) { return "host.example.test", nil },
		kodeletVersion: func() string { return "v-test" },
	}
}

func saveEnrollmentTestPending(t *testing.T, store *localstate.Store, server, workspace string, now time.Time, pollIntervalMS int64, discriminator byte) localstate.PendingEnrollment {
	t.Helper()
	publicKey, privateKey, fingerprint := enrollmentTestKeyPair(t, discriminator)
	pending := localstate.PendingEnrollment{
		Server:                  server,
		Workspace:               workspace,
		EnrollmentID:            "enrollment-persisted",
		DeviceCode:              testEnrollmentAccessToken(t),
		UserCode:                "PEND-ING1",
		VerificationURL:         "https://control.example/runner/enroll",
		VerificationURLComplete: "https://control.example/runner/enroll?user_code=PEND-ING1",
		Fingerprint:             fingerprint,
		PublicKey:               publicKey,
		PrivateKey:              privateKey,
		ExpiresAt:               now.Add(10 * time.Minute),
		PollIntervalMS:          pollIntervalMS,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	require.NoError(t, store.SavePendingEnrollment(pending))
	loaded, found, err := store.LoadPendingEnrollment(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	return loaded
}

func saveEnrollmentTestCredential(t *testing.T, store *localstate.Store, server, workspace, credentialID string, discriminator byte) localstate.Credential {
	t.Helper()
	publicKey, privateKey, fingerprint := enrollmentTestKeyPair(t, discriminator)
	credential := localstate.Credential{
		Server:       server,
		Workspace:    workspace,
		CredentialID: credentialID,
		AccessToken:  testEnrollmentAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}
	require.NoError(t, store.SaveCredential(credential))
	loaded, found, err := store.LoadCredential(server, workspace)
	require.NoError(t, err)
	require.True(t, found)
	return loaded
}

func testEnrollmentAccessToken(t *testing.T) string {
	t.Helper()
	token, err := protocol.NewRunnerAccessToken()
	require.NoError(t, err)
	return token
}

func enrollmentTestKeyPair(t *testing.T, discriminator byte) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	seed := bytes.Repeat([]byte{discriminator}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	require.NoError(t, err)
	return publicKey, privateKey, fingerprint
}

func decodeEnrollmentJSON[T any](t *testing.T, reader io.Reader) T {
	t.Helper()
	var value T
	require.NoError(t, json.NewDecoder(reader).Decode(&value))
	return value
}

func enrollmentJSONResponse(t *testing.T, statusCode int, value any, header http.Header) *http.Response {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return enrollmentRawResponse(statusCode, payload, header)
}

func enrollmentRawResponse(statusCode int, body []byte, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}
