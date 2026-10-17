package chat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientTranscriptionResponseErrors(t *testing.T) {
	for _, test := range []struct {
		name            string
		status          int
		body, wantError string
	}{
		{"provider error", http.StatusBadGateway, `{"error":"provider unavailable"}`, "provider unavailable"},
		{"malformed JSON", http.StatusOK, "{", "invalid transcription response"},
		{"blank text", http.StatusOK, `{"text":" \n"}`, "no speech was recognized"},
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
