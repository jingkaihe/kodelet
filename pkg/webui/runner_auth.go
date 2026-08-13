package webui

import (
	"context"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

type runnerPrincipalContextKey struct{}

func contextWithRunnerPrincipal(ctx context.Context, principal runnerregistry.RegistrationPrincipal) context.Context {
	return context.WithValue(ctx, runnerPrincipalContextKey{}, principal)
}

func runnerPrincipalFromContext(ctx context.Context) (runnerregistry.RegistrationPrincipal, bool) {
	principal, ok := ctx.Value(runnerPrincipalContextKey{}).(runnerregistry.RegistrationPrincipal)
	return principal, ok
}

func (s *Server) authenticateRunnerRequest(w http.ResponseWriter, r *http.Request) (runnerregistry.RegistrationPrincipal, bool) {
	mode := s.config.resolvedRunnerAuthMode()
	accessToken, proof, hasDPoP, validDPoPHeaders := runnerDPoPHeaders(r)

	if mode == RunnerAuthModeNone {
		return runnerregistry.RegistrationPrincipal{Mode: runnerregistry.RegistrationAuthLegacy}, true
	}
	if mode == RunnerAuthModeEnrollment || (mode == RunnerAuthModeHybrid && hasDPoP) {
		if !validDPoPHeaders || s.authStore == nil {
			setRunnerDPoPAuthenticateHeader(w, "")
			s.writeAuthError(w, r, http.StatusUnauthorized, "runner DPoP authentication required")
			return runnerregistry.RegistrationPrincipal{}, false
		}
		targetURL := runnerDPoPTargetURL(r)
		identity, err := s.authStore.VerifyRunnerDPoP(r.Context(), accessToken, proof, r.Method, targetURL)
		if err != nil {
			errorCode := "invalid_dpop_proof"
			if errors.Is(err, errRunnerCredentialInvalid) {
				errorCode = "invalid_token"
			}
			setRunnerDPoPAuthenticateHeader(w, errorCode)
			s.writeAuthError(w, r, http.StatusUnauthorized, "runner DPoP authentication failed")
			return runnerregistry.RegistrationPrincipal{}, false
		}
		return runnerregistry.RegistrationPrincipal{
			Mode:           runnerregistry.RegistrationAuthKey,
			CredentialID:   identity.CredentialID,
			RunnerID:       identity.RunnerID,
			HostInstanceID: identity.HostInstanceID,
			WorkspacePath:  identity.WorkspacePath,
		}, true
	}
	if mode == RunnerAuthModeToken || (mode == RunnerAuthModeHybrid && !hasDPoP) {
		if s.config.RunnerAuthToken == "" || !constantTimeStringEqual(authHeaderToken(r.Header.Get("Authorization")), s.config.RunnerAuthToken) {
			s.writeAuthError(w, r, http.StatusUnauthorized, "runner authentication required")
			return runnerregistry.RegistrationPrincipal{}, false
		}
		return runnerregistry.RegistrationPrincipal{Mode: runnerregistry.RegistrationAuthLegacy}, true
	}
	s.writeAuthError(w, r, http.StatusUnauthorized, "runner authentication required")
	return runnerregistry.RegistrationPrincipal{}, false
}

func setRunnerDPoPAuthenticateHeader(w http.ResponseWriter, errorCode string) {
	value := `DPoP algs="EdDSA"`
	if errorCode != "" {
		value += `, error="` + errorCode + `"`
	}
	w.Header().Set("WWW-Authenticate", value)
}

func runnerDPoPHeaders(r *http.Request) (accessToken, proof string, present, valid bool) {
	if r == nil {
		return "", "", false, false
	}
	authorizationValues := r.Header.Values("Authorization")
	proofValues := r.Header.Values(protocol.DPoPHeader)
	for _, value := range authorizationValues {
		if strings.HasPrefix(strings.ToLower(value), strings.ToLower(protocol.DPoPAuthorizationScheme)+" ") {
			present = true
			break
		}
	}
	if len(proofValues) > 0 {
		present = true
	}
	if len(authorizationValues) != 1 || len(proofValues) != 1 {
		return "", "", present, false
	}
	authorization := authorizationValues[0]
	if strings.TrimSpace(authorization) != authorization || strings.TrimSpace(proofValues[0]) != proofValues[0] {
		return "", "", present, false
	}
	scheme, token, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(scheme, protocol.DPoPAuthorizationScheme) || strings.ContainsAny(token, " \t\r\n") || protocol.ValidateRunnerAccessToken(token) != nil {
		return "", "", present, false
	}
	proof = proofValues[0]
	if proof == "" || strings.ContainsAny(proof, " \t\r\n,") {
		return "", "", present, false
	}
	return token, proof, true, true
}

func (s *Server) handleStartRunnerEnrollment(w http.ResponseWriter, r *http.Request) {
	if !s.runnerEnrollmentEnabled() || s.authStore == nil {
		s.writeErrorResponse(w, http.StatusNotFound, "runner enrollment is not enabled", nil)
		return
	}
	if !s.allowPublicAuthRequest(r, maxEnrollmentStartsPerWindow) {
		w.Header().Set("Retry-After", strconv.Itoa(int(publicAuthRateWindow/time.Second)))
		s.writeErrorResponse(w, http.StatusTooManyRequests, "too many runner enrollment attempts; retry later", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request protocol.EnrollmentStartRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid runner enrollment request", err)
		return
	}
	if err := request.Validate(); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid runner enrollment request", nil)
		return
	}
	if !protocol.SupportsVersion(request.ProtocolVersions, protocol.Version) {
		s.writeErrorResponse(w, http.StatusBadRequest, "unsupported runner protocol version", nil)
		return
	}
	response, err := s.authStore.StartRunnerEnrollment(r.Context(), request, requestAbsoluteURL(r, "/runner/enroll"))
	if err != nil {
		if errors.Is(err, errTooManyEnrollments) {
			s.writeErrorResponse(w, http.StatusTooManyRequests, "too many runner enrollments are pending", nil)
			return
		}
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to start runner enrollment", err)
		return
	}
	encodeAuthJSON(w, http.StatusCreated, response)
}

func (s *Server) handlePollRunnerEnrollment(w http.ResponseWriter, r *http.Request) {
	if !s.runnerEnrollmentEnabled() || s.authStore == nil {
		s.writeErrorResponse(w, http.StatusNotFound, "runner enrollment is not enabled", nil)
		return
	}
	if !s.allowPublicAuthRequest(r, maxEnrollmentPollsPerWindow) {
		w.Header().Set("Retry-After", strconv.Itoa(int(publicAuthRateWindow/time.Second)))
		s.writeErrorResponse(w, http.StatusTooManyRequests, "too many runner enrollment polls; retry later", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request protocol.EnrollmentPollRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid runner enrollment poll", err)
		return
	}
	if err := request.Validate(); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid runner enrollment poll", nil)
		return
	}
	response, err := s.authStore.PollRunnerEnrollment(r.Context(), request)
	if err != nil {
		var slowDown *enrollmentSlowDownError
		if errors.As(err, &slowDown) {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(slowDown.RetryAfter.Seconds()))))
			encodeAuthJSON(w, http.StatusTooManyRequests, map[string]any{"error": "slow_down", "retryAfterMs": slowDown.RetryAfter.Milliseconds()})
			return
		}
		if errors.Is(err, errEnrollmentNotFound) {
			s.writeErrorResponse(w, http.StatusNotFound, "runner enrollment not found", nil)
			return
		}
		if errors.Is(err, errEnrollmentNotPending) {
			s.writeErrorResponse(w, http.StatusConflict, "runner enrollment is not pending", nil)
			return
		}
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to poll runner enrollment", err)
		return
	}
	encodeAuthJSON(w, http.StatusOK, response)
}

