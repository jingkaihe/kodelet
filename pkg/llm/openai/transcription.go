package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
)

const (
	defaultTranscriptionModel = "gpt-4o-mini-transcribe"
	transcriptionBaseURL      = "https://api.openai.com/v1"
	codexEndpoint             = "https://chatgpt.com/backend-api/transcribe"
	codexLoginReason          = "Sign in on the server with 'kodelet codex login' to use transcription."
	codexExpiredReason        = "ChatGPT credentials have expired; sign in on the server with 'kodelet codex login'."
)

// Transcriber holds immutable OpenAI/Codex speech recognition settings. Credentials are resolved at
// request time so signing in after server startup does not require a restart.
type Transcriber struct {
	platform     string
	endpoint     string
	model        string
	apiKeyEnvVar string
	client       *http.Client
	readCodex    func() (*auth.CodexCredentials, error)
	requestCodex func(context.Context) (*auth.CodexCredentials, error)
}

// NewTranscriber validates typed provider configuration without reading or
// refreshing credentials. It is independent of chat threads and root enablement.
func NewTranscriber(config llmtypes.Config) (*Transcriber, error) {
	s := &Transcriber{platform: "openai", apiKeyEnvVar: "OPENAI_API_KEY"}
	baseURL := transcriptionBaseURL
	if config.OpenAI != nil {
		if platform := strings.ToLower(strings.TrimSpace(config.OpenAI.Platform)); platform != "" {
			s.platform = platform
		}
		if value := strings.TrimSpace(config.OpenAI.BaseURL); value != "" {
			baseURL = value
		}
		if value := strings.TrimSpace(config.OpenAI.APIKeyEnvVar); value != "" {
			s.apiKeyEnvVar = value
		}
	}
	s.model = strings.TrimSpace(config.TranscriptionModel)
	switch s.platform {
	case "openai":
		base, err := url.Parse(baseURL)
		if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") ||
			base.User != nil || base.RawQuery != "" || base.Fragment != "" {
			return nil, errors.New("transcription profile openai.base_url must be an HTTP(S) URL without credentials, query, or fragment")
		}
		s.endpoint = strings.TrimRight(base.String(), "/") + "/audio/transcriptions"
		if s.model == "" {
			s.model = defaultTranscriptionModel
		}
	case "codex":
		if s.model != "" {
			return nil, errors.New("transcription_model cannot be set for platform codex; ChatGPT selects the model")
		}
		s.endpoint = codexEndpoint
	default:
		return nil, errors.New("transcription supports only openai.platform openai or codex")
	}
	s.client = &http.Client{
		Timeout: llmtypes.TranscriptionRequestTimeout,
		// Never resend private audio to a redirect target. A redirect is reported
		// as a provider error rather than following a new upload destination.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	s.readCodex = auth.GetCodexCredentials
	s.requestCodex = auth.GetCodexCredentialsForRequest
	return s, nil
}

// CheckAvailability checks current local credentials without a network request
// or token refresh. It is not a provider access probe.
func (s *Transcriber) CheckAvailability() error {
	if s.platform == "codex" {
		creds, err := s.readCodex()
		if err != nil || !auth.IsCodexOAuthEnabled(creds) {
			return errors.New(codexLoginReason)
		}
		if creds.ExpiresAt > 0 && creds.ExpiresAt <= time.Now().Unix() && creds.RefreshToken == "" {
			return errors.New(codexExpiredReason)
		}
	} else if strings.TrimSpace(os.Getenv(s.apiKeyEnvVar)) == "" {
		return errors.New("Configure " + s.apiKeyEnvVar + " on the server to use transcription.")
	}
	return nil
}

// Transcribe uploads a bounded recording to the configured provider. No audio
// is persisted and neither upstream error bodies nor credential errors escape.
func (s *Transcriber) Transcribe(ctx context.Context, input llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error) {
	audio, filename, contentType, language := input.Audio, input.Filename, input.ContentType, input.Language
	ctx, cancel := context.WithTimeout(ctx, llmtypes.TranscriptionRequestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return llmtypes.TranscriptionResult{}, errors.Wrap(err, "transcription canceled")
	}
	if len(audio) == 0 {
		return llmtypes.TranscriptionResult{}, errors.New("audio is empty")
	}
	if int64(len(audio)) > llmtypes.MaxTranscriptionAudioBytes {
		return llmtypes.TranscriptionResult{}, errors.New("audio exceeds the transcription size limit")
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !slices.Contains(llmtypes.SupportedTranscriptionMimeTypes(), mediaType) {
		return llmtypes.TranscriptionResult{}, errors.New("unsupported transcription audio type")
	}
	filename = path.Base(strings.ReplaceAll(filename, `\`, "/"))
	if filename == "" || filename == "." || filename == "/" || len(filename) > 255 || strings.ContainsAny(filename, "\r\n\x00") {
		return llmtypes.TranscriptionResult{}, errors.New("invalid transcription filename")
	}
	language = strings.TrimSpace(language)
	if len(language) > 64 {
		return llmtypes.TranscriptionResult{}, errors.New("transcription language hint is too long")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name": "file", "filename": filename,
	}))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return llmtypes.TranscriptionResult{}, errors.Wrap(err, "failed to prepare transcription audio")
	}
	if _, err := part.Write(audio); err != nil {
		return llmtypes.TranscriptionResult{}, errors.Wrap(err, "failed to prepare transcription audio")
	}
	if s.model != "" {
		_ = writer.WriteField("model", s.model)
	}
	if language != "" {
		_ = writer.WriteField("language", language)
	}
	if err := writer.Close(); err != nil {
		return llmtypes.TranscriptionResult{}, errors.Wrap(err, "failed to prepare transcription upload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, &body)
	if err != nil {
		return llmtypes.TranscriptionResult{}, errors.New("failed to create transcription request")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if s.platform == "codex" {
		creds, err := s.requestCodex(ctx)
		if err != nil || !auth.IsCodexOAuthEnabled(creds) {
			return llmtypes.TranscriptionResult{}, requestError(ctx, codexLoginReason)
		}
		req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		req.Header.Set("ChatGPT-Account-ID", creds.AccountID)
		req.Header.Set("originator", auth.CodexOriginator)
		// The ChatGPT edge can challenge Go's generic user agent even with
		// valid OAuth credentials. Identify this application explicitly.
		req.Header.Set("User-Agent", "kodelet/"+strings.TrimPrefix(version.Version, "v"))
	} else {
		key := strings.TrimSpace(os.Getenv(s.apiKeyEnvVar))
		if key == "" {
			return llmtypes.TranscriptionResult{}, errors.New("transcription API key is not configured on the server")
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return llmtypes.TranscriptionResult{}, requestError(ctx, "transcription request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if s.platform == "codex" && resp.StatusCode == http.StatusForbidden {
			return llmtypes.TranscriptionResult{}, codexForbiddenError(resp)
		}
		return llmtypes.TranscriptionResult{}, errors.Errorf("transcription provider returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, llmtypes.MaxTranscriptionResponseBytes+1))
	if err != nil {
		return llmtypes.TranscriptionResult{}, requestError(ctx, "failed to read transcription response")
	}
	if int64(len(data)) > llmtypes.MaxTranscriptionResponseBytes {
		return llmtypes.TranscriptionResult{}, errors.New("transcription response exceeds the size limit")
	}
	var result struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return llmtypes.TranscriptionResult{}, errors.New("transcription provider returned an invalid response")
	}
	if result.Text == nil {
		return llmtypes.TranscriptionResult{}, errors.New("transcription provider returned an invalid response")
	}
	text := strings.TrimSpace(*result.Text)
	if text == "" {
		return llmtypes.TranscriptionResult{}, llmtypes.ErrNoSpeech
	}
	return llmtypes.TranscriptionResult{Text: text}, nil
}

func codexForbiddenError(response *http.Response) error {
	challenge := strings.EqualFold(response.Header.Get("cf-mitigated"), "challenge")
	if !challenge {
		// Inspect a bounded prefix for challenge markers, never returning or
		// logging the provider body. Merely being proxied by Cloudflare is not
		// evidence of a challenge.
		data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		body := strings.ToLower(string(data))
		challenge = strings.Contains(body, "<html") &&
			(strings.Contains(body, "__cf_chl") || strings.Contains(body, "/cdn-cgi/challenge-platform/"))
	}
	if challenge {
		return errors.New("transcription via ChatGPT was blocked by a Cloudflare challenge (HTTP 403). This experimental endpoint may be unavailable from the server's network. Use an OpenAI API transcription profile (separate API billing), or try a different server network")
	}
	return errors.New("transcription access was denied by ChatGPT (HTTP 403). A working Codex chat login does not guarantee access to this experimental endpoint. Use an OpenAI API transcription profile (separate API billing)")
}

func requestError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, "transcription canceled")
	}
	return errors.New(message)
}
