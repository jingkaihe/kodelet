package controlplane

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerWebsocketKeyProofAuthenticationAndReplayProtection(t *testing.T) {
	server, store, clock, privateKey, accessToken := newRunnerAuthIntegrationServer(t, RunnerAuthModeEnrollment)
	httpServer := httptest.NewServer(server.router)
	t.Cleanup(httpServer.Close)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + protocol.Endpoint

	headers := runnerDPoPTestHeaders(t, wsURL, accessToken, privateKey, "proof-success", clock.current)
	peer := dialRunnerPeer(t, wsURL, headers)
	var registration protocol.RegisterResult
	require.NoError(t, peer.Call(t.Context(), protocol.MethodRunnerRegister, testRunnerAuthRegisterParams("runner-proof", "host-proof", "/work/proof"), &registration))
	assert.Equal(t, "runner-proof", registration.RunnerID)
	require.NoError(t, peer.Close())

	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	_, response, err := dialer.Dial(wsURL, headers)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())

	_, wrongPrivateKey := testEd25519KeyPair(0x62)
	_, response, err = dialer.Dial(wsURL, runnerDPoPTestHeaders(t, wsURL, accessToken, wrongPrivateKey, "proof-wrong-key", clock.current))
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())

	peer = dialRunnerPeer(t, wsURL, runnerDPoPTestHeaders(t, wsURL, accessToken, privateKey, "proof-mismatched-registration", clock.current))
	mismatched := testRunnerAuthRegisterParams("runner-other", "host-proof", "/work/proof")
	err = peer.Call(t.Context(), protocol.MethodRunnerRegister, mismatched, &registration)
	require.ErrorContains(t, err, "runner id does not match its enrolled credential")
	require.NoError(t, peer.Close())

	_, response, err = dialer.Dial(wsURL, runnerDPoPTestHeaders(t, wsURL, accessToken, privateKey, "proof-expired", clock.current.Add(-defaultRunnerDPoPProofMaxAge-time.Second)))
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())

	var lastUsedAt time.Time
	require.NoError(t, store.db.GetContext(t.Context(), &lastUsedAt, `SELECT last_used_at FROM runner_credentials WHERE id = ?`, "credential-proof"))
	assert.Equal(t, time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC), lastUsedAt.UTC())
}

func TestTokenRunnerCanRegisterIdentityWithStoredEnrollmentCredential(t *testing.T) {
	server, _, _, _, _ := newRunnerAuthIntegrationServer(t, RunnerAuthModeToken)
	httpServer := httptest.NewServer(server.router)
	t.Cleanup(httpServer.Close)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + protocol.Endpoint

	peer := dialRunnerPeer(t, wsURL, http.Header{"Authorization": []string{"Bearer legacy-runner-token"}})
	var registration protocol.RegisterResult
	require.NoError(t, peer.Call(t.Context(), protocol.MethodRunnerRegister, testRunnerAuthRegisterParams("runner-proof", "host-proof", "/work/proof"), &registration))
	assert.Equal(t, "runner-proof", registration.RunnerID)
	require.NoError(t, peer.Close())

	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	_, response, err := dialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer wrong-token"}})
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestPublicRunnerAuthEndpointsHideStoreErrors(t *testing.T) {
	server := &Server{
		config:    &ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "web-token", RunnerAuthMode: RunnerAuthModeEnrollment},
		authStore: &authStore{},
	}

	request := testEnrollmentRequest(t, "host-public", "/work/public", "public", 0x71)
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	server.handleStartRunnerEnrollment(recorder, httptest.NewRequest(http.MethodPost, "https://kodelet.example"+protocol.EnrollmentStartPath, strings.NewReader(string(payload))))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "authentication store is closed")
	assert.Contains(t, recorder.Body.String(), "failed to start runner enrollment")

	payload, err = json.Marshal(protocol.EnrollmentPollRequest{EnrollmentID: "enrollment-public", DeviceCode: "device-public"})
	require.NoError(t, err)
	recorder = httptest.NewRecorder()
	server.handlePollRunnerEnrollment(recorder, httptest.NewRequest(http.MethodPost, "https://kodelet.example"+protocol.EnrollmentPollPath, strings.NewReader(string(payload))))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "authentication store is closed")
	assert.Contains(t, recorder.Body.String(), "failed to poll runner enrollment")
}

