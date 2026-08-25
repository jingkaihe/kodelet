package controlplane

import (
	"context"
	"encoding/json"
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

type copilotProviderAuthService interface {
	Connected() (bool, error)
	RequestDeviceCode(context.Context) (*providerauth.CopilotDeviceCodeResponse, error)
	CompleteDeviceCodeLogin(context.Context, *providerauth.CopilotDeviceCodeResponse) (*providerauth.CopilotCredentials, error)
	SaveCredentials(*providerauth.CopilotCredentials) error
}

type defaultCopilotProviderAuthService struct{}

func (defaultCopilotProviderAuthService) Connected() (bool, error) {
	return providerauth.GetCopilotCredentialsExists()
}

func (defaultCopilotProviderAuthService) RequestDeviceCode(ctx context.Context) (*providerauth.CopilotDeviceCodeResponse, error) {
	return providerauth.GenerateCopilotDeviceFlow(ctx)
}

func (defaultCopilotProviderAuthService) CompleteDeviceCodeLogin(ctx context.Context, deviceCode *providerauth.CopilotDeviceCodeResponse) (*providerauth.CopilotCredentials, error) {
	token, err := providerauth.PollCopilotToken(ctx, deviceCode.DeviceCode, deviceCode.Interval)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get GitHub OAuth access token")
	}
	copilotToken, err := providerauth.ExchangeCopilotToken(ctx, token.AccessToken)
	if err != nil {
		return nil, errors.Wrap(err, "failed to exchange GitHub Copilot token")
	}
	return &providerauth.CopilotCredentials{
		AccessToken:    token.AccessToken,
		CopilotToken:   copilotToken.Token,
		Scope:          token.Scope,
		CopilotExpires: copilotToken.ExpiresAt,
	}, nil
}

func (defaultCopilotProviderAuthService) SaveCredentials(credentials *providerauth.CopilotCredentials) error {
	_, err := providerauth.SaveCopilotCredentials(credentials)
	return err
}

type anthropicProviderAuthService interface {
	Connected() (bool, error)
	GenerateAuthURL() (authURL string, verifier string, err error)
	ExchangeCode(context.Context, string, string) (*providerauth.AnthropicCredentials, error)
	SaveCredentials(*providerauth.AnthropicCredentials) error
}

type defaultAnthropicProviderAuthService struct{}

func (defaultAnthropicProviderAuthService) Connected() (bool, error) {
	accounts, err := providerauth.ListAnthropicAccounts()
	if err != nil {
		return false, err
	}
	for _, account := range accounts {
		if account.IsDefault {
			return true, nil
		}
	}
	return false, nil
}

func (defaultAnthropicProviderAuthService) GenerateAuthURL() (string, string, error) {
	return providerauth.GenerateAnthropicAuthURL()
}

func (defaultAnthropicProviderAuthService) ExchangeCode(ctx context.Context, code string, verifier string) (*providerauth.AnthropicCredentials, error) {
	return providerauth.ExchangeAnthropicCode(ctx, code, verifier)
}

func (defaultAnthropicProviderAuthService) SaveCredentials(credentials *providerauth.AnthropicCredentials) error {
	accounts, err := providerauth.ListAnthropicAccounts()
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if account.IsDefault {
			_, err = providerauth.SaveAnthropicCredentialsWithAlias(account.Alias, credentials)
			return err
		}
	}
	_, err = providerauth.SaveAnthropicCredentials(credentials)
	return err
}

type codexDeviceLoginStatus string

const (
	codexDeviceLoginStarting  codexDeviceLoginStatus = "starting"
	codexDeviceLoginPending   codexDeviceLoginStatus = "pending"
	codexDeviceLoginConnected codexDeviceLoginStatus = "connected"
	codexDeviceLoginFailed    codexDeviceLoginStatus = "failed"
	codexDeviceLoginCanceled  codexDeviceLoginStatus = "canceled"

	codexDeviceLoginTimeout       = 15 * time.Minute
	codexDeviceCodeRequestLimit   = 30 * time.Second
	copilotDeviceCodeRequestLimit = 30 * time.Second
	anthropicOAuthLoginTimeout    = 15 * time.Minute
	anthropicOAuthExchangeLimit   = 30 * time.Second
	providerLoginBodyLimit        = 16 << 10
)

