package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	transcribe      func(context.Context, transcriptiontypes.Request) (transcriptiontypes.Result, error)
}

func TestNewTranscriberFromViper(t *testing.T) {
	for _, test := range []struct {
		name, key, wantErr string
		value              any
		enabled            bool
	}{
		{name: "disabled by default"},
		{name: "missing profile", enabled: true, key: "transcription.profile", value: " ", wantErr: "transcription.profile is required"},
		{name: "unknown profile", enabled: true, key: "transcription.profile", value: "missing", wantErr: "invalid transcription profile"},
		{name: "invalid speech model", enabled: true, key: "profiles.speech.transcription_model", value: []string{"invalid"}, wantErr: "transcription_model in profile"},
		{name: "invalid switch", key: "transcription.enabled", value: "nonsense", wantErr: "invalid transcription configuration"},
		{name: "disabled ignores missing profile", key: "transcription.profile", value: "missing"},
		{name: "disabled ignores invalid profile", key: "profiles.speech", value: "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			if test.enabled {
				viper.Set("transcription.enabled", true)
			}
			viper.Set("transcription.profile", "speech")
			viper.Set("profiles.speech", map[string]any{"provider": "openai", "model": "chat-model"})
			if test.key != "" {
				viper.Set(test.key, test.value)
			}
			transcriber, err := newTranscriberFromViper()
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Nil(t, transcriber)
		})
	}
}

func TestTranscriptionProfileRoundTrip(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("OPENAI_API_BASE", "")
	languages := make(chan string, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer speech-key", r.Header.Get("Authorization"))
		assert.Equal(t, "/speech/v1/audio/transcriptions", r.URL.Path)
		if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		assert.Equal(t, "speech-model", r.FormValue("model"))
		file, header, err := r.FormFile("file")
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		audio, err := io.ReadAll(file)
		assert.NoError(t, err)
		assert.Equal(t, "RIFF1234WAVEdata", string(audio))
		assert.Equal(t, "recording.wav", header.Filename, "client filenames must not reach the provider")
		assert.Equal(t, "audio/wav", header.Header.Get("Content-Type"))
		languages <- r.FormValue("language")
		assert.NoError(t, json.NewEncoder(w).Encode(transcriptiontypes.Result{Text: "  Recognized words. \n"}))
	}))
	t.Cleanup(provider.Close)
	viper.Set("profile", "coding")
	viper.Set("profiles", map[string]any{
		"coding": map[string]any{"provider": "anthropic", "model": "chat-model"},
		"speech.dev": map[string]any{
			"provider": "openai", "model": "unused-chat-model", "transcription_model": " speech.v1 ",
			"aliases": map[string]string{"speech.v1": "speech-model"},
			"openai": map[string]any{
				"base_url": provider.URL + "/speech/v1/", "api_key_env_var": "TEST_TRANSCRIPTION_KEY",
			},
		},
	})
	viper.Set("aliases", map[string]string{"speech.v1": "overridden-model", "speech-model": "do-not-resolve-twice"})
	viper.Set("transcription.enabled", true)
	viper.Set("transcription.profile", "speech.dev")
	t.Setenv("TEST_TRANSCRIPTION_KEY", "speech-key")
	before := viper.AllSettings()
	transcriber, err := newTranscriberFromViper()
	require.NoError(t, err)
	assert.Equal(t, before, viper.AllSettings(), "must not alter the active chat profile")
	s := transcriptionProtocolServer(transcriber)
	assert.Nil(t, s.chatRunner)
	assert.Nil(t, s.conversationService)
	server := httptest.NewServer(http.StripPrefix("/prefix", s))
	t.Cleanup(server.Close)
	client, err := chat.NewClient(server.URL+"/prefix", "user-token", "unused-runner")
	require.NoError(t, err)
	caps, err := client.TranscriptionCapabilities(t.Context())
	require.NoError(t, err)
	assert.Equal(t, availableTranscription(), caps)
	for _, language := range []string{"", "en-GB"} {
		result, err := client.TranscribeAudio(t.Context(), []byte("RIFF1234WAVEdata"), "private-name.wav", "audio/wav", language)
		require.NoError(t, err)
		assert.Equal(t, "Recognized words.", result.Text)
		assert.Equal(t, language, <-languages)
	}
}

func (f *protocolTranscriber) CheckAvailability() error { return f.availabilityErr }

