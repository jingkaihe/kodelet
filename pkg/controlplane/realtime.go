package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

func newRealtimeFromViper() (llmtypes.RealtimeProvider, error) {
	var config struct {
		Enabled bool
		Profile string
		Model   string
		Voice   string
	}
	if err := viper.UnmarshalKey("realtime", &config); err != nil {
		return nil, errors.Wrap(err, "expected enabled, profile, model and voice settings")
	}
	if !config.Enabled {
		return nil, nil //nolint:nilnil // Disabled voice intentionally has no provider.
	}
	if strings.TrimSpace(config.Profile) == "" {
		return nil, errors.New("realtime.profile is required when realtime is enabled")
	}
	selected, err := llm.GetConfigFromViperWithProfile(strings.TrimSpace(config.Profile))
	if err != nil {
		return nil, errors.Wrap(err, "invalid realtime profile")
	}
	return llm.NewRealtimeProvider(selected, config.Model, config.Voice)
}

// realtimeEvent is presentation only. Delegation is accepted exclusively from
// the authenticated provider sideband, never from the browser media channel.
type realtimeEvent struct {
	Kind           string `json:"kind"`
	ID             string `json:"id,omitempty"`
	Role           string `json:"role,omitempty"`
	Text           string `json:"text,omitempty"`
	Final          bool   `json:"final,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	TurnID         string `json:"turnId,omitempty"`
	Anchor         *int   `json:"anchor,omitempty"`
}

type realtimeCall struct {
	server                *Server
	id, ownerID, clientID string
	chat                  chat.ChatRequest
	ctx                   context.Context
	cancel                context.CancelFunc
	mu                    sync.Mutex
	session               llmtypes.RealtimeSession
	attached              bool
	closed                bool
	// Buffer the POST-to-GET gap. A second attachment is deliberately refused;
	// reconnecting must never replay an execution instruction.
	events   chan realtimeEvent
	handoffs map[string]struct{}
	work     sync.WaitGroup
}

func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	available := struct {
		Enabled      bool     `json:"enabled"`
		Available    bool     `json:"available"`
		Reason       string   `json:"reason,omitempty"`
		DefaultVoice string   `json:"defaultVoice,omitempty"`
		Voices       []string `json:"voices,omitempty"`
	}{Reason: "Live voice is disabled."}
	if s.realtime != nil {
		available.Enabled = true
		available.DefaultVoice, available.Voices = s.realtime.VoiceOptions()
		if err := s.realtime.CheckAvailability(); err != nil {
			available.Reason = err.Error()
		} else {
			available.Available, available.Reason = true, ""
		}
	}
	if r.Method == http.MethodGet {
		s.writeJSONResponse(w, available)
		return
	}
	if !available.Available {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, available.Reason, nil)
		return
	}
	var request struct {
		Offer string           `json:"offer"`
		Chat  chat.ChatRequest `json:"chat"`
		Voice string           `json:"voice,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid live voice request", err)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF || strings.TrimSpace(request.Offer) == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "a single live voice request with an SDP offer is required", nil)
		return
	}
	principal, ok := principalFromContext(r.Context())
	request.Voice = strings.TrimSpace(request.Voice)
	if request.Voice != "" && !slices.Contains(available.Voices, request.Voice) {
		s.writeErrorResponse(w, http.StatusBadRequest, "unsupported live voice selection", nil)
		return
	}
	clientID := strings.TrimSpace(r.Header.Get(chat.ClientIDHeader))
	if !ok || !validUIClientID(clientID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "live voice requires an authenticated owner and X-Kodelet-Client-ID", nil)
		return
	}
	req := request.Chat
	req.ConversationID = strings.TrimSpace(req.ConversationID)
	if !validReceiptID(req.ConversationID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "a valid conversationId is required", nil)
		return
	}
	if s.conversationService == nil || s.turns == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "durable chat storage is unavailable", nil)
		return
	}
	// Native extension attachments have a separate owner lifetime. Browser voice
	// must not claim one or manufacture interactive capabilities.
	if req.SessionExtensionsID != "" || req.ParentConversationID != "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "live voice requires a browser conversation", nil)
		return
	}
	record, err := s.conversationService.GetConversation(r.Context(), req.ConversationID)
	if errors.Is(err, convtypes.ErrConversationNotFound) {
		if err := s.createRealtimeConversation(r.Context(), &req); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "could not create the voice conversation", err)
			return
		}
		record, err = s.conversationService.GetConversation(r.Context(), req.ConversationID)
	}
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "could not load the voice conversation", err)
		return
	}
	profile := resolveConversationProfile(record.Metadata)
	if req.Profile != "" && req.Profile != profile || req.CWD != "" && req.CWD != record.CWD {
		s.writeErrorResponse(w, http.StatusConflict, "live voice must use the conversation's saved profile and workspace", nil)
		return
	}
	req.Profile, req.CWD = profile, record.CWD
	if s.runnerRegistry != nil {
		affinity, found, err := s.runnerRegistry.ResolveConversationAffinity(r.Context(), req.ConversationID)
		if err != nil || !found {
			s.writeErrorResponse(w, http.StatusConflict, "select a runner for this conversation before starting live voice", err)
			return
		}
		if req.RunnerID != "" && req.RunnerID != affinity.RunnerID || req.EnvironmentProfile != "" && req.EnvironmentProfile != affinity.EnvironmentProfile {
			s.writeErrorResponse(w, http.StatusConflict, "live voice must use the conversation's runner", nil)
			return
		}
		req.RunnerID, req.EnvironmentProfile = affinity.RunnerID, affinity.EnvironmentProfile
	}
	req.Message, req.Content, req.TurnID = "", nil, ""
	ctx, cancel := context.WithTimeout(s.chatExecutionContext(r.Context()), 30*time.Minute)
	call := &realtimeCall{
		server: s, id: convtypes.GenerateID(), ownerID: principal.ID, clientID: clientID,
		chat: req, ctx: ctx, cancel: cancel, events: make(chan realtimeEvent, 256), handoffs: make(map[string]struct{}),
	}
	s.realtimeMu.Lock()
	for _, existing := range s.realtimeCalls {
		if existing.chat.ConversationID == req.ConversationID {
			s.realtimeMu.Unlock()
			cancel()
			s.writeErrorResponse(w, http.StatusConflict, "this conversation already has a live voice call", nil)
			return
		}
	}
	if s.realtimeCalls == nil {
		s.realtimeCalls = make(map[string]*realtimeCall)
	}
	s.realtimeCalls[call.id] = call
	s.realtimeMu.Unlock()
	// This also covers an abandoned negotiation or a POST that never attaches.
	attachTimer := time.AfterFunc(30*time.Second, func() {
		call.mu.Lock()
		attached := call.attached
		call.mu.Unlock()
		if !attached {
			call.close()
		}
	})
	context.AfterFunc(ctx, func() { attachTimer.Stop(); call.close() })
	stopDisconnect := context.AfterFunc(r.Context(), call.close)
	defer stopDisconnect()
	session, err := s.realtime.Open(ctx, llmtypes.RealtimeOffer{
		SDP: request.Offer, Voice: request.Voice,
		Instructions: s.realtimeInstructions(ctx, req.ConversationID),
	})
	if err != nil {
		call.close()
		logger.G(r.Context()).WithError(err).Warn("live voice negotiation failed")
		s.writeErrorResponse(w, http.StatusBadGateway, "could not start live voice", nil)
		return
	}
	call.mu.Lock()
	if call.closed {
		call.mu.Unlock()
		_ = session.Close()
		s.writeErrorResponse(w, http.StatusRequestTimeout, "live voice negotiation expired", nil)
		return
	}
	call.session = session
	call.mu.Unlock()
	call.emit(realtimeEvent{Kind: "state", Text: "connected"})
	go call.readProvider(session)
	s.writeJSONResponse(w, struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversationId"`
		Answer         string `json:"answer"`
	}{call.id, req.ConversationID, session.Answer()})
}

