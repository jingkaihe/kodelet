package webui

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

const (
	webUIAuthCookieName  = "kodelet_auth_token"
	webSessionCookieName = "kodelet_session"
	webCSRFCookieName    = "kodelet_csrf"
	oidcStateCookieName  = "kodelet_oidc_state"
	oidcCallbackPath     = "/auth/oidc/callback"
)

// WebAuthMode selects the authentication mechanism for browser and control-plane API requests.
type WebAuthMode string

const (
	WebAuthModeToken WebAuthMode = "token"
	WebAuthModeOIDC  WebAuthMode = "oidc"
	WebAuthModeNone  WebAuthMode = "none"
)

// RunnerAuthMode selects the authentication mechanisms accepted by runner registration.
type RunnerAuthMode string

const (
	RunnerAuthModeToken      RunnerAuthMode = "token"
	RunnerAuthModeEnrollment RunnerAuthMode = "enrollment"
	RunnerAuthModeHybrid     RunnerAuthMode = "hybrid"
	RunnerAuthModeNone       RunnerAuthMode = "none"
)

// Role identifies a server-side authorization capability.
type Role string

const (
	RoleUser        Role = "user"
	RoleTerminal    Role = "terminal"
	RoleRunnerAdmin Role = "runner-admin"
	RoleAdmin       Role = "admin"
)

// Principal is the authenticated human/API identity attached to a request.
type Principal struct {
	ID           string   `json:"id"`
	Issuer       string   `json:"issuer,omitempty"`
	Subject      string   `json:"subject,omitempty"`
	Name         string   `json:"name,omitempty"`
	Email        string   `json:"email,omitempty"`
	Roles        []string `json:"roles"`
	SessionID    string   `json:"-"`
	CredentialID string   `json:"-"`
}

func (p Principal) HasRole(role Role) bool {
	if slices.Contains(p.Roles, string(RoleAdmin)) {
		return true
	}
	return slices.Contains(p.Roles, string(role))
}

type principalContextKey struct{}

func principalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func contextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// OIDCIdentity is the verified identity returned by an OIDC authorization-code exchange.
type OIDCIdentity struct {
	Issuer        string
	Subject       string
	Name          string
	Email         string
	EmailVerified bool
	HostedDomain  string
}

// OIDCFlow is the testable authorization-code boundary used by the web server.
type OIDCFlow interface {
	AuthorizationURL(state, nonce, verifier string) string
	Exchange(ctx context.Context, code, verifier, expectedNonce string) (OIDCIdentity, error)
}

// OIDCConfig configures generic OpenID Connect authentication for browser users.
type OIDCConfig struct {
	IssuerURL         string
	ClientID          string
	ClientSecret      string
	RedirectURL       string
	Scopes            []string
	AllowedEmails     []string
	AllowedDomains    []string
	AdminEmails       []string
	TerminalEmails    []string
	RunnerAdminEmails []string
	AllowAnyUser      bool
	SessionDuration   time.Duration
	Flow              OIDCFlow
}

func (c *ServerConfig) normalizeAuth() {
	if c == nil {
		return
	}
	c.AuthToken = strings.TrimSpace(c.AuthToken)
	c.RunnerAuthToken = strings.TrimSpace(c.RunnerAuthToken)
	c.WebAuthMode = WebAuthMode(strings.ToLower(strings.TrimSpace(string(c.WebAuthMode))))
	c.RunnerAuthMode = RunnerAuthMode(strings.ToLower(strings.TrimSpace(string(c.RunnerAuthMode))))
	c.OIDC.normalize()
}

func (c *ServerConfig) resolvedWebAuthMode() WebAuthMode {
	if c == nil {
		return WebAuthModeNone
	}
	if mode := WebAuthMode(strings.ToLower(strings.TrimSpace(string(c.WebAuthMode)))); mode != "" {
		return mode
	}
	if strings.TrimSpace(c.AuthToken) != "" {
		return WebAuthModeToken
	}
	return WebAuthModeNone
}