type codexDeviceLoginSession struct {
	ID              string
	Status          codexDeviceLoginStatus
	VerificationURL string
	UserCode        string
	Message         string
	cancel          context.CancelFunc
}

type codexDeviceLoginResponse struct {
	ID              string                 `json:"id"`
	Status          codexDeviceLoginStatus `json:"status"`
	VerificationURL string                 `json:"verificationUrl,omitempty"`
	UserCode        string                 `json:"userCode,omitempty"`
	Message         string                 `json:"message,omitempty"`
}

type copilotDeviceLoginStatus string

const (
	copilotDeviceLoginStarting  copilotDeviceLoginStatus = "starting"
	copilotDeviceLoginPending   copilotDeviceLoginStatus = "pending"
	copilotDeviceLoginConnected copilotDeviceLoginStatus = "connected"
	copilotDeviceLoginFailed    copilotDeviceLoginStatus = "failed"
	copilotDeviceLoginCanceled  copilotDeviceLoginStatus = "canceled"
)

type copilotDeviceLoginSession struct {
	ID              string
	Status          copilotDeviceLoginStatus
	VerificationURL string
	UserCode        string
	Message         string
	cancel          context.CancelFunc
}

type copilotDeviceLoginResponse struct {
	ID              string                   `json:"id"`
	Status          copilotDeviceLoginStatus `json:"status"`
	VerificationURL string                   `json:"verificationUrl,omitempty"`
	UserCode        string                   `json:"userCode,omitempty"`
	Message         string                   `json:"message,omitempty"`
}

type anthropicOAuthLoginStatus string

const (
	anthropicOAuthLoginStarting   anthropicOAuthLoginStatus = "starting"
	anthropicOAuthLoginPending    anthropicOAuthLoginStatus = "pending"
	anthropicOAuthLoginCompleting anthropicOAuthLoginStatus = "completing"
	anthropicOAuthLoginConnected  anthropicOAuthLoginStatus = "connected"
	anthropicOAuthLoginFailed     anthropicOAuthLoginStatus = "failed"
	anthropicOAuthLoginCanceled   anthropicOAuthLoginStatus = "canceled"
)

type anthropicOAuthLoginSession struct {
	ID               string
	Status           anthropicOAuthLoginStatus
	AuthorizationURL string
	Verifier         string
	Message          string
	ExpiresAt        time.Time
}

type providerConnectionResponse struct {
	Provider  string `json:"provider"`
	Connected bool   `json:"connected"`
}

type anthropicOAuthLoginResponse struct {
	ID               string                    `json:"id"`
	Status           anthropicOAuthLoginStatus `json:"status"`
	AuthorizationURL string                    `json:"authorizationUrl,omitempty"`
	Message          string                    `json:"message,omitempty"`
}

type completeAnthropicOAuthLoginRequest struct {
	Code string `json:"code"`
}

func (s *Server) codexProviderAuth() codexProviderAuthService {
	if s.codexAuth != nil {
		return s.codexAuth
	}
	return defaultCodexProviderAuthService{}
}

func (s *Server) copilotProviderAuth() copilotProviderAuthService {
	if s.copilotAuth != nil {
		return s.copilotAuth
	}
	return defaultCopilotProviderAuthService{}
}

func (s *Server) anthropicProviderAuth() anthropicProviderAuthService {
	if s.anthropicAuth != nil {
		return s.anthropicAuth
	}
	return defaultAnthropicProviderAuthService{}
}

func (s *Server) handleGetCodexProvider(w http.ResponseWriter, _ *http.Request) {
	setProviderResponseHeaders(w)
	connected, err := s.codexProviderAuth().Connected()
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read Codex provider status", err)
		return
	}
	s.writeJSONResponse(w, providerConnectionResponse{Provider: "codex", Connected: connected})
}

func (s *Server) handleStartCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
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
	setProviderResponseHeaders(w)
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
	setProviderResponseHeaders(w)
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

func (s *Server) handleGetCopilotProvider(w http.ResponseWriter, _ *http.Request) {
	setProviderResponseHeaders(w)
	connected, err := s.copilotProviderAuth().Connected()
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read GitHub Copilot provider status", err)
		return
	}
	s.writeJSONResponse(w, providerConnectionResponse{Provider: "copilot", Connected: connected})
}

