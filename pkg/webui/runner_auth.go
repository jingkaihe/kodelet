package webui

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
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
	accessToken, proof, _, validDPoPHeaders := runnerDPoPHeaders(r)

	if mode == RunnerAuthModeNone {
		return runnerregistry.RegistrationPrincipal{Mode: runnerregistry.RegistrationAuthLegacy}, true
	}
	if mode == RunnerAuthModeEnrollment {
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
	if mode == RunnerAuthModeToken {
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
	setAuthApprovalPageHeaders(w)
	if !s.runnerEnrollmentEnabled() || s.authStore == nil {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	_, ok := principalFromContext(r.Context())
	if !ok {
		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	s.handleReactSPA(w, r)
}

func (s *Server) handleRunnerEnrollmentContext(w http.ResponseWriter, r *http.Request) {
	if !s.runnerEnrollmentEnabled() || s.authStore == nil {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusNotFound, "runner enrollment is not enabled", nil)
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	encodeAuthJSON(w, http.StatusOK, principal)
}

type runnerEnrollmentDecisionRequest struct {
	UserCode  string `json:"userCode"`
	Decision  string `json:"decision"`
	CSRFToken string `json:"csrfToken"`
	Replace   bool   `json:"replace,omitempty"`
}

type runnerEnrollmentResponse struct {
	Status         protocol.EnrollmentStatus         `json:"status"`
	UserCode       string                            `json:"userCode"`
	DisplayName    string                            `json:"displayName,omitempty"`
	Host           runnerEnrollmentHostResponse      `json:"host"`
	Workspace      runnerEnrollmentWorkspaceResponse `json:"workspace"`
	KodeletVersion string                            `json:"kodeletVersion,omitempty"`
	Fingerprint    string                            `json:"fingerprint"`
	ExpiresAt      time.Time                         `json:"expiresAt"`
	ReplaceNeeded  bool                              `json:"replaceNeeded"`
}

type runnerEnrollmentHostResponse struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

type runnerEnrollmentWorkspaceResponse struct {
	Path string `json:"path"`
}

type runnerEnrollmentDecisionResponse struct {
	Status     protocol.EnrollmentStatus `json:"status"`
	Enrollment *runnerEnrollmentResponse `json:"enrollment,omitempty"`
	Message    string                    `json:"message,omitempty"`
}

func (s *Server) handleRunnerEnrollmentDecision(w http.ResponseWriter, r *http.Request) {
	if !s.runnerEnrollmentEnabled() || s.authStore == nil {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusNotFound, "runner enrollment is not enabled", nil)
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	if !principal.HasRole(RoleRunnerAdmin) {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusForbidden, "insufficient permissions", nil)
		return
	}
	if !sameOriginEnrollmentDecision(r) {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusForbidden, "invalid enrollment request origin", nil)
		return
	}
	var request runnerEnrollmentDecisionRequest
	if err := decodeRunnerEnrollmentDecisionJSON(w, r, &request); err != nil {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusBadRequest, "invalid enrollment decision", nil)
		return
	}
	if principal.SessionID != "" {
		formToken := strings.TrimSpace(request.CSRFToken)
		cookieToken := csrfCookieValue(r)
		valid, err := s.authStore.WebSessionCSRFValid(r.Context(), principal.SessionID, formToken)
		if err != nil || !valid || !constantTimeStringEqual(formToken, cookieToken) {
			s.writeRunnerEnrollmentDecisionError(w, r, http.StatusForbidden, "invalid CSRF token", nil)
			return
		}
	}
	userCode := strings.TrimSpace(request.UserCode)
	if _, err := normalizeRunnerUserCode(userCode); err != nil {
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusNotFound, "Runner enrollment request not found.", nil)
		return
	}
	decision := strings.TrimSpace(request.Decision)
	switch decision {
	case "lookup":
		enrollment, err := s.authStore.RunnerEnrollmentByUserCode(r.Context(), userCode)
		if err != nil {
			s.writeRunnerEnrollmentStoreError(w, r, err)
			return
		}
		response := runnerEnrollmentResponseFromEnrollment(enrollment)
		encodeAuthJSON(w, http.StatusOK, runnerEnrollmentDecisionResponse{Status: enrollment.Status, Enrollment: &response})
	case "deny":
		if err := s.authStore.DenyRunnerEnrollment(r.Context(), userCode, principal.ID); err != nil {
			s.writeRunnerEnrollmentStoreError(w, r, err)
			return
		}
		encodeAuthJSON(w, http.StatusOK, runnerEnrollmentDecisionResponse{Status: protocol.EnrollmentStatusDenied, Message: "Runner enrollment denied. You can close this page."})
	case "approve":
		enrollment, err := s.authStore.RunnerEnrollmentByUserCode(r.Context(), userCode)
		if err != nil {
			s.writeRunnerEnrollmentStoreError(w, r, err)
			return
		}
		publicKey, err := protocol.EncodePublicKey(enrollment.PublicKey)
		if err != nil {
			s.writeRunnerEnrollmentDecisionError(w, r, http.StatusInternalServerError, "invalid enrollment public key", err)
			return
		}
		replace := request.Replace
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
			s.writeRunnerEnrollmentStoreError(w, r, err)
			return
		}
		enrollment, err = s.authStore.ApproveRunnerEnrollment(r.Context(), userCode, principal.ID, runner.ID, replace)
		if err != nil {
			s.writeRunnerEnrollmentStoreError(w, r, err)
			return
		}
		s.runnerRegistry.DisconnectRunnerExceptCredential(runner.ID, enrollment.CredentialID, errors.New("runner credential was replaced by an approved enrollment"))
		encodeAuthJSON(w, http.StatusOK, runnerEnrollmentDecisionResponse{Status: protocol.EnrollmentStatusApproved, Message: "Runner enrollment approved. You can return to the runner terminal."})
	default:
		s.writeRunnerEnrollmentDecisionError(w, r, http.StatusBadRequest, "invalid enrollment decision", nil)
	}
}

func decodeRunnerEnrollmentDecisionJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
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

func runnerEnrollmentResponseFromEnrollment(enrollment runnerEnrollment) runnerEnrollmentResponse {
	return runnerEnrollmentResponse{
		Status:      enrollment.Status,
		UserCode:    enrollment.UserCode,
		DisplayName: enrollment.DisplayName,
		Host: runnerEnrollmentHostResponse{
			Hostname: enrollment.Host.Hostname,
			OS:       enrollment.Host.OS,
			Arch:     enrollment.Host.Arch,
		},
		Workspace:      runnerEnrollmentWorkspaceResponse{Path: enrollment.Workspace.Path},
		KodeletVersion: enrollment.KodeletVersion,
		Fingerprint:    enrollment.Fingerprint,
		ExpiresAt:      enrollment.ExpiresAt,
		ReplaceNeeded:  enrollment.ReplaceNeeded,
	}
}

func (s *Server) writeRunnerEnrollmentStoreError(w http.ResponseWriter, r *http.Request, err error) {
	statusCode := http.StatusInternalServerError
	message := "Failed to process the runner enrollment request."
	switch {
	case errors.Is(err, errEnrollmentNotFound):
		statusCode = http.StatusNotFound
		message = "Runner enrollment request not found."
	case errors.Is(err, errEnrollmentExpired):
		statusCode = http.StatusConflict
		message = "Runner enrollment request has expired."
	case errors.Is(err, errEnrollmentNotPending):
		statusCode = http.StatusConflict
		message = "Runner enrollment request is no longer pending."
	case errors.Is(err, errRunnerCredentialExists):
		statusCode = http.StatusConflict
		message = "This runner already has an active credential. Confirm replacement to continue."
	case errors.Is(err, runnerregistry.ErrRunnerConnected):
		statusCode = http.StatusConflict
		message = "This runner is currently connected. Stop it before enrolling a new credential, then try again."
	default:
		logger.G(r.Context()).WithError(err).Error("failed to process runner enrollment approval")
	}
	s.writeRunnerEnrollmentDecisionError(w, r, statusCode, message, nil)
}

func (s *Server) writeRunnerEnrollmentDecisionError(w http.ResponseWriter, r *http.Request, statusCode int, message string, err error) {
	if err != nil {
		logger.G(r.Context()).WithError(err).Error(message)
	}
	encodeAuthJSON(w, statusCode, map[string]any{
		"error":   message,
		"status":  statusCode,
		"success": false,
	})
}

func (s *Server) runnerEnrollmentEnabled() bool {
	return s.config.resolvedRunnerAuthMode() == RunnerAuthModeEnrollment
}

func csrfCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(webCSRFCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func requestAbsoluteURL(r *http.Request, path string) string {
	if r == nil {
		return ""
	}
	scheme := "http"
	if isHTTPSRequest(r) {
		scheme = "https"
	}
	host := r.Host
	if forwardedHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); validForwardedHost(forwardedHost) {
		host = forwardedHost
	}
	requestPath := path
	if requestPath != "" {
		if prefix := firstForwardedValue(r.Header.Get("X-Forwarded-Prefix")); validForwardedPrefix(prefix) {
			requestPath = strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(requestPath, "/")
		}
	}
	result := &url.URL{Scheme: scheme, Host: host, Path: requestPath}
	return result.String()
}

func runnerDPoPTargetURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	return requestAbsoluteURL(r, r.URL.Path)
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