func (c *ServerConfig) resolvedRunnerAuthMode() RunnerAuthMode {
	if c == nil {
		return RunnerAuthModeNone
	}
	if mode := RunnerAuthMode(strings.ToLower(strings.TrimSpace(string(c.RunnerAuthMode)))); mode != "" {
		return mode
	}
	if strings.TrimSpace(c.RunnerAuthToken) != "" {
		return RunnerAuthModeToken
	}
	return RunnerAuthModeNone
}

func (c *ServerConfig) validateAuthModes() error {
	webMode := c.resolvedWebAuthMode()
	switch webMode {
	case WebAuthModeNone:
		if c.WebAuthMode != "" && c.AuthToken != "" {
			return errors.New("web auth token cannot be used when web authentication mode is none")
		}
	case WebAuthModeToken:
		if c.AuthToken == "" {
			return errors.New("web auth token is required when web authentication mode is token")
		}
	case WebAuthModeOIDC:
		if err := c.OIDC.Validate(); err != nil {
			return errors.Wrap(err, "invalid OIDC configuration")
		}
	default:
		return errors.Errorf("unsupported web authentication mode %q", webMode)
	}

	runnerMode := c.resolvedRunnerAuthMode()
	switch runnerMode {
	case RunnerAuthModeNone:
		if c.RunnerAuthMode != "" && c.RunnerAuthToken != "" {
			return errors.New("runner auth token cannot be used when runner authentication mode is none")
		}
	case RunnerAuthModeToken:
		if c.RunnerAuthToken == "" {
			return errors.New("runner auth token is required when runner authentication mode is token")
		}
	case RunnerAuthModeEnrollment:
		if c.RunnerAuthToken != "" {
			return errors.New("runner auth token cannot be used when runner authentication mode is enrollment")
		}
		if webMode == WebAuthModeNone {
			return errors.New("runner enrollment requires web authentication")
		}
	case RunnerAuthModeHybrid:
		if c.RunnerAuthToken == "" {
			return errors.New("runner auth token is required when runner authentication mode is hybrid")
		}
		if webMode == WebAuthModeNone {
			return errors.New("runner enrollment requires web authentication")
		}
	default:
		return errors.Errorf("unsupported runner authentication mode %q", runnerMode)
	}
	if (runnerMode == RunnerAuthModeEnrollment || runnerMode == RunnerAuthModeHybrid) && webMode == WebAuthModeOIDC && c.AuthToken == "" && len(c.OIDC.AdminEmails) == 0 && len(c.OIDC.RunnerAdminEmails) == 0 {
		return errors.New("OIDC runner enrollment requires a runner-admin/admin email or an administrative compatibility token")
	}
	return nil
}

func (c *ServerConfig) requiresAuthStore() bool {
	if c == nil {
		return false
	}
	runnerMode := c.resolvedRunnerAuthMode()
	return c.resolvedWebAuthMode() == WebAuthModeOIDC || runnerMode == RunnerAuthModeEnrollment || runnerMode == RunnerAuthModeHybrid
}

