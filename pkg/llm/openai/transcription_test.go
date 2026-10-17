package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func transcriptionConfig(t *testing.T, platform string) llmtypes.Config {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_API_BASE", "")
	return llmtypes.Config{
		Provider: "openai", Model: "unused-chat-model",
		OpenAI: &llmtypes.OpenAIConfig{Platform: platform},
	}
}

func TestNewTranscriber(t *testing.T) {
	for _, test := range []struct {
		name, platform, model, baseURL, wantErr string
	}{
		{name: "OpenAI defaults", platform: "openai"},
		{name: "implicit OpenAI", platform: ""},
		{name: "normalized platform", platform: " OPENAI "},
		{name: "Codex without credentials", platform: "codex"},
		{name: "unsupported platform", platform: "copilot", wantErr: "supports only"},
		{name: "Codex model override", platform: "codex", model: defaultTranscriptionModel, wantErr: "cannot be set"},
		{name: "invalid URL", baseURL: "://bad", wantErr: "base_url"},
		{name: "URL credentials", baseURL: "https://user:secret@example.com/v1", wantErr: "base_url"},
		{name: "URL query", baseURL: "https://example.com/v1?key=secret", wantErr: "base_url"},
		{name: "URL fragment", baseURL: "https://example.com/v1#secret", wantErr: "base_url"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := transcriptionConfig(t, test.platform)
			config.OpenAI.BaseURL = test.baseURL
			before := *config.OpenAI
			s, err := NewTranscriber(config, test.model)
			assert.Equal(t, before, *config.OpenAI)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				assert.NotContains(t, err.Error(), "secret")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, s)
			assert.Equal(t, transcriptiontypes.RequestTimeout, s.client.Timeout)
			if s.platform == "codex" {
				assert.Empty(t, s.model)
				assert.Equal(t, codexEndpoint, s.endpoint)
			} else {
				assert.Equal(t, defaultTranscriptionModel, s.model)
				assert.Equal(t, openaipreset.BaseURL+"/audio/transcriptions", s.endpoint)
				assert.Equal(t, openaipreset.APIKeyEnvVar, s.apiKeyEnvVar)
			}
		})
	}
}

func TestOpenAITranscriptionAvailabilityResolvesCurrentConfiguredKey(t *testing.T) {
	config := transcriptionConfig(t, "openai")
	config.OpenAI.APIKeyEnvVar = "TEST_TRANSCRIPTION_KEY"
	t.Setenv("TEST_TRANSCRIPTION_KEY", "")
	t.Setenv("OPENAI_API_KEY", "must-not-fall-back")
	s, err := NewTranscriber(config, "")
	require.NoError(t, err)
	require.ErrorContains(t, s.CheckAvailability(), "TEST_TRANSCRIPTION_KEY")
	_, err = s.Transcribe(t.Context(), transcriptiontypes.Request{
		Audio: []byte("audio"), ContentType: "audio/wav",
	})
	require.ErrorContains(t, err, "API key is not configured")
	t.Setenv("TEST_TRANSCRIPTION_KEY", "configured-later")
	require.NoError(t, s.CheckAvailability())
}

func TestCodexTranscriptionCredentials(t *testing.T) {
	config := transcriptionConfig(t, "codex")
	t.Setenv("OPENAI_API_KEY", "must-not-fall-back")
	s, err := NewTranscriber(config, "")
	require.NoError(t, err)
	var creds *auth.CodexCredentials
	s.readCodex = func() (*auth.CodexCredentials, error) { return creds, nil }
	s.requestCodex = func(context.Context) (*auth.CodexCredentials, error) {
		t.Fatal("capabilities must never refresh credentials")
		return nil, errors.New("unexpected refresh")
	}
	require.ErrorContains(t, s.CheckAvailability(), "kodelet codex login")
	creds = &auth.CodexCredentials{AccessToken: "token", AccountID: "account"}
	require.NoError(t, s.CheckAvailability())
	creds.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	require.ErrorContains(t, s.CheckAvailability(), "expired")
	creds.RefreshToken = "refresh-on-request"
	require.NoError(t, s.CheckAvailability())
	s.readCodex = func() (*auth.CodexCredentials, error) { return nil, errors.New("private-token") }
	require.Error(t, s.CheckAvailability())
	assert.NotContains(t, s.CheckAvailability().Error(), "private-token")
	s.requestCodex = func(context.Context) (*auth.CodexCredentials, error) {
		return nil, errors.New("refresh failed: private-token in raw provider body")
	}
	_, err = s.Transcribe(t.Context(), transcriptiontypes.Request{
		Audio: []byte("audio"), ContentType: "audio/wav",
	})
	require.ErrorContains(t, err, "kodelet codex login")
	assert.NotContains(t, err.Error(), "private-token")
}