func (s *Server) handleStartCopilotDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	loginID, err := newAuthID("copilot_login")
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to start GitHub Copilot device login", err)
		return
	}

	session := &copilotDeviceLoginSession{ID: loginID, Status: copilotDeviceLoginStarting}
	s.copilotDeviceLoginMu.Lock()
	if copilotDeviceLoginActive(s.copilotDeviceLogin) {
		s.copilotDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "GitHub Copilot device login is already in progress", nil)
		return
	}
	s.copilotDeviceLogin = session
	s.copilotDeviceLoginMu.Unlock()

	deviceCodeCtx, cancelDeviceCode := context.WithTimeout(r.Context(), copilotDeviceCodeRequestLimit)
	deviceCode, err := s.copilotProviderAuth().RequestDeviceCode(deviceCodeCtx)
	cancelDeviceCode()
	if err != nil {
		s.copilotDeviceLoginMu.Lock()
		if s.copilotDeviceLogin != nil && s.copilotDeviceLogin.ID == loginID {
			s.copilotDeviceLogin = nil
		}
		s.copilotDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusBadGateway, "failed to request a GitHub Copilot device code", err)
		return
	}

	baseCtx := s.runCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	baseCtx = logger.WithLogger(baseCtx, logger.G(r.Context()))
	loginCtx, cancelLogin := context.WithTimeout(baseCtx, time.Duration(deviceCode.ExpiresIn)*time.Second)

	s.copilotDeviceLoginMu.Lock()
	if s.copilotDeviceLogin == nil || s.copilotDeviceLogin.ID != loginID {
		s.copilotDeviceLoginMu.Unlock()
		cancelLogin()
		s.writeErrorResponse(w, http.StatusConflict, "GitHub Copilot device login is no longer available", nil)
		return
	}
	session.Status = copilotDeviceLoginPending
	session.VerificationURL = deviceCode.VerificationURI
	session.UserCode = deviceCode.UserCode
	session.cancel = cancelLogin
	response := copilotDeviceLoginResponseFromSession(session)
	s.copilotDeviceLoginMu.Unlock()

	go func() {
		defer cancelLogin()
		s.completeCopilotDeviceLogin(loginCtx, loginID, deviceCode)
	}()
	s.writeJSONResponse(w, response)
}

func (s *Server) completeCopilotDeviceLogin(ctx context.Context, loginID string, deviceCode *providerauth.CopilotDeviceCodeResponse) {
	defer func() {
		s.copilotDeviceLoginMu.Lock()
		if s.copilotDeviceLogin != nil && s.copilotDeviceLogin.ID == loginID {
			s.copilotDeviceLogin.cancel = nil
		}
		s.copilotDeviceLoginMu.Unlock()
	}()

	credentials, err := s.copilotProviderAuth().CompleteDeviceCodeLogin(ctx, deviceCode)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.finishCopilotDeviceLogin(loginID, copilotDeviceLoginCanceled, "GitHub Copilot sign-in was canceled.")
			return
		}
		logger.G(ctx).WithError(err).Error("failed to complete GitHub Copilot device login")
		s.finishCopilotDeviceLogin(loginID, copilotDeviceLoginFailed, "Could not complete GitHub Copilot sign-in. Please try again.")
		return
	}

	if err := s.copilotProviderAuth().SaveCredentials(credentials); err != nil {
		logger.G(ctx).WithError(err).Error("failed to save GitHub Copilot credentials")
		s.finishCopilotDeviceLogin(loginID, copilotDeviceLoginFailed, "Could not save the GitHub Copilot connection. Please try again.")
		return
	}
	s.finishCopilotDeviceLogin(loginID, copilotDeviceLoginConnected, "GitHub Copilot subscription connected.")
}

func (s *Server) finishCopilotDeviceLogin(loginID string, status copilotDeviceLoginStatus, message string) {
	s.copilotDeviceLoginMu.Lock()
	defer s.copilotDeviceLoginMu.Unlock()
	if s.copilotDeviceLogin == nil || s.copilotDeviceLogin.ID != loginID {
		return
	}
	s.copilotDeviceLogin.Status = status
	s.copilotDeviceLogin.Message = message
}

