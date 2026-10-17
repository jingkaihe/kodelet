package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type protocolTranscriber struct {
	availabilityErr error
	transcribe      func(context.Context, llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error)
}

func TestNewTranscriberFromViper(t *testing.T) {
	for _, test := range []struct {
		name, key, wantErr string
		value              any
		disabled           bool
	}{
		{name: "explicit speech profile"},
		{name: "Codex without credentials", key: "profiles.speech.openai.platform", value: "codex"},
		{name: "missing profile", key: "transcription.profile", value: " ", wantErr: "transcription.profile is required"},
		{name: "unknown profile", key: "transcription.profile", value: "missing", wantErr: "invalid transcription profile"},
		{name: "unsupported provider", key: "transcription.profile", value: "coding", wantErr: "provider: openai"},
		{name: "unsupported platform", key: "profiles.speech.openai.platform", value: "copilot", wantErr: "supports only"},
		{name: "invalid switch", key: "transcription.enabled", value: "nonsense", wantErr: "invalid transcription configuration"},
		{name: "disabled ignores missing profile", key: "transcription.profile", value: "missing", disabled: true},
		{name: "disabled ignores invalid profile", key: "profiles.speech", value: "invalid", disabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set("profile", "coding")
			viper.Set("profiles.coding", map[string]any{
				"provider": "anthropic", "model": "coding-model",
			})
			viper.Set("profiles.speech", map[string]any{
				"provider": "openai", "model": "unused-chat-model",
			})
			viper.Set("transcription.enabled", !test.disabled)
			viper.Set("transcription.profile", "speech")
			if test.key != "" {
				viper.Set(test.key, test.value)
			}
			before := viper.AllSettings()
			transcriber, err := newTranscriberFromViper()
			assert.Equal(t, before, viper.AllSettings(), "must not alter the active chat profile")
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			if test.disabled {
				assert.Nil(t, transcriber)
			} else {
				assert.NotNil(t, transcriber)
			}
		})
	}

	t.Run("disabled by default", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		transcriber, err := newTranscriberFromViper()
		require.NoError(t, err)
		assert.Nil(t, transcriber)
	})
}

func TestTranscriptionProfileRoundTrip(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer speech-key", r.Header.Get("Authorization"))
		assert.Equal(t, "/speech/v1/audio/transcriptions", r.URL.Path)
		if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		assert.Equal(t, "speech-model", r.FormValue("model"))
		assert.NoError(t, json.NewEncoder(w).Encode(transcriptiontypes.Result{Text: "Recognized words."}))
	}))
	t.Cleanup(provider.Close)
	viper.Set("profile", "coding")
	viper.Set("profiles.coding", map[string]any{"provider": "anthropic", "model": "chat-model"})
	viper.Set("profiles.speech", map[string]any{
		"provider": "openai", "model": "unused-chat-model", "transcription_model": "speech.v1",
		"openai": map[string]any{
			"base_url": provider.URL + "/speech/v1/", "api_key_env_var": "TEST_TRANSCRIPTION_KEY",
		},
	})
	viper.Set("aliases", map[string]string{"speech.v1": "speech-model", "speech-model": "do-not-resolve-twice"})
	viper.Set("transcription.enabled", true)
	viper.Set("transcription.profile", "speech")
	t.Setenv("TEST_TRANSCRIPTION_KEY", "")
	t.Setenv("OPENAI_API_KEY", "must-not-fall-back")
	transcriber, err := newTranscriberFromViper()
	require.NoError(t, err)
	server := httptest.NewServer(transcriptionProtocolServer(transcriber))
	t.Cleanup(server.Close)
	client, err := chat.NewClient(server.URL, "user-token", "")
	require.NoError(t, err)
	caps, err := client.TranscriptionCapabilities(t.Context())
	require.NoError(t, err)
	assert.True(t, caps.Enabled)
	assert.False(t, caps.Available)
	assert.Contains(t, caps.Reason, "TEST_TRANSCRIPTION_KEY")
	t.Setenv("TEST_TRANSCRIPTION_KEY", "speech-key")
	caps, err = client.TranscriptionCapabilities(t.Context())
	require.NoError(t, err)
	assert.Equal(t, availableTranscription(), caps)
	result, err := client.TranscribeAudio(t.Context(), []byte("RIFF1234WAVEdata"), "clip.wav", "audio/wav", "")
	require.NoError(t, err)
	assert.Equal(t, "Recognized words.", result.Text)
}

