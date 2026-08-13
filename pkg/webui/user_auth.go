package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

const (
	maxUserLoginStartRequestBytes = 16 * 1024
	maxUserLoginPollRequestBytes  = 8 * 1024
	maxUserLoginDecisionBytes     = 32 * 1024
)

func (s *Server) handleStartUserLogin(w http.ResponseWriter, r *http.Request) {
	if !s.userLoginEnabled() {
		s.writeUserAuthAPIError(w, r, http.StatusNotFound, "user login is not enabled", nil)
		return
	}
	if !s.allowPublicAuthRequest(r, maxUserLoginStartsPerWindow) {
		w.Header().Set("Retry-After", strconv.Itoa(int(publicAuthRateWindow/time.Second)))
		s.writeUserAuthAPIError(w, r, http.StatusTooManyRequests, "too many user login attempts; retry later", nil)
		return
	}

	var request userauth.DeviceStartRequest
	if err := decodeUserAuthJSON(w, r, maxUserLoginStartRequestBytes, &request); err != nil {
		s.writeUserAuthAPIError(w, r, http.StatusBadRequest, "invalid user login request", nil)
		return
	}
	if err := request.Validate(); err != nil {
		s.writeUserAuthAPIError(w, r, http.StatusBadRequest, "invalid user login request", nil)
		return
	}
	verificationURL, err := trustedDeviceVerificationURL(s.config)
	if err != nil {
		s.writeUserAuthAPIError(w, r, http.StatusInternalServerError, "failed to start user login", err)
		return
	}
	response, err := s.authStore.StartUserLogin(r.Context(), request, verificationURL)
	if err != nil {
		if errors.Is(err, errTooManyUserLogins) {
			w.Header().Set("Retry-After", strconv.Itoa(int(publicAuthRateWindow/time.Second)))
			s.writeUserAuthAPIError(w, r, http.StatusTooManyRequests, "too many user logins are pending", nil)
			return
		}
		s.writeUserAuthAPIError(w, r, http.StatusInternalServerError, "failed to start user login", err)
		return
	}
	encodeAuthJSON(w, http.StatusCreated, response)
}

func (s *Server) handlePollUserLogin(w http.ResponseWriter, r *http.Request) {
	if !s.userLoginEnabled() {
		s.writeUserAuthAPIError(w, r, http.StatusNotFound, "user login is not enabled", nil)
		return
	}
	if !s.allowPublicAuthRequest(r, maxUserLoginPollsPerWindow) {
		w.Header().Set("Retry-After", strconv.Itoa(int(publicAuthRateWindow/time.Second)))
		s.writeUserAuthAPIError(w, r, http.StatusTooManyRequests, "too many user login polls; retry later", nil)
		return
	}

	var request userauth.DevicePollRequest
	if err := decodeUserAuthJSON(w, r, maxUserLoginPollRequestBytes, &request); err != nil {
		s.writeUserAuthAPIError(w, r, http.StatusBadRequest, "invalid user login poll", nil)
		return
	}
	if err := request.Validate(); err != nil {
		s.writeUserAuthAPIError(w, r, http.StatusBadRequest, "invalid user login poll", nil)
		return
	}
	response, err := s.authStore.PollUserLogin(r.Context(), request)
	if err != nil {
		var slowDown *userLoginSlowDownError
		switch {
		case errors.As(err, &slowDown):
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(slowDown.RetryAfter)))
			encodeAuthJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":        "slow_down",
				"retryAfterMs": max(int64(1), slowDown.RetryAfter.Milliseconds()),
			})
		case errors.Is(err, errUserAuthorizationNotFound):
			s.writeUserAuthAPIError(w, r, http.StatusNotFound, "user login authorization not found", nil)
		case errors.Is(err, errUserAuthorizationNotPending), errors.Is(err, errUserAuthorizationExpired):
			s.writeUserAuthAPIError(w, r, http.StatusConflict, "user login authorization is no longer pending", nil)
		default:
			s.writeUserAuthAPIError(w, r, http.StatusInternalServerError, "failed to poll user login", err)
		}
		return
	}
	encodeAuthJSON(w, http.StatusOK, response)
}