func (s *Server) handleGetCopilotDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	loginID := strings.TrimSpace(mux.Vars(r)["id"])
	if loginID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "GitHub Copilot device login ID is required", nil)
		return
	}

	s.copilotDeviceLoginMu.Lock()
	if s.copilotDeviceLogin == nil || s.copilotDeviceLogin.ID != loginID {
		s.copilotDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "GitHub Copilot device login not found", nil)
		return
	}
	response := copilotDeviceLoginResponseFromSession(s.copilotDeviceLogin)
	s.copilotDeviceLoginMu.Unlock()
	s.writeJSONResponse(w, response)
}

func (s *Server) handleCancelCopilotDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	loginID := strings.TrimSpace(mux.Vars(r)["id"])
	if loginID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "GitHub Copilot device login ID is required", nil)
		return
	}

	s.copilotDeviceLoginMu.Lock()
	if s.copilotDeviceLogin == nil || s.copilotDeviceLogin.ID != loginID {
		s.copilotDeviceLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "GitHub Copilot device login not found", nil)
		return
	}
	cancel := s.copilotDeviceLogin.cancel
	s.copilotDeviceLoginMu.Unlock()

	if cancel != nil {
		cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

func copilotDeviceLoginActive(session *copilotDeviceLoginSession) bool {
	return session != nil && (session.Status == copilotDeviceLoginStarting || session.Status == copilotDeviceLoginPending)
}

func copilotDeviceLoginResponseFromSession(session *copilotDeviceLoginSession) copilotDeviceLoginResponse {
	return copilotDeviceLoginResponse{
		ID:              session.ID,
		Status:          session.Status,
		VerificationURL: session.VerificationURL,
		UserCode:        session.UserCode,
		Message:         session.Message,
	}
}

func (s *Server) handleGetAnthropicProvider(w http.ResponseWriter, _ *http.Request) {
	setProviderResponseHeaders(w)
	connected, err := s.anthropicProviderAuth().Connected()
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read Anthropic provider status", err)
		return
	}
	s.writeJSONResponse(w, providerConnectionResponse{Provider: "anthropic", Connected: connected})
}

func (s *Server) handleStartAnthropicOAuthLogin(w http.ResponseWriter, _ *http.Request) {
	setProviderResponseHeaders(w)
	loginID, err := newAuthID("anthropic_login")
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to start Anthropic login", err)
		return
	}

	session := &anthropicOAuthLoginSession{
		ID:        loginID,
		Status:    anthropicOAuthLoginStarting,
		ExpiresAt: time.Now().Add(anthropicOAuthLoginTimeout),
	}
	s.anthropicOAuthLoginMu.Lock()
	if anthropicOAuthLoginActive(s.anthropicOAuthLogin) {
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "Anthropic login is already in progress", nil)
		return
	}
	s.anthropicOAuthLogin = session
	s.anthropicOAuthLoginMu.Unlock()

	authorizationURL, verifier, err := s.anthropicProviderAuth().GenerateAuthURL()
	if err != nil {
		s.anthropicOAuthLoginMu.Lock()
		if s.anthropicOAuthLogin != nil && s.anthropicOAuthLogin.ID == loginID {
			s.anthropicOAuthLogin = nil
		}
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to generate Anthropic authorization URL", err)
		return
	}

	s.anthropicOAuthLoginMu.Lock()
	if s.anthropicOAuthLogin == nil || s.anthropicOAuthLogin.ID != loginID {
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "Anthropic login is no longer available", nil)
		return
	}
	session.Status = anthropicOAuthLoginPending
	session.AuthorizationURL = authorizationURL
	session.Verifier = verifier
	response := anthropicOAuthLoginResponseFromSession(session)
	s.anthropicOAuthLoginMu.Unlock()
	s.writeJSONResponse(w, response)
}