func TestTranscribeProviderRequests(t *testing.T) {
	for _, test := range []struct {
		name, platform  string
		environmentBase bool
	}{
		{name: "OpenAI profile", platform: "openai"},
		{name: "OpenAI environment override", platform: "openai", environmentBase: true},
		{name: "Codex dedicated endpoint", platform: "codex", environmentBase: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			platform := test.platform
			config := transcriptionConfig(t, platform)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Accept"))
				if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				file, header, err := r.FormFile("file")
				if !assert.NoError(t, err) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer file.Close()
				data, err := io.ReadAll(file)
				assert.NoError(t, err)
				assert.Equal(t, "private-audio", string(data))
				assert.Equal(t, "recording.webm", header.Filename)
				assert.Equal(t, "audio/webm", header.Header.Get("Content-Type"))
				assert.Equal(t, "en", r.FormValue("language"))
				if platform == "openai" {
					assert.Equal(t, "/custom/v1/audio/transcriptions", r.URL.Path)
					assert.Equal(t, "Bearer speech-key", r.Header.Get("Authorization"))
					assert.Equal(t, "custom-transcription-model", r.FormValue("model"))
				} else {
					assert.Equal(t, "/backend-api/transcribe", r.URL.Path)
					assert.Equal(t, "Bearer refreshed-token", r.Header.Get("Authorization"))
					assert.Equal(t, "account", r.Header.Get("ChatGPT-Account-ID"))
					assert.Equal(t, auth.CodexOriginator, r.Header.Get("originator"))
					assert.Equal(t, "kodelet/"+strings.TrimPrefix(version.Version, "v"), r.Header.Get("User-Agent"))
					assert.NotContains(t, r.MultipartForm.Value, "model")
				}
				_, _ = io.WriteString(w, `{"text":"  Fix the test.  "}`)
			}))
			t.Cleanup(server.Close)
			config.OpenAI.BaseURL = server.URL + "/custom/v1/"
			if test.environmentBase {
				profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					t.Error("transcription must not contact the overridden profile endpoint")
					w.WriteHeader(http.StatusInternalServerError)
				}))
				t.Cleanup(profileServer.Close)
				t.Setenv("OPENAI_API_BASE", config.OpenAI.BaseURL)
				config.OpenAI.BaseURL = profileServer.URL
			}
			config.OpenAI.APIKeyEnvVar = "TEST_TRANSCRIPTION_KEY"
			t.Setenv("TEST_TRANSCRIPTION_KEY", "speech-key")
			model := ""
			if platform == "openai" {
				model = "custom-transcription-model"
			}
			s, err := NewTranscriber(config, model)
			require.NoError(t, err)
			var credentialLoads atomic.Int32
			if platform == "codex" {
				assert.Equal(t, codexEndpoint, s.endpoint, "Codex must ignore OpenAI base URL overrides")
				s.endpoint = server.URL + "/backend-api/transcribe"
				s.readCodex = func() (*auth.CodexCredentials, error) {
					t.Fatal("requests must use the refresh-aware loader")
					return nil, errors.New("wrong credential loader")
				}
				s.requestCodex = func(ctx context.Context) (*auth.CodexCredentials, error) {
					credentialLoads.Add(1)
					deadline, ok := ctx.Deadline()
					assert.True(t, ok)
					assert.LessOrEqual(t, time.Until(deadline), transcriptiontypes.RequestTimeout)
					return &auth.CodexCredentials{AccessToken: "refreshed-token", AccountID: "account"}, nil
				}
			}
			for range 2 {
				result, err := s.Transcribe(t.Context(), transcriptiontypes.Request{
					Audio:       []byte("private-audio"),
					ContentType: "audio/webm;codecs=opus",
					Language:    " en ",
				})
				require.NoError(t, err)
				assert.Equal(t, "Fix the test.", result.Text)
			}
			assert.EqualValues(t, 2, calls.Load())
			if platform == "codex" {
				assert.EqualValues(t, 2, credentialLoads.Load(), "each request must resolve current credentials")
			}
		})
	}
}

