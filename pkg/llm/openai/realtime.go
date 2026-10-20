package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/auth"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
)

const (
	realtimeSetupTimeout = 30 * time.Second
	realtimeWriteTimeout = 5 * time.Second
	realtimeMaxSDP       = 128 << 10
	realtimeMaxText      = 64 << 10
	realtimeMaxFrame     = 1 << 20
	realtimeMaxItems     = 1024
	realtimeDelegateTool = "delegate_to_agent"
)

var realtimeCallID = regexp.MustCompile(`^(rtc_[A-Za-z0-9_-]{1,180}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

// RealtimeProvider keeps connection settings separate from the coding model.
// Codex uses the experimental native frameless protocol; OpenAI uses GA Realtime.
type RealtimeProvider struct {
	platform, baseURL, sidebandBaseURL, apiKeyEnvVar, model, voice string
	client                                                         *http.Client
	dialer                                                         *websocket.Dialer
	readCodex                                                      func() (*auth.CodexCredentials, error)
	requestCodex                                                   func(context.Context) (*auth.CodexCredentials, error)
}

// NewRealtimeProvider validates connection settings without fetching credentials.
func NewRealtimeProvider(config llmtypes.Config, model, voice string) (*RealtimeProvider, error) {
	p := &RealtimeProvider{
		platform: resolvePlatformName(config), apiKeyEnvVar: GetAPIKeyEnvVar(config),
		model: strings.TrimSpace(model), voice: strings.TrimSpace(voice),
		client:    &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		dialer:    &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: realtimeSetupTimeout},
		readCodex: auth.GetCodexCredentials, requestCodex: auth.GetCodexCredentialsForRequest,
	}
	switch p.platform {
	case "openai":
		base, err := url.Parse(strings.TrimSpace(GetBaseURL(config)))
		if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") ||
			base.User != nil || base.RawQuery != "" || base.Fragment != "" {
			return nil, errors.New("realtime profile openai.base_url must be an HTTP(S) URL without credentials, query, or fragment")
		}
		p.baseURL = strings.TrimRight(base.String(), "/")
		p.sidebandBaseURL = p.baseURL
		if p.model == "" {
			p.model = "gpt-realtime"
		}
		if p.voice == "" {
			p.voice = "marin"
		}
	case "codex":
		// These are different trusted authorities by design, not Location redirects.
		p.baseURL = "https://chatgpt.com/backend-api/codex"
		p.sidebandBaseURL = "https://api.openai.com/v1"
		if p.model == "" {
			p.model = "gpt-live-1-codex"
		}
		if p.voice == "" {
			p.voice = "cove"
		}
	default:
		return nil, errors.New("realtime supports only openai.platform openai or codex")
	}
	return p, nil
}

// CheckAvailability only inspects local credentials; Open resolves them again.
func (p *RealtimeProvider) CheckAvailability() error {
	if p.platform == "codex" {
		creds, err := p.readCodex()
		if err != nil || !auth.IsCodexOAuthEnabled(creds) {
			return errors.New("sign in on the server with 'kodelet codex login' to use realtime voice")
		}
		if creds.ExpiresAt > 0 && creds.ExpiresAt <= time.Now().Unix() && creds.RefreshToken == "" {
			return errors.New("codex credentials have expired; run 'kodelet codex login' again")
		}
	} else if strings.TrimSpace(os.Getenv(p.apiKeyEnvVar)) == "" {
		return errors.New("configure " + p.apiKeyEnvVar + " on the server to use realtime voice")
	}
	return nil
}

func (p *RealtimeProvider) headers(ctx context.Context) (http.Header, error) {
	header := make(http.Header)
	header.Set("User-Agent", "kodelet/"+strings.TrimPrefix(version.Version, "v"))
	if p.platform == "codex" {
		creds, err := p.requestCodex(ctx)
		if err != nil || !auth.IsCodexOAuthEnabled(creds) {
			return nil, errors.New("realtime credentials unavailable; run 'kodelet codex login' on the server")
		}
		header.Set("Authorization", "Bearer "+creds.AccessToken)
		header.Set("ChatGPT-Account-ID", creds.AccountID)
		header.Set("originator", auth.CodexOriginator)
		header.Set("OpenAI-Alpha", "quicksilver=v2")
	} else {
		key := strings.TrimSpace(os.Getenv(p.apiKeyEnvVar))
		if key == "" {
			return nil, errors.New("realtime API key is not configured on the server")
		}
		header.Set("Authorization", "Bearer "+key)
	}
	return header, nil
}

// VoiceOptions lists the voices for the selected protocol, plus any configured
// custom default. Codex's native protocol uses its v1 voice set, not GA voices.
func (p *RealtimeProvider) VoiceOptions() (string, []string) {
	voices := []string{"alloy", "ash", "ballad", "coral", "echo", "sage", "shimmer", "verse", "marin", "cedar"}
	if p.platform == "codex" {
		voices = []string{"juniper", "maple", "spruce", "ember", "vale", "breeze", "arbor", "sol", "cove"}
	}
	if !slices.Contains(voices, p.voice) {
		voices = append(voices, p.voice)
	}
	return p.voice, voices
}

func (p *RealtimeProvider) sessionConfig(offer llmtypes.RealtimeOffer) map[string]any {
	voice := strings.TrimSpace(offer.Voice)
	if voice == "" {
		voice = p.voice
	}
	if p.platform == "codex" {
		return map[string]any{
			"model": p.model, "instructions": offer.Instructions,
			"audio":      map[string]any{"output": map[string]any{"voice": voice}},
			"delegation": map[string]any{"type": "client", "ack_filler": true},
		}
	}
	return map[string]any{
		"type": "realtime", "model": p.model, "instructions": offer.Instructions,
		"output_modalities": []string{"audio"}, "tool_choice": "auto",
		"audio": map[string]any{
			"input": map[string]any{
				"transcription": map[string]any{"model": "gpt-4o-mini-transcribe"},
				"turn_detection": map[string]any{
					"type": "server_vad", "create_response": true, "interrupt_response": true,
					"silence_duration_ms": 500,
				},
			},
			"output": map[string]any{"voice": voice},
		},
		"tools": []any{map[string]any{
			"type": "function", "name": realtimeDelegateTool,
			"description": "Delegate all user requests, including coding and general-purpose tasks, to the agent. Pass the user's complete request faithfully and immediately relay follow-ups, corrections, constraints, and new context about ongoing work. The agent determines feasibility and safety under the ordinary approval policy. Do not claim work succeeded until its result confirms it.",
			"parameters": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"request"},
				"properties": map[string]any{"request": map[string]any{"type": "string"}},
			},
		}},
	}
}

// Open creates one call (never retried), attaches its server-owned sideband, and
// only then returns the SDP answer. The caller's context owns the live session.
func (p *RealtimeProvider) Open(ctx context.Context, offer llmtypes.RealtimeOffer) (llmtypes.RealtimeSession, error) {
	if strings.TrimSpace(offer.SDP) == "" || len(offer.SDP) > realtimeMaxSDP || len(offer.Instructions) > realtimeMaxText {
		return nil, errors.New("invalid or oversized realtime offer")
	}
	setupCtx, cancel := context.WithTimeout(ctx, realtimeSetupTimeout)
	defer cancel()
	header, err := p.headers(setupCtx)
	if err != nil {
		return nil, err
	}
	endpoint := p.baseURL + "/realtime/calls"
	var body bytes.Buffer
	if p.platform == "codex" {
		endpoint += "?intent=quicksilver&architecture=avas"
		err = json.NewEncoder(&body).Encode(map[string]any{"sdp": offer.SDP, "session": p.sessionConfig(offer)})
		header.Set("Content-Type", "application/json")
	} else {
		writer := multipart.NewWriter(&body)
		session, _ := json.Marshal(p.sessionConfig(offer))
		_ = writer.WriteField("sdp", offer.SDP)
		_ = writer.WriteField("session", string(session))
		err = writer.Close()
		header.Set("Content-Type", writer.FormDataContentType())
	}
	if err != nil {
		return nil, errors.New("failed to encode realtime offer")
	}
	req, err := http.NewRequestWithContext(setupCtx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, errors.New("failed to create realtime request")
	}
	req.Header = header.Clone()
	response, err := p.client.Do(req)
	if err != nil {
		return nil, errors.New("realtime call request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.Errorf("realtime provider returned HTTP %d", response.StatusCode)
	}
	// Use only an opaque call ID. Never send credentials to Location's authority.
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return nil, errors.New("realtime provider returned an invalid call location")
	}
	segments := strings.Split(strings.Trim(location.Path, "/"), "/")
	callID := segments[len(segments)-1]
	if !realtimeCallID.MatchString(callID) {
		return nil, errors.New("realtime provider returned an invalid call ID")
	}
	header.Del("Content-Type")
	answer, err := io.ReadAll(io.LimitReader(response.Body, realtimeMaxSDP+1))
	if err != nil || len(answer) > realtimeMaxSDP || strings.TrimSpace(string(answer)) == "" {
		if p.closeCall(callID, header, nil) != nil {
			return nil, errors.New("realtime provider returned an invalid SDP answer; upstream call cleanup could not be confirmed")
		}
		return nil, errors.New("realtime provider returned an invalid SDP answer")
	}
	conn, err := p.dial(setupCtx, callID, header)
	if err != nil {
		closeErr := p.closeCall(callID, header, nil)
		if closeErr != nil {
			return nil, errors.New("realtime sideband connection failed; upstream call cleanup could not be confirmed")
		}
		return nil, errors.New("realtime sideband connection failed")
	}
	session := newRealtimeSession(ctx, p, callID, string(answer), header, conn)
	return session, nil
}

func (p *RealtimeProvider) dial(ctx context.Context, callID string, header http.Header) (*websocket.Conn, error) {
	endpoint, _ := url.Parse(p.sidebandBaseURL)
	if endpoint.Scheme == "http" {
		endpoint.Scheme = "ws"
	} else {
		endpoint.Scheme = "wss"
	}
	if p.platform == "codex" {
		endpoint.Path += "/live/" + callID
	} else {
		endpoint.Path += "/realtime"
		endpoint.RawQuery = url.Values{"call_id": []string{callID}}.Encode()
	}
	conn, response, err := p.dialer.DialContext(ctx, endpoint.String(), header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return conn, err
}

func (p *RealtimeProvider) closeCall(callID string, header http.Header, conn *websocket.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), realtimeWriteTimeout)
	defer cancel()
	if p.platform == "codex" {
		// Native Codex closes over its sideband. There is no verified separate
		// OAuth HTTP hangup route; a failed attachment gets one cleanup-only dial.
		cleanupOnly := conn == nil
		if cleanupOnly {
			var err error
			conn, err = p.dial(ctx, callID, header)
			if err != nil {
				return errors.New("could not close upstream realtime session")
			}
			defer conn.Close()
		}
		_ = conn.SetWriteDeadline(time.Now().Add(realtimeWriteTimeout))
		if err := conn.WriteJSON(map[string]any{"type": "session.close"}); err != nil {
			return errors.New("could not close upstream realtime session")
		}
		if cleanupOnly {
			deadline, _ := ctx.Deadline()
			_ = conn.SetReadDeadline(deadline)
			conn.SetReadLimit(realtimeMaxFrame)
			for {
				var event realtimeWireEvent
				if err := conn.ReadJSON(&event); err != nil {
					return errors.New("upstream realtime hangup was not acknowledged")
				}
				if event.Type == "session.closed" || event.Type == "session.close" {
					break
				}
			}
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/realtime/calls/"+callID+"/hangup", nil)
	if err != nil {
		return errors.New("could not close upstream realtime session")
	}
	req.Header = header.Clone()
	response, err := p.client.Do(req)
	if err != nil {
		return errors.New("could not close upstream realtime session")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusGone {
		return errors.New("could not close upstream realtime session")
	}
	return nil
}
