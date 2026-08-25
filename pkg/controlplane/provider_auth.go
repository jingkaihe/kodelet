package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	providerauth "github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

type codexProviderAuthService interface {
	Connected() (bool, error)
	RequestDeviceCode(context.Context) (*providerauth.CodexDeviceCode, error)
	CompleteDeviceCodeLogin(context.Context, *providerauth.CodexDeviceCode) (*providerauth.CodexCredentials, error)
	SaveCredentials(*providerauth.CodexCredentials) error
}

type defaultCodexProviderAuthService struct{}

func (defaultCodexProviderAuthService) Connected() (bool, error) {
	exists, err := providerauth.GetCodexCredentialsExists()
	if err != nil || !exists {
		return false, err
	}
	credentials, err := providerauth.GetCodexCredentials()
	if err != nil {
		return false, err
	}
	return providerauth.IsCodexOAuthEnabled(credentials), nil
}

func (defaultCodexProviderAuthService) RequestDeviceCode(ctx context.Context) (*providerauth.CodexDeviceCode, error) {
	return providerauth.RequestCodexDeviceCode(ctx)
}

func (defaultCodexProviderAuthService) CompleteDeviceCodeLogin(ctx context.Context, deviceCode *providerauth.CodexDeviceCode) (*providerauth.CodexCredentials, error) {
	return providerauth.CompleteCodexDeviceCodeLogin(ctx, deviceCode)
}

func (defaultCodexProviderAuthService) SaveCredentials(credentials *providerauth.CodexCredentials) error {
	_, err := providerauth.SaveCodexCredentials(credentials)
	return err
}

type codexDeviceLoginStatus string

const (
	codexDeviceLoginStarting  codexDeviceLoginStatus = "starting"
	codexDeviceLoginPending   codexDeviceLoginStatus = "pending"
	codexDeviceLoginConnected codexDeviceLoginStatus = "connected"
	codexDeviceLoginFailed    codexDeviceLoginStatus = "failed"
	codexDeviceLoginCanceled  codexDeviceLoginStatus = "canceled"

	codexDeviceLoginTimeout     = 15 * time.Minute
	codexDeviceCodeRequestLimit = 30 * time.Second
)

type codexDeviceLoginSession struct {
	ID              string
	Status          codexDeviceLoginStatus
	VerificationURL string
	UserCode        string
	Message         string
	cancel          context.CancelFunc
}

type codexProviderResponse struct {
	Provider  string `json:"provider"`
	Connected bool   `json:"connected"`
}

type codexDeviceLoginResponse struct {
	ID              string                 `json:"id"`
	Status          codexDeviceLoginStatus `json:"status"`
	VerificationURL string                 `json:"verificationUrl,omitempty"`
	UserCode        string                 `json:"userCode,omitempty"`
	Message         string                 `json:"message,omitempty"`
}

func (s *Server) codexProviderAuth() codexProviderAuthService {
	if s.codexAuth != nil {
		return s.codexAuth
	}
	return defaultCodexProviderAuthService{}
}

func (s *Server) handleGetCodexProvider(w http.ResponseWriter, _ *http.Request) {
	setCodexProviderResponseHeaders(w)
	connected, err := s.codexProviderAuth().Connected()
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read Codex provider status", err)
		return
	}
	s.writeJSONResponse(w, codexProviderResponse{Provider: "codex", Connected: connected})
}

func (s *Server) handleStartCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexProviderResponseHeaders(w)
	loginID, err := newAuthID("codex_login")
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to start Codex device login", err)
		return
	}

	session := &codexDeviceLoginSession{ID: loginID, Status: codexDeviceLoginStarting}
	s.codexDeviceLoginMu.Lock()
	if codexDeviceLoginActive(s.codexDeviceLogin) {
		s.codexDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "Codex device login is already in progress", nil)
		return
	}
	s.codexDeviceLogin = session
	s.codexDeviceLoginMu.Unlock()

	deviceCodeCtx, cancelDeviceCode := context.WithTimeout(r.Context(), codexDeviceCodeRequestLimit)
	deviceCode, err := s.codexProviderAuth().RequestDeviceCode(deviceCodeCtx)
	cancelDeviceCode()
	if err != nil {
		s.codexDeviceLoginMu.Lock()
		if s.codexDeviceLogin != nil && s.codexDeviceLogin.ID == loginID {
			s.codexDeviceLogin = nil
		}
		s.codexDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusBadGateway, "failed to request a ChatGPT device code", err)
		return
	}

	baseCtx := s.runCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	baseCtx = logger.WithLogger(baseCtx, logger.G(r.Context()))
	loginCtx, cancelLogin := context.WithTimeout(baseCtx, codexDeviceLoginTimeout)

	s.codexDeviceLoginMu.Lock()
	if s.codexDeviceLogin == nil || s.codexDeviceLogin.ID != loginID {
		s.codexDeviceLoginMu.Unlock()
		cancelLogin()
		s.writeErrorResponse(w, http.StatusConflict, "Codex device login is no longer available", nil)
		return
	}
	session.Status = codexDeviceLoginPending
	session.VerificationURL = deviceCode.VerificationURL
	session.UserCode = deviceCode.UserCode
	session.cancel = cancelLogin
	response := codexDeviceLoginResponseFromSession(session)
	s.codexDeviceLoginMu.Unlock()

	go func() {
		defer cancelLogin()
		s.completeCodexDeviceLogin(loginCtx, loginID, deviceCode)
	}()
	s.writeJSONResponse(w, response)
}