func TestTranscribeRejectsInvalidInput(t *testing.T) {
	config := transcriptionConfig(t, "openai")
	s, err := NewTranscriber(config, "")
	require.NoError(t, err)
	for _, test := range []struct {
		name, contentType, language, wantErr string
		audio                                []byte
	}{
		{name: "empty", wantErr: "audio is empty"},
		{name: "too large", audio: make([]byte, transcriptiontypes.MaxAudioBytes+1), wantErr: "size limit"},
		{name: "invalid MIME", audio: []byte("audio"), contentType: "not a type", wantErr: "unsupported"},
		{name: "unsupported MIME", audio: []byte("audio"), contentType: "text/plain", wantErr: "unsupported"},
		{name: "long language", audio: []byte("audio"), contentType: "audio/wav", language: strings.Repeat("x", 65), wantErr: "language"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := s.Transcribe(t.Context(), transcriptiontypes.Request{
				Audio: test.audio, ContentType: test.contentType, Language: test.language,
			})
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestTranscribeProviderErrorsAreSafe(t *testing.T) {
	for _, test := range []struct {
		name, platform, response, wantErr, cfMitigated string
		status                                         int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, response: "secret token and private audio", wantErr: "HTTP 401"},
		{name: "OpenAI forbidden", status: http.StatusForbidden, response: "secret token", wantErr: "transcription provider returned HTTP 403"},
		{name: "invalid JSON", status: http.StatusOK, response: "secret token", wantErr: "invalid response"},
		{name: "wrong text type", status: http.StatusOK, response: `{"text":{"secret":"token"}}`, wantErr: "invalid response"},
		{name: "missing text", status: http.StatusOK, response: `{"secret":"token"}`, wantErr: "invalid response"},
		{name: "blank text", status: http.StatusOK, response: `{"text":" \n ","secret":"token"}`, wantErr: transcriptiontypes.ErrNoSpeech.Error()},
		{name: "multiple JSON values", status: http.StatusOK, response: `{"text":"secret"} {"text":"token"}`, wantErr: "invalid response"},
		{name: "oversized", status: http.StatusOK, response: strings.Repeat("x", int(transcriptiontypes.MaxResponseBytes)+1), wantErr: "size limit"},
		{
			name: "Codex challenge header", platform: "codex", status: http.StatusForbidden, cfMitigated: "Challenge",
			response: "private response", wantErr: "Cloudflare challenge",
		},
		{
			name: "Codex challenge HTML", platform: "codex", status: http.StatusForbidden,
			response: `<html><script>__cf_chl_tk = "private";</script></html>`, wantErr: "Cloudflare challenge",
		},
		{
			name: "Codex challenge script", platform: "codex", status: http.StatusForbidden,
			response: `<HTML><script src="/cdn-cgi/challenge-platform/scripts/private"></script></HTML>`, wantErr: "Cloudflare challenge",
		},
		{
			name: "Codex ordinary Cloudflare HTML", platform: "codex", status: http.StatusForbidden,
			response: "<html>Access denied. Cloudflare. private response</html>", wantErr: "access was denied by ChatGPT",
		},
		{
			name: "Codex JSON denial", platform: "codex", status: http.StatusForbidden,
			response: `{"error":"private account restriction"}`, wantErr: "access was denied by ChatGPT",
		},
		{
			name: "Codex bounded error body", platform: "codex", status: http.StatusForbidden,
			response: strings.Repeat("x", 16<<10) + "<html>__cf_chl private</html>", wantErr: "access was denied by ChatGPT",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := transcriptionConfig(t, test.platform)
			t.Setenv("OPENAI_API_KEY", "secret")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("cf-mitigated", test.cfMitigated)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.response)
			}))
			t.Cleanup(server.Close)
			config.OpenAI.BaseURL = server.URL
			s, err := NewTranscriber(config, "")
			require.NoError(t, err)
			if test.platform == "codex" {
				s.endpoint = server.URL
				s.requestCodex = func(context.Context) (*auth.CodexCredentials, error) {
					return &auth.CodexCredentials{AccessToken: "secret", AccountID: "account"}, nil
				}
			}
			_, err = s.Transcribe(t.Context(), transcriptiontypes.Request{
				Audio: []byte("private audio"), ContentType: "audio/wav", Language: "",
			})
			require.ErrorContains(t, err, test.wantErr)
			assert.NotContains(t, err.Error(), "secret")
			assert.NotContains(t, err.Error(), "private")
			assert.Equal(t, test.wantErr == transcriptiontypes.ErrNoSpeech.Error(), errors.Is(err, transcriptiontypes.ErrNoSpeech))
			assert.EqualValues(t, 1, calls.Load(), "no automatic billing fallback or retries")
			if test.platform == "codex" {
				assert.Contains(t, err.Error(), "HTTP 403")
				assert.Contains(t, err.Error(), "OpenAI API transcription profile (separate API billing)")
				assert.Equal(t, test.wantErr == "Cloudflare challenge", strings.Contains(err.Error(), "Cloudflare"))
			}
		})
	}
}