func (s *Server) handleUserLoginVerificationPage(w http.ResponseWriter, r *http.Request) {
	if !s.userLoginEnabled() {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	if !isGenuineOIDCWebPrincipal(principal) {
		s.writeAuthError(w, r, http.StatusForbidden, "an active OIDC web session is required")
		return
	}
	if r.Method == http.MethodPost {
		s.handleUserLoginDecision(w, r, principal)
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("user_code"))
	if code == "" {
		s.renderUserLoginVerificationPage(w, http.StatusOK, userLoginVerificationPageData{
			Principal: principal,
			CSRFToken: csrfCookieValue(r),
			EnterCode: true,
		})
		return
	}
	normalized, err := normalizeUserCode(code)
	if err != nil {
		s.renderUserLoginVerificationPage(w, http.StatusNotFound, userLoginVerificationPageData{
			Principal: principal,
			CSRFToken: csrfCookieValue(r),
			EnterCode: true,
			Error:     "User login request not found.",
		})
		return
	}
	authorization, err := s.authStore.UserLoginByUserCode(r.Context(), normalized)
	if err != nil {
		s.renderUserLoginStoreError(w, r, principal, err)
		return
	}
	s.renderUserLoginVerificationPage(w, http.StatusOK, userLoginVerificationPageData{
		Principal:     principal,
		Authorization: &authorization,
		CSRFToken:     csrfCookieValue(r),
	})
}

func (s *Server) handleUserLoginDecision(w http.ResponseWriter, r *http.Request, principal Principal) {
	if !sameOriginUserLoginDecision(r, s.config) {
		s.writeAuthError(w, r, http.StatusForbidden, "invalid user login request origin")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUserLoginDecisionBytes)
	if err := r.ParseForm(); err != nil {
		s.writeAuthError(w, r, http.StatusBadRequest, "invalid user login decision")
		return
	}

	formToken := strings.TrimSpace(r.PostForm.Get("csrf_token"))
	cookieToken := csrfCookieValue(r)
	valid, err := s.authStore.WebSessionCSRFValid(r.Context(), principal.SessionID, formToken)
	if err != nil {
		logger.G(r.Context()).WithError(err).Error("failed to validate user login CSRF token")
		s.writeAuthError(w, r, http.StatusInternalServerError, "failed to validate user login decision")
		return
	}
	if formToken == "" || cookieToken == "" || !valid || !constantTimeStringEqual(formToken, cookieToken) {
		s.writeAuthError(w, r, http.StatusForbidden, "invalid CSRF token")
		return
	}

	userCode := strings.TrimSpace(r.PostForm.Get("user_code"))
	if _, err := normalizeUserCode(userCode); err != nil {
		s.renderUserLoginVerificationPage(w, http.StatusNotFound, userLoginVerificationPageData{
			Principal: principal,
			CSRFToken: cookieToken,
			EnterCode: true,
			Error:     "User login request not found.",
		})
		return
	}
	decision := strings.TrimSpace(r.PostForm.Get("decision"))
	switch decision {
	case "approve":
		if _, err := s.authStore.ApproveUserLogin(r.Context(), userCode, principal, s.config.OIDC.SessionDuration); err != nil {
			s.renderUserLoginStoreError(w, r, principal, err)
			return
		}
		s.renderUserLoginVerificationPage(w, http.StatusOK, userLoginVerificationPageData{
			Principal: principal,
			Message:   "User login approved. You can return to the Kodelet client.",
		})
	case "deny":
		if err := s.authStore.DenyUserLogin(r.Context(), userCode, principal.ID); err != nil {
			s.renderUserLoginStoreError(w, r, principal, err)
			return
		}
		s.renderUserLoginVerificationPage(w, http.StatusOK, userLoginVerificationPageData{
			Principal: principal,
			Message:   "User login denied. You can close this page.",
		})
	default:
		s.writeAuthError(w, r, http.StatusBadRequest, "invalid user login decision")
	}
}

func (s *Server) handleRevokeCurrentUserCredential(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeUserAuthAPIError(w, r, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	if strings.TrimSpace(principal.CredentialID) == "" {
		s.writeUserAuthAPIError(w, r, http.StatusForbidden, "current authentication is not a revocable user credential", nil)
		return
	}
	if s.authStore == nil {
		s.writeUserAuthAPIError(w, r, http.StatusInternalServerError, "user credential revocation is unavailable", nil)
		return
	}
	if err := s.authStore.RevokeUserCredential(r.Context(), principal.CredentialID, "self-revoked"); err != nil {
		if errors.Is(err, errUserCredentialInvalid) {
			s.writeUserAuthAPIError(w, r, http.StatusUnauthorized, "user credential is invalid or revoked", nil)
			return
		}
		s.writeUserAuthAPIError(w, r, http.StatusInternalServerError, "failed to revoke user credential", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userLoginEnabled() bool {
	return s != nil && s.config != nil && s.config.resolvedWebAuthMode() == WebAuthModeOIDC && s.authStore != nil
}

func decodeUserAuthJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func trustedDeviceVerificationURL(config *ServerConfig) (string, error) {
	if config == nil {
		return "", errors.New("server configuration is required")
	}
	raw := strings.TrimSpace(config.OIDC.RedirectURL)
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" || !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || strings.Contains(raw, "#") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("OIDC redirect URL cannot provide a trusted user login verification origin")
	}
	parsed.Path = userauth.DeviceVerificationPath
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

func sameOriginUserLoginDecision(r *http.Request, config *ServerConfig) bool {
	if r == nil {
		return false
	}
	source := strings.TrimSpace(r.Header.Get("Origin"))
	if source == "" {
		source = strings.TrimSpace(r.Referer())
	}
	actual, err := normalizedOrigin(source)
	if err != nil {
		return false
	}
	verificationURL, err := trustedDeviceVerificationURL(config)
	if err != nil {
		return false
	}
	expected, err := normalizedOrigin(verificationURL)
	return err == nil && actual == expected
}

func normalizedOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("origin is invalid")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return normalizeCORSOrigin(parsed.String())
}

func isGenuineOIDCWebPrincipal(principal Principal) bool {
	issuer := strings.TrimSpace(principal.Issuer)
	subject := strings.TrimSpace(principal.Subject)
	return strings.TrimSpace(principal.SessionID) != "" && strings.TrimSpace(principal.CredentialID) == "" && issuer != "" && subject != "" && principal.ID == issuer+"|"+subject
}

func retryAfterSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 1
	}
	return max(1, int((duration+time.Second-1)/time.Second))
}

func (s *Server) writeUserAuthAPIError(w http.ResponseWriter, r *http.Request, statusCode int, message string, err error) {
	if err != nil {
		logger.G(r.Context()).WithError(err).Error(message)
	}
	encodeAuthJSON(w, statusCode, map[string]any{
		"error":   message,
		"status":  statusCode,
		"success": false,
	})
}

func (s *Server) renderUserLoginStoreError(w http.ResponseWriter, r *http.Request, principal Principal, err error) {
	data := userLoginVerificationPageData{
		Principal: principal,
		CSRFToken: csrfCookieValue(r),
		EnterCode: true,
	}
	statusCode := http.StatusInternalServerError
	switch {
	case errors.Is(err, errUserAuthorizationNotFound):
		statusCode = http.StatusNotFound
		data.Error = "User login request not found."
	case errors.Is(err, errUserAuthorizationExpired):
		statusCode = http.StatusConflict
		data.Error = "User login request has expired."
	case errors.Is(err, errUserAuthorizationNotPending):
		statusCode = http.StatusConflict
		data.Error = "User login request is no longer pending."
	default:
		data.Error = "Failed to process the user login request."
		logger.G(r.Context()).WithError(err).Error("failed to process user login verification")
	}
	s.renderUserLoginVerificationPage(w, statusCode, data)
}

type userLoginVerificationPageData struct {
	Principal     Principal
	Authorization *userLoginAuthorization
	CSRFToken     string
	EnterCode     bool
	Message       string
	Error         string
}

var userLoginVerificationPage = template.Must(template.New("user-login-verification").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Kodelet user login</title><style>
body{font-family:system-ui,sans-serif;max-width:760px;margin:4rem auto;padding:0 1rem;color:#202020}main{border:1px solid #ddd;border-radius:16px;padding:2rem}code{background:#f4f4f4;padding:.15rem .35rem;border-radius:4px;word-break:break-all}.error{color:#a40000}.success{color:#126b2e}label{display:block;margin:.8rem 0}.actions{display:flex;gap:.75rem;margin-top:1.5rem}button{padding:.65rem 1rem}input[type=text]{font-size:1.1rem;padding:.6rem;width:14rem}dt{font-weight:600;margin-top:.75rem}dd{margin-left:0}</style></head>
<body><main><h1>Kodelet user login</h1>
{{if .Principal.Email}}<p>Signed in as <strong>{{.Principal.Email}}</strong>.</p>{{end}}
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Message}}<p class="success">{{.Message}}</p>{{end}}
{{if .EnterCode}}<form method="get"><label>Login code <input type="text" name="user_code" autocomplete="one-time-code" required></label><button type="submit">Continue</button></form>{{end}}
{{with .Authorization}}<p>Approve only if you initiated this login from a Kodelet client.</p><dl><dt>Code</dt><dd><code>{{.UserCode}}</code></dd><dt>Client</dt><dd>{{.ClientName}}</dd><dt>Platform</dt><dd>{{.ClientOS}}/{{.ClientArch}}</dd><dt>Kodelet version</dt><dd>{{.KodeletVersion}}</dd><dt>Expires</dt><dd>{{.ExpiresAt}}</dd></dl>
{{if eq .Status "pending"}}<form method="post"><input type="hidden" name="user_code" value="{{.UserCode}}"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><div class="actions"><button type="submit" name="decision" value="approve">Approve</button><button type="submit" name="decision" value="deny">Deny</button></div></form>{{else}}<p>Status: <strong>{{.Status}}</strong></p>{{end}}{{end}}
</main></body></html>`))

func (s *Server) renderUserLoginVerificationPage(w http.ResponseWriter, statusCode int, data userLoginVerificationPageData) {
	var body bytes.Buffer
	if err := userLoginVerificationPage.Execute(&body, data); err != nil {
		logger.G(context.Background()).WithError(err).Error("failed to render user login verification page")
		http.Error(w, "failed to render user login verification", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_, _ = w.Write(body.Bytes())
}