func TestStartRunnerEnrollmentUsesForwardedExternalURL(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	server := &Server{
		config:    &ServerConfig{RunnerAuthMode: RunnerAuthModeEnrollment},
		authStore: store,
	}
	payload, err := json.Marshal(testEnrollmentRequest(t, "host-forwarded", "/work/forwarded", "forwarded", 0x74))
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "http://runner-backend.internal"+protocol.EnrollmentStartPath, strings.NewReader(string(payload)))
	request.Header.Set("X-Forwarded-Proto", "https, http")
	request.Header.Set("X-Forwarded-Host", "kodelet.example:8443")
	request.Header.Set("X-Forwarded-Prefix", "/control")
	response := httptest.NewRecorder()

	server.handleStartRunnerEnrollment(response, request)

	require.Equal(t, http.StatusCreated, response.Code)
	var started protocol.EnrollmentStartResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &started))
	assert.Equal(t, "https://kodelet.example:8443/control/runner/enroll", started.VerificationURL)
}

func TestRunnerEnrollmentDecisionRequiresSameOrigin(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		referer     string
		forward     string
		forwardHost string
		want        bool
	}{
		{name: "matching origin", origin: "https://kodelet.example", want: true},
		{name: "matching referer", referer: "https://kodelet.example/runner/enroll?user_code=ABCD-EFGH", want: true},
		{name: "forwarded https", origin: "https://kodelet.example", forward: "https", want: true},
		{name: "forwarded https chain with default port", origin: "https://kodelet.example", forward: "https, http", forwardHost: "kodelet.example:443", want: true},
		{name: "forwarded development host", origin: "http://localhost:3000", forward: "http", forwardHost: "localhost:3000", want: true},
		{name: "missing source", want: false},
		{name: "other port", origin: "https://kodelet.example:8443", want: false},
		{name: "sibling domain", origin: "https://attacker.example", want: false},
		{name: "opaque origin", origin: "null", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := "https"
			if test.forward != "" {
				scheme = "http"
			}
			request := httptest.NewRequest(http.MethodPost, scheme+"://kodelet.example/runner/enroll", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Referer", test.referer)
			request.Header.Set("X-Forwarded-Host", test.forwardHost)
			if test.forward != "" {
				request.Header.Set("X-Forwarded-Proto", test.forward)
			}
			assert.Equal(t, test.want, sameOriginEnrollmentDecision(request))
		})
	}
}

