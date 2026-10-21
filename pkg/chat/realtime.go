package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const (
	maxRealtimeResponseBytes = 1 << 20
	realtimeRequestTimeout   = 45 * time.Second
)

// RealtimeCapabilities reports the selected server's live voice availability.
type RealtimeCapabilities struct {
	Enabled      bool     `json:"enabled"`
	Available    bool     `json:"available"`
	Reason       string   `json:"reason,omitempty"`
	DefaultVoice string   `json:"defaultVoice,omitempty"`
	Voices       []string `json:"voices,omitempty"`
}

// RealtimeSession identifies a voice call and supplies its WebRTC answer.
type RealtimeSession struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	Answer         string `json:"answer"`
}

// RealtimeEvent is presentation only; only the server's provider sideband can
// delegate coding work. Transcript text is cumulative, not an append-only delta.
type RealtimeEvent struct {
	Kind           string `json:"kind"`
	ID             string `json:"id,omitempty"`
	Role           string `json:"role,omitempty"`
	Text           string `json:"text,omitempty"`
	Final          bool   `json:"final,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	TurnID         string `json:"turnId,omitempty"`
	Anchor         *int   `json:"anchor,omitempty"`
}

// RealtimeCapabilities discovers voice support without starting a call.
func (r *Client) RealtimeCapabilities(ctx context.Context) (RealtimeCapabilities, error) {
	var result RealtimeCapabilities
	err := r.realtimeAPIRequest(ctx, http.MethodGet, []string{"api", "realtime"}, nil, &result)
	return result, err
}

// StartRealtime negotiates a call once, without retries or redirects. It does not
// allocate a conversation ID. Use this same Client to keep StreamConversation
// attached: the conversation watcher, not StreamRealtime, owns delegated UI.
// Session-extension attachments and parent creation retain server restrictions.
// If the response includes an ID even on validation failure, the caller can use
// EndRealtime to clean up the allocated call.
func (r *Client) StartRealtime(ctx context.Context, offer string, request ChatRequest, voice string) (RealtimeSession, error) {
	var result RealtimeSession
	if strings.TrimSpace(offer) == "" {
		return result, errors.New("a realtime SDP offer is required")
	}
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	if request.ConversationID == "" {
		return result, errors.New("conversation ID is required")
	}
	if r != nil && r.runnerID != "" {
		request.RunnerID = r.runnerID
	}
	capabilities := controlPlaneClientCapabilities(ctx)
	request.ClientCapabilities = &capabilities
	payload := struct {
		Offer string      `json:"offer"`
		Chat  ChatRequest `json:"chat"`
		Voice string      `json:"voice,omitempty"`
	}{Offer: offer, Chat: request, Voice: strings.TrimSpace(voice)}
	if err := r.realtimeAPIRequest(ctx, http.MethodPost, []string{"api", "realtime"}, payload, &result); err != nil {
		return result, err
	}
	if strings.TrimSpace(result.ID) == "" || strings.TrimSpace(result.Answer) == "" || result.ConversationID != request.ConversationID {
		return result, errors.New("server returned an invalid realtime session")
	}
	return result, nil
}

// EndRealtime ends voice, not accepted coding work. It does not retry. Use a
// fresh context for cleanup if the recording or event-stream context is canceled.
func (r *Client) EndRealtime(ctx context.Context, callID string) error {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errors.New("realtime call ID is required")
	}
	return r.realtimeAPIRequest(ctx, http.MethodDelete, []string{"api", "realtime", callID}, nil, nil)
}

// StreamRealtime attaches once to a call's NDJSON presentation stream. The caller
// owns its lifetime through ctx; the HTTP client's overall Timeout is disabled
// for this stream without changing its transport or the shared Client. It never
// reconnects. onEvent runs synchronously and must not block. A closed event ends
// the stream successfully; EOF without one reports an interrupted call.
func (r *Client) StreamRealtime(ctx context.Context, callID string, onEvent func(RealtimeEvent)) error {
	if r == nil || r.client == nil {
		return errors.New("the chat connection is not initialized")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errors.New("realtime call ID is required")
	}
	if onEvent == nil {
		return errors.New("realtime event callback is required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "realtime", callID, "events")
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create realtime stream request")
	}
	r.authorize(request)
	request.Header.Set(ClientIDHeader, r.clientID)
	request.Header.Set("Accept", "application/x-ndjson")
	client := *r.client
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to stream realtime events")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return controlPlaneResponseError(response)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), maxRealtimeResponseBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue // Server keepalive.
		}
		var event RealtimeEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return &ControlPlaneStreamProtocolError{err: errors.Wrap(err, "failed to decode realtime event")}
		}
		if strings.TrimSpace(event.Kind) == "" {
			return &ControlPlaneStreamProtocolError{err: errors.New("realtime event kind is required")}
		}
		onEvent(event)
		if event.Kind == "closed" {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		wrapped := errors.Wrap(err, "failed to read realtime stream")
		if errors.Is(err, bufio.ErrTooLong) {
			return &ControlPlaneStreamProtocolError{err: wrapped}
		}
		return wrapped
	}
	return errors.Wrap(io.ErrUnexpectedEOF, "realtime stream ended without a closed event")
}

func (r *Client) realtimeAPIRequest(ctx context.Context, method string, path []string, payload, result any) error {
	if r == nil || r.client == nil {
		return errors.New("the chat connection is not initialized")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, path...)
	if err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return errors.Wrap(err, "failed to encode realtime request")
		}
		body = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, realtimeRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.Wrap(err, "failed to create realtime request")
	}
	r.authorize(request)
	request.Header.Set(ClientIDHeader, r.clientID)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := *r.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return errors.Wrap(err, "realtime request failed; check the conversation before starting another call")
	}
	defer response.Body.Close()
	wantStatus := http.StatusOK
	if method == http.MethodDelete {
		wantStatus = http.StatusNoContent
	}
	if response.StatusCode != wantStatus {
		return controlPlaneResponseError(response)
	}
	if result == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRealtimeResponseBytes+1))
	if err != nil {
		return errors.Wrap(err, "failed to read realtime response")
	}
	if len(data) > maxRealtimeResponseBytes {
		return errors.New("realtime response exceeds the size limit")
	}
	return errors.Wrap(json.Unmarshal(data, result), "invalid realtime response")
}