func (c *OIDCConfig) normalize() {
	if c == nil {
		return
	}
	c.IssuerURL = strings.TrimSpace(c.IssuerURL)
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.ClientSecret = strings.TrimSpace(c.ClientSecret)
	c.RedirectURL = strings.TrimSpace(c.RedirectURL)
	if len(c.Scopes) == 0 {
		c.Scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	c.Scopes = normalizeStringSet(c.Scopes)
	c.AllowedEmails = normalizeEmailSet(c.AllowedEmails)
	c.AllowedDomains = normalizeDomainSet(c.AllowedDomains)
	c.AdminEmails = normalizeEmailSet(c.AdminEmails)
	c.TerminalEmails = normalizeEmailSet(c.TerminalEmails)
	c.RunnerAdminEmails = normalizeEmailSet(c.RunnerAdminEmails)
	if c.SessionDuration <= 0 {
		c.SessionDuration = defaultWebSessionDuration
	}
}

func (c OIDCConfig) Validate() error {
	c.normalize()
	if c.Flow == nil {
		if err := validateOIDCIssuerURL(c.IssuerURL); err != nil {
			return err
		}
		if c.ClientID == "" {
			return errors.New("OIDC client ID is required")
		}
		if c.ClientSecret == "" {
			return errors.New("OIDC client secret is required")
		}
		if c.RedirectURL == "" {
			return errors.New("OIDC redirect URL is required")
		}
		redirect, err := url.Parse(c.RedirectURL)
		if err != nil || redirect.Host == "" || (redirect.Scheme != "http" && redirect.Scheme != "https") || redirect.Fragment != "" {
			return errors.New("OIDC redirect URL must be an absolute http:// or https:// URL without a fragment")
		}
		if redirect.Scheme == "http" && !controlplaneurl.IsLoopbackHostname(redirect.Hostname()) {
			return errors.New("OIDC redirect URL must use https except on loopback hosts")
		}
	}
	if !c.AllowAnyUser && len(c.AllowedEmails) == 0 && len(c.AllowedDomains) == 0 && len(c.AdminEmails) == 0 && len(c.TerminalEmails) == 0 && len(c.RunnerAdminEmails) == 0 {
		return errors.New("OIDC requires an allowed email/domain or --oidc-allow-any-user")
	}
	return nil
}

func validateOIDCIssuerURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("OIDC issuer URL is required")
	}
	issuer, err := url.Parse(raw)
	if err != nil || !issuer.IsAbs() || issuer.Host == "" || issuer.Hostname() == "" || issuer.User != nil || issuer.Opaque != "" || issuer.RawQuery != "" || issuer.ForceQuery || strings.Contains(raw, "#") || (issuer.Scheme != "http" && issuer.Scheme != "https") {
		return errors.New("OIDC issuer URL must be an absolute http:// or https:// URL without user information, a query, or a fragment")
	}
	if issuer.Scheme == "http" && !controlplaneurl.IsLoopbackHostname(issuer.Hostname()) {
		return errors.New("OIDC issuer URL must use https except on loopback hosts")
	}
	return nil
}

type providerOIDCFlow struct {
	issuer   string
	oauth2   oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func newProviderOIDCFlow(ctx context.Context, config OIDCConfig) (OIDCFlow, error) {
	config.normalize()
	if err := validateOIDCIssuerURL(config.IssuerURL); err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(ctx, config.IssuerURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to discover OIDC provider")
	}
	return &providerOIDCFlow{
		issuer: config.IssuerURL,
		oauth2: oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  config.RedirectURL,
			Scopes:       slices.Clone(config.Scopes),
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: config.ClientID}),
	}, nil
}

func (f *providerOIDCFlow) AuthorizationURL(state, nonce, verifier string) string {
	return f.oauth2.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

func (f *providerOIDCFlow) Exchange(ctx context.Context, code, verifier, expectedNonce string) (OIDCIdentity, error) {
	token, err := f.oauth2.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return OIDCIdentity{}, errors.Wrap(err, "failed to exchange OIDC authorization code")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		return OIDCIdentity{}, errors.New("OIDC token response did not include an ID token")
	}
	idToken, err := f.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return OIDCIdentity{}, errors.Wrap(err, "failed to verify OIDC ID token")
	}
	if !constantTimeStringEqual(strings.TrimSpace(idToken.Nonce), strings.TrimSpace(expectedNonce)) {
		return OIDCIdentity{}, errors.New("OIDC ID token nonce does not match the login transaction")
	}
	var claims struct {
		Name          string `json:"name"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HostedDomain  string `json:"hd"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return OIDCIdentity{}, errors.Wrap(err, "failed to decode OIDC ID token claims")
	}
	return OIDCIdentity{
		Issuer:        idToken.Issuer,
		Subject:       idToken.Subject,
		Name:          strings.TrimSpace(claims.Name),
		Email:         strings.ToLower(strings.TrimSpace(claims.Email)),
		EmailVerified: claims.EmailVerified,
		HostedDomain:  strings.ToLower(strings.TrimSpace(claims.HostedDomain)),
	}, nil
}

