package webui

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testOIDCFlow struct{}

func (testOIDCFlow) AuthorizationURL(state, nonce, verifier string) string {
	return "https://issuer.example.com/authorize"
}

func (testOIDCFlow) Exchange(context.Context, string, string, string) (OIDCIdentity, error) {
	return OIDCIdentity{}, nil
}

type recordingOIDCFlow struct {
	state         string
	nonce         string
	verifier      string
	code          string
	exchangeNonce string
	identity      OIDCIdentity
}

func (f *recordingOIDCFlow) AuthorizationURL(state, nonce, verifier string) string {
	f.state = state
	f.nonce = nonce
	f.verifier = verifier
	query := url.Values{"state": []string{state}}
	return "https://issuer.example.com/authorize?" + query.Encode()
}

func (f *recordingOIDCFlow) Exchange(_ context.Context, code, verifier, expectedNonce string) (OIDCIdentity, error) {
	f.code = code
	f.verifier = verifier
	f.exchangeNonce = expectedNonce
	return f.identity, nil
}

func TestServerConfigResolvedAuthModes(t *testing.T) {
	tests := []struct {
		name       string
		config     *ServerConfig
		wantWeb    WebAuthMode
		wantRunner RunnerAuthMode
	}{
		{
			name:       "nil configuration",
			wantWeb:    WebAuthModeNone,
			wantRunner: RunnerAuthModeNone,
		},
		{
			name:       "empty configuration",
			config:     &ServerConfig{},
			wantWeb:    WebAuthModeNone,
			wantRunner: RunnerAuthModeNone,
		},
		{
			name: "legacy tokens infer token modes",
			config: &ServerConfig{
				AuthToken:       "web-token",
				RunnerAuthToken: "runner-token",
			},
			wantWeb:    WebAuthModeToken,
			wantRunner: RunnerAuthModeToken,
		},
		{
			name: "explicit modes are normalized",
			config: &ServerConfig{
				WebAuthMode:    " OIDC ",
				RunnerAuthMode: " Enrollment ",
			},
			wantWeb:    WebAuthModeOIDC,
			wantRunner: RunnerAuthModeEnrollment,
		},
		{
			name: "explicit none wins during resolution",
			config: &ServerConfig{
				AuthToken:       "web-token",
				RunnerAuthToken: "runner-token",
				WebAuthMode:     WebAuthModeNone,
				RunnerAuthMode:  RunnerAuthModeNone,
			},
			wantWeb:    WebAuthModeNone,
			wantRunner: RunnerAuthModeNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantWeb, tt.config.resolvedWebAuthMode())
			assert.Equal(t, tt.wantRunner, tt.config.resolvedRunnerAuthMode())
		})
	}
}

