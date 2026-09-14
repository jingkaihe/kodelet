package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/browser"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

const (
	maxBrowserHandles        = 128
	maxBrowserAttachments    = 8 // Per runner, including connections still opening.
	browserHandleTTL         = 30 * time.Minute
	browserTicketTTL         = 25 * time.Second
	browserMessageLimit      = 16 * 1024 * 1024
	maxBrowserAssetBytes     = 16 * 1024 * 1024
	browserAuthCheckInterval = 5 * time.Second
	browserAuthCheckTimeout  = 5 * time.Second
)

type browserHandle struct {
	ID             string `json:"id"`
	SessionID      string `json:"sessionId"`
	CWD            string `json:"cwd"`
	DevTools       bool   `json:"devTools"`
	principalID    string
	runnerID       string
	generation     int64
	conversationID string
	timer          *time.Timer
	attachments    map[*browserAttachment]struct{}
}

type browserAttachment struct {
	handle       *browserHandle
	sessionID    string // The viewer's authentication session, not the runner's Chrome session.
	credentialID string
	token        string
	expires      time.Time
	ctx          context.Context
	cancel       context.CancelFunc
	ready        chan struct{}
	relay        *websocket.Conn // Guarded by Server.browserMu.
	timer        *time.Timer
}

func browserOpaqueID() string {
	var data [32]byte
	_, _ = rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:])
}

func (s *Server) browserAllowed(r *http.Request) bool {
	principal, ok := principalFromContext(r.Context())
	return s.config != nil && s.config.BrowserEnabled && ok && principal.HasRole(RoleTerminal)
}

func (s *Server) requireBrowser(handler http.HandlerFunc) http.HandlerFunc {
	return s.requireRole(RoleTerminal, func(w http.ResponseWriter, r *http.Request) {
		if s.config == nil || !s.config.BrowserEnabled {
			s.writeErrorResponse(w, http.StatusForbidden, "browser access is disabled by the server", nil)
			return
		}
		handler(w, r)
	})
}

func (s *Server) handleBrowserOpen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	target, targetErr := s.resolveBrowserTarget(r)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	if !target.Runner.WorkspaceBrowser {
		s.writeErrorResponse(w, http.StatusNotImplemented, "browser support is not enabled on this runner", nil)
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	conversationID := r.URL.Query().Get("conversationId")
	var info browser.Info
	if err := s.runnerRegistry.CallRunner(ctx, target.Runner.ID, target.Runner.Generation, protocol.MethodWorkspaceBrowserOpen,
		protocol.WorkspaceBrowserParams{ConversationID: conversationID, CWD: target.CWD}, &info); err != nil {
		s.writeErrorResponse(w, http.StatusBadGateway, "could not open runner browser", err)
		return
	}
	if info.SessionID == "" || info.CWD != target.CWD || info.ConversationID != conversationID {
		s.writeErrorResponse(w, http.StatusBadGateway, "runner returned an invalid browser session", nil)
		return
	}
	handle := &browserHandle{
		ID: browserOpaqueID(), SessionID: info.SessionID, CWD: info.CWD, DevTools: info.DevTools,
		principalID: principal.ID, runnerID: target.Runner.ID, generation: target.Runner.Generation,
		conversationID: conversationID, attachments: make(map[*browserAttachment]struct{}),
	}
	s.browserMu.Lock()
	if s.browserClosed {
		s.browserMu.Unlock()
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "browser service is stopping", nil)
		return
	}
	if s.browserHandles == nil {
		s.browserHandles = make(map[string]*browserHandle)
		s.browserTickets = make(map[string]*browserAttachment)
	}
	// Reopening a panel shares its handle, rather than accumulating abandoned leases.
	for _, existing := range s.browserHandles {
		if existing.principalID == handle.principalID && existing.runnerID == handle.runnerID && existing.generation == handle.generation &&
			existing.SessionID == handle.SessionID && existing.conversationID == handle.conversationID {
			existing.timer.Reset(browserHandleTTL)
			s.browserMu.Unlock()
			s.writeJSONResponse(w, existing)
			return
		}
	}
	if len(s.browserHandles) >= maxBrowserHandles {
		s.browserMu.Unlock()
		s.writeErrorResponse(w, http.StatusTooManyRequests, "too many browser session handles", nil)
		return
	}
	s.browserHandles[handle.ID] = handle
	handle.timer = time.AfterFunc(browserHandleTTL, func() { s.expireBrowserHandle(handle) })
	s.browserMu.Unlock()
	// Fence a disconnect racing the open RPC and handle insertion.
	if err := s.runnerRegistry.ValidateRunnerCall(handle.runnerID, handle.generation, protocol.MethodWorkspaceBrowserOpen); err != nil {
		s.browserMu.Lock()
		s.removeBrowserHandleLocked(handle)
		s.browserMu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "browser runner connection changed", nil)
		return
	}
	s.writeJSONResponse(w, handle)
}

