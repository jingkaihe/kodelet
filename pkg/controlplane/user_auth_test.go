package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserLoginHTTPApprovalLifecycleAndTrustedVerificationURL(t *testing.T) {
	store, clock := newAuthStoreTest(t)
	config := testUserAuthServerConfig()
	config.OIDC.SessionDuration = 90 * time.Minute
	config.OIDC.RedirectURL = "https://trusted.example:8443/base/oidc/callback?tenant=one"
	server := newUserAuthRouteServer(config, store)

	started := startUserLoginHTTP(t, server, "https://attacker.example", testUserLoginStartRequest())
	assert.Equal(t, "https://trusted.example:8443"+userauth.DeviceVerificationPath, started.VerificationURL)
	assert.Empty(t, started.VerificationURLComplete)

	unauthenticated := httptest.NewRequest(http.MethodGet, "https://trusted.example:8443"+userauth.DeviceVerificationPath+"?user_code="+url.QueryEscape(started.UserCode), nil)
	unauthenticatedResponse := serveUserAuthRequest(server, unauthenticated)
	assert.Equal(t, http.StatusFound, unauthenticatedResponse.Code)
	assert.Contains(t, unauthenticatedResponse.Header().Get("Location"), "/auth/login?return_to=")

	sessionToken, csrfToken, err := store.CreateWebSession(
		t.Context(),
		"https://issuer.example.com",
		"subject-user",
		"Test User",
		"user@example.com",
		[]string{string(RoleUser), string(RoleTerminal)},
		time.Hour,
	)
	require.NoError(t, err)

	pageRequest := httptest.NewRequest(http.MethodGet, "https://trusted.example:8443"+userauth.DeviceVerificationPath+"?user_code="+url.QueryEscape(started.UserCode), nil)
	addUserAuthSessionCookies(pageRequest, sessionToken, csrfToken)
	pageResponse := serveUserAuthRequest(server, pageRequest)
	require.Equal(t, http.StatusOK, pageResponse.Code)
	assert.Contains(t, pageResponse.Header().Get("Cache-Control"), "no-store")
	assert.Equal(t, "frame-ancestors 'none'", pageResponse.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "DENY", pageResponse.Header().Get("X-Frame-Options"))
	assert.Contains(t, pageResponse.Body.String(), `<div id="root"></div>`)
	assert.NotContains(t, pageResponse.Body.String(), started.UserCode)
	assert.NotContains(t, pageResponse.Body.String(), started.DeviceCode)
	assert.NotContains(t, pageResponse.Body.String(), started.BearerToken)

	headRequest := httptest.NewRequest(http.MethodHead, "https://trusted.example:8443"+userauth.DeviceVerificationPath, nil)
	addUserAuthSessionCookies(headRequest, sessionToken, csrfToken)
	headResponse := serveUserAuthRequest(server, headRequest)
	assert.Equal(t, http.StatusOK, headResponse.Code)
	assert.Equal(t, "frame-ancestors 'none'", headResponse.Header().Get("Content-Security-Policy"))

	contextRequest := httptest.NewRequest(http.MethodGet, "https://trusted.example:8443/api/auth/v1/device/context", nil)
	addUserAuthSessionCookies(contextRequest, sessionToken, csrfToken)
	contextResponse := serveUserAuthRequest(server, contextRequest)
	require.Equal(t, http.StatusOK, contextResponse.Code)
	assert.Equal(t, "no-store", contextResponse.Header().Get("Cache-Control"))
	var contextPrincipal Principal
	require.NoError(t, json.Unmarshal(contextResponse.Body.Bytes(), &contextPrincipal))
	assert.Equal(t, "user@example.com", contextPrincipal.Email)

	nonSessionContextRequest := httptest.NewRequest(http.MethodGet, "https://trusted.example:8443/api/auth/v1/device/context", nil)
	nonSessionContextRequest = nonSessionContextRequest.WithContext(contextWithPrincipal(nonSessionContextRequest.Context(), administrativePrincipal("token")))
	nonSessionContextResponse := httptest.NewRecorder()
	server.handleUserLoginContext(nonSessionContextResponse, nonSessionContextRequest)
	assert.Equal(t, http.StatusForbidden, nonSessionContextResponse.Code)

	wrongOriginResponse := performUserLoginDecision(t, server, started.UserCode, "lookup", sessionToken, csrfToken, csrfToken, "https://attacker.example", "")
	assert.Equal(t, http.StatusForbidden, wrongOriginResponse.Code)
	pending, err := store.UserLoginByUserCode(t.Context(), started.UserCode)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusPending, pending.Status)

	wrongCSRFResponse := performUserLoginDecision(t, server, started.UserCode, "lookup", sessionToken, csrfToken, "wrong-csrf-token", "https://trusted.example:8443", "")
	assert.Equal(t, http.StatusForbidden, wrongCSRFResponse.Code)
	pending, err = store.UserLoginByUserCode(t.Context(), started.UserCode)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusPending, pending.Status)

	lookupResponse := performUserLoginDecision(t, server, started.UserCode, "lookup", sessionToken, csrfToken, csrfToken, "https://trusted.example:8443", "")
	require.Equal(t, http.StatusOK, lookupResponse.Code)
	assert.Equal(t, "no-store", lookupResponse.Header().Get("Cache-Control"))
	var lookup userLoginDecisionResponse
	require.NoError(t, json.Unmarshal(lookupResponse.Body.Bytes(), &lookup))
	require.NotNil(t, lookup.Authorization)
	assert.Equal(t, userauth.DeviceStatusPending, lookup.Status)
	assert.Equal(t, started.UserCode, lookup.Authorization.UserCode)
	assert.Equal(t, "kodelet", lookup.Authorization.ClientName)
	assert.Equal(t, "linux", lookup.Authorization.ClientOS)
	assert.Equal(t, "amd64", lookup.Authorization.ClientArch)
	assert.Equal(t, "v-test", lookup.Authorization.KodeletVersion)
	assert.NotContains(t, lookupResponse.Body.String(), started.DeviceCode)
	assert.NotContains(t, lookupResponse.Body.String(), started.BearerToken)

	approvedResponse := performUserLoginDecision(t, server, started.UserCode, "approve", sessionToken, csrfToken, csrfToken, "https://trusted.example:8443", "")
	require.Equal(t, http.StatusOK, approvedResponse.Code)
	var approved userLoginDecisionResponse
	require.NoError(t, json.Unmarshal(approvedResponse.Body.Bytes(), &approved))
	assert.Equal(t, userauth.DeviceStatusApproved, approved.Status)
	assert.Contains(t, approved.Message, "Sign-in approved")
	assert.Equal(t, "no-store", approvedResponse.Header().Get("Cache-Control"))

	polled := pollUserLoginHTTP(t, server, started)
	assert.Equal(t, userauth.DeviceStatusApproved, polled.Status)
	assert.NotEmpty(t, polled.CredentialID)
	assert.Equal(t, userauth.PrincipalSnapshot{
		ID:      "https://issuer.example.com|subject-user",
		Issuer:  "https://issuer.example.com",
		Subject: "subject-user",
		Name:    "Test User",
		Email:   "user@example.com",
		Roles:   []string{string(RoleUser), string(RoleTerminal)},
	}, polled.Principal)
	assert.Equal(t, clock.current.Add(config.OIDC.SessionDuration), polled.ExpiresAt)

	conflictResponse := performUserLoginDecision(t, server, started.UserCode, "approve", sessionToken, csrfToken, csrfToken, "https://trusted.example:8443", "")
	assert.Equal(t, http.StatusConflict, conflictResponse.Code)
	assert.Contains(t, conflictResponse.Body.String(), "no longer pending")
}