func TestServerConfigValidateAuthModes(t *testing.T) {
	validOIDC := OIDCConfig{Flow: testOIDCFlow{}, AllowAnyUser: true, RunnerAdminEmails: []string{"runner-admin@example.com"}}
	tests := []struct {
		name    string
		config  ServerConfig
		wantErr string
	}{
		{
			name: "none modes",
		},
		{
			name: "inferred token modes",
			config: ServerConfig{
				AuthToken:       "web-token",
				RunnerAuthToken: "runner-token",
			},
		},
		{
			name: "OIDC enrollment",
			config: ServerConfig{
				WebAuthMode:    WebAuthModeOIDC,
				RunnerAuthMode: RunnerAuthModeEnrollment,
				OIDC:           validOIDC,
			},
		},
		{
			name: "web none rejects token",
			config: ServerConfig{
				WebAuthMode: WebAuthModeNone,
				AuthToken:   "web-token",
			},
			wantErr: "web auth token cannot be used",
		},
		{
			name: "web token mode requires token",
			config: ServerConfig{
				WebAuthMode: WebAuthModeToken,
			},
			wantErr: "web auth token is required",
		},
		{
			name: "OIDC requires an access rule",
			config: ServerConfig{
				WebAuthMode: WebAuthModeOIDC,
				OIDC:        OIDCConfig{Flow: testOIDCFlow{}},
			},
			wantErr: "OIDC requires an allowed email/domain",
		},
		{
			name: "OIDC enrollment requires an approver",
			config: ServerConfig{
				WebAuthMode:    WebAuthModeOIDC,
				RunnerAuthMode: RunnerAuthModeEnrollment,
				OIDC:           OIDCConfig{Flow: testOIDCFlow{}, AllowAnyUser: true},
			},
			wantErr: "requires a runner-admin/admin email",
		},
		{
			name: "OIDC enrollment accepts compatibility administrator",
			config: ServerConfig{
				WebAuthMode:    WebAuthModeOIDC,
				RunnerAuthMode: RunnerAuthModeEnrollment,
				AuthToken:      "compatibility-admin-token",
				OIDC:           OIDCConfig{Flow: testOIDCFlow{}, AllowAnyUser: true},
			},
		},
		{
			name: "unsupported web mode",
			config: ServerConfig{
				WebAuthMode: "password",
			},
			wantErr: "unsupported web authentication mode",
		},
		{
			name: "runner none rejects token",
			config: ServerConfig{
				RunnerAuthMode:  RunnerAuthModeNone,
				RunnerAuthToken: "runner-token",
			},
			wantErr: "runner auth token cannot be used",
		},
		{
			name: "runner token mode requires token",
			config: ServerConfig{
				RunnerAuthMode: RunnerAuthModeToken,
			},
			wantErr: "runner auth token is required",
		},
		{
			name: "enrollment rejects ignored runner token",
			config: ServerConfig{
				WebAuthMode:     WebAuthModeOIDC,
				RunnerAuthMode:  RunnerAuthModeEnrollment,
				RunnerAuthToken: "runner-token",
				OIDC:            validOIDC,
			},
			wantErr: "runner auth token cannot be used when runner authentication mode is enrollment",
		},
		{
			name: "enrollment requires web authentication",
			config: ServerConfig{
				RunnerAuthMode: RunnerAuthModeEnrollment,
			},
			wantErr: "runner enrollment requires web authentication",
		},
		{
			name: "hybrid is unsupported",
			config: ServerConfig{
				RunnerAuthMode: "hybrid",
			},
			wantErr: "unsupported runner authentication mode",
		},
		{
			name: "unsupported runner mode",
			config: ServerConfig{
				RunnerAuthMode: "password",
			},
			wantErr: "unsupported runner authentication mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config
			config.normalizeAuth()
			err := config.validateAuthModes()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestServerConfigRequiresAuthStore(t *testing.T) {
	assert.False(t, (*ServerConfig)(nil).requiresAuthStore())
	assert.False(t, (&ServerConfig{WebAuthMode: WebAuthModeToken, RunnerAuthMode: RunnerAuthModeToken}).requiresAuthStore())
	assert.True(t, (&ServerConfig{WebAuthMode: WebAuthModeOIDC}).requiresAuthStore())
	assert.True(t, (&ServerConfig{RunnerAuthMode: RunnerAuthModeEnrollment}).requiresAuthStore())
}

func TestOIDCConfigValidateIssuerURL(t *testing.T) {
	tests := []struct {
		name      string
		issuer    string
		wantError string
	}{
		{name: "HTTPS issuer", issuer: "https://issuer.example.com/realms/kodelet"},
		{name: "loopback HTTP issuer", issuer: "http://localhost:5556/realms/kodelet"},
		{name: "loopback IPv4 HTTP issuer", issuer: "http://127.0.0.1:5556"},
		{name: "remote HTTP issuer", issuer: "http://issuer.example.com", wantError: "must use https except on loopback hosts"},
		{name: "relative issuer", issuer: "issuer.example.com", wantError: "must be an absolute http:// or https:// URL"},
		{name: "non HTTP issuer", issuer: "ftp://issuer.example.com", wantError: "must be an absolute http:// or https:// URL"},
		{name: "issuer user information", issuer: "https://user@issuer.example.com", wantError: "without user information"},
		{name: "issuer query", issuer: "https://issuer.example.com?tenant=one", wantError: "without user information"},
		{name: "issuer fragment", issuer: "https://issuer.example.com#fragment", wantError: "without user information"},
		{name: "issuer empty fragment", issuer: "https://issuer.example.com#", wantError: "without user information"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := OIDCConfig{
				IssuerURL:    test.issuer,
				ClientID:     "kodelet",
				ClientSecret: "secret",
				RedirectURL:  "http://localhost:8080/auth/oidc/callback",
				AllowAnyUser: true,
			}
			err := config.Validate()
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestOIDCConfigPrincipalAccessRules(t *testing.T) {
	baseIdentity := OIDCIdentity{
		Issuer:        "https://issuer.example.com",
		Subject:       "subject-1",
		Name:          " Test User ",
		Email:         "user@example.com",
		EmailVerified: true,
	}
	tests := []struct {
		name     string
		config   OIDCConfig
		identity OIDCIdentity
		wantErr  string
	}{
		{
			name:     "allow any verified user",
			config:   OIDCConfig{AllowAnyUser: true},
			identity: baseIdentity,
		},
		{
			name:     "exact email is case insensitive",
			config:   OIDCConfig{AllowedEmails: []string{" USER@EXAMPLE.COM "}},
			identity: baseIdentity,
		},
		{
			name:     "generic provider domain allowlist",
			config:   OIDCConfig{AllowedDomains: []string{"example.com"}},
			identity: baseIdentity,
		},
		{
			name:   "Google domain requires matching signed hosted domain",
			config: OIDCConfig{AllowedDomains: []string{"example.com"}},
			identity: func() OIDCIdentity {
				identity := baseIdentity
				identity.Issuer = "https://accounts.google.com"
				identity.HostedDomain = "EXAMPLE.COM"
				return identity
			}(),
		},
		{
			name:   "Google domain rejects missing hosted domain",
			config: OIDCConfig{AllowedDomains: []string{"example.com"}},
			identity: func() OIDCIdentity {
				identity := baseIdentity
				identity.Issuer = "https://accounts.google.com"
				return identity
			}(),
			wantErr: "not allowed",
		},
		{
			name:   "Google domain rejects mismatched hosted domain",
			config: OIDCConfig{AllowedDomains: []string{"example.com"}},
			identity: func() OIDCIdentity {
				identity := baseIdentity
				identity.Issuer = "accounts.google.com/"
				identity.HostedDomain = "other.example.com"
				return identity
			}(),
			wantErr: "not allowed",
		},
		{
			name:   "Google exact email does not require hosted domain",
			config: OIDCConfig{AllowedEmails: []string{"user@example.com"}},
			identity: func() OIDCIdentity {
				identity := baseIdentity
				identity.Issuer = "https://accounts.google.com"
				return identity
			}(),
		},
		{
			name:     "unverified email is rejected even when allowed",
			config:   OIDCConfig{AllowAnyUser: true},
			identity: OIDCIdentity{Issuer: baseIdentity.Issuer, Subject: baseIdentity.Subject, Email: baseIdentity.Email},
			wantErr:  "requires a verified email address",
		},
		{
			name:     "missing email is rejected",
			config:   OIDCConfig{AllowAnyUser: true},
			identity: OIDCIdentity{Issuer: baseIdentity.Issuer, Subject: baseIdentity.Subject, EmailVerified: true},
			wantErr:  "requires a verified email address",
		},
		{
			name:     "missing stable identity is rejected",
			config:   OIDCConfig{AllowAnyUser: true},
			identity: OIDCIdentity{Email: baseIdentity.Email, EmailVerified: true},
			wantErr:  "missing issuer or subject",
		},
		{
			name:     "disallowed email is rejected",
			config:   OIDCConfig{AllowedDomains: []string{"other.example.com"}},
			identity: baseIdentity,
			wantErr:  "not allowed",
		},
		{
			name:   "malformed email cannot suffix-match an allowed domain",
			config: OIDCConfig{AllowedDomains: []string{"example.com"}},
			identity: func() OIDCIdentity {
				identity := baseIdentity
				identity.Email = "user@evil.example@example.com"
				return identity
			}(),
			wantErr: "not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal, err := tt.config.principal(tt.identity)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.identity.Issuer+"|"+tt.identity.Subject, principal.ID)
			assert.Equal(t, "user@example.com", principal.Email)
			assert.Equal(t, "Test User", principal.Name)
			assert.True(t, principal.HasRole(RoleUser))
		})
	}
}

func TestOIDCConfigPrincipalRoleAssignment(t *testing.T) {
	identity := func(email string) OIDCIdentity {
		return OIDCIdentity{
			Issuer:        "https://issuer.example.com",
			Subject:       email,
			Email:         email,
			EmailVerified: true,
		}
	}
	tests := []struct {
		name      string
		config    OIDCConfig
		email     string
		wantRoles []string
	}{
		{
			name:      "ordinary allowed user",
			config:    OIDCConfig{AllowedEmails: []string{"user@example.com"}},
			email:     "user@example.com",
			wantRoles: []string{string(RoleUser)},
		},
		{
			name:      "terminal email implicitly allows access",
			config:    OIDCConfig{TerminalEmails: []string{"terminal@example.com"}},
			email:     "terminal@example.com",
			wantRoles: []string{string(RoleUser), string(RoleTerminal)},
		},
		{
			name:      "runner admin email implicitly allows access",
			config:    OIDCConfig{RunnerAdminEmails: []string{"runner@example.com"}},
			email:     "runner@example.com",
			wantRoles: []string{string(RoleUser), string(RoleRunnerAdmin)},
		},
		{
			name: "role lists combine",
			config: OIDCConfig{
				TerminalEmails:    []string{"operator@example.com"},
				RunnerAdminEmails: []string{"operator@example.com"},
			},
			email:     "operator@example.com",
			wantRoles: []string{string(RoleUser), string(RoleTerminal), string(RoleRunnerAdmin)},
		},
		{
			name:      "admin email implicitly allows access",
			config:    OIDCConfig{AdminEmails: []string{"admin@example.com"}},
			email:     "admin@example.com",
			wantRoles: []string{string(RoleUser), string(RoleAdmin)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal, err := tt.config.principal(identity(tt.email))
			require.NoError(t, err)
			assert.Equal(t, tt.wantRoles, principal.Roles)
			if principal.HasRole(RoleAdmin) {
				assert.True(t, principal.HasRole(RoleTerminal))
				assert.True(t, principal.HasRole(RoleRunnerAdmin))
				assert.True(t, principal.HasRole(RoleUser))
			}
		})
	}
}

func TestOIDCConfigNormalizeSets(t *testing.T) {
	config := OIDCConfig{
		Scopes:          []string{"openid", " openid ", "email"},
		AllowedEmails:   []string{"USER@example.com", " user@EXAMPLE.com ", ""},
		AllowedDomains:  []string{"@Example.com", " example.COM ", ""},
		SessionDuration: -time.Minute,
	}

	config.normalize()

	assert.Equal(t, []string{"openid", "email"}, config.Scopes)
	assert.Equal(t, []string{"user@example.com"}, config.AllowedEmails)
	assert.Equal(t, []string{"example.com"}, config.AllowedDomains)
	assert.Equal(t, defaultWebSessionDuration, config.SessionDuration)
}

func TestOIDCConfigNormalizeAddsOpenIDToCustomScopes(t *testing.T) {
	config := OIDCConfig{Scopes: []string{"profile", "email"}}

	config.normalize()

	assert.Equal(t, []string{"openid", "profile", "email"}, config.Scopes)
}

func TestProviderOIDCFlowFetchesAndSubjectChecksRequiredUserInfoClaims(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: privateKey},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), "test-key"),
	)
	require.NoError(t, err)
	now := time.Now().UTC()
	userInfoSubject := "subject-1"
	userInfoCalls := 0
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                providerServer.URL,
				"authorization_endpoint":                providerServer.URL + "/authorize",
				"token_endpoint":                        providerServer.URL + "/token",
				"jwks_uri":                              providerServer.URL + "/keys",
				"userinfo_endpoint":                     providerServer.URL + "/userinfo",
				"id_token_signing_alg_values_supported": []string{string(jose.EdDSA)},
			}))
		case "/keys":
			require.NoError(t, json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
				Key: publicKey, KeyID: "test-key", Algorithm: string(jose.EdDSA), Use: "sig",
			}}}))
		case "/token":
			rawIDToken, signErr := jwt.Signed(signer).Claims(jwt.Claims{
				Issuer:   providerServer.URL,
				Subject:  "subject-1",
				Audience: jwt.Audience{"kodelet-client"},
				Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
				IssuedAt: jwt.NewNumericDate(now),
			}).Claims(struct {
				Nonce string `json:"nonce"`
			}{Nonce: "nonce-1"}).Serialize()
			require.NoError(t, signErr)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
				"id_token":     rawIDToken,
			}))
		case "/userinfo":
			userInfoCalls++
			assert.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"sub":            userInfoSubject,
				"name":           "User Info Name",
				"email":          "USER@example.com",
				"email_verified": true,
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(providerServer.Close)

	flow, err := newProviderOIDCFlow(t.Context(), OIDCConfig{
		IssuerURL:    providerServer.URL,
		ClientID:     "kodelet-client",
		ClientSecret: "client-secret",
		RedirectURL:  "http://localhost:8080/auth/oidc/callback",
		Scopes:       []string{"email", "profile"},
	})
	require.NoError(t, err)
	identity, err := flow.Exchange(t.Context(), "authorization-code", "verifier", "nonce-1")
	require.NoError(t, err)
	assert.Equal(t, "subject-1", identity.Subject)
	assert.Equal(t, "User Info Name", identity.Name)
	assert.Equal(t, "user@example.com", identity.Email)
	assert.True(t, identity.EmailVerified)
	assert.Equal(t, 1, userInfoCalls)

	userInfoSubject = "different-subject"
	_, err = flow.Exchange(t.Context(), "authorization-code", "verifier", "nonce-1")
	require.ErrorContains(t, err, "UserInfo subject does not match")
}