func (c OIDCConfig) principal(identity OIDCIdentity) (Principal, error) {
	c.normalize()
	identity.Issuer = strings.TrimSpace(identity.Issuer)
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	identity.HostedDomain = strings.ToLower(strings.TrimSpace(identity.HostedDomain))
	if identity.Issuer == "" || identity.Subject == "" {
		return Principal{}, errors.New("OIDC identity is missing issuer or subject")
	}
	if identity.Email == "" || !identity.EmailVerified {
		return Principal{}, errors.New("OIDC identity requires a verified email address")
	}
	if !c.identityAllowed(identity) {
		return Principal{}, errors.New("OIDC identity is not allowed to access this Kodelet server")
	}
	roles := []string{string(RoleUser)}
	if containsNormalized(c.AdminEmails, identity.Email) {
		roles = append(roles, string(RoleAdmin))
	} else {
		if containsNormalized(c.TerminalEmails, identity.Email) {
			roles = append(roles, string(RoleTerminal))
		}
		if containsNormalized(c.RunnerAdminEmails, identity.Email) {
			roles = append(roles, string(RoleRunnerAdmin))
		}
	}
	return Principal{
		ID:      identity.Issuer + "|" + identity.Subject,
		Issuer:  identity.Issuer,
		Subject: identity.Subject,
		Name:    strings.TrimSpace(identity.Name),
		Email:   identity.Email,
		Roles:   normalizeRoleNames(roles),
	}, nil
}

func (c OIDCConfig) identityAllowed(identity OIDCIdentity) bool {
	if c.AllowAnyUser || containsNormalized(c.AllowedEmails, identity.Email) || containsNormalized(c.AdminEmails, identity.Email) || containsNormalized(c.TerminalEmails, identity.Email) || containsNormalized(c.RunnerAdminEmails, identity.Email) {
		return true
	}
	if strings.Count(identity.Email, "@") != 1 {
		return false
	}
	separator := strings.IndexByte(identity.Email, '@')
	if separator == 0 || separator == len(identity.Email)-1 {
		return false
	}
	domain := identity.Email[separator+1:]
	if !containsNormalized(c.AllowedDomains, domain) {
		return false
	}
	if isGoogleIssuer(identity.Issuer) {
		return identity.HostedDomain == domain
	}
	return true
}