func TestUserLoginHTTPDenialLifecycle(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	config := testUserAuthServerConfig()
	server := newUserAuthRouteServer(config, store)
	started := startUserLoginHTTP(t, server, "https://kodelet.example", testUserLoginStartRequest())
	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), "https://issuer.example.com", "subject-denier", "Denier", "denier@example.com", []string{string(RoleUser)}, time.Hour)
	require.NoError(t, err)

	deniedResponse := performUserLoginDecision(t, server, started.UserCode, "deny", sessionToken, csrfToken, csrfToken, "https://kodelet.example", "")
	require.Equal(t, http.StatusOK, deniedResponse.Code)
	var denied userLoginDecisionResponse
	require.NoError(t, json.Unmarshal(deniedResponse.Body.Bytes(), &denied))
	assert.Equal(t, userauth.DeviceStatusDenied, denied.Status)
	assert.Contains(t, denied.Message, "Sign-in denied")

	polled := pollUserLoginHTTP(t, server, started)
	assert.Equal(t, userauth.DeviceStatusDenied, polled.Status)
	assert.Empty(t, polled.CredentialID)
	assert.Empty(t, polled.Principal.ID)

	conflictResponse := performUserLoginDecision(t, server, started.UserCode, "deny", sessionToken, csrfToken, csrfToken, "https://kodelet.example", "")
	assert.Equal(t, http.StatusConflict, conflictResponse.Code)
}