func TestTranscribeDoesNotFollowRedirects(t *testing.T) {
	config := transcriptionConfig(t, "openai")
	t.Setenv("OPENAI_API_KEY", "secret")
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Store(true) }))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	config.OpenAI.BaseURL = server.URL
	s, err := NewTranscriber(config, "")
	require.NoError(t, err)
	_, err = s.Transcribe(t.Context(), transcriptiontypes.Request{
		Audio: []byte("audio"), ContentType: "audio/wav", Language: "",
	})
	require.ErrorContains(t, err, "HTTP 307")
	assert.False(t, leaked.Load())
}

func TestTranscribeTransportErrorDoesNotLeakURL(t *testing.T) {
	config := transcriptionConfig(t, "openai")
	t.Setenv("OPENAI_API_KEY", "private-token")
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	s, err := NewTranscriber(config, "")
	require.NoError(t, err)
	// Production configuration rejects query parameters. Still prove that a
	// transport's URL-bearing error can never expose a credential to the UI.
	s.endpoint = server.URL + "/audio/transcriptions?key=private-token"
	_, err = s.Transcribe(t.Context(), transcriptiontypes.Request{
		Audio: []byte("private-audio"), ContentType: "audio/wav", Language: "",
	})
	require.EqualError(t, err, "transcription request failed")
}

func TestTranscribeCancellation(t *testing.T) {
	config := transcriptionConfig(t, "openai")
	t.Setenv("OPENAI_API_KEY", "secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	config.OpenAI.BaseURL = server.URL
	s, err := NewTranscriber(config, "")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = s.Transcribe(ctx, transcriptiontypes.Request{
		Audio: []byte("audio"), ContentType: "audio/wav", Language: "",
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	_, err = s.Transcribe(ctx, transcriptiontypes.Request{
		Audio: []byte("audio"), ContentType: "audio/wav", Language: "",
	})
	require.ErrorIs(t, err, context.Canceled)
}