func TestRunnerEnrollmentPageServesSPAAndDecisionAPIRequiresManualCodeEntry(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	started, err := store.StartRunnerEnrollment(
		t.Context(),
		testEnrollmentRequest(t, "host-manual", "/work/manual", "manual", 0x72),
		"https://kodelet.example/runner/enroll",
	)
	require.NoError(t, err)
	assert.Empty(t, started.VerificationURLComplete)

	server := &Server{
		router: mux.NewRouter(),
		config: &ServerConfig{
			WebAuthMode:    WebAuthModeToken,
			AuthToken:      "web-token",
			RunnerAuthMode: RunnerAuthModeEnrollment,
		},
		authStore:       store,
		frontendHandler: testFrontendHandler(),
	}
	server.setupRoutes()
	principal := administrativePrincipal("manual-entry-test")

	pageRequest := httptest.NewRequest(http.MethodGet, "https://kodelet.example/runner/enroll?user_code="+url.QueryEscape(started.UserCode), nil)
	pageRequest = pageRequest.WithContext(contextWithPrincipal(pageRequest.Context(), principal))
	pageResponse := httptest.NewRecorder()
	server.handleRunnerEnrollmentPage(pageResponse, pageRequest)
	require.Equal(t, http.StatusOK, pageResponse.Code)
	assert.Contains(t, pageResponse.Header().Get("Cache-Control"), "no-store")
	assert.Equal(t, "frame-ancestors 'none'", pageResponse.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "DENY", pageResponse.Header().Get("X-Frame-Options"))
	assert.Contains(t, pageResponse.Body.String(), `<div id="root"></div>`)
	assert.NotContains(t, pageResponse.Body.String(), started.UserCode)

	headRequest := httptest.NewRequest(http.MethodHead, "https://kodelet.example/runner/enroll", nil)
	headRequest.AddCookie(&http.Cookie{Name: webUIAuthCookieName, Value: "web-token"})
	headResponse := httptest.NewRecorder()
	server.router.ServeHTTP(headResponse, headRequest)
	assert.Equal(t, http.StatusOK, headResponse.Code)
	assert.Equal(t, "frame-ancestors 'none'", headResponse.Header().Get("Content-Security-Policy"))

	contextRequest := httptest.NewRequest(http.MethodGet, "https://kodelet.example/api/runner/v1/enrollment/context", nil)
	contextRequest.AddCookie(&http.Cookie{Name: webUIAuthCookieName, Value: "web-token"})
	contextResponse := httptest.NewRecorder()
	server.router.ServeHTTP(contextResponse, contextRequest)
	require.Equal(t, http.StatusOK, contextResponse.Code)
	assert.Equal(t, "no-store", contextResponse.Header().Get("Cache-Control"))
	var contextPrincipal Principal
	require.NoError(t, json.Unmarshal(contextResponse.Body.Bytes(), &contextPrincipal))
	assert.Equal(t, "token", contextPrincipal.ID)

	payload, err := json.Marshal(runnerEnrollmentDecisionRequest{UserCode: started.UserCode, Decision: "lookup"})
	require.NoError(t, err)
	lookupRequest := httptest.NewRequest(http.MethodPost, "https://kodelet.example/api/runner/v1/enrollment/decision", strings.NewReader(string(payload)))
	lookupRequest.Header.Set("Content-Type", "application/json")
	lookupRequest.Header.Set("Origin", "https://kodelet.example")
	lookupRequest = lookupRequest.WithContext(contextWithPrincipal(lookupRequest.Context(), principal))
	lookupResponse := httptest.NewRecorder()
	server.handleRunnerEnrollmentDecision(lookupResponse, lookupRequest)
	require.Equal(t, http.StatusOK, lookupResponse.Code)
	assert.Equal(t, "no-store", lookupResponse.Header().Get("Cache-Control"))
	var response runnerEnrollmentDecisionResponse
	require.NoError(t, json.Unmarshal(lookupResponse.Body.Bytes(), &response))
	require.NotNil(t, response.Enrollment)
	assert.Equal(t, protocol.EnrollmentStatusPending, response.Status)
	assert.Equal(t, started.UserCode, response.Enrollment.UserCode)
	assert.Equal(t, "host.example.test", response.Enrollment.Host.Hostname)
	assert.Equal(t, "/work/manual", response.Enrollment.Workspace.Path)
	assert.NotEmpty(t, response.Enrollment.Fingerprint)
	assert.NotContains(t, lookupResponse.Body.String(), started.DeviceCode)
	assert.NotContains(t, lookupResponse.Body.String(), "host-manual")
}

func TestRunnerEnrollmentDecisionRequiresWebSessionCSRFAndStrictJSON(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	started, err := store.StartRunnerEnrollment(
		t.Context(),
		testEnrollmentRequest(t, "host-csrf", "/work/csrf", "csrf", 0x73),
		"https://kodelet.example/runner/enroll",
	)
	require.NoError(t, err)
	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), "https://issuer.example.com", "subject-runner-admin", "Runner Admin", "runner-admin@example.com", []string{string(RoleRunnerAdmin)}, time.Hour)
	require.NoError(t, err)
	session, found, err := store.LoadWebSession(t.Context(), sessionToken)
	require.NoError(t, err)
	require.True(t, found)
	principal := principalFromWebSession(session)
	server := &Server{
		config:    &ServerConfig{RunnerAuthMode: RunnerAuthModeEnrollment},
		authStore: store,
	}

	performDecision := func(payload any, cookieCSRF string) *httptest.ResponseRecorder {
		body, marshalErr := json.Marshal(payload)
		require.NoError(t, marshalErr)
		request := httptest.NewRequest(http.MethodPost, "https://kodelet.example/api/runner/v1/enrollment/decision", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://kodelet.example")
		request.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: cookieCSRF})
		request = request.WithContext(contextWithPrincipal(request.Context(), principal))
		response := httptest.NewRecorder()
		server.handleRunnerEnrollmentDecision(response, request)
		return response
	}

	wrongCSRF := performDecision(runnerEnrollmentDecisionRequest{UserCode: started.UserCode, Decision: "lookup", CSRFToken: "wrong-token"}, csrfToken)
	assert.Equal(t, http.StatusForbidden, wrongCSRF.Code)

	unknownField := performDecision(map[string]any{
		"userCode":   started.UserCode,
		"decision":   "lookup",
		"csrfToken":  csrfToken,
		"unexpected": true,
	}, csrfToken)
	assert.Equal(t, http.StatusBadRequest, unknownField.Code)

	valid := performDecision(runnerEnrollmentDecisionRequest{UserCode: started.UserCode, Decision: "lookup", CSRFToken: csrfToken}, csrfToken)
	assert.Equal(t, http.StatusOK, valid.Code)
	assert.Equal(t, "no-store", valid.Header().Get("Cache-Control"))
}