func TestUserLoginDecisionRejectsUnknownJSONAndLegacyFormPosts(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	server := newUserAuthRouteServer(testUserAuthServerConfig(), store)
	started := startUserLoginHTTP(t, server, "https://kodelet.example", testUserLoginStartRequest())
	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), "https://issuer.example.com", "subject-json", "JSON User", "json@example.com", []string{string(RoleUser)}, time.Hour)
	require.NoError(t, err)

	payload, err := json.Marshal(map[string]any{
		"userCode":   started.UserCode,
		"decision":   "lookup",
		"csrfToken":  csrfToken,
		"unexpected": true,
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "https://kodelet.example/api/auth/v1/device/decision", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://kodelet.example")
	addUserAuthSessionCookies(request, sessionToken, csrfToken)
	response := serveUserAuthRequest(server, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	legacyRequest := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DeviceVerificationPath, strings.NewReader("decision=lookup"))
	legacyRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addUserAuthSessionCookies(legacyRequest, sessionToken, csrfToken)
	legacyResponse := serveUserAuthRequest(server, legacyRequest)
	assert.Equal(t, http.StatusMethodNotAllowed, legacyResponse.Code)
	assert.Equal(t, "GET, HEAD", legacyResponse.Header().Get("Allow"))

	pending, err := store.UserLoginByUserCode(t.Context(), started.UserCode)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusPending, pending.Status)
}

func TestUserLoginHTTPStrictJSONPollingAndPublicErrors(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	server := newUserAuthRouteServer(testUserAuthServerConfig(), store)

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"clientName":"kodelet","clientOS":"linux","clientArch":"amd64","kodeletVersion":"v-test","unexpected":true}`},
		{name: "trailing JSON", body: `{"clientName":"kodelet","clientOS":"linux","clientArch":"amd64","kodeletVersion":"v-test"}{}`},
		{name: "oversized", body: `{"clientName":"` + strings.Repeat("x", maxUserLoginStartRequestBytes) + `","clientOS":"linux","clientArch":"amd64","kodeletVersion":"v-test"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DeviceStartPath, strings.NewReader(test.body))
			response := serveUserAuthRequest(server, request)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Contains(t, response.Body.String(), "invalid user login request")
		})
	}

	started := startUserLoginHTTP(t, server, "https://kodelet.example", testUserLoginStartRequest())
	firstPoll := pollUserLoginHTTPResponse(t, server, started)
	require.Equal(t, http.StatusOK, firstPoll.Code)
	var pending userauth.DevicePollResponse
	require.NoError(t, json.Unmarshal(firstPoll.Body.Bytes(), &pending))
	assert.Equal(t, userauth.DeviceStatusPending, pending.Status)
	assert.Equal(t, defaultUserPollInterval.Milliseconds(), pending.RetryAfterMS)
	assert.Equal(t, "no-store", firstPoll.Header().Get("Cache-Control"))

	slowDown := pollUserLoginHTTPResponse(t, server, started)
	assert.Equal(t, http.StatusTooManyRequests, slowDown.Code)
	assert.Equal(t, strconvForDuration(defaultUserPollInterval), slowDown.Header().Get("Retry-After"))
	assert.Equal(t, "no-store", slowDown.Header().Get("Cache-Control"))
	assert.Contains(t, slowDown.Body.String(), `"error":"slow_down"`)
	assert.NotContains(t, slowDown.Body.String(), started.DeviceCode)

	missingPayload, err := json.Marshal(userauth.DevicePollRequest{AuthorizationID: "authorization-missing", DeviceCode: "device-secret"})
	require.NoError(t, err)
	missingRequest := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DevicePollPath, bytes.NewReader(missingPayload))
	missingResponse := serveUserAuthRequest(server, missingRequest)
	assert.Equal(t, http.StatusNotFound, missingResponse.Code)
	assert.Equal(t, "no-store", missingResponse.Header().Get("Cache-Control"))
	assert.NotContains(t, missingResponse.Body.String(), "device-secret")

	invalidPollRequest := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DevicePollPath, strings.NewReader(`{"authorizationId":"one","deviceCode":"two","extra":true}`))
	invalidPollResponse := serveUserAuthRequest(server, invalidPollRequest)
	assert.Equal(t, http.StatusBadRequest, invalidPollResponse.Code)
	assert.Equal(t, "no-store", invalidPollResponse.Header().Get("Cache-Control"))

	closedStoreServer := newUserAuthRouteServer(testUserAuthServerConfig(), &authStore{})
	secretRequest := testUserLoginStartRequest()
	secretRequest.ClientName = "secret-client-name"
	payload, err := json.Marshal(secretRequest)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DeviceStartPath, bytes.NewReader(payload))
	response := serveUserAuthRequest(closedStoreServer, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), "failed to start user login")
	assert.NotContains(t, response.Body.String(), secretRequest.ClientName)
}