func (f *protocolTranscriber) CheckAvailability() error { return f.availabilityErr }

func (f *protocolTranscriber) Transcribe(ctx context.Context, request llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error) {
	return f.transcribe(ctx, request)
}

func transcriptionProtocolServer(service llmtypes.Transcriber) *Server {
	s := &Server{
		router:        mux.NewRouter(),
		config:        &ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "user-token", RunnerAuthToken: "runner-token"},
		transcription: service,
	}
	s.setupRoutes()
	return s
}

func availableTranscription() transcriptiontypes.Capabilities {
	return transcriptiontypes.Capabilities{
		Enabled: true, Available: true,
		MaxAudioBytes:      transcriptiontypes.MaxAudioBytes,
		MaxDurationSeconds: transcriptiontypes.MaxDurationSeconds,
		MimeTypes:          transcriptiontypes.SupportedMimeTypes(),
	}
}

type transcriptionPart struct {
	name, filename, contentType string
	data                        []byte
}

func transcriptionRequest(t *testing.T, parts ...transcriptionPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		header := make(textproto.MIMEHeader)
		params := map[string]string{"name": part.name}
		if part.filename != "" {
			params["filename"] = part.filename
		}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", params))
		if part.contentType != "" {
			header.Set("Content-Type", part.contentType)
		}
		w, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = w.Write(part.data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, transcriptiontypes.Path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer user-token")
	return request
}

func TestTranscriptionCapabilitiesAndAvailability(t *testing.T) {
	available := availableTranscription()
	missingCredentials := available
	missingCredentials.Available = false
	missingCredentials.Reason = "Sign in to enable transcription."
	for _, test := range []struct {
		name    string
		service llmtypes.Transcriber
		want    transcriptiontypes.Capabilities
	}{
		{
			name: "disabled",
			want: transcriptiontypes.Capabilities{
				Reason:             "Transcription is disabled.",
				MaxAudioBytes:      transcriptiontypes.MaxAudioBytes,
				MaxDurationSeconds: transcriptiontypes.MaxDurationSeconds,
				MimeTypes:          transcriptiontypes.SupportedMimeTypes(),
			},
		},
		{name: "available", service: &protocolTranscriber{}, want: available},
		{name: "credentials missing", service: &protocolTranscriber{availabilityErr: errors.New(missingCredentials.Reason)}, want: missingCredentials},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := transcriptionProtocolServer(test.service)
			request := httptest.NewRequest(http.MethodGet, transcriptiontypes.Path, nil)
			request.Header.Set("Authorization", "Bearer user-token")
			response := httptest.NewRecorder()
			s.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			var got transcriptiontypes.Capabilities
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))
			assert.Equal(t, test.want, got)
			if !test.want.Available {
				response = httptest.NewRecorder()
				s.ServeHTTP(response, transcriptionRequest(t))
				assert.Equal(t, http.StatusServiceUnavailable, response.Code)
				assert.Contains(t, response.Body.String(), test.want.Reason)
			}
		})
	}
}