func (s *Server) ownedRealtimeCall(w http.ResponseWriter, r *http.Request) *realtimeCall {
	s.realtimeMu.Lock()
	call := s.realtimeCalls[mux.Vars(r)["id"]]
	s.realtimeMu.Unlock()
	principal, ok := principalFromContext(r.Context())
	if call == nil || !ok || principal.ID != call.ownerID || r.Header.Get(chat.ClientIDHeader) != call.clientID {
		s.writeErrorResponse(w, http.StatusNotFound, "live voice call not found", nil)
		return nil
	}
	return call
}

func (s *Server) handleCloseRealtime(w http.ResponseWriter, r *http.Request) {
	if call := s.ownedRealtimeCall(w, r); call != nil {
		call.close()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleRealtimeEvents(w http.ResponseWriter, r *http.Request) {
	call := s.ownedRealtimeCall(w, r)
	if call == nil {
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		s.writeErrorResponse(w, http.StatusInternalServerError, "streaming is unavailable", nil)
		return
	}
	call.mu.Lock()
	if call.attached || call.closed {
		call.mu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "live voice event stream is already attached or closed", nil)
		return
	}
	call.attached = true
	call.mu.Unlock()
	defer call.close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	_ = controller.Flush()
	ticker := time.NewTicker(conversationStreamKeepAliveInterval)
	defer ticker.Stop()
	for {
		var event *realtimeEvent
		select {
		case value := <-call.events:
			event = &value
		case <-ticker.C:
		case <-r.Context().Done():
			return
		case <-call.ctx.Done():
			// Deliver queued events first: the last one usually explains why
			// the call ended, and select would otherwise drop it at random.
			select {
			case value := <-call.events:
				event = &value
			default:
				event = &realtimeEvent{Kind: "closed", ConversationID: call.chat.ConversationID}
			}
		}
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		var err error
		if event != nil {
			err = json.NewEncoder(w).Encode(event)
		} else {
			_, err = w.Write([]byte("\n"))
		}
		if err != nil || controller.Flush() != nil {
			return
		}
		_ = controller.SetWriteDeadline(time.Time{})
		if event != nil && event.Kind == "closed" {
			return
		}
	}
}

func (c *realtimeCall) emit(event realtimeEvent) {
	event.ConversationID = c.chat.ConversationID
	select {
	case c.events <- event:
	case <-c.ctx.Done():
	default:
		c.close() // A stalled browser must not keep an unobserved call alive.
	}
}

func (c *realtimeCall) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	session := c.session
	c.mu.Unlock()
	c.cancel()
	if session != nil {
		_ = session.Close()
	}
	c.server.realtimeMu.Lock()
	delete(c.server.realtimeCalls, c.id)
	c.server.realtimeMu.Unlock()
}