// Drafts already have their eventual conversation ID and may have a pending
// first-turn runner assignment. Until saved, they may only use that runner's
// startup directory. Saved conversations must use their persisted workspace.
func (s *Server) resolveBrowserTarget(r *http.Request) (*workspaceRunnerTarget, *workspaceRunnerTargetError) {
	conversationID := r.URL.Query().Get("conversationId")
	if !validReceiptID(conversationID) {
		return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "a valid conversationId is required for the browser"}
	}
	if s.runnerRegistry == nil || s.conversationService == nil {
		return nil, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "conversation workspace is unavailable"}
	}
	affinity, found, err := s.runnerRegistry.ResolveConversationAffinity(r.Context(), conversationID)
	if err != nil {
		return nil, &workspaceRunnerTargetError{status: http.StatusInternalServerError, message: "failed to resolve conversation runner", err: err}
	}
	if _, err := s.conversationService.GetConversation(r.Context(), conversationID); !errors.Is(err, convtypes.ErrConversationNotFound) {
		if err != nil {
			return nil, &workspaceRunnerTargetError{status: http.StatusInternalServerError, message: "failed to load browser conversation", err: err}
		}
		return s.resolveWorkspaceRunnerTarget(r)
	}
	check := r.Clone(r.Context())
	query := r.URL.Query()
	query.Del("conversationId")
	if found {
		if runnerID := strings.TrimSpace(query.Get("runnerId")); runnerID != "" && runnerID != affinity.RunnerID {
			return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "the runner differs from the conversation's reserved runner"}
		}
		query.Set("runnerId", affinity.RunnerID)
	}
	check.URL = &url.URL{RawQuery: query.Encode()}
	target, targetErr := s.resolveWorkspaceRunnerTarget(check)
	if targetErr != nil {
		return nil, targetErr
	}
	target.CWD = target.Runner.Workspace.Path
	return target, nil
}

func (s *Server) browserHandleForRequest(w http.ResponseWriter, r *http.Request) *browserHandle {
	principal, ok := principalFromContext(r.Context())
	s.browserMu.Lock()
	handle := s.browserHandles[mux.Vars(r)["id"]]
	if !ok || handle == nil || handle.principalID != principal.ID {
		s.browserMu.Unlock()
		s.writeErrorResponse(w, http.StatusNotFound, "browser session not found", nil)
		return nil
	}
	handle.timer.Reset(browserHandleTTL)
	s.browserMu.Unlock()
	if err := s.runnerRegistry.ValidateRunnerCall(handle.runnerID, handle.generation, protocol.MethodWorkspaceBrowserOpen); err != nil {
		s.writeErrorResponse(w, http.StatusConflict, "browser runner connection changed; reopen the browser", nil)
		return nil
	}
	// Revalidate even draft handles: their conversation may now have saved affinity.
	check := r.Clone(r.Context())
	check.URL = &url.URL{RawQuery: url.Values{"runnerId": {handle.runnerID}, "conversationId": {handle.conversationID}}.Encode()}
	target, targetErr := s.resolveBrowserTarget(check)
	if targetErr != nil || target.CWD != handle.CWD {
		s.writeErrorResponse(w, http.StatusConflict, "browser conversation workspace changed", nil)
		return nil
	}
	return handle
}