func isGoogleIssuer(issuer string) bool {
	issuer = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(issuer)), "/")
	return issuer == "https://accounts.google.com" || issuer == "accounts.google.com"
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidcFlow == nil || s.authStore == nil {
		s.writeAuthError(w, r, http.StatusServiceUnavailable, "OIDC authentication is unavailable")
		return
	}
	if !s.allowPublicAuthRequest(r, maxOIDCLoginRequestsPerWindow) {
		w.Header().Set("Retry-After", strconv.Itoa(int(publicAuthRateWindow/time.Second)))
		s.writeAuthError(w, r, http.StatusTooManyRequests, "too many OIDC login attempts; retry later")
		return
	}
	state, _, err := newAuthSecret()
	if err != nil {
		s.writeAuthError(w, r, http.StatusInternalServerError, "failed to initialize OIDC login")
		return
	}
	nonce, _, err := newAuthSecret()
	if err != nil {
		s.writeAuthError(w, r, http.StatusInternalServerError, "failed to initialize OIDC login")
		return
	}
	verifier := oauth2.GenerateVerifier()
	returnTo := sanitizeReturnTo(r.URL.Query().Get("return_to"))
	if err := s.authStore.CreateOIDCTransaction(r.Context(), state, nonce, verifier, returnTo, defaultOIDCTransactionTTL); err != nil {
		if errors.Is(err, errTooManyOIDCTransactions) {
			s.writeAuthError(w, r, http.StatusTooManyRequests, "too many OIDC logins are pending; retry later")
			return
		}
		s.writeAuthError(w, r, http.StatusInternalServerError, "failed to initialize OIDC login")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    state,
		Path:     oidcCallbackPath,
		MaxAge:   int(defaultOIDCTransactionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPSRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.oidcFlow.AuthorizationURL(state, nonce, verifier), http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidcFlow == nil || s.authStore == nil {
		s.writeAuthError(w, r, http.StatusServiceUnavailable, "OIDC authentication is unavailable")
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		s.writeAuthError(w, r, http.StatusUnauthorized, "OIDC provider denied authentication")
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	stateCookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || !constantTimeStringEqual(state, stateCookie.Value) {
		s.writeAuthError(w, r, http.StatusUnauthorized, "OIDC login state is invalid")
		return
	}
	transaction, err := s.authStore.ConsumeOIDCTransaction(r.Context(), state)
	if err != nil {
		s.writeAuthError(w, r, http.StatusUnauthorized, "OIDC login state is invalid or expired")
		return
	}
	identity, err := s.oidcFlow.Exchange(r.Context(), strings.TrimSpace(r.URL.Query().Get("code")), transaction.PKCEVerifier, transaction.Nonce)
	if err != nil {
		s.writeAuthError(w, r, http.StatusUnauthorized, "OIDC authentication failed")
		return
	}
	principal, err := s.config.OIDC.principal(identity)
	if err != nil {
		s.writeAuthError(w, r, http.StatusForbidden, err.Error())
		return
	}
	sessionToken, csrfToken, err := s.authStore.CreateWebSession(r.Context(), principal.Issuer, principal.Subject, principal.Name, principal.Email, principal.Roles, s.config.OIDC.SessionDuration)
	if err != nil {
		s.writeAuthError(w, r, http.StatusInternalServerError, "failed to create authenticated session")
		return
	}
	setWebSessionCookies(w, r, sessionToken, csrfToken, s.config.OIDC.SessionDuration)
	clearCookie(w, r, oidcStateCookieName, oidcCallbackPath, true)
	http.Redirect(w, r, sanitizeReturnTo(transaction.ReturnTo), http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(webSessionCookieName); err == nil && s.authStore != nil {
		_ = s.authStore.DeleteWebSession(r.Context(), cookie.Value)
	}
	clearCookie(w, r, webSessionCookieName, "/", true)
	clearCookie(w, r, webCSRFCookieName, "/", false)
	clearCookie(w, r, webUIAuthCookieName, "/", true)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	encodeAuthJSON(w, http.StatusOK, principal)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || isPublicAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.config == nil {
			next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal("anonymous"))))
			return
		}
		if r.URL.Path == protocol.Endpoint {
			principal, ok := s.authenticateRunnerRequest(w, r)
			if !ok {
				return
			}
			next.ServeHTTP(w, r.WithContext(contextWithRunnerPrincipal(r.Context(), principal)))
			return
		}

		mode := s.config.resolvedWebAuthMode()
		if mode == WebAuthModeNone {
			next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal("anonymous"))))
			return
		}

		if authorization, present := explicitAuthorizationHeader(r); present {
			if s.config.AuthToken != "" && constantTimeStringEqual(authHeaderToken(authorization), s.config.AuthToken) {
				next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal("token"))))
				return
			}
			if mode == WebAuthModeOIDC {
				bearerToken, valid := strictKodeletBearerToken(authorization)
				if !valid {
					s.writeAuthError(w, r, http.StatusUnauthorized, "invalid authentication credentials")
					return
				}
				if s.authStore == nil {
					s.writeAuthError(w, r, http.StatusInternalServerError, "user credential authentication is unavailable")
					return
				}
				identity, err := s.authStore.LoadUserCredential(r.Context(), bearerToken)
				if err != nil {
					if errors.Is(err, errUserCredentialInvalid) {
						s.writeAuthError(w, r, http.StatusUnauthorized, "invalid authentication credentials")
						return
					}
					s.writeAuthError(w, r, http.StatusInternalServerError, "failed to load user credential")
					return
				}
				principal := principalFromUserCredential(identity)
				next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), principal)))
				return
			}
			s.writeAuthError(w, r, http.StatusUnauthorized, "invalid authentication token")
			return
		}

		queryToken, hasQueryToken := authQueryToken(r)
		if hasQueryToken {
			if s.config.AuthToken == "" || !constantTimeStringEqual(queryToken, s.config.AuthToken) {
				s.writeAuthError(w, r, http.StatusUnauthorized, "invalid authentication token")
				return
			}
			setWebUIAuthCookie(w, r, s.config.AuthToken)
			if shouldRedirectTokenRequest(r) {
				http.Redirect(w, r, tokenlessURL(r), http.StatusFound)
				return
			}
			next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal("token"))))
			return
		}
		if s.config.AuthToken != "" && requestHasAuthToken(r, s.config.AuthToken) {
			next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal("token"))))
			return
		}

		if mode == WebAuthModeOIDC {
			if cookie, err := r.Cookie(webSessionCookieName); err == nil && s.authStore != nil {
				session, found, loadErr := s.authStore.LoadWebSession(r.Context(), cookie.Value)
				if loadErr != nil {
					s.writeAuthError(w, r, http.StatusInternalServerError, "failed to load authenticated session")
					return
				}
				if found {
					principal := principalFromWebSession(session)
					next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), principal)))
					return
				}
			}
			if shouldRedirectOIDCRequest(r) {
				http.Redirect(w, r, oidcLoginURL(r), http.StatusFound)
				return
			}
			s.writeAuthError(w, r, http.StatusUnauthorized, "OIDC authentication required")
			return
		}

		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
	})
}