func TestSanitizeReturnTo(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: "/"},
		{name: "absolute path", value: "/chat", want: "/chat"},
		{name: "query is preserved", value: " /chat?id=one%20two ", want: "/chat?id=one%20two"},
		{name: "fragment is omitted", value: "/chat#section", want: "/chat"},
		{name: "relative path", value: "chat", want: "/"},
		{name: "absolute URL", value: "https://evil.example.com/path", want: "/"},
		{name: "scheme relative URL", value: "//evil.example.com/path", want: "/"},
		{name: "encoded scheme relative path", value: "/%2f%2fevil.example.com", want: "/"},
		{name: "raw backslash authority ambiguity", value: `/\evil.example.com`, want: "/"},
		{name: "encoded backslash authority ambiguity", value: "/%5c%5cevil.example.com", want: "/"},
		{name: "malformed URL", value: "/%zz", want: "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeReturnTo(tt.value))
		})
	}
}

func TestOIDCLoginCallbackCreatesAuthenticatedSession(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	flow := &recordingOIDCFlow{identity: OIDCIdentity{
		Issuer:        "https://issuer.example.com",
		Subject:       "subject-one",
		Name:          "Test Admin",
		Email:         "admin@example.com",
		EmailVerified: true,
	}}
	config := &ServerConfig{
		WebAuthMode: WebAuthModeOIDC,
		OIDC: OIDCConfig{
			Flow:            flow,
			AdminEmails:     []string{"admin@example.com"},
			SessionDuration: time.Hour,
		},
	}
	config.normalizeAuth()
	server := &Server{config: config, authStore: store, oidcFlow: flow}

	loginRequest := httptest.NewRequest(http.MethodGet, "/auth/login?return_to=%2Frunner%2Fenroll%3Fuser_code%3DABCD-EFGH", nil)
	loginResponse := httptest.NewRecorder()
	server.handleOIDCLogin(loginResponse, loginRequest)
	require.Equal(t, http.StatusFound, loginResponse.Code)
	stateCookie := responseCookie(t, loginResponse.Result().Cookies(), oidcStateCookieName)
	assert.Equal(t, oidcCallbackPath, stateCookie.Path)
	assert.True(t, stateCookie.HttpOnly)
	assert.Equal(t, flow.state, stateCookie.Value)
	assert.NotEmpty(t, flow.nonce)
	assert.NotEmpty(t, flow.verifier)
	authorizationURL, err := url.Parse(loginResponse.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, flow.state, authorizationURL.Query().Get("state"))

	callbackRequest := httptest.NewRequest(http.MethodGet, oidcCallbackPath+"?state="+url.QueryEscape(flow.state)+"&code=authorization-code", nil)
	callbackRequest.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	server.handleOIDCCallback(callbackResponse, callbackRequest)
	require.Equal(t, http.StatusFound, callbackResponse.Code)
	assert.Equal(t, "/runner/enroll?user_code=ABCD-EFGH", callbackResponse.Header().Get("Location"))
	assert.Equal(t, "authorization-code", flow.code)
	assert.Equal(t, flow.nonce, flow.exchangeNonce)
	sessionCookie := responseCookie(t, callbackResponse.Result().Cookies(), webSessionCookieName)
	csrfCookie := responseCookie(t, callbackResponse.Result().Cookies(), webCSRFCookieName)
	assert.True(t, sessionCookie.HttpOnly)
	assert.False(t, csrfCookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, csrfCookie.SameSite)

	session, found, err := store.LoadWebSession(t.Context(), sessionCookie.Value)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "admin@example.com", session.Email)
	assert.Contains(t, session.Roles, string(RoleAdmin))
	valid, err := store.WebSessionCSRFValid(t.Context(), session.ID, csrfCookie.Value)
	require.NoError(t, err)
	assert.True(t, valid)

	meHandler := server.authMiddleware(http.HandlerFunc(server.handleAuthMe))
	meRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meRequest.AddCookie(sessionCookie)
	meResponse := httptest.NewRecorder()
	meHandler.ServeHTTP(meResponse, meRequest)
	assert.Equal(t, http.StatusOK, meResponse.Code)
	assert.Contains(t, meResponse.Body.String(), `"email":"admin@example.com"`)
	assert.Contains(t, meResponse.Body.String(), `"admin"`)

	replayedResponse := httptest.NewRecorder()
	server.handleOIDCCallback(replayedResponse, callbackRequest)
	assert.Equal(t, http.StatusUnauthorized, replayedResponse.Code)
}