func (s *Server) handleBrowserStop(w http.ResponseWriter, r *http.Request) {
	handle := s.browserHandleForRequest(w, r)
	if handle == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.runnerRegistry.CallRunner(ctx, handle.runnerID, handle.generation, protocol.MethodWorkspaceBrowserStop,
		protocol.WorkspaceBrowserParams{ConversationID: handle.conversationID, CWD: handle.CWD, SessionID: handle.SessionID}, &struct{}{}); err != nil {
		s.writeErrorResponse(w, http.StatusBadGateway, "could not stop runner browser", err)
		return
	}
	s.browserMu.Lock()
	for _, candidate := range s.browserHandles {
		if candidate.runnerID == handle.runnerID && candidate.generation == handle.generation &&
			candidate.conversationID == handle.conversationID && candidate.SessionID == handle.SessionID {
			s.removeBrowserHandleLocked(candidate)
		}
	}
	s.browserMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) browserOriginAllowed(r *http.Request) bool {
	origin, err := url.Parse(strings.TrimSpace(r.Header.Get("Origin")))
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	return s.terminalOriginAllowed(r)
}

func (s *Server) handleBrowserWebsocket(w http.ResponseWriter, r *http.Request) {
	if !s.browserOriginAllowed(r) {
		s.writeErrorResponse(w, http.StatusForbidden, "browser websocket origin is not allowed", nil)
		return
	}
	handle := s.browserHandleForRequest(w, r)
	if handle == nil {
		return
	}
	attachment, err := s.newBrowserAttachment(r.Context(), handle)
	if err != nil {
		s.writeErrorResponse(w, http.StatusTooManyRequests, err.Error(), nil)
		return
	}
	defer s.releaseBrowserAttachment(attachment)
	if err := s.bindBrowserAttachmentAuth(r, attachment); err != nil {
		s.writeAuthError(w, r, http.StatusUnauthorized, "browser authentication is no longer valid")
		return
	}
	ctx, cancel := context.WithTimeout(attachment.ctx, browserTicketTTL)
	err = s.runnerRegistry.CallRunner(ctx, handle.runnerID, handle.generation, protocol.MethodWorkspaceBrowserConnect,
		protocol.WorkspaceBrowserConnectParams{ConversationID: handle.conversationID, CWD: handle.CWD, SessionID: handle.SessionID, RelayToken: attachment.token}, &struct{}{})
	if err == nil {
		select {
		case <-attachment.ready:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	cancel()
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadGateway, "could not attach runner browser", err)
		return
	}
	upgrader := s.terminalUpgrader()
	upgrader.CheckOrigin = s.browserOriginAllowed
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.browserMu.Lock()
	relay := attachment.relay
	s.browserMu.Unlock()
	proxyBrowserAttachment(attachment.ctx, conn, relay)
}

func (s *Server) newBrowserAttachment(parent context.Context, handle *browserHandle) (*browserAttachment, error) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	if s.browserClosed || s.browserHandles[handle.ID] != handle {
		return nil, errors.New("browser session is no longer available")
	}
	count := 0
	for _, candidate := range s.browserHandles {
		if candidate.runnerID == handle.runnerID {
			count += len(candidate.attachments)
		}
	}
	if count >= maxBrowserAttachments {
		return nil, errors.New("too many browser attachments for runner")
	}
	ctx, cancel := context.WithCancel(parent)
	a := &browserAttachment{handle: handle, token: browserOpaqueID(), expires: time.Now().Add(browserTicketTTL), ctx: ctx, cancel: cancel, ready: make(chan struct{})}
	if principal, ok := principalFromContext(parent); ok {
		a.sessionID = principal.SessionID
		a.credentialID = principal.CredentialID
	}
	handle.attachments[a] = struct{}{}
	s.browserTickets[a.token] = a
	a.timer = time.AfterFunc(browserTicketTTL, func() {
		s.browserMu.Lock()
		if s.browserTickets[a.token] == a {
			delete(s.browserTickets, a.token)
			a.cancel()
		}
		s.browserMu.Unlock()
	})
	return a, nil
}

