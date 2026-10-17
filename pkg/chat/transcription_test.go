package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientTranscriptionMultipartProtocol(t *testing.T) {
	for _, language := range []string{"", "en-GB"} {
		t.Run("language="+language, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/prefix"+transcriptiontypes.Path, r.URL.Path)
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
				assert.Empty(t, r.URL.RawQuery, "dictation is independent of runner and conversation selection")
				if !assert.NoError(t, r.ParseMultipartForm(1024)) {
					http.Error(w, "invalid multipart request", http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				file, header, err := r.FormFile("file")
				if !assert.NoError(t, err) {
					http.Error(w, "missing file", http.StatusBadRequest)
					return
				}
				defer file.Close()
				audio, err := io.ReadAll(file)
				assert.NoError(t, err)
				assert.Equal(t, "RIFF1234WAVEdata", string(audio))
				assert.Equal(t, "my recording.wav", header.Filename)
				assert.Equal(t, "audio/wav", header.Header.Get("Content-Type"))
				assert.Len(t, r.MultipartForm.File, 1)
				if language == "" {
					assert.Empty(t, r.MultipartForm.Value)
				} else {
					assert.Equal(t, map[string][]string{"language": {language}}, r.MultipartForm.Value)
				}
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(transcriptiontypes.Result{Text: "  Dictated text. \n"}))
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL+"/prefix", "secret-token", "unused-runner")
			require.NoError(t, err)
			result, err := client.TranscribeAudio(t.Context(), []byte("RIFF1234WAVEdata"), "my recording.wav", "audio/wav", language)
			require.NoError(t, err)
			assert.Equal(t, "Dictated text.", result.Text)
		})
	}
}

func TestClientTranscriptionCapabilities(t *testing.T) {
	want := transcriptiontypes.Capabilities{
		Enabled: true, Available: false, Reason: "Sign in to enable transcription.",
		MaxAudioBytes:      transcriptiontypes.MaxAudioBytes,
		MaxDurationSeconds: transcriptiontypes.MaxDurationSeconds,
		MimeTypes:          []string{"audio/wav", "audio/webm"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/prefix"+transcriptiontypes.Path, r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		assert.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/prefix", "secret-token", "")
	require.NoError(t, err)
	got, err := client.TranscriptionCapabilities(t.Context())
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestClientTranscriptionRejectsInvalidInputs(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "token", "")
	require.NoError(t, err)
	for _, test := range []struct {
		name                  string
		audio                 []byte
		filename, contentType string
	}{
		{"empty", nil, "clip.wav", "audio/wav"},
		{"too large", make([]byte, transcriptiontypes.MaxAudioBytes+1), "clip.wav", "audio/wav"},
		{"missing filename", []byte("audio"), "", "audio/wav"},
		{"malformed MIME", []byte("audio"), "clip.wav", "audio/wav;codecs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.TranscribeAudio(t.Context(), test.audio, test.filename, test.contentType, "")
			assert.Error(t, err)
		})
	}
	assert.Zero(t, calls.Load(), "invalid recordings must not be uploaded")
	var uninitialized *Client
	_, err = uninitialized.TranscribeAudio(t.Context(), []byte("audio"), "clip.wav", "audio/wav", "")
	assert.ErrorContains(t, err, "not initialized")
}

func TestClientTranscriptionResponseErrors(t *testing.T) {
	for _, test := range []struct {
		name            string
		status          int
		body, wantError string
	}{
		{"provider error", http.StatusBadGateway, `{"error":"provider unavailable"}`, "provider unavailable"},
		{"unavailable", http.StatusServiceUnavailable, `{"error":"transcription disabled"}`, "transcription disabled"},
		{"provider timeout", http.StatusGatewayTimeout, `{"error":"transcription timed out"}`, "transcription timed out"},
		{"no speech", http.StatusUnprocessableEntity, `{"error":"no speech was recognized"}`, "no speech was recognized"},
		{"empty response", http.StatusOK, "", "invalid transcription response"},
		{"malformed JSON", http.StatusOK, "{", "invalid transcription response"},
		{"wrong type", http.StatusOK, `{"text":42}`, "invalid transcription response"},
		{"missing text", http.StatusOK, "{}", "no speech was recognized"},
		{"blank text", http.StatusOK, `{"text":" \n"}`, "no speech was recognized"},
		{"trailing JSON", http.StatusOK, `{"text":"hello"}{}`, "invalid transcription response"},
		{"oversized", http.StatusOK, strings.Repeat(" ", int(transcriptiontypes.MaxResponseBytes)+1), "size limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL, "token", "")
			require.NoError(t, err)
			_, err = client.TranscribeAudio(t.Context(), []byte("audio"), "clip.wav", "audio/wav", "")
			assert.ErrorContains(t, err, test.wantError)
			assert.EqualValues(t, 1, calls.Load(), "a potentially billed upload must not be retried")
			if test.status != http.StatusOK {
				var apiErr *ControlPlaneHTTPError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, test.status, apiErr.StatusCode)
			}
		})
	}
}

func TestClientTranscriptionCancellation(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Consume the request before observing disconnect so HTTP/1 detects
		// cancellation while the transcription provider would be running.
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(finished)
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	client, err := NewClient(server.URL, "token", "")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.TranscribeAudio(ctx, []byte("audio"), "clip.wav", "audio/wav", "")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not reach server")
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("client did not cancel transcription")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation did not reach server")
	}
}
