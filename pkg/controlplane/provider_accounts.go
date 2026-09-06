package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
)

// Serialize administrative read-modify-write operations with OAuth saves.
var anthropicAccountMutationMu sync.Mutex

func (s *Server) handleAnthropicAccounts(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	if r.Method == http.MethodGet {
		accounts, err := auth.ListAnthropicAccounts()
		if err != nil {
			s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read daemon accounts", err)
			return
		}
		result := chat.AnthropicAccounts{Accounts: make([]chat.AnthropicAccountSummary, 0, len(accounts))}
		for _, account := range accounts {
			result.Accounts = append(result.Accounts, chat.AnthropicAccountSummary{Alias: account.Alias, Email: account.Email, ExpiresAt: account.ExpiresAt, IsDefault: account.IsDefault})
		}
		s.writeJSONResponse(w, result)
		return
	}
	var request chat.AnthropicAccountMutation
	if !s.decodeProviderAccountRequest(w, r, &request) {
		return
	}
	if err := request.Validate(); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid account mutation", err)
		return
	}
	// Changing accounts while a code is being exchanged must not resurrect a
	// removed account. The existing OAuth session mutex defines this ordering.
	s.anthropicOAuthLoginMu.Lock()
	defer s.anthropicOAuthLoginMu.Unlock()
	if anthropicOAuthLoginActive(s.anthropicOAuthLogin) {
		s.writeErrorResponse(w, http.StatusConflict, "finish or cancel the pending Anthropic login before changing accounts", nil)
		return
	}
	anthropicAccountMutationMu.Lock()
	defer anthropicAccountMutationMu.Unlock()
	var err error
	switch request.Action {
	case "default":
		err = auth.SetDefaultAnthropicAccount(request.Alias)
	case "rename":
		err = auth.RenameAnthropicAccount(request.Alias, request.NewAlias)
	case "remove":
		err = auth.RemoveAnthropicAccount(request.Alias)
	case "logout":
		var accounts []auth.AnthropicAccountInfo
		accounts, err = auth.ListAnthropicAccounts()
		if err == nil {
			for _, account := range accounts {
				if err = auth.RemoveAnthropicAccount(account.Alias); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "daemon account mutation failed; inspect current accounts", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) decodeProviderAccountRequest(w http.ResponseWriter, r *http.Request, result any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid provider account request", err)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		s.writeErrorResponse(w, http.StatusBadRequest, "expected one provider account request", err)
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "provider account request must be an object", err)
		return false
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			s.writeErrorResponse(w, http.StatusBadRequest, "provider account fields cannot be null", nil)
			return false
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid provider account request", err)
		return false
	}
	return true
}

func (s *Server) handleAnthropicAccountUsage(w http.ResponseWriter, r *http.Request) {
	setProviderResponseHeaders(w)
	var request struct {
		Alias string `json:"alias,omitempty"`
	}
	if !s.decodeProviderAccountRequest(w, r, &request) {
		return
	}
	if request.Alias != "" {
		if err := auth.ValidateAlias(request.Alias); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid account alias", err)
			return
		}
	}
	accounts, err := auth.ListAnthropicAccounts()
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to read daemon accounts", err)
		return
	}
	var selected *auth.AnthropicAccountInfo
	for _, account := range accounts {
		if account.Alias == request.Alias || (request.Alias == "" && account.IsDefault) {
			selected = &account
			break
		}
	}
	if selected == nil {
		s.writeErrorResponse(w, http.StatusNotFound, "daemon account not found", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	stats, err := anthropic.GetRateLimitStats(ctx, selected.Alias)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadGateway, "failed to inspect daemon account usage", err)
		return
	}
	window := func(status string, utilization float64, reset time.Time) chat.AnthropicUsageWindow {
		return chat.AnthropicUsageWindow{Status: status, Utilization: utilization, ResetTime: reset.Format(time.RFC3339), ResetUnix: reset.Unix()}
	}
	s.writeJSONResponse(w, chat.AnthropicAccountUsage{Account: selected.Alias, Email: selected.Email, Window5h: window(stats.Status5h, stats.Utilization5h, stats.Reset5h), Window7d: window(stats.Status7d, stats.Utilization7d, stats.Reset7d)})
}
