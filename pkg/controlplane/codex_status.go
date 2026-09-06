package controlplane

import (
	"context"
	"net/http"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/logger"
)

type codexStatusService interface {
	Status(context.Context) (chat.CodexStatus, error)
}

func (defaultCodexProviderAuthService) Status(ctx context.Context) (chat.CodexStatus, error) {
	result := chat.CodexStatus{}
	exists, err := auth.GetCodexCredentialsExists()
	if err != nil || !exists {
		return result, err
	}
	credentials, err := auth.GetCodexCredentials()
	if err != nil {
		return result, err
	}
	if !auth.IsCodexOAuthEnabled(credentials) {
		return result, nil
	}
	result.Connected, result.Authentication = true, "OAuth (ChatGPT account)"
	refreshed, refreshErr := auth.GetCodexCredentialsForRequest(ctx)
	if refreshErr == nil {
		credentials = refreshed
	}
	result.AccountID = maskedCodexAccountID(credentials.AccountID)
	result.ExpiresAt, result.CanRefresh = credentials.ExpiresAt, credentials.RefreshToken != ""
	result.Usage, err = auth.GetCodexUsageStatsWithCredentials(ctx, credentials)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Could not load ChatGPT usage")
		result.UsageMessage = "Live usage is unavailable. Visit https://chatgpt.com/codex/settings/usage for current usage."
		if refreshErr != nil {
			logger.G(ctx).WithError(refreshErr).Warn("Could not refresh ChatGPT sign-in")
			result.UsageMessage = "Live usage is unavailable. Run 'kodelet codex login' to reconnect your account."
		}
	}
	return result, nil
}

func maskedCodexAccountID(id string) string {
	if len(id) <= 12 {
		return "****"
	}
	return id[:4] + "..." + id[len(id)-4:]
}

func (s *Server) handleCodexStatus(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	service, ok := s.codexProviderAuth().(codexStatusService)
	if !ok {
		s.writeErrorResponse(w, http.StatusNotImplemented, "detailed ChatGPT status is unavailable", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	status, err := service.Status(ctx)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "could not read ChatGPT sign-in details", err)
		return
	}
	s.writeJSONResponse(w, status)
}
