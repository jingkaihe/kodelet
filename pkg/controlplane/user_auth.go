package controlplane

import (
	"encoding/json"
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
	setAuthApprovalPageHeaders(w)
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
	s.serveFrontend(w, r)
}

func (s *Server) handleUserLoginContext(w http.ResponseWriter, r *http.Request) {
	if !s.userLoginEnabled() {
		s.writeUserAuthAPIError(w, r, http.StatusNotFound, "user login is not enabled", nil)
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeUserAuthAPIError(w, r, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	if !isGenuineOIDCWebPrincipal(principal) {
		s.writeUserAuthAPIError(w, r, http.StatusForbidden, "an active OIDC web session is required", nil)
		return
	}
	encodeAuthJSON(w, http.StatusOK, principal)
}

type userLoginDecisionRequest struct {
	UserCode  string `json:"userCode"`
	Decision  string `json:"decision"`
	CSRFToken string `json:"csrfToken"`
}

type userLoginAuthorizationResponse struct {
	Status         userauth.DeviceStatus `json:"status"`
	UserCode       string                `json:"userCode"`
	ClientName     string                `json:"clientName"`
	ClientOS       string                `json:"clientOS"`
	ClientArch     string                `json:"clientArch"`
	KodeletVersion string                `json:"kodeletVersion"`
	ExpiresAt      time.Time             `json:"expiresAt"`
}

type userLoginDecisionResponse struct {
	Status        userauth.DeviceStatus           `json:"status"`
	Authorization *userLoginAuthorizationResponse `json:"authorization,omitempty"`
	Message       string                          `json:"message,omitempty"`
}

func (s *Server) handleUserLoginDecision(w http.ResponseWriter, r *http.Request) {
	if !s.userLoginEnabled() {
		s.writeUserAuthAPIError(w, r, http.StatusNotFound, "user login is not enabled", nil)
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeUserAuthAPIError(w, r, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	if !isGenuineOIDCWebPrincipal(principal) {
		s.writeUserAuthAPIError(w, r, http.StatusForbidden, "an active OIDC web session is required", nil)
		return
	}
	if !sameOriginUserLoginDecision(r, s.config) {
		s.writeUserAuthAPIError(w, r, http.StatusForbidden, "invalid user login request origin", nil)
		return
	}
	var request userLoginDecisionRequest
	if err := decodeUserAuthJSON(w, r, maxUserLoginDecisionBytes, &request); err != nil {
		s.writeUserAuthAPIError(w, r, http.StatusBadRequest, "invalid user login decision", nil)
		return
	}

	formToken := strings.TrimSpace(request.CSRFToken)
	cookieToken := csrfCookieValue(r)
	valid, err := s.authStore.WebSessionCSRFValid(r.Context(), principal.SessionID, formToken)
	if err != nil {
		logger.G(r.Context()).WithError(err).Error("failed to validate user login CSRF token")
		s.writeUserAuthAPIError(w, r, http.StatusInternalServerError, "failed to validate user login decision", nil)
		return
	}
	if formToken == "" || cookieToken == "" || !valid || !constantTimeStringEqual(formToken, cookieToken) {
		s.writeUserAuthAPIError(w, r, http.StatusForbidden, "invalid CSRF token", nil)
		return
	}

	userCode := strings.TrimSpace(request.UserCode)
	if _, err := normalizeUserCode(userCode); err != nil {
		s.writeUserAuthAPIError(w, r, http.StatusNotFound, "User login request not found.", nil)
		return
	}
	decision := strings.TrimSpace(request.Decision)
	switch decision {
	case "lookup":
		authorization, err := s.authStore.UserLoginByUserCode(r.Context(), userCode)
		if err != nil {
			s.writeUserLoginDecisionError(w, r, err)
			return
		}
		encodeAuthJSON(w, http.StatusOK, userLoginDecisionResponse{
			Status: authorization.Status,
			Authorization: &userLoginAuthorizationResponse{
				Status:         authorization.Status,
				UserCode:       authorization.UserCode,
				ClientName:     authorization.ClientName,
				ClientOS:       authorization.ClientOS,
				ClientArch:     authorization.ClientArch,
				KodeletVersion: authorization.KodeletVersion,
				ExpiresAt:      authorization.ExpiresAt,
			},
		})
	case "approve":
		if _, err := s.authStore.ApproveUserLogin(r.Context(), userCode, principal, s.config.OIDC.SessionDuration); err != nil {
			s.writeUserLoginDecisionError(w, r, err)
			return
		}
		encodeAuthJSON(w, http.StatusOK, userLoginDecisionResponse{
			Status:  userauth.DeviceStatusApproved,
			Message: "Sign-in approved. You can return to the Kodelet client.",
		})
	case "deny":
		if err := s.authStore.DenyUserLogin(r.Context(), userCode, principal.ID); err != nil {
			s.writeUserLoginDecisionError(w, r, err)
			return
		}
		encodeAuthJSON(w, http.StatusOK, userLoginDecisionResponse{
			Status:  userauth.DeviceStatusDenied,
			Message: "Sign-in denied. You can close this page.",
		})
	default:
		s.writeUserAuthAPIError(w, r, http.StatusBadRequest, "invalid user login decision", nil)
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

func (s *Server) writeUserLoginDecisionError(w http.ResponseWriter, r *http.Request, err error) {
	statusCode := http.StatusInternalServerError
	message := "Failed to process the user login request."
	switch {
	case errors.Is(err, errUserAuthorizationNotFound):
		statusCode = http.StatusNotFound
		message = "User login request not found."
	case errors.Is(err, errUserAuthorizationExpired):
		statusCode = http.StatusConflict
		message = "User login request has expired."
	case errors.Is(err, errUserAuthorizationNotPending):
		statusCode = http.StatusConflict
		message = "User login request is no longer pending."
	default:
		logger.G(r.Context()).WithError(err).Error("failed to process user login verification")
	}
	s.writeUserAuthAPIError(w, r, statusCode, message, nil)
}