func TestExpiredEnrollmentApprovalDoesNotPublishOrPersistRunner(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	started, err := store.StartRunnerEnrollment(
		t.Context(),
		testEnrollmentRequest(t, "host-expired-approval", "/work/expired-approval", "expired-approval", 0x75),
		"https://kodelet.example/runner/enroll",
	)
	require.NoError(t, err)
	var sequence int
	var name, dbPath string
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &dbPath))
	persistence, err := runnerregistry.NewSQLitePersistence(t.Context(), dbPath, controlPlaneOwnerID)
	require.NoError(t, err)
	registry, err := runnerregistry.New(t.Context(), runnerregistry.Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		Persistence:       persistence,
		Credentials:       store,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	server := &Server{
		config:         &ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "web-token", RunnerAuthMode: RunnerAuthModeEnrollment},
		authStore:      store,
		runnerRegistry: registry,
	}
	clock.current = started.ExpiresAt
	payload, err := json.Marshal(runnerEnrollmentDecisionRequest{UserCode: started.UserCode, Decision: "approve"})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "https://kodelet.example/api/runner/v1/enrollment/decision", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://kodelet.example")
	request = request.WithContext(contextWithPrincipal(request.Context(), administrativePrincipal("token")))
	response := httptest.NewRecorder()

	server.handleRunnerEnrollmentDecision(response, request)

	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Empty(t, registry.Runners())
	var runnerCount int
	require.NoError(t, store.db.GetContext(t.Context(), &runnerCount, `SELECT COUNT(*) FROM runner_registrations WHERE host_instance_id = ? AND workspace_path = ?`, "host-expired-approval", "/work/expired-approval"))
	assert.Zero(t, runnerCount)
}

func TestRunnerEnrollmentConnectedConflictReturnsActionableConflict(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "https://kodelet.example/api/runner/v1/enrollment/decision", nil)
	response := httptest.NewRecorder()

	server.writeRunnerEnrollmentStoreError(response, request, runnerregistry.ErrRunnerConnected)

	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Contains(t, response.Body.String(), "currently connected")
	assert.Contains(t, response.Body.String(), "Stop it before enrolling")
}

func TestRunnerDPoPTargetURLUsesForwardedExternalAddress(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://runner-backend.internal"+protocol.Endpoint+"?ignored=true", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "kodelet.example:8443")
	request.Header.Set("X-Forwarded-Prefix", "/control")

	assert.Equal(t, "https://kodelet.example:8443/control"+protocol.Endpoint, runnerDPoPTargetURL(request))

	request.Header.Set("X-Forwarded-Proto", "wss")
	assert.Equal(t, "https://kodelet.example:8443/control"+protocol.Endpoint, runnerDPoPTargetURL(request))

	request.Header.Set("X-Forwarded-Host", "attacker.example/path")
	request.Header.Set("X-Forwarded-Prefix", "control")
	assert.Equal(t, "https://runner-backend.internal"+protocol.Endpoint, runnerDPoPTargetURL(request))
}