func (s *Server) completeCodexDeviceLogin(ctx context.Context, loginID string, deviceCode *providerauth.CodexDeviceCode) {
	defer func() {
		s.codexDeviceLoginMu.Lock()
		if s.codexDeviceLogin != nil && s.codexDeviceLogin.ID == loginID {
			s.codexDeviceLogin.cancel = nil
		}
		s.codexDeviceLoginMu.Unlock()
	}()

	credentials, err := s.codexProviderAuth().CompleteDeviceCodeLogin(ctx, deviceCode)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.finishCodexDeviceLogin(loginID, codexDeviceLoginCanceled, "ChatGPT sign-in was canceled.")
			return
		}
		logger.G(ctx).WithError(err).Error("failed to complete Codex device login")
		s.finishCodexDeviceLogin(loginID, codexDeviceLoginFailed, "Could not complete ChatGPT sign-in. Please try again.")
		return
	}

	if err := s.codexProviderAuth().SaveCredentials(credentials); err != nil {
		logger.G(ctx).WithError(err).Error("failed to save Codex credentials")
		s.finishCodexDeviceLogin(loginID, codexDeviceLoginFailed, "Could not save the ChatGPT connection. Please try again.")
		return
	}
	s.finishCodexDeviceLogin(loginID, codexDeviceLoginConnected, "ChatGPT subscription connected.")
}

func (s *Server) finishCodexDeviceLogin(loginID string, status codexDeviceLoginStatus, message string) {
	s.codexDeviceLoginMu.Lock()
	defer s.codexDeviceLoginMu.Unlock()
	if s.codexDeviceLogin == nil || s.codexDeviceLogin.ID != loginID {
		return
	}
	s.codexDeviceLogin.Status = status
	s.codexDeviceLogin.Message = message
}

func (s *Server) handleGetCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexProviderResponseHeaders(w)
	loginID := strings.TrimSpace(mux.Vars(r)["id"])
	if loginID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "Codex device login ID is required", nil)
		return
	}

	s.codexDeviceLoginMu.Lock()
	if s.codexDeviceLogin == nil || s.codexDeviceLogin.ID != loginID {
		s.codexDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "Codex device login not found", nil)
		return
	}
	response := codexDeviceLoginResponseFromSession(s.codexDeviceLogin)
	s.codexDeviceLoginMu.Unlock()
	s.writeJSONResponse(w, response)
}

func (s *Server) handleCancelCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexProviderResponseHeaders(w)
	loginID := strings.TrimSpace(mux.Vars(r)["id"])
	if loginID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "Codex device login ID is required", nil)
		return
	}

	s.codexDeviceLoginMu.Lock()
	if s.codexDeviceLogin == nil || s.codexDeviceLogin.ID != loginID {
		s.codexDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "Codex device login not found", nil)
		return
	}
	cancel := s.codexDeviceLogin.cancel
	s.codexDeviceLoginMu.Unlock()

	if cancel != nil {
		cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

func codexDeviceLoginActive(session *codexDeviceLoginSession) bool {
	return session != nil && (session.Status == codexDeviceLoginStarting || session.Status == codexDeviceLoginPending)
}

func codexDeviceLoginResponseFromSession(session *codexDeviceLoginSession) codexDeviceLoginResponse {
	return codexDeviceLoginResponse{
		ID:              session.ID,
		Status:          session.Status,
		VerificationURL: session.VerificationURL,
		UserCode:        session.UserCode,
		Message:         session.Message,
	}
}

func setCodexProviderResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