func TestTranscriptionClientRoundTripWithoutConversationOrRunner(t *testing.T) {
	// These fixtures test container recognition only; codec decoding belongs to
	// the upstream provider, not the control-plane protocol.
	for _, test := range []struct {
		name, audio, declared, canonical, filename, language string
	}{
		{"webm", "\x1a\x45\xdf\xa3audio", "audio/webm;codecs=opus", "audio/webm", "recording.webm", "en"},
		{"mp4", "\x00\x00\x00\x18ftypM4A ", "audio/mp4;codecs=mp4a.40.2", "audio/mp4", "recording.m4a", ""},
		{"wav", "RIFF\x24\x00\x00\x00WAVEdata", "audio/wav", "audio/wav", "recording.wav", "en-GB"},
		{"wav alias", "RIFF\x24\x00\x00\x00WAVEdata", "audio/x-wav", "audio/wav", "recording.wav", ""},
		{"generic upload", "RIFF\x24\x00\x00\x00WAVEdata", "application/octet-stream", "audio/wav", "recording.wav", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := make(chan llmtypes.TranscriptionRequest, 1)
			service := &protocolTranscriber{
				transcribe: func(ctx context.Context, request llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error) {
					deadline, ok := ctx.Deadline()
					assert.True(t, ok)
					assert.WithinDuration(t, time.Now().Add(transcriptiontypes.RequestTimeout), deadline, 2*time.Second)
					calls <- request
					return llmtypes.TranscriptionResult{Text: "  Fix the login test. \n"}, nil
				},
			}
			s := transcriptionProtocolServer(service)
			assert.Nil(t, s.chatRunner)
			assert.Nil(t, s.conversationService)
			server := httptest.NewServer(s)
			t.Cleanup(server.Close)
			client, err := chat.NewClient(server.URL, "user-token", "")
			require.NoError(t, err)
			capabilities, err := client.TranscriptionCapabilities(t.Context())
			require.NoError(t, err)
			assert.Equal(t, availableTranscription(), capabilities)
			result, err := client.TranscribeAudio(t.Context(), []byte(test.audio), "private-name.bin", test.declared, test.language)
			require.NoError(t, err)
			assert.Equal(t, "Fix the login test.", result.Text)
			got := <-calls
			assert.Equal(t, test.audio, string(got.Audio))
			assert.Equal(t, test.filename, got.Filename, "client filenames must not be forwarded upstream")
			assert.Equal(t, test.canonical, got.ContentType)
			assert.Equal(t, test.language, got.Language)
		})
	}
}

func TestTranscriptionRejectsInvalidUploads(t *testing.T) {
	valid := transcriptionPart{"file", "recording.webm", "audio/webm;codecs=opus", []byte{0x1a, 0x45, 0xdf, 0xa3, 1}}
	for _, test := range []struct {
		name   string
		parts  []transcriptionPart
		status int
	}{
		{"missing file", nil, http.StatusBadRequest},
		{"empty file", []transcriptionPart{{"file", "empty.wav", "audio/wav", nil}}, http.StatusBadRequest},
		{"no filename", []transcriptionPart{{"file", "", "audio/webm", valid.data}}, http.StatusBadRequest},
		{"duplicate file", []transcriptionPart{valid, valid}, http.StatusBadRequest},
		{"unknown field", []transcriptionPart{valid, {"profile", "", "", []byte("codex")}}, http.StatusBadRequest},
		{"duplicate language", []transcriptionPart{valid, {"language", "", "", []byte("en")}, {"language", "", "", []byte("fr")}}, http.StatusBadRequest},
		{"language as file", []transcriptionPart{valid, {"language", "lang.txt", "text/plain", []byte("en")}}, http.StatusBadRequest},
		{"invalid language", []transcriptionPart{valid, {"language", "", "", []byte("en\nignore")}}, http.StatusBadRequest},
		{"unsupported container", []transcriptionPart{{"file", "recording.wav", "audio/wav", []byte("not audio")}}, http.StatusUnsupportedMediaType},
		{"mismatched MIME", []transcriptionPart{{"file", "recording.wav", "audio/wav", valid.data}}, http.StatusUnsupportedMediaType},
		{"unsupported MIME", []transcriptionPart{{"file", "recording.webm", "text/plain", valid.data}}, http.StatusUnsupportedMediaType},
		{"malformed MIME", []transcriptionPart{{"file", "recording.webm", "audio/webm;codecs", valid.data}}, http.StatusUnsupportedMediaType},
		{"oversize", []transcriptionPart{{"file", "large.wav", "audio/wav", make([]byte, transcriptiontypes.MaxAudioBytes+1)}}, http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := transcriptionProtocolServer(&protocolTranscriber{
				transcribe: func(context.Context, llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error) {
					t.Error("invalid upload reached provider")
					return llmtypes.TranscriptionResult{}, nil
				},
			})
			response := httptest.NewRecorder()
			s.ServeHTTP(response, transcriptionRequest(t, test.parts...))
			assert.Equal(t, test.status, response.Code, response.Body.String())
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
			var envelope struct {
				Error   string
				Status  int
				Success bool
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			assert.NotEmpty(t, envelope.Error)
			assert.Equal(t, test.status, envelope.Status)
			assert.False(t, envelope.Success)
		})
	}

	s := transcriptionProtocolServer(&protocolTranscriber{})
	request := httptest.NewRequest(http.MethodPost, transcriptiontypes.Path, strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestTranscriptionProviderFailureAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name, text string
		err        error
		status     int
	}{
		{"provider failure", "", errors.New("transcription provider unavailable"), http.StatusBadGateway},
		{"provider deadline", "", errors.Wrap(context.DeadlineExceeded, "transcription timed out"), http.StatusGatewayTimeout},
		{"no speech sentinel", "", errors.Wrap(llmtypes.ErrNoSpeech, "transcription"), http.StatusUnprocessableEntity},
		{"empty transcript", " \n", nil, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := transcriptionProtocolServer(&protocolTranscriber{
				transcribe: func(context.Context, llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error) {
					return llmtypes.TranscriptionResult{Text: test.text}, test.err
				},
			})
			response := httptest.NewRecorder()
			s.ServeHTTP(response, transcriptionRequest(t, transcriptionPart{"file", "clip.wav", "audio/wav", []byte("RIFF1234WAVEdata")}))
			assert.Equal(t, test.status, response.Code)
			assert.Contains(t, response.Body.String(), "error")
		})
	}

	t.Run("caller cancels provider work", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		s := transcriptionProtocolServer(&protocolTranscriber{
			transcribe: func(ctx context.Context, _ llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error) {
				cancel()
				assert.ErrorIs(t, ctx.Err(), context.Canceled)
				return llmtypes.TranscriptionResult{}, ctx.Err()
			},
		})
		request := transcriptionRequest(t, transcriptionPart{"file", "clip.wav", "audio/wav", []byte("RIFF1234WAVEdata")}).WithContext(ctx)
		response := httptest.NewRecorder()
		s.ServeHTTP(response, request)
		assert.Empty(t, response.Body.String(), "disconnected requests must not receive a transcript or error payload")
	})
}