func TestPublicAuthRequestRateLimitIsPerDirectPeerAndEndpoint(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+protocol.EnrollmentStartPath, nil)
	request.RemoteAddr = "192.0.2.10:1234"
	assert.True(t, server.allowPublicAuthRequest(request, 2))
	assert.True(t, server.allowPublicAuthRequest(request, 2))
	assert.False(t, server.allowPublicAuthRequest(request, 2))

	otherClient := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+protocol.EnrollmentStartPath, nil)
	otherClient.RemoteAddr = "192.0.2.11:1234"
	assert.True(t, server.allowPublicAuthRequest(otherClient, 2))

	otherEndpoint := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+protocol.EnrollmentPollPath, nil)
	otherEndpoint.RemoteAddr = request.RemoteAddr
	assert.True(t, server.allowPublicAuthRequest(otherEndpoint, 2))

	spoofedForwardedPeer := httptest.NewRequest(http.MethodPost, "https://kodelet.example/auth/login", nil)
	spoofedForwardedPeer.RemoteAddr = "192.0.2.12:1234"
	spoofedForwardedPeer.Header.Set("X-Forwarded-For", "198.51.100.1")
	assert.True(t, server.allowPublicAuthRequest(spoofedForwardedPeer, 1))
	spoofedForwardedPeer = httptest.NewRequest(http.MethodPost, "https://kodelet.example/auth/login", nil)
	spoofedForwardedPeer.RemoteAddr = "192.0.2.12:5678"
	spoofedForwardedPeer.Header.Set("X-Forwarded-For", "198.51.100.2")
	assert.False(t, server.allowPublicAuthRequest(spoofedForwardedPeer, 1))
}

func TestPublicAuthRequestRateLimitTableIsBounded(t *testing.T) {
	server := &Server{}
	for index := range maxPublicAuthRateEntries + 32 {
		request := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+protocol.EnrollmentStartPath, nil)
		request.RemoteAddr = net.JoinHostPort(net.IPv4(10, byte(index>>16), byte(index>>8), byte(index)).String(), "1234")
		assert.True(t, server.allowPublicAuthRequest(request, 1))
	}
	assert.LessOrEqual(t, len(server.publicAuthRates), maxPublicAuthRateEntries)
}

func newRunnerAuthIntegrationServer(t *testing.T, mode RunnerAuthMode) (*Server, *authStore, *authStoreTestClock, ed25519.PrivateKey, string) {
	t.Helper()
	store, clock := newAuthStoreTest(t)
	publicKey, privateKey := testEd25519KeyPair(0x61)
	insertAuthStoreTestRunner(t, store, "runner-proof", "host-proof", "/work/proof", "proof")
	accessToken := insertAuthStoreTestCredential(t, store, "credential-proof", "runner-proof", publicKey, clock.current)

	var sequence int
	var name, dbPath string
	require.NoError(t, store.db.QueryRowxContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &dbPath))
	persistence, err := runnerregistry.NewSQLitePersistence(context.Background(), dbPath, controlPlaneOwnerID)
	require.NoError(t, err)
	registry, err := runnerregistry.New(context.Background(), runnerregistry.Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		Persistence:       persistence,
		Credentials:       store,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })

	config := &ServerConfig{
		WebAuthMode:    WebAuthModeToken,
		AuthToken:      "web-token",
		RunnerAuthMode: mode,
	}
	if mode == RunnerAuthModeToken {
		config.RunnerAuthToken = "legacy-runner-token"
	}
	server := &Server{
		router:                mux.NewRouter(),
		config:                config,
		authStore:             store,
		runnerRegistry:        registry,
		activeChats:           make(map[string]*activeChatRun),
		chatSubscribers:       make(map[string]map[*subscriberEventSink]struct{}),
		pendingChatStops:      make(map[string]time.Time),
		deletingConversations: make(map[string]struct{}),
	}
	server.setupRoutes()
	return server, store, clock, privateKey, accessToken
}

func runnerDPoPTestHeaders(t *testing.T, targetURL, accessToken string, privateKey ed25519.PrivateKey, jti string, issuedAt time.Time) http.Header {
	t.Helper()
	proof, err := protocol.SignDPoPProof(privateKey, protocol.DPoPProofOptions{
		Method:      http.MethodGet,
		TargetURL:   targetURL,
		AccessToken: accessToken,
		JTI:         jti,
		IssuedAt:    issuedAt,
	})
	require.NoError(t, err)
	return http.Header{
		"Authorization":     []string{protocol.DPoPAuthorizationScheme + " " + accessToken},
		protocol.DPoPHeader: []string{proof},
	}
}

func testRunnerAuthRegisterParams(runnerID, hostInstanceID, workspacePath string) protocol.RegisterParams {
	return protocol.RegisterParams{
		RunnerID:         runnerID,
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{ConcurrentRuns: true},
		Host: protocol.Host{
			InstanceID: hostInstanceID,
			Hostname:   "host",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace:      protocol.Workspace{Path: workspacePath, Name: "proof"},
		KodeletVersion: "v-test",
	}
}