func (s *Server) handleCompleteAnthropicOAuthLogin(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	loginID := strings.TrimSpace(mux.Vars(r)["id"])
	if loginID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "Anthropic login ID is required", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, providerLoginBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request completeAnthropicOAuthLoginRequest
	if err := decoder.Decode(&request); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid Anthropic login request", err)
		return
	}
	request.Code = strings.TrimSpace(request.Code)
	if request.Code == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "Anthropic authorization code is required", nil)
		return
	}

	s.anthropicOAuthLoginMu.Lock()
	if s.anthropicOAuthLogin == nil || s.anthropicOAuthLogin.ID != loginID {
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "Anthropic login not found", nil)
		return
	}
	if s.anthropicOAuthLogin.Status != anthropicOAuthLoginPending {
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "Anthropic login is not awaiting a code", nil)
		return
	}
	s.anthropicOAuthLogin.Status = anthropicOAuthLoginCompleting
	verifier := s.anthropicOAuthLogin.Verifier
	s.anthropicOAuthLoginMu.Unlock()

	exchangeCtx, cancelExchange := context.WithTimeout(r.Context(), anthropicOAuthExchangeLimit)
	credentials, err := s.anthropicProviderAuth().ExchangeCode(exchangeCtx, request.Code, verifier)
	cancelExchange()
	if err != nil {
		logger.G(r.Context()).WithError(err).Error("failed to complete Anthropic OAuth login")
		s.finishAnthropicOAuthLogin(loginID, anthropicOAuthLoginFailed, "Could not complete Anthropic sign-in. Please try again.")
		s.writeAnthropicOAuthLoginResponse(w, loginID)
		return
	}

	if err := s.anthropicProviderAuth().SaveCredentials(credentials); err != nil {
		logger.G(r.Context()).WithError(err).Error("failed to save Anthropic credentials")
		s.finishAnthropicOAuthLogin(loginID, anthropicOAuthLoginFailed, "Could not save the Anthropic connection. Please try again.")
		s.writeAnthropicOAuthLoginResponse(w, loginID)
		return
	}

	s.finishAnthropicOAuthLogin(loginID, anthropicOAuthLoginConnected, "Anthropic subscription connected.")
	s.writeAnthropicOAuthLoginResponse(w, loginID)
}

func (s *Server) handleCancelAnthropicOAuthLogin(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	loginID := strings.TrimSpace(mux.Vars(r)["id"])
	if loginID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "Anthropic login ID is required", nil)
		return
	}

	s.anthropicOAuthLoginMu.Lock()
	if s.anthropicOAuthLogin == nil || s.anthropicOAuthLogin.ID != loginID {
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "Anthropic login not found", nil)
		return
	}
	if s.anthropicOAuthLogin.Status == anthropicOAuthLoginCompleting {
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "Anthropic login is already completing", nil)
		return
	}
	s.anthropicOAuthLogin.Status = anthropicOAuthLoginCanceled
	s.anthropicOAuthLogin.Message = "Anthropic sign-in was canceled."
	s.anthropicOAuthLoginMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) finishAnthropicOAuthLogin(loginID string, status anthropicOAuthLoginStatus, message string) {
	s.anthropicOAuthLoginMu.Lock()
	defer s.anthropicOAuthLoginMu.Unlock()
	if s.anthropicOAuthLogin == nil || s.anthropicOAuthLogin.ID != loginID {
		return
	}
	s.anthropicOAuthLogin.Status = status
	s.anthropicOAuthLogin.Message = message
}

func (s *Server) writeAnthropicOAuthLoginResponse(w http.ResponseWriter, loginID string) {
	s.anthropicOAuthLoginMu.Lock()
	if s.anthropicOAuthLogin == nil || s.anthropicOAuthLogin.ID != loginID {
		s.anthropicOAuthLoginMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "Anthropic login not found", nil)
		return
	}
	response := anthropicOAuthLoginResponseFromSession(s.anthropicOAuthLogin)
	s.anthropicOAuthLoginMu.Unlock()
	s.writeJSONResponse(w, response)
}

func anthropicOAuthLoginActive(session *anthropicOAuthLoginSession) bool {
	if session == nil {
		return false
	}
	if session.Status == anthropicOAuthLoginStarting || session.Status == anthropicOAuthLoginCompleting {
		return true
	}
	return session.Status == anthropicOAuthLoginPending && time.Now().Before(session.ExpiresAt)
}

func anthropicOAuthLoginResponseFromSession(session *anthropicOAuthLoginSession) anthropicOAuthLoginResponse {
	return anthropicOAuthLoginResponse{
		ID:               session.ID,
		Status:           session.Status,
		AuthorizationURL: session.AuthorizationURL,
		Message:          session.Message,
	}
}

func setProviderResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