func TestLogoutInvalidatesSessionAndStopsOnPublicSignedOutPage(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), "issuer", "subject", "User", "user@example.com", []string{string(RoleUser)}, time.Hour)
	require.NoError(t, err)
	server := &Server{
		config:    &ServerConfig{WebAuthMode: WebAuthModeOIDC},
		authStore: store,
	}

	logoutRequest := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	logoutRequest.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
	logoutRequest.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: csrfToken})
	logoutRequest.AddCookie(&http.Cookie{Name: webUIAuthCookieName, Value: "compatibility-token"})
	logoutResponse := httptest.NewRecorder()

	server.handleLogout(logoutResponse, logoutRequest)

	assert.Equal(t, http.StatusFound, logoutResponse.Code)
	assert.Equal(t, signedOutPath, logoutResponse.Header().Get("Location"))
	_, found, err := store.LoadWebSession(t.Context(), sessionToken)
	require.NoError(t, err)
	assert.False(t, found)
	for _, name := range []string{webSessionCookieName, webCSRFCookieName, webUIAuthCookieName} {
		cookie := responseClearedCookie(t, logoutResponse.Result().Cookies(), name)
		assert.Empty(t, cookie.Value)
		assert.Equal(t, -1, cookie.MaxAge)
	}

	signedOutRequest := httptest.NewRequest(http.MethodGet, signedOutPath, nil)
	signedOutResponse := httptest.NewRecorder()
	server.authMiddleware(http.HandlerFunc(server.handleReactSPA)).ServeHTTP(signedOutResponse, signedOutRequest)

	assert.Equal(t, http.StatusOK, signedOutResponse.Code)
	assert.Empty(t, signedOutResponse.Header().Get("Location"))
	assert.Contains(t, signedOutResponse.Body.String(), "<html")
}