// bindBrowserAttachmentAuth checks after registration so a concurrent logout either
// finds this attachment or makes this initial load fail. Compatibility-token and
// no-auth principals have neither ID and retain their existing connection lifetime.
func (s *Server) bindBrowserAttachmentAuth(r *http.Request, a *browserAttachment) error {
	if a.sessionID == "" && a.credentialID == "" {
		return nil
	}
	var token string
	if a.sessionID != "" {
		cookie, err := r.Cookie(webSessionCookieName)
		if err != nil {
			return errors.Wrap(err, "browser session cookie is unavailable")
		}
		token = cookie.Value
	} else {
		var valid bool
		token, valid = strictKodeletBearerToken(r.Header.Get("Authorization"))
		if !valid {
			return errors.New("browser user credential is unavailable")
		}
	}
	check := func() (time.Duration, error) {
		ctx, cancel := context.WithTimeout(a.ctx, browserAuthCheckTimeout)
		defer cancel()
		expires, err := s.browserAttachmentAuthExpiry(ctx, a, token)
		if err != nil {
			return 0, err
		}
		remaining := expires.Sub(s.authStore.now())
		if remaining <= 0 {
			return 0, errors.New("browser authentication expired")
		}
		return remaining, nil
	}
	remaining, err := check()
	if err != nil {
		return err
	}
	// This timer is independent of CDP traffic, ping/pong, and slow store reads.
	// Revalidation never extends the attachment past its original authentication lifetime.
	deadline := time.Now().Add(remaining)
	expiry := time.AfterFunc(remaining, a.cancel)
	go func() {
		defer expiry.Stop()
		timer := time.NewTimer(min(browserAuthCheckInterval, remaining))
		defer timer.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-timer.C:
				remaining, err := check()
				remaining = min(remaining, time.Until(deadline))
				if err != nil || remaining <= 0 {
					a.cancel()
					return
				}
				expiry.Reset(remaining)
				timer.Reset(min(browserAuthCheckInterval, remaining))
			}
		}
	}()
	return nil
}

func (s *Server) browserAttachmentAuthExpiry(ctx context.Context, a *browserAttachment, token string) (time.Time, error) {
	if s.authStore == nil {
		return time.Time{}, errors.New("browser authentication store is unavailable")
	}
	if a.sessionID != "" {
		session, found, err := s.authStore.LoadWebSession(ctx, token)
		if err != nil {
			return time.Time{}, err
		}
		principal := principalFromWebSession(session)
		if !found || session.ID != a.sessionID || principal.ID != a.handle.principalID || !principal.HasRole(RoleTerminal) {
			return time.Time{}, errors.New("browser authentication session is no longer authorized")
		}
		return session.ExpiresAt, nil
	}
	identity, err := s.authStore.LoadUserCredential(ctx, token)
	if err != nil {
		return time.Time{}, err
	}
	principal := principalFromUserCredential(identity)
	if principal.CredentialID != a.credentialID || principal.ID != a.handle.principalID || !principal.HasRole(RoleTerminal) {
		return time.Time{}, errors.New("browser user credential is no longer authorized")
	}
	// LoadUserCredential validates revocation and roles but does not expose expiry.
	var expires time.Time
	err = s.authStore.db.GetContext(ctx, &expires, `
		SELECT expires_at FROM user_api_credentials
		WHERE id = ? AND revoked_at IS NULL AND expires_at > ?
	`, a.credentialID, s.authStore.now().UTC())
	return expires, errors.Wrap(err, "failed to load browser user credential expiry")
}

func (s *Server) cancelBrowserSessionAttachments(sessionID string) {
	if sessionID == "" {
		return
	}
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	for _, handle := range s.browserHandles {
		for a := range handle.attachments {
			if a.sessionID == sessionID {
				a.cancel()
			}
		}
	}
}

func (s *Server) handleBrowserRelay(w http.ResponseWriter, r *http.Request) {
	if s.config == nil || !s.config.BrowserEnabled {
		s.writeErrorResponse(w, http.StatusForbidden, "browser access is disabled by the server", nil)
		return
	}
	// This route deliberately accepts neither user cookies nor general runner credentials.
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || len(header) > 256 || (r.Header.Get("Origin") != "" && !s.browserOriginAllowed(r)) {
		s.writeAuthError(w, r, http.StatusUnauthorized, "invalid browser relay ticket")
		return
	}
	token := strings.TrimPrefix(header, "Bearer ")
	s.browserMu.Lock()
	a := s.browserTickets[token]
	delete(s.browserTickets, token) // A ticket is consumed even if its upgrade fails.
	if a != nil {
		a.timer.Stop()
	}
	valid := a != nil && a.expires.After(time.Now()) && a.ctx.Err() == nil && s.browserHandles[a.handle.ID] == a.handle
	s.browserMu.Unlock()
	if !valid {
		s.writeAuthError(w, r, http.StatusUnauthorized, "invalid browser relay ticket")
		return
	}
	if err := s.runnerRegistry.ValidateRunnerCall(a.handle.runnerID, a.handle.generation, protocol.MethodWorkspaceBrowserConnect); err != nil {
		a.cancel()
		s.writeAuthError(w, r, http.StatusUnauthorized, "browser relay generation is no longer valid")
		return
	}
	upgrader := s.terminalUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		a.cancel()
		return
	}
	s.browserMu.Lock()
	if a.ctx.Err() != nil || s.browserHandles[a.handle.ID] != a.handle {
		s.browserMu.Unlock()
		_ = conn.Close()
		return
	}
	a.relay = conn
	close(a.ready)
	s.browserMu.Unlock()
	// The user-facing attachment owns IO and closes this socket on every exit path.
	<-a.ctx.Done()
	_ = conn.Close()
}