func (s *Server) handleRunnerEnrollmentPage(w http.ResponseWriter, r *http.Request) {
	if !s.runnerEnrollmentEnabled() || s.authStore == nil {
		http.NotFound(w, r)
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	if r.Method == http.MethodPost {
		s.handleRunnerEnrollmentDecision(w, r, principal)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("user_code"))
	if code == "" {
		s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, EnterCode: true, CSRFToken: csrfCookieValue(r)})
		return
	}
	enrollment, err := s.authStore.RunnerEnrollmentByUserCode(r.Context(), code)
	if err != nil {
		s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Error: err.Error(), EnterCode: true, CSRFToken: csrfCookieValue(r)})
		return
	}
	s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Enrollment: &enrollment, CSRFToken: csrfCookieValue(r)})
}

func (s *Server) handleRunnerEnrollmentDecision(w http.ResponseWriter, r *http.Request, principal Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	if err := r.ParseForm(); err != nil {
		s.writeAuthError(w, r, http.StatusBadRequest, "invalid enrollment decision")
		return
	}
	if !sameOriginEnrollmentDecision(r) {
		s.writeAuthError(w, r, http.StatusForbidden, "invalid enrollment request origin")
		return
	}
	if principal.SessionID != "" {
		formToken := strings.TrimSpace(r.FormValue("csrf_token"))
		cookieToken := csrfCookieValue(r)
		valid, err := s.authStore.WebSessionCSRFValid(r.Context(), principal.SessionID, formToken)
		if err != nil || !valid || !constantTimeStringEqual(formToken, cookieToken) {
			s.writeAuthError(w, r, http.StatusForbidden, "invalid CSRF token")
			return
		}
	}
	userCode := strings.TrimSpace(r.FormValue("user_code"))
	decision := strings.TrimSpace(r.FormValue("decision"))
	switch decision {
	case "deny":
		if err := s.authStore.DenyRunnerEnrollment(r.Context(), userCode, principal.ID); err != nil {
			s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Error: err.Error(), EnterCode: true, CSRFToken: csrfCookieValue(r)})
			return
		}
		s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Message: "Runner enrollment denied."})
	case "approve":
		enrollment, err := s.authStore.RunnerEnrollmentByUserCode(r.Context(), userCode)
		if err != nil {
			s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Error: err.Error(), EnterCode: true, CSRFToken: csrfCookieValue(r)})
			return
		}
		publicKey, err := protocol.EncodePublicKey(enrollment.PublicKey)
		if err != nil {
			s.writeAuthError(w, r, http.StatusInternalServerError, "invalid enrollment public key")
			return
		}
		replace := strings.EqualFold(strings.TrimSpace(r.FormValue("replace")), "true") || r.FormValue("replace") == "on"
		runner, err := s.runnerRegistry.EnsureEnrollmentRegistration(protocol.EnrollmentStartRequest{
			ProtocolVersions: []int{protocol.Version},
			PublicKey:        publicKey,
			Fingerprint:      enrollment.Fingerprint,
			Host:             enrollment.Host,
			Workspace:        enrollment.Workspace,
			DisplayName:      enrollment.DisplayName,
			KodeletVersion:   enrollment.KodeletVersion,
		}, replace)
		if err != nil {
			s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Enrollment: &enrollment, Error: err.Error(), CSRFToken: csrfCookieValue(r)})
			return
		}
		enrollment, err = s.authStore.ApproveRunnerEnrollment(r.Context(), userCode, principal.ID, runner.ID, replace)
		if err != nil {
			s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Enrollment: &enrollment, Error: err.Error(), CSRFToken: csrfCookieValue(r)})
			return
		}
		s.runnerRegistry.DisconnectRunnerExceptCredential(runner.ID, enrollment.CredentialID, errors.New("runner credential was replaced by an approved enrollment"))
		s.renderRunnerEnrollmentPage(w, runnerEnrollmentPageData{Principal: principal, Message: "Runner enrollment approved. You can return to the runner terminal."})
	default:
		s.writeAuthError(w, r, http.StatusBadRequest, "invalid enrollment decision")
	}
}