func TestAuthMiddlewareAndRoleAuthorization(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	userToken, _, err := store.CreateWebSession(t.Context(), "issuer", "user", "User", "user@example.com", []string{string(RoleUser)}, time.Hour)
	require.NoError(t, err)
	terminalToken, _, err := store.CreateWebSession(t.Context(), "issuer", "terminal", "Terminal", "terminal@example.com", []string{string(RoleUser), string(RoleTerminal)}, time.Hour)
	require.NoError(t, err)
	server := &Server{
		config: &ServerConfig{
			WebAuthMode: WebAuthModeOIDC,
			AuthToken:   "compat-admin-token",
		},
		authStore: store,
	}
	allowed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	runnerAdminHandler := server.authMiddleware(server.requireRole(RoleRunnerAdmin, allowed))
	request := httptest.NewRequest(http.MethodGet, "/api/runners", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: userToken})
	response := httptest.NewRecorder()
	runnerAdminHandler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)

	request = httptest.NewRequest(http.MethodGet, "/api/runners", nil)
	request.Header.Set("Authorization", "Bearer compat-admin-token")
	response = httptest.NewRecorder()
	runnerAdminHandler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)

	terminalHandler := server.authMiddleware(server.requireRole(RoleTerminal, allowed))
	request = httptest.NewRequest(http.MethodGet, "/api/terminal/ws", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: terminalToken})
	response = httptest.NewRecorder()
	terminalHandler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)

	request = httptest.NewRequest(http.MethodGet, "/app?view=full", nil)
	response = httptest.NewRecorder()
	server.authMiddleware(allowed).ServeHTTP(response, request)
	assert.Equal(t, http.StatusFound, response.Code)
	assert.Equal(t, "/auth/login?return_to=%2Fapp%3Fview%3Dfull", response.Header().Get("Location"))

	request = httptest.NewRequest(http.MethodGet, "/api/chat/settings", nil)
	response = httptest.NewRecorder()
	server.authMiddleware(allowed).ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	noAuthServer := &Server{config: &ServerConfig{WebAuthMode: WebAuthModeNone}}
	adminHandler := noAuthServer.authMiddleware(noAuthServer.requireRole(RoleAdmin, allowed))
	request = httptest.NewRequest(http.MethodGet, "/api/runners", nil)
	response = httptest.NewRecorder()
	adminHandler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestAuthMiddlewareRequiresCSRFForWebSessionWrites(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), "issuer", "subject", "User", "user@example.com", []string{string(RoleUser)}, time.Hour)
	require.NoError(t, err)
	server := &Server{
		config:    &ServerConfig{WebAuthMode: WebAuthModeOIDC, AuthToken: "compat-admin-token"},
		authStore: store,
	}
	handler := server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		require.True(t, ok)
		assert.Equal(t, "issuer|subject", principal.ID)
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/chat/settings", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)

	request = httptest.NewRequest(http.MethodPost, "/runner/enroll", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)

	for _, test := range []struct {
		name         string
		cookieTokens []string
		headerTokens []string
		expectedCode int
	}{
		{name: "missing token", expectedCode: http.StatusForbidden},
		{name: "cookie only", cookieTokens: []string{csrfToken}, expectedCode: http.StatusForbidden},
		{name: "header only", headerTokens: []string{csrfToken}, expectedCode: http.StatusForbidden},
		{name: "mismatched token", cookieTokens: []string{csrfToken}, headerTokens: []string{"wrong-token"}, expectedCode: http.StatusForbidden},
		{name: "duplicate cookie", cookieTokens: []string{csrfToken, csrfToken}, headerTokens: []string{csrfToken}, expectedCode: http.StatusForbidden},
		{name: "duplicate header", cookieTokens: []string{csrfToken}, headerTokens: []string{csrfToken, csrfToken}, expectedCode: http.StatusForbidden},
		{name: "valid token", cookieTokens: []string{csrfToken}, headerTokens: []string{csrfToken}, expectedCode: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"run a tool"}`))
			request.Header.Set("Content-Type", "text/plain")
			request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
			for _, token := range test.cookieTokens {
				request.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: token})
			}
			for _, token := range test.headerTokens {
				request.Header.Add(webCSRFHeaderName, token)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.Equal(t, test.expectedCode, response.Code)
		})
	}

	request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"run a tool"}`))
	request.Header.Set("Authorization", "Bearer compat-admin-token")
	response = httptest.NewRecorder()
	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)
}

func responseCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name && cookie.MaxAge >= 0 {
			return cookie
		}
	}
	t.Fatalf("response cookie %s was not set", name)
	return nil
}

func responseClearedCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name && cookie.MaxAge < 0 {
			return cookie
		}
	}
	t.Fatalf("response cookie %s was not cleared", name)
	return nil
}