func TestTranscriptionRouteAuthenticationAndCSRF(t *testing.T) {
	s := transcriptionProtocolServer(&protocolTranscriber{
		transcribe: func(context.Context, llmtypes.TranscriptionRequest) (llmtypes.TranscriptionResult, error) {
			return llmtypes.TranscriptionResult{Text: "dictated text"}, nil
		},
	})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, token := range []string{"", "wrong-token", "runner-token"} {
			t.Run(method+"/"+token, func(t *testing.T) {
				request := httptest.NewRequest(method, transcriptiontypes.Path, nil)
				if token != "" {
					request.Header.Set("Authorization", "Bearer "+token)
				}
				response := httptest.NewRecorder()
				s.ServeHTTP(response, request)
				assert.Equal(t, http.StatusUnauthorized, response.Code)
			})
		}
	}

	store, _ := newAuthStoreTest(t)
	s.authStore = store
	s.config.WebAuthMode = WebAuthModeOIDC
	sessionToken, csrfToken, err := store.CreateWebSession(t.Context(), "issuer", "subject", "User", "user@example.com", []string{string(RoleUser)}, time.Hour)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, transcriptiontypes.Path, nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	assert.Equal(t, http.StatusOK, response.Code, "capability discovery needs no CSRF token")
	for _, test := range []struct {
		name, cookie, header string
		status               int
	}{
		{"missing", "", "", http.StatusForbidden},
		{"cookie only", csrfToken, "", http.StatusForbidden},
		{"header only", "", csrfToken, http.StatusForbidden},
		{"mismatch", csrfToken, "wrong-token", http.StatusForbidden},
		{"valid", csrfToken, csrfToken, http.StatusOK},
	} {
		t.Run("OIDC/"+test.name, func(t *testing.T) {
			request := transcriptionRequest(t, transcriptionPart{"file", "clip.wav", "audio/wav", []byte("RIFF1234WAVEdata")})
			request.Header.Del("Authorization")
			request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: test.cookie})
			}
			if test.header != "" {
				request.Header.Set(webCSRFHeaderName, test.header)
			}
			response := httptest.NewRecorder()
			s.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code, response.Body.String())
		})
	}
}