type runnerEnrollmentPageData struct {
	Principal  Principal
	Enrollment *runnerEnrollment
	CSRFToken  string
	EnterCode  bool
	Message    string
	Error      string
}

var runnerEnrollmentPage = template.Must(template.New("runner-enrollment").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Kodelet runner enrollment</title><style>
body{font-family:system-ui,sans-serif;max-width:760px;margin:4rem auto;padding:0 1rem;color:#202020}main{border:1px solid #ddd;border-radius:16px;padding:2rem}code{background:#f4f4f4;padding:.15rem .35rem;border-radius:4px;word-break:break-all}.error{color:#a40000}.success{color:#126b2e}label{display:block;margin:.8rem 0}.actions{display:flex;gap:.75rem;margin-top:1.5rem}button{padding:.65rem 1rem}input[type=text]{font-size:1.1rem;padding:.6rem;width:14rem}</style></head>
<body><main><h1>Kodelet runner enrollment</h1>
{{if .Principal.Email}}<p>Signed in as <strong>{{.Principal.Email}}</strong>.</p>{{end}}
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Message}}<p class="success">{{.Message}}</p>{{end}}
{{if .EnterCode}}<form method="get"><label>Enrollment code <input type="text" name="user_code" autocomplete="one-time-code" required></label><button type="submit">Continue</button></form>{{end}}
{{with .Enrollment}}<dl><dt>Code</dt><dd><code>{{.UserCode}}</code></dd><dt>Runner</dt><dd>{{.DisplayName}}</dd><dt>Host</dt><dd>{{.Host.Hostname}} ({{.Host.OS}}/{{.Host.Arch}})</dd><dt>Workspace</dt><dd><code>{{.Workspace.Path}}</code></dd><dt>Public-key fingerprint</dt><dd><code>{{.Fingerprint}}</code></dd><dt>Expires</dt><dd>{{.ExpiresAt}}</dd></dl>
{{if eq .Status "pending"}}<form method="post"><input type="hidden" name="user_code" value="{{.UserCode}}"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">{{if .ReplaceNeeded}}<label><input type="checkbox" name="replace" value="true" required> Revoke and replace the existing runner credential</label>{{end}}<div class="actions"><button type="submit" name="decision" value="approve">Approve</button><button type="submit" name="decision" value="deny">Deny</button></div></form>{{else}}<p>Status: <strong>{{.Status}}</strong></p>{{end}}{{end}}
</main></body></html>`))

func (s *Server) renderRunnerEnrollmentPage(w http.ResponseWriter, data runnerEnrollmentPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := runnerEnrollmentPage.Execute(w, data); err != nil {
		http.Error(w, "failed to render runner enrollment", http.StatusInternalServerError)
	}
}

func (s *Server) runnerEnrollmentEnabled() bool {
	mode := s.config.resolvedRunnerAuthMode()
	return mode == RunnerAuthModeEnrollment || mode == RunnerAuthModeHybrid
}

func csrfCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(webCSRFCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func requestAbsoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if isHTTPSRequest(r) {
		scheme = "https"
	}
	result := &url.URL{Scheme: scheme, Host: r.Host, Path: path}
	return result.String()
}

func runnerDPoPTargetURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := r.Host
	if forwardedHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); validForwardedHost(forwardedHost) {
		host = forwardedHost
	}
	requestPath := r.URL.Path
	if prefix := firstForwardedValue(r.Header.Get("X-Forwarded-Prefix")); validForwardedPrefix(prefix) {
		requestPath = strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(requestPath, "/")
	}
	scheme := "http"
	if isHTTPSRequest(r) {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: requestPath}).String()
}

func firstForwardedValue(value string) string {
	first, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(first)
}

func validForwardedHost(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n/\\?#") {
		return false
	}
	parsed, err := url.Parse("//" + value)
	return err == nil && parsed.Host == value && parsed.Hostname() != "" && parsed.User == nil
}

func validForwardedPrefix(value string) bool {
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\r\n\\?#") {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Path == value
}

func sameOriginEnrollmentDecision(r *http.Request) bool {
	if r == nil {
		return false
	}
	source := strings.TrimSpace(r.Header.Get("Origin"))
	if source == "" {
		source = strings.TrimSpace(r.Referer())
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return false
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	actual, err := normalizeCORSOrigin(parsed.String())
	if err != nil {
		return false
	}
	expected, err := normalizeCORSOrigin(requestAbsoluteURL(r, ""))
	return err == nil && actual == expected
}

func (s *Server) allowPublicAuthRequest(r *http.Request, limit int) bool {
	if s == nil || r == nil || limit <= 0 {
		return false
	}
	key := r.URL.Path + "\x00" + directPeerAddress(r)
	now := time.Now().UTC()
	s.publicAuthRatesMu.Lock()
	defer s.publicAuthRatesMu.Unlock()
	if s.publicAuthRates == nil {
		s.publicAuthRates = make(map[string]publicAuthRateEntry)
	}
	if entry, ok := s.publicAuthRates[key]; ok {
		if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= publicAuthRateWindow {
			s.publicAuthRates[key] = publicAuthRateEntry{windowStart: now, count: 1}
			return true
		}
		if entry.count >= limit {
			return false
		}
		entry.count++
		s.publicAuthRates[key] = entry
		return true
	}
	if len(s.publicAuthRates) >= maxPublicAuthRateEntries {
		oldestKey := ""
		var oldestStart time.Time
		for candidateKey, candidate := range s.publicAuthRates {
			if candidate.windowStart.IsZero() || now.Sub(candidate.windowStart) >= publicAuthRateWindow {
				delete(s.publicAuthRates, candidateKey)
				continue
			}
			if oldestKey == "" || candidate.windowStart.Before(oldestStart) {
				oldestKey = candidateKey
				oldestStart = candidate.windowStart
			}
		}
		if len(s.publicAuthRates) >= maxPublicAuthRateEntries && oldestKey != "" {
			delete(s.publicAuthRates, oldestKey)
		}
	}
	s.publicAuthRates[key] = publicAuthRateEntry{windowStart: now, count: 1}
	return true
}

func directPeerAddress(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	remoteAddress := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(remoteAddress); err == nil {
		remoteAddress = host
	}
	remoteAddress = strings.Trim(strings.TrimSpace(remoteAddress), "[]")
	if ip := net.ParseIP(remoteAddress); ip != nil {
		return ip.String()
	}
	if remoteAddress == "" {
		return "unknown"
	}
	return strings.ToLower(remoteAddress)
}