func (s *Server) requireRole(role Role, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok {
			s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
			return
		}
		if !principal.HasRole(role) {
			s.writeAuthError(w, r, http.StatusForbidden, "insufficient permissions")
			return
		}
		handler(w, r)
	}
}

func administrativePrincipal(id string) Principal {
	return Principal{ID: id, Roles: []string{string(RoleUser), string(RoleTerminal), string(RoleRunnerAdmin), string(RoleAdmin)}}
}

func principalFromWebSession(session storedWebSession) Principal {
	return Principal{
		ID:        session.Issuer + "|" + session.Subject,
		Issuer:    session.Issuer,
		Subject:   session.Subject,
		Name:      session.Name,
		Email:     session.Email,
		Roles:     slices.Clone(session.Roles),
		SessionID: session.ID,
	}
}

func principalFromUserCredential(identity userCredentialIdentity) Principal {
	principal := principalFromUserSnapshot(identity.Principal)
	principal.CredentialID = identity.CredentialID
	return principal
}

func principalFromUserSnapshot(snapshot userauth.PrincipalSnapshot) Principal {
	return Principal{
		ID:      snapshot.ID,
		Issuer:  snapshot.Issuer,
		Subject: snapshot.Subject,
		Name:    snapshot.Name,
		Email:   snapshot.Email,
		Roles:   slices.Clone(snapshot.Roles),
	}
}

func isPublicAuthPath(path string) bool {
	if strings.HasPrefix(path, "/assets/") || path == "/favicon.ico" {
		return true
	}
	switch path {
	case "/auth/login", oidcCallbackPath, "/auth/logout", protocol.EnrollmentStartPath, protocol.EnrollmentPollPath, userauth.DeviceStartPath, userauth.DevicePollPath:
		return true
	default:
		return false
	}
}

func explicitAuthorizationHeader(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	values, present := r.Header[http.CanonicalHeaderKey("Authorization")]
	if !present {
		return "", false
	}
	if len(values) != 1 {
		return "", true
	}
	return values[0], true
}

func strictKodeletBearerToken(header string) (string, bool) {
	if strings.TrimSpace(header) != header {
		return "", false
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	if err := userauth.ValidateBearerToken(token); err != nil {
		return "", false
	}
	return token, true
}

func shouldRedirectOIDCRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") && !isWebsocketUpgrade(r)
}

func oidcLoginURL(r *http.Request) string {
	returnTo := sanitizeReturnTo(r.URL.RequestURI())
	return "/auth/login?return_to=" + url.QueryEscape(returnTo)
}

func sanitizeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") || strings.Contains(parsed.Path, `\`) {
		return "/"
	}
	return parsed.RequestURI()
}

func setWebSessionCookies(w http.ResponseWriter, r *http.Request, sessionToken, csrfToken string, duration time.Duration) {
	maxAge := int(duration.Seconds())
	http.SetCookie(w, &http.Cookie{Name: webSessionCookieName, Value: sessionToken, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: isHTTPSRequest(r), SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: webCSRFCookieName, Value: csrfToken, Path: "/", MaxAge: maxAge, HttpOnly: false, Secure: isHTTPSRequest(r), SameSite: http.SameSiteStrictMode})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name, path string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: httpOnly, Secure: isHTTPSRequest(r), SameSite: http.SameSiteLaxMode})
}

func normalizeEmailSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeDomainSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "@")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeStringSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsNormalized(values []string, value string) bool {
	return slices.Contains(values, strings.ToLower(strings.TrimSpace(value)))
}

func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) writeAuthError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.writeErrorResponse(w, statusCode, message, nil)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write([]byte(message + "\n"))
}

func encodeAuthJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