func (s *Server) releaseBrowserAttachment(a *browserAttachment) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	a.cancel()
	a.timer.Stop()
	delete(s.browserTickets, a.token)
	delete(a.handle.attachments, a)
	if a.relay != nil {
		_ = a.relay.Close()
	}
}

func (s *Server) expireBrowserHandle(handle *browserHandle) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	if s.browserHandles[handle.ID] != handle {
		return
	}
	if len(handle.attachments) > 0 {
		handle.timer.Reset(browserHandleTTL)
		return
	}
	s.removeBrowserHandleLocked(handle)
}

func (s *Server) removeBrowserHandleLocked(handle *browserHandle) {
	delete(s.browserHandles, handle.ID)
	handle.timer.Stop()
	for a := range handle.attachments {
		delete(s.browserTickets, a.token)
		a.timer.Stop()
		a.cancel()
		if a.relay != nil {
			_ = a.relay.Close()
		}
	}
}

func (s *Server) closeBrowserHandles(runnerID string, generation int64) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	if runnerID == "" {
		s.browserClosed = true
	}
	for _, handle := range s.browserHandles {
		if runnerID == "" || (handle.runnerID == runnerID && handle.generation == generation) {
			s.removeBrowserHandleLocked(handle)
		}
	}
}

func (s *Server) handleBrowserAsset(w http.ResponseWriter, r *http.Request) {
	handle := s.browserHandleForRequest(w, r)
	if handle == nil {
		return
	}
	if !handle.DevTools {
		s.writeErrorResponse(w, http.StatusNotFound, "DevTools assets are not installed on this runner", nil)
		return
	}
	path := mux.Vars(r)["path"]
	if path == "" || len(path) > 2048 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var offset int64
	for {
		var chunk browser.AssetChunk
		err := s.runnerRegistry.CallRunner(ctx, handle.runnerID, handle.generation, protocol.MethodWorkspaceBrowserAsset,
			protocol.WorkspaceBrowserAssetParams{Path: path, Offset: offset}, &chunk)
		if err != nil || len(chunk.Data) > 128*1024 || offset+int64(len(chunk.Data)) > maxBrowserAssetBytes || (len(chunk.Data) == 0 && !chunk.EOF) {
			if offset == 0 {
				s.writeErrorResponse(w, http.StatusBadGateway, "could not load runner DevTools asset", nil)
			} else {
				// Never return a successful, silently truncated JavaScript module.
				panic(http.ErrAbortHandler)
			}
			return
		}
		if offset == 0 {
			w.Header().Set("Content-Type", chunk.ContentType)
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'; object-src 'none'")
		}
		if _, err := w.Write(chunk.Data); err != nil {
			return
		}
		offset += int64(len(chunk.Data))
		if chunk.EOF {
			return
		}
	}
}

func proxyBrowserAttachment(ctx context.Context, left, right *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer left.Close()
	defer right.Close()
	stop := context.AfterFunc(ctx, func() {
		_ = left.Close()
		_ = right.Close()
	})
	defer stop()
	for _, conn := range []*websocket.Conn{left, right} {
		conn.SetReadLimit(browserMessageLimit)
		_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	}
	var copies sync.WaitGroup
	copies.Add(2)
	copyMessages := func(dst, src *websocket.Conn) {
		defer copies.Done()
		defer cancel()
		for {
			kind, payload, err := src.ReadMessage()
			if err != nil || kind != websocket.TextMessage {
				return
			}
			if err := dst.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return
			}
			if err := dst.WriteMessage(kind, payload); err != nil {
				return
			}
		}
	}
	go copyMessages(left, right)
	go copyMessages(right, left)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-ticker.C:
			for _, conn := range []*websocket.Conn{left, right} {
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					cancel()
				}
			}
		}
	}
	copies.Wait()
}