func TestUserLoginHTTPDisabledModesReturnNotFound(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	tests := []struct {
		name   string
		config *ServerConfig
		store  *authStore
	}{
		{name: "no auth", config: &ServerConfig{WebAuthMode: WebAuthModeNone}, store: store},
		{name: "token auth", config: &ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "compat-token"}, store: store},
		{name: "OIDC without store", config: testUserAuthServerConfig(), store: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newUserAuthRouteServer(test.config, test.store)
			startPayload, err := json.Marshal(testUserLoginStartRequest())
			require.NoError(t, err)
			startRequest := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DeviceStartPath, bytes.NewReader(startPayload))
			startResponse := serveUserAuthRequest(server, startRequest)
			assert.Equal(t, http.StatusNotFound, startResponse.Code)
			assert.Equal(t, "no-store", startResponse.Header().Get("Cache-Control"))

			pollPayload, err := json.Marshal(userauth.DevicePollRequest{AuthorizationID: "authorization", DeviceCode: "device"})
			require.NoError(t, err)
			pollRequest := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DevicePollPath, bytes.NewReader(pollPayload))
			pollResponse := serveUserAuthRequest(server, pollRequest)
			assert.Equal(t, http.StatusNotFound, pollResponse.Code)
			assert.Equal(t, "no-store", pollResponse.Header().Get("Cache-Control"))
		})
	}
}