func (s *Server) closeRealtimeCalls() {
	s.realtimeMu.Lock()
	calls := make([]*realtimeCall, 0, len(s.realtimeCalls))
	for _, call := range s.realtimeCalls {
		calls = append(calls, call)
	}
	s.realtimeMu.Unlock()
	for _, call := range calls {
		call.close()
	}
}

func (s *Server) closeRealtimeConversation(conversationID string) {
	s.realtimeMu.Lock()
	var selected *realtimeCall
	for _, call := range s.realtimeCalls {
		if call.chat.ConversationID == conversationID {
			selected = call
			break
		}
	}
	s.realtimeMu.Unlock()
	if selected != nil {
		selected.close()
	}
}

func (c *realtimeCall) readProvider(session llmtypes.RealtimeSession) {
	defer c.close()
	for {
		select {
		case <-c.ctx.Done():
			return
		case event, ok := <-session.Events():
			if !ok {
				return
			}
			switch event.Kind {
			case "speech_started":
				if event.ItemID != "" {
					if err := c.server.saveRealtimeTranscript(c.ctx, c.chat.ConversationID, realtimeEvent{ID: c.id + ":" + event.ItemID, Role: "user"}); err != nil {
						c.emit(realtimeEvent{Kind: "error", Text: "Could not save the voice transcript. Live voice has stopped."})
						return
					}
				}
			case "transcript":
				if event.ID == "" || event.Role != "user" && event.Role != "assistant" || len(event.Text) > 64<<10 {
					continue
				}
				e := realtimeEvent{Kind: "transcript", ID: c.id + ":" + event.ID, Role: event.Role, Text: event.Text, Final: event.Final}
				if err := c.server.saveRealtimeTranscript(c.ctx, c.chat.ConversationID, e); err != nil {
					logger.G(c.ctx).WithError(err).Warn("could not save live voice transcript")
					c.emit(realtimeEvent{Kind: "error", Text: "Could not save the voice transcript. Live voice has stopped."})
					return
				}
				var anchor int
				if err := c.server.turns.db.GetContext(c.ctx, &anchor, "SELECT anchor FROM realtime_transcript WHERE conversation_id=? AND id=?", c.chat.ConversationID, e.ID); err != nil {
					return
				}
				e.Anchor = &anchor
				c.emit(e)
			case "delegate":
				c.delegate(event)
			case "error":
				c.emit(realtimeEvent{Kind: "error", Text: "Live voice was interrupted. Coding work continues."})
				return
			case "closed":
				return
			}
		}
	}
}