func (f *protocolTranscriber) Transcribe(ctx context.Context, request transcriptiontypes.Request) (transcriptiontypes.Result, error) {
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
	for _, test := range []struct {
		name, reason string
		service      llmtypes.Transcriber
	}{
		{"disabled", "Transcription is disabled.", nil},
		{"credentials missing", "sign in to enable transcription", &protocolTranscriber{availabilityErr: errors.New("sign in to enable transcription")}},
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
			assert.Equal(t, test.service != nil, got.Enabled)
			assert.False(t, got.Available)
			assert.Equal(t, test.reason, got.Reason)
			response = httptest.NewRecorder()
			s.ServeHTTP(response, transcriptionRequest(t))
			assert.Equal(t, http.StatusServiceUnavailable, response.Code)
			assert.Contains(t, response.Body.String(), test.reason)
		})
	}
}

func TestReadTranscriptionUpload(t *testing.T) {
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
			request := transcriptionRequest(t,
				transcriptionPart{"file", "private-name.bin", test.declared, []byte(test.audio)},
				transcriptionPart{"language", "", "", []byte(test.language)},
			)
			got, err := readTranscriptionUpload(request)
			require.NoError(t, err)
			assert.Equal(t, test.audio, string(got.Audio))
			assert.Equal(t, test.canonical, got.ContentType)
			assert.Equal(t, test.language, got.Language)
			format, err := transcriptiontypes.ParseAudioFormat(got.ContentType)
			require.NoError(t, err)
			assert.Equal(t, test.filename, format.Filename)
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
				transcribe: func(context.Context, transcriptiontypes.Request) (transcriptiontypes.Result, error) {
					t.Error("invalid upload reached provider")
					return transcriptiontypes.Result{}, nil
				},
			})
			response := httptest.NewRecorder()
			s.ServeHTTP(response, transcriptionRequest(t, test.parts...))
			assert.Equal(t, test.status, response.Code, response.Body.String())
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
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

func TestTranscriptionProviderErrors(t *testing.T) {
	for _, test := range []struct {
		name, text string
		err        error
		status     int
	}{
		{"provider failure", "", errors.New("transcription provider unavailable"), http.StatusBadGateway},
		{"provider deadline", "", errors.Wrap(context.DeadlineExceeded, "transcription timed out"), http.StatusGatewayTimeout},
		{"no speech sentinel", "", errors.Wrap(transcriptiontypes.ErrNoSpeech, "transcription"), http.StatusUnprocessableEntity},
		{"empty transcript", " \n", nil, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := transcriptionProtocolServer(&protocolTranscriber{
				transcribe: func(context.Context, transcriptiontypes.Request) (transcriptiontypes.Result, error) {
					return transcriptiontypes.Result{Text: test.text}, test.err
				},
			})
			response := httptest.NewRecorder()
			s.ServeHTTP(response, transcriptionRequest(t, transcriptionPart{"file", "clip.wav", "audio/wav", []byte("RIFF1234WAVEdata")}))
			assert.Equal(t, test.status, response.Code)
			assert.Contains(t, response.Body.String(), "error")
		})
	}
}

func TestTranscriptionClientCancellationReachesProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	server := httptest.NewServer(transcriptionProtocolServer(&protocolTranscriber{
		transcribe: func(ctx context.Context, _ transcriptiontypes.Request) (transcriptiontypes.Result, error) {
			deadline, ok := ctx.Deadline()
			assert.True(t, ok)
			assert.WithinDuration(t, time.Now().Add(transcriptiontypes.RequestTimeout), deadline, 2*time.Second)
			cancel()
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Error("client cancellation did not reach provider")
			}
			finished <- ctx.Err()
			return transcriptiontypes.Result{}, ctx.Err()
		},
	}))
	t.Cleanup(server.Close)
	client, err := chat.NewClient(server.URL, "user-token", "")
	require.NoError(t, err)
	_, err = client.TranscribeAudio(ctx, []byte("RIFF1234WAVEdata"), "clip.wav", "audio/wav", "")
	require.ErrorIs(t, err, context.Canceled)
	select {
	case err := <-finished:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not finish after cancellation")
	}
}

func TestTranscriptionRouteAuthenticationAndCSRF(t *testing.T) {
	s := transcriptionProtocolServer(&protocolTranscriber{
		transcribe: func(context.Context, transcriptiontypes.Request) (transcriptiontypes.Result, error) {
			return transcriptiontypes.Result{Text: "dictated text"}, nil
		},
	})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, token := range []string{"", "runner-token"} {
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
		name, csrf string
		status     int
	}{
		{"missing", "", http.StatusForbidden},
		{"valid", csrfToken, http.StatusOK},
	} {
		t.Run("OIDC/"+test.name, func(t *testing.T) {
			request := transcriptionRequest(t, transcriptionPart{"file", "clip.wav", "audio/wav", []byte("RIFF1234WAVEdata")})
			request.Header.Del("Authorization")
			request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
			if test.csrf != "" {
				request.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: test.csrf})
				request.Header.Set(webCSRFHeaderName, test.csrf)
			}
			response := httptest.NewRecorder()
			s.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code, response.Body.String())
		})
	}
}