func TestUserCredentialMiddlewareVerificationAndSelfRevocation(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	config := testUserAuthServerConfig()
	config.AuthToken = "compat-admin-token"
	server := newUserAuthRouteServer(config, store)
	started, approved := issueUserCredentialForHTTPTest(t, store, Principal{
		ID:      "https://issuer.example.com|subject-credential",
		Issuer:  "https://issuer.example.com",
		Subject: "subject-credential",
		Name:    "Credential User",
		Email:   "credential@example.com",
		Roles:   []string{string(RoleUser)},
	}, time.Hour)

	meRequest := httptest.NewRequest(http.MethodGet, "https://kodelet.example"+userauth.MePath, nil)
	meRequest.Header.Set("Authorization", "Bearer "+started.BearerToken)
	meResponse := serveUserAuthRequest(server, meRequest)
	require.Equal(t, http.StatusOK, meResponse.Code)
	assert.Equal(t, "no-store", meResponse.Header().Get("Cache-Control"))
	var snapshot userauth.PrincipalSnapshot
	require.NoError(t, json.Unmarshal(meResponse.Body.Bytes(), &snapshot))
	assert.Equal(t, approved.Principal, snapshot)
	assert.Equal(t, []string{string(RoleUser)}, snapshot.Roles)
	assert.NotContains(t, meResponse.Body.String(), "credentialId")
	assert.NotContains(t, meResponse.Body.String(), approved.CredentialID)
	assert.NotContains(t, meResponse.Body.String(), string(RoleAdmin))

	var authenticated Principal
	capture := server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated, _ = principalFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	captureRequest := httptest.NewRequest(http.MethodGet, "/api/capture", nil)
	captureRequest.Header.Set("Authorization", "Bearer "+started.BearerToken)
	captureResponse := httptest.NewRecorder()
	capture.ServeHTTP(captureResponse, captureRequest)
	require.Equal(t, http.StatusNoContent, captureResponse.Code)
	assert.Equal(t, approved.CredentialID, authenticated.CredentialID)
	assert.Equal(t, []string{string(RoleUser)}, authenticated.Roles)
	assert.Empty(t, authenticated.SessionID)

	terminalOnly := server.authMiddleware(server.requireRole(RoleTerminal, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	terminalRequest := httptest.NewRequest(http.MethodGet, "/api/terminal-only", nil)
	terminalRequest.Header.Set("Authorization", "Bearer "+started.BearerToken)
	terminalResponse := httptest.NewRecorder()
	terminalOnly.ServeHTTP(terminalResponse, terminalRequest)
	assert.Equal(t, http.StatusForbidden, terminalResponse.Code)

	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), "https://issuer.example.com", "subject-cookie", "Cookie User", "cookie@example.com", []string{string(RoleUser), string(RoleAdmin)}, time.Hour)
	require.NoError(t, err)
	unknownBearer, err := userauth.GenerateBearerToken()
	require.NoError(t, err)
	for _, test := range []struct {
		name    string
		headers []string
	}{
		{name: "wrong scheme", headers: []string{"Basic credentials"}},
		{name: "non bearer Kodelet token", headers: []string{"Token " + started.BearerToken}},
		{name: "unknown bearer", headers: []string{"Bearer " + unknownBearer}},
		{name: "multiple headers", headers: []string{"Bearer " + started.BearerToken, "Bearer " + unknownBearer}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://kodelet.example"+userauth.MePath, nil)
			request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
			for _, header := range test.headers {
				request.Header.Add("Authorization", header)
			}
			response := serveUserAuthRequest(server, request)
			assert.Equal(t, http.StatusUnauthorized, response.Code)
			assert.NotContains(t, response.Body.String(), "cookie@example.com")
		})
	}

	compatibilityMe := httptest.NewRequest(http.MethodGet, "https://kodelet.example"+userauth.MePath, nil)
	compatibilityMe.Header.Set("Authorization", "Bearer compat-admin-token")
	compatibilityMeResponse := serveUserAuthRequest(server, compatibilityMe)
	require.Equal(t, http.StatusOK, compatibilityMeResponse.Code)
	assert.Contains(t, compatibilityMeResponse.Body.String(), string(RoleAdmin))

	pending := startUserLoginHTTP(t, server, "https://kodelet.example", testUserLoginStartRequest())
	compatibilityApproval := performUserLoginDecision(t, server, pending.UserCode, "approve", "", "", "", "https://kodelet.example", "Bearer compat-admin-token")
	assert.Equal(t, http.StatusForbidden, compatibilityApproval.Code)
	pendingView, err := store.UserLoginByUserCode(t.Context(), pending.UserCode)
	require.NoError(t, err)
	assert.Equal(t, userauth.DeviceStatusPending, pendingView.Status)

	compatibilityRevoke := httptest.NewRequest(http.MethodDelete, "https://kodelet.example"+userauth.CurrentCredentialPath, nil)
	compatibilityRevoke.Header.Set("Authorization", "Bearer compat-admin-token")
	compatibilityRevokeResponse := serveUserAuthRequest(server, compatibilityRevoke)
	assert.Equal(t, http.StatusForbidden, compatibilityRevokeResponse.Code)

	cookieRevoke := httptest.NewRequest(http.MethodDelete, "https://kodelet.example"+userauth.CurrentCredentialPath, nil)
	addUserAuthSessionCookies(cookieRevoke, sessionToken, csrfToken)
	cookieRevokeResponse := serveUserAuthRequest(server, cookieRevoke)
	assert.Equal(t, http.StatusForbidden, cookieRevokeResponse.Code)
	_, err = store.LoadUserCredential(t.Context(), started.BearerToken)
	require.NoError(t, err)

	revokeRequest := httptest.NewRequest(http.MethodDelete, "https://kodelet.example"+userauth.CurrentCredentialPath, nil)
	revokeRequest.Header.Set("Authorization", "Bearer "+started.BearerToken)
	revokeResponse := serveUserAuthRequest(server, revokeRequest)
	require.Equal(t, http.StatusNoContent, revokeResponse.Code)
	assert.Equal(t, "no-store", revokeResponse.Header().Get("Cache-Control"))
	assert.Empty(t, revokeResponse.Body.String())

	_, err = store.LoadUserCredential(t.Context(), started.BearerToken)
	require.ErrorIs(t, err, errUserCredentialInvalid)
	meAfterRevoke := httptest.NewRequest(http.MethodGet, "https://kodelet.example"+userauth.MePath, nil)
	meAfterRevoke.Header.Set("Authorization", "Bearer "+started.BearerToken)
	meAfterRevokeResponse := serveUserAuthRequest(server, meAfterRevoke)
	assert.Equal(t, http.StatusUnauthorized, meAfterRevokeResponse.Code)
}

func newUserAuthRouteServer(config *ServerConfig, store *authStore) *Server {
	server := &Server{
		router:          mux.NewRouter(),
		config:          config,
		authStore:       store,
		frontendHandler: testFrontendHandler(),
	}
	server.setupRoutes()
	return server
}

func testUserAuthServerConfig() *ServerConfig {
	return &ServerConfig{
		WebAuthMode: WebAuthModeOIDC,
		OIDC: OIDCConfig{
			RedirectURL:     "https://kodelet.example/auth/oidc/callback",
			SessionDuration: time.Hour,
		},
	}
}

func startUserLoginHTTP(t *testing.T, server *Server, requestOrigin string, request userauth.DeviceStartRequest) userauth.DeviceStartResponse {
	t.Helper()
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	httpRequest := httptest.NewRequest(http.MethodPost, requestOrigin+userauth.DeviceStartPath, bytes.NewReader(payload))
	httpRequest.Header.Set("Content-Type", "application/json")
	response := serveUserAuthRequest(server, httpRequest)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var started userauth.DeviceStartResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &started))
	return started
}

func pollUserLoginHTTP(t *testing.T, server *Server, started userauth.DeviceStartResponse) userauth.DevicePollResponse {
	t.Helper()
	response := pollUserLoginHTTPResponse(t, server, started)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var polled userauth.DevicePollResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &polled))
	return polled
}

func pollUserLoginHTTPResponse(t *testing.T, server *Server, started userauth.DeviceStartResponse) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(userauth.DevicePollRequest{AuthorizationID: started.AuthorizationID, DeviceCode: started.DeviceCode})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "https://kodelet.example"+userauth.DevicePollPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	return serveUserAuthRequest(server, request)
}

func performUserLoginDecision(t *testing.T, server *Server, userCode, decision, sessionToken, cookieCSRF, formCSRF, origin, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(userLoginDecisionRequest{UserCode: userCode, Decision: decision, CSRFToken: formCSRF})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, origin+"/api/auth/v1/device/decision", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if sessionToken != "" {
		addUserAuthSessionCookies(request, sessionToken, cookieCSRF)
	}
	return serveUserAuthRequest(server, request)
}

func addUserAuthSessionCookies(request *http.Request, sessionToken, csrfToken string) {
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
	request.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: csrfToken})
	request.Header.Set(webCSRFHeaderName, csrfToken)
}

func issueUserCredentialForHTTPTest(t *testing.T, store *authStore, principal Principal, duration time.Duration) (userauth.DeviceStartResponse, userLoginAuthorization) {
	t.Helper()
	started, err := store.StartUserLogin(t.Context(), testUserLoginStartRequest(), "https://kodelet.example"+userauth.DeviceVerificationPath)
	require.NoError(t, err)
	approved, err := store.ApproveUserLogin(t.Context(), started.UserCode, principal, duration)
	require.NoError(t, err)
	return started, approved
}

func serveUserAuthRequest(server *Server, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}

func strconvForDuration(duration time.Duration) string {
	return strconv.Itoa(retryAfterSeconds(duration))
}
